package producerhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestOfficialAlpha2PinnedSet(t *testing.T) {
	sum := sha256.Sum256(alpha2JSON)
	if hex.EncodeToString(sum[:]) != Alpha2SHA256 {
		t.Fatalf("embedded iso3166 sha256=%s want %s", hex.EncodeToString(sum[:]), Alpha2SHA256)
	}
	for _, code := range []string{"RU", "DE", "US"} {
		if !OfficialAlpha2(code) {
			t.Fatalf("want %s in official set", code)
		}
	}
	for _, code := range []string{"", "XX", "T1", "ru", "AA", "ZZ", "QZ", "UK", "XK", "RUS"} {
		if OfficialAlpha2(code) {
			t.Fatalf("%q must not be official", code)
		}
	}
	if PrepareOfficialAlpha2() != nil {
		t.Fatal("ISO set must be prepared at init, not on first request")
	}
	if !OfficialAlpha2("GB") {
		t.Fatal("GB is officially assigned")
	}
}
