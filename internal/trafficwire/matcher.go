package trafficwire

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ValidateMatcherShape checks predicate shape only: ops, nesting, required
// fields, IN cardinality. Field/operator executability is a Prepare concern.
func ValidateMatcherShape(m Matcher) error {
	return validateMatcherShape(m, 0)
}

func validateMatcherShape(m Matcher, depth int) error {
	if depth > MaxMatcherDepth {
		return fmt.Errorf("matcher nesting exceeds %d", MaxMatcherDepth)
	}
	switch m.Op {
	case OpALL, OpANY:
		if len(m.Rules) == 0 {
			return fmt.Errorf("%s requires rules", m.Op)
		}
		if m.Field != "" || m.Operator != "" || len(m.Value) > 0 || len(m.Values) > 0 || m.Rule != nil {
			return fmt.Errorf("%s must not carry CONDITION fields", m.Op)
		}
		for i := range m.Rules {
			if err := validateMatcherShape(m.Rules[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	case OpNOT:
		if m.Rule == nil {
			return fmt.Errorf("NOT requires rule")
		}
		if len(m.Rules) > 0 || m.Field != "" || m.Operator != "" || len(m.Value) > 0 || len(m.Values) > 0 {
			return fmt.Errorf("NOT must not carry extra predicate fields")
		}
		return validateMatcherShape(*m.Rule, depth+1)
	case OpCondition:
		if m.Field == "" || m.Operator == "" {
			return fmt.Errorf("CONDITION requires field and operator")
		}
		if len(m.Rules) > 0 || m.Rule != nil {
			return fmt.Errorf("CONDITION must not nest rules")
		}
		switch m.Operator {
		case OperatorEQ:
			if len(m.Value) == 0 || len(m.Values) > 0 {
				return fmt.Errorf("EQ requires value")
			}
		case OperatorIN:
			if len(m.Value) > 0 {
				return fmt.Errorf("IN must not carry value")
			}
			n, err := countUniqueIN(m.Values)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("IN requires values")
			}
			if n > MaxINValues {
				return fmt.Errorf("IN exceeds %d values", MaxINValues)
			}
		case OperatorEXISTS:
			if len(m.Value) > 0 || len(m.Values) > 0 {
				return fmt.Errorf("EXISTS must not carry a value")
			}
		default:
			return fmt.Errorf("unsupported operator %q", m.Operator)
		}
		return nil
	default:
		return fmt.Errorf("unsupported matcher op %q", m.Op)
	}
}

func countUniqueIN(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("IN values: %w", err)
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '[' {
		return 0, fmt.Errorf("IN values must be an array")
	}
	seen := make(map[string]struct{})
	n := 0
	for dec.More() {
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return 0, fmt.Errorf("IN values: %w", err)
		}
		key := string(bytes.TrimSpace(item))
		if _, dup := seen[key]; dup {
			return 0, fmt.Errorf("IN values must be unique")
		}
		seen[key] = struct{}{}
		n++
	}
	end, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("IN values: %w", err)
	}
	if end != json.Delim(']') {
		return 0, fmt.Errorf("IN values: expected closing ]")
	}
	return n, nil
}
