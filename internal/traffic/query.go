package traffic

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"ta-events-ingest/internal/producerhandoff"
)

const (
	maxRawQueryBytes   = 4096
	maxQueryValueRunes = 128
	queryKeyTASource   = "ta_source"
	queryKeyTASub      = "ta_sub"
	queryKeyCampaignID = "campaign_id"
	queryKeyZoneID     = "zone_id"
	queryKeyCost       = "cost"
	packedIDSource     = "exoclick"
	rejectTooLong      = "too_long"
	rejectInvalidUTF8  = "invalid_utf8"
	rejectControlChars = "control_chars"
	queryErrTooLong    = "too_long"
	queryErrMalformed  = "malformed"
	queryErrDuplicate  = "duplicate_key"
	rejectMacroBraces  = "macro_braces"
	edgeTokenHeader    = "X-Traffic-Edge-Token"
	envEdgeToken       = "TRAFFIC_EDGE_TOKEN"
)

var packedSubRe = regexp.MustCompile(`^([0-9]{1,20})_([0-9]{1,20})$`)

// QueryError is a low-cardinality HTTP adapter failure. It never carries
// the rejected value.
type QueryError struct {
	Status int
	Reason string
}

func (e *QueryError) Error() string {
	if e == nil {
		return "query error"
	}
	return e.Reason
}

// AllowlistedQuery is one decode of query-v1 keys. Unknown keys are dropped.
// campaign_id / zone_id / cost are event attrs, not decision context.
type AllowlistedQuery struct {
	TASource   string
	TASub      string
	CampaignID string
	ZoneID     string
	Cost       string
}

// ParseAllowlistedQuery enforces the Q-MACRO-NORMALIZE HTTP boundary:
// raw length, parse errors, duplicate allowlisted keys. It does not
// normalize values.
func ParseAllowlistedQuery(raw string) (AllowlistedQuery, error) {
	if len(raw) > maxRawQueryBytes {
		return AllowlistedQuery{}, &QueryError{Status: 414, Reason: queryErrTooLong}
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return AllowlistedQuery{}, &QueryError{Status: 400, Reason: queryErrMalformed}
	}
	for _, key := range []string{queryKeyTASource, queryKeyTASub, queryKeyCampaignID, queryKeyZoneID, queryKeyCost} {
		if len(values[key]) > 1 {
			return AllowlistedQuery{}, &QueryError{Status: 400, Reason: queryErrDuplicate}
		}
	}
	out := AllowlistedQuery{}
	if v := values[queryKeyTASource]; len(v) == 1 {
		out.TASource = v[0]
	}
	if v := values[queryKeyTASub]; len(v) == 1 {
		out.TASub = v[0]
	}
	if v := values[queryKeyCampaignID]; len(v) == 1 {
		out.CampaignID = v[0]
	}
	if v := values[queryKeyZoneID]; len(v) == 1 {
		out.ZoneID = v[0]
	}
	if v := values[queryKeyCost]; len(v) == 1 {
		out.Cost = v[0]
	}
	return out, nil
}

// NormalizeQueryValue applies HTTP adapter rules: missing/empty → absent;
// invalid UTF-8, controls, or too many code points → absent + reason.
// No truncation, case folding, or second decode. This is not producer
// STRING EXISTS (own-property); resolver context can still hold present-empty.
func NormalizeQueryValue(v string) (string, string) {
	if v == "" {
		return "", ""
	}
	if !utf8.ValidString(v) {
		return "", rejectInvalidUTF8
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", rejectControlChars
	}
	if utf8.RuneCountInString(v) > maxQueryValueRunes {
		return "", rejectTooLong
	}
	return v, ""
}

// NormalizeIDValue is for campaign_id / zone_id only. Unexpanded macros
// (literal braces after one URL decode) are absent so packed fallback
// can still fill. Opaque ta_source / ta_sub must use NormalizeQueryValue.
func NormalizeIDValue(v string) (string, string) {
	v, reason := NormalizeQueryValue(v)
	if v == "" {
		return "", reason
	}
	if strings.ContainsAny(v, "{}") {
		return "", rejectMacroBraces
	}
	return v, ""
}

// FillPackedIDs copies zone/campaign from ta_sub when ta_source uses the
// packed digits_digits encoding of one affiliate source. Each explicit
// field wins independently. Other sources are not unpacked. Unexpanded
// macros (braces) do not match.
func FillPackedIDs(taSource, taSub, zoneID, campaignID string) (string, string) {
	if taSource != packedIDSource {
		return zoneID, campaignID
	}
	m := packedSubRe.FindStringSubmatch(taSub)
	if m == nil {
		return zoneID, campaignID
	}
	if zoneID == "" {
		zoneID = m[1]
	}
	if campaignID == "" {
		campaignID = m[2]
	}
	return zoneID, campaignID
}

// EventCountryFromCF maps a CF-IPCountry value onto the event country
// column only after membership in the pinned producer ISO alpha-2 set.
// It must not be copied into decision.Context. No case folding.
func EventCountryFromCF(v string) string {
	if !producerhandoff.OfficialAlpha2(v) {
		return ""
	}
	return v
}

func queryErrorMessage(err error) string {
	qe, ok := err.(*QueryError)
	if !ok || qe == nil {
		return "bad query"
	}
	switch qe.Reason {
	case queryErrTooLong:
		return "query too long"
	case queryErrDuplicate:
		return "duplicate query key"
	default:
		return "bad query"
	}
}

func queryErrorStatus(err error) int {
	if qe, ok := err.(*QueryError); ok && qe != nil && qe.Status != 0 {
		return qe.Status
	}
	return 400
}
