package runtimeloader_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ta-events-ingest/internal/runtimeloader"
)

type mapReader map[string][]byte

func pinKey(p runtimeloader.ObjectPin) string {
	return fmt.Sprintf("%s/%s#%d", p.Bucket, p.Object, p.Generation)
}

func (m mapReader) ReadObject(_ context.Context, pin runtimeloader.ObjectPin) ([]byte, error) {
	b, ok := m[pinKey(pin)]
	if !ok {
		return nil, fmt.Errorf("missing %s", pinKey(pin))
	}
	if len(b) != pin.Size {
		return nil, fmt.Errorf("size")
	}
	return append([]byte(nil), b...), nil
}

func TestLoadPinsFromReader(t *testing.T) {
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
	pins := runtimeloader.GCSPins{
		Release: filePin("release", rel),
		Program: filePin("program", prog),
		Weights: filePin("weights", w),
	}
	r := mapReader{
		pinKey(pins.Release): rel,
		pinKey(pins.Program): prog,
		pinKey(pins.Weights): w,
	}
	b, err := runtimeloader.LoadPins(context.Background(), r, pins)
	if err != nil {
		t.Fatal(err)
	}
	if b.Release.LayoutID == "" {
		t.Fatal("empty layoutId")
	}
}

func filePin(kind string, raw []byte) runtimeloader.ObjectPin {
	sum := sha256.Sum256(raw)
	return runtimeloader.ObjectPin{
		Bucket:     "test",
		Object:     kind,
		Generation: 1,
		Size:       len(raw),
		SHA256:     hex.EncodeToString(sum[:]),
	}
}

func TestLoadPinsRejectsWrongGenerationKey(t *testing.T) {
	pins := runtimeloader.DefaultPins()
	r := mapReader{}
	if _, err := runtimeloader.LoadPins(context.Background(), r, pins); err == nil {
		t.Fatal("expected missing object")
	}
}

func TestDefaultPinsMatchEnvFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "traffic-runtime-pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(v, `"`)
	}
	pins := runtimeloader.DefaultPins()
	assertEq := func(got, want, name string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: got %q want %q", name, got, want)
		}
	}
	assertEq(pins.Program.Bucket, vals["BUCKET"], "bucket")
	assertEq(pins.Program.SHA256, vals["PROGRAM_SHA256"], "program sha")
	assertEq(pins.Weights.SHA256, vals["WEIGHTS_SHA256"], "weights sha")
	assertEq(pins.Release.SHA256, vals["RELEASE_SHA256"], "release sha")
	assertEq(strconv.FormatInt(pins.Program.Generation, 10), vals["PROGRAM_GENERATION"], "program gen")
	assertEq(strconv.Itoa(pins.Program.Size), vals["PROGRAM_SIZE"], "program size")
	assertEq(pins.Program.Object, "traffic-runtime/v1/program/sha256/"+vals["PROGRAM_SHA256"]+".json", "program object")
}

func TestGCSReaderRequiresGeneration(t *testing.T) {
	r := &runtimeloader.GCSReader{}
	_, err := r.ReadObject(context.Background(), runtimeloader.ObjectPin{Object: "x", Size: 1, SHA256: "a"})
	if err == nil {
		t.Fatal("expected generation required")
	}
}
