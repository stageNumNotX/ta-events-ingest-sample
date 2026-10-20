// Package redirect собирает HTTP Location для сценария redirect.
// Это чистая функция: без cloud-клиентов, БД, HTTP и таймеров.
package redirect

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	// MaxSubstitutionLen ограничивает ta_source/ta_sub, которые можно
	// перенести в Location. click_id задаёт runtime и здесь не режется.
	MaxSubstitutionLen = 128
)

// Substitutions — единственные query-подстановки, которые executor
// имеет право добавить или заменить в цели из release.
type Substitutions struct {
	ClickID  string
	TASource string
	TASub    string
}

// ValidateTarget проверяет цель редиректа на load path: HTTPS, без userinfo,
// без CR/LF, с хостом. Не принимает произвольный ввод запроса.
func ValidateTarget(raw string) error {
	if raw == "" {
		return fmt.Errorf("empty redirect target")
	}
	if strings.ContainsAny(raw, "\r\n") {
		return fmt.Errorf("CR/LF not allowed in redirect target")
	}
	if !utf8.ValidString(raw) {
		return fmt.Errorf("redirect target is not valid UTF-8")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse redirect target: %w", err)
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed in redirect target")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("unsupported redirect scheme %q (https required)", u.Scheme)
	}
	if u.Host == "" || strings.Contains(u.Host, "@") {
		return fmt.Errorf("redirect target host is required")
	}
	if u.Fragment != "" {
		return fmt.Errorf("fragment is not allowed in redirect target")
	}
	return nil
}

// Build собирает Location из уже проверенной цели release и объявленных
// подстановок. Сгенерированный click_id заменяет одноимённый query-параметр
// цели. Входные url/redirect_to/прочие параметры запроса сюда не передаются.
func Build(target string, sub Substitutions) (string, error) {
	if err := ValidateTarget(target); err != nil {
		return "", err
	}
	if sub.ClickID == "" {
		return "", fmt.Errorf("click_id is required")
	}
	if strings.ContainsAny(sub.ClickID, "\r\n") || !utf8.ValidString(sub.ClickID) {
		return "", fmt.Errorf("invalid click_id")
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse redirect target: %w", err)
	}
	q := u.Query()
	q.Set("click_id", sub.ClickID)
	if v := sanitizeSubstitution(sub.TASource); v != "" {
		q.Set("ta_source", v)
	}
	if v := sanitizeSubstitution(sub.TASub); v != "" {
		q.Set("ta_sub", v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func sanitizeSubstitution(v string) string {
	if v == "" {
		return ""
	}
	if strings.ContainsAny(v, "\r\n") || !utf8.ValidString(v) {
		return ""
	}
	if utf8.RuneCountInString(v) > MaxSubstitutionLen {
		return ""
	}
	return v
}
