package traffichash

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestBodyRoundTripAndDigest(t *testing.T) {
	p := Parts{
		AssignmentKey:   "SUBJECT_ID",
		AssignmentValue: "",
		PolicyID:        "ta-traffic-bootstrap-v1",
		DecisionScopeID: "scenario",
		DistributionKey: "scenario.default",
		Salt:            "scenario-v1",
	}
	body, err := Body(p)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(Algorithm), 0)
	if !bytes.HasPrefix(body, want) {
		t.Fatalf("body prefix %q", body[:len(want)])
	}
	// empty assignmentValue keeps its uint16BE(0) prefix
	if body[len(want)+2+len("SUBJECT_ID")] != 0 || body[len(want)+2+len("SUBJECT_ID")+1] != 0 {
		t.Fatalf("empty part must encode as 0x0000: %x", body)
	}
	back, err := DecodeBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if back != p {
		t.Fatalf("roundtrip %+v != %+v", back, p)
	}
	d, err := Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	if d != sha256.Sum256(body) {
		t.Fatal("Digest must equal SHA256(Body)")
	}
}

func TestDecodeBodyRejects(t *testing.T) {
	good, _ := Body(Parts{AssignmentKey: "k", AssignmentValue: "v"})
	cases := map[string][]byte{
		"wrong algorithm":  append([]byte("cs.hash.v1\x00"), good[len(Algorithm)+1:]...),
		"truncated length": good[:len(good)-1],
		"trailing byte":    append(append([]byte(nil), good...), 0),
		"empty":            nil,
	}
	for name, body := range cases {
		if _, err := DecodeBody(body); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	// 5 parts only
	five := append([]byte(Algorithm), 0)
	for i := 0; i < 5; i++ {
		five = append(five, 0, 0)
	}
	if _, err := DecodeBody(five); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("5 parts: %v", err)
	}
	// uint32 prefix must not decode as 6 parts
	u32 := append([]byte(Algorithm), 0)
	for i := 0; i < 6; i++ {
		u32 = append(u32, 0, 0, 0, 1, 'x')
	}
	if _, err := DecodeBody(u32); err == nil {
		t.Fatal("uint32BE body must not decode")
	}
	over := append([]byte(Algorithm), 0)
	over = append(over, 0, 1, 'k')
	over = append(over, 0x01, 0x01)
	over = append(over, bytes.Repeat([]byte{'a'}, 257)...)
	for i := 0; i < 4; i++ {
		over = append(over, 0, 0)
	}
	if _, err := DecodeBody(over); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("257-byte assignmentValue: %v", err)
	}
}
