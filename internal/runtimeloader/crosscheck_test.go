package runtimeloader

import (
	"testing"

	"ta-events-ingest/internal/trafficwire"
)

func TestZeroShareCumulativeEndsAllowed(t *testing.T) {
	prog := &trafficwire.Program{
		TotalShare: 10000,
		Vectors: []trafficwire.Vector{
			{DistributionKey: "k", ChoiceIDs: []string{"a", "b", "c"}},
		},
		ScenarioProgram: trafficwire.DecisionProgram{DefaultBranch: trafficwire.Branch{VectorIndex: 0}},
	}
	weights := &trafficwire.Weights{
		Vectors: []trafficwire.WeightVector{
			{DistributionKey: "k", CumulativeEnds: []int{0, 5000, 10000}},
		},
	}
	if err := crossCheck(prog, weights); err != nil {
		t.Fatal(err)
	}
	weights.Vectors[0].CumulativeEnds = []int{5000, 5000, 10000}
	if err := crossCheck(prog, weights); err != nil {
		t.Fatal(err)
	}
	weights.Vectors[0].CumulativeEnds = []int{6000, 4000, 10000}
	if err := crossCheck(prog, weights); err == nil {
		t.Fatal("decreasing cumulativeEnds must fail")
	}
}
