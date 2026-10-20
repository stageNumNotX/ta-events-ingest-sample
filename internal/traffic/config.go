package traffic

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"ta-events-ingest/internal/producerhandoff"
	"ta-events-ingest/internal/scenarios/redirect"
)

const (
	defaultPort                 = "8080"
	defaultPrototypeRedirectURL = "https://example.com/"
	defaultPubSubTopic          = "ta-traffic-runtime-proto-events"
	prototypeReleaseName        = "prototype-fixture"
	artifactSourceDir           = "dir"
	artifactSourceGCS           = "gcs"
)

const (
	// ActivationRevisionPinned — bytes copied into the image.
	ActivationRevisionPinned = "revision-pinned"
	// ActivationGCSGeneration — bytes read from generation-pinned GCS pins.
	ActivationGCSGeneration = "gcs-generation"
	// ActivationGCSPointer — pins taken from the producer pointer object.
	ActivationGCSPointer = "gcs-pointer"
)

// Config — конфигурация traffic-runtime. Не смешивается с ingest config.
type Config struct {
	Port                 string
	ProjectID            string
	PubSubTopic          string
	PrototypeMode        bool
	PrototypeRedirectURL string
	PinnedDir            string
	ArtifactSource       string
	QueueSize            int
	QueueWorkers         int
	ContextMode          string
	EdgeToken            string
}

const (
	// ContextModePrototype keeps GET / on PrototypeContext (existing HTTP).
	ContextModePrototype = "prototype"
	// ContextModeQueryV1 reads allowlisted query keys into request context.
	ContextModeQueryV1 = "query-v1"
)

// Load читает env. PROTOTYPE_MODE=true — единственный разрешённый способ
// включить fixture; без него процесс слушает /health, но /ready ложен.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                 getEnvDefault("PORT", defaultPort),
		ProjectID:            os.Getenv("PROJECT_ID"),
		PubSubTopic:          getEnvDefault("PUBSUB_TOPIC", defaultPubSubTopic),
		PrototypeMode:        parseBool(os.Getenv("PROTOTYPE_MODE")),
		PrototypeRedirectURL: getEnvDefault("PROTOTYPE_REDIRECT_URL", defaultPrototypeRedirectURL),
		PinnedDir:            os.Getenv("PINNED_DIR"),
		ArtifactSource:       getEnvDefault("ARTIFACT_SOURCE", artifactSourceDir),
		QueueSize:            256,
		QueueWorkers:         2,
		ContextMode:          getEnvDefault("TRAFFIC_CONTEXT_MODE", ContextModePrototype),
		EdgeToken:            os.Getenv(envEdgeToken),
	}
	if err := producerhandoff.PrepareOfficialAlpha2(); err != nil {
		return nil, fmt.Errorf("producer ISO alpha-2 pin: %w", err)
	}
	if cfg.Port == "" {
		return nil, fmt.Errorf("PORT must not be empty")
	}
	switch cfg.ContextMode {
	case ContextModePrototype, ContextModeQueryV1:
	default:
		return nil, fmt.Errorf("TRAFFIC_CONTEXT_MODE must be prototype or query-v1")
	}
	switch cfg.ArtifactSource {
	case artifactSourceDir, artifactSourceGCS:
	default:
		return nil, fmt.Errorf("ARTIFACT_SOURCE must be dir or gcs")
	}
	if cfg.PrototypeMode {
		if err := redirect.ValidateTarget(cfg.PrototypeRedirectURL); err != nil {
			return nil, fmt.Errorf("PROTOTYPE_REDIRECT_URL: %w", err)
		}
	}
	return cfg, nil
}

// PrototypeRelease возвращает immutable fixture. Не называется
// traffic-runtime/v1 и не является production-контрактом.
func PrototypeRelease(target string) (*PreparedRelease, error) {
	if err := redirect.ValidateTarget(target); err != nil {
		return nil, err
	}
	return &PreparedRelease{
		Name:   prototypeReleaseName,
		Target: target,
	}, nil
}

func parseBool(v string) bool {
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false
	}
	return b
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
