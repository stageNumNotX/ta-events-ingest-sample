package traffic

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimeloader"
)

type recQ struct {
	n atomic.Int64
}

func (r *recQ) TryEnqueue(ev ingest.CanonicalEvent) bool {
	r.n.Add(1)
	return true
}

type dropQ struct{ n atomic.Int64 }

func (d *dropQ) TryEnqueue(ingest.CanonicalEvent) bool {
	d.n.Add(1)
	return false
}

func TestContractJSON(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h.StoreContract(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/contract", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control")
	}
	var st ContractStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.T6.MatchedScenarioRuleID != "bots-redirect" || st.T6.Enabled || st.T6.HasURL {
		t.Fatalf("%+v", st)
	}
	if st.Request.ContextSource != PrototypeContextSource {
		t.Fatalf("request source %q", st.Request.ContextSource)
	}
	if st.LiveRedirectFromThisRelease {
		t.Fatal("bootstrap request path has no executable URL")
	}
	if st.HTTPRelease != prototypeReleaseName {
		t.Fatalf("httpRelease %q", st.HTTPRelease)
	}
}

func TestContractMissingIs503(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/contract", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestContractDoesNotMint(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := false
	h.mint = func() (string, error) {
		called = true
		return "nope", nil
	}
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h.StoreContract(c)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/contract", nil))
	if called {
		t.Fatal("GET /contract must not mint click_id")
	}
}

func TestRootEnqueuesThreeEvents(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-evt", nil }
	q := &recQ{}
	h.SetEnqueue(q)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=proto-smoke", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if q.n.Load() != 3 {
		t.Fatalf("enqueued=%d want 3", q.n.Load())
	}
}

func TestRootRedirectsWhenEnqueueDrops(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-drop", nil }
	h.SetEnqueue(&dropQ{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Location") == "" {
		t.Fatal("Location")
	}
}

func TestContractHead405(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/contract", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestRootDoesNotUseContractDisabledTarget(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "c1", nil }
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h.StoreContract(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (selected disabled_target)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "" {
		t.Fatalf("must not fall back to fixture or scan, Location=%q", loc)
	}
}
