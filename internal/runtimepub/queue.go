// Package runtimepub — bounded best-effort публикация событий runtime.
// Не использует ingest TopicPublisher: там горутина на каждое сообщение.
package runtimepub

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"ta-events-ingest/internal/ingest"
)

const (
	defaultQueueSize = 256
	defaultWorkers   = 2
	publishTimeout   = 5 * time.Second
	maxAttempts      = 3
	retryBackoff     = 50 * time.Millisecond
)

type Metrics struct {
	Queued  atomic.Int64
	Acked   atomic.Int64
	Failed  atomic.Int64
	Dropped atomic.Int64
}

type Enqueuer interface {
	TryEnqueue(ev ingest.CanonicalEvent) bool
}

type Publisher func(ctx context.Context, ev ingest.CanonicalEvent) error

type Queue struct {
	ch      chan ingest.CanonicalEvent
	publish Publisher
	log     *slog.Logger
	Metrics *Metrics
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
}

func NewQueue(size, workers int, publish Publisher, log *slog.Logger) *Queue {
	if size <= 0 {
		size = defaultQueueSize
	}
	if workers <= 0 {
		workers = defaultWorkers
	}
	if log == nil {
		log = slog.Default()
	}
	q := &Queue{
		ch:      make(chan ingest.CanonicalEvent, size),
		publish: publish,
		log:     log,
		Metrics: &Metrics{},
	}
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q
}

func (q *Queue) TryEnqueue(ev ingest.CanonicalEvent) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		q.Metrics.Dropped.Add(1)
		return false
	}
	select {
	case q.ch <- ev:
		q.Metrics.Queued.Add(1)
		return true
	default:
		q.Metrics.Dropped.Add(1)
		return false
	}
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for ev := range q.ch {
		var err error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
			err = q.publish(ctx, ev)
			cancel()
			if err == nil {
				q.Metrics.Acked.Add(1)
				break
			}
			if attempt < maxAttempts {
				time.Sleep(retryBackoff)
			}
		}
		if err != nil {
			q.Metrics.Failed.Add(1)
			q.log.Error("runtime event publish failed",
				"event_name", ev.EventName,
				"attempts", maxAttempts,
				"error", err.Error(),
			)
		}
	}
}

func (q *Queue) Drain(ctx context.Context) {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		q.log.Error("publisher drain timed out",
			"queued", q.Metrics.Queued.Load(),
			"acked", q.Metrics.Acked.Load(),
			"failed", q.Metrics.Failed.Load(),
			"dropped", q.Metrics.Dropped.Load(),
		)
		return
	}
	q.log.Info("publisher drained",
		"queued", q.Metrics.Queued.Load(),
		"acked", q.Metrics.Acked.Load(),
		"failed", q.Metrics.Failed.Load(),
		"dropped", q.Metrics.Dropped.Load(),
	)
}

type discard struct{}

func Discard() Enqueuer { return discard{} }

func (discard) TryEnqueue(ingest.CanonicalEvent) bool { return false }
