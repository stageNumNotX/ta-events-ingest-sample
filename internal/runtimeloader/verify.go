package runtimeloader

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"ta-events-ingest/internal/trafficwire"
)

const maxArtifactBytes = 2 << 20

type Bundle struct {
	ReleaseBytes []byte
	ProgramBytes []byte
	WeightsBytes []byte
	Release      *trafficwire.Release
	Program      *trafficwire.Program
	Weights      *trafficwire.Weights
}

func LoadDir(dir string) (*Bundle, error) {
	rel, err := readCapped(filepath.Join(dir, "release.json"))
	if err != nil {
		return nil, err
	}
	prog, err := readCapped(filepath.Join(dir, "program.json"))
	if err != nil {
		return nil, err
	}
	w, err := readCapped(filepath.Join(dir, "weights.json"))
	if err != nil {
		return nil, err
	}
	return Verify(rel, prog, w)
}

func readCapped(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxArtifactBytes {
		return nil, fmt.Errorf("%s too large: %d", path, st.Size())
	}
	return os.ReadFile(path)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func Verify(releaseRaw, programRaw, weightsRaw []byte) (*Bundle, error) {
	if len(releaseRaw) == 0 || len(programRaw) == 0 || len(weightsRaw) == 0 {
		return nil, fmt.Errorf("empty artifact bytes")
	}
	if len(releaseRaw) > maxArtifactBytes || len(programRaw) > maxArtifactBytes || len(weightsRaw) > maxArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds size bound")
	}
	programSHA := sha256Hex(programRaw)
	weightsSHA := sha256Hex(weightsRaw)
	rel, err := trafficwire.ParseRelease(releaseRaw)
	if err != nil {
		return nil, err
	}
	if programSHA != rel.Program.SHA256 || len(programRaw) != rel.Program.Bytes {
		return nil, fmt.Errorf("program sha256/bytes mismatch")
	}
	if weightsSHA != rel.Weights.SHA256 || len(weightsRaw) != rel.Weights.Bytes {
		return nil, fmt.Errorf("weights sha256/bytes mismatch")
	}
	prog, err := trafficwire.ParseProgram(programRaw)
	if err != nil {
		return nil, err
	}
	weights, err := trafficwire.ParseWeights(weightsRaw)
	if err != nil {
		return nil, err
	}
	if rel.ArtifactType != trafficwire.ArtifactRelease || rel.CompileTarget != trafficwire.CompileTarget {
		return nil, fmt.Errorf("release artifactType/compileTarget mismatch")
	}
	if prog.ArtifactType != trafficwire.ArtifactProgram || prog.CompileTarget != trafficwire.CompileTarget {
		return nil, fmt.Errorf("program artifactType/compileTarget mismatch")
	}
	if weights.ArtifactType != trafficwire.ArtifactWeights || weights.CompileTarget != trafficwire.CompileTarget {
		return nil, fmt.Errorf("weights artifactType/compileTarget mismatch")
	}
	if rel.LayoutID == "" || rel.LayoutID != prog.LayoutID || rel.LayoutID != weights.LayoutID {
		return nil, fmt.Errorf("layoutId mismatch")
	}
	if prog.HashAlgorithm != trafficwire.HashAlgorithmV1 {
		return nil, fmt.Errorf("unsupported hashAlgorithm %q", prog.HashAlgorithm)
	}
	if err := crossCheck(prog, weights); err != nil {
		return nil, err
	}
	return &Bundle{
		ReleaseBytes: append([]byte(nil), releaseRaw...),
		ProgramBytes: append([]byte(nil), programRaw...),
		WeightsBytes: append([]byte(nil), weightsRaw...),
		Release:      rel,
		Program:      prog,
		Weights:      weights,
	}, nil
}

func crossCheck(prog *trafficwire.Program, weights *trafficwire.Weights) error {
	if prog.TotalShare <= 0 {
		return fmt.Errorf("totalShare must be positive")
	}
	wByKey := make(map[string]trafficwire.WeightVector, len(weights.Vectors))
	for _, wv := range weights.Vectors {
		if _, ok := wByKey[wv.DistributionKey]; ok {
			return fmt.Errorf("duplicate weights key %q", wv.DistributionKey)
		}
		wByKey[wv.DistributionKey] = wv
	}
	for i, vec := range prog.Vectors {
		wv, ok := wByKey[vec.DistributionKey]
		if !ok {
			return fmt.Errorf("missing weights for %q", vec.DistributionKey)
		}
		if len(wv.CumulativeEnds) != len(vec.ChoiceIDs) {
			return fmt.Errorf("vector %d length mismatch", i)
		}
		prev := 0
		for i, end := range wv.CumulativeEnds {
			if end < 0 {
				return fmt.Errorf("negative cumulativeEnds on %q", vec.DistributionKey)
			}
			if i > 0 && end < prev {
				return fmt.Errorf("cumulativeEnds decreased on %q", vec.DistributionKey)
			}
			prev = end
		}
		if prev != prog.TotalShare {
			return fmt.Errorf("cumulativeEnds last != totalShare on %q", vec.DistributionKey)
		}
	}
	if err := checkProgramIndexes(prog); err != nil {
		return err
	}
	return nil
}

func checkProgramIndexes(prog *trafficwire.Program) error {
	nvec := len(prog.Vectors)
	check := func(p trafficwire.DecisionProgram, label string) error {
		if p.DefaultBranch.VectorIndex < 0 || p.DefaultBranch.VectorIndex >= nvec {
			return fmt.Errorf("%s default vectorIndex out of range", label)
		}
		for _, r := range p.Rules {
			if r.Branch.VectorIndex < 0 || r.Branch.VectorIndex >= nvec {
				return fmt.Errorf("%s rule %s vectorIndex out of range", label, r.RuleID)
			}
			if err := trafficwire.ValidateMatcherShape(r.When); err != nil {
				return fmt.Errorf("%s rule %s: %w", label, r.RuleID, err)
			}
		}
		return nil
	}
	if err := check(prog.ScenarioProgram, "scenario"); err != nil {
		return err
	}
	for _, sp := range prog.SlotPrograms {
		if err := check(sp.Program, sp.ScenarioID+"."+sp.SlotID); err != nil {
			return err
		}
	}
	return nil
}
