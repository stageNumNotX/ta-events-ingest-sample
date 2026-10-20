package decision

import (
	"encoding/json"
	"fmt"

	"ta-events-ingest/internal/trafficwire"
)

type fieldKind int

const (
	fieldKindString fieldKind = iota
	fieldKindBool
	fieldKindEnum
)

type fieldSpec struct {
	kind fieldKind
	ops  map[string]struct{}
}

func ops(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// executableRegistry is the consumer-executable subset of pinned context
// registry 1. It is not a competing field registry: unsupported registered
// names (region, city, os, …) and unknown names fail load rather than match.
var executableRegistry = map[string]fieldSpec{
	"country":    {fieldKindString, ops(trafficwire.OperatorEQ, trafficwire.OperatorIN, trafficwire.OperatorEXISTS)},
	"deviceType": {fieldKindEnum, ops(trafficwire.OperatorEQ, trafficwire.OperatorIN)},
	"isBot":      {fieldKindBool, ops(trafficwire.OperatorEQ)},
	"ta_source":  {fieldKindString, ops(trafficwire.OperatorEQ, trafficwire.OperatorIN, trafficwire.OperatorEXISTS)},
	"ta_sub":     {fieldKindString, ops(trafficwire.OperatorEQ, trafficwire.OperatorIN, trafficwire.OperatorEXISTS)},
}

func validateExecutablePrograms(prog *trafficwire.Program) error {
	if prog == nil {
		return fmt.Errorf("nil program")
	}
	if err := validateDecisionProgram(prog.ScenarioProgram, "scenario"); err != nil {
		return err
	}
	for _, sp := range prog.SlotPrograms {
		label := sp.ScenarioID + "." + sp.SlotID
		if err := validateDecisionProgram(sp.Program, label); err != nil {
			return err
		}
	}
	return nil
}

func validateDecisionProgram(p trafficwire.DecisionProgram, label string) error {
	if p.AssignmentKey != trafficwire.AssignmentSubjectID {
		return fmt.Errorf("%s: unsupported assignmentKey %q", label, p.AssignmentKey)
	}
	for _, r := range p.Rules {
		if err := trafficwire.ValidateMatcherShape(r.When); err != nil {
			return fmt.Errorf("%s rule %s: %w", label, r.RuleID, err)
		}
		if err := validateExecutableMatcher(r.When); err != nil {
			return fmt.Errorf("%s rule %s: %w", label, r.RuleID, err)
		}
	}
	return nil
}

func validateExecutableMatcher(m trafficwire.Matcher) error {
	switch m.Op {
	case trafficwire.OpALL, trafficwire.OpANY:
		for i := range m.Rules {
			if err := validateExecutableMatcher(m.Rules[i]); err != nil {
				return err
			}
		}
		return nil
	case trafficwire.OpNOT:
		if m.Rule == nil {
			return fmt.Errorf("NOT requires rule")
		}
		return validateExecutableMatcher(*m.Rule)
	case trafficwire.OpCondition:
		spec, ok := executableRegistry[m.Field]
		if !ok {
			return fmt.Errorf("unsupported field %q", m.Field)
		}
		if _, ok := spec.ops[m.Operator]; !ok {
			return fmt.Errorf("unsupported operator %s on %s", m.Operator, m.Field)
		}
		switch m.Operator {
		case trafficwire.OperatorEQ:
			return validateEQValue(spec, m.Field, m.Value)
		case trafficwire.OperatorIN:
			return validateINValues(spec, m.Field, m.Values)
		case trafficwire.OperatorEXISTS:
			return nil
		default:
			return fmt.Errorf("unsupported operator %q", m.Operator)
		}
	default:
		return fmt.Errorf("unsupported matcher op %q", m.Op)
	}
}

func validateEQValue(spec fieldSpec, field string, raw json.RawMessage) error {
	switch spec.kind {
	case fieldKindBool:
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %s EQ: boolean value required", field)
		}
		return nil
	case fieldKindString, fieldKindEnum:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %s EQ: string value required", field)
		}
		return nil
	default:
		return fmt.Errorf("field %s: unknown type", field)
	}
}

func validateINValues(spec fieldSpec, field string, raw json.RawMessage) error {
	switch spec.kind {
	case fieldKindBool:
		var v []bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %s IN: boolean values required", field)
		}
		if len(v) == 0 {
			return fmt.Errorf("field %s IN: empty values", field)
		}
		return nil
	case fieldKindString, fieldKindEnum:
		var v []string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %s IN: string values required", field)
		}
		if len(v) == 0 {
			return fmt.Errorf("field %s IN: empty values", field)
		}
		return nil
	default:
		return fmt.Errorf("field %s: unknown type", field)
	}
}

// EvalMatcher is the producer-corpus entry for a matcher tree. Shape and
// executable-registry checks run first so an unsupported field/operator is a
// vector failure, not a silent miss.
func EvalMatcher(m trafficwire.Matcher, ctx Context) (bool, error) {
	if err := trafficwire.ValidateMatcherShape(m); err != nil {
		return false, err
	}
	if err := validateExecutableMatcher(m); err != nil {
		return false, err
	}
	return match(m, ctx)
}

func match(c trafficwire.Matcher, ctx Context) (bool, error) {
	return matchAt(c, ctx, 0)
}

func matchAt(m trafficwire.Matcher, ctx Context, depth int) (bool, error) {
	if depth > trafficwire.MaxMatcherDepth {
		return false, fmt.Errorf("matcher nesting exceeds %d", trafficwire.MaxMatcherDepth)
	}
	switch m.Op {
	case trafficwire.OpALL:
		for i := range m.Rules {
			ok, err := matchAt(m.Rules[i], ctx, depth+1)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	case trafficwire.OpANY:
		if len(m.Rules) == 0 {
			return false, fmt.Errorf("ANY requires rules")
		}
		for i := range m.Rules {
			ok, err := matchAt(m.Rules[i], ctx, depth+1)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case trafficwire.OpNOT:
		if m.Rule == nil {
			return false, fmt.Errorf("NOT requires rule")
		}
		ok, err := matchAt(*m.Rule, ctx, depth+1)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case trafficwire.OpCondition:
		return matchCondition(m, ctx)
	default:
		return false, fmt.Errorf("unsupported matcher op %q", m.Op)
	}
}

func matchCondition(m trafficwire.Matcher, ctx Context) (bool, error) {
	got, ok := ctx.field(m.Field)
	switch m.Operator {
	case trafficwire.OperatorEXISTS:
		return ok, nil
	case trafficwire.OperatorEQ:
		if !ok {
			return false, nil
		}
		return eqValue(got, m.Value)
	case trafficwire.OperatorIN:
		if !ok {
			return false, nil
		}
		return inValues(got, m.Values)
	default:
		return false, fmt.Errorf("unsupported operator %q", m.Operator)
	}
}

func eqValue(got any, raw json.RawMessage) (bool, error) {
	switch g := got.(type) {
	case bool:
		var want bool
		if err := json.Unmarshal(raw, &want); err != nil {
			return false, err
		}
		return g == want, nil
	case string:
		var want string
		if err := json.Unmarshal(raw, &want); err != nil {
			return false, err
		}
		return g == want, nil
	default:
		return false, fmt.Errorf("unsupported field type %T", got)
	}
}

func inValues(got any, raw json.RawMessage) (bool, error) {
	switch g := got.(type) {
	case bool:
		var want []bool
		if err := json.Unmarshal(raw, &want); err != nil {
			return false, err
		}
		for _, w := range want {
			if g == w {
				return true, nil
			}
		}
		return false, nil
	case string:
		var want []string
		if err := json.Unmarshal(raw, &want); err != nil {
			return false, err
		}
		for _, w := range want {
			if g == w {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported field type %T", got)
	}
}

func (ctx Context) field(name string) (any, bool) {
	switch name {
	case "isBot":
		if ctx.IsBot == nil {
			return nil, false
		}
		return *ctx.IsBot, true
	case "country":
		if ctx.Country == "" {
			return nil, false
		}
		return ctx.Country, true
	case "deviceType":
		if ctx.DeviceType == "" {
			return nil, false
		}
		return ctx.DeviceType, true
	case "ta_source":
		// nil = missing; non-nil including &"" = present (own-property).
		if ctx.TASource == nil {
			return nil, false
		}
		return *ctx.TASource, true
	case "ta_sub":
		if ctx.TASub == nil {
			return nil, false
		}
		return *ctx.TASub, true
	default:
		return nil, false
	}
}
