package decision

import (
	"encoding/json"
	"fmt"

	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/scenarios/redirect"
	"ta-events-ingest/internal/traffichash"
	"ta-events-ingest/internal/trafficwire"
)

// Prepared — immutable indexes, собранные только на load path.
type Prepared struct {
	program  *trafficwire.Program
	weights  map[string]trafficwire.WeightVector
	variant  map[string]trafficwire.Variant
	scenario map[string]trafficwire.Scenario
	slots    []trafficwire.SlotProgram
}

func Prepare(bundle *runtimeloader.Bundle) (*Prepared, error) {
	if bundle == nil || bundle.Program == nil || bundle.Weights == nil {
		return nil, fmt.Errorf("nil bundle")
	}
	if err := validateExecutablePrograms(bundle.Program); err != nil {
		return nil, err
	}
	p := &Prepared{
		program:  bundle.Program,
		weights:  make(map[string]trafficwire.WeightVector, len(bundle.Weights.Vectors)),
		variant:  make(map[string]trafficwire.Variant, len(bundle.Program.Variants)),
		scenario: make(map[string]trafficwire.Scenario, len(bundle.Program.Scenarios)),
		slots:    append([]trafficwire.SlotProgram(nil), bundle.Program.SlotPrograms...),
	}
	for _, wv := range bundle.Weights.Vectors {
		p.weights[wv.DistributionKey] = wv
	}
	for _, v := range bundle.Program.Variants {
		p.variant[v.ID] = v
	}
	for _, s := range bundle.Program.Scenarios {
		p.scenario[s.ID] = s
	}
	for _, v := range p.variant {
		if v.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		pl, err := trafficwire.ParseRedirect(v.HandlerVersion, v.SchemaVersion, v.Payload)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", v.ID, err)
		}
		if err := validateRedirect(v.ID, pl); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func validateRedirect(variantID string, pl trafficwire.RedirectTarget) error {
	if pl.Handler == trafficwire.HandlerRedirectTarget {
		if pl.Enabled {
			return fmt.Errorf("variant %s: redirect-target-v1 cannot carry a URL; enabled target requires v2", variantID)
		}
		return nil
	}
	if pl.Handler != trafficwire.HandlerRedirectTargetV2 {
		return fmt.Errorf("variant %s: unsupported redirect handler %q", variantID, pl.Handler)
	}
	if !pl.Enabled && pl.URL == "" {
		return nil
	}
	if err := redirect.ValidateTarget(pl.URL); err != nil {
		return fmt.Errorf("variant %s url: %w", variantID, err)
	}
	return nil
}

// RedirectURL возвращает исполняемый HTTPS URL только из redirect-target-v2.
func RedirectURL(pl trafficwire.RedirectTarget) (string, error) {
	if pl.Handler != trafficwire.HandlerRedirectTargetV2 || pl.Schema != 2 {
		return "", fmt.Errorf("redirect URL requires redirect-target-v2")
	}
	if pl.TargetID == "" {
		return "", fmt.Errorf("empty targetId")
	}
	if !pl.Enabled {
		return "", fmt.Errorf("redirect target %s is disabled", pl.TargetID)
	}
	if err := redirect.ValidateTarget(pl.URL); err != nil {
		return "", err
	}
	return pl.URL, nil
}

// Context — явные нейтральные поля. Отсутствующее значение не равно false.
// SubjectID: nil = missing assignment; &"" = empty value (hashed, not fallback).
// TASource/TASub: nil = missing; &"" = present-empty (STRING EXISTS is
// own-property presence). HTTP query adapter may still map empty query to
// absent; corpus/parity vectors must be able to set present-empty here.
type Context struct {
	SubjectID  *string
	IsBot      *bool
	Country    string
	DeviceType string
	TASource   *string
	TASub      *string
}

type SlotDecision struct {
	SlotID         string
	TypeID         string
	SchemaVersion  int
	HandlerVersion string
	VariantID      string
	MatchedRuleID  string
	Payload        json.RawMessage
}

type Decision struct {
	ScenarioID            string
	ScenarioKind          string
	MatchedScenarioRuleID string
	FallbackReasons       []string
	Slots                 []SlotDecision
}

func (p *Prepared) Resolve(ctx Context) (Decision, error) {
	sid, ruleID, fb, err := p.pick(p.program.ScenarioProgram, ctx)
	if err != nil {
		return Decision{}, err
	}
	sc, ok := p.scenario[sid]
	if !ok {
		return Decision{}, fmt.Errorf("unknown scenario %q", sid)
	}
	out := Decision{ScenarioID: sid, ScenarioKind: sc.Kind, MatchedScenarioRuleID: ruleID}
	if fb != "" {
		out.FallbackReasons = append(out.FallbackReasons, fb)
	}
	for _, slot := range p.slots {
		if slot.ScenarioID != sid {
			continue
		}
		vid, slotRule, slotFB, err := p.pick(slot.Program, ctx)
		if err != nil {
			return Decision{}, fmt.Errorf("slot %s: %w", slot.SlotID, err)
		}
		v, ok := p.variant[vid]
		if !ok {
			return Decision{}, fmt.Errorf("unknown variant %q", vid)
		}
		if v.TypeID != slot.TypeID {
			return Decision{}, fmt.Errorf("variant %s type %s != slot %s", vid, v.TypeID, slot.TypeID)
		}
		payload := append(json.RawMessage(nil), v.Payload...)
		out.Slots = append(out.Slots, SlotDecision{
			SlotID:         slot.SlotID,
			TypeID:         slot.TypeID,
			SchemaVersion:  v.SchemaVersion,
			HandlerVersion: v.HandlerVersion,
			VariantID:      vid,
			MatchedRuleID:  slotRule,
			Payload:        payload,
		})
		if slotFB != "" {
			out.FallbackReasons = append(out.FallbackReasons, slotFB)
		}
	}
	return out, nil
}

// ResolveScenario выбирает только scenario (для векторов без hash, как T6 / ru-landing).
func (p *Prepared) ResolveScenario(ctx Context) (scenarioID, kind, matchedRule string, err error) {
	sid, ruleID, _, err := p.pick(p.program.ScenarioProgram, ctx)
	if err != nil {
		return "", "", "", err
	}
	sc, ok := p.scenario[sid]
	if !ok {
		return "", "", "", fmt.Errorf("unknown scenario %q", sid)
	}
	return sid, sc.Kind, ruleID, nil
}

func (p *Prepared) pick(prog trafficwire.DecisionProgram, ctx Context) (choiceID, matchedRule, fallback string, err error) {
	idx := prog.DefaultBranch.VectorIndex
	matchedRule = ""
	// Missing SUBJECT_ID is decided before CONDITION rules. Producer
	// decision-vectors "subject-missing" (country=RU) still selects
	// missingAssignmentChoiceId on the default vector, not ru-landing-only.
	if ctx.SubjectID != nil {
		for _, r := range prog.Rules {
			ok, err := match(r.When, ctx)
			if err != nil {
				return "", "", "", err
			}
			if ok {
				idx = r.Branch.VectorIndex
				matchedRule = r.RuleID
				break
			}
		}
	}
	if idx < 0 || idx >= len(p.program.Vectors) {
		return "", "", "", fmt.Errorf("vectorIndex %d out of range", idx)
	}
	vec := p.program.Vectors[idx]
	id, fb, err := p.assign(prog, vec, ctx.SubjectID)
	if err != nil {
		return "", "", "", err
	}
	return id, matchedRule, fb, nil
}

func missingAssignmentReason(assignmentKey string) string {
	if assignmentKey == "" {
		return "ASSIGNMENT_MISSING"
	}
	return assignmentKey + "_MISSING"
}

func (p *Prepared) assign(prog trafficwire.DecisionProgram, vec trafficwire.Vector, assignment *string) (string, string, error) {
	if assignment == nil {
		if prog.MissingAssignmentChoiceID != "" {
			for _, id := range vec.ChoiceIDs {
				if id == prog.MissingAssignmentChoiceID {
					return id, missingAssignmentReason(prog.AssignmentKey), nil
				}
			}
			return "", "", fmt.Errorf("missing assignment choice %q is not in vector %s", prog.MissingAssignmentChoiceID, vec.DistributionKey)
		}
		if len(vec.ChoiceIDs) == 1 {
			return vec.ChoiceIDs[0], "", nil
		}
		return "", "", fmt.Errorf("missing assignment value for %s", vec.DistributionKey)
	}
	if len(vec.ChoiceIDs) == 1 {
		return vec.ChoiceIDs[0], "", nil
	}
	if p.program == nil {
		return "", "", fmt.Errorf("nil program")
	}
	wv, ok := p.weights[vec.DistributionKey]
	if !ok {
		return "", "", fmt.Errorf("missing weights for %s", vec.DistributionKey)
	}
	point, err := traffichash.Point(traffichash.Parts{
		AssignmentKey:   prog.AssignmentKey,
		AssignmentValue: *assignment,
		PolicyID:        p.program.PolicyID,
		DecisionScopeID: prog.DecisionScopeID,
		DistributionKey: vec.DistributionKey,
		Salt:            prog.Salt,
	}, uint64(p.program.TotalShare))
	if err != nil {
		return "", "", err
	}
	id, err := traffichash.Lookup(point, wv.CumulativeEnds, vec.ChoiceIDs)
	if err != nil {
		return "", "", err
	}
	return id, "", nil
}
