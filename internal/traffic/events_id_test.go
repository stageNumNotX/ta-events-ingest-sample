package traffic

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ta-events-ingest/internal/ingest"
)

func TestCanonicalEventIDStableOnRetry(t *testing.T) {
	h := NewHandler(testRelease(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-retry", nil }
	q := &recEvents{}
	h.SetEnqueue(q)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=proto-smoke", nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("status=%d", rec.Code)
		}
	}
	evs := q.events()
	if len(evs) != 6 {
		t.Fatalf("enqueued=%d want 6", len(evs))
	}
	first := map[string]ingest.CanonicalEvent{}
	second := map[string]ingest.CanonicalEvent{}
	for i, ev := range evs {
		if i < 3 {
			first[ev.EventName] = ev
		} else {
			second[ev.EventName] = ev
		}
	}
	for _, name := range []string{ingest.EventIngress, ingest.EventScenarioAssigned, ingest.EventRedirect} {
		want := canonicalEventID("click-retry", name)
		a, okA := first[name]
		b, okB := second[name]
		if !okA || !okB {
			t.Fatalf("missing %s", name)
		}
		if a.EventID != want || b.EventID != want {
			t.Fatalf("%s event_id first=%q second=%q want=%q", name, a.EventID, b.EventID, want)
		}
		if a.ClickID != "click-retry" || b.ClickID != "click-retry" {
			t.Fatalf("%s click_id first=%q second=%q", name, a.ClickID, b.ClickID)
		}
	}
}
