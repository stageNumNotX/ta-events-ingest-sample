package traffic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

// Contract — verified traffic-runtime/v1 snapshot. GET / captures this
// pointer once and calls Resolve; HTTPReleaseFromBundle is load-path
// evidence only. Fixture PROTOTYPE_MODE is not this path.
type Contract struct {
	activation        string
	compileTarget     string
	layoutID          string
	programSHA256     string
	weightsSHA256     string
	releaseSHA256     string
	prepared          *decision.Prepared
	bundle            *runtimeloader.Bundle
	pointerVersion    int64
	pointerGeneration int64
	artifactSetID     string
	hold              <-chan struct{}
	holdReady         chan struct{}
}

type T6Status struct {
	ContextSource         string `json:"contextSource"`
	ScenarioID            string `json:"scenarioId"`
	ScenarioKind          string `json:"scenarioKind"`
	MatchedScenarioRuleID string `json:"matchedScenarioRuleId"`
	VariantID             string `json:"variantId"`
	HandlerVersion        string `json:"handlerVersion"`
	Enabled               bool   `json:"enabled"`
	HasURL                bool   `json:"hasURL"`
}

type ContractStatus struct {
	Activation                  string   `json:"activation"`
	CompileTarget               string   `json:"compileTarget"`
	LayoutID                    string   `json:"layoutId"`
	ProgramSHA256               string   `json:"programSha256"`
	WeightsSHA256               string   `json:"weightsSha256"`
	ReleaseSHA256               string   `json:"releaseSha256"`
	HTTPRelease                 string   `json:"httpRelease"`
	PointerVersion              int64    `json:"pointerVersion,omitempty"`
	PointerGeneration           int64    `json:"pointerGeneration,omitempty"`
	ArtifactSetID               string   `json:"artifactSetId,omitempty"`
	T6                          T6Status `json:"t6"`
	Request                     T6Status `json:"request"`
	LiveRedirectFromThisRelease bool     `json:"liveRedirectFromThisRelease"`
}

func (c *Contract) Activation() string {
	if c == nil {
		return ""
	}
	return c.activation
}

func (c *Contract) LayoutID() string {
	if c == nil {
		return ""
	}
	return c.layoutID
}

func (c *Contract) PointerVersion() int64 {
	if c == nil {
		return 0
	}
	return c.pointerVersion
}

func (c *Contract) PointerGeneration() int64 {
	if c == nil {
		return 0
	}
	return c.pointerGeneration
}

func (c *Contract) ArtifactSetID() string {
	if c == nil {
		return ""
	}
	return c.artifactSetID
}

func (c *Contract) ReleaseSHA256() string {
	if c == nil {
		return ""
	}
	return c.releaseSHA256
}

func (c *Contract) attachPointer(version, generation int64, artifactSetID string) {
	if c == nil {
		return
	}
	c.pointerVersion = version
	c.pointerGeneration = generation
	c.artifactSetID = artifactSetID
}

func NewContract(bundle *runtimeloader.Bundle, activation string) (*Contract, error) {
	if bundle == nil || bundle.Release == nil {
		return nil, fmt.Errorf("nil bundle")
	}
	p, err := decision.Prepare(bundle)
	if err != nil {
		return nil, err
	}
	return &Contract{
		activation:    activation,
		compileTarget: bundle.Release.CompileTarget,
		layoutID:      bundle.Release.LayoutID,
		programSHA256: bundle.Release.Program.SHA256,
		weightsSHA256: bundle.Release.Weights.SHA256,
		releaseSHA256: shaOf(bundle.ReleaseBytes),
		prepared:      p,
		bundle:        bundle,
	}, nil
}

func (c *Contract) ProbeT6() (T6Status, error) {
	bot := true
	sid := "t6-probe"
	return c.probe(decision.Context{IsBot: &bot, SubjectID: &sid}, "neutral-probe")
}

func (c *Contract) ProbeRequest() (T6Status, error) {
	return c.probe(PrototypeContext(), PrototypeContextSource)
}

func (c *Contract) probe(ctx decision.Context, source string) (T6Status, error) {
	if c == nil || c.prepared == nil {
		return T6Status{}, fmt.Errorf("nil contract")
	}
	d, err := c.prepared.Resolve(ctx)
	if err != nil {
		return T6Status{}, err
	}
	out := T6Status{
		ContextSource:         source,
		ScenarioID:            d.ScenarioID,
		ScenarioKind:          d.ScenarioKind,
		MatchedScenarioRuleID: d.MatchedScenarioRuleID,
	}
	for _, slot := range d.Slots {
		if slot.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		out.VariantID = slot.VariantID
		out.HandlerVersion = slot.HandlerVersion
		pl, err := trafficwire.ParseRedirect(slot.HandlerVersion, slot.SchemaVersion, slot.Payload)
		if err != nil {
			return T6Status{}, err
		}
		out.Enabled = pl.Enabled
		out.HasURL = pl.URL != ""
		break
	}
	return out, nil
}

func (c *Contract) Status(httpRelease string) (ContractStatus, error) {
	t6, err := c.ProbeT6()
	if err != nil {
		return ContractStatus{}, err
	}
	req, err := c.ProbeRequest()
	if err != nil {
		return ContractStatus{}, err
	}
	return ContractStatus{
		Activation:                  c.activation,
		CompileTarget:               c.compileTarget,
		LayoutID:                    c.layoutID,
		ProgramSHA256:               c.programSHA256,
		WeightsSHA256:               c.weightsSHA256,
		ReleaseSHA256:               c.releaseSHA256,
		HTTPRelease:                 httpRelease,
		PointerVersion:              c.pointerVersion,
		PointerGeneration:           c.pointerGeneration,
		ArtifactSetID:               c.artifactSetID,
		T6:                          t6,
		Request:                     req,
		LiveRedirectFromThisRelease: req.Enabled && req.HasURL,
	}, nil
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
