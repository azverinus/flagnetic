package flags_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// declarations are a project's standard ones plus a few of every type.
var declarations = flags.NewDeclarations(
	[]workspace.UnitType{{Key: "user"}, {Key: "anonymous"}},
	[]workspace.Attribute{
		{Key: "os", Type: workspace.TypeString}, {Key: "app_version", Type: workspace.TypeSemver},
		{Key: "seats", Type: workspace.TypeNumber}, {Key: "beta", Type: workspace.TypeBoolean},
		{Key: "signed_up", Type: workspace.TypeDate}, {Key: "roles", Type: workspace.TypeList},
	},
)

// cond returns an attribute condition whose value is the JSON v, absent
// when v is "".
func cond(attribute, op, v string) flags.Condition {
	c := flags.Condition{Kind: "attribute", Attribute: attribute, Op: op}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &c.Value); err != nil {
			panic(err)
		}
	}
	return c
}

func segment(key string, conditions ...flags.Condition) flags.SegmentInput {
	return flags.SegmentInput{
		ID: id.New[flags.Segment](ids), Project: project, Key: key, Name: "Segment " + key,
		Conditions: conditions, Declarations: declarations, Now: now,
	}
}

func segmentViolations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_segment" {
		t.Fatalf("err = %v, want invalid_segment", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestASegmentConditionNamesADeclarationWithAnOperatorAndValueOfItsType(t *testing.T) {
	for _, c := range []flags.Condition{
		cond("os", "eq", `"android"`), cond("os", "neq", `"ios"`), cond("os", "in", `["android","ios"]`), cond("os", "not_in", `[]`),
		cond("os", "contains", `"oid"`), cond("os", "starts_with", `"a"`), cond("os", "ends_with", `"d"`),
		cond("os", "matches", `"^(android|ios)$"`), cond("os", "exists", ""), cond("os", "not_exists", ""),
		cond("seats", "eq", `5`), cond("seats", "neq", `5.5`), cond("seats", "lt", `-1`), cond("seats", "gte", `1e3`), cond("seats", "in", `[1,2,3]`),
		cond("beta", "eq", `false`), cond("beta", "exists", `null`),
		cond("app_version", "lt", `"1.41"`), cond("app_version", "eq", `"v1.41.0"`), cond("app_version", "gte", `"1.41.0-beta.2"`),
		cond("app_version", "in", `["1.40.0","1.41.0"]`), cond("app_version", "not_in", `["1.39"]`),
		cond("signed_up", "before", `"2026-01-01T00:00:00Z"`), cond("signed_up", "after", `"2025-06-30T12:00:00.5+03:00"`),
		cond("roles", "contains", `"admin"`), cond("roles", "contains_any", `["a","b"]`), cond("roles", "contains_all", `["a"]`),
		cond("roles", "is_empty", ""),
		cond("user", "in", `["u-1","u-2"]`), cond("anonymous", "not_exists", ""), cond("user", "starts_with", `"b2b-"`),
	} {
		if _, err := flags.NewSegment(segment("s", c)); err != nil {
			t.Errorf("%s %s %v: %v", c.Attribute, c.Op, c.Value, err)
		}
	}
	for name, tc := range map[string]struct {
		c    flags.Condition
		want string
	}{
		"an undeclared attribute":       {cond("plan", "eq", `"pro"`), "/conditions/0/attribute unknown_attribute"},
		"lt on a string":                {cond("os", "lt", `"x"`), "/conditions/0/op invalid_operator"},
		"before on a string":            {cond("os", "before", `"x"`), "/conditions/0/op invalid_operator"},
		"contains on a number":          {cond("seats", "contains", `"5"`), "/conditions/0/op invalid_operator"},
		"neq on a boolean":              {cond("beta", "neq", `true`), "/conditions/0/op invalid_operator"},
		"matches on a semver":           {cond("app_version", "matches", `"1"`), "/conditions/0/op invalid_operator"},
		"eq on a date":                  {cond("signed_up", "eq", `"2026-01-01T00:00:00Z"`), "/conditions/0/op invalid_operator"},
		"in on a list":                  {cond("roles", "in", `["a"]`), "/conditions/0/op invalid_operator"},
		"an unknown operator":           {cond("os", "resembles", `"x"`), "/conditions/0/op invalid_operator"},
		"lt on a user":                  {cond("user", "lt", `"u"`), "/conditions/0/op invalid_operator"},
		"a number for a string":         {cond("os", "eq", `5`), "/conditions/0/value invalid_value"},
		"a string for in":               {cond("os", "in", `"android"`), "/conditions/0/value invalid_value"},
		"a mixed list":                  {cond("os", "in", `["android",5]`), "/conditions/0/value invalid_value"},
		"a string for a number":         {cond("seats", "eq", `"5"`), "/conditions/0/value invalid_value"},
		"a string for a boolean":        {cond("beta", "eq", `"true"`), "/conditions/0/value invalid_value"},
		"a list for contains":           {cond("roles", "contains", `["admin"]`), "/conditions/0/value invalid_value"},
		"no value":                      {cond("os", "eq", ""), "/conditions/0/value invalid_value"},
		"a look-ahead":                  {cond("os", "matches", `"(?=x)"`), "/conditions/0/value invalid_value"},
		"a version that is not one":     {cond("app_version", "lt", `"latest"`), "/conditions/0/value invalid_value"},
		"a list with a broken version":  {cond("app_version", "in", `["1.41.0","garbage"]`), "/conditions/0/value invalid_value"},
		"a date that is not RFC 3339":   {cond("signed_up", "before", `"2026-01-01"`), "/conditions/0/value invalid_value"},
		"a date as a number":            {cond("signed_up", "after", `1767225600000`), "/conditions/0/value invalid_value"},
		"a value for exists":            {cond("os", "exists", `"android"`), "/conditions/0/value invalid_value"},
		"a value for is_empty":          {cond("roles", "is_empty", `[]`), "/conditions/0/value invalid_value"},
		"no attribute":                  {cond("", "eq", `"x"`), "/conditions/0/attribute invalid_value"},
		"no operator":                   {cond("os", "", `"x"`), "/conditions/0/op invalid_operator"},
		"a segment on an attribute":     {flags.Condition{Kind: "attribute", Attribute: "os", Op: "exists", Segment: "x"}, "/conditions/0 invalid_value"},
		"a nested segment":              {flags.Condition{Kind: "in_segment", Segment: "x"}, "/conditions/0/kind invalid_value"},
		"a segment excluded":            {flags.Condition{Kind: "not_in_segment", Segment: "x"}, "/conditions/0/kind invalid_value"},
		"a prerequisite":                {flags.Condition{Kind: "prerequisite", Flag: "f", Variation: "v"}, "/conditions/0/kind invalid_value"},
		"an unknown kind":               {flags.Condition{Kind: "expression"}, "/conditions/0/kind invalid_value"},
		"a list of 10,001 unit IDs":     {cond("user", "in", `[`+strings.Repeat(`"u",`, 10000)+`"u"]`), "/conditions/0/value too_many"},
		"a list of 10,001 for contains": {cond("roles", "contains_any", `[`+strings.Repeat(`"r",`, 10000)+`"r"]`), "/conditions/0/value too_many"},
	} {
		_, err := flags.NewSegment(segment("s", cond("os", "exists", ""), tc.c))
		want := strings.Replace(tc.want, "/conditions/0", "/conditions/1", 1)
		if got := segmentViolations(t, err); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, want)
		}
	}
	if _, err := flags.NewSegment(segment("s", cond("user", "in", `[`+strings.Repeat(`"u",`, 9999)+`"u"]`))); err != nil {
		t.Errorf("10,000 unit IDs: %v", err)
	}
}

func TestASegmentHasAKeyANameAndAtMostTwentyConditions(t *testing.T) {
	for _, key := range []string{"", "Employees", "1st", "with space", strings.Repeat("a", 65)} {
		_, err := flags.NewSegment(segment(key))
		if got := segmentViolations(t, err); !slices.Equal(got, []string{"/key invalid_key"}) {
			t.Errorf("key %q: violations = %v", key, got)
		}
	}
	in := segment("s")
	in.Name, in.Description = " ", strings.Repeat("d", 2001)
	_, err := flags.NewSegment(in)
	if got := segmentViolations(t, err); !slices.Equal(got, []string{"/name invalid_name", "/description too_long"}) {
		t.Errorf("violations = %v", got)
	}
	twenty := make([]flags.Condition, 20)
	for i := range twenty {
		twenty[i] = cond("os", "exists", "")
	}
	s, err := flags.NewSegment(segment("s", twenty...))
	if err != nil || s.Version != 1 || len(s.Conditions) != 20 {
		t.Fatalf("20 conditions = %+v, %v", s, err)
	}
	_, err = flags.NewSegment(segment("s", append(twenty, cond("os", "exists", ""))...))
	if got := segmentViolations(t, err); !slices.Equal(got, []string{"/conditions too_many"}) {
		t.Errorf("21 conditions: violations = %v", got)
	}
	empty, err := flags.NewSegment(segment("everyone"))
	if err != nil || empty.Conditions == nil {
		t.Errorf("no conditions = %#v, %v, want an empty list", empty.Conditions, err)
	}
}

func TestASegmentChangeSaysWhetherItChangedTheRuleset(t *testing.T) {
	s, err := flags.NewSegment(segment("android_old", cond("os", "eq", `"android"`), cond("app_version", "lt", `"1.41.0"`)))
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	for name, tc := range map[string]struct {
		change           flags.SegmentChange
		changed, ruleset bool
	}{
		"nothing":               {flags.SegmentChange{}, false, false},
		"the same conditions":   {flags.SegmentChange{Conditions: &[]flags.Condition{cond("os", "eq", `"android"`), cond("app_version", "lt", `"1.41.0"`)}, Private: ptr(false)}, false, false},
		"a name":                {flags.SegmentChange{Name: ptr("Old Android")}, true, false},
		"a description":         {flags.SegmentChange{Description: ptr("Before 1.41")}, true, false},
		"private":               {flags.SegmentChange{Private: ptr(true)}, true, true},
		"conditions":            {flags.SegmentChange{Conditions: &[]flags.Condition{cond("os", "in", `["android","harmony"]`)}}, true, true},
		"no conditions":         {flags.SegmentChange{Conditions: &[]flags.Condition{}}, true, true},
		"the conditions' order": {flags.SegmentChange{Conditions: &[]flags.Condition{cond("app_version", "lt", `"1.41.0"`), cond("os", "eq", `"android"`)}}, true, true},
	} {
		after, ch, e := s.Change(tc.change, declarations, later)
		if e != nil {
			t.Errorf("%s: %v", name, e)
			continue
		}
		if ch.Changed != tc.changed || ch.Ruleset != tc.ruleset {
			t.Errorf("%s: changed %t ruleset %t, want %t %t", name, ch.Changed, ch.Ruleset, tc.changed, tc.ruleset)
		}
		if tc.changed && (after.Version != 2 || !after.UpdatedAt.Equal(later)) {
			t.Errorf("%s: version %d at %v, want 2 at %v", name, after.Version, after.UpdatedAt, later)
		}
	}
	_, _, err = s.Change(flags.SegmentChange{Key: ptr("android"), Conditions: &[]flags.Condition{cond("plan", "eq", `"x"`)}}, declarations, later)
	if got := segmentViolations(t, err); !slices.Equal(got, []string{"/key immutable", "/conditions/0/attribute unknown_attribute"}) {
		t.Errorf("violations = %v", got)
	}
}
