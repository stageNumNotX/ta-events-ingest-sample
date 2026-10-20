package qmacrocontract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOracleDigestIsCompiledPin(t *testing.T) {
	if OracleDigest != "eddc83fd05225ec71368f4570fce593f30a2d9bef3b8a1abc4981ad6686208f5" {
		t.Fatalf("OracleDigest=%s", OracleDigest)
	}
	if len(OracleDigest) != 64 {
		t.Fatalf("OracleDigest length %d", len(OracleDigest))
	}
	for _, c := range OracleDigest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("OracleDigest is not lowercase hex: %s", OracleDigest)
		}
	}
	if ProducerCommitPrefix != "39db6c0" {
		t.Fatalf("ProducerCommitPrefix=%s", ProducerCommitPrefix)
	}
	if EvidencePath != "docs/control-plane/evidence/q-macro-contract" {
		t.Fatalf("EvidencePath=%s", EvidencePath)
	}
	const t2Manifest = "7e0adb92f208d399072d75b4ce2ef6ced74c966b991a64eccacd0626b8fb01eb"
	if OracleDigest == t2Manifest {
		t.Fatal("Q-MACRO oracleDigest must not be the T2 country-handoff manifest pin")
	}
}

func TestDefaultDirPinsOracleDigest(t *testing.T) {
	t.Setenv(EnvDir, "")
	got := DefaultDir()
	base := filepath.Base(got)
	if base != OracleDigest && base != "q-macro-contract" {
		t.Fatalf("DefaultDir %s must be testdata/<digest> or testdata/traffic-contract/q-macro-contract", got)
	}
	t.Setenv(EnvDir, "/tmp/qmacro-override")
	if DefaultDir() != "/tmp/qmacro-override" {
		t.Fatal("env override ignored")
	}
}

func TestOpenVendoredBindingDigest(t *testing.T) {
	t.Setenv(EnvDir, "")
	pkg, err := Open(DefaultDir())
	if errors.Is(err, ErrAbsent) {
		t.Fatalf("vendored Q-MACRO package required at %s", DefaultDir())
	}
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Digest != OracleDigest {
		t.Fatalf("digest %s", pkg.Digest)
	}
	if filepath.Base(pkg.OracleFile) != DecisionVectorsFile {
		t.Fatalf("OracleFile %s", pkg.OracleFile)
	}
	if pkg.fileDigest {
		t.Fatal("producer package uses binding digest, not a single-file hash")
	}
	raw, err := pkg.ReadVerified(pkg.OracleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty decision-vectors")
	}
}

func TestOpenAbsentIsErrAbsent(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, ErrAbsent) {
		t.Fatalf("want ErrAbsent, got %v", err)
	}
}

func TestOpenRequiresDigestFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil || errors.Is(err, ErrAbsent) {
		t.Fatalf("dir without DIGEST must hard-fail, got %v", err)
	}
}

func TestOpenRejectsWrongDigest(t *testing.T) {
	dir := t.TempDir()
	wrong := strings.Repeat("ab", 32)
	if err := os.WriteFile(filepath.Join(dir, DigestFile), []byte(wrong+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil {
		t.Fatal("wrong DIGEST must fail")
	}
	if !strings.Contains(err.Error(), OracleDigest) {
		t.Fatalf("error must name compiled pin: %v", err)
	}
}

func TestOpenRejectsPinWithoutMatchingCorpusBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DigestFile), []byte(OracleDigest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DecisionVectorsFile), []byte(`{"cases":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil {
		t.Fatal("DIGEST pin without corpus bytes hashing to OracleDigest must fail")
	}
	if !strings.Contains(err.Error(), "oracleDigest") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenIgnoresSiblingChecksum(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DigestFile), []byte(OracleDigest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"note":"not-oracle"}`))
	if err := os.WriteFile(filepath.Join(dir, DigestFile+".sha256"), []byte(hex.EncodeToString(sum[:])+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("sibling checksum must not close the pin")
	}
}

func TestParseDigestOracleEqualsForm(t *testing.T) {
	got, files, err := parseDigest([]byte("oracleDigest=" + OracleDigest + "\n# comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != OracleDigest {
		t.Fatalf("got %s", got)
	}
	if len(files) != 0 {
		t.Fatalf("files=%v", files)
	}
}

func TestParseDigestSha256sumLines(t *testing.T) {
	got, files, err := parseDigest([]byte(OracleDigest + "  corpus/" + DecisionVectorsFile + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != OracleDigest {
		t.Fatalf("got %s", got)
	}
	if files["corpus/"+DecisionVectorsFile] != OracleDigest {
		t.Fatalf("files=%v", files)
	}
}
