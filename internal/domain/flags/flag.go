package flags

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// FlagID identifies a flag.
type FlagID = id.Of[Flag]

// Type is the kind of value a flag serves.
type Type string

// The flag types.
const (
	TypeBoolean Type = "boolean"
	TypeString  Type = "string"
	TypeNumber  Type = "number"
	TypeJSON    Type = "json"
)

// Types returns every flag type.
func Types() []Type { return []Type{TypeBoolean, TypeString, TypeNumber, TypeJSON} }

// Lifetime says whether a flag is meant to be removed one day.
type Lifetime string

// The lifetimes.
const (
	// Permanent flags stay, such as an operational switch.
	Permanent Lifetime = "permanent"
	// Temporary flags are removed once their feature is released, by an
	// expected date after which they are reported.
	Temporary Lifetime = "temporary"
)

// defaultLifetime is how long a temporary flag is expected to live when its
// author names no date: long enough to release a feature, short enough that
// a forgotten flag is reported within a quarter.
const defaultLifetime = 90 * 24 * time.Hour

// flagKeyPattern lets a flag be named the way code names it
// (checkout.newFlow): capitals and dots are allowed, unlike other keys.
var flagKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$`)

// variationKeyPattern names a variation in rules and results.
var variationKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

// Bounds of a flag definition.
const (
	maxTags        = 50
	maxTagLength   = 64
	maxValueLength = 64 * 1024
)

// Variation is one of the values a flag can serve.
type Variation struct {
	Key string
	// Value is compact JSON of the flag's type.
	Value       json.RawMessage
	Name        string
	Description string
}

// VariationInput is a variation as a write gives it: a value as JSON, nil
// when absent, and a name and description that may be absent.
type VariationInput struct {
	Key               string
	Value             json.RawMessage
	Name, Description *string
}

// Owner is the member a write names as a flag's owner, and whether they may
// own one: an active member holding a grant in the project, or an instance
// admin. The handler finds that out; the flag only refuses one who may not.
type Owner struct {
	Member   access.MemberID
	Eligible bool
}

// Flag is a flag's definition: what it is in every environment at once.
type Flag struct {
	ID      FlagID
	Project workspace.ProjectID
	// Key names the flag in code and never changes; unique, case
	// sensitively, in its project.
	Key string
	// Type never changes either: code reads the flag's value as it.
	Type        Type
	Name        string
	Description string
	Owner       access.MemberID
	Tags        []string
	Lifetime    Lifetime
	// ExpectedRemovalAt is set for a temporary flag only.
	ExpectedRemovalAt *time.Time
	// AutomaticExposure makes SDKs record an exposure after every evaluation
	// whose result carries an experiment.
	AutomaticExposure bool
	Variations        []Variation
	// ArchivedAt is set while the flag is archived: hidden from SDKs and
	// kept until deleted.
	ArchivedAt *time.Time
	// CreatedBy is the member who created the flag; nil for a project token.
	CreatedBy *access.MemberID
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts the definition's accepted changes, from 1.
	Version int64
}

// FlagInput is what a flag is created with. A nil Variations gives a
// boolean flag its two; every other type needs them listed.
type FlagInput struct {
	ID          FlagID
	Project     workspace.ProjectID
	Key         string
	Type        Type
	Name        string
	Description string
	Owner       Owner
	Tags        []string
	// Lifetime is temporary when empty.
	Lifetime          Lifetime
	ExpectedRemovalAt *time.Time
	AutomaticExposure bool
	Variations        *[]VariationInput
	CreatedBy         *access.MemberID
	Now               time.Time
}

// NewFlag returns the flag in, at version 1, or invalid_flag with every
// field to fix.
func NewFlag(in FlagInput) (Flag, error) {
	c := newCheck("invalid_flag", "the flag is not valid")
	if !flagKeyPattern.MatchString(in.Key) {
		c.add("/key", "invalid_key",
			"a flag key starts with a letter and has at most 128 letters, digits, dots, hyphens and underscores",
			map[string]any{"pattern": flagKeyPattern.String()})
	}
	typeKnown := slices.Contains(Types(), in.Type)
	if !typeKnown {
		types := make([]string, 0, 4)
		for _, t := range Types() {
			types = append(types, string(t))
		}
		c.add("/type", "invalid_type", "the type is not one a flag can have", map[string]any{"types": types})
	}
	f := Flag{
		ID: in.ID, Project: in.Project, Key: in.Key, Type: in.Type,
		Name: c.name("/name", in.Name), Description: c.description("/description", in.Description),
		Owner: c.owner(in.Owner), Tags: c.tags(in.Tags), AutomaticExposure: in.AutomaticExposure,
		CreatedBy: in.CreatedBy, CreatedAt: in.Now, UpdatedAt: in.Now, Version: 1,
	}
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = Temporary
	}
	f.Lifetime, f.ExpectedRemovalAt = c.lifetime(lifetime, in.ExpectedRemovalAt, nil, in.Now)
	if typeKnown {
		f.Variations = c.variations(in.Type, in.Variations)
	}
	if err := c.err(); err != nil {
		return Flag{}, err
	}
	return f, nil
}

func (c *check) owner(o Owner) access.MemberID {
	if !o.Eligible {
		c.add("/owner", "invalid_owner",
			"a flag's owner is an active member holding a grant in the project, or an instance admin", nil)
	}
	return o.Member
}

// tags returns tags trimmed, never nil.
func (c *check) tags(tags []string) []string {
	if len(tags) > maxTags {
		c.add("/tags", "too_many", "a flag has at most 50 tags", map[string]any{"max": maxTags})
		return tags
	}
	out := make([]string, 0, len(tags))
	for i, t := range tags {
		t = strings.TrimSpace(t)
		pointer := "/tags/" + strconv.Itoa(i)
		switch {
		case t == "" || utf8.RuneCountInString(t) > maxTagLength:
			c.add(pointer, "invalid_value", "a tag has 1 to 64 characters and is not blank",
				map[string]any{"min": 1, "max": maxTagLength})
		case slices.Contains(out, t):
			c.add(pointer, "duplicate", "the flag has this tag already", nil)
		}
		out = append(out, t)
	}
	return out
}

// lifetime returns the lifetime and expected removal date a flag has after
// a write naming l and at; current is the date it had before, if any. A
// temporary flag keeps its date unless given one, and is given 90 days from
// now when it has none; a permanent flag has none.
func (c *check) lifetime(l Lifetime, at, current *time.Time, now time.Time) (Lifetime, *time.Time) {
	switch l {
	case Permanent:
		if at != nil {
			c.add("/expected_removal_at", "invalid_value", "a permanent flag has no expected removal date", nil)
		}
		return l, nil
	case Temporary:
		switch {
		case at != nil:
			d := at.UTC().Truncate(time.Microsecond)
			return l, &d
		case current != nil:
			return l, current
		default:
			d := now.Add(defaultLifetime).UTC().Truncate(time.Microsecond)
			return l, &d
		}
	}
	c.add("/lifetime", "invalid_value", "a lifetime is permanent or temporary",
		map[string]any{"lifetimes": []string{string(Permanent), string(Temporary)}})
	return l, nil
}

// booleanVariations are the two a boolean flag has, whatever else it lists.
func booleanVariations() []VariationInput {
	return []VariationInput{{Key: "true", Value: json.RawMessage("true")}, {Key: "false", Value: json.RawMessage("false")}}
}

// variations returns the variations of a flag of typ as given, each value
// compact and each name defaulting to its key.
func (c *check) variations(typ Type, given *[]VariationInput) []Variation {
	var in []VariationInput
	switch {
	case given != nil:
		in = *given
	case typ == TypeBoolean:
		in = booleanVariations()
	}
	if len(in) == 0 {
		c.add("/variations", "invalid_value", "a flag has at least one variation", map[string]any{"min": 1})
		return nil
	}
	out := make([]Variation, 0, len(in))
	seen := map[string]bool{}
	for i, v := range in {
		pointer := "/variations/" + strconv.Itoa(i)
		switch {
		case !variationKeyPattern.MatchString(v.Key):
			c.add(pointer+"/key", "invalid_key", "a variation key has 1 to 64 letters, digits, dots, hyphens and underscores",
				map[string]any{"pattern": variationKeyPattern.String()})
		case seen[v.Key]:
			c.add(pointer+"/key", "duplicate", "the flag has a variation with this key already", nil)
		}
		seen[v.Key] = true
		value := c.value(pointer+"/value", typ, v.Value)
		name := v.Key
		if v.Name != nil && strings.TrimSpace(*v.Name) != "" {
			name = c.name(pointer+"/name", *v.Name)
		}
		description := ""
		if v.Description != nil {
			description = c.description(pointer+"/description", *v.Description)
		}
		out = append(out, Variation{Key: v.Key, Value: value, Name: name, Description: description})
	}
	if typ == TypeBoolean && !isBooleanPair(out) {
		c.add("/variations", "invalid_value",
			"a boolean flag has exactly two variations: true with the value true and false with the value false", nil)
	}
	return out
}

func isBooleanPair(vs []Variation) bool {
	if len(vs) != 2 {
		return false
	}
	for _, v := range vs {
		if (v.Key != "true" || string(v.Value) != "true") && (v.Key != "false" || string(v.Value) != "false") {
			return false
		}
	}
	return vs[0].Key != vs[1].Key
}

// value returns raw compact, refusing a value that is absent, null, not
// JSON, not of typ or over 64 KiB, which an SDK would otherwise receive as
// a value its code cannot read.
func (c *check) value(pointer string, typ Type, raw json.RawMessage) json.RawMessage {
	var compact bytes.Buffer
	if len(bytes.TrimSpace(raw)) == 0 || json.Compact(&compact, raw) != nil || compact.String() == "null" {
		c.add(pointer, "invalid_value", "a variation needs a JSON value other than null", nil)
		return nil
	}
	if compact.Len() > maxValueLength {
		c.add(pointer, "too_long", "a variation's value has at most 64 KiB of compact JSON", map[string]any{"max": maxValueLength})
		return nil
	}
	first := compact.Bytes()[0]
	var ok bool
	switch typ {
	case TypeBoolean:
		ok = first == 't' || first == 'f'
	case TypeString:
		ok = first == '"'
	case TypeNumber:
		ok = first == '-' || (first >= '0' && first <= '9')
	default:
		ok = true
	}
	if !ok {
		c.add(pointer, "invalid_type", "the value is not of the flag's type", map[string]any{"type": string(typ)})
		return nil
	}
	return compact.Bytes()
}

// FlagChange is a change to a flag's definition; a nil field is left as it
// is.
type FlagChange struct {
	// Key and Type are refused whatever their value: code reading the flag
	// relies on both.
	Key               *string
	Type              *Type
	Name              *string
	Description       *string
	Owner             *Owner
	Tags              *[]string
	Lifetime          *Lifetime
	ExpectedRemovalAt *time.Time
	AutomaticExposure *bool
	Variations        *[]VariationInput
}

// Changed is what a change did.
type Changed struct {
	// Changed is false for a change to the values the flag already has,
	// which is stored and recorded nowhere.
	Changed bool
	// Ruleset reports a change to what a ruleset carries of the flag: its
	// variations' keys, values and order, or automatic exposure.
	Ruleset bool
	// RemovedVariations are the keys of the variations the change removes,
	// which nothing may still serve.
	RemovedVariations []string
}

// Change returns the flag with ch applied at now, its version incremented
// when anything changed, and what changed. The owner is judged only when
// the change names a new one: ownership is never checked afterwards, so a
// member losing their grants never blocks a change to their flags.
func (f Flag) Change(ch FlagChange, now time.Time) (Flag, Changed, error) {
	c := newCheck("invalid_flag", "the flag is not valid")
	if ch.Key != nil {
		c.immutable("/key", "a flag's key")
	}
	if ch.Type != nil {
		c.immutable("/type", "a flag's type")
	}
	out := f
	if ch.Name != nil {
		out.Name = c.name("/name", *ch.Name)
	}
	if ch.Description != nil {
		out.Description = c.description("/description", *ch.Description)
	}
	if ch.Owner != nil && ch.Owner.Member != f.Owner {
		out.Owner = c.owner(*ch.Owner)
	}
	if ch.Tags != nil {
		out.Tags = c.tags(*ch.Tags)
	}
	if ch.Lifetime != nil || ch.ExpectedRemovalAt != nil {
		l := f.Lifetime
		if ch.Lifetime != nil {
			l = *ch.Lifetime
		}
		current := f.ExpectedRemovalAt
		if l != f.Lifetime {
			current = nil
		}
		out.Lifetime, out.ExpectedRemovalAt = c.lifetime(l, ch.ExpectedRemovalAt, current, now)
	}
	if ch.AutomaticExposure != nil {
		out.AutomaticExposure = *ch.AutomaticExposure
	}
	if ch.Variations != nil {
		out.Variations = c.variations(f.Type, ch.Variations)
	}
	if err := c.err(); err != nil {
		return f, Changed{}, err
	}
	if out.sameDefinition(f) {
		return f, Changed{}, nil
	}
	out.UpdatedAt, out.Version = now, f.Version+1
	return out, Changed{Changed: true, Ruleset: out.rulesetDiffers(f), RemovedVariations: removed(f.Variations, out.Variations)}, nil
}

func (f Flag) sameDefinition(g Flag) bool {
	return f.Name == g.Name && f.Description == g.Description && f.Owner == g.Owner && slices.Equal(f.Tags, g.Tags) &&
		f.Lifetime == g.Lifetime && sameTime(f.ExpectedRemovalAt, g.ExpectedRemovalAt) &&
		f.AutomaticExposure == g.AutomaticExposure && slices.EqualFunc(f.Variations, g.Variations, func(a, b Variation) bool {
		return a.Key == b.Key && bytes.Equal(a.Value, b.Value) && a.Name == b.Name && a.Description == b.Description
	})
}

// rulesetDiffers reports whether f and g differ in what a ruleset carries
// of a flag's definition.
func (f Flag) rulesetDiffers(g Flag) bool {
	return f.AutomaticExposure != g.AutomaticExposure || (f.ArchivedAt == nil) != (g.ArchivedAt == nil) ||
		!slices.EqualFunc(f.Variations, g.Variations, func(a, b Variation) bool {
			return a.Key == b.Key && bytes.Equal(a.Value, b.Value)
		})
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// removed returns the keys of before that after no longer has.
func removed(before, after []Variation) []string {
	var out []string
	for _, v := range before {
		if !slices.ContainsFunc(after, func(w Variation) bool { return w.Key == v.Key }) {
			out = append(out, v.Key)
		}
	}
	return out
}

// HasVariation reports whether the flag has a variation with key.
func (f Flag) HasVariation(key string) bool {
	return slices.ContainsFunc(f.Variations, func(v Variation) bool { return v.Key == key })
}

// Archive returns the flag archived at now, hidden from SDKs in every
// environment, and whether it changed: archiving an archived flag does not.
func (f Flag) Archive(now time.Time) (Flag, bool) {
	if f.ArchivedAt != nil {
		return f, false
	}
	out := f
	out.ArchivedAt, out.UpdatedAt, out.Version = &now, now, f.Version+1
	return out, true
}

// Restore returns the flag brought back to SDKs at now, and whether it
// changed: restoring a flag that is not archived does not.
func (f Flag) Restore(now time.Time) (Flag, bool) {
	if f.ArchivedAt == nil {
		return f, false
	}
	out := f
	out.ArchivedAt, out.UpdatedAt, out.Version = nil, now, f.Version+1
	return out, true
}

// errNotArchived refuses to delete a flag that is not archived: deleting is
// a deliberate second step, after the flag has left every ruleset.
var errNotArchived = apperrors.Conflict("not_archived", "only an archived flag is deleted; archive it first")

// CanDelete refuses to delete a flag that is not archived.
func (f Flag) CanDelete() error {
	if f.ArchivedAt == nil {
		return errNotArchived
	}
	return nil
}
