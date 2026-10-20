package traffic

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"ta-events-ingest/internal/ingest"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

const (
	queryDefaultURL = "https://example.com/?campaign=query-default"
	querySourceURL  = "https://example.com/?campaign=query-source"
	queryDefaultID  = "query-default"
	querySourceID   = "query-source"
	testEdgeToken   = "test-edge-token"
)

func queryV1Handler(t *testing.T, b *runtimeloader.Bundle) (*Handler, *recEvents) {
	t.Helper()
	c, err := NewContract(b, ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetContextMode(ContextModeQueryV1)
	h.mint = func() (string, error) { return "click-query-v1", nil }
	h.StoreContract(c)
	q := &recEvents{}
	h.SetEnqueue(q)
	return h, q
}

func queryMatchBundle(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b := twoExecutableBundle(t)
	b.Program.Variants = append(b.Program.Variants,
		trafficwire.Variant{
			ID:             queryDefaultID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"` + queryDefaultID + `","url":"` + queryDefaultURL + `"}`),
		},
		trafficwire.Variant{
			ID:             querySourceID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"` + querySourceID + `","url":"` + querySourceURL + `"}`),
		},
	)
	b.Program.Vectors = append([]trafficwire.Vector(nil), b.Program.Vectors...)
	for i, vec := range b.Program.Vectors {
		if vec.DistributionKey == "redirect.target.default" {
			b.Program.Vectors[i].ChoiceIDs = []string{queryDefaultID}
		}
	}
	vec := trafficwire.Vector{
		DistributionKey: "redirect.target.rule.source-eq",
		ChoiceDomain:    trafficwire.ChoiceDomainVariant,
		ChoiceIDs:       []string{querySourceID},
	}
	b.Program.Vectors = append(b.Program.Vectors, vec)
	idx := len(b.Program.Vectors) - 1
	b.Program.SlotPrograms = append([]trafficwire.SlotProgram(nil), b.Program.SlotPrograms...)
	for i, sp := range b.Program.SlotPrograms {
		if sp.ScenarioID != "redirect" || sp.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		rule := trafficwire.Rule{
			RuleID: "source-network-a",
			When: trafficwire.Matcher{
				Op:       trafficwire.OpCondition,
				Field:    "ta_source",
				Operator: trafficwire.OperatorEQ,
				Value:    json.RawMessage(`"network-a"`),
			},
			Branch: trafficwire.Branch{VectorIndex: idx},
		}
		b.Program.SlotPrograms[i].Program.Rules = append([]trafficwire.Rule{rule}, append([]trafficwire.Rule(nil), sp.Program.Rules...)...)
	}
	return b
}

func queryExistsBundle(t *testing.T) *runtimeloader.Bundle {
	t.Helper()
	b := twoExecutableBundle(t)
	b.Program.Variants = append(b.Program.Variants,
		trafficwire.Variant{
			ID:             queryDefaultID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"` + queryDefaultID + `","url":"` + queryDefaultURL + `"}`),
		},
		trafficwire.Variant{
			ID:             querySourceID,
			TypeID:         trafficwire.TypeRedirectTarget,
			SchemaVersion:  2,
			HandlerVersion: trafficwire.HandlerRedirectTargetV2,
			Payload:        json.RawMessage(`{"enabled":true,"targetId":"` + querySourceID + `","url":"` + querySourceURL + `"}`),
		},
	)
	b.Program.Vectors = append([]trafficwire.Vector(nil), b.Program.Vectors...)
	for i, vec := range b.Program.Vectors {
		if vec.DistributionKey == "redirect.target.default" {
			b.Program.Vectors[i].ChoiceIDs = []string{queryDefaultID}
		}
	}
	vec := trafficwire.Vector{
		DistributionKey: "redirect.target.rule.source-exists",
		ChoiceDomain:    trafficwire.ChoiceDomainVariant,
		ChoiceIDs:       []string{querySourceID},
	}
	b.Program.Vectors = append(b.Program.Vectors, vec)
	idx := len(b.Program.Vectors) - 1
	b.Program.SlotPrograms = append([]trafficwire.SlotProgram(nil), b.Program.SlotPrograms...)
	for i, sp := range b.Program.SlotPrograms {
		if sp.ScenarioID != "redirect" || sp.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		rule := trafficwire.Rule{
			RuleID: "source-exists",
			When: trafficwire.Matcher{
				Op:       trafficwire.OpCondition,
				Field:    "ta_source",
				Operator: trafficwire.OperatorEXISTS,
			},
			Branch: trafficwire.Branch{VectorIndex: idx},
		}
		b.Program.SlotPrograms[i].Program.Rules = append([]trafficwire.Rule{rule}, append([]trafficwire.Rule(nil), sp.Program.Rules...)...)
	}
	return b
}

func TestQueryV1PresentSourceChangesMatch(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=campaign-42", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("Cache-Control")
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("click_id") != "click-query-v1" {
		t.Fatalf("click_id=%q", u.Query().Get("click_id"))
	}
	if u.Query().Get("ta_source") != "network-a" || u.Query().Get("ta_sub") != "campaign-42" {
		t.Fatalf("subs %q", loc)
	}
	if !strings.Contains(loc, "campaign=query-source") {
		t.Fatalf("matching did not use ta_source: %q", loc)
	}
	evs := q.events()
	if len(evs) != 3 {
		t.Fatalf("enqueued=%d", len(evs))
	}
	seen := map[string]ingest.CanonicalEvent{}
	for _, ev := range evs {
		seen[ev.EventName] = ev
		if ev.ClickID != "click-query-v1" {
			t.Fatalf("click_id %q", ev.ClickID)
		}
		if ev.TASource != "network-a" || ev.TASub != "campaign-42" {
			t.Fatalf("canonical source/sub %+v", ev)
		}
		if ev.CampaignID != "" || ev.ZoneID != "" {
			t.Fatalf("ids must stay empty without query keys: %+v", ev)
		}
		if ev.Headers != "{}" || ev.CF != "{}" || ev.Raw != "{}" {
			t.Fatalf("must not copy request headers/cf/raw: %+v", ev)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["context_source"] != QueryV1ContextSource {
			t.Fatalf("context_source %q", params["context_source"])
		}
		if params["ta_source"] != "network-a" || params["ta_sub"] != "campaign-42" {
			t.Fatalf("params %+v", params)
		}
		if params["contract_release_sha256"] == "" {
			t.Fatal("missing captured release digest")
		}
	}
	if seen[ingest.EventRedirect].SmartlinkVariantID != querySourceID {
		t.Fatalf("variant %q", seen[ingest.EventRedirect].SmartlinkVariantID)
	}
}

func TestQueryV1AbsentKeepsDefault(t *testing.T) {
	h, _ := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("%q", rec.Header().Get("Location"))
	}
}

func TestQueryV1HTTPEmptyDoesNotSatisfyExists(t *testing.T) {
	h, _ := queryV1Handler(t, queryExistsBundle(t))
	empty := httptest.NewRecorder()
	h.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/?ta_source=&ta_sub=", nil))
	if empty.Code != http.StatusFound || !strings.Contains(empty.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("HTTP empty → absent, EXISTS must not match: %d %q", empty.Code, empty.Header().Get("Location"))
	}
	omitted := httptest.NewRecorder()
	h.ServeHTTP(omitted, httptest.NewRequest(http.MethodGet, "/", nil))
	if omitted.Code != http.StatusFound || !strings.Contains(omitted.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("omitted → absent: %d %q", omitted.Code, omitted.Header().Get("Location"))
	}
	present := httptest.NewRecorder()
	h.ServeHTTP(present, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil))
	if present.Code != http.StatusFound || !strings.Contains(present.Header().Get("Location"), "campaign=query-source") {
		t.Fatalf("nonempty EXISTS: %d %q", present.Code, present.Header().Get("Location"))
	}
}

func TestQueryV1EmptyInvalidOversizeDuplicate(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))

	empty := httptest.NewRecorder()
	h.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/?ta_source=&ta_sub=", nil))
	if empty.Code != http.StatusFound || !strings.Contains(empty.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("empty → absent: %d %q", empty.Code, empty.Header().Get("Location"))
	}

	dup := httptest.NewRecorder()
	h.ServeHTTP(dup, httptest.NewRequest(http.MethodGet, "/?ta_source=a&ta_source=b", nil))
	if dup.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status=%d", dup.Code)
	}
	if dup.Header().Get("Location") != "" {
		t.Fatal("400 must not redirect")
	}
	if q.events() != nil && len(q.events()) != 3 {
		// only the empty request should have enqueued
	}
	if n := len(q.events()); n != 3 {
		t.Fatalf("duplicate must not enqueue, have %d", n)
	}

	oversize := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "ta_source=" + strings.Repeat("a", maxRawQueryBytes)
	h.ServeHTTP(oversize, req)
	if oversize.Code != http.StatusRequestURITooLong {
		t.Fatalf("oversize status=%d", oversize.Code)
	}

	badUTF := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "ta_source=" + url.QueryEscape(string([]byte{0xff}))
	h.ServeHTTP(badUTF, req)
	if badUTF.Code != http.StatusFound || !strings.Contains(badUTF.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("invalid value must be absent still 302: %d %q", badUTF.Code, badUTF.Header().Get("Location"))
	}
	u, _ := url.Parse(badUTF.Header().Get("Location"))
	if u.Query().Get("ta_source") != "" {
		t.Fatalf("invalid value leaked to Location %q", badUTF.Header().Get("Location"))
	}

	longVal := httptest.NewRecorder()
	runes := strings.Repeat("x", maxQueryValueRunes+1)
	if utf8.RuneCountInString(runes) != maxQueryValueRunes+1 {
		t.Fatal("setup")
	}
	h.ServeHTTP(longVal, httptest.NewRequest(http.MethodGet, "/?ta_source="+runes, nil))
	if longVal.Code != http.StatusFound {
		t.Fatalf("oversize value status=%d", longVal.Code)
	}
	lu, _ := url.Parse(longVal.Header().Get("Location"))
	if lu.Query().Get("ta_source") != "" {
		t.Fatal("oversize value must not substitute")
	}
}

func TestQueryV1IgnoresUnknownKeysAndCFHeaders(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&m_campaign=spoof&country=RU&isBot=true&url=https://evil.example/", nil)
	req.Header.Set("CF-IPCountry", "RU")
	req.Header.Set("CF-Bot-Score", "1")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "evil.example") || strings.Contains(loc, "m_campaign") {
		t.Fatalf("spoof leaked: %q", loc)
	}
	if !strings.Contains(loc, "campaign=query-source") {
		t.Fatalf("CF/spoofed keys must not hide ta_source match: %q", loc)
	}
	for _, ev := range q.events() {
		if ev.CF != "{}" || strings.Contains(ev.Params, "m_campaign") || strings.Contains(ev.Params, "1.2.3.4") {
			t.Fatalf("event leaked untrusted data: %+v", ev)
		}
		if ev.Country != "" {
			t.Fatalf("unauthenticated CF-IPCountry must not record country: %+v", ev)
		}
	}
}

func TestQueryV1ClickIDIsAssignmentAndEventID(t *testing.T) {
	ctx := QueryV1Context("click-query-v1", "network-a", "campaign-42")
	if ctx.SubjectID == nil || *ctx.SubjectID != "click-query-v1" {
		t.Fatalf("subject %+v", ctx.SubjectID)
	}
	if ctx.IsBot != nil || ctx.Country != "" || ctx.DeviceType != "" {
		t.Fatalf("must not manufacture bot/geo: %+v", ctx)
	}
	if ctx.TASource == nil || *ctx.TASource != "network-a" || ctx.TASub == nil || *ctx.TASub != "campaign-42" {
		t.Fatalf("%+v", ctx)
	}
}

func TestQueryV1ContextHTTPEmptyIsAbsent(t *testing.T) {
	ctx := QueryV1Context("click-query-v1", "", "")
	if ctx.TASource != nil || ctx.TASub != nil {
		t.Fatalf("HTTP empty query must stay absent: %+v", ctx)
	}
	ctx = QueryV1Context("click-query-v1", "network-a", "")
	if ctx.TASource == nil || *ctx.TASource != "network-a" || ctx.TASub != nil {
		t.Fatalf("nonempty source + empty sub: %+v", ctx)
	}
}

func TestQueryV1HEADAndNoRelease(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetContextMode(ContextModeQueryV1)
	called := false
	h.mint = func() (string, error) {
		called = true
		return "nope", nil
	}
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/?ta_source=network-a", nil))
	if head.Code != http.StatusMethodNotAllowed || called {
		t.Fatalf("HEAD status=%d minted=%v", head.Code, called)
	}
	root := httptest.NewRecorder()
	h.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil))
	if root.Code != http.StatusServiceUnavailable || root.Header().Get("Location") != "" {
		t.Fatalf("no-release %d %q", root.Code, root.Header().Get("Location"))
	}
}

func TestQueryV1DisabledTargetNoEvents(t *testing.T) {
	b, err := runtimeloader.LoadDir("../runtimeloader/testdata/pinned")
	if err != nil {
		t.Fatal(err)
	}
	h, q := queryV1Handler(t, b)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Location") != "" {
		t.Fatalf("disabled/non-redirect %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(q.events()) != 0 {
		t.Fatalf("failed selection must not emit: %d", len(q.events()))
	}
}

func TestQueryV1MalformedPercentIs400(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "ta_source=%zz"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	if len(q.events()) != 0 {
		t.Fatal("400 must not enqueue")
	}
}

func TestQueryV1QueueFullStill302(t *testing.T) {
	c, err := NewContract(queryMatchBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetContextMode(ContextModeQueryV1)
	h.mint = func() (string, error) { return "click-full", nil }
	h.StoreContract(c)
	h.SetEnqueue(&dropQ{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPrototypeModeIgnoresQueryV1Rules(t *testing.T) {
	c, err := NewContract(queryMatchBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "proto", nil }
	h.StoreContract(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_source=other", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("prototype duplicate must not 400, status=%d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "campaign=query-default") {
		t.Fatalf("prototype must not use query matcher: %q", rec.Header().Get("Location"))
	}
}

func TestQueryV1ExplicitCampaignZoneOnAllEventsNotLocation(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=keep&zone_id=987654&campaign_id=1234567", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("zone_id") != "" || u.Query().Get("campaign_id") != "" {
		t.Fatalf("zone/campaign must not be Location substitutions: %q", loc)
	}
	if u.Query().Get("ta_sub") != "keep" {
		t.Fatalf("ta_sub Location %q", loc)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	for _, ev := range q.events() {
		if ev.ZoneID != "987654" || ev.CampaignID != "1234567" || ev.TASub != "keep" {
			t.Fatalf("attrs %+v", ev)
		}
	}
}

func TestQueryV1PackedAndIndependentExplicit(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=exoclick&ta_sub=111_222&zone_id=999", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.ZoneID != "999" || ev.CampaignID != "222" {
			t.Fatalf("explicit zone wins, packed fills campaign: %+v", ev)
		}
	}
}

func TestQueryV1PackedIgnoredUnlessPackedSource(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=111_222", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.ZoneID != "" || ev.CampaignID != "" {
			t.Fatalf("non-packed source must not unpack ta_sub: %+v", ev)
		}
	}
}

func TestQueryV1QueryCountryDoesNotFillEvent(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&country=DE", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.Country != "" {
			t.Fatalf("query country must not fill events: %+v", ev)
		}
	}
}

func TestQueryV1CFXXNotEventCountry(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	h.SetEdgeToken(testEdgeToken)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil)
	req.Header.Set("CF-IPCountry", "XX")
	req.Header.Set(edgeTokenHeader, testEdgeToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, ev := range q.events() {
		if ev.Country != "" {
			t.Fatalf("XX must not be stored: %+v", ev)
		}
	}
}

func TestQueryV1TrustedEdgeRecordsISOCountryOnEvents(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	h.SetEdgeToken(testEdgeToken)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&country=DE", nil)
	req.Header.Set("CF-IPCountry", "RU")
	req.Header.Set(edgeTokenHeader, testEdgeToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, testEdgeToken) || strings.Contains(loc, edgeTokenHeader) {
		t.Fatalf("edge token leaked to Location: %q", loc)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	for _, ev := range q.events() {
		if ev.Country != "RU" {
			t.Fatalf("trusted CF-IPCountry must record event country: %+v", ev)
		}
		if strings.Contains(ev.Params, testEdgeToken) || ev.CF != "{}" {
			t.Fatalf("token/CF blob leaked: %+v", ev)
		}
	}
	ctx := QueryV1Context("click-query-v1", "network-a", "")
	if ctx.Country != "" {
		t.Fatalf("must not fill Context.Country: %+v", ctx)
	}
}

func TestQueryV1WrongEdgeTokenNoCountry(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	h.SetEdgeToken(testEdgeToken)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil)
	req.Header.Set("CF-IPCountry", "RU")
	req.Header.Set(edgeTokenHeader, "other")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.Country != "" {
			t.Fatalf("spoofed edge token must not record country: %+v", ev)
		}
	}
}

func TestQueryV1UserAssignedAANotEventCountry(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	h.SetEdgeToken(testEdgeToken)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil)
	req.Header.Set("CF-IPCountry", "AA")
	req.Header.Set(edgeTokenHeader, testEdgeToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, ev := range q.events() {
		if ev.Country != "" {
			t.Fatalf("user-assigned AA must not be stored: %+v", ev)
		}
	}
}

func TestQueryV1TrustedEdgeRejectsNonOfficialCF(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	h.SetEdgeToken(testEdgeToken)
	for _, code := range []string{"ZZ", "QZ", "UK", "XK"} {
		q = &recEvents{}
		h.SetEnqueue(q)
		req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a", nil)
		req.Header.Set("CF-IPCountry", code)
		req.Header.Set(edgeTokenHeader, testEdgeToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("%s status=%d", code, rec.Code)
		}
		if loc := rec.Header().Get("Location"); strings.Contains(loc, "country=") {
			t.Fatalf("%s leaked to Location: %q", code, loc)
		}
		if len(q.events()) != 3 {
			t.Fatalf("%s events=%d", code, len(q.events()))
		}
		for _, ev := range q.events() {
			if ev.Country != "" {
				t.Fatalf("%s must not be stored: %+v", code, ev)
			}
		}
	}
}

func TestQueryV1ExplicitMacroBracesAbsentThenPacked(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=exoclick&ta_sub=111_222&zone_id={zone_id}&campaign_id={campaign_id}", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("decoded braces status=%d", rec.Code)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	for _, ev := range q.events() {
		if ev.ZoneID != "111" || ev.CampaignID != "222" || ev.TASub != "111_222" {
			t.Fatalf("decoded braces must not store macros; packed fills: %+v", ev)
		}
		if strings.Contains(ev.ZoneID, "{") || strings.Contains(ev.CampaignID, "{") {
			t.Fatalf("brace leaked: %+v", ev)
		}
	}

	h, q = queryV1Handler(t, queryMatchBundle(t))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=exoclick&ta_sub=111_222&zone_id=%7Bzone_id%7D&campaign_id=%7Bcampaign_id%7D", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("encoded braces status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.ZoneID != "111" || ev.CampaignID != "222" {
			t.Fatalf("encoded braces must reject then packed fill: %+v", ev)
		}
	}

	h, q = queryV1Handler(t, queryMatchBundle(t))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=%7Bkeep%7D&zone_id=%7Bzone_id%7D&campaign_id=1234567", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("opaque sub status=%d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "zone_id=") || strings.Contains(loc, "campaign_id=") {
		t.Fatalf("ids must not be Location keys: %q", loc)
	}
	for _, ev := range q.events() {
		if ev.TASub != "{keep}" {
			t.Fatalf("opaque ta_sub must keep braces: %+v", ev)
		}
		if ev.ZoneID != "" || ev.CampaignID != "1234567" {
			t.Fatalf("independent: zone braces absent, explicit campaign kept: %+v", ev)
		}
	}
}

func TestQueryV1CostCopiedOnAllEventsNotLocationOrContext(t *testing.T) {
	withCost := "/?ta_source=network-a&ta_sub=campaign-42&zone_id=987654&campaign_id=1234567&cost=0.012300"
	withoutCost := "/?ta_source=network-a&ta_sub=campaign-42&zone_id=987654&campaign_id=1234567"

	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, withCost, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("cost") != "" || u.Query().Get("zone_id") != "" || u.Query().Get("campaign_id") != "" {
		t.Fatalf("cost/zone/campaign must not be Location substitutions: %q", loc)
	}
	if !strings.Contains(loc, "campaign=query-source") {
		t.Fatalf("cost must not hide ta_source match: %q", loc)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	names := map[string]bool{}
	for _, ev := range q.events() {
		names[ev.EventName] = true
		if ev.Cost != "0.012300" {
			t.Fatalf("cost not copied: %+v", ev)
		}
		if ev.CampaignID != "1234567" || ev.ZoneID != "987654" || ev.TASub != "campaign-42" {
			t.Fatalf("attribution regression: %+v", ev)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if _, ok := params["cost"]; ok {
			t.Fatalf("cost must not be duplicated into params: %+v", params)
		}
		if params["query_cost_reject"] != "" {
			t.Fatalf("valid cost must not reject: %+v", params)
		}
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"cost":"0.012300"`) {
			t.Fatalf("wire missing exact cost: %s", raw)
		}
	}
	for _, name := range []string{ingest.EventIngress, ingest.EventScenarioAssigned, ingest.EventRedirect} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}

	h2, q2 := queryV1Handler(t, queryMatchBundle(t))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, withoutCost, nil))
	if rec2.Code != http.StatusFound {
		t.Fatalf("without cost status=%d", rec2.Code)
	}
	if rec2.Header().Get("Location") != loc {
		t.Fatalf("cost must not change Location\nwith=%q\nwithout=%q", loc, rec2.Header().Get("Location"))
	}
	if len(q2.events()) != 3 {
		t.Fatalf("without cost events=%d", len(q2.events()))
	}
	for i, ev := range q2.events() {
		if ev.Cost != "" {
			t.Fatalf("absent cost must be empty: %+v", ev)
		}
		if ev.SmartlinkVariantID != q.events()[i].SmartlinkVariantID {
			t.Fatalf("cost must not change Resolve variant: with=%q without=%q", q.events()[i].SmartlinkVariantID, ev.SmartlinkVariantID)
		}
		if ev.CampaignID != "1234567" || ev.ZoneID != "987654" {
			t.Fatalf("ids without cost: %+v", ev)
		}
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"cost":""`) {
			t.Fatalf("empty cost must still serialize: %s", raw)
		}
	}
	ctx := QueryV1Context("click-query-v1", "network-a", "campaign-42")
	if ctx.Country != "" {
		t.Fatalf("Context.Country must stay empty: %+v", ctx)
	}
}

func TestQueryV1CostExactStringsIncludingMacros(t *testing.T) {
	values := []string{"0.00002", "0.012300", "0", "abc", "{actual_cost}", "[BID_PRICE]", "[CPV_PRICE]"}
	for _, want := range values {
		h, q := queryV1Handler(t, queryMatchBundle(t))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost="+url.QueryEscape(want), nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("%q status=%d", want, rec.Code)
		}
		if loc := rec.Header().Get("Location"); strings.Contains(loc, "cost=") {
			t.Fatalf("%q leaked to Location: %q", want, loc)
		}
		if len(q.events()) != 3 {
			t.Fatalf("%q events=%d", want, len(q.events()))
		}
		for _, ev := range q.events() {
			if ev.Cost != want {
				t.Fatalf("want %q got %+v", want, ev)
			}
		}
	}
}

func TestQueryV1CostEmptyQueryIsEmptyEventField(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost=", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	for _, ev := range q.events() {
		if ev.Cost != "" {
			t.Fatalf("empty query cost must store empty: %+v", ev)
		}
	}
}

func TestQueryV1CostRejectStill302(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))

	badUTF := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "ta_source=network-a&cost=" + url.QueryEscape(string([]byte{0xff}))
	h.ServeHTTP(badUTF, req)
	if badUTF.Code != http.StatusFound || !strings.Contains(badUTF.Header().Get("Location"), "campaign=query-source") {
		t.Fatalf("invalid cost must still 302: %d %q", badUTF.Code, badUTF.Header().Get("Location"))
	}
	if loc := badUTF.Header().Get("Location"); strings.Contains(loc, "cost=") {
		t.Fatalf("rejected cost leaked to Location: %q", loc)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	for _, ev := range q.events() {
		if ev.Cost != "" {
			t.Fatalf("rejected cost must be empty: %+v", ev)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["query_cost_reject"] != rejectInvalidUTF8 {
			t.Fatalf("reject reason %+v", params)
		}
		if strings.Contains(ev.Params, "\xff") || strings.Contains(string([]byte{0xff}), params["query_cost_reject"]) {
			t.Fatalf("rejected value leaked: %s", ev.Params)
		}
	}

	h, q = queryV1Handler(t, queryMatchBundle(t))
	ctrl := httptest.NewRecorder()
	h.ServeHTTP(ctrl, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost=bad%0Avalue", nil))
	if ctrl.Code != http.StatusFound {
		t.Fatalf("control status=%d", ctrl.Code)
	}
	for _, ev := range q.events() {
		if ev.Cost != "" {
			t.Fatalf("control cost must be empty: %+v", ev)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["query_cost_reject"] != rejectControlChars {
			t.Fatalf("control reject %+v", params)
		}
	}

	h, q = queryV1Handler(t, queryMatchBundle(t))
	longVal := httptest.NewRecorder()
	runes := strings.Repeat("x", maxQueryValueRunes+1)
	h.ServeHTTP(longVal, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost="+runes, nil))
	if longVal.Code != http.StatusFound {
		t.Fatalf("oversize value status=%d", longVal.Code)
	}
	for _, ev := range q.events() {
		if ev.Cost != "" {
			t.Fatalf("oversize cost must be empty: %+v", ev)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["query_cost_reject"] != rejectTooLong {
			t.Fatalf("oversize reject %+v", params)
		}
	}
}

func TestQueryV1CostDuplicateIs400(t *testing.T) {
	h, q := queryV1Handler(t, queryMatchBundle(t))
	dup := httptest.NewRecorder()
	h.ServeHTTP(dup, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost=1&cost=2", nil))
	if dup.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status=%d", dup.Code)
	}
	if dup.Header().Get("Location") != "" {
		t.Fatal("400 must not redirect")
	}
	if len(q.events()) != 0 {
		t.Fatalf("duplicate must not enqueue, have %d", len(q.events()))
	}

	h, q = queryV1Handler(t, queryMatchBundle(t))
	enc := httptest.NewRecorder()
	h.ServeHTTP(enc, httptest.NewRequest(http.MethodGet, "/?cost=1&c%6fst=2", nil))
	if enc.Code != http.StatusBadRequest {
		t.Fatalf("encoded duplicate status=%d", enc.Code)
	}
	if len(q.events()) != 0 {
		t.Fatal("encoded duplicate must not enqueue")
	}
}

func TestQueryV1QueueFullStill302WithCost(t *testing.T) {
	c, err := NewContract(queryMatchBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetContextMode(ContextModeQueryV1)
	h.mint = func() (string, error) { return "click-full-cost", nil }
	h.StoreContract(c)
	h.SetEnqueue(&dropQ{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&cost=0.0123", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Location") == "" {
		t.Fatal("full queue must still redirect")
	}
}
