package producerhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	_ "embed"
)

//go:embed testdata/297d8d0c1473fa1dd3608ab91ef42575b3a255be/testdata/iso3166/alpha2.json
var alpha2JSON []byte

// Alpha2SHA256 is the producer pin of testdata/iso3166/alpha2.json
// (manifest.files entry on CONTRACT_READY 297d8d0c). This is not a
// consumer country registry.
const Alpha2SHA256 = "8de0dc83b9c07378a18a26a04f19881121b879ba3eddf364ce2901d69deedda6"

const Alpha2ManifestPath = "testdata/iso3166/alpha2.json"

var (
	officialSet map[string]struct{}
	officialErr error
)

type alpha2Doc struct {
	Codes []string `json:"codes"`
}

func init() {
	officialErr = parseOfficialAlpha2()
}

func parseOfficialAlpha2() error {
	sum := sha256.Sum256(alpha2JSON)
	got := hex.EncodeToString(sum[:])
	if got != Alpha2SHA256 {
		officialSet = nil
		return fmt.Errorf("iso3166/alpha2.json sha256=%s want %s", got, Alpha2SHA256)
	}
	var doc alpha2Doc
	if err := json.Unmarshal(alpha2JSON, &doc); err != nil {
		officialSet = nil
		return err
	}
	set := make(map[string]struct{}, len(doc.Codes))
	for _, c := range doc.Codes {
		if len(c) != 2 || c == "XX" || c == "T1" {
			continue
		}
		set[c] = struct{}{}
	}
	officialSet = set
	return nil
}

// PrepareOfficialAlpha2 returns the init-time pin error. Call from the
// load path so the map is ready before any request. Request path only
// does a map lookup.
func PrepareOfficialAlpha2() error {
	return officialErr
}

// OfficialAlpha2 reports whether code is in the pinned producer ISO 3166-1
// alpha-2 officially-assigned set. No case folding. XX, T1, user-assigned
// (AA/ZZ/QZ), exceptionally reserved UK, XK and unknown values are false.
func OfficialAlpha2(code string) bool {
	if officialErr != nil || officialSet == nil {
		return false
	}
	_, ok := officialSet[code]
	return ok
}
