package traffic

import "testing"

func TestLoadPrototypeMode(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("PROTOTYPE_MODE", "true")
	t.Setenv("PROTOTYPE_REDIRECT_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PrototypeMode {
		t.Fatal("expected PrototypeMode")
	}
	if cfg.PrototypeRedirectURL != defaultPrototypeRedirectURL {
		t.Errorf("default target = %q", cfg.PrototypeRedirectURL)
	}
}

func TestLoadRejectsHTTPFixture(t *testing.T) {
	t.Setenv("PROTOTYPE_MODE", "true")
	t.Setenv("PROTOTYPE_REDIRECT_URL", "http://example.com/")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for http fixture")
	}
}

func TestLoadWithoutPrototypeMode(t *testing.T) {
	t.Setenv("PROTOTYPE_MODE", "")
	t.Setenv("PROTOTYPE_REDIRECT_URL", "https://example.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrototypeMode {
		t.Fatal("empty PROTOTYPE_MODE must be false")
	}
}

func TestLoadRejectsUnknownArtifactSource(t *testing.T) {
	t.Setenv("ARTIFACT_SOURCE", "r2")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for ARTIFACT_SOURCE=r2")
	}
}

func TestLoadDefaultContextModePrototype(t *testing.T) {
	t.Setenv("TRAFFIC_CONTEXT_MODE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextMode != ContextModePrototype {
		t.Fatalf("mode=%q", cfg.ContextMode)
	}
}

func TestLoadContextModeQueryV1(t *testing.T) {
	t.Setenv("TRAFFIC_CONTEXT_MODE", ContextModeQueryV1)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextMode != ContextModeQueryV1 {
		t.Fatalf("mode=%q", cfg.ContextMode)
	}
}

func TestLoadRejectsUnknownContextMode(t *testing.T) {
	t.Setenv("TRAFFIC_CONTEXT_MODE", "headers-v1")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for unknown TRAFFIC_CONTEXT_MODE")
	}
}

func TestLoadEdgeTokenIsNotAContextMode(t *testing.T) {
	t.Setenv("TRAFFIC_CONTEXT_MODE", ContextModeQueryV1)
	t.Setenv(envEdgeToken, "unit-test-token")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextMode != ContextModeQueryV1 {
		t.Fatalf("mode=%q", cfg.ContextMode)
	}
	if cfg.EdgeToken != "unit-test-token" {
		t.Fatal("edge token must load as a secret, not a mode")
	}
}
