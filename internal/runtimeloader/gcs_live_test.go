package runtimeloader_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

func TestLiveGCSPins(t *testing.T) {
	if os.Getenv("TRAFFIC_RUNTIME_LIVE_GCS") != "1" {
		t.Skip("set TRAFFIC_RUNTIME_LIVE_GCS=1 to read pinned generations via ADC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := runtimeloader.NewGCSReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pins, err := runtimeloader.PinsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	b, err := runtimeloader.LoadPins(ctx, r, pins)
	if err != nil {
		t.Fatal(err)
	}
	if b.Release.CompileTarget != trafficwire.CompileTarget {
		t.Fatalf("compileTarget %q", b.Release.CompileTarget)
	}
	if b.Release.Program.SHA256 != runtimeloader.PinProgramSHA256 {
		t.Fatalf("program sha %s", b.Release.Program.SHA256)
	}
	if b.Release.Weights.SHA256 != runtimeloader.PinWeightsSHA256 {
		t.Fatalf("weights sha %s", b.Release.Weights.SHA256)
	}
	if _, err := decision.Prepare(b); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range b.Program.Variants {
		if v.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		pl, err := trafficwire.ParseRedirect(v.HandlerVersion, v.SchemaVersion, v.Payload)
		if err != nil {
			t.Fatalf("variant %s: %v", v.ID, err)
		}
		if strings.Contains(pl.URL, runtimeloader.ContractSmokeURLMarker) {
			found = true
			if !pl.Enabled {
				t.Fatalf("contract-smoke URL on disabled variant %s", v.ID)
			}
		}
	}
	if !found {
		t.Fatal("C1 program has no redirect-target payload.url with campaign=contract-smoke")
	}
}

func TestLiveGCSPointer(t *testing.T) {
	if os.Getenv("TRAFFIC_RUNTIME_LIVE_GCS") != "1" {
		t.Skip("set TRAFFIC_RUNTIME_LIVE_GCS=1 to read the producer pointer via ADC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := runtimeloader.NewGCSReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pin := runtimeloader.DefaultPointerPin()
	body, err := r.ReadObject(ctx, pin)
	compiledOK := err == nil
	if !compiledOK {
		// Battle overwrites retire the compiled generation. Observe
		// latest only — do not rewrite DefaultPointerPin (no self-oracle).
		latest, lerr := r.ReadLatest(ctx, pin.Bucket, pin.Object)
		if lerr != nil {
			t.Fatalf("compiled pointer #%d gone and latest unread: pin=%v latest=%v", pin.Generation, err, lerr)
		}
		t.Logf("compiled pointer generation %d unreadable; observing latest generation %d", pin.Generation, latest.Generation)
		body = latest.Data
	}
	p, err := runtimeloader.ParsePointer(body)
	if err != nil {
		t.Fatalf("parse pointer keys=%v: %v", runtimeloader.PointerJSONKeys(body), err)
	}
	if p.Version < 1 {
		t.Fatalf("version=%d", p.Version)
	}
	if strings.TrimSpace(p.ArtifactSetID) == "" {
		t.Fatal("artifactSetId is required")
	}
	if compiledOK {
		if p.ArtifactSetID != runtimeloader.PinArtifactSet {
			t.Fatalf("artifactSetId=%s want %s", p.ArtifactSetID, runtimeloader.PinArtifactSet)
		}
		if p.Pins != runtimeloader.DefaultPins() {
			t.Fatalf("pointer pins are not C1 generations: program=%d weights=%d release=%d",
				p.Pins.Program.Generation, p.Pins.Weights.Generation, p.Pins.Release.Generation)
		}
	}
	b, err := runtimeloader.LoadPins(ctx, r, p.Pins)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decision.Prepare(b); err != nil {
		t.Fatal(err)
	}
}
