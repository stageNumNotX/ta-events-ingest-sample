package runtimeloader_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

func samplePointerJSON(t *testing.T, wrap string, genAsString bool) []byte {
	t.Helper()
	pins := runtimeloader.DefaultPins()
	type art struct {
		Kind       string `json:"kind"`
		SHA256     string `json:"sha256"`
		SizeBytes  int    `json:"sizeBytes"`
		Generation any    `json:"generation"`
		URI        string `json:"uri"`
	}
	var g1, g2, g3 any = pins.Program.Generation, pins.Weights.Generation, pins.Release.Generation
	if genAsString {
		g1 = "1789081598223417"
		g2 = "1789081598353827"
		g3 = "1789081598443957"
	}
	arts := []art{
		{Kind: trafficwire.ArtifactProgram, SHA256: pins.Program.SHA256, SizeBytes: pins.Program.Size, Generation: g1, URI: "gs://" + pins.Program.Bucket + "/" + pins.Program.Object},
		{Kind: trafficwire.ArtifactWeights, SHA256: pins.Weights.SHA256, SizeBytes: pins.Weights.Size, Generation: g2, URI: "gs://" + pins.Weights.Bucket + "/" + pins.Weights.Object},
		{Kind: trafficwire.ArtifactRelease, SHA256: pins.Release.SHA256, SizeBytes: pins.Release.Size, Generation: g3, URI: "gs://" + pins.Release.Bucket + "/" + pins.Release.Object},
	}
	var payload any
	switch wrap {
	case "artifacts":
		payload = map[string]any{
			"targetId":      "prototype",
			"version":       1,
			"artifactSetId": runtimeloader.PinArtifactSet,
			"jobId":         runtimeloader.PinCompilerJob,
			"extraIgnored":  map[string]any{"foo": "bar"},
			"artifacts":     arts,
		}
	case "live":
		payload = map[string]any{
			"live": map[string]any{
				"targetId":      "prototype",
				"version":       1,
				"artifactSetId": runtimeloader.PinArtifactSet,
				"artifacts":     arts,
			},
		}
	case "named":
		payload = map[string]any{
			"targetId":      "prototype",
			"version":       1,
			"artifactSetId": runtimeloader.PinArtifactSet,
			"program": map[string]any{
				"sha256": pins.Program.SHA256, "sizeBytes": pins.Program.Size,
				"generation": pins.Program.Generation,
				"uri":        "gs://" + pins.Program.Bucket + "/" + pins.Program.Object,
			},
			"weights": map[string]any{
				"sha256": pins.Weights.SHA256, "size": pins.Weights.Size,
				"generation": pins.Weights.Generation,
				"object":     pins.Weights.Object,
			},
			"release": map[string]any{
				"sha256": pins.Release.SHA256, "bytes": pins.Release.Size,
				"generation": pins.Release.Generation,
			},
		}
	default:
		t.Fatalf("unknown wrap %s", wrap)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParsePointerArtifacts(t *testing.T) {
	p, err := runtimeloader.ParsePointer(samplePointerJSON(t, "artifacts", false))
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.ArtifactSetID != runtimeloader.PinArtifactSet || p.TargetID != "prototype" {
		t.Fatalf("meta=%+v", p)
	}
	if p.Pins != runtimeloader.DefaultPins() {
		t.Fatalf("pins=%+v", p.Pins)
	}
}

func TestParsePointerStringGenerationAndLiveWrap(t *testing.T) {
	p, err := runtimeloader.ParsePointer(samplePointerJSON(t, "live", true))
	if err != nil {
		t.Fatal(err)
	}
	if p.Pins != runtimeloader.DefaultPins() {
		t.Fatalf("pins=%+v", p.Pins)
	}
}

func TestParsePointerNamedObjects(t *testing.T) {
	p, err := runtimeloader.ParsePointer(samplePointerJSON(t, "named", false))
	if err != nil {
		t.Fatal(err)
	}
	want := runtimeloader.DefaultPins()
	if p.Pins.Program != want.Program || p.Pins.Weights != want.Weights || p.Pins.Release != want.Release {
		t.Fatalf("pins=%+v want=%+v", p.Pins, want)
	}
}

func TestParsePointerRejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"artifactSetId":"x","artifacts":[]}`,
		`{"version":1,"artifacts":[]}`,
		`{"version":0,"artifactSetId":"x"}`,
		`[]`,
		`""`,
	}
	for _, raw := range cases {
		if _, err := runtimeloader.ParsePointer([]byte(raw)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestParsePointerRejectsConflictingPins(t *testing.T) {
	raw := []byte(`{
		"version":1,
		"artifactSetId":"59e20642-0d5d-47e1-ac55-80730d8ccaa3",
		"program":{"sha256":"53bfd83f26c6e7d3bc434236b51d1d618bf10b0e498ceb622eda52e7c05ae8d3","sizeBytes":5589,"generation":1789081598223417},
		"weights":{"sha256":"8cc0537da05eed40ae702585373714bbaa1f6849a6cee9e8761aacde06fe3a7f","sizeBytes":977,"generation":1789081598353827},
		"release":{"sha256":"e6f73084ed2b112effccdf9461c7a2cfdb81bf97dd1349137d628c242427d70c","sizeBytes":850,"generation":1789081598443957},
		"artifacts":[{"kind":"traffic-program/v1","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sizeBytes":1,"generation":1}]
	}`)
	if _, err := runtimeloader.ParsePointer(raw); err == nil {
		t.Fatal("conflicting program pins must fail")
	}
}

func TestPointerFromEnvEmptyMatchesDefault(t *testing.T) {
	p, err := runtimeloader.PointerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p != runtimeloader.DefaultPointerPin() {
		t.Fatalf("got %+v", p)
	}
}

func TestPointerFromEnvRejectsMalformed(t *testing.T) {
	cases := []struct {
		key, val, want string
	}{
		{"TRAFFIC_POINTER_GENERATION", "abc", "GENERATION"},
		{"TRAFFIC_POINTER_GENERATION", "0", "must be > 0"},
		{"TRAFFIC_POINTER_SIZE", "nope", "SIZE"},
		{"TRAFFIC_POINTER_SIZE", "0", "SIZE"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.val, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			p, err := runtimeloader.PointerFromEnv()
			if err == nil {
				t.Fatalf("want error, got %+v", p)
			}
			if p != (runtimeloader.ObjectPin{}) {
				t.Fatal("error must not return compiled pointer pin")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestPointerRequested(t *testing.T) {
	if runtimeloader.PointerRequested() {
		t.Fatal("empty env must not request pointer (T3/T5 stay on C1 pins)")
	}
	t.Setenv("TRAFFIC_USE_POINTER", "1")
	if !runtimeloader.PointerRequested() {
		t.Fatal("TRAFFIC_USE_POINTER=1")
	}
}

func TestPointerPollRequested(t *testing.T) {
	if runtimeloader.PointerPollRequested() {
		t.Fatal("empty env must not poll (probes stay off)")
	}
	t.Setenv("TRAFFIC_USE_POINTER", "1")
	if !runtimeloader.PointerPollRequested() {
		t.Fatal("unset TRAFFIC_POINTER_POLL follows PointerRequested")
	}
	t.Setenv("TRAFFIC_POINTER_POLL", "0")
	if runtimeloader.PointerPollRequested() {
		t.Fatal("TRAFFIC_POINTER_POLL=0 must disable")
	}
	t.Setenv("TRAFFIC_POINTER_POLL", "1")
	if !runtimeloader.PointerPollRequested() {
		t.Fatal("TRAFFIC_POINTER_POLL=1")
	}
}

func TestPointerPollInterval(t *testing.T) {
	if runtimeloader.PointerPollInterval() != runtimeloader.DefaultPointerPollInterval {
		t.Fatal("default interval")
	}
	t.Setenv("TRAFFIC_POINTER_POLL_INTERVAL", "15s")
	if runtimeloader.PointerPollInterval() != 15*time.Second {
		t.Fatalf("got %s", runtimeloader.PointerPollInterval())
	}
	t.Setenv("TRAFFIC_POINTER_POLL_INTERVAL", "200ms")
	if runtimeloader.PointerPollInterval() != runtimeloader.DefaultPointerPollInterval {
		t.Fatal("sub-second interval must fall back")
	}
}

func TestPointerJSONKeys(t *testing.T) {
	keys := runtimeloader.PointerJSONKeys([]byte(`{"version":1,"artifacts":[]}`))
	if strings.Join(keys, ",") != "artifacts,version" {
		t.Fatalf("keys=%v", keys)
	}
}

func TestDefaultPointerMatchesEnvFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "traffic-runtime-pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(v, `"`)
	}
	p := runtimeloader.DefaultPointerPin()
	if p.Object != vals["POINTER_OBJECT"] ||
		strconv.FormatInt(p.Generation, 10) != vals["POINTER_GENERATION"] ||
		strconv.Itoa(p.Size) != vals["POINTER_SIZE"] ||
		p.SHA256 != vals["POINTER_SHA256"] {
		t.Fatalf("pointer pin != pins.env: %+v vals=%v", p, vals)
	}
}

func TestDefaultPointerMatchesConstants(t *testing.T) {
	p := runtimeloader.DefaultPointerPin()
	if p.Object != runtimeloader.PointerObject || p.Generation != runtimeloader.PointerGen || p.Size != runtimeloader.PointerSize || p.SHA256 != runtimeloader.PointerSHA256 {
		t.Fatalf("%+v", p)
	}
	if p.Bucket != runtimeloader.PinBucket {
		t.Fatal("pointer must stay on the pinned sample bucket")
	}
}
