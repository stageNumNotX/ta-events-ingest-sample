// Package config loads ingest service configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	PublisherPubSub = "pubsub"
	PublisherLog    = "log"
)

// Config is the ingest HTTP service configuration.
type Config struct {
	ProjectID    string
	PubSubTopic  string
	IngestSecret string
	IPHashSalt   string
	Port         string
	MaxBatchSize int
	Publisher    string
}

const (
	defaultPubSubTopic  = "ta-events"
	defaultPort         = "8080"
	defaultMaxBatchSize = 20
	defaultPublisher    = PublisherPubSub
)

// Load reads env and fail-fast validates required fields so a misconfigured
// process never accepts traffic.
func Load() (*Config, error) {
	cfg := &Config{
		ProjectID:    os.Getenv("PROJECT_ID"),
		PubSubTopic:  getEnvDefault("PUBSUB_TOPIC", defaultPubSubTopic),
		IngestSecret: os.Getenv("EVENTS_INGEST_SECRET"),
		IPHashSalt:   os.Getenv("IP_HASH_SALT"),
		Port:         getEnvDefault("PORT", defaultPort),
		MaxBatchSize: defaultMaxBatchSize,
		Publisher:    strings.ToLower(getEnvDefault("PUBLISHER", defaultPublisher)),
	}

	switch cfg.Publisher {
	case PublisherPubSub, PublisherLog:
	default:
		return nil, fmt.Errorf("invalid PUBLISHER %q: must be %s or %s", cfg.Publisher, PublisherPubSub, PublisherLog)
	}

	var missing []string
	if cfg.Publisher == PublisherPubSub && cfg.ProjectID == "" {
		missing = append(missing, "PROJECT_ID")
	}
	if cfg.IngestSecret == "" {
		missing = append(missing, "EVENTS_INGEST_SECRET")
	}
	if cfg.IPHashSalt == "" {
		missing = append(missing, "IP_HASH_SALT")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %v", missing)
	}

	if v := os.Getenv("MAX_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MAX_BATCH_SIZE %q: must be a positive integer", v)
		}
		cfg.MaxBatchSize = n
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
