package transfer

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Mode is how an import applies a document to the platform.
type Mode string

// The modes.
const (
	// ModeCreate makes a new project of exactly the document.
	ModeCreate Mode = "create"
)

// Source is what an import read its changes from.
const (
	SourceDocument = "document"
)

// The refusal of a document that breaks a rule, whatever the rule, with a
// violation for each fault found.
const (
	invalidDocument       = "invalid_document"
	invalidDocumentDetail = "the project document is not valid"
)

// Warning says something an import did differently from what the document
// asks, or could not do, without refusing it.
type Warning struct {
	// Code is the stable name of the warning, such as owner_replaced.
	Code string
	// Pointer is where in the document it applies, "" for the document as a
	// whole.
	Pointer string
	Detail  string
	// Params are the values a client explains the warning with.
	Params map[string]any
}

// WarningOwnerReplaced is the warning of an owner the document names who
// may not own in the project.
const WarningOwnerReplaced = "owner_replaced"

// PatchOperation is one operation of an RFC 6902 JSON Patch.
type PatchOperation struct {
	Op    string
	Path  string
	From  string
	Value any
}

// Update is an entity an import changed, with the patch that turns the
// entity as it was into the entity as it is, over the entity's entry of the
// document.
type Update struct {
	Key   string
	Patch []PatchOperation
}

// KindChanges are what an import did to the entities of one kind, by key.
type KindChanges struct {
	Created []string
	Updated []Update
	Deleted []string
}

// Changes are what an import did, by kind of entity.
type Changes struct {
	Environments KindChanges
	UnitTypes    KindChanges
	Attributes   KindChanges
	Segments     KindChanges
	Flags        KindChanges
	Experiments  KindChanges
}

// Counts returns how many entities of each kind the import created, updated
// and deleted, as the summary entry of the audit log records them.
func (c Changes) Counts() map[string]any {
	count := func(k KindChanges) map[string]any {
		return map[string]any{"created": len(k.Created), "updated": len(k.Updated), "deleted": len(k.Deleted)}
	}
	return map[string]any{
		"environments": count(c.Environments), "unit_types": count(c.UnitTypes), "attributes": count(c.Attributes),
		"segments": count(c.Segments), "flags": count(c.Flags), "experiments": count(c.Experiments),
	}
}

// saltPattern is the form of a rollout's salt, whoever drew it.
var saltPattern = regexp.MustCompile(`^[a-z0-9]{16}$`)

// Pointer returns the JSON Pointer to the member or element parts name: a
// name or the index of an element.
func Pointer(parts ...any) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		switch p := p.(type) {
		case int:
			b.WriteString(strconv.Itoa(p))
		case string:
			b.WriteString(escapePointer(p))
		default:
			panic(fmt.Sprintf("transfer: a pointer part is a name or an index, not a %T", p))
		}
	}
	return b.String()
}

// faults collects the violations of a document.
type faults []apperrors.Violation

func (f *faults) add(pointer, code, detail string, params map[string]any) {
	*f = append(*f, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
}

// CheckCreate returns the refusal of d as the document to make a new
// project of, nil when it holds what a document alone can tell. Every
// fault is reported at once, with a pointer into the document, in the
// order of the document.
//
// It judges what no context judges alone: which sections a create needs,
// keys repeated within a kind, a unit type and an attribute sharing a key,
// a standard attribute declared as another type, a configuration for an
// environment the document does not have, and the form of the rule
// identifiers and salts. The rules each context holds of its own entities
// are judged where they are written.
func CheckCreate(d Document) error {
	var f faults
	f.createSections(d)
	f.uniqueKeys(d)
	f.declarations(d)
	f.configurations(d)
	f.salts(d)
	if len(f) == 0 {
		return nil
	}
	return apperrors.Invalid(invalidDocument, invalidDocumentDetail, f...)
}

// createSections requires every section, and an environment.
func (f *faults) createSections(d Document) {
	required := func(name string, present bool) {
		if !present {
			f.add(Pointer(name), "required", "a document that creates a project holds this section", nil)
		}
	}
	required("project", d.Project != nil)
	required("environments", d.Environments != nil)
	required("unit_types", d.UnitTypes != nil)
	required("attributes", d.Attributes != nil)
	required("segments", d.Segments != nil)
	required("flags", d.Flags != nil)
	required("experiments", d.Experiments != nil)
	if d.Environments != nil && len(d.Environments) == 0 {
		f.add(Pointer("environments"), "invalid_value", "a project has at least one environment", map[string]any{"min": 1})
	}
	// Experiments are imported by a later change: refusing the document
	// whole is better than creating a project that silently lacks them.
	for i := range d.Experiments {
		f.add(Pointer("experiments", i), "unsupported", "a document that creates a project cannot hold experiments yet", nil)
	}
}

// uniqueKeys refuses a key that two entities of one kind share.
func (f *faults) uniqueKeys(d Document) {
	unique := func(section string, keys []string) {
		seen := map[string]bool{}
		for i, k := range keys {
			if seen[k] {
				f.add(Pointer(section, i, "key"), "duplicate", "the document has an entry with this key already", nil)
			}
			seen[k] = true
		}
	}
	unique("environments", mapKeys(d.Environments, func(e Environment) string { return e.Key }))
	unique("unit_types", mapKeys(d.UnitTypes, func(u UnitType) string { return u.Key }))
	unique("attributes", mapKeys(d.Attributes, func(a Attribute) string { return a.Key }))
	unique("segments", mapKeys(d.Segments, func(s Segment) string { return s.Key }))
	unique("flags", mapKeys(d.Flags, func(fl Flag) string { return fl.Key }))
}

func mapKeys[T any](in []T, key func(T) string) []string {
	out := make([]string, len(in))
	for i, e := range in {
		out[i] = key(e)
	}
	return out
}

// declarations refuses an attribute whose key a unit type has, and a unit
// type keyed as a standard attribute every project has, since a rule names
// either by its key alone; and a standard attribute declared as another
// type than SDKs send it as.
func (f *faults) declarations(d Document) {
	units := map[string]bool{}
	for i, u := range d.UnitTypes {
		units[u.Key] = true
		if _, standard := workspace.StandardAttributeType(u.Key); standard {
			f.add(Pointer("unit_types", i, "key"), "duplicate", "a standard attribute every project has has this key; a key means one thing", nil)
		}
	}
	for i, a := range d.Attributes {
		if units[a.Key] {
			f.add(Pointer("attributes", i, "key"), "duplicate", "a unit type of the document has this key; a key means one thing", nil)
		}
		if typ, standard := workspace.StandardAttributeType(a.Key); standard && string(typ) != a.Type {
			f.add(Pointer("attributes", i, "type"), "type_immutable",
				"the attribute is standard, and its type never changes", map[string]any{"type": string(typ)})
		}
	}
}

// configurations refuses a configuration for an environment the document
// does not have.
func (f *faults) configurations(d Document) {
	environments := map[string]bool{}
	for _, e := range d.Environments {
		environments[e.Key] = true
	}
	for i, fl := range d.Flags {
		for _, env := range sortedConfigurationKeys(fl.Configurations) {
			if !environments[env] {
				f.add(Pointer("flags", i, "configurations", env), "unknown_environment",
					"the document has no environment "+strconv.Quote(env), map[string]any{"environment": env})
			}
		}
	}
}

func sortedConfigurationKeys(m map[string]Configuration) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// salts refuses a rule identifier that is not a UUID, a salt that is not 16
// lower-case letters and digits, and a salt shared by rollouts that are not
// the same rule of one flag in several environments, which is what copying
// a configuration makes.
func (f *faults) salts(d Document) {
	type owner struct{ flag, rule string }
	owners := map[string]owner{}
	for i, fl := range d.Flags {
		for _, env := range sortedConfigurationKeys(fl.Configurations) {
			for j, r := range fl.Configurations[env].Rules {
				at := Pointer("flags", i, "configurations", env, "rules", j)
				if r.ID != "" {
					if _, err := id.Parse[flags.Rule](r.ID); err != nil {
						f.add(at+"/id", "invalid_value", "a rule identifier is a UUID", nil)
					}
				}
				if r.Serve.Kind != flags.ServeRollout || r.Serve.Salt == "" {
					continue
				}
				if !saltPattern.MatchString(r.Serve.Salt) {
					f.add(at+"/serve/salt", "invalid_value", "a salt is 16 lower-case letters and digits", nil)
					continue
				}
				// A rule without an identifier is a rule of its own; one with
				// an identifier is the same rule whatever case its hexadecimal
				// digits are written in.
				who := owner{flag: fl.Key, rule: at}
				if ruleID, err := id.Parse[flags.Rule](r.ID); err == nil {
					who.rule = ruleID.String()
				}
				first, taken := owners[r.Serve.Salt]
				if !taken {
					// The first rollout with a salt owns it; a later one that
					// is refused does not take it over, or the same rule
					// further on would be refused too.
					owners[r.Serve.Salt] = who
					continue
				}
				if first != who {
					f.add(at+"/serve/salt", "duplicate", "another rollout has this salt; only the same rule of a flag in another environment may share it", nil)
				}
			}
		}
	}
}
