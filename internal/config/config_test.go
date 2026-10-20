package config

import "testing"

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("PROJECT_ID", "proj")
	t.Setenv("EVENTS_INGEST_SECRET", "secret")
	t.Setenv("IP_HASH_SALT", "salt")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PubSubTopic != defaultPubSubTopic {
		t.Errorf("PubSubTopic = %q, want %q", cfg.PubSubTopic, defaultPubSubTopic)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, defaultPort)
	}
	if cfg.MaxBatchSize != defaultMaxBatchSize {
		t.Errorf("MaxBatchSize = %d, want %d", cfg.MaxBatchSize, defaultMaxBatchSize)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("PROJECT_ID", "")
	t.Setenv("EVENTS_INGEST_SECRET", "")
	t.Setenv("IP_HASH_SALT", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing required env vars, got nil")
	}
}

func TestLoadLogPublisherSkipsProjectID(t *testing.T) {
	t.Setenv("PROJECT_ID", "")
	t.Setenv("PUBLISHER", "log")
	t.Setenv("EVENTS_INGEST_SECRET", "secret")
	t.Setenv("IP_HASH_SALT", "salt")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Publisher != PublisherLog {
		t.Fatalf("Publisher = %q", cfg.Publisher)
	}
}

func TestLoadInvalidPublisher(t *testing.T) {
	setRequired(t)
	t.Setenv("PUBLISHER", "kafka")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid PUBLISHER")
	}
}

func TestLoadInvalidMaxBatchSize(t *testing.T) {
	setRequired(t)
	t.Setenv("MAX_BATCH_SIZE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for MAX_BATCH_SIZE=0, got nil")
	}
	t.Setenv("MAX_BATCH_SIZE", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-numeric MAX_BATCH_SIZE, got nil")
	}
}
