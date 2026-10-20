package traffic

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

const (
	decoyURL   = "https://example.com/?campaign=decoy"
	chosenURL  = "https://example.com/?campaign=contract-smoke"
	chosenID   = "chosen-target"
	decoyVarID = preferredVariantID
)

type recEvents struct {
	mu sync.Mutex
	ev []ingest.CanonicalEvent
}

func (r *recEvents) TryEnqueue(ev ingest.CanonicalEvent) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, ev)
	return true
}

func (r *recEvents) events() []ingest.CanonicalEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ingest.CanonicalEvent, len(r.ev))
	copy(out, r.ev)
	return out
}

func loadPinnedBundle(t testing.TB) *runtimeloader.Bundle {
	t.Helper()
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func twoExecutableBundle(t testing.TB) *runtimeloader.Bundle {
	t.Helper()
	b := loadPinnedBundle(t)
	b.Program.Variants = append(append([]trafficwire.Variant(nil), b.Program.Variants...),
		trafficwire.Variant{
			ID:             decoyVarID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"smartlink-main","url":"` + decoyURL + `"}`),
		},
		trafficwire.Variant{
			ID:             chosenID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"chosen-target","url":"` + chosenURL + `"}`),
		},
	)
	for i, vec := range b.Program.Vectors {
		switch vec.DistributionKey {
		case "redirect.target.default":
			b.Program.Vectors[i].ChoiceIDs = []string{chosenID}
		case "scenario.default":
			b.Program.Vectors[i].ChoiceIDs = []string{"redirect"}
		}
	}
	return b
}

func TestHTTPReleasePrefersUnselectedDecoy(t *testing.T) {
	b := twoExecutableBundle(t)
	rel, err := HTTPReleaseFromBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Target != decoyURL {
		t.Fatalf("scan target %q, want decoy", rel.Target)
	}
}

func TestRootUsesSelectedVariantNotScannedDecoy(t *testing.T) {
	b := twoExecutableBundle(t)
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := HTTPReleaseFromBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(scanned, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-resolve", nil }
	q := &recEvents{}
	h.SetEnqueue(q)
	h.StoreContract(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=proto-smoke&url="+url.QueryEscape(decoyURL), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "campaign=contract-smoke") {
		t.Fatalf("Location missing selected URL: %q", loc)
	}
	if strings.Contains(loc, "campaign=decoy") {
		t.Fatalf("Location used scanned decoy: %q", loc)
	}
	if !strings.Contains(loc, "click_id=click-resolve") {
		t.Fatalf("click_id missing: %q", loc)
	}

	var assigned, redir ingest.CanonicalEvent
	for _, ev := range q.events() {
		switch ev.EventName {
		case ingest.EventScenarioAssigned:
			assigned = ev
		case ingest.EventRedirect:
			redir = ev
		}
	}
	if assigned.ScenarioID != "redirect" || assigned.SmartlinkVariantID != chosenID {
		t.Fatalf("scenario_assigned scenario=%q variant=%q", assigned.ScenarioID, assigned.SmartlinkVariantID)
	}
	if strings.Contains(assigned.ScenarioID, httpReleasePrefix) {
		t.Fatalf("scenario_id still httpRelease %q", assigned.ScenarioID)
	}
	if redir.SmartlinkVariantID != chosenID || redir.ScenarioID != "redirect" {
		t.Fatalf("redirect event %+v", redir)
	}
}

func TestReadyWithContractOnly(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b := twoExecutableBundle(t)
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h.StoreContract(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready=%d", rec.Code)
	}
}

func TestRootDoesNotReadContextFromRequest(t *testing.T) {
	b := twoExecutableBundle(t)
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "c1", nil }
	h.StoreContract(c)
	req := httptest.NewRequest(http.MethodGet, "/?country=RU&isBot=true", nil)
	req.Header.Set("CF-IPCountry", "RU")
	req.Header.Set("CF-Bot-Score", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "campaign=contract-smoke") {
		t.Fatalf("headers/query must not change prototype Resolve, Location=%q", loc)
	}
}
