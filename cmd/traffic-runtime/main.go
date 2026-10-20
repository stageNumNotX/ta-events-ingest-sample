package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/runtimepub"
	"ta-events-ingest/internal/traffic"
)

const (
	drainTimeout    = 4 * time.Second
	httpStopTimeout = 4 * time.Second
)

// revision задаётся ldflags при сборке образа; локально остаётся unknown.
var revision = "unknown"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := traffic.Load()
	if err != nil {
		logger.Error("config load failed", "error", err.Error())
		os.Exit(1)
	}

	handler := traffic.NewHandler(nil, logger)
	handler.SetContextMode(cfg.ContextMode)
	handler.SetEdgeToken(cfg.EdgeToken)
	if cfg.EdgeToken != "" {
		logger.Info("event country requires verified edge token")
	} else {
		logger.Info("event country disabled; edge token empty")
	}
	httpFromPayload := false

	ctx, cancelLoad := context.WithTimeout(context.Background(), 15*time.Second)
	contract, gcs := loadContract(ctx, cfg, logger)
	cancelLoad()
	if gcs != nil {
		defer gcs.Close()
	}
	if contract != nil {
		handler.StoreContract(contract)
		if rel, err := contract.HTTPRelease(); err != nil {
			logger.Error("no executable payload.url in pinned bundle", "error", err.Error())
			if !cfg.PrototypeMode {
				os.Exit(1)
			}
		} else {
			handler.StoreRelease(rel)
			httpFromPayload = true
			logger.Info("HTTP redirect load-path evidence",
				"release", rel.Name,
				"note", "GET / uses Resolve, not this scanned Target",
			)
		}
	}

	pollCtx, stopPoll := context.WithCancel(context.Background())
	var pollWG sync.WaitGroup
	if gcs != nil && runtimeloader.PointerPollRequested() {
		pin, err := runtimeloader.PointerFromEnv()
		if err != nil {
			logger.Error("pointer poll not started; TRAFFIC_POINTER_* invalid", "error", err.Error())
		} else {
			poller := traffic.NewPointerPoller(traffic.PointerPollerConfig{
				Handler:  handler,
				Latest:   gcs,
				Pins:     gcs,
				Bucket:   pin.Bucket,
				Object:   pin.Object,
				Interval: runtimeloader.PointerPollInterval(),
				Log:      logger,
			})
			pollWG.Add(1)
			go func() {
				defer pollWG.Done()
				poller.Run(pollCtx)
			}()
			logger.Info("pointer poll started",
				"object", pin.Object,
				"interval", runtimeloader.PointerPollInterval().String(),
				"note", "out of request path; validate then CAS; not T4 PASS until live A→B→A",
			)
		}
	}

	if !httpFromPayload {
		if !cfg.PrototypeMode {
			logger.Info("no HTTP fixture; /ready will stay false until a redirect-capable release is configured")
		} else {
			rel, err := traffic.PrototypeRelease(cfg.PrototypeRedirectURL)
			if err != nil {
				logger.Error("prototype fixture rejected", "error", err.Error())
				os.Exit(1)
			}
			handler.StoreRelease(rel)
			logger.Info("prototype fixture loaded",
				"release", rel.Name,
				"note", "not traffic-runtime/v1",
			)
		}
	}

	var queue *runtimepub.Queue
	var topic *runtimepub.Topic
	if cfg.ProjectID != "" && cfg.PubSubTopic != "" {
		pctx, cancelPub := context.WithTimeout(context.Background(), 8*time.Second)
		topic, err = runtimepub.NewTopic(pctx, cfg.ProjectID, cfg.PubSubTopic)
		cancelPub()
		if err != nil {
			logger.Error("pubsub client not started; redirects continue without telemetry",
				"error", err.Error(),
			)
		} else {
			queue = runtimepub.NewQueue(cfg.QueueSize, cfg.QueueWorkers, topic.Publish, logger)
			handler.SetEnqueue(queue)
			logger.Info("bounded event queue started",
				"topic", cfg.PubSubTopic,
				"queue_size", cfg.QueueSize,
				"workers", cfg.QueueWorkers,
			)
		}
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("traffic-runtime starting",
			"port", cfg.Port,
			"prototype_mode", cfg.PrototypeMode,
			"context_mode", cfg.ContextMode,
			"project", cfg.ProjectID,
			"revision", revision,
			"artifact_source", cfg.ArtifactSource,
			"pinned_dir", cfg.PinnedDir,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		logger.Error("server error", "error", err.Error())
	case sig := <-stop:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	stopPoll()
	pollDone := make(chan struct{})
	go func() {
		pollWG.Wait()
		close(pollDone)
	}()
	select {
	case <-pollDone:
	case <-time.After(2 * time.Second):
		logger.Error("pointer poll stop timed out")
	}

	// Cloud Run gives ~10s after SIGTERM. Drain telemetry first so it
	// is not skipped if HTTP shutdown consumes the whole window.
	if queue != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), drainTimeout)
		queue.Drain(drainCtx)
		drainCancel()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpStopTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
	}
	if topic != nil {
		topic.Stop()
	}
	logger.Info("stopped")
}

func loadContract(ctx context.Context, cfg *traffic.Config, logger *slog.Logger) (*traffic.Contract, *runtimeloader.GCSReader) {
	in := traffic.StartupInput{
		PinnedDir:      cfg.PinnedDir,
		ArtifactSource: cfg.ArtifactSource,
	}
	var gcs *runtimeloader.GCSReader
	if cfg.ArtifactSource == "gcs" {
		r, err := runtimeloader.NewGCSReader(ctx)
		if err != nil {
			logger.Error("gcs reader not created; keeping dir LKG", "error", err.Error())
		} else {
			gcs = r
			in.GCS = r
		}
		if runtimeloader.PointerRequested() {
			in.UsePointer = true
			pin, err := runtimeloader.PointerFromEnv()
			if err != nil {
				in.PointerErr = err
				logger.Error("TRAFFIC_POINTER_* invalid; not overlaying GCS", "error", err.Error())
			} else {
				in.Pointer = pin
			}
		} else {
			pins, err := runtimeloader.PinsFromEnv()
			if err != nil {
				in.PinErr = err
				logger.Error("TRAFFIC_PIN_* invalid; not overlaying GCS", "error", err.Error())
			} else {
				in.Pins = pins
			}
		}
	}
	st, err := traffic.LoadStartup(ctx, in)
	if err != nil {
		if gcs != nil {
			_ = gcs.Close()
		}
		logger.Error("startup load failed", "error", err.Error())
		os.Exit(1)
	}
	for _, e := range st.Errs {
		logger.Error("startup overlay failed; keeping LKG", "error", e.Error(), "kept_lkg", st.KeptLKG)
	}
	if st.Contract == nil {
		logger.Info("no traffic-runtime/v1 contract loaded; GET /contract will be 503")
		return nil, gcs
	}
	logger.Info("contract loaded",
		"activation", st.Contract.Activation(),
		"layout_id", st.Contract.LayoutID(),
		"pointer_version", st.Contract.PointerVersion(),
		"pointer_generation", st.Contract.PointerGeneration(),
		"artifact_set_id", st.Contract.ArtifactSetID(),
		"kept_lkg", st.KeptLKG,
	)
	return st.Contract, gcs
}
