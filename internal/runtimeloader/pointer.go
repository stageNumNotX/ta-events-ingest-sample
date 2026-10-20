package runtimeloader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultPointerPollInterval = 5 * time.Second

// Default pointer object. Runtime only reads it.
// Generation/size/sha256 are the trust anchor — do not rewrite them from a
// download (no self-oracle).
const (
	PointerObject = "traffic-runtime/v1/targets/prototype.json"
	PointerGen    = 1789108332117984
	PointerSize   = 1362
	PointerSHA256 = "2a7d46ddd9a76b07794d095d7b70b112821ab15f65d051aecfff709a19be462f"
	PointerTarget = "prototype"
)

// Pointer is the parsed producer traffic-pointer. Pins are the three
// generation-pinned artifacts; no fourth registry field.
type Pointer struct {
	TargetID      string
	Version       int64
	ArtifactSetID string
	JobID         string
	Pins          GCSPins
}

func DefaultPointerPin() ObjectPin {
	return ObjectPin{
		Bucket:     PinBucket,
		Object:     PointerObject,
		Generation: PointerGen,
		Size:       PointerSize,
		SHA256:     PointerSHA256,
	}
}

// PointerRequested is true when serving (or a test) asked for the producer
// pointer overlay. T3/T5 probe revisions leave this unset and keep C1 pins.
func PointerRequested() bool {
	if os.Getenv("TRAFFIC_USE_POINTER") == "1" {
		return true
	}
	return os.Getenv("TRAFFIC_POINTER_GENERATION") != "" ||
		os.Getenv("TRAFFIC_POINTER_OBJECT") != "" ||
		os.Getenv("TRAFFIC_POINTER_SHA256") != ""
}

// PointerPollRequested включает out-of-request poll latest pointer.
// Пустой TRAFFIC_POINTER_POLL следует за PointerRequested. Явные
// 0/false/off выключают poll (T3/T5 probe). Неизвестный extra — fail-closed off.
func PointerPollRequested() bool {
	raw := strings.TrimSpace(os.Getenv("TRAFFIC_POINTER_POLL"))
	if raw == "" {
		return PointerRequested()
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// PointerPollInterval читает TRAFFIC_POINTER_POLL_INTERVAL. Пустое,
// битое или <1s значение — default 5s (не молотить GCS).
func PointerPollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("TRAFFIC_POINTER_POLL_INTERVAL"))
	if raw == "" {
		return DefaultPointerPollInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second {
		return DefaultPointerPollInterval
	}
	return d
}

// PointerFromEnv starts from DefaultPointerPin and applies TRAFFIC_POINTER_*
// overrides. A set but malformed/zero GENERATION or SIZE is an error: do not
// fall back to the compiled pin (fail-closed exact generation pinning).
func PointerFromEnv() (ObjectPin, error) {
	p := DefaultPointerPin()
	if v := os.Getenv("TRAFFIC_POINTER_BUCKET"); v != "" {
		p.Bucket = v
	}
	if v := os.Getenv("TRAFFIC_POINTER_OBJECT"); v != "" {
		p.Object = v
	}
	if v := os.Getenv("TRAFFIC_POINTER_SHA256"); v != "" {
		p.SHA256 = strings.TrimPrefix(v, "sha256:")
	}
	if v := os.Getenv("TRAFFIC_POINTER_GENERATION"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return ObjectPin{}, fmt.Errorf("TRAFFIC_POINTER_GENERATION %q: %w", v, err)
		}
		if n <= 0 {
			return ObjectPin{}, fmt.Errorf("TRAFFIC_POINTER_GENERATION must be > 0, got %d", n)
		}
		p.Generation = n
	}
	if v := os.Getenv("TRAFFIC_POINTER_SIZE"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return ObjectPin{}, fmt.Errorf("TRAFFIC_POINTER_SIZE %q: %w", v, err)
		}
		if n <= 0 || n > int64(maxArtifactBytes) {
			return ObjectPin{}, fmt.Errorf("TRAFFIC_POINTER_SIZE must be in 1..%d, got %d", maxArtifactBytes, n)
		}
		p.Size = int(n)
	}
	return p, nil
}

func LoadPointerPins(ctx context.Context, r ObjectReader, pin ObjectPin) (*Pointer, error) {
	if r == nil {
		return nil, fmt.Errorf("nil object reader")
	}
	body, err := r.ReadObject(ctx, pin)
	if err != nil {
		return nil, err
	}
	return ParsePointer(body)
}

// PointerJSONKeys returns top-level JSON object keys. Used in evidence and
// live-test errors — never dump the pointer body (may contain extras).
func PointerJSONKeys(raw []byte) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParsePointer is fail-closed on structure: version>0, artifactSetId, and
// three artifact pins. Unknown fields are ignored. The live 1362 B layout
// is not a closed schema in this repo (compiler-service is unread).
func ParsePointer(raw []byte) (*Pointer, error) {
	if len(raw) == 0 || len(raw) > maxArtifactBytes {
		return nil, fmt.Errorf("pointer bytes out of bounds: %d", len(raw))
	}
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("pointer json: %w", err)
	}
	obj, ok := top.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pointer json must be an object (keys=%v)", PointerJSONKeys(raw))
	}
	p := &Pointer{}
	collectPointerMeta(obj, p)
	for _, nest := range []string{"live", "pointer", "current", "target", "status", "data", "spec"} {
		if nested, ok := asObject(mapVal(obj, nest)); ok {
			collectPointerMeta(nested, p)
		}
	}
	pins, err := extractPins(obj)
	if err != nil {
		return nil, fmt.Errorf("pointer pins (keys=%v): %w", PointerJSONKeys(raw), err)
	}
	p.Pins = pins
	if p.Version <= 0 {
		return nil, fmt.Errorf("pointer version must be > 0 (keys=%v)", PointerJSONKeys(raw))
	}
	if strings.TrimSpace(p.ArtifactSetID) == "" {
		return nil, fmt.Errorf("pointer artifactSetId is required (keys=%v)", PointerJSONKeys(raw))
	}
	if err := validatePins(p.Pins); err != nil {
		return nil, fmt.Errorf("pointer pins (keys=%v): %w", PointerJSONKeys(raw), err)
	}
	return p, nil
}

func PinsFromPointer(p *Pointer) (GCSPins, error) {
	if p == nil {
		return GCSPins{}, fmt.Errorf("nil pointer")
	}
	if err := validatePins(p.Pins); err != nil {
		return GCSPins{}, err
	}
	return p.Pins, nil
}

func collectPointerMeta(m map[string]any, p *Pointer) {
	if p.Version <= 0 {
		if v := asInt64(mapVal(m, "version", "activationVersion", "targetVersion")); v > 0 {
			p.Version = v
		}
	}
	if p.ArtifactSetID == "" {
		p.ArtifactSetID = asString(mapVal(m, "artifactSetId", "artifact_set_id"))
	}
	if p.TargetID == "" {
		p.TargetID = asString(mapVal(m, "targetId", "target_id"))
	}
	if p.JobID == "" {
		p.JobID = asString(mapVal(m, "jobId", "job_id"))
	}
}

func extractPins(root map[string]any) (GCSPins, error) {
	var out GCSPins
	if err := walkPins(root, &out, 0); err != nil {
		return GCSPins{}, err
	}
	return out, nil
}

func walkPins(v any, out *GCSPins, depth int) error {
	if v == nil || depth > 6 {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		if err := tryAssignArtifact(t, out); err != nil {
			return err
		}
		for _, key := range []string{"artifacts", "pins", "objects", "live", "pointer", "current", "target", "artifactSet", "artifact_set"} {
			if child := mapVal(t, key); child != nil {
				if err := walkPins(child, out, depth+1); err != nil {
					return err
				}
			}
		}
		for _, kind := range []string{"program", "weights", "release"} {
			if child := mapVal(t, kind); child != nil {
				if m, ok := asObject(child); ok {
					if err := assignNamedPin(out, kind, m); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		for _, el := range t {
			if err := walkPins(el, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func tryAssignArtifact(m map[string]any, out *GCSPins) error {
	kind := pinKind(asString(mapVal(m, "kind", "artifactType", "type", "name")))
	if kind == "" {
		return nil
	}
	pin, ok := pinFromMap(m, kind)
	if !ok {
		return nil
	}
	return assignPin(out, kind, pin)
}

func assignNamedPin(out *GCSPins, kind string, m map[string]any) error {
	pin, ok := pinFromMap(m, kind)
	if !ok {
		return nil
	}
	return assignPin(out, kind, pin)
}

func pinFromMap(m map[string]any, kind string) (ObjectPin, bool) {
	sha := strings.TrimPrefix(asString(mapVal(m, "sha256", "digest", "hash")), "sha256:")
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !validSHA256(sha) {
		return ObjectPin{}, false
	}
	gen := asInt64(mapVal(m, "generation", "objectGeneration", "gcsGeneration"))
	if gen <= 0 {
		return ObjectPin{}, false
	}
	size := int(asInt64(mapVal(m, "sizeBytes", "size", "bytes")))
	if size <= 0 || size > maxArtifactBytes {
		return ObjectPin{}, false
	}
	uri := asString(mapVal(m, "uri", "gcsUri", "object", "path"))
	bucket, object := parseGCSURI(uri)
	if object == "" {
		object = ArtifactObject(kind, sha)
	}
	if bucket == "" {
		bucket = PinBucket
	}
	return ObjectPin{
		Bucket:     bucket,
		Object:     object,
		Generation: gen,
		Size:       size,
		SHA256:     sha,
	}, true
}

func assignPin(out *GCSPins, kind string, pin ObjectPin) error {
	cur := pinOf(out, kind)
	if cur.SHA256 != "" {
		if cur.SHA256 != pin.SHA256 || cur.Generation != pin.Generation || cur.Size != pin.Size {
			return fmt.Errorf("conflicting %s pins", kind)
		}
		return nil
	}
	setPin(out, kind, pin)
	return nil
}

func pinOf(p *GCSPins, kind string) ObjectPin {
	switch kind {
	case "program":
		return p.Program
	case "weights":
		return p.Weights
	case "release":
		return p.Release
	default:
		return ObjectPin{}
	}
}

func setPin(p *GCSPins, kind string, pin ObjectPin) {
	switch kind {
	case "program":
		p.Program = pin
	case "weights":
		p.Weights = pin
	case "release":
		p.Release = pin
	}
}

func validatePins(p GCSPins) error {
	for _, item := range []struct {
		kind string
		pin  ObjectPin
	}{
		{"program", p.Program},
		{"weights", p.Weights},
		{"release", p.Release},
	} {
		if item.pin.Generation <= 0 || item.pin.Size <= 0 || item.pin.Size > maxArtifactBytes {
			return fmt.Errorf("%s pin missing generation/size", item.kind)
		}
		if !validSHA256(item.pin.SHA256) {
			return fmt.Errorf("%s pin sha256 is required", item.kind)
		}
		if item.pin.Bucket == "" || item.pin.Object == "" {
			return fmt.Errorf("%s pin object path is required", item.kind)
		}
	}
	return nil
}

func pinKind(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "program"):
		return "program"
	case strings.Contains(s, "weight"):
		return "weights"
	case strings.Contains(s, "release"):
		return "release"
	default:
		return ""
	}
}

func parseGCSURI(uri string) (bucket, object string) {
	uri = strings.TrimSpace(uri)
	if i := strings.Index(uri, "#"); i >= 0 {
		uri = uri[:i]
	}
	const prefix = "gs://"
	if strings.HasPrefix(uri, prefix) {
		rest := strings.TrimPrefix(uri, prefix)
		b, o, ok := strings.Cut(rest, "/")
		if !ok || b == "" || o == "" {
			return "", ""
		}
		return b, o
	}
	return "", strings.TrimPrefix(uri, "/")
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mapVal(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	lower := make(map[string]any, len(m))
	for k, v := range m {
		lower[strings.ToLower(k)] = v
	}
	for _, k := range keys {
		if v, ok := lower[strings.ToLower(k)]; ok {
			return v
		}
	}
	return nil
}

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return strings.TrimSpace(t.String())
	default:
		return ""
	}
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		if t <= 0 {
			return 0
		}
		n := int64(t)
		if float64(n) != t {
			return 0
		}
		return n
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0
		}
		return n
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}
