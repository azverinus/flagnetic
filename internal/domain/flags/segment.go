package flags

import (
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SegmentID identifies a segment.
type SegmentID = id.Of[Segment]

// segmentKeyPattern is the key every entity of a project but a flag has:
// short, lower case and free of anything a URL or a rule would escape.
var segmentKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// maxConditions bounds the conditions of a segment or a rule, which every
// evaluation of every flag naming it checks.
const maxConditions = 20

// Segment is a reusable, named set of attribute conditions, all of which a
// context meets to be in it.
type Segment struct {
	ID      SegmentID
	Project workspace.ProjectID
	// Key names the segment in rules and never changes.
	Key         string
	Name        string
	Description string
	// Private keeps the segment's conditions on the server: only
	// server-side evaluation may use it.
	Private    bool
	Conditions []Condition
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// Version counts the segment's accepted changes, from 1.
	Version int64
}

// SegmentInput is what a segment is created with, and the declarations of
// its project its conditions are checked against.
type SegmentInput struct {
	ID                SegmentID
	Project           workspace.ProjectID
	Key               string
	Name, Description string
	Private           bool
	Conditions        []Condition
	Declarations      Declarations
	Now               time.Time
}

// NewSegment returns the segment in, at version 1, or invalid_segment with
// every field to fix.
func NewSegment(in SegmentInput) (Segment, error) {
	c := newCheck("invalid_segment", "the segment is not valid")
	if !segmentKeyPattern.MatchString(in.Key) {
		c.add("/key", "invalid_key", "a key starts with a lower-case letter and has at most 64 lower-case letters, digits, hyphens and underscores",
			map[string]any{"pattern": segmentKeyPattern.String()})
	}
	s := Segment{
		ID: in.ID, Project: in.Project, Key: in.Key, Name: c.name("/name", in.Name),
		Description: c.description("/description", in.Description), Private: in.Private,
		Conditions: c.segmentConditions(in.Conditions, in.Declarations), CreatedAt: in.Now, UpdatedAt: in.Now, Version: 1,
	}
	if err := c.err(); err != nil {
		return Segment{}, err
	}
	return s, nil
}

// segmentConditions checks a segment's conditions and returns them, never
// nil.
func (c *check) segmentConditions(conditions []Condition, d Declarations) []Condition {
	if len(conditions) > maxConditions {
		c.add("/conditions", "too_many", "a segment has at most 20 conditions", map[string]any{"max": maxConditions})
		return conditions
	}
	for i, cond := range conditions {
		c.segmentCondition("/conditions/"+strconv.Itoa(i), cond, d)
	}
	if conditions == nil {
		return []Condition{}
	}
	return conditions
}

// SegmentChange is a change to a segment; a nil field is left as it is.
type SegmentChange struct {
	// Key is refused whatever its value: rules name the segment by it.
	Key         *string
	Name        *string
	Description *string
	Private     *bool
	Conditions  *[]Condition
}

// Change returns the segment with ch applied at now, its conditions checked
// against d, its version incremented when anything changed, and what
// changed: a change to its conditions or to private changes what every
// ruleset carries of it.
func (s Segment) Change(ch SegmentChange, d Declarations, now time.Time) (Segment, Changed, error) {
	c := newCheck("invalid_segment", "the segment is not valid")
	if ch.Key != nil {
		c.immutable("/key", "a segment's key")
	}
	out := s
	if ch.Name != nil {
		out.Name = c.name("/name", *ch.Name)
	}
	if ch.Description != nil {
		out.Description = c.description("/description", *ch.Description)
	}
	if ch.Private != nil {
		out.Private = *ch.Private
	}
	if ch.Conditions != nil {
		out.Conditions = c.segmentConditions(*ch.Conditions, d)
	}
	if err := c.err(); err != nil {
		return s, Changed{}, err
	}
	ruleset := out.Private != s.Private || !reflect.DeepEqual(out.Conditions, s.Conditions)
	if !ruleset && out.Name == s.Name && out.Description == s.Description {
		return s, Changed{}, nil
	}
	out.UpdatedAt, out.Version = now, s.Version+1
	return out, Changed{Changed: true, Ruleset: ruleset}, nil
}
