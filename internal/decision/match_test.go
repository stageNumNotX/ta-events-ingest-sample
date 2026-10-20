package decision_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

const (
	matchDefaultURL = "https://example.com/?campaign=default-choice"
	matchSourceURL  = "https://example.com/?campaign=source-network-a"
	matchSubURL     = "https://example.com/?campaign=sub-tagged"
	matchAllURL     = "https://example.com/?campaign=all-source-sub"
	matchDefaultID  = "match-default"
	matchSourceID   = "match-source"
	matchSubID      = "match-sub"
	matchAllID      = "match-all"
)

func matchBaseBundle(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b := loadBundle(t)
	b.Program.Variants = append(append([]trafficwire.Variant(nil), b.Program.Variants...),
		v2Variant(matchDefaultID, matchDefaultURL),
		v2Variant(matchSourceID, matchSourceURL),
		v2Variant(matchSubID, matchSubURL),
		v2Variant(matchAllID, matchAllURL),
	)
	b.Program.Vectors = append([]trafficwire.Vector(nil), b.Program.Vectors...)
	for i, vec := range b.Program.Vectors {
		switch vec.DistributionKey {
		case "redirect.target.default":
			b.Program.Vectors[i].ChoiceIDs = []string{matchDefaultID}
		case "scenario.default":
			b.Program.Vectors[i].ChoiceIDs = []string{"redirect"}
		}
	}
	b.Program.SlotPrograms = append([]trafficwire.SlotProgram(nil), b.Program.SlotPrograms...)
	return b
}

func present(s string) *string { return &s }

func v2Variant(id, url string) trafficwire.Variant {
	return trafficwire.Variant{
		ID:             id,
		TypeID:         trafficwire.TypeRedirectTarget,
		SchemaVersion:  2,
		HandlerVersion: trafficwire.HandlerRedirectTargetV2,
		Payload:        json.RawMessage(`{"enabled":true,"targetId":"` + id + `","url":"` + url + `"}`),
	}
}

func prependRedirectRule(t *testing.T, b *runtimeloader.Bundle, rule trafficwire.Rule, vec trafficwire.Vector) {
	t.Helper()
	b.Program.Vectors = append(b.Program.Vectors, vec)
	rule.Branch.VectorIndex = len(b.Program.Vectors) - 1
	for i, sp := range b.Program.SlotPrograms {
		if sp.ScenarioID != "redirect" || sp.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		rules := append([]trafficwire.Rule{rule}, append([]trafficwire.Rule(nil), sp.Program.Rules...)...)
		b.Program.SlotPrograms[i].Program.Rules = rules
		return
	}
	t.Fatal("redirect slot missing")
}

func cond(field, op string, value, values json.RawMessage) trafficwire.Matcher {
	return trafficwire.Matcher{Op: trafficwire.OpCondition, Field: field, Operator: op, Value: value, Values: values}
}

func resolveMatchVariant(t *testing.T, b *runtimeloader.Bundle, ctx decision.Context) string {
	t.Helper()
	p, err := decision.Prepare(b)
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sl := range d.Slots {
		if sl.TypeID == trafficwire.TypeRedirectTarget {
			return sl.VariantID
		}
	}
	t.Fatalf("no redirect slot in %+v", d)
	return ""
}

func TestMatchTASourceEQ(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "source-eq",
		When:   cond("ta_source", trafficwire.OperatorEQ, json.RawMessage(`"network-a"`), nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.source-eq", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})

	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASource: present("network-a")}); got != matchSourceID {
		t.Fatalf("present EQ got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj}); got != matchDefaultID {
		t.Fatalf("absent got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASource: present("other")}); got != matchDefaultID {
		t.Fatalf("other got %s", got)
	}
}

func TestMatchTASubINAndEXISTS(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "sub-in",
		When:   cond("ta_sub", trafficwire.OperatorIN, nil, json.RawMessage(`["campaign-42","campaign-99"]`)),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.sub-in", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSubID}})
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "sub-exists",
		When:   cond("ta_sub", trafficwire.OperatorEXISTS, nil, nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.sub-exists", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})

	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASub: present("campaign-42")}); got != matchSourceID {
		t.Fatalf("EXISTS is first and must win for present sub, got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj}); got != matchDefaultID {
		t.Fatalf("missing EXISTS/IN got %s", got)
	}
}

func TestMatchTASubINOnly(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "sub-in",
		When:   cond("ta_sub", trafficwire.OperatorIN, nil, json.RawMessage(`["campaign-42","campaign-99"]`)),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.sub-in-only", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSubID}})
	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASub: present("campaign-99")}); got != matchSubID {
		t.Fatalf("IN hit got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASub: present("nope")}); got != matchDefaultID {
		t.Fatalf("IN miss got %s", got)
	}
}

func TestMatchALLSourceAndSub(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "all-source-sub",
		When: trafficwire.Matcher{
			Op: trafficwire.OpALL,
			Rules: []trafficwire.Matcher{
				cond("ta_source", trafficwire.OperatorEQ, json.RawMessage(`"network-a"`), nil),
				cond("ta_sub", trafficwire.OperatorEQ, json.RawMessage(`"campaign-42"`), nil),
			},
		},
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.all", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchAllID}})
	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASource: present("network-a"), TASub: present("campaign-42")}); got != matchAllID {
		t.Fatalf("ALL hit got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASource: present("network-a")}); got != matchDefaultID {
		t.Fatalf("ALL missing sub got %s", got)
	}
}

func TestMatchNOTExists(t *testing.T) {
	b := matchBaseBundle(t)
	inner := cond("ta_source", trafficwire.OperatorEXISTS, nil, nil)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "not-source",
		When:   trafficwire.Matcher{Op: trafficwire.OpNOT, Rule: &inner},
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.not-source", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})
	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj}); got != matchSourceID {
		t.Fatalf("NOT EXISTS when missing got %s", got)
	}
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj, TASource: present("x")}); got != matchDefaultID {
		t.Fatalf("NOT EXISTS when present got %s", got)
	}
}

func TestPrepareRejectsUnsupportedFieldAndOperator(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "m-campaign",
		When:   cond("m_campaign", trafficwire.OperatorEQ, json.RawMessage(`"x"`), nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.bad-field", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})
	if _, err := decision.Prepare(b); err == nil || !strings.Contains(err.Error(), "unsupported field") {
		t.Fatalf("m_campaign: %v", err)
	}

	b = matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "gt",
		When:   cond("ta_source", "GT", json.RawMessage(`"x"`), nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.gt", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})
	if _, err := decision.Prepare(b); err == nil {
		t.Fatal("GT must fail load")
	}

	b = matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "bot-exists",
		When:   cond("isBot", trafficwire.OperatorEXISTS, nil, nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.bot-exists", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})
	if _, err := decision.Prepare(b); err == nil || !strings.Contains(err.Error(), "unsupported operator") {
		t.Fatalf("isBot EXISTS: %v", err)
	}
}

func TestPrepareRejectsRequestIDAssignment(t *testing.T) {
	b := matchBaseBundle(t)
	b.Program.ScenarioProgram.AssignmentKey = trafficwire.AssignmentRequestID
	if _, err := decision.Prepare(b); err == nil || !strings.Contains(err.Error(), "assignmentKey") {
		t.Fatalf("REQUEST_ID: %v", err)
	}
}

func TestPrepareRejectsCountryAsMCountryMasquerade(t *testing.T) {
	b := matchBaseBundle(t)
	prependRedirectRule(t, b, trafficwire.Rule{
		RuleID: "m-country",
		When:   cond("m_country", trafficwire.OperatorEQ, json.RawMessage(`"DE"`), nil),
	}, trafficwire.Vector{DistributionKey: "redirect.target.rule.m-country", ChoiceDomain: trafficwire.ChoiceDomainVariant, ChoiceIDs: []string{matchSourceID}})
	if _, err := decision.Prepare(b); err == nil || !strings.Contains(err.Error(), "unsupported field") {
		t.Fatalf("m_country: %v", err)
	}
}

func TestEvalMatcherTASourceEQIsNotProducerParity(t *testing.T) {
	when := cond("ta_source", trafficwire.OperatorEQ, json.RawMessage(`"network-a"`), nil)
	got, err := decision.EvalMatcher(when, decision.Context{TASource: present("network-a")})
	if err != nil || !got {
		t.Fatalf("local EQ hit: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(when, decision.Context{})
	if err != nil || got {
		t.Fatalf("local EQ miss: %v %v", got, err)
	}
	bad := cond("m_campaign", trafficwire.OperatorEQ, json.RawMessage(`"x"`), nil)
	if _, err := decision.EvalMatcher(bad, decision.Context{}); err == nil || !strings.Contains(err.Error(), "unsupported field") {
		t.Fatalf("m_campaign: %v", err)
	}
}

func TestMissingBotGeoStillExecutableDefault(t *testing.T) {
	b := matchBaseBundle(t)
	subj := "fixed-assignment"
	if got := resolveMatchVariant(t, b, decision.Context{SubjectID: &subj}); got != matchDefaultID {
		t.Fatalf("missing bot/geo got %s", got)
	}
}

func TestPresentEmptyTASourceIsOwnProperty(t *testing.T) {
	empty := ""
	exists := cond("ta_source", trafficwire.OperatorEXISTS, nil, nil)
	eqEmpty := cond("ta_source", trafficwire.OperatorEQ, json.RawMessage(`""`), nil)
	inEmpty := cond("ta_source", trafficwire.OperatorIN, nil, json.RawMessage(`[""]`))
	notExists := trafficwire.Matcher{Op: trafficwire.OpNOT, Rule: &exists}

	presentEmpty := decision.Context{TASource: &empty}
	missing := decision.Context{}

	got, err := decision.EvalMatcher(exists, presentEmpty)
	if err != nil || !got {
		t.Fatalf("EXISTS present-empty: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(exists, missing)
	if err != nil || got {
		t.Fatalf("EXISTS missing: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(eqEmpty, presentEmpty)
	if err != nil || !got {
		t.Fatalf("EQ \"\" present-empty: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(eqEmpty, missing)
	if err != nil || got {
		t.Fatalf("EQ \"\" missing: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(inEmpty, presentEmpty)
	if err != nil || !got {
		t.Fatalf("IN [\"\"] present-empty: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(inEmpty, missing)
	if err != nil || got {
		t.Fatalf("IN [\"\"] missing: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(notExists, presentEmpty)
	if err != nil || got {
		t.Fatalf("NOT EXISTS present-empty: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(notExists, missing)
	if err != nil || !got {
		t.Fatalf("NOT EXISTS missing: %v %v", got, err)
	}

	subEmpty := decision.Context{TASub: &empty}
	subExists := cond("ta_sub", trafficwire.OperatorEXISTS, nil, nil)
	got, err = decision.EvalMatcher(subExists, subEmpty)
	if err != nil || !got {
		t.Fatalf("ta_sub EXISTS present-empty: %v %v", got, err)
	}
	got, err = decision.EvalMatcher(subExists, missing)
	if err != nil || got {
		t.Fatalf("ta_sub EXISTS missing: %v %v", got, err)
	}
}
