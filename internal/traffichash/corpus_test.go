package traffichash_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"ta-events-ingest/internal/producerhandoff"
	"ta-events-ingest/internal/traffichash"
)

// Producer file (compiler-service@297d8d0c hash-vectors.json): flat vectors
// with assignmentKey, assignmentValue, ..., bodyHex and expected.sha256/point.
// bodyHex is the authoritative wire body: it is decoded with DecodeBody and
// re-encoded with Body, so parity does not depend on JSON key spelling of the
// remaining parts. Unknown vector shape is a failure, never a skip.
type producerHashExpected struct {
	SHA256 string          `json:"sha256"`
	Point  json.RawMessage `json:"point"`
}

func TestProducerHashCorpus(t *testing.T) {
	pkg, err := producerhandoff.Open(producerhandoff.DefaultDir())
	if errors.Is(err, producerhandoff.ErrAbsent) {
		t.Skipf("T2 hash corpus NOT RUN: %v (set %s or vendor the package)", err, producerhandoff.EnvDir)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pkg.ReadVerified(producerhandoff.HashVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	vectors, fileTotalShare := hashVectors(t, raw)
	if len(vectors) == 0 {
		t.Fatalf("%s: no vectors found; top-level shape not understood", producerhandoff.HashVectorsFile)
	}
	for i, vec := range vectors {
		name := vectorName(vec, i)
		bodyHex := requireString(t, name, vec, "bodyHex")
		body, err := hex.DecodeString(bodyHex)
		if err != nil {
			t.Fatalf("%s: bodyHex: %v", name, err)
		}
		parts, err := traffichash.DecodeBody(body)
		if err != nil {
			t.Fatalf("%s: producer body does not decode as %s: %v", name, traffichash.Algorithm, err)
		}
		if got := requireString(t, name, vec, "assignmentKey"); got != parts.AssignmentKey {
			t.Fatalf("%s: assignmentKey %q != body part[0] %q", name, got, parts.AssignmentKey)
		}
		if got := requireString(t, name, vec, "assignmentValue"); got != parts.AssignmentValue {
			t.Fatalf("%s: assignmentValue %q != body part[1] %q", name, got, parts.AssignmentValue)
		}
		reEncoded, err := traffichash.Body(parts)
		if err != nil {
			t.Fatalf("%s: Body: %v", name, err)
		}
		if hex.EncodeToString(reEncoded) != strings.ToLower(bodyHex) {
			t.Fatalf("%s: Body re-encode mismatch\n got %x\nwant %s", name, reEncoded, bodyHex)
		}

		var exp producerHashExpected
		expRaw, ok := vec["expected"]
		if !ok {
			t.Fatalf("%s: missing expected", name)
		}
		if err := json.Unmarshal(expRaw, &exp); err != nil {
			t.Fatalf("%s: expected: %v", name, err)
		}
		if exp.SHA256 == "" {
			t.Fatalf("%s: expected.sha256 missing", name)
		}
		if bodySum := sha256.Sum256(body); hex.EncodeToString(bodySum[:]) != strings.ToLower(exp.SHA256) {
			t.Fatalf("%s: producer bodyHex/expected.sha256 are internally inconsistent", name)
		}
		d, err := traffichash.Digest(parts)
		if err != nil {
			t.Fatalf("%s: Digest: %v", name, err)
		}
		if got := hex.EncodeToString(d[:]); got != strings.ToLower(exp.SHA256) {
			t.Fatalf("%s: digest %s want %s", name, got, exp.SHA256)
		}

		if len(exp.Point) == 0 || string(exp.Point) == "null" {
			continue
		}
		wantPoint, err := parseUint(exp.Point)
		if err != nil {
			t.Fatalf("%s: expected.point: %v", name, err)
		}
		share, ok := totalShare(vec, fileTotalShare)
		if !ok {
			t.Fatalf("%s: expected.point present but totalShare not found in vector or file; keys=%v", name, keys(vec))
		}
		pt, err := traffichash.Point(parts, share)
		if err != nil {
			t.Fatalf("%s: Point: %v", name, err)
		}
		if pt != wantPoint {
			t.Fatalf("%s: point %d want %d (totalShare=%d)", name, pt, wantPoint, share)
		}
	}
	t.Logf("T2 hash corpus: %d vectors from %s @ %s", len(vectors), producerhandoff.HashVectorsFile, producerhandoff.ProducerSHA)
}

// hashVectors accepts either a top-level array or an object whose single
// array member holds the vectors. Anything else fails loudly.
func hashVectors(t *testing.T, raw []byte) ([]map[string]json.RawMessage, json.RawMessage) {
	t.Helper()
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", producerhandoff.HashVectorsFile, err)
	}
	var found []map[string]json.RawMessage
	var foundKey string
	for k, v := range obj {
		var cand []map[string]json.RawMessage
		if err := json.Unmarshal(v, &cand); err == nil && len(cand) > 0 {
			if foundKey != "" {
				t.Fatalf("%s: ambiguous vector arrays %q and %q", producerhandoff.HashVectorsFile, foundKey, k)
			}
			found, foundKey = cand, k
		}
	}
	if foundKey == "" {
		t.Fatalf("%s: no array of vectors; top-level keys=%v", producerhandoff.HashVectorsFile, keys(obj))
	}
	return found, obj["totalShare"]
}

func totalShare(vec map[string]json.RawMessage, fileLevel json.RawMessage) (uint64, bool) {
	if v, ok := vec["totalShare"]; ok {
		if n, err := parseUint(v); err == nil && n > 0 {
			return n, true
		}
	}
	if expRaw, ok := vec["expected"]; ok {
		var exp map[string]json.RawMessage
		if json.Unmarshal(expRaw, &exp) == nil {
			if v, ok := exp["totalShare"]; ok {
				if n, err := parseUint(v); err == nil && n > 0 {
					return n, true
				}
			}
		}
	}
	if len(fileLevel) > 0 {
		if n, err := parseUint(fileLevel); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func parseUint(raw json.RawMessage) (uint64, error) {
	var n uint64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("not a number or numeric string: %s", raw)
	}
	var out uint64
	if _, err := fmt.Sscan(s, &out); err != nil {
		return 0, err
	}
	return out, nil
}

func requireString(t *testing.T, name string, vec map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := vec[key]
	if !ok {
		t.Fatalf("%s: missing %q; keys=%v", name, key, keys(vec))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %s: %v", name, key, err)
	}
	return s
}

func vectorName(vec map[string]json.RawMessage, i int) string {
	for _, k := range []string{"name", "id", "case", "description"} {
		if raw, ok := vec[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return fmt.Sprintf("vector[%d]", i)
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
