package ingest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testSecret = "s3cret-token"

type mockPublisher struct {
	mu     sync.Mutex
	events []CanonicalEvent
	err    error
}

func (m *mockPublisher) Publish(ev CanonicalEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, ev)
	return nil
}

func (m *mockPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func newTestHandler(pub Publisher) (http.Handler, *Metrics) {
	m := &Metrics{}
	h := NewHandler(Options{
		Secret:       testSecret,
		Salt:         "pepper",
		MaxBatchSize: 3,
		Publisher:    pub,
		Metrics:      m,
		Now:          fixedNow,
	})
	return h, m
}

func doEvents(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h, _ := newTestHandler(&mockPublisher{})
	for _, path := range []string{"/healthz", "/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("%s body = %q, want ok", path, rec.Body.String())
		}
	}
}

func TestAuth(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"wrong token", "nope", http.StatusUnauthorized},
		{"correct token", testSecret, http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandler(&mockPublisher{})
			rec := doEvents(t, h, tt.token, `{"event_name":"ingress","click_id":"k1"}`)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestSingleEvent(t *testing.T) {
	pub := &mockPublisher{}
	h, m := newTestHandler(pub)
	rec := doEvents(t, h, testSecret, `{"event_name":"ingress","click_id":"k1","ip":"1.2.3.4","url":"https://example.com/?a=b"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 1 {
		t.Fatalf("published %d events, want 1", pub.count())
	}
	if m.EventsPublished.Load() != 1 {
		t.Errorf("EventsPublished = %d, want 1", m.EventsPublished.Load())
	}
	ev := pub.events[0]
	if ev.EventName != "ingress" || ev.ClickID != "k1" {
		t.Errorf("unexpected mapping: %+v", ev)
	}
	if ev.IPHash == "" {
		t.Errorf("expected ip_hash to be set")
	}
	if ev.Path != "/" || ev.Search != "?a=b" {
		t.Errorf("path/search mapping wrong: path=%q search=%q", ev.Path, ev.Search)
	}
	if ev.Cost != "" {
		t.Errorf("omitted cost = %q, want empty", ev.Cost)
	}
}

func TestCostStringCopiedNumericRejected(t *testing.T) {
	pub := &mockPublisher{}
	h, _ := newTestHandler(pub)
	rec := doEvents(t, h, testSecret, `{"event_name":"ingress","click_id":"k1","cost":"0.0123"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 1 {
		t.Fatalf("published %d events, want 1", pub.count())
	}
	if pub.events[0].Cost != "0.0123" {
		t.Fatalf("cost = %q", pub.events[0].Cost)
	}

	pub = &mockPublisher{}
	h, _ = newTestHandler(pub)
	rec = doEvents(t, h, testSecret, `{"event_name":"ingress","click_id":"k1","cost":null}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("null cost status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.events[0].Cost != "" {
		t.Fatalf("JSON null cost = %q, want empty", pub.events[0].Cost)
	}

	pub = &mockPublisher{}
	h, m := newTestHandler(pub)
	rec = doEvents(t, h, testSecret, `{"event_name":"ingress","click_id":"k1","cost":0.0123}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("numeric cost status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 0 {
		t.Errorf("numeric cost must not publish, got %d", pub.count())
	}
	if m.BadRequests.Load() != 1 {
		t.Errorf("BadRequests = %d, want 1", m.BadRequests.Load())
	}
}

func TestCostNumericInBatchRejectsWholeBatch(t *testing.T) {
	pub := &mockPublisher{}
	h, m := newTestHandler(pub)
	body := `[
		{"event_name":"ingress","click_id":"k1","cost":"0.0123"},
		{"event_name":"ingress","click_id":"k2","cost":0.0123}
	]`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed batch status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 0 {
		t.Fatalf("numeric cost in batch must publish 0 events, got %d", pub.count())
	}
	if m.EventsPublished.Load() != 0 {
		t.Errorf("EventsPublished = %d, want 0", m.EventsPublished.Load())
	}
	if m.BadRequests.Load() != 1 {
		t.Errorf("BadRequests = %d, want 1", m.BadRequests.Load())
	}
	if m.ValidationErrors.Load() != 0 {
		t.Errorf("ValidationErrors = %d, want 0 (type error is BadRequests)", m.ValidationErrors.Load())
	}
}

func TestBatch(t *testing.T) {
	pub := &mockPublisher{}
	h, _ := newTestHandler(pub)
	body := `[
		{"event_name":"ingress","click_id":"k1"},
		{"event_name":"scenario_assigned","click_id":"k1","scenario_id":"smartlink","seed":"abc"},
		{"event_name":"redirect","click_id":"k1","smartlink_variant_id":"v1","redirect_to":"https://x"}
	]`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 3 {
		t.Fatalf("published %d events, want 3", pub.count())
	}
}

func TestBatchExceedsLimit(t *testing.T) {
	pub := &mockPublisher{}
	h, m := newTestHandler(pub) // MaxBatchSize = 3
	body := `[
		{"event_name":"ingress","click_id":"k1"},
		{"event_name":"ingress","click_id":"k2"},
		{"event_name":"ingress","click_id":"k3"},
		{"event_name":"ingress","click_id":"k4"}
	]`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if pub.count() != 0 {
		t.Errorf("nothing should be published on oversize batch, got %d", pub.count())
	}
	if m.BadRequests.Load() != 1 {
		t.Errorf("BadRequests = %d, want 1", m.BadRequests.Load())
	}
}

func TestMalformedAndEmpty(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"truncated object", `{"event_name":"ingress"`},
		{"garbage", `not json`},
		{"empty body", ``},
		{"empty array", `[]`},
		{"array with garbage", `[{"event_name":"ingress","click_id":"k1"}, 123, "x"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &mockPublisher{}
			h, _ := newTestHandler(pub)
			rec := doEvents(t, h, testSecret, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			if pub.count() != 0 {
				t.Errorf("nothing should be published, got %d", pub.count())
			}
		})
	}
}

func TestValidationRejectsWholeBatch(t *testing.T) {
	pub := &mockPublisher{}
	h, m := newTestHandler(pub)
	// Второе событие без click_id — весь батч отклоняется, ничего не публикуется.
	body := `[
		{"event_name":"ingress","click_id":"k1"},
		{"event_name":"ingress"}
	]`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if pub.count() != 0 {
		t.Errorf("nothing should be published on validation error, got %d", pub.count())
	}
	if m.ValidationErrors.Load() != 1 {
		t.Errorf("ValidationErrors = %d, want 1", m.ValidationErrors.Load())
	}
}

func TestFieldTooLong(t *testing.T) {
	pub := &mockPublisher{}
	h, m := newTestHandler(pub)
	body := `{"event_name":"ingress","click_id":"` + strings.Repeat("x", maxClickIDLen+1) + `"}`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 0 {
		t.Errorf("nothing should be published, got %d", pub.count())
	}
	if m.ValidationErrors.Load() != 1 {
		t.Errorf("ValidationErrors = %d, want 1", m.ValidationErrors.Load())
	}
	if !strings.Contains(rec.Body.String(), `"error":"field too long"`) {
		t.Errorf("want field too long, got %s", rec.Body.String())
	}
}

func TestBodyTooLarge(t *testing.T) {
	pub := &mockPublisher{}
	m := &Metrics{}
	h := NewHandler(Options{
		Secret:       testSecret,
		Salt:         "pepper",
		MaxBatchSize: 3,
		MaxBodyBytes: 64,
		Publisher:    pub,
		Metrics:      m,
		Now:          fixedNow,
	})
	body := `{"event_name":"ingress","click_id":"k1","url":"https://example.com/` + strings.Repeat("a", 80) + `"}`
	rec := doEvents(t, h, testSecret, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if pub.count() != 0 {
		t.Errorf("nothing should be published, got %d", pub.count())
	}
	if m.BadRequests.Load() != 1 {
		t.Errorf("BadRequests = %d, want 1", m.BadRequests.Load())
	}
}

func TestPublishErrorIsCounted(t *testing.T) {
	pub := &mockPublisher{err: errors.New("boom")}
	h, m := newTestHandler(pub)
	rec := doEvents(t, h, testSecret, `{"event_name":"ingress","click_id":"k1"}`)
	// Сбой публикации не превращается в 5xx: ответ всё равно 202 (fire-and-forget).
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if m.PublishErrors.Load() != 1 {
		t.Errorf("PublishErrors = %d, want 1", m.PublishErrors.Load())
	}
	if m.EventsPublished.Load() != 0 {
		t.Errorf("EventsPublished = %d, want 0", m.EventsPublished.Load())
	}
}
