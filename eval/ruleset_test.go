package eval_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
)

func TestDecodeRefusesWhatItCannotRead(t *testing.T) {
	for name, doc := range map[string]string{
		"malformed JSON":               `{"schema_version":1,`,
		"missing schema_version":       `{"version":1,"scope":"server"}`,
		"future schema_version":        `{"schema_version":2,"version":1,"scope":"server"}`,
		"unknown scope":                `{"schema_version":1,"version":1,"scope":"edge"}`,
		"flags of the wrong JSON type": `{"schema_version":1,"version":1,"scope":"server","flags":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eval.Decode([]byte(doc)); err == nil {
				t.Fatal("decoded")
			}
		})
	}
}

func TestProtectedEnvironment(t *testing.T) {
	for doc, want := range map[string]bool{
		`{"schema_version":1,"version":1,"scope":"server","environment":{"key":"production","protected":true}}`: true,
		`{"schema_version":1,"version":1,"scope":"server","environment":{"key":"staging","protected":false}}`:   false,
		`{"schema_version":1,"version":1,"scope":"server"}`:                                                     false,
	} {
		if got := decode(t, []byte(doc)).Protected(); got != want {
			t.Errorf("%s: Protected() = %v, want %v", doc, got, want)
		}
	}
}

func TestFlagLooksAFlagUpByKey(t *testing.T) {
	rs := decode(t, []byte(`{"schema_version":1,"version":1,"scope":"server","flags":[
		{"key":"copy","type":"string","automatic_exposure":true,"on":false,"variations":[{"key":"a","value":"A"}],"off_variation":"a"}]}`))
	f, ok := rs.Flag("copy")
	if !ok || f.Key != "copy" || f.Type != "string" || !f.AutomaticExposure {
		t.Errorf("Flag(copy) = %+v, %v", f, ok)
	}
	if f, ok := rs.Flag("nope"); ok || f != nil {
		t.Errorf("Flag(nope) = %+v, %v", f, ok)
	}
}

// TestContextAcceptsGoValues: a Go caller passes native values, not only
// what encoding/json produces.
func TestContextAcceptsGoValues(t *testing.T) {
	rs := decode(t, []byte(`{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],
		"attributes":[{"key":"n","type":"number"},{"key":"l","type":"list"},{"key":"d","type":"date"}],
		"flags":[{"key":"f","type":"string","on":true,"off_variation":"off","fallthrough":"off",
		  "variations":[{"key":"off","value":"off"},{"key":"on","value":{"copy":"Try free"}}],
		  "rules":[{"id":"r1","conditions":[
		    {"kind":"attribute","attribute":"n","op":"gte","value":10},
		    {"kind":"attribute","attribute":"l","op":"contains","value":"beta"},
		    {"kind":"attribute","attribute":"d","op":"before","value":"2026-09-19T12:00:00Z"}],
		   "serve":{"kind":"variation","variation":"on"}}]}]}`))
	noon := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for name, attrs := range map[string]map[string]any{
		"int, []string, time.Time":         {"n": 10, "l": []string{"beta"}, "d": noon.Add(-time.Second)},
		"int64, []any, epoch milliseconds": {"n": int64(11), "l": []any{"beta"}, "d": noon.UnixMilli() - 1},
		"json.Number, RFC 3339":            {"n": json.Number("12.5"), "l": []string{"alpha", "beta"}, "d": "2026-09-19T11:00:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			r := rs.Evaluate(eval.Context{Attributes: attrs}, "f")
			if r.Reason != eval.ReasonRule || r.Variation != "on" || string(r.Value) != `{"copy":"Try free"}` {
				t.Fatalf("got %+v (value %s)", r, r.Value)
			}
		})
	}
}

func TestEvaluateAllCoversEveryFlag(t *testing.T) {
	rs := decode(t, []byte(`{"schema_version":1,"version":1,"scope":"client","unit_types":[],
		"flags":[{"key":"a","type":"boolean","on":false,"off_variation":"false",
		  "variations":[{"key":"true","value":true},{"key":"false","value":false}]},
		  {"key":"private","type":"boolean","server_only":true}]}`))
	all := rs.EvaluateAll(eval.Context{})
	if len(all) != 2 || all["a"].Reason != eval.ReasonOff || all["private"].ErrorCode != eval.ErrorServerOnly {
		t.Fatalf("got %+v", all)
	}
}

func TestCheckOperatorFollowsTheOperatorTable(t *testing.T) {
	for _, c := range []struct {
		typ   eval.AttributeType
		op    string
		value any
		ok    bool
	}{
		{eval.TypeString, "matches", "^a", true},
		{eval.TypeString, "matches", "(?=a)", false},
		{eval.TypeString, "lt", "a", false},
		{eval.TypeString, "in", []any{"a", "b"}, true},
		{eval.TypeString, "in", []any{"a", 1.0}, false},
		{eval.TypeNumber, "gte", 1.5, true},
		{eval.TypeNumber, "gte", "1.5", false},
		{eval.TypeBoolean, "eq", false, true},
		{eval.TypeBoolean, "neq", false, false},
		{eval.TypeSemver, "lt", "1.17", true},
		{eval.TypeDate, "before", "2026-01-01T00:00:00Z", true},
		{eval.TypeDate, "eq", "2026-01-01T00:00:00Z", false},
		{eval.TypeList, "contains_all", []any{"a"}, true},
		{eval.TypeList, "is_empty", nil, true},
		{eval.TypeString, "is_empty", nil, false},
		{eval.TypeString, "exists", nil, true},
		{"object", "exists", nil, false},
	} {
		err := eval.CheckOperator(c.typ, c.op, c.value)
		if (err == nil) != c.ok {
			t.Errorf("CheckOperator(%s, %s, %v) = %v, want ok %v", c.typ, c.op, c.value, err, c.ok)
		}
	}
}

// TestServerOnlyFlagEncodesWithoutConfiguration: a client document carries
// a server_only flag's key, type and automatic_exposure and nothing else.
func TestServerOnlyFlagEncodesWithoutConfiguration(t *testing.T) {
	f := eval.Flag{Key: "employee_feature", Type: "boolean", AutomaticExposure: true, ServerOnly: true, On: true,
		OffVariation: "false", Fallthrough: "true", Variations: []eval.Variation{{Key: "true", Value: json.RawMessage("true")}},
		Rules: []eval.Rule{{ID: "r1", Serve: eval.Serve{Kind: eval.ServeVariation, Variation: "true"}}}}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"key":"employee_feature","type":"boolean","automatic_exposure":true,"server_only":true}`; string(got) != want {
		t.Errorf("server_only flag = %s, want %s", got, want)
	}
	f.ServerOnly = false
	if got, err = json.Marshal(f); err != nil {
		t.Fatal(err)
	}
	var back eval.Flag
	if err := json.Unmarshal(got, &back); err != nil || !back.On || back.Rules[0].ID != "r1" {
		t.Errorf("configured flag does not round-trip: %s, %v", got, err)
	}
}
