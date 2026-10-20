// Command server is the HTTP ingest service for canonical traffic events.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ta-events-ingest/internal/config"
	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/pubsub"
)

const shutdownTimeout = 10 * time.Second

type stopPublisher interface {
	ingest.Publisher
	Stop()
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "error", err.Error())
		os.Exit(1)
	}

	metrics := &ingest.Metrics{}

	var pub stopPublisher
	if cfg.Publisher == config.PublisherLog {
		pub = pubsub.NewLogPublisher(logger)
		logger.Info("using log publisher (no Pub/Sub)")
	} else {
		p, err := pubsub.New(context.Background(), cfg.ProjectID, cfg.PubSubTopic, func(err error) {
			if err != nil {
				metrics.PublishErrors.Add(1)
				logger.Error("pubsub delivery failed", "error", err.Error())
			}
		})
		if err != nil {
			logger.Error("pubsub init failed", "error", err.Error())
			os.Exit(1)
		}
		pub = p
	}

	handler := ingest.NewHandler(ingest.Options{
		Secret:       cfg.IngestSecret,
		Salt:         cfg.IPHashSalt,
		MaxBatchSize: cfg.MaxBatchSize,
		Publisher:    pub,
		Logger:       logger,
		Metrics:      metrics,
	})

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
		logger.Info("server starting",
			"port", cfg.Port,
			"project", cfg.ProjectID,
			"topic", cfg.PubSubTopic,
			"publisher", cfg.Publisher,
			"max_batch_size", cfg.MaxBatchSize,
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
	}
	pub.Stop()

	logger.Info("stopped",
		"batches_accepted", metrics.BatchesAccepted.Load(),
		"events_accepted", metrics.EventsAccepted.Load(),
		"events_published", metrics.EventsPublished.Load(),
		"publish_errors", metrics.PublishErrors.Load(),
		"validation_errors", metrics.ValidationErrors.Load(),
		"bad_requests", metrics.BadRequests.Load(),
	)
}
