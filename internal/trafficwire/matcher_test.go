package trafficwire_test

import (
	"strings"
	"testing"

	"ta-events-ingest/internal/trafficwire"
)

func TestValidateMatcherShapeINAndEXISTS(t *testing.T) {
	if err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpCondition, Field: "ta_source", Operator: trafficwire.OperatorEXISTS,
	}); err != nil {
		t.Fatal(err)
	}
	if err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpCondition, Field: "ta_sub", Operator: trafficwire.OperatorIN,
		Values: []byte(`["a","b"]`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpCondition, Field: "ta_sub", Operator: trafficwire.OperatorIN,
		Values: []byte(`["a","a"]`),
	}); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("dup IN: %v", err)
	}
	if err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpCondition, Field: "ta_source", Operator: "GT", Value: []byte(`"x"`),
	}); err == nil {
		t.Fatal("GT")
	}
	if err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpCondition, Field: "ta_source", Operator: trafficwire.OperatorEXISTS,
		Value: []byte(`true`),
	}); err == nil {
		t.Fatal("EXISTS with value")
	}
}

func TestValidateMatcherShapeALL(t *testing.T) {
	err := trafficwire.ValidateMatcherShape(trafficwire.Matcher{
		Op: trafficwire.OpALL,
		Rules: []trafficwire.Matcher{
			{Op: trafficwire.OpCondition, Field: "ta_source", Operator: trafficwire.OperatorEQ, Value: []byte(`"a"`)},
			{Op: trafficwire.OpCondition, Field: "ta_sub", Operator: trafficwire.OperatorEQ, Value: []byte(`"b"`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}
