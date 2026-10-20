package runtimeloader_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
)

func testdata(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "pinned")
}

func TestLoadPinnedHashes(t *testing.T) {
	b, err := runtimeloader.LoadDir(testdata(t))
	if err != nil {
		t.Fatal(err)
	}
	if b.Release.CompileTarget != "traffic-runtime/v1" {
		t.Fatalf("compileTarget %q", b.Release.CompileTarget)
	}
	sum := sha256.Sum256(b.ProgramBytes)
	if hex.EncodeToString(sum[:]) != b.Release.Program.SHA256 {
		t.Fatal("program digest drifted")
	}
}

func TestRejectTamperedProgram(t *testing.T) {
	dir := testdata(t)
	rel, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := os.ReadFile(filepath.Join(dir, "program.json"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.ReadFile(filepath.Join(dir, "weights.json"))
	if err != nil {
		t.Fatal(err)
	}
	prog[len(prog)-2] ^= 0x01
	if _, err := runtimeloader.Verify(rel, prog, w); err == nil {
		t.Fatal("expected hash mismatch")
	}
}
