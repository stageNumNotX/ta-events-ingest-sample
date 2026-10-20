package traffic

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func t11Event(click, name, source, sub, sha string) map[string]string {
	return map[string]string{
		"click_id":                click,
		"event_id":                click + "/" + name,
		"event_name":              name,
		"ta_source":               source,
		"ta_sub":                  sub,
		"contract_release_sha256": sha,
	}
}

func t11TripleJSON(t *testing.T, extra ...map[string]string) string {
	t.Helper()
	rows := []map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	rows = append(rows, extra...)
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func t11RepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func t11FakeGCP(t *testing.T, stdoutByBin map[string]string, exitByBin map[string]int) (pathDir, marker string) {
	t.Helper()
	pathDir = t.TempDir()
	marker = filepath.Join(pathDir, "gcp-called")
	bins := []string{"bq", "gcloud"}
	for _, name := range bins {
		stdout := "[]\n"
		if v, ok := stdoutByBin[name]; ok {
			stdout = v
		}
		code := 99
		if v, ok := exitByBin[name]; ok {
			code = v
		}
		outFile := filepath.Join(pathDir, name+".stdout")
		if err := os.WriteFile(outFile, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
		martOut := stdoutByBin["bq.mart"]
		martFile := filepath.Join(pathDir, "bq.mart.stdout")
		if name == "bq" && martOut != "" {
			if err := os.WriteFile(martFile, []byte(martOut), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		body := fmt.Sprintf("#!/bin/sh\nprintf 'called %s\\n' >> %q\nif [ %q = bq ] && printf '%%s' \"$*\" | grep -q mart_found && [ -f %q ]; then cat %q; else cat %q; fi\nexit %d\n",
			name, marker, name, martFile, martFile, outFile, code)
		if err := os.WriteFile(filepath.Join(pathDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return pathDir, marker
}

func t11RunScript(t *testing.T, script string, fakePath string, env []string) (string, error) {
	t.Helper()
	root := t11RepoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", script))
	cmd.Dir = root
	path := os.Getenv("PATH")
	if fakePath != "" {
		path = fakePath + string(os.PathListSeparator) + path
	}
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + os.Getenv("HOME")}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func t11IsolatedIDs() []string {
	return []string{
		"T11_TOPIC=ta-traffic-runtime-t11-events",
		"T11_SUBSCRIPTION=ta-traffic-runtime-t11-to-bq",
		"T11_DATASET=traffic_runtime_t11",
		"T11_TABLE=events_raw_isolated",
		"SERVICE=ta-traffic-runtime-proto",
	}
}

func t11VerifyEnv(extra ...string) []string {
	vals := map[string]string{
		"T11_DATASET":               "traffic_runtime_t11",
		"T11_TABLE":                 "events_raw_isolated",
		"T11_CLICK_IDS":             "c1",
		"T11_EXPECT_TA_SOURCE":      "network-a",
		"T11_EXPECT_TA_SUB":         "campaign-42",
		"T11_EXPECT_CAMPAIGN_ID":    "",
		"T11_EXPECT_ZONE_ID":        "",
		"T11_EXPECT_COUNTRY":        "",
		"T11_EXPECT_RELEASE_SHA256": "abc123",
		"T11_WINDOW_START":          "2026-09-01",
		"T11_GATE":                  "probe",
	}
	for _, kv := range extra {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		vals[k] = v
	}
	env := make([]string, 0, len(vals))
	for k, v := range vals {
		env = append(env, k+"="+v)
	}
	return env
}

func TestT11SetupRejectsProductionAndMissing(t *testing.T) {
	run := func(env []string) (string, error) {
		return t11RunScript(t, "traffic-runtime-t11-setup.sh", "", env)
	}
	out, err := run([]string{
		"PROJECT_ID=example-gcp-project",
		"T11_TOPIC=ta-events",
		"T11_SUBSCRIPTION=iso-sub",
		"T11_DATASET=iso_ds",
		"T11_TABLE=iso_table",
		"SERVICE=ta-traffic-runtime-proto",
	})
	if err == nil || !strings.Contains(out, "refusing production Pub/Sub") {
		t.Fatalf("topic ta-events: err=%v out=%s", err, out)
	}
	out, err = run([]string{
		"PROJECT_ID=example-gcp-project",
		"T11_TOPIC=iso-topic",
		"T11_SUBSCRIPTION=ta-events-to-bq",
		"T11_DATASET=iso_ds",
		"T11_TABLE=iso_table",
		"SERVICE=ta-traffic-runtime-proto",
	})
	if err == nil || !strings.Contains(out, "refusing production Pub/Sub") {
		t.Fatalf("sub production: err=%v out=%s", err, out)
	}
	out, err = run([]string{
		"PROJECT_ID=example-gcp-project",
		"T11_TOPIC=iso-topic",
		"T11_SUBSCRIPTION=iso-sub",
		"T11_DATASET=events_raw",
		"T11_TABLE=events",
		"SERVICE=ta-traffic-runtime-proto",
	})
	if err == nil || !strings.Contains(out, "refusing production BigQuery") {
		t.Fatalf("events_raw.events: err=%v out=%s", err, out)
	}
	out, err = run([]string{
		"PROJECT_ID=example-gcp-project",
		"SERVICE=ta-traffic-runtime-proto",
	})
	if err == nil || !strings.Contains(out, "T11 requires") {
		t.Fatalf("missing ids: err=%v out=%s", err, out)
	}
	out, err = run(append([]string{
		"PROJECT_ID=example-gcp-project",
		"APPLY=0",
	}, t11IsolatedIDs()...))
	if err != nil || !strings.Contains(out, "preflight_ok=yes") {
		t.Fatalf("isolated preflight: err=%v out=%s", err, out)
	}
	out, err = run([]string{
		"PROJECT_ID=example-gcp-project",
		"T11_TOPIC=ta-traffic-runtime-t11-events",
		"T11_SUBSCRIPTION=ta-traffic-runtime-t11-to-bq",
		"T11_DATASET=traffic_runtime_t11",
		"T11_TABLE=events_raw_isolated",
		"SERVICE=ta-events-ingest",
	})
	if err == nil || !strings.Contains(out, "refusing production ingest service") {
		t.Fatalf("ingest service: err=%v out=%s", err, out)
	}
}

func TestT11SetupMissingProjectIDRefusesBeforeGCP(t *testing.T) {
	fake, marker := t11FakeGCP(t, nil, nil)
	out, err := t11RunScript(t, "traffic-runtime-t11-setup.sh", fake, append([]string{"APPLY=1"}, t11IsolatedIDs()...))
	if err == nil || !strings.Contains(out, "PROJECT_ID is required") {
		t.Fatalf("missing PROJECT_ID: err=%v out=%s", err, out)
	}
	if strings.Contains(out, "preflight_ok=yes") {
		t.Fatalf("must not fall through to implicit project preflight: %s", out)
	}
	if strings.Contains(out, "example-gcp-project") {
		t.Fatalf("must not apply deploy-env project default: %s", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("gcloud/bq invoked without PROJECT_ID: %s", readFileOrEmpty(t, marker))
	}

	out, err = t11RunScript(t, "traffic-runtime-t11-setup.sh", fake, append([]string{"PROJECT_ID=", "APPLY=1"}, t11IsolatedIDs()...))
	if err == nil || !strings.Contains(out, "PROJECT_ID is required") {
		t.Fatalf("empty PROJECT_ID: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("gcloud/bq invoked with empty PROJECT_ID: %s", readFileOrEmpty(t, marker))
	}
}

func TestT11VerifyMissingProjectIDRefusesBeforeGCP(t *testing.T) {
	fake, marker := t11FakeGCP(t, map[string]string{"bq": "[]\n"}, map[string]int{"bq": 0})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv())
	if err == nil || !strings.Contains(out, "PROJECT_ID is required") {
		t.Fatalf("missing PROJECT_ID: err=%v out=%s", err, out)
	}
	if strings.Contains(out, "example-gcp-project") {
		t.Fatalf("must not apply deploy-env project default: %s", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("bq invoked without PROJECT_ID: %s", readFileOrEmpty(t, marker))
	}

	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv("PROJECT_ID="))
	if err == nil || !strings.Contains(out, "PROJECT_ID is required") {
		t.Fatalf("empty PROJECT_ID: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("bq invoked with empty PROJECT_ID: %s", readFileOrEmpty(t, marker))
	}
}

func TestT11VerifyEmptyAndPartialBQResultFail(t *testing.T) {
	emptyFake, emptyMarker := t11FakeGCP(t, map[string]string{"bq": "[]\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", emptyFake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "result=FAIL") || !strings.Contains(out, "empty_result") {
		t.Fatalf("empty BQ: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(emptyMarker); statErr != nil {
		t.Fatal("expected bq to run when PROJECT_ID is set")
	}

	partial, err := json.Marshal([]map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
	})
	if err != nil {
		t.Fatal(err)
	}
	partialFake, _ := t11FakeGCP(t, map[string]string{"bq": string(partial) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", partialFake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "result=FAIL") || !strings.Contains(out, "incomplete_events") {
		t.Fatalf("partial BQ: err=%v out=%s", err, out)
	}
}

func TestT11VerifyCompleteBQResultPass(t *testing.T) {
	fake, _ := t11FakeGCP(t, map[string]string{"bq": t11TripleJSON(t) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err != nil || !strings.Contains(out, "result=PASS") || !strings.Contains(out, "exact_match") {
		t.Fatalf("complete BQ: err=%v out=%s", err, out)
	}
}

func TestT11VerifyMismatchAndTwoClicksFail(t *testing.T) {
	wrong, err := json.Marshal([]map[string]string{
		t11Event("c1", "ingress", "other", "campaign-42", "nope"),
		t11Event("c1", "scenario_assigned", "other", "campaign-42", "nope"),
		t11Event("c1", "redirect", "other", "campaign-42", "nope"),
	})
	if err != nil {
		t.Fatal(err)
	}
	fake, _ := t11FakeGCP(t, map[string]string{"bq": string(wrong) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "ta_source_mismatch") || !strings.Contains(out, "release_sha_mismatch") {
		t.Fatalf("mismatch: err=%v out=%s", err, out)
	}

	oneFake, _ := t11FakeGCP(t, map[string]string{"bq": t11TripleJSON(t) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", oneFake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_CLICK_IDS=c1,c2",
	))
	if err == nil || !strings.Contains(out, "missing_click_id=c2") {
		t.Fatalf("missing second click: err=%v out=%s", err, out)
	}
}

func TestT11VerifyPresenceOnlyAndExtraChainFail(t *testing.T) {
	presence := `[{"click_id":"c1","event_names":["ingress","redirect","scenario_assigned"],"ta_source_present":3,"ta_sub_present":3,"provenance_present":3}]`
	presenceFake, _ := t11FakeGCP(t, map[string]string{"bq": presence + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", presenceFake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "result=FAIL") || !strings.Contains(out, "incomplete_events") {
		t.Fatalf("presence-only must not PASS: err=%v out=%s", err, out)
	}

	mixed := []map[string]string{
		t11Event("c1", "ingress", "other", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	mixedRaw, err := json.Marshal(mixed)
	if err != nil {
		t.Fatal(err)
	}
	mixedFake, _ := t11FakeGCP(t, map[string]string{"bq": string(mixedRaw) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", mixedFake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "ta_source_mismatch") {
		t.Fatalf("per-event mismatch: err=%v out=%s", err, out)
	}

	extraFake, _ := t11FakeGCP(t, map[string]string{"bq": t11TripleJSON(t, t11Event("c1", "pageview", "network-a", "campaign-42", "abc123")) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", extraFake, t11VerifyEnv("PROJECT_ID=example-gcp-project"))
	if err == nil || !strings.Contains(out, "extra_events") {
		t.Fatalf("extra chain: err=%v out=%s", err, out)
	}
}

func TestT11VerifyAllowsExplicitProductionTarget(t *testing.T) {
	fake, marker := t11FakeGCP(t, map[string]string{"bq": t11TripleJSON(t) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_DATASET=events_raw",
		"T11_TABLE=events",
		"T11_GATE=probe",
	))
	if err != nil || !strings.Contains(out, "result=PASS") {
		t.Fatalf("explicit production probe: err=%v out=%s", err, out)
	}
	if strings.Contains(out, "refusing production BigQuery") {
		t.Fatalf("verify must not refuse an explicit production table: %s", out)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatal("bq must run for an explicit production verify target")
	}
}

func TestT11VerifyFinalRequiresMartAndRevision(t *testing.T) {
	fake, marker := t11FakeGCP(t, map[string]string{"bq": t11TripleJSON(t) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
	))
	if err == nil || !strings.Contains(out, "final gate requires T11_MART_DATASET") {
		t.Fatalf("final without mart: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("bq invoked before final mart env: %s", readFileOrEmpty(t, marker))
	}

	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
	))
	if err == nil || !strings.Contains(out, "T11_EXPECT_K_REVISION must be set") {
		t.Fatalf("final without revision: err=%v out=%s", err, out)
	}
}

func TestT11VerifyFinalMartMissingAndWrongSubFail(t *testing.T) {
	triple := []map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	for i := range triple {
		triple[i]["k_revision"] = "rev-1"
		triple[i]["country"] = "RU"
	}
	raw, err := json.Marshal(triple)
	if err != nil {
		t.Fatal(err)
	}
	missingMart, err := json.Marshal([]map[string]any{
		{"requested_click_id": "c1", "click_id": nil, "ta_source": nil, "ta_sub": nil, "campaign_id": nil, "zone_id": nil, "country": nil, "mart_found": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake, _ := t11FakeGCP(t, map[string]string{"bq": string(raw) + "\n", "bq.mart": string(missingMart) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=rev-1",
		"T11_EXPECT_COUNTRY=RU",
	))
	if err == nil || !strings.Contains(out, "mart_missing_click_id=c1") {
		t.Fatalf("missing mart row: err=%v out=%s", err, out)
	}

	wrongSub, err := json.Marshal([]map[string]any{
		{"requested_click_id": "c1", "click_id": "c1", "ta_source": "network-a", "ta_sub": "WRONG_SUB", "campaign_id": "", "zone_id": "", "country": "RU", "mart_found": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongFake, _ := t11FakeGCP(t, map[string]string{"bq": string(raw) + "\n", "bq.mart": string(wrongSub) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", wrongFake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=rev-1",
		"T11_EXPECT_COUNTRY=RU",
	))
	if err == nil || !strings.Contains(out, "mart_ta_sub_mismatch") {
		t.Fatalf("wrong mart ta_sub: err=%v out=%s", err, out)
	}

	oldRev := []map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	for i := range oldRev {
		oldRev[i]["k_revision"] = "old-rev"
		oldRev[i]["country"] = "RU"
	}
	oldRaw, err := json.Marshal(oldRev)
	if err != nil {
		t.Fatal(err)
	}
	okMart, err := json.Marshal([]map[string]any{
		{"requested_click_id": "c1", "click_id": "c1", "ta_source": "network-a", "ta_sub": "campaign-42", "campaign_id": "", "zone_id": "", "country": "RU", "mart_found": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldFake, _ := t11FakeGCP(t, map[string]string{"bq": string(oldRaw) + "\n", "bq.mart": string(okMart) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", oldFake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=rev-1",
		"T11_EXPECT_COUNTRY=RU",
	))
	if err == nil || !strings.Contains(out, "k_revision_mismatch") {
		t.Fatalf("old revision: err=%v out=%s", err, out)
	}
}

func TestT11VerifyFinalEmptyCountryOrRevisionFailAndPositivePass(t *testing.T) {
	triple := []map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	for i := range triple {
		triple[i]["k_revision"] = ""
		triple[i]["country"] = ""
	}
	raw, err := json.Marshal(triple)
	if err != nil {
		t.Fatal(err)
	}
	emptyMart, err := json.Marshal([]map[string]any{
		{"requested_click_id": "c1", "click_id": "c1", "ta_source": "network-a", "ta_sub": "campaign-42", "campaign_id": "", "zone_id": "", "country": "", "mart_found": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake, marker := t11FakeGCP(t, map[string]string{"bq": string(raw) + "\n", "bq.mart": string(emptyMart) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err := t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=",
		"T11_EXPECT_COUNTRY=RU",
	))
	if err == nil || !strings.Contains(out, "final gate requires nonempty T11_EXPECT_K_REVISION") {
		t.Fatalf("empty revision: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("bq must not run when final revision is empty: %s", readFileOrEmpty(t, marker))
	}

	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", fake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=rev-1",
		"T11_EXPECT_COUNTRY=",
	))
	if err == nil || !strings.Contains(out, "final gate requires nonempty T11_EXPECT_COUNTRY") {
		t.Fatalf("empty country: err=%v out=%s", err, out)
	}

	okTriple := []map[string]string{
		t11Event("c1", "ingress", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "scenario_assigned", "network-a", "campaign-42", "abc123"),
		t11Event("c1", "redirect", "network-a", "campaign-42", "abc123"),
	}
	for i := range okTriple {
		okTriple[i]["k_revision"] = "rev-1"
		okTriple[i]["country"] = "RU"
	}
	okRaw, err := json.Marshal(okTriple)
	if err != nil {
		t.Fatal(err)
	}
	okMart, err := json.Marshal([]map[string]any{
		{"requested_click_id": "c1", "click_id": "c1", "ta_source": "network-a", "ta_sub": "campaign-42", "campaign_id": "", "zone_id": "", "country": "RU", "mart_found": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	okFake, _ := t11FakeGCP(t, map[string]string{"bq": string(okRaw) + "\n", "bq.mart": string(okMart) + "\n"}, map[string]int{"bq": 0, "gcloud": 99})
	out, err = t11RunScript(t, "traffic-runtime-t11-verify.sh", okFake, t11VerifyEnv(
		"PROJECT_ID=example-gcp-project",
		"T11_GATE=final",
		"T11_MART_DATASET=traffic_mart",
		"T11_MART_TABLE=clicks",
		"T11_EXPECT_K_REVISION=rev-1",
		"T11_EXPECT_COUNTRY=RU",
	))
	if err != nil || !strings.Contains(out, "result=PASS") {
		t.Fatalf("positive final: err=%v out=%s", err, out)
	}
}

func TestT11EvalSelfTest(t *testing.T) {
	root := t11RepoRoot(t)
	cmd := exec.Command("python3", filepath.Join(root, "scripts", "traffic-runtime-t11-eval.py"), "--self-test")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("t11-eval --self-test: err=%v out=%s", err, out)
	}
}

func TestT11ScriptsDoNotSourceDeployEnv(t *testing.T) {
	for _, name := range []string{
		"scripts/traffic-runtime-t11-setup.sh",
		"scripts/traffic-runtime-t11-verify.sh",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasPrefix(trimmed, "source ") && strings.Contains(trimmed, "traffic-runtime-env.sh") {
				t.Fatalf("%s must not source traffic-runtime-env.sh: %s", name, trimmed)
			}
		}
	}
}

func TestT11SQLTemplateIsIsolated(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "traffic-runtime-t11.bigquery.sql"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "`{{PROJECT_ID}}.events_raw.events`") || strings.Contains(s, "example-gcp-project.events_raw") {
		t.Fatal("T11 SQL must not hardcode production table")
	}
	if !strings.Contains(s, "require_partition_filter") || !strings.Contains(s, "partition_expiration_days") {
		t.Fatal("T11 SQL must set partition filter and TTL")
	}
}

func TestT11VerifySQLHasNoHaving(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "traffic-runtime-t11-verify.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if strings.Contains(strings.ToUpper(body.String()), "HAVING") {
		t.Fatal("T11 verify SQL must not hide incomplete clicks with HAVING")
	}
	if strings.Contains(string(raw), "ta_source_present") || strings.Contains(string(raw), "provenance_present") {
		t.Fatal("T11 verify SQL must return stored values, not COUNTIF-present flags")
	}
	if !strings.Contains(string(raw), "event_name") || !strings.Contains(string(raw), "contract_release_sha256") {
		t.Fatal("T11 verify SQL must return per-event name and release SHA for exact matching")
	}
	if !strings.Contains(string(raw), "campaign_id") || !strings.Contains(string(raw), "zone_id") || !strings.Contains(string(raw), "country") {
		t.Fatal("T11 verify SQL must return campaign_id, zone_id, country for exact matching")
	}
	if !strings.Contains(string(raw), "k_revision") {
		t.Fatal("T11 verify SQL must return params.k_revision")
	}
	if !strings.Contains(string(raw), "UNNEST") || !strings.Contains(string(raw), "LEFT JOIN") {
		t.Fatal("T11 verify SQL must LEFT JOIN requested click_ids so missing clicks stay visible")
	}
	if strings.Contains(string(raw), "USING (click_id)") || strings.Contains(string(raw), "USING(click_id)") {
		t.Fatal("T11 verify SQL must not USING(click_id); that hides a missing right row")
	}
	mart, err := os.ReadFile(filepath.Join("..", "..", "scripts", "traffic-runtime-t11-verify-mart.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mart)
	if !strings.Contains(ms, "requested_click_id") || !strings.Contains(ms, "mart_found") || !strings.Contains(ms, "d.click_id") {
		t.Fatal("mart SQL must expose requested_click_id, d.click_id, mart_found")
	}
	if strings.Contains(ms, "USING (click_id)") || strings.Contains(ms, "USING(click_id)") {
		t.Fatal("mart SQL must not USING(click_id)")
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
