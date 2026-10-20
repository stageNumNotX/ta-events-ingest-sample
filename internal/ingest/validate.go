package ingest

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrValidation is a structural validation failure. The HTTP handler maps it to 400.
var ErrValidation = errors.New("event validation failed")

const (
	maxClickIDLen    = 128
	maxCountryLen    = 8
	maxSourceLen     = 128
	maxIDLen         = 128
	maxURLLen        = 2048
	maxUserAgentLen  = 512
	maxMethodLen     = 16
	maxCostLen       = 32
	maxScenarioLen   = 128
	maxRedirectToLen = 2048
	maxSeedLen       = 128
	maxVariantLen    = 128
)

// Validate checks structural constraints only (no business rules):
//   - event_name is present and in the supported set
//   - click_id is present
//   - string fields stay within size limits (DoS / log-injection bound)
//
// occurred_at is not strictly validated: Canonicalize falls back to received_at.
// Dedup, bot flags, and event-specific completeness live in BigQuery ELT.
func Validate(ev IncomingEvent) error {
	if ev.EventName == "" {
		return fmt.Errorf("%w: event_name is required", ErrValidation)
	}
	if _, ok := allowedEventNames[ev.EventName]; !ok {
		return fmt.Errorf("%w: unknown event_name", ErrValidation)
	}
	if ev.ClickID == "" {
		return fmt.Errorf("%w: click_id is required", ErrValidation)
	}
	if err := utf8Len("click_id", ev.ClickID, maxClickIDLen); err != nil {
		return err
	}
	checks := []struct {
		name string
		val  string
		max  int
	}{
		{"country", ev.Country, maxCountryLen},
		{"ta_source", ev.TASource, maxSourceLen},
		{"ta_sub", ev.TASub, maxSourceLen},
		{"campaign_id", ev.CampaignID, maxIDLen},
		{"zone_id", ev.ZoneID, maxIDLen},
		{"cost", ev.Cost, maxCostLen},
		{"user_agent", ev.UserAgent, maxUserAgentLen},
		{"url", ev.URL, maxURLLen},
		{"method", ev.Method, maxMethodLen},
		{"scenario_id", ev.ScenarioID, maxScenarioLen},
		{"seed", ev.Seed, maxSeedLen},
		{"smartlink_variant_id", ev.SmartlinkVariantID, maxVariantLen},
		{"redirect_to", ev.RedirectTo, maxRedirectToLen},
	}
	for _, c := range checks {
		if err := utf8Len(c.name, c.val, c.max); err != nil {
			return err
		}
	}
	if strings.IndexFunc(ev.ClickID, func(r rune) bool { return r < 32 }) >= 0 {
		return fmt.Errorf("%w: click_id contains control characters", ErrValidation)
	}
	return nil
}

func utf8Len(field, val string, max int) error {
	if val == "" {
		return nil
	}
	if !utf8.ValidString(val) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrValidation, field)
	}
	if utf8.RuneCountInString(val) > max {
		return fmt.Errorf("%w: %s too long", ErrValidation, field)
	}
	return nil
}

// publicValidationMessage returns a low-cardinality client error. It never
// echoes the rejected payload.
func publicValidationMessage(err error) string {
	if err == nil {
		return "invalid event"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "event_name is required"):
		return "event_name is required"
	case strings.Contains(msg, "unknown event_name"):
		return "unknown event_name"
	case strings.Contains(msg, "click_id is required"):
		return "click_id is required"
	case strings.Contains(msg, "too long"):
		return "field too long"
	case strings.Contains(msg, "control characters"):
		return "invalid field"
	case strings.Contains(msg, "not valid UTF-8"):
		return "invalid field"
	default:
		return "invalid event"
	}
}
