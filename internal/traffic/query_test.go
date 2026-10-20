package traffic

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseAllowlistedPresent(t *testing.T) {
	q, err := ParseAllowlistedQuery("ta_source=network-a&ta_sub=campaign-42")
	if err != nil {
		t.Fatal(err)
	}
	if q.TASource != "network-a" || q.TASub != "campaign-42" {
		t.Fatalf("%+v", q)
	}
}

func TestParseAllowlistedAbsentAndEmpty(t *testing.T) {
	q, err := ParseAllowlistedQuery("")
	if err != nil {
		t.Fatal(err)
	}
	if q.TASource != "" || q.TASub != "" {
		t.Fatalf("empty query: %+v", q)
	}
	q, err = ParseAllowlistedQuery("ta_source=&ta_sub=")
	if err != nil {
		t.Fatal(err)
	}
	src, srcR := NormalizeQueryValue(q.TASource)
	sub, subR := NormalizeQueryValue(q.TASub)
	if src != "" || sub != "" || srcR != "" || subR != "" {
		t.Fatalf("empty values must be absent src=%q sub=%q r=%q/%q", src, sub, srcR, subR)
	}
}

func TestParseAllowlistedIgnoresUnknownAndSpoofed(t *testing.T) {
	q, err := ParseAllowlistedQuery("ta_source=network-a&m_campaign=x&url=https://evil.example/&redirect_to=https://evil.example/&click_id=attacker&country=RU&isBot=true&ta_sub=campaign-42")
	if err != nil {
		t.Fatal(err)
	}
	if q.TASource != "network-a" || q.TASub != "campaign-42" {
		t.Fatalf("%+v", q)
	}
}

func TestParseAllowlistedOneDecode(t *testing.T) {
	q, err := ParseAllowlistedQuery("ta_source=%2561")
	if err != nil {
		t.Fatal(err)
	}
	if q.TASource != "%61" {
		t.Fatalf("second decode leaked: %q", q.TASource)
	}
}

func TestParseAllowlistedPercentEncodedKeyDuplicate(t *testing.T) {
	_, err := ParseAllowlistedQuery("ta_source=a&ta%5Fsource=b")
	if err == nil {
		t.Fatal("encoded duplicate key must be 400")
	}
	qe := err.(*QueryError)
	if qe.Status != 400 || qe.Reason != queryErrDuplicate {
		t.Fatalf("%+v", qe)
	}
}

func TestParseAllowlistedDuplicateSameValue(t *testing.T) {
	_, err := ParseAllowlistedQuery("ta_sub=x&ta_sub=x")
	if err == nil {
		t.Fatal("duplicate ta_sub must be 400")
	}
}

func TestParseAllowlistedMalformed(t *testing.T) {
	_, err := ParseAllowlistedQuery("ta_source=%zz")
	if err == nil {
		t.Fatal("malformed percent encoding must be 400")
	}
	if queryErrorStatus(err) != 400 {
		t.Fatalf("status %d", queryErrorStatus(err))
	}
}

func TestParseAllowlistedOversizeRaw(t *testing.T) {
	raw := "ta_source=" + strings.Repeat("a", maxRawQueryBytes)
	if len(raw) <= maxRawQueryBytes {
		t.Fatalf("len=%d", len(raw))
	}
	_, err := ParseAllowlistedQuery(raw)
	if err == nil {
		t.Fatal("oversize raw query must be 414")
	}
	if queryErrorStatus(err) != 414 {
		t.Fatalf("status %d", queryErrorStatus(err))
	}
}

func TestParseAllowlistedExactMaxRawOK(t *testing.T) {
	// "x=" + 4094 a = 4096
	raw := "x=" + strings.Repeat("a", maxRawQueryBytes-2)
	if len(raw) != maxRawQueryBytes {
		t.Fatalf("len=%d", len(raw))
	}
	if _, err := ParseAllowlistedQuery(raw); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeInvalidUTF8AndControlAndOversize(t *testing.T) {
	if v, r := NormalizeQueryValue(string([]byte{0xff, 0xfe})); v != "" || r != rejectInvalidUTF8 {
		t.Fatalf("utf8 %q %q", v, r)
	}
	if v, r := NormalizeQueryValue("bad\nvalue"); v != "" || r != rejectControlChars {
		t.Fatalf("control %q %q", v, r)
	}
	if v, r := NormalizeQueryValue("ok\t"); v != "" || r != rejectControlChars {
		t.Fatalf("tab %q %q", v, r)
	}
	ok := strings.Repeat("é", maxQueryValueRunes)
	if utf8Count := len([]rune(ok)); utf8Count != maxQueryValueRunes {
		t.Fatal(utf8Count)
	}
	if v, r := NormalizeQueryValue(ok); v != ok || r != "" {
		t.Fatalf("128 runes must pass")
	}
	if v, r := NormalizeQueryValue(ok + "x"); v != "" || r != rejectTooLong {
		t.Fatalf("129 runes %q %q", v, r)
	}
}

func TestNormalizeDoesNotCaseFoldOrTrim(t *testing.T) {
	v, r := NormalizeQueryValue(" Network-A ")
	if r != "" || v != " Network-A " {
		t.Fatalf("got %q %q", v, r)
	}
}

func TestParseQueryUsesURLParseQuery(t *testing.T) {
	// Guard: RawQuery decoding matches net/url once.
	raw := "ta_source=" + url.QueryEscape("a+b")
	q, err := ParseAllowlistedQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	if q.TASource != "a+b" {
		t.Fatalf("got %q", q.TASource)
	}
}

func TestParseAllowlistedCampaignZoneAndDuplicates(t *testing.T) {
	q, err := ParseAllowlistedQuery("ta_source=exoclick&zone_id=1&campaign_id=2")
	if err != nil {
		t.Fatal(err)
	}
	if q.ZoneID != "1" || q.CampaignID != "2" {
		t.Fatalf("%+v", q)
	}
	if q.Cost != "" {
		t.Fatalf("absent cost must stay empty: %+v", q)
	}
	if _, err := ParseAllowlistedQuery("zone_id=1&zone_id=2"); err == nil {
		t.Fatal("duplicate zone_id")
	}
}

func TestParseAllowlistedCostRawStringAndDuplicates(t *testing.T) {
	q, err := ParseAllowlistedQuery("ta_source=exoclick&campaign_id=42&zone_id=7&cost=0.012300")
	if err != nil {
		t.Fatal(err)
	}
	if q.Cost != "0.012300" || q.CampaignID != "42" || q.ZoneID != "7" {
		t.Fatalf("%+v", q)
	}
	q, err = ParseAllowlistedQuery("cost=0")
	if err != nil {
		t.Fatal(err)
	}
	if q.Cost != "0" {
		t.Fatalf("zero must stay string 0: %+v", q)
	}
	q, err = ParseAllowlistedQuery("cost=")
	if err != nil {
		t.Fatal(err)
	}
	got, reason := NormalizeQueryValue(q.Cost)
	if got != "" || reason != "" {
		t.Fatalf("empty cost must be absent got=%q reason=%q", got, reason)
	}
	q, err = ParseAllowlistedQuery("cost={actual_cost}")
	if err != nil {
		t.Fatal(err)
	}
	got, reason = NormalizeQueryValue(q.Cost)
	if got != "{actual_cost}" || reason != "" {
		t.Fatalf("unexpanded cost macro must use NormalizeQueryValue: %q %q", got, reason)
	}
	if _, err := ParseAllowlistedQuery("cost=1&cost=2"); err == nil {
		t.Fatal("duplicate cost")
	}
	if _, err := ParseAllowlistedQuery("cost=1&c%6fst=2"); err == nil {
		t.Fatal("percent-encoded duplicate cost")
	}
}

func TestFillPackedIndependentExplicit(t *testing.T) {
	z, c := FillPackedIDs("exoclick", "111_222", "999", "")
	if z != "999" || c != "222" {
		t.Fatalf("zone=%q campaign=%q", z, c)
	}
	z, c = FillPackedIDs("network-a", "111_222", "", "")
	if z != "" || c != "" {
		t.Fatalf("other source must not unpack ta_sub: %q %q", z, c)
	}
	z, c = FillPackedIDs("exoclick", "{zone_id}_{campaign_id}", "", "")
	if z != "" || c != "" {
		t.Fatalf("unexpanded %q %q", z, c)
	}
}

func TestNormalizeIDValueRejectsBracesThenPackedCanFill(t *testing.T) {
	got, reason := NormalizeIDValue("{campaign_id}")
	if got != "" || reason != rejectMacroBraces {
		t.Fatalf("decoded braces: %q %q", got, reason)
	}
	got, reason = NormalizeIDValue("1234567")
	if got != "1234567" || reason != "" {
		t.Fatalf("numeric: %q %q", got, reason)
	}
	src, _ := NormalizeQueryValue("{keep}")
	if src != "{keep}" {
		t.Fatalf("opaque ta_source/ta_sub must keep braces: %q", src)
	}
	z, c := FillPackedIDs("exoclick", "111_222", "", "")
	id, _ := NormalizeIDValue("{campaign_id}")
	if id != "" {
		t.Fatal("braces must be absent before packed")
	}
	z, c = FillPackedIDs("exoclick", "111_222", z, id)
	if z != "111" || c != "222" {
		t.Fatalf("packed after brace reject zone=%q campaign=%q", z, c)
	}
}

func TestEventCountryFromCF(t *testing.T) {
	if EventCountryFromCF("RU") != "RU" || EventCountryFromCF("GB") != "GB" {
		t.Fatal("official")
	}
	if EventCountryFromCF("de") != "" || EventCountryFromCF("XX") != "" || EventCountryFromCF("T1") != "" {
		t.Fatal("rejected")
	}
	for _, code := range []string{"", "RUS", "AA", "ZZ", "QZ", "UK", "XK"} {
		if EventCountryFromCF(code) != "" {
			t.Fatalf("%q must be empty", code)
		}
	}
}
