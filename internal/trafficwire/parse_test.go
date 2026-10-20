package trafficwire_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ta-events-ingest/internal/trafficwire"
)

func TestParsePinned(t *testing.T) {
	dir := filepath.Join("..", "runtimeloader", "testdata", "pinned")
	rel, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := trafficwire.ParseRelease(rel)
	if err != nil {
		t.Fatal(err)
	}
	if r.ArtifactType != trafficwire.ArtifactRelease {
		t.Fatalf("%q", r.ArtifactType)
	}
}

func TestRejectTrailing(t *testing.T) {
	if _, err := trafficwire.ParseRelease([]byte(`{"schemaVersion":1,"artifactType":"traffic-release/v1","compileTarget":"traffic-runtime/v1","layoutId":"x","program":{"sha256":"a","bytes":1},"weights":{"sha256":"b","bytes":1},"source":{"configurationRevision":"c","trafficPolicyRevision":"p","trafficSignalsRevision":"s","trafficBoundsRevision":"b","trafficFixRevision":"f","contextRegistryVersion":1,"contentRegistryVersion":1,"modelVersion":"m","sourceWindow":{"start":"t","end":"t","watermark":"t"}}}{}`)); err == nil {
		t.Fatal("expected trailing error")
	}
}

func TestRedirectTargetV1RejectsURLField(t *testing.T) {
	_, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTarget, 1, []byte(`{"enabled":true,"targetId":"x","url":"https://example.com/from-payload"}`))
	if err == nil {
		t.Fatal("url must not extend redirect-target-v1")
	}
}

func TestRedirectTargetV2URLIsExecutableValue(t *testing.T) {
	p, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTargetV2, 2, []byte(`{"enabled":true,"targetId":"offer-a","url":"https://example.com/from-payload"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.TargetID != "offer-a" || p.URL != "https://example.com/from-payload" {
		t.Fatalf("%+v", p)
	}
}

func TestRedirectTargetRejectsHTTPSURLRegistryField(t *testing.T) {
	_, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTargetV2, 2, []byte(`{"enabled":true,"targetId":"x","httpsURL":"https://example.com/"}`))
	if err == nil {
		t.Fatal("httpsURL is superseded registry field; v2 field is url")
	}
}

func TestRedirectTargetV1RequiresSchema1(t *testing.T) {
	_, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTarget, 2, []byte(`{"enabled":false,"targetId":"x"}`))
	if err == nil {
		t.Fatal("v1 handler with schemaVersion 2 must fail")
	}
}

func TestRedirectTargetV2RequiresSchema2(t *testing.T) {
	_, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTargetV2, 1, []byte(`{"enabled":true,"targetId":"x","url":"https://example.com/"}`))
	if err == nil {
		t.Fatal("v2 handler with schemaVersion 1 must fail")
	}
}

func TestReleaseRejectsRedirectTargetsRef(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"artifactType":"traffic-release/v1","compileTarget":"traffic-runtime/v1","layoutId":"x","program":{"sha256":"a","bytes":1},"weights":{"sha256":"b","bytes":1},"redirectTargets":{"sha256":"a","bytes":1},"source":{"configurationRevision":"c","trafficPolicyRevision":"p","trafficSignalsRevision":"s","trafficBoundsRevision":"b","trafficFixRevision":"f","contextRegistryVersion":1,"contentRegistryVersion":1,"modelVersion":"m","sourceWindow":{"start":"t","end":"t","watermark":"t"}}}`)
	if _, err := trafficwire.ParseRelease(raw); err == nil {
		t.Fatal("redirectTargets registry ref must be unknown")
	}
}

func TestReleaseHasNoRedirectTargetsField(t *testing.T) {
	rt := reflect.TypeOf(trafficwire.Release{})
	if _, ok := rt.FieldByName("RedirectTargets"); ok {
		t.Fatal("superseded target registry must not be a Release field")
	}
}

func TestRedirectTargetPinnedShape(t *testing.T) {
	p, err := trafficwire.ParseRedirect(trafficwire.HandlerRedirectTarget, 1, []byte(`{"enabled":false,"targetId":"disabled_target"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled || p.TargetID != "disabled_target" || p.URL != "" {
		t.Fatalf("%+v", p)
	}
}

func TestPinnedRedirectTargetOmitsURL(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "runtimeloader", "testdata", "pinned", "program.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prog struct {
		Variants []struct {
			ID      string          `json:"id"`
			TypeID  string          `json:"typeId"`
			Payload json.RawMessage `json:"payload"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(raw, &prog); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range prog.Variants {
		if v.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		found = true
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(v.Payload, &keys); err != nil {
			t.Fatal(err)
		}
		if _, ok := keys["url"]; ok {
			t.Fatalf("current accepted compiler emit must omit url; variant %s has it", v.ID)
		}
		if _, ok := keys["targetId"]; !ok {
			t.Fatalf("variant %s missing targetId", v.ID)
		}
		if _, ok := keys["enabled"]; !ok {
			t.Fatalf("variant %s missing enabled", v.ID)
		}
	}
	if !found {
		t.Fatal("no redirect-target variant in pinned program")
	}
}

func TestRejectDuplicateField(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"schemaVersion":2,"artifactType":"traffic-release/v1","compileTarget":"traffic-runtime/v1","layoutId":"x","program":{"sha256":"a","bytes":1},"weights":{"sha256":"b","bytes":1},"source":{"configurationRevision":"c","trafficPolicyRevision":"p","trafficSignalsRevision":"s","trafficBoundsRevision":"b","trafficFixRevision":"f","contextRegistryVersion":1,"contentRegistryVersion":1,"modelVersion":"m","sourceWindow":{"start":"t","end":"t","watermark":"t"}}}`)
	if _, err := trafficwire.ParseRelease(raw); err == nil {
		t.Fatal("expected duplicate field error")
	}
}
