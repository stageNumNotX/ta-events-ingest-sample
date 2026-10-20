package traffic

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

func TestHTTPReleaseFromOldPinUnavailable(t *testing.T) {
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HTTPReleaseFromBundle(b); err == nil {
		t.Fatal("bootstrap v1 disabled_target must not yield payload.url")
	}
}

func TestHTTPReleasePrefersSmartlinkMain(t *testing.T) {
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.com/?campaign=contract-smoke"
	found := false
	for i, v := range b.Program.Variants {
		if v.ID != "disabled_target" {
			continue
		}
		b.Program.Variants[i].ID = preferredVariantID
		b.Program.Variants[i].HandlerVersion = trafficwire.HandlerRedirectTargetV2
		b.Program.Variants[i].SchemaVersion = 2
		b.Program.Variants[i].Payload = json.RawMessage(`{"enabled":true,"targetId":"smartlink-main","url":"` + url + `"}`)
		found = true
	}
	if !found {
		t.Fatal("disabled_target missing")
	}
	rel, err := HTTPReleaseFromBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Target != url {
		t.Fatalf("target %q", rel.Target)
	}
	if !strings.Contains(rel.Name, preferredVariantID) {
		t.Fatalf("name %q", rel.Name)
	}
}
