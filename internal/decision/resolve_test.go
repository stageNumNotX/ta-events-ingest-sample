package decision_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

func loadPrepared(t *testing.T) *decision.Prepared {
	t.Helper()
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := decision.Prepare(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func botTrue() *bool  { v := true; return &v }
func botFalse() *bool { v := false; return &v }

func subject(s string) *string { return &s }

func TestT6BotsRedirect(t *testing.T) {
	d, err := loadPrepared(t).Resolve(decision.Context{IsBot: botTrue(), SubjectID: subject("any")})
	if err != nil {
		t.Fatal(err)
	}
	if d.ScenarioID != "redirect" || d.ScenarioKind != "redirect" {
		t.Fatalf("scenario %+v", d)
	}
	if d.MatchedScenarioRuleID != "bots-redirect" {
		t.Fatalf("rule %q", d.MatchedScenarioRuleID)
	}
	if len(d.Slots) != 1 || d.Slots[0].VariantID != "disabled_target" {
		t.Fatalf("slots %+v", d.Slots)
	}
	if d.Slots[0].HandlerVersion != trafficwire.HandlerRedirectTarget {
		t.Fatalf("handler %q", d.Slots[0].HandlerVersion)
	}
	pl, err := trafficwire.ParseRedirect(d.Slots[0].HandlerVersion, d.Slots[0].SchemaVersion, d.Slots[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Enabled || pl.URL != "" || pl.TargetID != "disabled_target" {
		t.Fatalf("disabled target %+v", pl)
	}
}

func TestRULandingRule(t *testing.T) {
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{IsBot: botFalse(), Country: "RU", SubjectID: subject("subj")})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "landing" || kind != "render" || rule != "ru-landing-only" {
		t.Fatalf("sid=%s kind=%s rule=%s", sid, kind, rule)
	}
}

func TestBotWinsOverCountry(t *testing.T) {
	sid, _, rule, err := loadPrepared(t).ResolveScenario(decision.Context{IsBot: botTrue(), Country: "RU", SubjectID: subject("subj")})
	if err != nil {
		t.Fatal(err)
	}
	if rule != "bots-redirect" || sid != "redirect" {
		t.Fatalf("first-match failed sid=%s rule=%s", sid, rule)
	}
}

func TestDefaultScenarioHashLanding(t *testing.T) {
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{
		IsBot:     botFalse(),
		Country:   "DE",
		SubjectID: subject("subj"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "landing" || kind != "render" || rule != "" {
		t.Fatalf("subj → sid=%s kind=%s rule=%s", sid, kind, rule)
	}
}

func TestDefaultScenarioHashRedirect(t *testing.T) {
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{
		IsBot:     botFalse(),
		Country:   "DE",
		SubjectID: subject("s1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "redirect" || kind != "redirect" || rule != "" {
		t.Fatalf("s1 → sid=%s kind=%s rule=%s", sid, kind, rule)
	}
}

func TestLandingSlotsUseHash(t *testing.T) {
	d, err := loadPrepared(t).Resolve(decision.Context{
		IsBot:     botFalse(),
		Country:   "RU",
		SubjectID: subject("subj"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ScenarioID != "landing" || d.MatchedScenarioRuleID != "ru-landing-only" {
		t.Fatalf("scenario %+v", d)
	}
	got := map[string]string{}
	for _, sl := range d.Slots {
		got[sl.SlotID] = sl.VariantID
	}
	if got["gallery"] != "home-sauna-swimwear" {
		t.Fatalf("gallery=%q want home-sauna-swimwear (uint16 point 3322)", got["gallery"])
	}
	if got["skin"] != "glass_right_form" {
		t.Fatalf("skin=%q want glass_right_form (uint16 point 2753)", got["skin"])
	}
	if got["handoff.from_landing"] != "disabled_dsp" {
		t.Fatalf("handoff=%q", got["handoff.from_landing"])
	}
}

func TestMissingSubjectIgnoresCountryRule(t *testing.T) {
	// Producer decision-vectors "subject-missing": country=RU, no SUBJECT_ID
	// → default missingAssignmentChoiceId (redirect), not ru-landing-only.
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{IsBot: botFalse(), Country: "RU"})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "redirect" || kind != "redirect" || rule != "" {
		t.Fatalf("missing SUBJECT_ID must beat country rule: sid=%s kind=%s rule=%s", sid, kind, rule)
	}
}

func TestDefaultMissingSubjectUsesFallback(t *testing.T) {
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{IsBot: botFalse(), Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "redirect" || kind != "redirect" || rule != "" {
		t.Fatalf("sid=%s kind=%s rule=%s", sid, kind, rule)
	}
}

func TestEmptyAssignmentIsHashedNotMissingFallback(t *testing.T) {
	empty := ""
	sid, kind, rule, err := loadPrepared(t).ResolveScenario(decision.Context{
		IsBot:     botFalse(),
		Country:   "DE",
		SubjectID: &empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "landing" || kind != "render" || rule != "" {
		t.Fatalf("empty assignment hashed → sid=%s kind=%s rule=%s (want landing, not missing fallback redirect)", sid, kind, rule)
	}
}

func TestMissingSubjectFallbackReasons(t *testing.T) {
	d, err := loadPrepared(t).Resolve(decision.Context{IsBot: botFalse(), Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if d.ScenarioID != "redirect" {
		t.Fatalf("scenario %s", d.ScenarioID)
	}
	if len(d.FallbackReasons) == 0 {
		t.Fatal("missing SUBJECT_ID must record fallbackReasons")
	}
	for _, r := range d.FallbackReasons {
		if r != "SUBJECT_ID_MISSING" {
			t.Fatalf("fallbackReasons %v", d.FallbackReasons)
		}
	}
}

func TestMobileMinimalMatchedRuleID(t *testing.T) {
	d, err := loadPrepared(t).Resolve(decision.Context{
		IsBot:      botFalse(),
		Country:    "RU",
		DeviceType: "mobile",
		SubjectID:  subject("subj"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.MatchedScenarioRuleID != "ru-landing-only" {
		t.Fatalf("scenario rule %q", d.MatchedScenarioRuleID)
	}
	got := map[string]decision.SlotDecision{}
	for _, sl := range d.Slots {
		got[sl.SlotID] = sl
	}
	skin, ok := got["skin"]
	if !ok {
		t.Fatalf("slots %+v", d.Slots)
	}
	if skin.MatchedRuleID != "mobile-minimal" {
		t.Fatalf("skin matchedRuleId %q", skin.MatchedRuleID)
	}
	if skin.VariantID != "minimal_center" {
		t.Fatalf("skin variant %q", skin.VariantID)
	}
}

func loadBundle(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func setDisabledTargetPayload(t *testing.T, b *runtimeloader.Bundle, raw string) {
	t.Helper()
	for i, v := range b.Program.Variants {
		if v.ID == "disabled_target" {
			b.Program.Variants[i].Payload = json.RawMessage(raw)
			return
		}
	}
	t.Fatal("disabled_target missing")
}

func setDisabledTargetV2(t *testing.T, b *runtimeloader.Bundle, raw string) {
	t.Helper()
	for i, v := range b.Program.Variants {
		if v.ID == "disabled_target" {
			b.Program.Variants[i].HandlerVersion = trafficwire.HandlerRedirectTargetV2
			b.Program.Variants[i].SchemaVersion = 2
			b.Program.Variants[i].Payload = json.RawMessage(raw)
			return
		}
	}
	t.Fatal("disabled_target missing")
}

func TestPrepareV1EnabledHasNoURL(t *testing.T) {
	b := loadBundle(t)
	setDisabledTargetPayload(t, b, `{"enabled":true,"targetId":"disabled_target"}`)
	if _, err := decision.Prepare(b); err == nil {
		t.Fatal("enabled redirect-target-v1 must fail prepare")
	}
}

func TestPrepareV1RejectsURLField(t *testing.T) {
	b := loadBundle(t)
	setDisabledTargetPayload(t, b, `{"enabled":true,"targetId":"disabled_target","url":"https://example.com/from-payload"}`)
	if _, err := decision.Prepare(b); err == nil {
		t.Fatal("url on redirect-target-v1 must be unknown")
	}
}

func TestPrepareV2EnabledMissingURLFails(t *testing.T) {
	b := loadBundle(t)
	setDisabledTargetV2(t, b, `{"enabled":true,"targetId":"disabled_target"}`)
	if _, err := decision.Prepare(b); err == nil {
		t.Fatal("enabled redirect-target-v2 without url must fail prepare")
	}
}

func TestPrepareV2AcceptsPayloadURL(t *testing.T) {
	b := loadBundle(t)
	setDisabledTargetV2(t, b, `{"enabled":true,"targetId":"disabled_target","url":"https://example.com/from-payload"}`)
	if _, err := decision.Prepare(b); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareV2RejectsHTTPPayloadURL(t *testing.T) {
	b := loadBundle(t)
	setDisabledTargetV2(t, b, `{"enabled":true,"targetId":"disabled_target","url":"http://example.com/"}`)
	if _, err := decision.Prepare(b); err == nil {
		t.Fatal("http url must fail prepare")
	}
}

func TestRedirectURLRequiresV2(t *testing.T) {
	if _, err := decision.RedirectURL(trafficwire.RedirectTarget{
		Handler:  trafficwire.HandlerRedirectTarget,
		Schema:   1,
		Enabled:  true,
		TargetID: "x",
		URL:      "https://example.com/from-payload",
	}); err == nil {
		t.Fatal("v1 must not yield a Location")
	}
	got, err := decision.RedirectURL(trafficwire.RedirectTarget{
		Handler:  trafficwire.HandlerRedirectTargetV2,
		Schema:   2,
		Enabled:  true,
		TargetID: "disabled_target",
		URL:      "https://example.com/from-payload",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/from-payload" {
		t.Fatalf("got %q", got)
	}
}

func TestRedirectURLDisabledHasNoLocation(t *testing.T) {
	if _, err := decision.RedirectURL(trafficwire.RedirectTarget{
		Handler:  trafficwire.HandlerRedirectTargetV2,
		Schema:   2,
		Enabled:  false,
		TargetID: "disabled_target",
	}); err == nil {
		t.Fatal("disabled target must not yield a URL")
	}
}
