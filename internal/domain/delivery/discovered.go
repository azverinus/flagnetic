package delivery

import (
	"slices"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ObservedType is the JSON type an attribute's value was seen with: a
// developer declaring a discovered attribute picks its type from these.
type ObservedType string

// The JSON types an attribute is observed with. A list is any JSON array.
const (
	ObservedString  ObservedType = "string"
	ObservedNumber  ObservedType = "number"
	ObservedBoolean ObservedType = "boolean"
	ObservedList    ObservedType = "list"
	ObservedObject  ObservedType = "object"
)

// ObservedAttribute is an attribute a context carried that the project does
// not declare: its key and the JSON type of its value, never the value,
// which may be personal data.
type ObservedAttribute struct {
	Key  string
	Type ObservedType
}

// ObserveUndeclared returns, ordered by key, the attributes of a context
// that rs does not declare and that could be declared: not a unit type's
// key (an attribute is never declared under one), not null (which carries
// no type), and a key the workspace accepts.
func ObserveUndeclared(rs *eval.Ruleset, attributes map[string]any) []ObservedAttribute {
	// No size hint: a sum of two lengths taken from a ruleset can overflow
	// an int, and the map grows as needed anyway.
	declared := map[string]bool{}
	for _, a := range rs.Attributes {
		declared[a.Key] = true
	}
	for _, u := range rs.UnitTypes {
		declared[u] = true
	}
	var out []ObservedAttribute
	for key, value := range attributes {
		if declared[key] || value == nil || !workspace.DiscoverableAttributeKey(key) {
			continue
		}
		out = append(out, ObservedAttribute{Key: key, Type: observedType(value)})
	}
	slices.SortFunc(out, func(a, b ObservedAttribute) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		}
		return 0
	})
	return out
}

// observedType returns the JSON type of a value as encoding/json decodes it.
func observedType(value any) ObservedType {
	switch value.(type) {
	case string:
		return ObservedString
	case bool:
		return ObservedBoolean
	case []any:
		return ObservedList
	case map[string]any:
		return ObservedObject
	}
	return ObservedNumber
}

// MaxDiscoveredAttributes is how many discovered attributes a project
// keeps, dismissed ones included, so that contexts carrying ever new keys
// cannot grow the table without bound. A new key beyond it is not kept.
const MaxDiscoveredAttributes = 500

// Sighting is what a replica saw of one undeclared attribute of a project
// between two writes: the types in the order first seen, how many contexts
// carried it, and when it was first and last seen.
type Sighting struct {
	Key         string
	Types       []ObservedType
	Count       int64
	First, Last time.Time
}

// DiscoveredAttribute is an attribute contexts carried that the project
// does not declare.
type DiscoveredAttribute struct {
	Project   workspace.ProjectID
	Key       string
	Types     []ObservedType
	FirstSeen time.Time
	LastSeen  time.Time
	Count     int64
	// Dismissed hides the attribute until it is seen with a type not seen
	// before, which may be a change worth a look.
	Dismissed bool
}

// NewDiscoveredAttribute returns the attribute of project first seen in s.
func NewDiscoveredAttribute(project workspace.ProjectID, s Sighting) DiscoveredAttribute {
	return DiscoveredAttribute{
		Project: project, Key: s.Key, Types: slices.Clone(s.Types), FirstSeen: s.First, LastSeen: s.Last, Count: s.Count,
	}
}

// Seen returns d with s added: its count, its new types after the known
// ones, and the span it was seen over widened. A dismissed attribute seen
// with a type not seen before is listed again.
func (d DiscoveredAttribute) Seen(s Sighting) DiscoveredAttribute {
	d.Types = slices.Clone(d.Types)
	for _, t := range s.Types {
		if !slices.Contains(d.Types, t) {
			d.Types = append(d.Types, t)
			d.Dismissed = false
		}
	}
	d.Count += s.Count
	if s.First.Before(d.FirstSeen) {
		d.FirstSeen = s.First
	}
	if s.Last.After(d.LastSeen) {
		d.LastSeen = s.Last
	}
	return d
}

// ErrDiscoveredAttributeNotFound answers an attribute that is not listed as
// discovered: never seen, dismissed, or declared since.
var ErrDiscoveredAttributeNotFound = apperrors.NotFound("not_found", "the project lists no discovered attribute with this key")

// Dismiss hides d; an attribute dismissed already is not listed, so not
// found.
func (d DiscoveredAttribute) Dismiss() (DiscoveredAttribute, error) {
	if d.Dismissed {
		return d, ErrDiscoveredAttributeNotFound
	}
	d.Dismissed = true
	return d, nil
}
