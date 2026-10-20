// Package qmacrocontract pins the producer Q-MACRO-CONTRACT oracle.
//
// Producer (compiler-service, PR #71 → 39db6c0) is the sole oracle. The
// compiled OracleDigest is the trust anchor: SHA-256 of the canonical
// JSON map of four file-byte hashes (decision-vectors, program, weights,
// release). A sibling checksum next to vendored bytes is not. Local
// matcher fixtures are not this contract. The gate stays OPEN until
// present-empty works and all packaged producer cases pass unchanged.
package qmacrocontract

import (
	"os"
	"path/filepath"
	"runtime"
)

// OracleDigest is the producer-declared corpus pin
// (compiler-service docs/control-plane/evidence/q-macro-contract).
const OracleDigest = "eddc83fd05225ec71368f4570fce593f30a2d9bef3b8a1abc4981ad6686208f5"

// ProducerCommitPrefix is the merged producer commit that published the
// package (PR #71). Full SHA is recorded from the package README when present.
const ProducerCommitPrefix = "39db6c0"

// ProducerRepo is read-only; this runtime does not commit there.
const ProducerRepo = "stageNumNotX/compiler-service"

// EvidencePath is the producer git path of the evidence directory.
const EvidencePath = "docs/control-plane/evidence/q-macro-contract"

// EvidencePathAlt is the other name used in the consumer handoff.
const EvidencePathAlt = "docs/control-plane/evidence/hotlink-macros"

// EnvDir overrides the vendored location (a read-only producer checkout).
const EnvDir = "TRAFFIC_QMACRO_HANDOFF_DIR"

const (
	DigestFile          = "DIGEST"
	ReadmeFile          = "README.md"
	DecisionVectorsFile = "decision-vectors.json"
	MatcherVectorsFile  = "matcher-vectors.json"
	VectorsFile         = "vectors.json"
	ManifestFile        = "manifest.json"
	InnerManifestFile   = "MANIFEST.json"
)

// BindingOracleFiles are the producer-declared file-byte map whose
// canonical JSON sha256 is OracleDigest. The hex is not the hash of
// decision-vectors.json alone (README: sort_keys, separators `,` `:`).
var BindingOracleFiles = []string{
	DecisionVectorsFile,
	"valid/program.json",
	"valid/weights.json",
	"valid/release.json",
}

func packageDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Dir(file)
}

func CandidateDirs() []string {
	here := packageDir()
	repoRoot := filepath.Clean(filepath.Join(here, "..", ".."))
	return []string{
		filepath.Join(here, "testdata", OracleDigest),
		filepath.Join(repoRoot, "testdata", "traffic-contract", "q-macro-contract"),
		filepath.Join("testdata", "traffic-contract", "q-macro-contract"),
		filepath.Join("..", "qmacrocontract", "testdata", OracleDigest),
	}
}

func DefaultDir() string {
	if d := os.Getenv(EnvDir); d != "" {
		return d
	}
	cands := CandidateDirs()
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return cands[0]
}
