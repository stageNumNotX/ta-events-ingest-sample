package redirect

import (
	"net/url"
	"strings"
	"testing"
)

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{"https example", "https://example.com/", false},
		{"https with path", "https://example.com/offer", false},
		{"http", "http://example.com/", true},
		{"empty scheme", "//example.com/", true},
		{"empty", "", true},
		{"userinfo", "https://user:pass@example.com/", true},
		{"crlf", "https://example.com/\r\n", true},
		{"fragment", "https://example.com/#x", true},
		{"relative", "/path", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTarget(tt.target)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %q", tt.target)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildSetsClickIDAndKeepsTargetParams(t *testing.T) {
	loc, err := Build("https://example.com/r?foo=1&click_id=old", Substitutions{
		ClickID:  "new-click",
		TASource: "proto-smoke",
		TASub:    "sub1",
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "example.com" || u.Path != "/r" {
		t.Fatalf("unexpected location %q", loc)
	}
	q := u.Query()
	if q.Get("click_id") != "new-click" {
		t.Errorf("click_id = %q, want new-click", q.Get("click_id"))
	}
	if q.Get("foo") != "1" {
		t.Errorf("target query foo should be preserved, got %q", q.Get("foo"))
	}
	if q.Get("ta_source") != "proto-smoke" || q.Get("ta_sub") != "sub1" {
		t.Errorf("substitutions missing: %q", loc)
	}
}

func TestBuildOmitsInvalidSubstitutions(t *testing.T) {
	loc, err := Build("https://example.com/", Substitutions{
		ClickID:  "abc",
		TASource: "bad\rvalue",
		TASub:    strings.Repeat("x", MaxSubstitutionLen+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if q.Query().Get("ta_source") != "" || q.Query().Get("ta_sub") != "" {
		t.Fatalf("invalid substitutions should be omitted, got %q", loc)
	}
}

func TestBuildRequiresClickID(t *testing.T) {
	if _, err := Build("https://example.com/", Substitutions{}); err == nil {
		t.Fatal("expected error for empty click_id")
	}
}
