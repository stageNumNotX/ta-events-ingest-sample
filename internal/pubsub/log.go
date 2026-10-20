package pubsub

import (
	"log/slog"

	"ta-events-ingest/internal/ingest"
)

// LogPublisher implements ingest.Publisher by writing accepted events to logs.
// Used for local development (PUBLISHER=log) so the HTTP path works without GCP.
type LogPublisher struct {
	log *slog.Logger
}

func NewLogPublisher(log *slog.Logger) *LogPublisher {
	if log == nil {
		log = slog.Default()
	}
	return &LogPublisher{log: log}
}

func (p *LogPublisher) Publish(ev ingest.CanonicalEvent) error {
	p.log.Info("ingest event",
		"event_id", ev.EventID,
		"event_name", ev.EventName,
		"click_id", ev.ClickID,
	)
	return nil
}

func (p *LogPublisher) Stop() {}
