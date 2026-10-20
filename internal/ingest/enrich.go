package ingest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// timeFormat — формат timestamp-полей в каноническом событии: RFC3339 в UTC с
// долями секунды, обрезанными до микросекунд. BigQuery TIMESTAMP принимает не
// более 6 знаков после запятой, поэтому наносекунды (которые может дать
// time.Now) недопустимы — иначе строка отвергается при записи в TIMESTAMP-колонку.
const timeFormat = "2006-01-02T15:04:05.999999Z07:00"

// emptyJSON — значение по умолчанию для JSON-полей, чтобы BigQuery JSON-колонки
// всегда получали валидный JSON (пустая строка не парсится как JSON).
const emptyJSON = "{}"

// Canonicalize обогащает входящее событие и маппит его в каноническую схему:
//   - event_id — UUID v4 (crypto/rand), уникален на каждое событие (для дедупа в BQ);
//   - received_at — время приёма сервисом (UTC);
//   - event_time — occurred_at от edge; при пустом/битом значении fallback на received_at;
//   - ip_hash — sha256(ip + salt); сырой IP не сохраняется (PII);
//   - path/search — извлекаются из url;
//   - headers/cf/params — JSON как строка; raw — исходный объект события как пришёл.
func Canonicalize(raw json.RawMessage, ev IncomingEvent, salt string, now time.Time) (CanonicalEvent, error) {
	id, err := newUUIDv4()
	if err != nil {
		return CanonicalEvent{}, fmt.Errorf("generate event_id: %w", err)
	}

	receivedAt := now.UTC()
	eventTime := receivedAt
	if ev.OccurredAt != "" {
		if t, perr := time.Parse(time.RFC3339, ev.OccurredAt); perr == nil {
			eventTime = t.UTC()
		}
	}

	path, search := splitURL(ev.URL)

	return CanonicalEvent{
		EventID:            id,
		EventName:          ev.EventName,
		EventTime:          eventTime.Format(timeFormat),
		ReceivedAt:         receivedAt.Format(timeFormat),
		ClickID:            ev.ClickID,
		Country:            ev.Country,
		Path:               path,
		Search:             search,
		TASource:           ev.TASource,
		TASub:              ev.TASub,
		CampaignID:         ev.CampaignID,
		ZoneID:             ev.ZoneID,
		Cost:               ev.Cost,
		UserAgent:          ev.UserAgent,
		IPHash:             hashIP(ev.IP, salt),
		URL:                ev.URL,
		Method:             ev.Method,
		ScenarioID:         ev.ScenarioID,
		SmartlinkVariantID: ev.SmartlinkVariantID,
		RedirectTo:         ev.RedirectTo,
		Seed:               ev.Seed,
		Headers:            jsonOrDefault(ev.Headers),
		CF:                 jsonOrDefault(ev.CF),
		Params:             jsonOrDefault(ev.Params),
		Raw:                rawOrDefault(raw),
	}, nil
}

// hashIP возвращает hex(sha256(ip + salt)). Для пустого IP возвращает "",
// чтобы не персистить хеш соли без полезной нагрузки.
func hashIP(ip, salt string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip + salt))
	return hex.EncodeToString(sum[:])
}

// splitURL извлекает path и search (query string с ведущим "?") из URL.
func splitURL(raw string) (path, search string) {
	if raw == "" {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	search = u.RawQuery
	if search != "" {
		search = "?" + search
	}
	return u.Path, search
}

func jsonOrDefault(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return emptyJSON
	}
	return string(raw)
}

func rawOrDefault(raw json.RawMessage) string {
	if len(raw) == 0 {
		return emptyJSON
	}
	return string(raw)
}

// newUUIDv4 генерирует UUID версии 4 на crypto/rand (без внешних зависимостей).
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // версия 4
	b[8] = (b[8] & 0x3f) | 0x80 // вариант 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
