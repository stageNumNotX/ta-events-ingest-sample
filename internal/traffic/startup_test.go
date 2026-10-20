package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

type errReader struct{ err error }

func (e errReader) ReadObject(context.Context, runtimeloader.ObjectPin) ([]byte, error) {
	return nil, e.err
}

func TestColdStartWithoutBundleNotReady(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Contract != nil {
		t.Fatal("cold start must not invent a contract")
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET / without LKG=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health must stay 200, got %d", rec.Code)
	}
}

func TestBrokenPinnedDirIsFatal(t *testing.T) {
	_, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("empty PINNED_DIR must be fatal; there is no LKG")
	}
}

func TestMissingGCSKeepsDirLKGRedirect(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      filepath.Join("..", "runtimeloader", "testdata", "pinned"),
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{err: errors.New("object missing")},
		Pins:           runtimeloader.DefaultPins(),
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil || len(st.Errs) == 0 {
		t.Fatalf("want LKG kept with overlay error, got %+v", st)
	}
	if st.Contract.activation != ActivationRevisionPinned {
		t.Fatalf("activation=%s", st.Contract.activation)
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestCorruptGCSKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			return nil, errors.New("sha256 mismatch")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil {
		t.Fatal("corrupt GCS must keep LKG")
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestMalformedPinEnvKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		PinErr:         errors.New("TRAFFIC_PIN_PROGRAM_GENERATION \"abc\""),
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			t.Fatal("LoadPins must not run when pin env is invalid")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil {
		t.Fatal("invalid TRAFFIC_PIN_* must keep LKG, not swap compiled C1")
	}
	if st.Contract.activation != ActivationRevisionPinned {
		t.Fatalf("activation=%s", st.Contract.activation)
	}
	if !strings.Contains(fmt.Sprint(st.Errs), "TRAFFIC_PIN_PROGRAM_GENERATION") {
		t.Fatalf("errors=%v", st.Errs)
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestNilGCSReaderKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil {
		t.Fatal("GCS reader outage must keep LKG")
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestIncompatibleExecutorKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			return incompatibleEnabledV1(t), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil {
		t.Fatal("incompatible executor must keep LKG, not swap")
	}
	if !strings.Contains(fmt.Sprint(st.Errs), "incompatible") {
		t.Fatalf("errors=%v", st.Errs)
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestUnsupportedMatcherFieldKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			return unsupportedFieldBundle(t), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil {
		t.Fatal("unsupported field must keep LKG, not swap")
	}
	if !strings.Contains(fmt.Sprint(st.Errs), "unsupported field") {
		t.Fatalf("errors=%v", st.Errs)
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestPointerOverlayReplacesLKGWhenValid(t *testing.T) {
	next := twoExecutableBundle(t)
	var gotPins runtimeloader.GCSPins
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		UsePointer:     true,
		Pointer:        runtimeloader.DefaultPointerPin(),
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return loadPinnedBundle(t), nil
		},
		LoadPointer: func(context.Context, runtimeloader.ObjectReader, runtimeloader.ObjectPin) (*runtimeloader.Pointer, error) {
			return &runtimeloader.Pointer{
				Version:       1,
				ArtifactSetID: runtimeloader.PinArtifactSet,
				Pins:          runtimeloader.DefaultPins(),
			}, nil
		},
		LoadPins: func(_ context.Context, _ runtimeloader.ObjectReader, pins runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			gotPins = pins
			return next, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.KeptLKG || st.Contract == nil || st.Contract.activation != ActivationGCSPointer {
		t.Fatalf("valid pointer must swap: kept=%v act=%s", st.KeptLKG, st.Contract.activation)
	}
	if gotPins != runtimeloader.DefaultPins() {
		t.Fatalf("LoadPins must receive pointer pins, got %+v", gotPins)
	}
	if st.Contract.PointerVersion() != 1 || st.Contract.ArtifactSetID() != runtimeloader.PinArtifactSet {
		t.Fatalf("startup pointer meta version=%d set=%s", st.Contract.PointerVersion(), st.Contract.ArtifactSetID())
	}
	if st.Contract.PointerGeneration() != runtimeloader.DefaultPointerPin().Generation {
		t.Fatalf("startup must record pinned pointer generation, got %d", st.Contract.PointerGeneration())
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestPointerReadFailKeepsLKGNotC1Pins(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		UsePointer:     true,
		Pins:           runtimeloader.DefaultPins(),
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPointer: func(context.Context, runtimeloader.ObjectReader, runtimeloader.ObjectPin) (*runtimeloader.Pointer, error) {
			return nil, errors.New("pointer missing")
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			t.Fatal("pointer failure must not fall through to C1 LoadPins")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil || st.Contract.activation != ActivationRevisionPinned {
		t.Fatalf("pointer miss must keep LKG, got %+v act=%s", st, st.Contract.Activation())
	}
	if !strings.Contains(fmt.Sprint(st.Errs), "pointer missing") {
		t.Fatalf("errors=%v", st.Errs)
	}
	assertRedirectFromContract(t, st.Contract)
}

func TestMalformedPointerEnvKeepsDirLKG(t *testing.T) {
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		UsePointer:     true,
		PointerErr:     errors.New("TRAFFIC_POINTER_GENERATION \"abc\""),
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return twoExecutableBundle(t), nil
		},
		LoadPointer: func(context.Context, runtimeloader.ObjectReader, runtimeloader.ObjectPin) (*runtimeloader.Pointer, error) {
			t.Fatal("LoadPointer must not run when pointer env is invalid")
			return nil, nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			t.Fatal("LoadPins must not run when pointer env is invalid")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.KeptLKG || st.Contract == nil || st.Contract.activation != ActivationRevisionPinned {
		t.Fatal("invalid TRAFFIC_POINTER_* must keep LKG")
	}
	if !strings.Contains(fmt.Sprint(st.Errs), "TRAFFIC_POINTER_GENERATION") {
		t.Fatalf("errors=%v", st.Errs)
	}
}

func TestGCSOverlayReplacesLKGWhenValid(t *testing.T) {
	next := twoExecutableBundle(t)
	st, err := LoadStartup(context.Background(), StartupInput{
		PinnedDir:      "lkg",
		ArtifactSource: artifactSourceGCS,
		GCS:            errReader{},
		LoadDir: func(string) (*runtimeloader.Bundle, error) {
			return loadPinnedBundle(t), nil
		},
		LoadPins: func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
			return next, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.KeptLKG || st.Contract == nil || st.Contract.activation != ActivationGCSGeneration {
		t.Fatalf("valid GCS must swap: kept=%v act=%s", st.KeptLKG, st.Contract.activation)
	}
	assertRedirectFromContract(t, st.Contract)
}

func incompatibleEnabledV1(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b := twoExecutableBundle(t)
	for i, v := range b.Program.Variants {
		if v.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		b.Program.Variants[i].SchemaVersion = 1
		b.Program.Variants[i].HandlerVersion = trafficwire.HandlerRedirectTarget
		b.Program.Variants[i].Payload = json.RawMessage(`{"enabled":true,"targetId":"broken"}`)
	}
	return b
}

func unsupportedFieldBundle(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b := twoExecutableBundle(t)
	b.Program.Vectors = append([]trafficwire.Vector(nil), b.Program.Vectors...)
	b.Program.Vectors = append(b.Program.Vectors, trafficwire.Vector{
		DistributionKey: "redirect.target.rule.m-campaign",
		ChoiceDomain:    trafficwire.ChoiceDomainVariant,
		ChoiceIDs:       []string{chosenID},
	})
	idx := len(b.Program.Vectors) - 1
	b.Program.SlotPrograms = append([]trafficwire.SlotProgram(nil), b.Program.SlotPrograms...)
	for i, sp := range b.Program.SlotPrograms {
		if sp.ScenarioID != "redirect" || sp.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		rule := trafficwire.Rule{
			RuleID: "m-campaign",
			When: trafficwire.Matcher{
				Op:       trafficwire.OpCondition,
				Field:    "m_campaign",
				Operator: trafficwire.OperatorEQ,
				Value:    json.RawMessage(`"x"`),
			},
			Branch: trafficwire.Branch{VectorIndex: idx},
		}
		b.Program.SlotPrograms[i].Program.Rules = append([]trafficwire.Rule{rule}, append([]trafficwire.Rule(nil), sp.Program.Rules...)...)
	}
	return b
}

func assertRedirectFromContract(t *testing.T, c *Contract) {
	t.Helper()
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "t5-lkg", nil }
	h.StoreContract(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("LKG GET / = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "campaign=contract-smoke") {
		t.Fatalf("Location=%q", rec.Header().Get("Location"))
	}
}
