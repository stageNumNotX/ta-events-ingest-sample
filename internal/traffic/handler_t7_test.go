package traffic

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimepub"
)

func TestRootRedirectWhenQueueFullAndPublishBlocked(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	q := runtimepub.NewQueue(1, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		once.Do(func() { close(started) })
		<-block
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		close(block)
		q.Drain(context.Background())
	})
	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "warmup", EventID: "w"}) {
		t.Fatal("warmup")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker idle")
	}
	if !q.TryEnqueue(ingest.CanonicalEvent{EventName: "fill", EventID: "f"}) {
		t.Fatal("buffer")
	}

	c, err := NewContract(twoExecutableBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "t7-full", nil }
	h.StoreContract(c)
	h.SetEnqueue(q)

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("redirect waited on full queue: %s", d)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if q.Metrics.Dropped.Load() < 1 {
		t.Fatalf("expected explicit drops, dropped=%d", q.Metrics.Dropped.Load())
	}
}

func TestRootRedirectWhenPublishAlwaysFails(t *testing.T) {
	q := runtimepub.NewQueue(8, 1, func(ctx context.Context, ev ingest.CanonicalEvent) error {
		return context.Canceled
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { q.Drain(context.Background()) })

	c, err := NewContract(twoExecutableBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "t7-fail", nil }
	h.StoreContract(c)
	h.SetEnqueue(q)

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("redirect waited on publish error: %s", d)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
}
