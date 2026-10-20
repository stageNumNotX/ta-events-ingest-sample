package pubsub

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"ta-events-ingest/internal/ingest"
)

func TestLogPublisherDoesNotEchoPII(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	p := NewLogPublisher(log)
	err := p.Publish(ingest.CanonicalEvent{
		EventID:   "evt-1",
		EventName: "ingress",
		ClickID:   "click-1",
		URL:       "https://example.com/secret-path",
		UserAgent: "Mozilla/5.0",
		IPHash:    "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Stop()
	out := buf.String()
	if !strings.Contains(out, "evt-1") || !strings.Contains(out, "ingress") {
		t.Fatalf("expected identifiers in log, got %s", out)
	}
	if strings.Contains(out, "secret-path") || strings.Contains(out, "Mozilla") || strings.Contains(out, "abc123") {
		t.Fatalf("PII leaked into local publisher log: %s", out)
	}
}
