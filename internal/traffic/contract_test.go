package traffic_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/traffic"
	"ta-events-ingest/internal/trafficwire"
)

func TestContractT6Probe(t *testing.T) {
	b, err := runtimeloader.LoadDir(filepath.Join("..", "runtimeloader", "testdata", "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := traffic.NewContract(b, traffic.ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Status("prototype-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if st.CompileTarget != trafficwire.CompileTarget {
		t.Fatalf("compileTarget %q", st.CompileTarget)
	}
	if st.T6.ScenarioID != "redirect" || st.T6.MatchedScenarioRuleID != "bots-redirect" {
		t.Fatalf("t6 %+v", st.T6)
	}
	if st.T6.VariantID != "disabled_target" || st.T6.Enabled || st.T6.HasURL {
		t.Fatalf("disabled target %+v", st.T6)
	}
	if st.Request.ContextSource != traffic.PrototypeContextSource {
		t.Fatalf("request %+v", st.Request)
	}
	if st.LiveRedirectFromThisRelease {
		t.Fatal("pinned release must not be used as live 302 target")
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatal(err)
	}
}
