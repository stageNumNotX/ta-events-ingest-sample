package runtimepub

import (
	"encoding/json"
	"strings"
	"testing"

	"ta-events-ingest/internal/ingest"
)

func TestMarshalCanonicalKeepsQueryAmpersand(t *testing.T) {
	ev := ingest.CanonicalEvent{
		EventID:            "click/redirect",
		EventName:          ingest.EventRedirect,
		EventTime:          "2026-09-11T02:13:54.000000Z",
		ReceivedAt:         "2026-09-11T02:13:54.000000Z",
		ClickID:            "click",
		Path:               "/",
		Method:             "GET",
		ScenarioID:         "redirect",
		SmartlinkVariantID: "smartlink-main",
		RedirectTo:         "https://example.com/?campaign=contract-smoke&click_id=abc&ta_source=proto-smoke",
		Headers:            "{}",
		CF:                 "{}",
		Params:             `{"context_source":"prototype-context"}`,
		Raw:                "{}",
	}
	b, err := marshalCanonical(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `\u0026`) {
		t.Fatalf("HTML-escaped ampersand in JSON: %s", s)
	}
	if !strings.Contains(s, `campaign=contract-smoke&click_id=abc`) {
		t.Fatalf("redirect_to missing raw query: %s", s)
	}
	var back ingest.CanonicalEvent
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.RedirectTo != ev.RedirectTo || back.EventName != ingest.EventRedirect {
		t.Fatalf("%+v", back)
	}
	attrs := eventAttributes(ev)
	if attrs["event_name"] != ingest.EventRedirect || attrs["click_id"] != "click" {
		t.Fatalf("%v", attrs)
	}
	if attrs["scenario_id"] != "redirect" || attrs["smartlink_variant_id"] != "smartlink-main" {
		t.Fatalf("%v", attrs)
	}
	if _, ok := attrs["redirect_to"]; ok {
		t.Fatal("redirect_to must not be a Pub/Sub attribute")
	}
	if !strings.Contains(s, `"cost":""`) {
		t.Fatalf("empty cost must serialize: %s", s)
	}
	if _, ok := attrs["cost"]; ok {
		t.Fatal("cost must not be a Pub/Sub attribute")
	}

	ev.Cost = "0.00002"
	b, err = marshalCanonical(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"cost":"0.00002"`) {
		t.Fatalf("non-empty cost missing: %s", b)
	}
	if _, ok := eventAttributes(ev)["cost"]; ok {
		t.Fatal("cost value must not become a Pub/Sub attribute")
	}
}
