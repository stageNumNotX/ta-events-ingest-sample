package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"ta-events-ingest/internal/runtimeloader"
)

const defaultPointerPollMaxBackoff = 60 * time.Second

// PointerPoller читает latest producer pointer вне request path,
// полностью готовит bundle и меняет contract только atomic swap при
// строго большей version. Immutable env pin / startup generation не
// заменяют этот цикл.
type PointerPoller struct {
	log        *slog.Logger
	handler    *Handler
	latest     runtimeloader.LatestReader
	pins       runtimeloader.ObjectReader
	loadPins   func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)
	bucket     string
	object     string
	interval   time.Duration
	jitter     time.Duration
	maxBackoff time.Duration
	flights    sync.Map
}

type PointerPollerConfig struct {
	Handler    *Handler
	Latest     runtimeloader.LatestReader
	Pins       runtimeloader.ObjectReader
	Bucket     string
	Object     string
	Interval   time.Duration
	Jitter     time.Duration
	MaxBackoff time.Duration
	LoadPins   func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)
	Log        *slog.Logger
}

type pollFlight struct {
	done     chan struct{}
	contract *Contract
	err      error
}

func NewPointerPoller(cfg PointerPollerConfig) *PointerPoller {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = runtimeloader.DefaultPointerPollInterval
	}
	jitter := cfg.Jitter
	if jitter <= 0 {
		jitter = interval / 5
		if jitter < time.Millisecond {
			jitter = time.Millisecond
		}
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultPointerPollMaxBackoff
	}
	loadPins := cfg.LoadPins
	if loadPins == nil {
		loadPins = runtimeloader.LoadPins
	}
	return &PointerPoller{
		log:        log,
		handler:    cfg.Handler,
		latest:     cfg.Latest,
		pins:       cfg.Pins,
		loadPins:   loadPins,
		bucket:     cfg.Bucket,
		object:     cfg.Object,
		interval:   interval,
		jitter:     jitter,
		maxBackoff: maxBackoff,
	}
}

func (p *PointerPoller) Run(ctx context.Context) {
	if p == nil {
		return
	}
	backoff := p.interval
	// Poll immediately. A retired startup generation must not leave
	// revision-pinned LKG on the request path for a full interval.
	_ = p.pollAndBackoff(ctx, &backoff)
	if ctx.Err() != nil {
		return
	}
	timer := time.NewTimer(p.nextWait(backoff))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_ = p.pollAndBackoff(ctx, &backoff)
			if ctx.Err() != nil {
				return
			}
			timer.Reset(p.nextWait(backoff))
		}
	}
}

func (p *PointerPoller) pollAndBackoff(ctx context.Context, backoff *time.Duration) error {
	err := p.PollOnce(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		p.log.Error("pointer poll failed; keeping LKG", "error", err.Error())
		if *backoff < p.maxBackoff {
			*backoff *= 2
			if *backoff > p.maxBackoff {
				*backoff = p.maxBackoff
			}
		}
		return err
	}
	*backoff = p.interval
	return nil
}

func (p *PointerPoller) nextWait(base time.Duration) time.Duration {
	if base <= 0 {
		base = p.interval
	}
	if p.jitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(p.jitter)+1))
}

func (p *PointerPoller) PollOnce(ctx context.Context) error {
	if p == nil || p.handler == nil {
		return fmt.Errorf("pointer poll: nil poller")
	}
	if p.latest == nil {
		return fmt.Errorf("pointer poll: nil latest reader")
	}
	if p.bucket == "" || p.object == "" {
		return fmt.Errorf("pointer poll: bucket and object are required")
	}
	obj, err := p.latest.ReadLatest(ctx, p.bucket, p.object)
	if err != nil {
		return err
	}
	cur := p.handler.CurrentContract()
	if cur != nil && obj.Generation > 0 && obj.Generation == cur.pointerGeneration {
		return nil
	}
	ptr, err := runtimeloader.ParsePointer(obj.Data)
	if err != nil {
		return fmt.Errorf("pointer poll parse: %w", err)
	}
	if cur != nil && ptr.Version <= cur.pointerVersion {
		return nil
	}
	c, err := p.loadVersion(ctx, ptr.Version, func() (*Contract, error) {
		return p.prepare(ctx, ptr, obj.Generation)
	})
	if err != nil {
		return err
	}
	if p.handler.SwapIfNewer(c) {
		p.log.Info("pointer poll swapped",
			"pointer_version", c.pointerVersion,
			"pointer_generation", c.pointerGeneration,
			"artifact_set_id", c.artifactSetID,
			"activation", c.activation,
		)
	}
	return nil
}

func (p *PointerPoller) prepare(ctx context.Context, ptr *runtimeloader.Pointer, generation int64) (*Contract, error) {
	pins, err := runtimeloader.PinsFromPointer(ptr)
	if err != nil {
		return nil, fmt.Errorf("pointer poll pins: %w", err)
	}
	b, err := p.loadPins(ctx, p.pins, pins)
	if err != nil {
		return nil, fmt.Errorf("pointer poll load: %w", err)
	}
	c, err := NewContract(b, ActivationGCSPointer)
	if err != nil {
		return nil, fmt.Errorf("pointer poll prepare: %w", err)
	}
	if _, err := c.HTTPRelease(); err != nil {
		return nil, fmt.Errorf("pointer poll executable url: %w", err)
	}
	c.attachPointer(ptr.Version, generation, ptr.ArtifactSetID)
	return c, nil
}

func (p *PointerPoller) loadVersion(ctx context.Context, version int64, prepare func() (*Contract, error)) (*Contract, error) {
	f := &pollFlight{done: make(chan struct{})}
	actual, loaded := p.flights.LoadOrStore(version, f)
	if loaded {
		existing := actual.(*pollFlight)
		select {
		case <-existing.done:
			return existing.contract, existing.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c, err := prepare()
	f.contract = c
	f.err = err
	close(f.done)
	p.flights.Delete(version)
	return c, err
}
