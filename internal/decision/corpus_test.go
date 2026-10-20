package decision_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/producerhandoff"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

// Producer file (compiler-service@297d8d0c decision-vectors.json): top-level
// cases[] with context, assignmentPresent/assignmentValue and
// expected.selections[] + expected.fallbackReasons. Cases run against the
// release/program/weights artifacts shipped in the same handoff package
// (found by artifactType, bound by release sha256), never against the old
// v1 pin in runtimeloader/testdata. Unknown shape fails; only an absent
// package skips.
type producerContext struct {
	IsBot      *bool   `json:"isBot"`
	Country    string  `json:"country"`
	DeviceType string  `json:"deviceType"`
	TASource   *string `json:"ta_source"` // nil = omitted; &"" = present-empty
	TASub      *string `json:"ta_sub"`
}

type producerSelection struct {
	SlotID        string `json:"slotId"`
	VariantID     string `json:"variantId"`
	MatchedRuleID string `json:"matchedRuleId"`
}

type producerExpected struct {
	ScenarioID            string            `json:"scenarioId"`
	MatchedScenarioRuleID json.RawMessage   `json:"matchedScenarioRuleId"`
	Selections            []json.RawMessage `json:"selections"`
	FallbackReasons       []string          `json:"fallbackReasons"`
}

type producerCase struct {
	Name              string           `json:"name"`
	Context           json.RawMessage  `json:"context"`
	AssignmentPresent *bool            `json:"assignmentPresent"`
	AssignmentValue   *string          `json:"assignmentValue"`
	Expected          producerExpected `json:"expected"`
}

type producerDecisionFile struct {
	PolicyID string         `json:"policyId"`
	Cases    []producerCase `json:"cases"`
}

func TestProducerDecisionCorpus(t *testing.T) {
	pkg, err := producerhandoff.Open(producerhandoff.DefaultDir())
	if errors.Is(err, producerhandoff.ErrAbsent) {
		t.Skipf("T2 decision corpus NOT RUN: %v (set %s or vendor the package)", err, producerhandoff.EnvDir)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pkg.ReadVerified(producerhandoff.DecisionVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var f producerDecisionFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", producerhandoff.DecisionVectorsFile, err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s: cases[] empty or absent; top-level keys=%v", producerhandoff.DecisionVectorsFile, topKeys(raw))
	}
	if f.PolicyID == "" {
		t.Fatalf("%s: top-level policyId required to bind the oracle; keys=%v", producerhandoff.DecisionVectorsFile, topKeys(raw))
	}
	p := prepareHandoffBundle(t, pkg, raw)

	for i, c := range f.Cases {
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("cases[%d]", i)
		}
		var pc producerContext
		dec := json.NewDecoder(bytes.NewReader(c.Context))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&pc); err != nil {
			t.Fatalf("%s: context has fields this consumer does not model: %v (%s)", name, err, c.Context)
		}
		if c.AssignmentPresent == nil {
			t.Fatalf("%s: assignmentPresent missing", name)
		}
		ctx := decision.Context{IsBot: pc.IsBot, Country: pc.Country, DeviceType: pc.DeviceType, TASource: pc.TASource, TASub: pc.TASub}
		if *c.AssignmentPresent {
			if c.AssignmentValue == nil {
				t.Fatalf("%s: assignmentPresent=true but assignmentValue missing", name)
			}
			v := *c.AssignmentValue
			ctx.SubjectID = &v
		} else if c.AssignmentValue != nil && *c.AssignmentValue != "" {
			t.Fatalf("%s: assignmentPresent=false with non-empty assignmentValue %q", name, *c.AssignmentValue)
		}

		compareProducerCase(t, p, name, ctx, c.Expected)
	}
	t.Logf("T2 decision corpus: %d cases from %s @ %s", len(f.Cases), producerhandoff.DecisionVectorsFile, producerhandoff.ProducerSHA)
}

func compareProducerCase(t *testing.T, p *decision.Prepared, name string, ctx decision.Context, exp producerExpected) {
	t.Helper()
	d, err := p.Resolve(ctx)
	if err != nil {
		t.Fatalf("%s: Resolve: %v", name, err)
	}
	if exp.ScenarioID != "" && d.ScenarioID != exp.ScenarioID {
		t.Fatalf("%s: scenario %q want %q", name, d.ScenarioID, exp.ScenarioID)
	}
	compareOptionalRuleID(t, name, "matchedScenarioRuleId", d.MatchedScenarioRuleID, exp.MatchedScenarioRuleID)
	if len(exp.Selections) == 0 && len(exp.FallbackReasons) == 0 && exp.ScenarioID == "" {
		t.Fatalf("%s: expected carries nothing this consumer compares: %+v", name, exp)
	}
	got := map[string]decision.SlotDecision{}
	for _, sl := range d.Slots {
		got[sl.SlotID] = sl
	}
	for _, selRaw := range exp.Selections {
		var sel producerSelection
		sdec := json.NewDecoder(bytes.NewReader(selRaw))
		sdec.DisallowUnknownFields()
		if err := sdec.Decode(&sel); err != nil {
			t.Fatalf("%s: selection shape not modelled (%v): %s", name, err, selRaw)
		}
		if sel.SlotID == "" || sel.VariantID == "" {
			t.Fatalf("%s: selection needs slotId and variantId: %s", name, selRaw)
		}
		sl, ok := got[sel.SlotID]
		if !ok || sl.VariantID != sel.VariantID {
			t.Fatalf("%s: slot %s = %q want %q", name, sel.SlotID, got[sel.SlotID].VariantID, sel.VariantID)
		}
		if sl.MatchedRuleID != sel.MatchedRuleID {
			t.Fatalf("%s: slot %s matchedRuleId %q want %q", name, sel.SlotID, sl.MatchedRuleID, sel.MatchedRuleID)
		}
	}
	if len(exp.Selections) != len(d.Slots) {
		t.Fatalf("%s: %d selections expected, resolver produced %d slots", name, len(exp.Selections), len(d.Slots))
	}
	if len(exp.FallbackReasons) != len(d.FallbackReasons) {
		t.Fatalf("%s: fallbackReasons got %v want %v", name, d.FallbackReasons, exp.FallbackReasons)
	}
	for i, want := range exp.FallbackReasons {
		if d.FallbackReasons[i] != want {
			t.Fatalf("%s: fallbackReasons got %v want %v", name, d.FallbackReasons, exp.FallbackReasons)
		}
	}
}

func compareOptionalRuleID(t *testing.T, name, field, got string, raw json.RawMessage) {
	t.Helper()
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	if string(raw) == "null" {
		if got != "" {
			t.Fatalf("%s: %s %q want null", name, field, got)
		}
		return
	}
	var want string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("%s: %s: %v (%s)", name, field, err, raw)
	}
	if got != want {
		t.Fatalf("%s: %s %q want %q", name, field, got, want)
	}
}

// prepareHandoffBundle finds release/program/weights artifacts inside the
// handoff package by artifactType, binds the decision corpus to the Program
// whose policyId equals the decision file's top-level policyId, then
// verifies release→program/weights sha256 (runtimeloader.Verify).
type verifiedTree interface {
	Location() string
	FilePaths() []string
	ReadVerified(nameOrPath string) ([]byte, error)
}

func prepareHandoffBundle(t *testing.T, pkg verifiedTree, decisionRaw []byte) *decision.Prepared {
	t.Helper()
	p, releasePath, err := bindHandoffBundle(pkg, decisionRaw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("handoff bundle: release=%s", releasePath)
	return p
}

func bindHandoffBundle(pkg verifiedTree, decisionRaw []byte) (*decision.Prepared, string, error) {
	var head struct {
		PolicyID string `json:"policyId"`
	}
	if err := json.Unmarshal(decisionRaw, &head); err != nil {
		return nil, "", fmt.Errorf("%s: %w", producerhandoff.DecisionVectorsFile, err)
	}
	if head.PolicyID == "" {
		return nil, "", fmt.Errorf("%s: top-level policyId required to bind among release sets", producerhandoff.DecisionVectorsFile)
	}

	type artifact struct {
		name     string
		raw      []byte
		sha      string
		policyID string
	}
	var releases []artifact
	programs := map[string]artifact{}
	weights := map[string]artifact{}
	for _, relPath := range pkg.FilePaths() {
		base := filepath.Base(relPath)
		switch base {
		case producerhandoff.ManifestFile, producerhandoff.ManifestSHAFile, producerhandoff.HashVectorsFile, producerhandoff.DecisionVectorsFile:
			continue
		}
		if !strings.HasSuffix(relPath, ".json") {
			continue
		}
		raw, err := pkg.ReadVerified(relPath)
		if err != nil {
			return nil, "", err
		}
		var meta struct {
			ArtifactType string `json:"artifactType"`
			PolicyID     string `json:"policyId"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.ArtifactType == "" {
			continue
		}
		sum := sha256.Sum256(raw)
		a := artifact{name: relPath, raw: raw, sha: hex.EncodeToString(sum[:]), policyID: meta.PolicyID}
		// Package files often keep a trailing "\n" (manifest.files digest), while
		// release.program/weights.sha256 pin newline-stripped canonical bytes.
		index := func(m map[string]artifact, art artifact) {
			m[art.sha] = art
			if len(art.raw) > 0 && art.raw[len(art.raw)-1] == '\n' {
				stripped := art.raw[:len(art.raw)-1]
				s := sha256.Sum256(stripped)
				canon := artifact{name: art.name, raw: stripped, sha: hex.EncodeToString(s[:]), policyID: art.policyID}
				m[canon.sha] = canon
			}
		}
		switch meta.ArtifactType {
		case trafficwire.ArtifactRelease:
			releases = append(releases, a)
		case trafficwire.ArtifactProgram:
			index(programs, a)
		case trafficwire.ArtifactWeights:
			index(weights, a)
		}
	}
	if len(releases) == 0 {
		return nil, "", fmt.Errorf("handoff %s: no %s artifact; decision cases need the package's own release/program/weights", pkg.Location(), trafficwire.ArtifactRelease)
	}

	type cand struct {
		rel  artifact
		prog artifact
		w    artifact
	}
	var matched []cand
	for _, relArt := range releases {
		var rel trafficwire.Release
		if err := json.Unmarshal(relArt.raw, &rel); err != nil {
			return nil, "", fmt.Errorf("%s: %w", relArt.name, err)
		}
		prog, ok := programs[strings.ToLower(rel.Program.SHA256)]
		if !ok {
			continue
		}
		if prog.policyID != head.PolicyID {
			continue
		}
		w, ok := weights[strings.ToLower(rel.Weights.SHA256)]
		if !ok {
			return nil, "", fmt.Errorf("handoff %s: release %s weights sha256 %s not present as an artifact file", pkg.Location(), relArt.name, rel.Weights.SHA256)
		}
		matched = append(matched, cand{rel: relArt, prog: prog, w: w})
	}
	if len(matched) == 0 {
		names := make([]string, 0, len(releases))
		for _, r := range releases {
			names = append(names, r.name)
		}
		return nil, "", fmt.Errorf("handoff %s: no release/program with policyId %q (releases %v)", pkg.Location(), head.PolicyID, names)
	}
	if len(matched) > 1 {
		names := make([]string, 0, len(matched))
		for _, c := range matched {
			names = append(names, c.rel.name)
		}
		return nil, "", fmt.Errorf("handoff %s: policyId %q matches several releases %v", pkg.Location(), head.PolicyID, names)
	}
	chosen := matched[0]
	b, err := runtimeloader.Verify(chosen.rel.raw, chosen.prog.raw, chosen.w.raw)
	if err != nil {
		return nil, "", fmt.Errorf("handoff release %s: %w", chosen.rel.name, err)
	}
	p, err := decision.Prepare(b)
	if err != nil {
		return nil, "", fmt.Errorf("handoff release %s: Prepare: %w", chosen.rel.name, err)
	}
	return p, chosen.rel.name, nil
}

func topKeys(raw []byte) []string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	out := make([]string, 0, len(obj))
	for k := range obj {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
