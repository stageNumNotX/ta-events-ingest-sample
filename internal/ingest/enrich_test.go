package ingest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
}

func TestCanonicalizeMapping(t *testing.T) {
	raw := json.RawMessage(`{"event_name":"ingress","click_id":"k7f3a1b2c","ip":"1.2.3.4"}`)
	ev := IncomingEvent{
		EventName:  "ingress",
		ClickID:    "k7f3a1b2c",
		OccurredAt: "2026-06-16T10:00:00.005Z",
		Country:    "DE",
		TASource:   "fb",
		TASub:      "123",
		CampaignID: "camp-1",
		ZoneID:     "zone-9",
		Cost:       "0.012300",
		UserAgent:  "Mozilla/5.0",
		IP:         "1.2.3.4",
		URL:        "https://example.com/?ta_source=fb&ta_sub=123",
		Method:     "GET",
		Headers:    json.RawMessage(`{"x":"y"}`),
		CF:         json.RawMessage(`{"asn":3320}`),
	}

	ce, err := Canonicalize(raw, ev, "pepper", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ce.EventID == "" || len(ce.EventID) != 36 {
		t.Errorf("event_id looks wrong: %q", ce.EventID)
	}
	if ce.EventName != "ingress" {
		t.Errorf("event_name = %q", ce.EventName)
	}
	if ce.EventTime != "2026-06-16T10:00:00.005Z" {
		t.Errorf("event_time = %q, want 2026-06-16T10:00:00.005Z", ce.EventTime)
	}
	if ce.ReceivedAt != "2026-06-16T10:00:00Z" {
		t.Errorf("received_at = %q", ce.ReceivedAt)
	}
	if ce.Path != "/" {
		t.Errorf("path = %q, want /", ce.Path)
	}
	if ce.Search != "?ta_source=fb&ta_sub=123" {
		t.Errorf("search = %q", ce.Search)
	}
	if ce.CampaignID != "camp-1" || ce.ZoneID != "zone-9" {
		t.Errorf("campaign/zone = %q %q", ce.CampaignID, ce.ZoneID)
	}
	if ce.Cost != "0.012300" {
		t.Errorf("cost = %q", ce.Cost)
	}
	rawJSON, err := json.Marshal(ce)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawJSON), `"cost":"0.012300"`) {
		t.Errorf("canonical JSON missing exact cost: %s", rawJSON)
	}
	wantHash := hashIP("1.2.3.4", "pepper")
	if ce.IPHash != wantHash || ce.IPHash == "" {
		t.Errorf("ip_hash = %q, want %q", ce.IPHash, wantHash)
	}
	if ce.Headers != `{"x":"y"}` {
		t.Errorf("headers = %q", ce.Headers)
	}
	if ce.CF != `{"asn":3320}` {
		t.Errorf("cf = %q", ce.CF)
	}
	if ce.Params != "{}" {
		t.Errorf("params default = %q, want {}", ce.Params)
	}
	if ce.Raw != string(raw) {
		t.Errorf("raw = %q, want %q", ce.Raw, string(raw))
	}
}

func TestCanonicalizeAbsentCostIsEmptyJSONKey(t *testing.T) {
	ev := IncomingEvent{EventName: "ingress", ClickID: "k1"}
	ce, err := Canonicalize(json.RawMessage(`{"event_name":"ingress","click_id":"k1"}`), ev, "salt", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ce.Cost != "" {
		t.Errorf("absent cost = %q, want empty", ce.Cost)
	}
	rawJSON, err := json.Marshal(ce)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawJSON), `"cost":""`) {
		t.Errorf("empty cost must serialize without omitempty: %s", rawJSON)
	}
}

func TestCanonicalizeOccurredAtFallback(t *testing.T) {
	ev := IncomingEvent{EventName: "ingress", ClickID: "k1"} // no occurred_at
	ce, err := Canonicalize(json.RawMessage(`{}`), ev, "salt", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ce.EventTime != ce.ReceivedAt {
		t.Errorf("expected event_time fallback to received_at: event_time=%q received_at=%q", ce.EventTime, ce.ReceivedAt)
	}
}

func TestCanonicalizeNoIP(t *testing.T) {
	ev := IncomingEvent{EventName: "ingress", ClickID: "k1"}
	ce, _ := Canonicalize(json.RawMessage(`{}`), ev, "salt", fixedNow())
	if ce.IPHash != "" {
		t.Errorf("ip_hash for empty ip = %q, want empty", ce.IPHash)
	}
}

func TestNewUUIDv4Unique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id, err := newUUIDv4()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) != 36 {
			t.Fatalf("unexpected uuid length: %q", id)
		}
		if id[14] != '4' {
			t.Errorf("version nibble not 4: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate uuid: %q", id)
		}
		seen[id] = struct{}{}
	}
}
