package traffichash

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Encoding vectors after uint16BE length prefix. These are not producer
// body/digest/point/Decision exports; T2 corpus is still missing.
func TestDigestAndPointUint16(t *testing.T) {
	tests := []struct {
		name      string
		parts     Parts
		digestHex string
		point     uint64
	}{
		{
			name:      "empty",
			parts:     Parts{},
			digestHex: "53bd4eb30af705c4148b9629679f0ad377ae86941fe465b2ee063e7ae34cffc0",
			point:     6212,
		},
		{
			name: "scenario.default subj",
			parts: Parts{
				AssignmentKey:   "SUBJECT_ID",
				AssignmentValue: "subj",
				PolicyID:        "ta-traffic-bootstrap-v1",
				DecisionScopeID: "scenario",
				DistributionKey: "scenario.default",
				Salt:            "scenario-v1",
			},
			digestHex: "7865911264b77c64b1b096864d470bfbeb6e5e17ad53970c4182ee2d374927d0",
			point:     3956,
		},
		{
			name: "scenario.default s1",
			parts: Parts{
				AssignmentKey:   "SUBJECT_ID",
				AssignmentValue: "s1",
				PolicyID:        "ta-traffic-bootstrap-v1",
				DecisionScopeID: "scenario",
				DistributionKey: "scenario.default",
				Salt:            "scenario-v1",
			},
			digestHex: "f7e11069be2775f1473ee3a2f453d8b82c6cce1b47265b384e1187ad151020af",
			point:     5425,
		},
		{
			name: "empty assignment value",
			parts: Parts{
				AssignmentKey:   "SUBJECT_ID",
				AssignmentValue: "",
				PolicyID:        "ta-traffic-bootstrap-v1",
				DecisionScopeID: "scenario",
				DistributionKey: "scenario.default",
				Salt:            "scenario-v1",
			},
			digestHex: "b51376eae75b20e97965388517511950fbada156bd3330ab7fd65d5adac23f01",
			point:     2809,
		},
		{
			name: "gallery.default subj",
			parts: Parts{
				AssignmentKey:   "SUBJECT_ID",
				AssignmentValue: "subj",
				PolicyID:        "ta-traffic-bootstrap-v1",
				DecisionScopeID: "landing.gallery",
				DistributionKey: "landing.gallery.default",
				Salt:            "gallery-v1",
			},
			digestHex: "f47d951ddbd143fad095d2d2c1f7bc775e6db58e765ba513912a034f9cdd6e29",
			point:     3322,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Digest(tt.parts)
			if err != nil {
				t.Fatal(err)
			}
			got := hex.EncodeToString(d[:])
			if tt.digestHex != "" && got != tt.digestHex {
				t.Fatalf("digest=%s want %s", got, tt.digestHex)
			}
			pt, err := Point(tt.parts, 10000)
			if err != nil {
				t.Fatal(err)
			}
			if pt != tt.point {
				t.Fatalf("point=%d want %d", pt, tt.point)
			}
		})
	}
}

func TestUint16WireEncodingIndependentOfDigestGoldens(t *testing.T) {
	p := Parts{
		AssignmentKey:   "A",
		AssignmentValue: "BC",
		PolicyID:        "",
		DecisionScopeID: "x",
		DistributionKey: "y",
		Salt:            "z",
	}
	var wire []byte
	wire = append(wire, []byte(Algorithm)...)
	wire = append(wire, 0)
	put := func(s string) {
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(len(s)))
		wire = append(wire, buf[:]...)
		wire = append(wire, []byte(s)...)
	}
	for _, s := range p.ordered() {
		put(s)
	}
	want := sha256.Sum256(wire)
	body, err := Body(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(wire) {
		t.Fatalf("Body != uint16BE wire")
	}
	got, err := Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest=%x want %x (uint16BE wire)", got, want)
	}
	if len(wire) != len(Algorithm)+1+2*6+len("A")+len("BC")+len("x")+len("y")+len("z") {
		t.Fatalf("wire length %d", len(wire))
	}
}

func TestNotUint32LengthPrefix(t *testing.T) {
	d, err := Digest(Parts{})
	if err != nil {
		t.Fatal(err)
	}
	const supersededUint32Empty = "f7d6b0f250fe558faceec3422478b8616a6408bb74a82876823cfef714f47f04"
	if hex.EncodeToString(d[:]) == supersededUint32Empty {
		t.Fatal("digest still matches superseded uint32BE encoding")
	}
	h := sha256.New()
	_, _ = h.Write([]byte(Algorithm))
	_, _ = h.Write([]byte{0})
	var buf [4]byte
	for i := 0; i < 6; i++ {
		binary.BigEndian.PutUint32(buf[:], 0)
		_, _ = h.Write(buf[:])
	}
	if hex.EncodeToString(d[:]) == hex.EncodeToString(h.Sum(nil)) {
		t.Fatal("digest matched explicit uint32BE encoding")
	}
}

func TestLookup(t *testing.T) {
	ids := []string{"landing", "redirect"}
	ends := []int{5000, 10000}
	got, err := Lookup(3956, ends, ids)
	if err != nil || got != "landing" {
		t.Fatalf("3956 → %s (%v)", got, err)
	}
	got, err = Lookup(5425, ends, ids)
	if err != nil || got != "redirect" {
		t.Fatalf("5425 → %s (%v)", got, err)
	}
	got, err = Lookup(0, ends, ids)
	if err != nil || got != "landing" {
		t.Fatalf("0 → %s (%v)", got, err)
	}
	got, err = Lookup(9999, ends, ids)
	if err != nil || got != "redirect" {
		t.Fatalf("9999 → %s (%v)", got, err)
	}
}

func TestLookupZeroShare(t *testing.T) {
	ids := []string{"zero", "live"}
	got, err := Lookup(0, []int{0, 10000}, ids)
	if err != nil || got != "live" {
		t.Fatalf("leading zero share: %s (%v)", got, err)
	}
	ids = []string{"a", "dropped", "c"}
	got, err = Lookup(5000, []int{5000, 5000, 10000}, ids)
	if err != nil || got != "c" {
		t.Fatalf("repeated end zero share: %s (%v)", got, err)
	}
	got, err = Lookup(4999, []int{5000, 5000, 10000}, ids)
	if err != nil || got != "a" {
		t.Fatalf("4999 → %s (%v)", got, err)
	}
}

func TestRejectsInvalidUTF8(t *testing.T) {
	p := Parts{AssignmentValue: string([]byte{0xff, 0xfe})}
	if _, err := Digest(p); err == nil {
		t.Fatal("expected invalid UTF-8 error")
	}
}

func TestAssignmentValue256BytesHashes(t *testing.T) {
	p := Parts{AssignmentValue: string(make([]byte, MaxAssignmentBytes))}
	if _, err := Digest(p); err != nil {
		t.Fatalf("256-byte assignmentValue must hash: %v", err)
	}
}

func TestAssignmentValue257BytesRejected(t *testing.T) {
	p := Parts{AssignmentValue: string(make([]byte, MaxAssignmentBytes+1))}
	if _, err := Digest(p); err == nil {
		t.Fatal("257-byte assignmentValue must fail (producer MaxAssignmentBytes=256)")
	}
}

func TestPartTooLong(t *testing.T) {
	p := Parts{PolicyID: string(make([]byte, maxPart+1))}
	if _, err := Digest(p); err == nil {
		t.Fatal("expected part too long")
	}
}

func TestPointRequiresShare(t *testing.T) {
	if _, err := Point(Parts{}, 0); err == nil {
		t.Fatal("expected totalShare error")
	}
}
