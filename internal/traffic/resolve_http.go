package traffic

import (
	"fmt"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/trafficwire"
)

const (
	// PrototypeContextSource is the documented Q-CONTEXT stand-in for
	// GET /. It is not derived from CF-* headers or request query.
	PrototypeContextSource = "prototype-context"
	// QueryV1ContextSource is GET / with TRAFFIC_CONTEXT_MODE=query-v1.
	QueryV1ContextSource = "query-v1"
	// PrototypeAssignmentValue is the prototype SUBJECT_ID. Missing
	// assignment on C1 selects disabled_target; this present value is
	// hashed by the resolver. It is not a variant-id preference.
	PrototypeAssignmentValue = "prototype"
)

// PrototypeContext is the prototype-scoped decision input for GET /.
// Q-CONTEXT remains open for production; this does not read the request.
func PrototypeContext() decision.Context {
	bot := false
	sid := PrototypeAssignmentValue
	return decision.Context{IsBot: &bot, SubjectID: &sid}
}

// QueryV1Context is the request adapter for query-v1. Bot/geo/device stay
// missing. clickID is the per-click SUBJECT_ID assignment, not a cookie.
// taSource/taSub are HTTP-normalized: empty stays absent. Present-empty for
// corpus/parity is represented on decision.Context via *string, not here.
func QueryV1Context(clickID, taSource, taSub string) decision.Context {
	ctx := decision.Context{TASource: presentNonEmpty(taSource), TASub: presentNonEmpty(taSub)}
	if clickID != "" {
		id := clickID
		ctx.SubjectID = &id
	}
	return ctx
}

func presentNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ResolvedRedirect is the selected redirect-target slot after Resolve.
// URL is only the selected variant's redirect-target-v2 payload.url.
type ResolvedRedirect struct {
	ScenarioID            string
	ScenarioKind          string
	MatchedScenarioRuleID string
	SlotID                string
	VariantID             string
	MatchedRuleID         string
	HandlerVersion        string
	SchemaVersion         int
	URL                   string
	FallbackReasons       []string
}

// ResolveRedirect runs the captured Prepared release, then uses only the
// selected redirect-target variant. It does not scan Program.Variants.
func (c *Contract) ResolveRedirect(ctx decision.Context) (ResolvedRedirect, error) {
	if c != nil && c.hold != nil {
		if c.holdReady != nil {
			select {
			case <-c.holdReady:
			default:
				close(c.holdReady)
			}
		}
		<-c.hold
	}
	d, slot, err := c.resolveRedirectSlot(ctx)
	if err != nil {
		return ResolvedRedirect{}, err
	}
	pl, err := trafficwire.ParseRedirect(slot.HandlerVersion, slot.SchemaVersion, slot.Payload)
	if err != nil {
		return ResolvedRedirect{}, err
	}
	url, err := decision.RedirectURL(pl)
	if err != nil {
		return ResolvedRedirect{}, err
	}
	return ResolvedRedirect{
		ScenarioID:            d.ScenarioID,
		ScenarioKind:          d.ScenarioKind,
		MatchedScenarioRuleID: d.MatchedScenarioRuleID,
		SlotID:                slot.SlotID,
		VariantID:             slot.VariantID,
		MatchedRuleID:         slot.MatchedRuleID,
		HandlerVersion:        slot.HandlerVersion,
		SchemaVersion:         slot.SchemaVersion,
		URL:                   url,
		FallbackReasons:       append([]string(nil), d.FallbackReasons...),
	}, nil
}

func (c *Contract) resolveRedirectSlot(ctx decision.Context) (decision.Decision, decision.SlotDecision, error) {
	var zero decision.SlotDecision
	if c == nil || c.prepared == nil {
		return decision.Decision{}, zero, fmt.Errorf("nil contract")
	}
	d, err := c.prepared.Resolve(ctx)
	if err != nil {
		return decision.Decision{}, zero, err
	}
	var found *decision.SlotDecision
	for i := range d.Slots {
		if d.Slots[i].TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		if found != nil {
			return decision.Decision{}, zero, fmt.Errorf("multiple redirect-target slots")
		}
		found = &d.Slots[i]
	}
	if found == nil {
		return decision.Decision{}, zero, fmt.Errorf("decision has no redirect-target slot")
	}
	return d, *found, nil
}
