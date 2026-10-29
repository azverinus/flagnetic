package transfer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func ptr[T any](v T) *T { return &v }

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// sample is a document using every section and every optional field, with
// each list in an order the encoding must not keep.
func sample() transfer.Document {
	removal := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	started := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return transfer.Document{
		SchemaVersion: transfer.Version,
		Project:       &transfer.Project{Key: "shop", Name: "Shop", Description: "The shop."},
		Environments: []transfer.Environment{
			{Key: "production", Name: "Production", Protected: true},
			{Key: "development", Name: "Development"},
		},
		UnitTypes: []transfer.UnitType{{Key: "user", Name: "User"}, {Key: "device", Name: "Device", Description: "A phone."}},
		Attributes: []transfer.Attribute{
			{Key: "plan", Type: "string", Name: "Plan"},
			{Key: "app_version", Type: "semver", Name: "App version"},
		},
		Segments: []transfer.Segment{
			{Key: "pro", Name: "Pro", Conditions: []transfer.Condition{{Kind: "attribute", Attribute: "plan", Op: "eq", Value: "pro"}}},
			{Key: "employees", Name: "Employees", Private: true,
				Conditions: []transfer.Condition{{Kind: "attribute", Attribute: "user", Op: "in", Value: []any{"u2", "u1"}}}},
		},
		Flags: []transfer.Flag{
			{
				Key: "new_matching", Type: "boolean", Name: "New matching", Owner: "owner@example.com",
				Tags: []string{"mobile", "matching"}, Lifetime: "temporary", ExpectedRemovalAt: &removal,
				Variations: []transfer.Variation{
					{Key: "true", Value: raw(`true`), Name: "true"}, {Key: "false", Value: raw(`false`), Name: "false"},
				},
				Configurations: map[string]transfer.Configuration{
					"production": {
						On: true, OffVariation: "false", Fallthrough: "false",
						Prerequisites: []transfer.Prerequisite{{Flag: "matching_v2", Variation: "true"}, {Flag: "a_flag", Variation: "x"}},
						Overrides: []transfer.Override{
							{UnitType: "user", UnitID: "qa-9", Variation: "true", Note: "Demo"},
							{UnitType: "device", UnitID: "z", Variation: "true"},
							{UnitType: "user", UnitID: "qa-7", Variation: "true"},
						},
						Rules: []transfer.Rule{
							{ID: "0192f3a0-0000-7000-8000-000000000002", Description: "Employees first",
								Conditions: []transfer.Condition{{Kind: "in_segment", Segment: "employees"}},
								Serve:      transfer.Serve{Kind: "variation", Variation: "true"}},
							{ID: "0192f3a0-0000-7000-8000-000000000001",
								Conditions: []transfer.Condition{{Kind: "attribute", Attribute: "app_version", Op: "gte", Value: "1.41.0"}},
								Serve:      transfer.Serve{Kind: "rollout", Variation: "true", UnitType: "user", Percentage: ptr(0), Salt: "abcdefghij012345"}},
						},
					},
					"development": {OffVariation: "false", Fallthrough: "true"},
				},
			},
			{
				Key: "banner", Type: "json", Name: "Banner", Lifetime: "permanent", Archived: true, AutomaticExposure: true,
				Variations: []transfer.Variation{
					{Key: "a", Value: raw(`{"b":1,"a":"<hello & goodbye>"}`), Name: "A"},
					{Key: "b", Value: raw(`[1, 2.5]`), Name: "B"},
				},
			},
		},
		Experiments: []transfer.Experiment{{
			Key: "zeta_draft", Name: "Zeta", Flag: "new_matching", UnitType: "user", TrafficAllocation: 10000, Status: "draft",
			Variations:    []transfer.ExperimentVariation{{Variation: "true", Weight: 10000}},
			InclusionSalt: "inclusion1111111", VariationSalt: "variation1111111",
		}, {
			Key: "checkout_test", Name: "Checkout", Hypothesis: "Fewer steps convert.", Owner: "owner@example.com",
			Flag: "new_matching", UnitType: "user", TrafficAllocation: 5000,
			Variations:    []transfer.ExperimentVariation{{Variation: "true", Weight: 5000}, {Variation: "false", Weight: 5000}},
			InclusionSalt: "inclusion0000000", VariationSalt: "variation0000000", Status: "stopped",
			ExpectedEndAt: &transfer.Date{Time: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)},
			StartedAt:     &started, StoppedAt: &started,
			Decision: &transfer.Decision{Outcome: "winner", WinnerVariation: "true", Note: "on"},
		}},
	}
}

func mustEncode(t *testing.T, format transfer.Format, d transfer.Document) []byte {
	t.Helper()
	b, err := transfer.Encode(format, d)
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return b
}

func TestJSONIsIndentedWithATrailingNewlineAndSortedByKey(t *testing.T) {
	b := mustEncode(t, transfer.JSON, sample())
	s := string(b)
	if !strings.HasPrefix(s, "{\n  \"schema_version\": 1,\n  \"project\": {\n    \"key\": \"shop\"") || !strings.HasSuffix(s, "}\n") {
		t.Errorf("document is not indented by two spaces and ended by a newline:\n%.200s", s)
	}

	var d struct {
		Environments []struct{ Key string }
		UnitTypes    []struct{ Key string } `json:"unit_types"`
		Attributes   []struct{ Key string }
		Segments     []struct{ Key string }
		Flags        []struct {
			Key            string
			Configurations map[string]struct {
				Prerequisites []struct{ Flag string }
				Overrides     []struct {
					UnitType string `json:"unit_type"`
					UnitID   string `json:"unit_id"`
				}
				Rules []struct{ ID string }
			}
		}
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	keys := func(v any) []string {
		var out []string
		switch v := v.(type) {
		case []struct{ Key string }:
			for _, e := range v {
				out = append(out, e.Key)
			}
		}
		return out
	}
	if got := keys(d.Environments); !slices.Equal(got, []string{"production", "development"}) {
		t.Errorf("environments = %v, want them in the given sort order", got)
	}
	for name, got := range map[string][]string{
		"unit_types": keys(d.UnitTypes), "attributes": keys(d.Attributes), "segments": keys(d.Segments),
	} {
		if !slices.IsSorted(got) || len(got) != 2 {
			t.Errorf("%s = %v, want them by key", name, got)
		}
	}
	if d.Flags[0].Key != "banner" || d.Flags[1].Key != "new_matching" {
		t.Errorf("flags = %v, want them by key", d.Flags)
	}
	c := d.Flags[1].Configurations["production"]
	if c.Prerequisites[0].Flag != "a_flag" {
		t.Errorf("prerequisites = %v, want them by flag", c.Prerequisites)
	}
	if o := c.Overrides; o[0].UnitType != "device" || o[1].UnitID != "qa-7" || o[2].UnitID != "qa-9" {
		t.Errorf("overrides = %v, want them by unit type and unit ID", o)
	}
	// A rule's place is its priority, so the rules keep theirs.
	if c.Rules[0].ID != "0192f3a0-0000-7000-8000-000000000002" {
		t.Errorf("rules = %v, want them in the given order", c.Rules)
	}
	if strings.Index(s, `"development": {`) > strings.Index(s, `"production": {`) {
		t.Errorf("configurations are not by environment key")
	}
}

func TestEveryOptionalFieldIsWritten(t *testing.T) {
	b := string(mustEncode(t, transfer.JSON, sample()))
	for _, want := range []string{
		`"description": ""`, `"owner": ""`, `"expected_removal_at": null`, `"note": ""`, `"percentage": 0`,
		`"started_at": null`, `"decision": null`, `"tags": []`, `"prerequisites": []`, `"rules": []`, `"overrides": []`,
		`"expected_end_at": "2026-11-05"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("document lacks %s:\n%s", want, b)
		}
	}
	// A value is written once, with its keys in order, its numbers as they
	// were and nothing escaped that JSON does not need escaped.
	if !strings.Contains(b, "\"value\": {\n            \"a\": \"<hello & goodbye>\",\n            \"b\": 1\n          }") || !strings.Contains(b, "2.5\n") {
		t.Errorf("variation values are not canonical:\n%s", b)
	}
}

func TestTwoEncodingsOfTheSameProjectAreTheSameBytes(t *testing.T) {
	a, b := sample(), sample()
	slices.Reverse(b.Flags)
	slices.Reverse(b.Segments)
	slices.Reverse(b.UnitTypes)
	slices.Reverse(b.Flags[0].Configurations["production"].Overrides)
	for _, f := range []transfer.Format{transfer.JSON, transfer.YAML} {
		if x, y := mustEncode(t, f, a), mustEncode(t, f, b); !bytes.Equal(x, y) {
			t.Errorf("%s: encodings differ:\n%s\n%s", f, x, y)
		}
		if x, y := mustEncode(t, f, a), mustEncode(t, f, a); !bytes.Equal(x, y) {
			t.Errorf("%s: two encodings of one document differ", f)
		}
	}
}

func TestEncodingDoesNotChangeTheDocumentItIsGiven(t *testing.T) {
	d := sample()
	mustEncode(t, transfer.JSON, d)
	if d.Flags[0].Key != "new_matching" || d.UnitTypes[0].Key != "user" {
		t.Errorf("encoding sorted the caller's lists")
	}
}

func TestYAMLIsTheJSONDocumentFieldForField(t *testing.T) {
	d := sample()
	asJSON, asYAML := mustEncode(t, transfer.JSON, d), mustEncode(t, transfer.YAML, d)

	var fromYAML, fromJSON any
	if err := yaml.Unmarshal(asYAML, &fromYAML); err != nil {
		t.Fatalf("YAML does not parse: %v\n%s", err, asYAML)
	}
	if err := json.Unmarshal(asJSON, &fromJSON); err != nil {
		t.Fatal(err)
	}
	// Compare as JSON text, which sorts map keys.
	x, _ := json.Marshal(fromYAML)
	y, _ := json.Marshal(fromJSON)
	if !bytes.Equal(x, y) {
		t.Errorf("YAML differs from JSON:\n%s\n%s", x, y)
	}
	// A YAML 1.1 reader takes a bare on for a boolean.
	if !bytes.Contains(asYAML, []byte(`"on": true`)) {
		t.Errorf("the key on is not quoted:\n%s", asYAML)
	}
	// Field order follows the JSON document's.
	if !strings.HasPrefix(string(asYAML), "schema_version: 1\nproject:\n  key: shop\n") {
		t.Errorf("YAML is not in the JSON document's order, indented by two spaces:\n%.120s", asYAML)
	}
}

func TestYAMLQuotesTextAYAML11ReaderTakesForAnotherType(t *testing.T) {
	d := sample()
	// Base-60 numbers, the merge key and the value key are not text to a
	// YAML 1.1 reader when written bare.
	odd := []string{"1:20", "-10:30:05", "1:20.5", "<<", "="}
	d.Flags[0].Tags = odd
	asYAML := mustEncode(t, transfer.YAML, d)
	for _, s := range odd {
		if !bytes.Contains(asYAML, []byte(`- "`+s+`"`)) {
			t.Errorf("%s is not quoted:\n%s", s, asYAML)
		}
	}
	got, err := transfer.Decode(transfer.YAML, asYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Flags[1].Tags, odd) {
		t.Errorf("tags = %q, want %q", got.Flags[1].Tags, odd)
	}
}

func TestYAMLTextThatLooksLikeATimestampStaysAsWritten(t *testing.T) {
	src := `
schema_version: 1
project: {key: demo, name: 2026-11-05, description: 2001-12-14 21:59:43.10 -5}
flags:
  - key: launch
    type: string
    name: Launch
    variations:
      - {key: day, value: 2026-11-05, name: Day}
experiments:
  - {key: e, expected_end_at: 2026-11-05, started_at: 2026-10-01T09:00:00Z}
`
	d, err := transfer.Decode(transfer.YAML, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.Project.Name != "2026-11-05" || d.Project.Description != "2001-12-14 21:59:43.10 -5" {
		t.Errorf("project = %+v, want its text as written", d.Project)
	}
	if v := string(d.Flags[0].Variations[0].Value); v != `"2026-11-05"` {
		t.Errorf("variation value = %s, want the text 2026-11-05", v)
	}
	e := d.Experiments[0]
	if e.ExpectedEndAt == nil || e.ExpectedEndAt.Format(time.DateOnly) != "2026-11-05" ||
		e.StartedAt == nil || !e.StartedAt.Equal(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("experiment = %+v, want its day and instant read", e)
	}
}

func TestADocumentSurvivesDecodingInEitherFormat(t *testing.T) {
	d := sample()
	want := mustEncode(t, transfer.JSON, d)
	for _, f := range []transfer.Format{transfer.JSON, transfer.YAML} {
		got, err := transfer.Decode(f, mustEncode(t, f, d))
		if err != nil {
			t.Fatalf("decode %s: %v", f, err)
		}
		if b := mustEncode(t, transfer.JSON, got); !bytes.Equal(b, want) {
			t.Errorf("%s: decoded document encodes differently:\n%s\nwant\n%s", f, b, want)
		}
	}
}

func TestAHandWrittenYAMLDocumentIsReadWithItsOptionalFieldsAbsent(t *testing.T) {
	src := `
schema_version: 1
project: {key: demo, name: Demo}
environments:
  - {key: staging, name: Staging, protected: false}
flags:
  - key: dark_mode
    type: boolean
    name: Dark mode
    expected_removal_at: 2027-01-02T03:04:05Z
    configurations:
      staging:
        "on": true
        rules:
          - conditions: []
            serve: {kind: rollout, unit_type: user, variation: "true", percentage: 2500}
experiments: []
`
	d, err := transfer.Decode(transfer.YAML, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.UnitTypes != nil || d.Segments != nil || d.Attributes != nil {
		t.Errorf("absent sections are not nil: %+v", d)
	}
	if d.Experiments == nil || d.Environments[0].Key != "staging" {
		t.Errorf("present sections are lost: %+v", d)
	}
	f := d.Flags[0]
	if f.ExpectedRemovalAt == nil || !f.ExpectedRemovalAt.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("expected_removal_at = %v", f.ExpectedRemovalAt)
	}
	r := f.Configurations["staging"].Rules[0]
	if r.ID != "" || r.Serve.Salt != "" || r.Serve.Percentage == nil || *r.Serve.Percentage != 2500 {
		t.Errorf("rule = %+v, want no ID or salt and a share of 2500", r)
	}
}

func violations(t *testing.T, err error, code string) []apperrors.Violation {
	t.Helper()
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Kind != apperrors.KindInvalid || e.Code != code {
		t.Fatalf("error = %v, want an invalid %s", err, code)
	}
	return e.Violations
}

func TestStrictDecodingRefusesWhatItDoesNotKnowNamingItsPointer(t *testing.T) {
	const doc = `{"schema_version": 1, "project": {"key": "p", "name": "P", "colour": "red"},
		"flags": [{"key": "f", "configurations": {"production": {"on": true, "rules": [{"serve": {"kind": "variation", "weight": 1}}]}}},
		          {"key": "g", "extra": 1}],
		"surprise": []}`
	for _, f := range []transfer.Format{transfer.JSON, transfer.YAML} {
		src := []byte(doc)
		if f == transfer.YAML {
			var v any
			if err := json.Unmarshal(src, &v); err != nil {
				t.Fatal(err)
			}
			src, _ = yaml.Marshal(v)
		}
		_, err := transfer.Decode(f, src)
		var got []string
		for _, v := range violations(t, err, "invalid_document") {
			if v.Code != "unknown_field" {
				t.Errorf("%s: violation %+v, want unknown_field", f, v)
			}
			got = append(got, v.Pointer)
		}
		want := []string{
			"/flags/0/configurations/production/rules/0/serve/weight", "/flags/1/extra", "/project/colour", "/surprise",
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: pointers = %v, want %v", f, got, want)
		}
	}
}

func TestStrictDecodingRefusesAValueOfTheWrongType(t *testing.T) {
	_, err := transfer.Decode(transfer.JSON, []byte(`{"schema_version": 1,
		"environments": [{"key": "a", "name": "A", "protected": "yes"}, {"key": 3}],
		"flags": {"key": "f"},
		"experiments": [{"traffic_allocation": 1.5, "started_at": "yesterday", "expected_end_at": "soon"}]}`))
	got := map[string]string{}
	for _, v := range violations(t, err, "invalid_document") {
		got[v.Pointer] = v.Code
	}
	want := map[string]string{
		"/environments/0/protected": "invalid_type", "/environments/1/key": "invalid_type", "/flags": "invalid_type",
		"/experiments/0/traffic_allocation": "invalid_type", "/experiments/0/started_at": "invalid_value",
		"/experiments/0/expected_end_at": "invalid_value",
	}
	if len(got) != len(want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("violation at %s = %q, want %q (all: %v)", p, got[p], c, got)
		}
	}
}

func TestADocumentOfAnotherSchemaVersionIsRefusedNamingTheVersionsRead(t *testing.T) {
	for _, src := range []string{`{"schema_version": 2, "newer": true}`, `{"schema_version": "1"}`, `{"schema_version": 1.5}`, `{"schema_version": 0}`} {
		_, err := transfer.Decode(transfer.JSON, []byte(src))
		var e *apperrors.Error
		if !errors.As(err, &e) || e.Kind != apperrors.KindInvalid || e.Code != "schema_version_unsupported" {
			t.Errorf("%s: error = %v, want schema_version_unsupported", src, err)
			continue
		}
		if v, _ := e.Extensions["supported_versions"].([]int); !slices.Equal(v, transfer.SupportedVersions) {
			t.Errorf("%s: supported_versions = %v", src, e.Extensions)
		}
	}
	_, err := transfer.Decode(transfer.JSON, []byte(`{"flags": []}`))
	if v := violations(t, err, "invalid_document"); len(v) != 1 || v[0].Pointer != "/schema_version" || v[0].Code != "required" {
		t.Errorf("a document without a schema_version: %+v", v)
	}
}

func TestMalformedAndTrailingInputIsRefusedAsMalformed(t *testing.T) {
	for name, src := range map[string]struct {
		format transfer.Format
		body   string
	}{
		"truncated JSON":      {transfer.JSON, `{"schema_version": 1,`},
		"trailing JSON":       {transfer.JSON, `{"schema_version": 1} {"schema_version": 1}`},
		"trailing JSON text":  {transfer.JSON, `{"schema_version": 1} x`},
		"not an object":       {transfer.JSON, `[1]`},
		"a repeated JSON key": {transfer.JSON, `{"schema_version": 1, "project": {"key": "a", "name": "A", "key": "b"}}`},
		"a repeated top key":  {transfer.JSON, `{"schema_version": 1, "flags": [], "flags": []}`},
		"a repeated YAML key": {transfer.YAML, "schema_version: 1\nflags: []\nflags: []\n"},
		"empty":               {transfer.JSON, ``},
		"bad YAML":            {transfer.YAML, "schema_version: [1\n"},
		"a second YAML file":  {transfer.YAML, "schema_version: 1\n---\nschema_version: 1\n"},
		"YAML with a non-key": {transfer.YAML, "schema_version: 1\n? [a]\n: b\n"},
	} {
		_, err := transfer.Decode(src.format, []byte(src.body))
		var m *transfer.MalformedError
		if !errors.As(err, &m) {
			t.Errorf("%s: error = %v, want a MalformedError", name, err)
		}
	}
}

func TestTheSameKeyInSeparateObjectsOrAsAValueIsNotRepeated(t *testing.T) {
	const doc = `{"schema_version": 1, "project": {"key": "name", "name": "key", "description": "description"},
		"environments": [{"key": "a", "name": "A", "protected": false}, {"key": "b", "name": "B", "protected": false}]}`
	if _, err := transfer.Decode(transfer.JSON, []byte(doc)); err != nil {
		t.Fatal(err)
	}
}

func TestFormatsAreNamedAndHaveAnExtension(t *testing.T) {
	if f, ok := transfer.ParseFormat("yaml"); !ok || f != transfer.YAML || f.Extension() != "yaml" {
		t.Errorf("yaml = %v %v", f, ok)
	}
	if f, ok := transfer.ParseFormat("json"); !ok || f != transfer.JSON || f.Extension() != "json" {
		t.Errorf("json = %v %v", f, ok)
	}
	if _, ok := transfer.ParseFormat("xml"); ok {
		t.Errorf("xml parsed")
	}
}
