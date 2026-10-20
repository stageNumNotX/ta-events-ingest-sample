package decision_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/qmacrocontract"
	"ta-events-ingest/internal/trafficwire"
)

func TestProducerQMacroCorpus(t *testing.T) {
	pkg, err := qmacrocontract.Open(qmacrocontract.DefaultDir())
	if errors.Is(err, qmacrocontract.ErrAbsent) {
		t.Fatalf("Q-MACRO-CONTRACT package absent at %s (set %s or vendor %s@%s %s). oracleDigest=%s. Local fixtures do not close this gate.",
			qmacrocontract.DefaultDir(), qmacrocontract.EnvDir, qmacrocontract.ProducerRepo, qmacrocontract.ProducerCommitPrefix, qmacrocontract.EvidencePath, qmacrocontract.OracleDigest)
	}
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Digest != qmacrocontract.OracleDigest {
		t.Fatalf("package digest %s want %s", pkg.Digest, qmacrocontract.OracleDigest)
	}
	raw, err := pkg.ReadVerified(pkg.OracleFile)
	if err != nil {
		t.Fatal(err)
	}
	kind, err := runQMacroOracle(t, pkg, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Q-MACRO-CONTRACT: %s from %s @ oracleDigest=%s producer=%s", kind, pkg.OracleFile, qmacrocontract.OracleDigest, qmacrocontract.ProducerCommitPrefix)
}

func runQMacroOracle(t *testing.T, pkg *qmacrocontract.Package, raw []byte) (string, error) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return "", fmt.Errorf("%s: not a JSON object: %w", pkg.OracleFile, err)
	}
	if d, ok := top["oracleDigest"]; ok {
		var got string
		if json.Unmarshal(d, &got) == nil && got != "" && !strings.EqualFold(got, qmacrocontract.OracleDigest) {
			return "", fmt.Errorf("%s: embedded oracleDigest %s want %s", pkg.OracleFile, got, qmacrocontract.OracleDigest)
		}
	}
	casesRaw, ok := top["cases"]
	if !ok {
		if v, ok2 := top["vectors"]; ok2 {
			casesRaw = v
		} else {
			return "", fmt.Errorf("%s: need cases[] or vectors[]; keys=%v", pkg.OracleFile, topKeys(raw))
		}
	}
	var cases []json.RawMessage
	if err := json.Unmarshal(casesRaw, &cases); err != nil {
		return "", fmt.Errorf("%s: cases: %w", pkg.OracleFile, err)
	}
	if len(cases) == 0 {
		return "", fmt.Errorf("%s: cases[] empty", pkg.OracleFile)
	}

	matcherN, decisionN := 0, 0
	for i, c := range cases {
		var probe struct {
			When     json.RawMessage `json:"when"`
			Matcher  json.RawMessage `json:"matcher"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(c, &probe); err != nil {
			return "", fmt.Errorf("cases[%d]: %w", i, err)
		}
		if len(probe.When) > 0 || len(probe.Matcher) > 0 {
			matcherN++
		}
		if looksLikeDecisionExpected(probe.Expected) {
			decisionN++
		}
	}
	if matcherN > 0 && decisionN > 0 {
		return "", fmt.Errorf("%s: mixed matcher and decision cases (%d matcher, %d decision); producer package must be one oracle shape", pkg.OracleFile, matcherN, decisionN)
	}
	if matcherN == len(cases) {
		if err := runQMacroMatcherCases(t, cases); err != nil {
			return "", err
		}
		return fmt.Sprintf("matcher-vectors n=%d", len(cases)), nil
	}
	_, hasPolicy := top["policyId"]
	if hasPolicy || decisionN > 0 {
		var f producerDecisionFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return "", fmt.Errorf("%s: decision corpus: %w", pkg.OracleFile, err)
		}
		if f.PolicyID == "" {
			return "", fmt.Errorf("%s: decision cases need top-level policyId to bind artifacts", pkg.OracleFile)
		}
		p := prepareHandoffBundle(t, pkg, raw)
		for i, c := range f.Cases {
			name := c.Name
			if name == "" {
				name = fmt.Sprintf("cases[%d]", i)
			}
			ctx, err := contextFromProducer(name, c)
			if err != nil {
				return "", err
			}
			compareProducerCase(t, p, name, ctx, c.Expected)
		}
		return fmt.Sprintf("decision-vectors n=%d policyId=%s", len(f.Cases), f.PolicyID), nil
	}
	return "", fmt.Errorf("%s: unknown vector shape keys=%v matcherN=%d decisionN=%d", pkg.OracleFile, topKeys(raw), matcherN, decisionN)
}

func looksLikeDecisionExpected(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	_, sel := obj["selections"]
	_, sc := obj["scenarioId"]
	_, fb := obj["fallbackReasons"]
	return sel || sc || fb
}

func contextFromProducer(name string, c producerCase) (decision.Context, error) {
	var pc producerContext
	dec := json.NewDecoder(bytes.NewReader(c.Context))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pc); err != nil {
		return decision.Context{}, fmt.Errorf("%s: context has fields this consumer does not model: %v (%s)", name, err, c.Context)
	}
	if c.AssignmentPresent == nil {
		return decision.Context{}, fmt.Errorf("%s: assignmentPresent missing", name)
	}
	ctx := decision.Context{IsBot: pc.IsBot, Country: pc.Country, DeviceType: pc.DeviceType, TASource: pc.TASource, TASub: pc.TASub}
	if *c.AssignmentPresent {
		if c.AssignmentValue == nil {
			return decision.Context{}, fmt.Errorf("%s: assignmentPresent=true but assignmentValue missing", name)
		}
		v := *c.AssignmentValue
		ctx.SubjectID = &v
	} else if c.AssignmentValue != nil && *c.AssignmentValue != "" {
		return decision.Context{}, fmt.Errorf("%s: assignmentPresent=false with non-empty assignmentValue %q", name, *c.AssignmentValue)
	}
	return ctx, nil
}

type qmacroMatcherCase struct {
	Name              string          `json:"name"`
	When              json.RawMessage `json:"when"`
	Matcher           json.RawMessage `json:"matcher"`
	Context           json.RawMessage `json:"context"`
	Expected          json.RawMessage `json:"expected"`
	ExpectedMatch     *bool           `json:"expectedMatch"`
	AssignmentPresent *bool           `json:"assignmentPresent"`
	AssignmentValue   *string         `json:"assignmentValue"`
}

func runQMacroMatcherCases(t *testing.T, cases []json.RawMessage) error {
	t.Helper()
	for i, raw := range cases {
		var c qmacroMatcherCase
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("cases[%d]: %w", i, err)
		}
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("cases[%d]", i)
		}
		whenRaw := c.When
		if len(whenRaw) == 0 {
			whenRaw = c.Matcher
		}
		if len(whenRaw) == 0 {
			return fmt.Errorf("%s: matcher/when missing", name)
		}
		var when trafficwire.Matcher
		if err := json.Unmarshal(whenRaw, &when); err != nil {
			return fmt.Errorf("%s: matcher: %w", name, err)
		}
		want, err := matcherExpected(c)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		ctx, err := matcherContext(name, c)
		if err != nil {
			return err
		}
		got, err := decision.EvalMatcher(when, ctx)
		if err != nil {
			return fmt.Errorf("%s: EvalMatcher: %w", name, err)
		}
		if got != want {
			return fmt.Errorf("%s: match=%v want %v", name, got, want)
		}
	}
	return nil
}

func matcherExpected(c qmacroMatcherCase) (bool, error) {
	if c.ExpectedMatch != nil {
		return *c.ExpectedMatch, nil
	}
	if len(c.Expected) == 0 {
		return false, fmt.Errorf("expected/expectedMatch missing")
	}
	var b bool
	if json.Unmarshal(c.Expected, &b) == nil {
		return b, nil
	}
	var obj struct {
		Match *bool `json:"match"`
	}
	if json.Unmarshal(c.Expected, &obj) == nil && obj.Match != nil {
		return *obj.Match, nil
	}
	return false, fmt.Errorf("expected is not a boolean match")
}

func matcherContext(name string, c qmacroMatcherCase) (decision.Context, error) {
	ctxRaw := c.Context
	if len(bytes.TrimSpace(ctxRaw)) == 0 {
		ctxRaw = []byte(`{}`)
	}
	present := c.AssignmentPresent
	val := c.AssignmentValue
	if present == nil {
		p := true
		v := "q-macro-subject"
		present, val = &p, &v
	}
	return contextFromProducer(name, producerCase{
		Name:              name,
		Context:           ctxRaw,
		AssignmentPresent: present,
		AssignmentValue:   val,
	})
}

func TestProducerContextJSONEmptyStringIsPresent(t *testing.T) {
	assign := true
	subj := "q-macro-subject"
	ctx, err := contextFromProducer("empty-present", producerCase{
		Name:              "empty-present",
		Context:           json.RawMessage(`{"ta_source":"","ta_sub":""}`),
		AssignmentPresent: &assign,
		AssignmentValue:   &subj,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.TASource == nil || *ctx.TASource != "" {
		t.Fatalf("JSON ta_source=\"\" must be present-empty, got %+v", ctx.TASource)
	}
	if ctx.TASub == nil || *ctx.TASub != "" {
		t.Fatalf("JSON ta_sub=\"\" must be present-empty, got %+v", ctx.TASub)
	}
	exists := trafficwire.Matcher{Op: trafficwire.OpCondition, Field: "ta_source", Operator: trafficwire.OperatorEXISTS}
	got, err := decision.EvalMatcher(exists, ctx)
	if err != nil || !got {
		t.Fatalf("EXISTS on JSON present-empty: %v %v", got, err)
	}

	missing, err := contextFromProducer("omitted", producerCase{
		Name:              "omitted",
		Context:           json.RawMessage(`{}`),
		AssignmentPresent: &assign,
		AssignmentValue:   &subj,
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing.TASource != nil || missing.TASub != nil {
		t.Fatalf("omitted keys must be missing: %+v", missing)
	}
	got, err = decision.EvalMatcher(exists, missing)
	if err != nil || got {
		t.Fatalf("EXISTS on omitted: %v %v", got, err)
	}
}
