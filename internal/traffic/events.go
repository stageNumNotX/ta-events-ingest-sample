package traffic

import (
	"encoding/json"
	"os"
	"strconv"
	"time"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimepub"
)

// eventTimeFormat совпадает с ingest: RFC3339 UTC с микросекундами.
const eventTimeFormat = "2006-01-02T15:04:05.999999Z07:00"

// clickEventAttrs is recorded on every event of the triple. Campaign/zone
// and cost are event columns only; they are not matcher fields or Location keys.
type clickEventAttrs struct {
	TASource       string
	TASub          string
	CampaignID     string
	ZoneID         string
	Cost           string
	Country        string
	SourceReject   string
	SubReject      string
	CampaignReject string
	ZoneReject     string
	CostReject     string
}

func (h *Handler) emitRedirect(clickID, location, taSource, taSub, httpRelease string) {
	h.emitChain(clickID, location, clickEventAttrs{TASource: taSource, TASub: taSub}, httpRelease, "", eventParamsFrom(nil, map[string]string{
		"http_release": httpRelease,
	}))
}

func (h *Handler) emitResolved(clickID, location string, attrs clickEventAttrs, rr ResolvedRedirect, c *Contract, contextSource string) {
	extra := map[string]string{
		"context_source":           contextSource,
		"scenario_id":              rr.ScenarioID,
		"variant_id":               rr.VariantID,
		"handler_version":          rr.HandlerVersion,
		"matched_scenario_rule_id": rr.MatchedScenarioRuleID,
		"matched_slot_rule_id":     rr.MatchedRuleID,
		"ta_source":                attrs.TASource,
		"ta_sub":                   attrs.TASub,
	}
	if attrs.CampaignID != "" {
		extra["campaign_id"] = attrs.CampaignID
	}
	if attrs.ZoneID != "" {
		extra["zone_id"] = attrs.ZoneID
	}
	if attrs.SourceReject != "" {
		extra["query_ta_source_reject"] = attrs.SourceReject
	}
	if attrs.SubReject != "" {
		extra["query_ta_sub_reject"] = attrs.SubReject
	}
	if attrs.CampaignReject != "" {
		extra["query_campaign_id_reject"] = attrs.CampaignReject
	}
	if attrs.ZoneReject != "" {
		extra["query_zone_id_reject"] = attrs.ZoneReject
	}
	if attrs.CostReject != "" {
		extra["query_cost_reject"] = attrs.CostReject
	}
	h.emitChain(clickID, location, attrs, rr.ScenarioID, rr.VariantID, eventParamsFrom(c, extra))
}

func (h *Handler) emitChain(clickID, location string, attrs clickEventAttrs, scenarioID, variantID string, params map[string]string) {
	if h.enqueue == nil {
		return
	}
	now := time.Now().UTC().Format(eventTimeFormat)
	raw, err := json.Marshal(params)
	if err != nil {
		raw = []byte(`{}`)
	}
	encoded := string(raw)
	events := []ingest.CanonicalEvent{
		h.baseEvent(clickID, ingest.EventIngress, now, attrs, "", "", "", encoded),
		h.baseEvent(clickID, ingest.EventScenarioAssigned, now, attrs, scenarioID, variantID, "", encoded),
		h.baseEvent(clickID, ingest.EventRedirect, now, attrs, scenarioID, variantID, location, encoded),
	}
	for _, ev := range events {
		if !h.enqueue.TryEnqueue(ev) {
			h.log.Error("runtime event enqueue dropped", "event_name", ev.EventName)
		}
	}
}

func eventParamsFrom(c *Contract, extra map[string]string) map[string]string {
	params := map[string]string{
		"k_revision": os.Getenv("K_REVISION"),
		"k_service":  os.Getenv("K_SERVICE"),
	}
	for k, v := range extra {
		if v != "" {
			params[k] = v
		}
	}
	if c != nil {
		params["contract_activation"] = c.activation
		params["contract_layout_id"] = c.layoutID
		params["contract_release_sha256"] = c.releaseSHA256
		if c.pointerVersion != 0 {
			params["pointer_version"] = strconv.FormatInt(c.pointerVersion, 10)
		}
		if c.pointerGeneration != 0 {
			params["pointer_generation"] = strconv.FormatInt(c.pointerGeneration, 10)
		}
		if c.artifactSetID != "" {
			params["artifact_set_id"] = c.artifactSetID
		}
	}
	return params
}

func (h *Handler) baseEvent(clickID, name, now string, attrs clickEventAttrs, scenarioID, variantID, redirectTo, params string) ingest.CanonicalEvent {
	return ingest.CanonicalEvent{
		EventID:            canonicalEventID(clickID, name),
		EventName:          name,
		EventTime:          now,
		ReceivedAt:         now,
		ClickID:            clickID,
		Country:            attrs.Country,
		Path:               "/",
		Method:             "GET",
		TASource:           attrs.TASource,
		TASub:              attrs.TASub,
		CampaignID:         attrs.CampaignID,
		ZoneID:             attrs.ZoneID,
		Cost:               attrs.Cost,
		ScenarioID:         scenarioID,
		SmartlinkVariantID: variantID,
		RedirectTo:         redirectTo,
		Headers:            "{}",
		CF:                 "{}",
		Params:             params,
		Raw:                "{}",
	}
}

// canonicalEventID is stable for a click/name pair. Publish retries reuse the
// same CanonicalEvent and must not mint a new id.
func canonicalEventID(clickID, name string) string {
	return clickID + "/" + name
}

// Enqueuer is the bounded runtime publisher used by the HTTP handler.
type Enqueuer = runtimepub.Enqueuer
