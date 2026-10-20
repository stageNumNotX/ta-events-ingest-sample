package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ta-events-ingest/internal/runtimeloader"
)

type memLatest struct {
	mu  sync.Mutex
	obj runtimeloader.LatestObject
	err error
}

func (m *memLatest) ReadLatest(ctx context.Context, _, _ string) (runtimeloader.LatestObject, error) {
	if err := ctx.Err(); err != nil {
		return runtimeloader.LatestObject{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return runtimeloader.LatestObject{}, m.err
	}
	out := m.obj
	if out.Data != nil {
		out.Data = append([]byte(nil), out.Data...)
	}
	return out, nil
}

func (m *memLatest) set(version, generation int64, setID string, pins runtimeloader.GCSPins) {
	body := mustPointerJSON(version, setID, pins)
	sum := sha256.Sum256(body)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = nil
	m.obj = runtimeloader.LatestObject{
		Data:       body,
		Generation: generation,
		Size:       len(body),
		SHA256:     fmt.Sprintf("%x", sum[:]),
	}
}

func (m *memLatest) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

type nopPins struct{}

func (nopPins) ReadObject(context.Context, runtimeloader.ObjectPin) ([]byte, error) {
	return nil, errors.New("generation-pinned ReadObject must not run in poller unit tests")
}

func mustPointerJSON(version int64, setID string, pins runtimeloader.GCSPins) []byte {
	type pinJSON struct {
		Object     string `json:"object"`
		Generation int64  `json:"generation"`
		SHA256     string `json:"sha256"`
		Size       int    `json:"size"`
	}
	raw, err := json.Marshal(map[string]any{
		"version":       version,
		"artifactSetId": setID,
		"targetId":      "prototype",
		"program": pinJSON{
			Object: pins.Program.Object, Generation: pins.Program.Generation,
			SHA256: pins.Program.SHA256, Size: pins.Program.Size,
		},
		"weights": pinJSON{
			Object: pins.Weights.Object, Generation: pins.Weights.Generation,
			SHA256: pins.Weights.SHA256, Size: pins.Weights.Size,
		},
		"release": pinJSON{
			Object: pins.Release.Object, Generation: pins.Release.Generation,
			SHA256: pins.Release.SHA256, Size: pins.Release.Size,
		},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func pinsAt(gen int64) runtimeloader.GCSPins {
	p := runtimeloader.DefaultPins()
	p.Program.Generation = gen
	p.Weights.Generation = gen
	p.Release.Generation = gen
	return p
}

func contractWithCampaign(t *testing.T, version, generation int64, setID, campaign string) *Contract {
	t.Helper()
	c, err := NewContract(bundleWithCampaign(t, campaign), ActivationGCSPointer)
	if err != nil {
		t.Fatal(err)
	}
	c.attachPointer(version, generation, setID)
	return c
}

func bundleWithCampaign(t *testing.T, campaign string) *runtimeloader.Bundle {
	t.Helper()
	b := twoExecutableBundle(t)
	url := "https://example.com/?campaign=" + campaign
	for i, v := range b.Program.Variants {
		if v.ID != chosenID {
			continue
		}
		b.Program.Variants[i].Payload = json.RawMessage(`{"enabled":true,"targetId":"chosen-target","url":"` + url + `"}`)
	}
	return b
}

func testPoller(t *testing.T, latest *memLatest, loadPins func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)) (*PointerPoller, *Handler) {
	t.Helper()
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "poll-click", nil }
	p := NewPointerPoller(PointerPollerConfig{
		Handler:  h,
		Latest:   latest,
		Pins:     nopPins{},
		Bucket:   runtimeloader.PinBucket,
		Object:   runtimeloader.PointerObject,
		Interval: 20 * time.Millisecond,
		Jitter:   time.Millisecond,
		LoadPins: loadPins,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return p, h
}

func TestPollerABAIncreasingVersions(t *testing.T) {
	latest := &memLatest{}
	bundleA := bundleWithCampaign(t, "set-a")
	bundleB := bundleWithCampaign(t, "set-b")
	p, h := testPoller(t, latest, func(_ context.Context, _ runtimeloader.ObjectReader, pins runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		switch pins.Program.Generation {
		case 10, 30:
			return bundleA, nil
		case 20:
			return bundleB, nil
		default:
			return nil, fmt.Errorf("unexpected pin gen %d", pins.Program.Generation)
		}
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))

	latest.set(2, 200, "set-b", pinsAt(20))
	if err := p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.CurrentContract(); got.PointerVersion() != 2 || got.ArtifactSetID() != "set-b" {
		t.Fatalf("B: version=%d set=%s", got.PointerVersion(), got.ArtifactSetID())
	}
	assertRootCampaign(t, h, "set-b")

	latest.set(3, 300, "set-a", pinsAt(30))
	if err := p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.CurrentContract(); got.PointerVersion() != 3 || got.ArtifactSetID() != "set-a" {
		t.Fatalf("A2: version=%d set=%s", got.PointerVersion(), got.ArtifactSetID())
	}
	assertRootCampaign(t, h, "set-a")
}

func TestStaleLoadDoesNotOverwriteNewer(t *testing.T) {
	latest := &memLatest{}
	v2started := make(chan struct{})
	v2release := make(chan struct{})
	p, h := testPoller(t, latest, func(ctx context.Context, _ runtimeloader.ObjectReader, pins runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		switch pins.Program.Generation {
		case 20:
			close(v2started)
			select {
			case <-v2release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return bundleWithCampaign(t, "set-b"), nil
		case 30:
			return bundleWithCampaign(t, "set-c"), nil
		default:
			return bundleWithCampaign(t, "set-a"), nil
		}
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))

	latest.set(2, 200, "set-b", pinsAt(20))
	done := make(chan error, 1)
	go func() { done <- p.PollOnce(context.Background()) }()
	select {
	case <-v2started:
	case <-time.After(2 * time.Second):
		t.Fatal("v2 load did not start")
	}

	latest.set(3, 300, "set-c", pinsAt(30))
	if err := p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.CurrentContract(); got.PointerVersion() != 3 || got.ArtifactSetID() != "set-c" {
		t.Fatalf("want v3, got version=%d set=%s", got.PointerVersion(), got.ArtifactSetID())
	}

	close(v2release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.CurrentContract(); got.PointerVersion() != 3 || got.ArtifactSetID() != "set-c" {
		t.Fatalf("stale v2 overwrote newer: version=%d set=%s", got.PointerVersion(), got.ArtifactSetID())
	}
}

func TestFailedPollKeepsLKG(t *testing.T) {
	latest := &memLatest{}
	p, h := testPoller(t, latest, func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		t.Fatal("LoadPins must not run when latest read fails")
		return nil, nil
	})
	start := contractWithCampaign(t, 1, 100, "set-a", "set-a")
	h.StoreContract(start)
	latest.fail(errors.New("pointer unavailable"))
	if err := p.PollOnce(context.Background()); err == nil {
		t.Fatal("want latest-read error")
	}
	if h.CurrentContract() != start {
		t.Fatal("failed poll must keep LKG pointer")
	}

	latest.set(2, 200, "set-b", pinsAt(20))
	p.loadPins = func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		return nil, errors.New("sha256 mismatch")
	}
	if err := p.PollOnce(context.Background()); err == nil {
		t.Fatal("want prepare error")
	}
	if h.CurrentContract() != start || h.CurrentContract().PointerVersion() != 1 {
		t.Fatal("prepare failure must keep LKG")
	}
}

func TestSameVersionNewGenerationDoesNotSwap(t *testing.T) {
	latest := &memLatest{}
	var loads atomic.Int64
	p, h := testPoller(t, latest, func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		loads.Add(1)
		return bundleWithCampaign(t, "set-a"), nil
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	latest.set(1, 101, "set-a-rewritten", pinsAt(11))
	if err := p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 0 {
		t.Fatal("same version must not load artifacts")
	}
	if got := h.CurrentContract(); got.PointerGeneration() != 100 || got.ArtifactSetID() != "set-a" {
		t.Fatalf("idempotent activate must not swap: gen=%d set=%s", got.PointerGeneration(), got.ArtifactSetID())
	}
}

func TestSingleflightSameVersion(t *testing.T) {
	latest := &memLatest{}
	var loads atomic.Int64
	p, h := testPoller(t, latest, func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		loads.Add(1)
		time.Sleep(80 * time.Millisecond)
		return bundleWithCampaign(t, "set-b"), nil
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	latest.set(2, 200, "set-b", pinsAt(20))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.PollOnce(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Fatalf("singleflight loads=%d want 1", n)
	}
	if h.CurrentContract().PointerVersion() != 2 {
		t.Fatal("coalesced load must still swap")
	}
}

func TestTwoPollersConverge(t *testing.T) {
	latest := &memLatest{}
	load := func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		return bundleWithCampaign(t, "set-b"), nil
	}
	p1, h1 := testPoller(t, latest, load)
	p2, h2 := testPoller(t, latest, load)
	h1.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	h2.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	latest.set(2, 200, "set-b", pinsAt(20))
	if err := p1.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p2.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h1.CurrentContract().PointerVersion() != 2 || h2.CurrentContract().PointerVersion() != 2 {
		t.Fatalf("instances must converge: %d %d", h1.CurrentContract().PointerVersion(), h2.CurrentContract().PointerVersion())
	}
	if h1.CurrentContract().ArtifactSetID() != "set-b" || h2.CurrentContract().ArtifactSetID() != "set-b" {
		t.Fatal("both instances must serve the same artifact set")
	}
}

func TestInFlightRequestIgnoresLaterSwap(t *testing.T) {
	a := contractWithCampaign(t, 1, 100, "set-a", "set-a")
	hold := make(chan struct{})
	ready := make(chan struct{})
	a.hold = hold
	a.holdReady = ready
	b := contractWithCampaign(t, 2, 200, "set-b", "set-b")

	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "held", nil }
	q := &recEvents{}
	h.SetEnqueue(q)
	h.StoreContract(a)

	locCh := make(chan string, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		locCh <- rec.Header().Get("Location")
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach Resolve")
	}
	if !h.SwapIfNewer(b) {
		t.Fatal("swap B")
	}
	if h.CurrentContract().ArtifactSetID() != "set-b" {
		t.Fatal("head must already be B")
	}
	close(hold)
	loc := <-locCh
	if !strings.Contains(loc, "campaign=set-a") {
		t.Fatalf("in-flight request mixed releases: %q", loc)
	}
	if strings.Contains(loc, "campaign=set-b") {
		t.Fatalf("in-flight used newer release: %q", loc)
	}
	evs := q.events()
	if len(evs) != 3 {
		t.Fatalf("in-flight events=%d", len(evs))
	}
	for _, ev := range evs {
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["artifact_set_id"] != "set-a" {
			t.Fatalf("event provenance mixed: artifact_set_id=%q want set-a (head is set-b) event=%s", params["artifact_set_id"], ev.EventName)
		}
		if params["pointer_version"] != "1" {
			t.Fatalf("pointer_version=%q", params["pointer_version"])
		}
		if params["contract_release_sha256"] == "" {
			t.Fatal("missing captured release digest")
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "campaign=set-b") {
		t.Fatalf("next request must use B: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSwapIfNewerRejectsStale(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.StoreContract(contractWithCampaign(t, 3, 300, "set-c", "set-c"))
	older := contractWithCampaign(t, 2, 200, "set-b", "set-b")
	if h.SwapIfNewer(older) {
		t.Fatal("stale version must not CAS")
	}
	if h.CurrentContract().PointerVersion() != 3 {
		t.Fatal("head changed")
	}
}

func TestPollerRunPollsImmediately(t *testing.T) {
	latest := &memLatest{}
	latest.set(2, 200, "set-b", pinsAt(20))
	p, h := testPoller(t, latest, func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		return bundleWithCampaign(t, "set-b"), nil
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	// A wait-first Run would miss this deadline.
	p.interval = 30 * time.Second
	p.jitter = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c := h.CurrentContract(); c != nil && c.PointerVersion() == 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Run must poll immediately, not wait a full interval")
}

func TestPollerRunPicksUpNewerVersion(t *testing.T) {
	latest := &memLatest{}
	p, h := testPoller(t, latest, func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error) {
		return bundleWithCampaign(t, "set-b"), nil
	})
	h.StoreContract(contractWithCampaign(t, 1, 100, "set-a", "set-a"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	latest.set(2, 200, "set-b", pinsAt(20))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c := h.CurrentContract(); c != nil && c.PointerVersion() == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Run did not observe version 2")
}

func TestNextWaitStaysInJitterWindow(t *testing.T) {
	p := NewPointerPoller(PointerPollerConfig{
		Handler:  NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Interval: 100 * time.Millisecond,
		Jitter:   50 * time.Millisecond,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	seen := map[time.Duration]struct{}{}
	for i := 0; i < 40; i++ {
		w := p.nextWait(p.interval)
		if w < 100*time.Millisecond || w > 150*time.Millisecond {
			t.Fatalf("wait %v outside [100ms,150ms]", w)
		}
		seen[w] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatal("expected jitter variation")
	}
}

func TestContractJSONIncludesPointerMeta(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := contractWithCampaign(t, 7, 1789108332117984, "set-observe", "set-observe")
	h.StoreContract(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/contract", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var st ContractStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.PointerVersion != 7 || st.PointerGeneration != 1789108332117984 || st.ArtifactSetID != "set-observe" {
		t.Fatalf("pointer meta %+v", st)
	}
	if st.Activation != ActivationGCSPointer {
		t.Fatalf("activation=%s", st.Activation)
	}
}

func assertRootCampaign(t *testing.T, h *Handler, campaign string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("GET / = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "campaign="+campaign) {
		t.Fatalf("Location=%q want campaign=%s", loc, campaign)
	}
}
