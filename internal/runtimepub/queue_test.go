package runtimepub_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimepub"
)

func silent() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTryEnqueueDropsWhenFull(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	q := runtimepub.NewQueue(1, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		once.Do(func() { close(started) })
		<-block
		return nil
	}, silent())
	t.Cleanup(func() {
		close(block)
		q.Drain(context.Background())
	})

	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "ingress", EventID: "a"}) {
		t.Fatal("first enqueue should succeed")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not take the first event")
	}
	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "scenario_assigned", EventID: "b"}) {
		t.Fatal("second enqueue should fill the buffer")
	}
	if q.TryEnqueue(ingest.CanonicalEvent{EventName: "redirect", EventID: "c"}) {
		t.Fatal("third enqueue should drop when worker is busy and buffer is full")
	}
	if q.Metrics.Dropped.Load() < 1 {
		t.Fatalf("dropped=%d", q.Metrics.Dropped.Load())
	}
}

func TestQueueAcksOnSuccess(t *testing.T) {
	var n atomic.Int64
	q := runtimepub.NewQueue(8, 2, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		n.Add(1)
		return nil
	}, silent())
	for i := 0; i < 3; i++ {
		if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "ingress", EventID: "x"}) {
			t.Fatal("enqueue")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	q.Drain(ctx)
	if n.Load() != 3 || q.Metrics.Acked.Load() != 3 {
		t.Fatalf("acked=%d n=%d", q.Metrics.Acked.Load(), n.Load())
	}
}

func TestQueueCountsFailedPublish(t *testing.T) {
	q := runtimepub.NewQueue(4, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		return errors.New("boom")
	}, silent())
	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "ingress", EventID: "f"}) {
		t.Fatal("enqueue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q.Drain(ctx)
	if q.Metrics.Failed.Load() != 1 {
		t.Fatalf("failed=%d", q.Metrics.Failed.Load())
	}
}

func TestPublishRetryKeepsEventID(t *testing.T) {
	var mu sync.Mutex
	var ids []string
	var n atomic.Int64
	q := runtimepub.NewQueue(4, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		mu.Lock()
		ids = append(ids, ev.EventID)
		mu.Unlock()
		if n.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	}, silent())
	const id = "click-retry/redirect"
	if !q.TryEnqueue(ingest.CanonicalEvent{
		EventName: ingest.EventRedirect,
		EventID:   id,
		ClickID:   "click-retry",
	}) {
		t.Fatal("enqueue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	q.Drain(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 2 {
		t.Fatalf("attempts=%d ids=%v", len(ids), ids)
	}
	if ids[0] != id || ids[1] != id {
		t.Fatalf("retry reminted event_id: %v", ids)
	}
	if q.Metrics.Acked.Load() != 1 {
		t.Fatalf("acked=%d failed=%d dropped=%d queued=%d",
			q.Metrics.Acked.Load(), q.Metrics.Failed.Load(), q.Metrics.Dropped.Load(), q.Metrics.Queued.Load())
	}
	if q.Metrics.Failed.Load() != 0 || q.Metrics.Dropped.Load() != 0 {
		t.Fatalf("failed=%d dropped=%d", q.Metrics.Failed.Load(), q.Metrics.Dropped.Load())
	}
}

func TestDrainReturnsWhileWorkerBlocked(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	q := runtimepub.NewQueue(1, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		once.Do(func() { close(started) })
		<-block
		return nil
	}, silent())
	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "ingress", EventID: "hang"}) {
		t.Fatal("enqueue")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker idle")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	q.Drain(ctx)
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("Drain not bounded: %s", d)
	}
	if q.TryEnqueue(ingest.CanonicalEvent{EventName: "redirect", EventID: "after"}) {
		t.Fatal("enqueue after Drain must drop")
	}
	if q.Metrics.Dropped.Load() < 1 {
		t.Fatalf("dropped=%d", q.Metrics.Dropped.Load())
	}
	close(block)
}

func TestQueueHasFixedWorkerCount(t *testing.T) {
	var inflight atomic.Int64
	var max atomic.Int64
	release := make(chan struct{})
	q := runtimepub.NewQueue(16, 2, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		n := inflight.Add(1)
		for {
			old := max.Load()
			if n <= old || max.CompareAndSwap(old, n) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return nil
	}, silent())
	for i := 0; i < 8; i++ {
		if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "ingress", EventID: "w"}) {
			t.Fatal("enqueue")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && max.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := max.Load(); got != 2 {
		close(release)
		t.Fatalf("workers=%d want 2", got)
	}
	close(release)
	q.Drain(context.Background())
}
