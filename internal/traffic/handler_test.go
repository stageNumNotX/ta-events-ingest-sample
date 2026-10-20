package traffic

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func testRelease(t *testing.T) *PreparedRelease {
	t.Helper()
	rel, err := PrototypeRelease("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestHealthAliveWithoutRelease(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want ok", rec.Body.String())
	}
}

func TestReadyRequiresRelease(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	h.StoreRelease(testRelease(t))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready after load status = %d, want 200", rec.Code)
	}
}

func TestRootRedirect(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-fixed", nil }

	req := httptest.NewRequest(http.MethodGet, "/?ta_source=proto-smoke", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "example.com" {
		t.Fatalf("Location = %q, want https://example.com/...", loc)
	}
	if u.Query().Get("click_id") != "click-fixed" {
		t.Errorf("click_id = %q", u.Query().Get("click_id"))
	}
	if u.Query().Get("ta_source") != "proto-smoke" {
		t.Errorf("ta_source = %q", u.Query().Get("ta_source"))
	}
	// httptest не следует Location — тело 302 не должно требовать второго запроса.
	if rec.Header().Get("Location") == "" {
		t.Fatal("missing Location")
	}
}

func TestRootIgnoresAttackerTarget(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "c1", nil }
	req := httptest.NewRequest(http.MethodGet, "/?url=https://evil.example/&redirect_to=https://evil.example/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "evil.example") {
		t.Fatalf("request target leaked into Location: %q", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "example.com" {
		t.Fatalf("Location host = %q, want example.com", u.Host)
	}
}

func TestHeadDoesNotMint(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := false
	h.mint = func() (string, error) {
		called = true
		return "nope", nil
	}
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("HEAD must not set Location, got %q", rec.Header().Get("Location"))
	}
	if called {
		t.Fatal("HEAD must not mint click_id")
	}
}

func TestMethodAndPath(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "c1", nil }

	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", post.Code)
	}

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", missing.Code)
	}
}

func TestRootNotReady(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("not-ready GET / must not redirect")
	}
}

func TestClickIDsAreUnique(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	const n = 50
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusFound {
				t.Errorf("status = %d", rec.Code)
				return
			}
			u, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = u.Query().Get("click_id")
		}()
	}
	wg.Wait()
	seen := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty click_id")
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate click_id %q", id)
		}
		seen[id] = struct{}{}
	}
}
