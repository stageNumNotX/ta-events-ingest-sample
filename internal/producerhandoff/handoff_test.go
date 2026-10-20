package producerhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pinManifest(t *testing.T, dir string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	// Independent pin (env), never a sibling checksum generated from these bytes.
	t.Setenv(EnvManifestSHA, hex.EncodeToString(sum[:]))
}

func TestOpenAbsentIsErrAbsent(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, ErrAbsent) {
		t.Fatalf("want ErrAbsent, got %v", err)
	}
}

func TestOpenRequiresPinnedManifestBlob(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil || errors.Is(err, ErrAbsent) {
		t.Fatalf("dir without manifest must hard-fail, got %v", err)
	}
	body := []byte(`{"producerSha":"10b0da8","files":{"a.json":"00"}}`)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("unpinned manifest blob must fail")
	}
	sum := sha256.Sum256(body)
	t.Setenv(EnvManifestSHA, hex.EncodeToString(sum[:]))
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsWrongManifestChecksum(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"producerSha":"10b0da8","files":{"a.json":"00"}}`)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvManifestSHA, strings.Repeat("ab", 32))
	if _, err := Open(dir); err == nil {
		t.Fatal("wrong manifest checksum must fail")
	}
}

func TestOpenIgnoresSiblingChecksum(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"producerSha":"10b0da8","files":{"a.json":"00"}}`)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, ManifestSHAFile), []byte(hexSum+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("sibling manifest.json.sha256 must not authenticate the blob")
	}
	t.Setenv(EnvManifestSHA, hexSum)
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedManifestSHA256IsOwnerPin(t *testing.T) {
	const want = "7e0adb92f208d399072d75b4ce2ef6ced74c966b991a64eccacd0626b8fb01eb"
	if ExpectedManifestSHA256 != want {
		t.Fatalf("ExpectedManifestSHA256=%s", ExpectedManifestSHA256)
	}
	if ExpectedManifestBytes != 4145 {
		t.Fatalf("ExpectedManifestBytes=%d", ExpectedManifestBytes)
	}
	if _, err := normalizeManifestSHA(ExpectedManifestSHA256); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDoesNotTrustSubstringWithoutBlobPin(t *testing.T) {
	dir := t.TempDir()
	// 10b0da8 appears in the JSON, but the blob is unpinned.
	body := []byte(`{"note":"10b0da8","files":{"a.json":"00"}}`)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("substring producer SHA without blob pin must fail")
	}
}

func TestOpenRequiresManifestWithProducerSHA(t *testing.T) {
	dir := t.TempDir()
	pinManifest(t, dir, []byte(`{"producer":"deadbeef","files":{"x.json":"00"}}`))
	if _, err := Open(dir); err == nil {
		t.Fatal("manifest without producer SHA must fail")
	}
	for _, m := range []string{
		`{"producerSha":"10b0da8","files":{"x.json":"00"}}`,
		`{"producerSha":"` + ImplementationSHA + `","files":{"x.json":"00"}}`,
		`{"contractReady":"` + ProducerSHA + `","files":{"x.json":"00"}}`,
	} {
		pinManifest(t, dir, []byte(m))
		if _, err := Open(dir); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
}

func TestReadVerifiedHonorsNestedManifestPaths(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.ToSlash(filepath.Join("testdata", "traffic-contract", "v1", HashVectorsFile))
	content := []byte(`[{"bodyHex":"00"}]`)
	sum := sha256.Sum256(content)
	hexSum := hex.EncodeToString(sum[:])
	man := `{"producerSha":"` + ImplementationSHA + `","root":"testdata/traffic-contract/v1","files":{` +
		`"` + nested + `":{"sha256":"` + hexSum + `"}}}`
	pinManifest(t, dir, []byte(man))
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(nested)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(nested)), content, 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := pkg.FilePaths(); len(got) != 1 || got[0] != nested {
		t.Fatalf("FilePaths=%v want [%s]", got, nested)
	}
	got, err := pkg.ReadVerified(HashVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("basename lookup returned %q", got)
	}
	if _, err := pkg.ReadVerified(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(nested)), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.ReadVerified(HashVectorsFile); err == nil {
		t.Fatal("modified bytes must be rejected")
	}
	if _, err := pkg.ReadVerified(DecisionVectorsFile); err == nil {
		t.Fatal("missing file must be rejected")
	}
}

func TestReadVerifiedIgnoresFlattenedRootDecoy(t *testing.T) {
	dir := t.TempDir()
	nested := "testdata/traffic-contract/v1/" + HashVectorsFile
	real := []byte(`[{"id":"nested"}]`)
	decoy := []byte(`[{"id":"flattened-root"}]`)
	sum := sha256.Sum256(real)
	man := `{"producerSha":"10b0da8","root":"testdata/traffic-contract/v1","files":{` +
		`"` + nested + `":"` + hex.EncodeToString(sum[:]) + `"}}`
	pinManifest(t, dir, []byte(man))
	if err := os.MkdirAll(filepath.Join(dir, "testdata", "traffic-contract", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(nested)), real, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, HashVectorsFile), decoy, 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pkg.ReadVerified(HashVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(real) {
		t.Fatalf("flattened root decoy was used: %s", got)
	}
}

func TestReadVerifiedBareKeyUsesManifestRoot(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`[{"bodyHex":"aa"}]`)
	sum := sha256.Sum256(content)
	man := `{"producerSha":"10b0da8","root":"testdata/traffic-contract/v1","files":{` +
		`"` + HashVectorsFile + `":"` + hex.EncodeToString(sum[:]) + `"}}`
	pinManifest(t, dir, []byte(man))
	nestedDir := filepath.Join(dir, "testdata", "traffic-contract", "v1")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, HashVectorsFile), content, 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pkg.ReadVerified(HashVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("root-relative read: %s", got)
	}
}

func TestReadVerifiedAmbiguousBasenameFails(t *testing.T) {
	dir := t.TempDir()
	a := []byte(`[{"a":1}]`)
	b := []byte(`[{"b":1}]`)
	sa := sha256.Sum256(a)
	sb := sha256.Sum256(b)
	man := `{"producerSha":"10b0da8","files":{` +
		`"testdata/traffic-contract/v1/` + HashVectorsFile + `":"` + hex.EncodeToString(sa[:]) + `",` +
		`"other/` + HashVectorsFile + `":"` + hex.EncodeToString(sb[:]) + `"}}`
	pinManifest(t, dir, []byte(man))
	pkg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.ReadVerified(HashVectorsFile); err == nil {
		t.Fatal("ambiguous basename must fail; do not flatten")
	}
}

func TestReadVerifiedExactHandoffLayout(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"testdata/traffic-contract/v1/" + HashVectorsFile:     []byte(`[{"bodyHex":"01"}]`),
		"testdata/traffic-contract/v1/" + DecisionVectorsFile: []byte(`{"cases":[]}`),
		"valid/smoke-redirect-v2/program.json":                []byte(`{"artifactType":"traffic-program/v1"}`),
		"valid/smoke-redirect-v2/weights.json":                []byte(`{"artifactType":"traffic-weights/v1"}`),
		"valid/smoke-redirect-v2/release.json":                []byte(`{"artifactType":"traffic-release/v1"}`),
	}
	manFiles := map[string]string{}
	for path, body := range files {
		sum := sha256.Sum256(body)
		manFiles[path] = hex.EncodeToString(sum[:])
		abs := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type mf struct {
		ProducerSha string            `json:"producerSha"`
		Root        string            `json:"root"`
		Files       map[string]string `json:"files"`
	}
	raw, err := json.Marshal(mf{ProducerSha: "10b0da8", Root: "testdata/traffic-contract/v1", Files: manFiles})
	if err != nil {
		t.Fatal(err)
	}
	pinManifest(t, dir, raw)
	pkg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Root != "testdata/traffic-contract/v1" {
		t.Fatalf("root %q", pkg.Root)
	}
	got := pkg.FilePaths()
	if len(got) != 5 {
		t.Fatalf("FilePaths %v", got)
	}
	for path, want := range files {
		b, err := pkg.ReadVerified(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(want) {
			t.Fatalf("%s: %s", path, b)
		}
	}
	if _, err := pkg.ReadVerified(HashVectorsFile); err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.ReadVerified(DecisionVectorsFile); err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.ReadVerified("program.json"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDirPinsProducerSHA(t *testing.T) {
	t.Setenv(EnvDir, "")
	if got := DefaultDir(); filepath.Base(got) != ProducerSHA {
		t.Fatalf("DefaultDir %s must end with producer SHA", got)
	}
	t.Setenv(EnvDir, "/tmp/x")
	if DefaultDir() != "/tmp/x" {
		t.Fatal("env override ignored")
	}
}

func TestDefaultDirFindsLegacyVendorPath(t *testing.T) {
	t.Setenv(EnvDir, "")
	// When the CONTRACT_READY package is vendored under testdata/<ProducerSHA>,
	// DefaultDir prefers that path (CandidateDirs[0]). Legacy producercorpus
	// handoff is only a fallback when the pin directory is absent.
	pin := filepath.Join(packageDir(), "testdata", ProducerSHA)
	if st, err := os.Stat(pin); err == nil && st.IsDir() {
		got := DefaultDir()
		if filepath.Base(got) != ProducerSHA {
			t.Fatalf("DefaultDir %s must prefer vendored ProducerSHA", got)
		}
		return
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "producercorpus", "testdata", "handoff")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, "producercorpus")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	got := DefaultDir()
	if !strings.HasSuffix(filepath.ToSlash(got), "producercorpus/testdata/handoff") {
		t.Fatalf("legacy vendored path not picked: %s", got)
	}
}
