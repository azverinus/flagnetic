package flags_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)

var ids = id.NewDeterministicSource(fixedClock{})

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

var (
	project = id.New[workspace.Project](ids)
	anna    = id.New[access.Member](ids)
)

func ptr[T any](v T) *T { return &v }

func variation(key, value string) flags.VariationInput {
	return flags.VariationInput{Key: key, Value: json.RawMessage(value)}
}

func input(key string, typ flags.Type, variations ...flags.VariationInput) flags.FlagInput {
	in := flags.FlagInput{
		ID: id.New[flags.Flag](ids), Project: project, Key: key, Type: typ, Name: "Flag " + key,
		Owner: flags.Owner{Member: anna, Eligible: true}, CreatedBy: &anna, Now: now,
	}
	if variations != nil {
		in.Variations = &variations
	}
	return in
}

// violations returns the pointer and code of every violation err carries,
// as "pointer code", failing unless err refuses the flag as invalid_flag.
func violations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_flag" {
		t.Fatalf("err = %v, want invalid_flag", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestABooleanFlagHasExactlyTrueAndFalseAndServesFalseWhenOff(t *testing.T) {
	f, err := flags.NewFlag(input("new_checkout", flags.TypeBoolean))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, v := range f.Variations {
		keys = append(keys, v.Key+"="+string(v.Value)+"/"+v.Name)
	}
	if !slices.Equal(keys, []string{"true=true/true", "false=false/false"}) {
		t.Errorf("variations = %v", keys)
	}
	if f.Version != 1 || f.ArchivedAt != nil || f.AutomaticExposure {
		t.Errorf("flag = %+v, want version 1, not archived, no automatic exposure", f)
	}
	c := flags.InitialConfiguration(f, id.New[workspace.Environment](ids), nil, now)
	if c.On || c.OffVariation != "false" || c.Fallthrough != "true" || c.Version != 1 {
		t.Errorf("configuration = %+v, want off serving false, falling through to true", c)
	}

	for name, vs := range map[string][]flags.VariationInput{
		"a third variation": {variation("true", "true"), variation("false", "false"), variation("maybe", "true")},
		"other keys":        {variation("on", "true"), variation("off", "false")},
		"swapped values":    {variation("true", "false"), variation("false", "true")},
	} {
		if _, refused := flags.NewFlag(input("b", flags.TypeBoolean, vs...)); refused == nil {
			t.Errorf("%s accepted", name)
		}
	}
	listed, err := flags.NewFlag(input("b", flags.TypeBoolean,
		flags.VariationInput{Key: "false", Value: json.RawMessage("false"), Name: ptr("Off")}, variation("true", "true")))
	if err != nil || listed.Variations[0].Name != "Off" {
		t.Errorf("listing both in any order = %+v, %v", listed.Variations, err)
	}
}

func TestAnotherTypeServesItsFirstVariationAndNamesVariationsAfterTheirKeys(t *testing.T) {
	f, err := flags.NewFlag(input("Checkout.Title", flags.TypeString, flags.VariationInput{
		Key: "short", Value: json.RawMessage(`"Pay"`), Name: ptr("Short"),
	}, flags.VariationInput{Key: "long", Value: json.RawMessage(` "Pay for the session" `), Description: ptr("Longer")}))
	if err != nil {
		t.Fatal(err)
	}
	if f.Variations[1].Name != "long" || f.Variations[1].Description != "Longer" || string(f.Variations[1].Value) != `"Pay for the session"` {
		t.Errorf("long = %+v, want named after its key and stored compact", f.Variations[1])
	}
	c := flags.InitialConfiguration(f, id.New[workspace.Environment](ids), nil, now)
	if c.On || c.OffVariation != "short" || c.Fallthrough != "short" {
		t.Errorf("configuration = %+v, want off with the first variation everywhere", c)
	}
}

func TestVariationsAreTypedByTheFlagType(t *testing.T) {
	big := `"` + strings.Repeat("x", 64*1024-2) + `"`
	for name, tc := range map[string]struct {
		typ   flags.Type
		value string
	}{
		"an empty string":     {flags.TypeString, `""`},
		"a number":            {flags.TypeNumber, `3`},
		"a negative fraction": {flags.TypeNumber, `-0.25`},
		"an exponent":         {flags.TypeNumber, `1e3`},
		"an object":           {flags.TypeJSON, `{"b":[1,2],"a":null}`},
		"an array":            {flags.TypeJSON, `[]`},
		"a JSON string":       {flags.TypeJSON, `"s"`},
		"64 KiB":              {flags.TypeJSON, big},
	} {
		if _, err := flags.NewFlag(input("f", tc.typ, variation("v", tc.value))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, tc := range map[string]struct {
		typ   flags.Type
		value string
		want  string
	}{
		"a string given a number":  {flags.TypeString, `1`, "/variations/0/value invalid_type"},
		"a string given null":      {flags.TypeString, `null`, "/variations/0/value invalid_value"},
		"a string given nothing":   {flags.TypeString, ``, "/variations/0/value invalid_value"},
		"a number given a string":  {flags.TypeNumber, `"5"`, "/variations/0/value invalid_type"},
		"a number given a boolean": {flags.TypeNumber, `true`, "/variations/0/value invalid_type"},
		"JSON given null":          {flags.TypeJSON, `null`, "/variations/0/value invalid_value"},
		"malformed JSON":           {flags.TypeJSON, `{`, "/variations/0/value invalid_value"},
		"over 64 KiB":              {flags.TypeJSON, `"` + strings.Repeat("x", 64*1024-1) + `"`, "/variations/0/value too_long"},
	} {
		_, err := flags.NewFlag(input("f", tc.typ, variation("v", tc.value)))
		if got := violations(t, err); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, tc.want)
		}
	}
	for name, tc := range map[string]struct {
		in   flags.FlagInput
		want []string
	}{
		"no variations":   {input("f", flags.TypeString), []string{"/variations invalid_value"}},
		"empty":           {input("f", flags.TypeString, []flags.VariationInput{}...), []string{"/variations invalid_value"}},
		"duplicate keys":  {input("f", flags.TypeString, variation("a", `"x"`), variation("a", `"y"`)), []string{"/variations/1/key duplicate"}},
		"a key's pattern": {input("f", flags.TypeString, variation("a b", `"x"`)), []string{"/variations/0/key invalid_key"}},
		"a long key":      {input("f", flags.TypeString, variation(strings.Repeat("k", 65), `"x"`)), []string{"/variations/0/key invalid_key"}},
	} {
		if name == "empty" {
			tc.in.Variations = &[]flags.VariationInput{}
		}
		_, err := flags.NewFlag(tc.in)
		if got := violations(t, err); !slices.Equal(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
}

func TestFlagKeysTypesNamesAndTagsAreValidated(t *testing.T) {
	for _, key := range []string{"a", "Checkout.V2", "new-checkout_3", strings.Repeat("a", 128)} {
		if _, err := flags.NewFlag(input(key, flags.TypeBoolean)); err != nil {
			t.Errorf("key %q: %v", key, err)
		}
	}
	for _, key := range []string{"", "1st", "_x", "has space", "ключ", strings.Repeat("a", 129)} {
		_, err := flags.NewFlag(input(key, flags.TypeBoolean))
		if got := violations(t, err); !slices.Equal(got, []string{"/key invalid_key"}) {
			t.Errorf("key %q: violations = %v", key, got)
		}
	}
	in := input("f", "yaml")
	in.Name, in.Description = " ", strings.Repeat("d", 2001)
	in.Tags = []string{"a", "a", " ", strings.Repeat("t", 65)}
	_, err := flags.NewFlag(in)
	want := []string{"/type invalid_type", "/name invalid_name", "/description too_long",
		"/tags/1 duplicate", "/tags/2 invalid_value", "/tags/3 invalid_value"}
	if got := violations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
	many := input("f", flags.TypeBoolean)
	for i := range 51 {
		many.Tags = append(many.Tags, "t"+strings.Repeat("x", i))
	}
	if got := violations(t, func() error { _, e := flags.NewFlag(many); return e }()); !slices.Equal(got, []string{"/tags too_many"}) {
		t.Errorf("51 tags: violations = %v", got)
	}
	none, err := flags.NewFlag(input("f", flags.TypeBoolean))
	if err != nil || none.Tags == nil {
		t.Errorf("no tags = %#v, %v, want an empty list", none.Tags, err)
	}
}

func TestAnOwnerMustBeEligible(t *testing.T) {
	in := input("f", flags.TypeBoolean)
	in.Owner.Eligible = false
	_, err := flags.NewFlag(in)
	if got := violations(t, err); !slices.Equal(got, []string{"/owner invalid_owner"}) {
		t.Errorf("violations = %v", got)
	}
	f, _ := flags.NewFlag(input("f", flags.TypeBoolean))
	_, _, err = f.Change(flags.FlagChange{Owner: &flags.Owner{Member: id.New[access.Member](ids)}}, now)
	if got := violations(t, err); !slices.Equal(got, []string{"/owner invalid_owner"}) {
		t.Errorf("changing to an ineligible owner: violations = %v", got)
	}
	_, ch, err := f.Change(flags.FlagChange{Owner: &flags.Owner{Member: anna}}, now)
	if err != nil || ch.Any {
		t.Errorf("naming the owner it has = %+v, %v, want no change whatever the owner's standing now", ch, err)
	}
}

func TestATemporaryFlagHasAnExpectedRemovalDate(t *testing.T) {
	f, err := flags.NewFlag(input("f", flags.TypeBoolean))
	if err != nil || f.Lifetime != flags.Temporary || f.ExpectedRemovalAt == nil || !f.ExpectedRemovalAt.Equal(now.AddDate(0, 0, 90)) {
		t.Fatalf("default lifetime = %v %v, %v, want temporary for 90 days", f.Lifetime, f.ExpectedRemovalAt, err)
	}
	dated := input("f", flags.TypeBoolean)
	at := time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)
	dated.ExpectedRemovalAt = &at
	if g, e := flags.NewFlag(dated); e != nil || !g.ExpectedRemovalAt.Equal(at) {
		t.Errorf("a date = %v, %v", g.ExpectedRemovalAt, e)
	}
	dated.Lifetime = flags.Permanent
	if got := violations(t, func() error { _, e := flags.NewFlag(dated); return e }()); !slices.Equal(got, []string{"/expected_removal_at invalid_value"}) {
		t.Errorf("a permanent flag with a date: violations = %v", got)
	}
	odd := input("f", flags.TypeBoolean)
	odd.Lifetime = "forever"
	if got := violations(t, func() error { _, e := flags.NewFlag(odd); return e }()); !slices.Equal(got, []string{"/lifetime invalid_value"}) {
		t.Errorf("an unknown lifetime: violations = %v", got)
	}

	permanent, _, err := f.Change(flags.FlagChange{Lifetime: ptr(flags.Permanent)}, now)
	if err != nil || permanent.ExpectedRemovalAt != nil {
		t.Fatalf("made permanent = %v, %v, want no date", permanent.ExpectedRemovalAt, err)
	}
	if _, _, e := permanent.Change(flags.FlagChange{ExpectedRemovalAt: &at}, now); e == nil {
		t.Error("a date on a permanent flag accepted")
	}
	later := now.Add(time.Hour)
	again, _, err := permanent.Change(flags.FlagChange{Lifetime: ptr(flags.Temporary)}, later)
	if err != nil || !again.ExpectedRemovalAt.Equal(later.AddDate(0, 0, 90)) {
		t.Errorf("temporary again = %v, %v, want 90 days on", again.ExpectedRemovalAt, err)
	}
	past := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	if dated, _, err := f.Change(flags.FlagChange{ExpectedRemovalAt: &past}, now); err != nil || !dated.ExpectedRemovalAt.Equal(past) {
		t.Errorf("a past date = %v, %v, want it kept as given", dated.ExpectedRemovalAt, err)
	}
}

func TestAChangeSaysWhetherItChangedTheRuleset(t *testing.T) {
	f, err := flags.NewFlag(input("copy", flags.TypeString, variation("a", `"A"`), variation("b", `"B"`)))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		change           flags.FlagChange
		changed, ruleset bool
	}{
		"nothing":                 {flags.FlagChange{}, false, false},
		"the same name":           {flags.FlagChange{Name: ptr(f.Name)}, false, false},
		"a name":                  {flags.FlagChange{Name: ptr("Copy")}, true, false},
		"tags":                    {flags.FlagChange{Tags: &[]string{"x"}}, true, false},
		"a lifetime":              {flags.FlagChange{Lifetime: ptr(flags.Permanent)}, true, false},
		"a variation's name":      {flags.FlagChange{Variations: &[]flags.VariationInput{{Key: "a", Value: json.RawMessage(`"A"`), Name: ptr("Ay")}, variation("b", `"B"`)}}, true, false},
		"a variation's value":     {flags.FlagChange{Variations: &[]flags.VariationInput{variation("a", `"AA"`), variation("b", `"B"`)}}, true, true},
		"the variations' order":   {flags.FlagChange{Variations: &[]flags.VariationInput{variation("b", `"B"`), variation("a", `"A"`)}}, true, true},
		"automatic exposure":      {flags.FlagChange{AutomaticExposure: ptr(true)}, true, true},
		"the same value, spaced ": {flags.FlagChange{Variations: &[]flags.VariationInput{variation("a", ` "A" `), variation("b", `"B"`)}}, false, false},
	} {
		after, ch, e := f.Change(tc.change, now.Add(time.Minute))
		if e != nil {
			t.Errorf("%s: %v", name, e)
			continue
		}
		if ch.Any != tc.changed || ch.Ruleset != tc.ruleset {
			t.Errorf("%s: changed %t ruleset %t, want %t %t", name, ch.Any, ch.Ruleset, tc.changed, tc.ruleset)
		}
		wantVersion := int64(1)
		if tc.changed {
			wantVersion = 2
		}
		if after.Version != wantVersion {
			t.Errorf("%s: version = %d, want %d", name, after.Version, wantVersion)
		}
	}
	_, ch, err := f.Change(flags.FlagChange{Variations: &[]flags.VariationInput{variation("A", `"A"`), variation("c", `"C"`)}}, now)
	if err != nil || !slices.Equal(ch.RemovedVariations, []string{"a", "b"}) {
		t.Errorf("renaming a and replacing b: removed %v, %v, want a and b", ch.RemovedVariations, err)
	}
}

func TestAFlagsKeyAndTypeNeverChange(t *testing.T) {
	f, _ := flags.NewFlag(input("f", flags.TypeBoolean))
	_, _, err := f.Change(flags.FlagChange{Key: ptr("f"), Type: ptr(flags.TypeString), Name: ptr("Renamed")}, now)
	if got := violations(t, err); !slices.Equal(got, []string{"/key immutable", "/type immutable"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestRemovingAVariationIsRefusedWhileAConfigurationServesIt(t *testing.T) {
	f, _ := flags.NewFlag(input("copy", flags.TypeString, variation("a", `"A"`), variation("b", `"B"`), variation("c", `"C"`)))
	staging := flags.InitialConfiguration(f, id.New[workspace.Environment](ids), nil, now)
	staging.Fallthrough = "b"
	configs := []flags.EnvironmentConfiguration{
		{Environment: "production", Configuration: flags.InitialConfiguration(f, id.New[workspace.Environment](ids), nil, now)},
		{Environment: "staging", Configuration: staging},
	}
	if refs := flags.VariationReferences(f, []string{"c"}, configs, nil); len(refs) != 0 {
		t.Errorf("c: references = %v, want none", refs)
	}
	refs := flags.VariationReferences(f, []string{"a", "b"}, configs, nil)
	want := []flags.Reference{{Kind: "flag", Key: "copy", Environment: "production"}, {Kind: "flag", Key: "copy", Environment: "staging"}}
	if !slices.Equal(refs, want) {
		t.Errorf("references = %v, want %v", refs, want)
	}
	e, ok := apperrors.From(flags.ErrInUse(refs))
	if !ok || e.Kind != apperrors.KindConflict || e.Code != "in_use" {
		t.Errorf("ErrInUse = %v, want in_use", e)
	}
}

func TestAReferenceNamingEveryEnvironmentCarriesEnvironmentNull(t *testing.T) {
	e, _ := apperrors.From(flags.ErrInUse([]flags.Reference{
		{Kind: "flag", Key: "copy", Environment: "staging"}, {Kind: "segment", Key: "beta_testers"},
	}))
	got, err := json.Marshal(e.Extensions["references"])
	want := `[{"environment":"staging","key":"copy","kind":"flag"},{"environment":null,"key":"beta_testers","kind":"segment"}]`
	if err != nil || string(got) != want {
		t.Errorf("references = %s, %v, want %s", got, err, want)
	}
}

func TestArchivingHidesAFlagUntilItIsRestoredAndOnlyAnArchivedFlagIsDeleted(t *testing.T) {
	f, _ := flags.NewFlag(input("f", flags.TypeBoolean))
	if e, ok := apperrors.From(f.CanDelete()); !ok || e.Kind != apperrors.KindConflict || e.Code != "not_archived" {
		t.Errorf("deleting an active flag = %v, want not_archived", f.CanDelete())
	}
	later := now.Add(time.Hour)
	archived, changed := f.Archive(later)
	if !changed || archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(later) || archived.Version != 2 {
		t.Fatalf("archived = %+v, %t, want archived at %v at version 2", archived, changed, later)
	}
	if err := archived.CanDelete(); err != nil {
		t.Errorf("deleting an archived flag = %v", err)
	}
	if again, twice := archived.Archive(later.Add(time.Hour)); twice || again.Version != 2 || !again.ArchivedAt.Equal(later) {
		t.Errorf("archiving again = %+v, %t, want no change", again, twice)
	}
	restored, changed := archived.Restore(later)
	if !changed || restored.ArchivedAt != nil || restored.Version != 3 {
		t.Errorf("restored = %+v, %t, want active at version 3", restored, changed)
	}
	if _, twice := restored.Restore(later); twice {
		t.Error("restoring an active flag changed it")
	}
}
