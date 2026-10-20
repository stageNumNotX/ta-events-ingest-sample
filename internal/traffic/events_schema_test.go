package traffic

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ta-events-ingest/internal/ingest"
)

func TestSerializedRuntimeEventMatchesAvroFields(t *testing.T) {
	c, err := NewContract(twoExecutableBundle(t), ActivationRevisionPinned)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.mint = func() (string, error) { return "click-schema", nil }
	h.StoreContract(c)
	h.SetContextMode(ContextModeQueryV1)
	q := &recEvents{}
	h.SetEnqueue(q)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=campaign-42", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if len(q.events()) != 3 {
		t.Fatalf("events=%d", len(q.events()))
	}
	avro := readAvroFieldNames(t)
	tags := canonicalJSONTags(t)
	if !reflect.DeepEqual(avro, tags) {
		t.Fatalf("avsc fields %v != CanonicalEvent tags %v", avro, tags)
	}
	bq := readT11ColumnNames(t)
	for _, name := range avro {
		if !bq[name] {
			t.Fatalf("T11 table missing avro field %s", name)
		}
	}
	for _, ev := range q.events() {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		for _, name := range avro {
			if _, ok := obj[name]; !ok {
				t.Fatalf("serialized event missing %s: %s", name, raw)
			}
		}
		if ev.TASource != "network-a" || ev.TASub != "campaign-42" {
			t.Fatalf("canonical source/sub %+v", ev)
		}
		if !json.Valid([]byte(ev.Params)) {
			t.Fatalf("params must be JSON text: %q", ev.Params)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["ta_source"] != "network-a" || params["ta_sub"] != "campaign-42" {
			t.Fatalf("params %+v", params)
		}
		if params["context_source"] != QueryV1ContextSource {
			t.Fatalf("context_source %q", params["context_source"])
		}
	}
}

func readAvroFieldNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "event.avsc"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		out = append(out, f.Name)
	}
	return out
}

func canonicalJSONTags(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(ingest.CanonicalEvent{})
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s missing json tag", rt.Field(i).Name)
		}
		out = append(out, name)
	}
	return out
}

func readT11ColumnNames(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "traffic-runtime-t11.bigquery.sql"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "CREATE") || strings.HasPrefix(line, "PARTITION") || strings.HasPrefix(line, "CLUSTER") || strings.HasPrefix(line, "OPTIONS") || strings.HasPrefix(line, ")") {
			continue
		}
		name := strings.Fields(line)[0]
		name = strings.TrimSuffix(name, ",")
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out
}
