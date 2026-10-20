package ingest

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		ev      IncomingEvent
		wantErr bool
	}{
		{"ingress ok", IncomingEvent{EventName: "ingress", ClickID: "k1"}, false},
		{"scenario_assigned ok", IncomingEvent{EventName: "scenario_assigned", ClickID: "k1"}, false},
		{"redirect ok", IncomingEvent{EventName: "redirect", ClickID: "k1"}, false},
		{"pageview ok", IncomingEvent{EventName: "pageview", ClickID: "k1"}, false},
		{"missing event_name", IncomingEvent{ClickID: "k1"}, true},
		{"unknown event_name", IncomingEvent{EventName: "click", ClickID: "k1"}, true},
		{"missing click_id", IncomingEvent{EventName: "ingress"}, true},
		{"empty", IncomingEvent{}, true},
		{"click_id too long", IncomingEvent{EventName: "ingress", ClickID: strings.Repeat("x", maxClickIDLen+1)}, true},
		{"url too long", IncomingEvent{EventName: "ingress", ClickID: "k1", URL: "https://example.com/" + strings.Repeat("x", maxURLLen)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.ev)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("expected ErrValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
