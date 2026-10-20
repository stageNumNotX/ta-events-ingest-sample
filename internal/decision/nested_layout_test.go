package decision_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/producerhandoff"
	"ta-events-ingest/internal/trafficwire"
)

const pinPolicyID = "ta-traffic-bootstrap-v1"
const smokePolicyID = "ta-traffic-smoke-redirect-v2"

type handoffFile struct {
	path string
	body []byte
}

func mustReadPin(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "runtimeloader", "testdata", "pinned", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func retargetRelease(t *testing.T, releaseRaw, newProg []byte) []byte {
	t.Helper()
	var rel trafficwire.Release
	if err := json.Unmarshal(releaseRaw, &rel); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(newProg)
	rel.Program.SHA256 = hex.EncodeToString(sum[:])
	rel.Program.Bytes = len(newProg)
	out, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func twoReleaseHandoffFiles(t *testing.T, decisionBody []byte) []handoffFile {
	t.Helper()
	prog := mustReadPin(t, "program.json")
	weights := mustReadPin(t, "weights.json")
	release := mustReadPin(t, "release.json")
	old := []byte(`"policyId":"` + pinPolicyID + `"`)
	if !bytes.Contains(prog, old) {
		t.Fatalf("pinned program missing %s", pinPolicyID)
	}
	smokeProg := bytes.Replace(prog, old, []byte(`"policyId":"`+smokePolicyID+`"`), 1)
	smokeRel := retargetRelease(t, release, smokeProg)
	return []handoffFile{
		{path: "testdata/traffic-contract/v1/" + producerhandoff.HashVectorsFile, body: []byte(`[{"bodyHex":"00"}]`)},
		{path: "testdata/traffic-contract/v1/" + producerhandoff.DecisionVectorsFile, body: decisionBody},
		{path: "valid/bootstrap/program.json", body: prog},
		{path: "valid/bootstrap/weights.json", body: weights},
		{path: "valid/bootstrap/release.json", body: release},
		{path: "valid/smoke-redirect-v2/program.json", body: smokeProg},
		{path: "valid/smoke-redirect-v2/weights.json", body: weights},
		{path: "valid/smoke-redirect-v2/release.json", body: smokeRel},
	}
}

func writeHandoffPackage(t *testing.T, files []handoffFile) string {
	t.Helper()
	dir := t.TempDir()
	manFiles := map[string]any{}
	for _, f := range files {
		sum := sha256.Sum256(f.body)
		manFiles[f.path] = map[string]string{"sha256": hex.EncodeToString(sum[:])}
		abs := filepath.Join(dir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, f.body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	man := map[string]any{
		"producerSha": "10b0da8",
		"root":        "testdata/traffic-contract/v1",
		"files":       manFiles,
	}
	raw, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, producerhandoff.ManifestFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	t.Setenv(producerhandoff.EnvManifestSHA, hex.EncodeToString(sum[:]))
	return dir
}

// Nested layout matching the CONTRACT_READY package (PR comments 5625730572
// and 5626365354). Uses the existing v1 pin as bootstrap and a policyId-mutated
// copy as smoke-redirect-v2 so the harness can be proven without claiming T2
// oracle parity. Not a producer export; do not vendor.
func TestHandoffNestedLayoutIsNotFlattened(t *testing.T) {
	decisionBody := []byte(`{"policyId":"` + pinPolicyID + `","cases":[]}`)
	dir := writeHandoffPackage(t, twoReleaseHandoffFiles(t, decisionBody))
	if err := os.WriteFile(filepath.Join(dir, producerhandoff.HashVectorsFile), []byte(`[{"id":"decoy"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := producerhandoff.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hv, err := pkg.ReadVerified(producerhandoff.HashVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(hv) != `[{"bodyHex":"00"}]` {
		t.Fatalf("used flattened decoy or wrong path: %s", hv)
	}
	dv, err := pkg.ReadVerified(producerhandoff.DecisionVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	p, releasePath, err := bindHandoffBundle(pkg, dv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(releasePath, "valid/bootstrap/") {
		t.Fatalf("bound %s, want valid/bootstrap", releasePath)
	}
	if strings.Contains(releasePath, "smoke-redirect-v2") {
		t.Fatalf("bound smoke set: %s", releasePath)
	}

	falseBot := false
	compareProducerCase(t, p, "subject-missing", decision.Context{IsBot: &falseBot, Country: "DE"}, producerExpected{
		ScenarioID: "redirect",
		Selections: []json.RawMessage{
			json.RawMessage(`{"slotId":"target","variantId":"disabled_target","matchedRuleId":""}`),
		},
		FallbackReasons: []string{"SUBJECT_ID_MISSING", "SUBJECT_ID_MISSING"},
	})

	subj := "subj"
	compareProducerCase(t, p, "mobile-minimal", decision.Context{
		IsBot:      &falseBot,
		Country:    "RU",
		DeviceType: "mobile",
		SubjectID:  &subj,
	}, producerExpected{
		ScenarioID:            "landing",
		MatchedScenarioRuleID: json.RawMessage(`"ru-landing-only"`),
		Selections: []json.RawMessage{
			json.RawMessage(`{"slotId":"gallery","variantId":"home-sauna-swimwear","matchedRuleId":""}`),
			json.RawMessage(`{"slotId":"handoff.from_landing","variantId":"disabled_dsp","matchedRuleId":""}`),
			json.RawMessage(`{"slotId":"handoff.from_mirror_1","variantId":"disabled_dsp","matchedRuleId":""}`),
			json.RawMessage(`{"slotId":"handoff.from_mirror_2","variantId":"disabled_dsp","matchedRuleId":""}`),
			json.RawMessage(`{"slotId":"skin","variantId":"minimal_center","matchedRuleId":"mobile-minimal"}`),
		},
	})
}

func TestPrepareHandoffBundleBindsByPolicyIDNotText(t *testing.T) {
	// Exact-package shape: two complete release sets; decision-vectors names
	// neither release sha256 nor layoutId, only policyId.
	decisionBody := []byte(`{"policyId":"` + pinPolicyID + `","cases":[]}`)
	if strings.Contains(string(decisionBody), "09dc2e6ab054242e") {
		t.Fatal("fixture must not embed layoutId")
	}
	dir := writeHandoffPackage(t, twoReleaseHandoffFiles(t, decisionBody))
	pkg, err := producerhandoff.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	dv, err := pkg.ReadVerified(producerhandoff.DecisionVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	releaseRaw := mustReadPin(t, "release.json")
	sum := sha256.Sum256(releaseRaw)
	if bytes.Contains(dv, []byte(hex.EncodeToString(sum[:]))) {
		t.Fatal("fixture must not embed release sha256")
	}
	_, releasePath, err := bindHandoffBundle(pkg, dv)
	if err != nil {
		t.Fatal(err)
	}
	if releasePath != "valid/bootstrap/release.json" {
		t.Fatalf("got %s", releasePath)
	}

	if _, _, err := bindHandoffBundle(pkg, []byte(`{"cases":[]}`)); err == nil {
		t.Fatal("missing policyId must fail")
	}
	if _, _, err := bindHandoffBundle(pkg, []byte(`{"policyId":"no-such-policy","cases":[]}`)); err == nil {
		t.Fatal("unknown policyId must fail")
	}
}
