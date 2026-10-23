// Package experiments is the domain of experiments: a time-boxed comparison
// of variations of one flag on a declared unit type, with a hypothesis, an
// owner, weights, a traffic allocation and two public salts. Every rule
// spanning the experiment and what it names (its flag, a declared unit
// type, the salts of the project) is a pure function over what the handler
// loaded under its project's row lock, so it holds for as long as that lock.
package experiments

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ExperimentID identifies an experiment.
type ExperimentID = id.Of[Experiment]

// Status is where an experiment is in its life.
type Status string

// The statuses, in the order they are reached. There is no pause (the
// flag's kill switch is the pause) and no restart (a clone with new salts is
// the rerun).
const (
	StatusDraft    Status = "draft"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusArchived Status = "archived"
)

// TotalWeight is what an experiment's weights sum to, and the most a traffic
// allocation can be: basis points, steps of 0.01 %.
const TotalWeight = 10000

// Bounds of an experiment.
const (
	minVariations        = 2
	maxVariations        = 50
	maxNameLength        = 200
	maxHypothesisLength  = 2000
	maxTags              = 50
	maxTagLength         = 64
	defaultAllocation    = TotalWeight
	invalidExperimentErr = "invalid_experiment"
)

// keyPattern is a flag key's: an experiment is named the way code names it.
var keyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$`)

// Weight is the share of the allocated traffic one variation of the
// experiment's flag gets, in basis points. The order of an experiment's
// weights fixes the cumulative bucket ranges.
type Weight struct {
	Variation string
	Weight    int
}

// Owner is the member a write names as an experiment's owner, and whether
// they may own one: an active member holding a grant in the project, or an
// instance admin, as for a flag. The handler finds that out; the experiment
// only refuses one who may not.
type Owner struct {
	Member   access.MemberID
	Eligible bool
}

// Experiment is an experiment's definition.
type Experiment struct {
	ID      ExperimentID
	Project workspace.ProjectID
	// Key names the experiment in rules and exposures and never changes;
	// unique, case sensitively, in its project.
	Key        string
	Name       string
	Hypothesis string
	Owner      access.MemberID
	Tags       []string
	// Flag is the flag the experiment compares variations of, and FlagKey
	// its key as it was when the experiment was written.
	Flag    flags.FlagID
	FlagKey string
	// UnitType is the declared unit type whose units are split.
	UnitType string
	// Variations are the weights, one per variation compared.
	Variations        []Weight
	TrafficAllocation int
	// InclusionSalt decides whether a unit is in the experiment at all and
	// VariationSalt which variation an included unit gets; each is used by
	// no other experiment or rollout of the project.
	InclusionSalt string
	VariationSalt string
	Status        Status
	// ExpectedEndAt is a calendar day, in UTC at midnight; nil when none.
	ExpectedEndAt *time.Time
	StartedAt     *time.Time
	StoppedAt     *time.Time
	ArchivedAt    *time.Time
	// CreatedBy is the member who created the experiment; nil for a
	// project token.
	CreatedBy *access.MemberID
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts the experiment's accepted changes, from 1.
	Version int64
}

// FlagFacts are what an experiment is judged against of the flag it names.
type FlagFacts struct {
	ID       flags.FlagID
	Key      string
	Archived bool
	// Variations are the keys of the flag's variations.
	Variations []string
}

// Input is what an experiment is created with, with the facts about the
// project it is judged against.
type Input struct {
	ID         ExperimentID
	Project    workspace.ProjectID
	Key        string
	Name       string
	Hypothesis string
	Owner      Owner
	Tags       []string
	// Flag is the flag named, nil when the project has none with the key
	// the request gave; FlagKey is that key.
	Flag     *FlagFacts
	FlagKey  string
	UnitType string
	// DeclaredUnitTypes are the keys of the project's unit types.
	DeclaredUnitTypes []string
	Variations        []Weight
	// TrafficAllocation is every unit's when nil.
	TrafficAllocation *int
	ExpectedEndAt     *time.Time
	// Salts are the two salts the experiment is given, from NewSalts.
	Salts     Salts
	CreatedBy *access.MemberID
	Now       time.Time
}

// New returns the draft in, at version 1, or invalid_experiment with every
// field to fix.
func New(in Input) (Experiment, error) {
	c := newCheck()
	if !keyPattern.MatchString(in.Key) {
		c.add("/key", "invalid_key",
			"an experiment key starts with a letter and has at most 128 letters, digits, dots, hyphens and underscores",
			map[string]any{"pattern": keyPattern.String()})
	}
	e := Experiment{
		ID: in.ID, Project: in.Project, Key: in.Key,
		Name: c.name(in.Name), Hypothesis: c.hypothesis(in.Hypothesis), Owner: c.owner(in.Owner), Tags: c.tags(in.Tags),
		FlagKey: in.FlagKey, UnitType: in.UnitType, TrafficAllocation: defaultAllocation,
		InclusionSalt: in.Salts.Inclusion, VariationSalt: in.Salts.Variation, Status: StatusDraft,
		CreatedBy: in.CreatedBy, CreatedAt: in.Now, UpdatedAt: in.Now, Version: 1,
	}
	if in.TrafficAllocation != nil {
		e.TrafficAllocation = c.trafficAllocation(*in.TrafficAllocation)
	}
	if in.ExpectedEndAt != nil {
		d := day(*in.ExpectedEndAt)
		e.ExpectedEndAt = &d
	}
	if !slices.Contains(in.DeclaredUnitTypes, in.UnitType) {
		c.add("/unit_type", "undeclared_unit_type", "the project has not declared this unit type", nil)
	}
	var flagVariations []string
	flagUsable := false
	switch {
	case in.Flag == nil:
		c.add("/flag", "unknown_flag", "the project has no flag with this key", nil)
	case in.Flag.Archived:
		c.add("/flag", "flag_archived", "an archived flag cannot be compared", nil)
	default:
		e.Flag, e.FlagKey = in.Flag.ID, in.Flag.Key
		flagVariations, flagUsable = in.Flag.Variations, true
	}
	e.Variations = c.weights(in.Variations, flagUsable, flagVariations)
	if err := c.err(); err != nil {
		return Experiment{}, err
	}
	return e, nil
}

// day returns the calendar day of t in UTC, at midnight.
func day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// check gathers every broken rule of one write, so a form shows every field
// to fix in one round.
type check struct{ violations []apperrors.Violation }

func newCheck() *check { return &check{} }

func (c *check) add(pointer, code, detail string, params map[string]any) {
	c.violations = append(c.violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
}

func (c *check) err() error {
	if len(c.violations) == 0 {
		return nil
	}
	return apperrors.Invalid(invalidExperimentErr, "the experiment is not valid", c.violations...)
}

func (c *check) name(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		c.add("/name", "invalid_name", "a name has 1 to 200 characters and is not blank",
			map[string]any{"min": 1, "max": maxNameLength})
	}
	return name
}

func (c *check) hypothesis(h string) string {
	h = strings.TrimSpace(h)
	if utf8.RuneCountInString(h) > maxHypothesisLength {
		c.add("/hypothesis", "too_long", "a hypothesis has at most 2,000 characters", map[string]any{"max": maxHypothesisLength})
	}
	return h
}

func (c *check) owner(o Owner) access.MemberID {
	if !o.Eligible {
		c.add("/owner", "invalid_owner",
			"an experiment's owner is an active member holding a grant in the project, or an instance admin", nil)
	}
	return o.Member
}

// tags returns tags trimmed, never nil.
func (c *check) tags(tags []string) []string {
	if len(tags) > maxTags {
		c.add("/tags", "too_many", "an experiment has at most 50 tags", map[string]any{"max": maxTags})
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
			c.add(pointer, "duplicate", "the experiment has this tag already", nil)
		}
		out = append(out, t)
	}
	return out
}

func (c *check) trafficAllocation(a int) int {
	if a < 0 || a > TotalWeight {
		c.add("/traffic_allocation", "out_of_range", "a traffic allocation is 0 to 10,000 basis points",
			map[string]any{"min": 0, "max": TotalWeight})
	}
	return a
}

// weights returns ws when they are 2 to 50 distinct variations of the flag,
// each weighing 0 to TotalWeight basis points, together exactly TotalWeight.
// flagUsable says whether the flag was found and not archived; without it the
// variations cannot be judged against it, and only their shape is.
func (c *check) weights(ws []Weight, flagUsable bool, flagVariations []string) []Weight {
	if len(ws) < minVariations || len(ws) > maxVariations {
		c.add("/variations", "invalid_count", "an experiment compares 2 to 50 variations",
			map[string]any{"min": minVariations, "max": maxVariations})
		return ws
	}
	sum, weightsValid := 0, true
	seen := make(map[string]bool, len(ws))
	for i, w := range ws {
		pointer := "/variations/" + strconv.Itoa(i)
		switch {
		case seen[w.Variation]:
			c.add(pointer+"/variation", "duplicate", "the experiment lists this variation already", nil)
		case flagUsable && !slices.Contains(flagVariations, w.Variation):
			c.add(pointer+"/variation", "unknown_variation", "the flag has no variation with this key", nil)
		}
		seen[w.Variation] = true
		// A weight above the total can never be part of a valid split, and
		// refusing it here also keeps the sum from overflowing: weights near
		// the largest int would otherwise wrap around to exactly the total.
		if w.Weight < 0 || w.Weight > TotalWeight {
			c.add(pointer+"/weight", "invalid_weight", "a weight is 0 to 10,000 basis points",
				map[string]any{"min": 0, "max": TotalWeight})
			weightsValid = false
			continue
		}
		sum += w.Weight
	}
	// The sum of weights some of which are out of range says nothing more
	// than the weights' own violations do.
	if weightsValid && sum != TotalWeight {
		c.add("/variations", "invalid_weights_sum", "the weights sum to exactly 10,000 basis points",
			map[string]any{"total": TotalWeight, "sum": sum})
	}
	return ws
}
