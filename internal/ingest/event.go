// Package ingest accepts, validates, enriches, and maps traffic events into
// a canonical schema before publish. This service is extract+load only;
// transforms live in BigQuery.
package ingest

import "encoding/json"

// EventName — допустимый тип события (дискриминатор цепочки одного захода).
type EventName = string

const (
	EventIngress          EventName = "ingress"
	EventScenarioAssigned EventName = "scenario_assigned"
	EventRedirect         EventName = "redirect"
	EventPageview         EventName = "pageview"
)

// allowedEventNames — множество поддерживаемых типов событий.
var allowedEventNames = map[string]struct{}{
	EventIngress:          {},
	EventScenarioAssigned: {},
	EventRedirect:         {},
	EventPageview:         {},
}

// IncomingEvent — событие в том виде, как его шлёт edge (producer ta-landing).
// Поля событийно-специфичны: часть присутствует только для некоторых event_name.
type IncomingEvent struct {
	EventName          string          `json:"event_name"`
	ClickID            string          `json:"click_id"`
	OccurredAt         string          `json:"occurred_at"`
	Country            string          `json:"country"`
	TASource           string          `json:"ta_source"`
	TASub              string          `json:"ta_sub"`
	CampaignID         string          `json:"campaign_id"`
	ZoneID             string          `json:"zone_id"`
	Cost               string          `json:"cost"`
	UserAgent          string          `json:"user_agent"`
	IP                 string          `json:"ip"`
	URL                string          `json:"url"`
	Method             string          `json:"method"`
	ScenarioID         string          `json:"scenario_id"`
	Seed               string          `json:"seed"`
	SmartlinkVariantID string          `json:"smartlink_variant_id"`
	RedirectTo         string          `json:"redirect_to"`
	Headers            json.RawMessage `json:"headers"`
	CF                 json.RawMessage `json:"cf"`
	Params             json.RawMessage `json:"params"`
}

// CanonicalEvent — каноническое событие, публикуемое в Pub/Sub (раздел 6 спеки).
// Все поля сериализуются как строки: это совместимо с Avro-схемой topic'а, где
// каждое поле объявлено как string с default "" (без union-обёрток). Поля
// headers/cf/params/raw — это JSON, упакованный в строку; timestamps — RFC3339.
type CanonicalEvent struct {
	EventID            string `json:"event_id"`
	EventName          string `json:"event_name"`
	EventTime          string `json:"event_time"`
	ReceivedAt         string `json:"received_at"`
	ClickID            string `json:"click_id"`
	Country            string `json:"country"`
	Path               string `json:"path"`
	Search             string `json:"search"`
	TASource           string `json:"ta_source"`
	TASub              string `json:"ta_sub"`
	UserAgent          string `json:"user_agent"`
	IPHash             string `json:"ip_hash"`
	URL                string `json:"url"`
	Method             string `json:"method"`
	ScenarioID         string `json:"scenario_id"`
	SmartlinkVariantID string `json:"smartlink_variant_id"`
	RedirectTo         string `json:"redirect_to"`
	Seed               string `json:"seed"`
	Headers            string `json:"headers"`
	CF                 string `json:"cf"`
	Params             string `json:"params"`
	Raw                string `json:"raw"`
	CampaignID         string `json:"campaign_id"`
	ZoneID             string `json:"zone_id"`
	Cost               string `json:"cost"`
}
