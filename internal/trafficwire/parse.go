package trafficwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

func parseStrict(raw []byte, dest any) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("artifact is not valid UTF-8")
	}
	if err := rejectDuplicateFields(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing JSON after first value")
	}
	return nil
}

func rejectDuplicateFields(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSON(dec); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON after first value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := kt.(string)
			if !ok {
				return fmt.Errorf("expected object key")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("expected closing }")
		}
		return nil
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("expected closing ]")
		}
		return nil
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
}

func ParseRelease(raw []byte) (*Release, error) {
	var v Release
	if err := parseStrict(raw, &v); err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	return &v, nil
}

func ParseProgram(raw []byte) (*Program, error) {
	var v Program
	if err := parseStrict(raw, &v); err != nil {
		return nil, fmt.Errorf("program: %w", err)
	}
	return &v, nil
}

func ParseWeights(raw []byte) (*Weights, error) {
	var v Weights
	if err := parseStrict(raw, &v); err != nil {
		return nil, fmt.Errorf("weights: %w", err)
	}
	return &v, nil
}

func ParseRedirect(handler string, schemaVersion int, raw json.RawMessage) (RedirectTarget, error) {
	switch handler {
	case HandlerRedirectTarget:
		if schemaVersion != 1 {
			return RedirectTarget{}, fmt.Errorf("redirect-target-v1 requires schemaVersion 1, got %d", schemaVersion)
		}
		var p RedirectTargetV1
		if err := parseStrict(raw, &p); err != nil {
			return RedirectTarget{}, err
		}
		if p.TargetID == "" {
			return RedirectTarget{}, fmt.Errorf("empty targetId")
		}
		return RedirectTarget{Handler: handler, Schema: 1, Enabled: p.Enabled, TargetID: p.TargetID}, nil
	case HandlerRedirectTargetV2:
		if schemaVersion != 2 {
			return RedirectTarget{}, fmt.Errorf("redirect-target-v2 requires schemaVersion 2, got %d", schemaVersion)
		}
		var p RedirectTargetV2
		if err := parseStrict(raw, &p); err != nil {
			return RedirectTarget{}, err
		}
		if p.TargetID == "" {
			return RedirectTarget{}, fmt.Errorf("empty targetId")
		}
		return RedirectTarget{Handler: handler, Schema: 2, Enabled: p.Enabled, TargetID: p.TargetID, URL: p.URL}, nil
	default:
		return RedirectTarget{}, fmt.Errorf("unsupported redirect handler %q", handler)
	}
}
