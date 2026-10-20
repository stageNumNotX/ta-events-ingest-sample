package runtimeloader_test

import (
	"reflect"
	"strings"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
)

func mustPinsFromEnv(t *testing.T) runtimeloader.GCSPins {
	t.Helper()
	p, err := runtimeloader.PinsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGCSPinsHasNoTargetRegistry(t *testing.T) {
	rt := reflect.TypeOf(runtimeloader.GCSPins{})
	if rt.NumField() != 3 {
		t.Fatalf("expected program/weights/release only, got %d fields", rt.NumField())
	}
	for _, name := range []string{"RedirectTargets", "Targets", "ContentRegistry"} {
		if _, ok := rt.FieldByName(name); ok {
			t.Fatalf("superseded target registry field %s must not exist", name)
		}
	}
}

func TestPinsFromEnvEmptyMatchesDefault(t *testing.T) {
	if mustPinsFromEnv(t) != runtimeloader.DefaultPins() {
		t.Fatal("empty TRAFFIC_PIN_* must keep compiled C1 pins")
	}
}

func TestPinsFromEnvSHAOnlyRewritesObject(t *testing.T) {
	const bad = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Setenv("TRAFFIC_PIN_PROGRAM_SHA256", bad)
	p := mustPinsFromEnv(t)
	want := runtimeloader.ArtifactObject("program", bad)
	if p.Program.Object != want {
		t.Fatalf("sha-only override must rewrite object, got %s", p.Program.Object)
	}
}

func TestPinsFromEnvMissingGeneration(t *testing.T) {
	t.Setenv("TRAFFIC_PIN_PROGRAM_GENERATION", "1")
	p := mustPinsFromEnv(t)
	if p.Program.Generation != 1 {
		t.Fatalf("generation=%d", p.Program.Generation)
	}
	if p.Program.SHA256 != runtimeloader.PinProgramSHA256 {
		t.Fatal("missing-generation probe must keep C1 object sha")
	}
}

func TestPinsFromEnvCorruptKeepsObjectPath(t *testing.T) {
	const bad = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Setenv("TRAFFIC_PIN_PROGRAM_SHA256", bad)
	t.Setenv("TRAFFIC_PIN_PROGRAM_OBJECT", runtimeloader.ArtifactObject("program", runtimeloader.PinProgramSHA256))
	p := mustPinsFromEnv(t)
	if p.Program.SHA256 != bad {
		t.Fatalf("sha=%s", p.Program.SHA256)
	}
	if p.Program.Object != runtimeloader.ArtifactObject("program", runtimeloader.PinProgramSHA256) {
		t.Fatalf("object path rewritten: %s", p.Program.Object)
	}
	if p.Program.Generation != runtimeloader.PinProgramGen {
		t.Fatal("generation must stay C1")
	}
}

func TestPinsFromEnvRejectsMalformedGenerationAndSize(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"gen_not_int", "TRAFFIC_PIN_PROGRAM_GENERATION", "abc", "GENERATION"},
		{"gen_float", "TRAFFIC_PIN_WEIGHTS_GENERATION", "1.5", "GENERATION"},
		{"gen_zero", "TRAFFIC_PIN_RELEASE_GENERATION", "0", "must be > 0"},
		{"gen_negative", "TRAFFIC_PIN_PROGRAM_GENERATION", "-1", "must be > 0"},
		{"size_not_int", "TRAFFIC_PIN_PROGRAM_SIZE", "nope", "SIZE"},
		{"size_zero", "TRAFFIC_PIN_WEIGHTS_SIZE", "0", "SIZE"},
		{"size_negative", "TRAFFIC_PIN_RELEASE_SIZE", "-8", "SIZE"},
		{"size_too_big", "TRAFFIC_PIN_PROGRAM_SIZE", "999999999", "SIZE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			p, err := runtimeloader.PinsFromEnv()
			if err == nil {
				t.Fatalf("want error, got pins generation=%d size=%d", p.Program.Generation, p.Program.Size)
			}
			if p != (runtimeloader.GCSPins{}) {
				t.Fatal("error must not return compiled C1 pins")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}
