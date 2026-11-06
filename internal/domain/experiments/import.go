package experiments

import (
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Imported is an experiment as a project document gives it, with the facts
// about the project it is judged against. A document carries what is needed
// to make an experiment a copy of another, so that a unit keeps its
// assignment: its status, both salts, its timestamps and its decision.
type Imported struct {
	ID         ExperimentID
	Project    workspace.ProjectID
	Key        string
	Name       string
	Hypothesis string
	Owner      Owner
	Tags       []string
	// Flag is the flag FlagKey names, nil when the project has none.
	Flag    *FlagFacts
	FlagKey string
	// UnitType is the unit type the document names, and DeclaredUnitTypes the
	// keys of the project's.
	UnitType          string
	DeclaredUnitTypes []string
	Variations        []Weight
	TrafficAllocation int
	Salts             Salts
	Status            Status
	ExpectedEndAt     *time.Time
	StartedAt         *time.Time
	StoppedAt         *time.Time
	ArchivedAt        *time.Time
	Decision          *Decision
	CreatedBy         *access.MemberID
	Now               time.Time
}

// Import returns the experiment the document describes, exactly as it
// describes it, at version 1 created at in.Now, or invalid_experiment with
// every field to fix. It is judged as a create is, and by the history its
// status leaves behind, which is what keeps a copy honest: a draft has none;
// a running experiment started, with the day it should end; a stopped one
// started, stopped and decided; an archived one the history of the draft or
// of the stopped experiment it was archived from. A draft and a running
// experiment need their flag not archived and a declared unit type, a stopped
// one a declared unit type, and an archived one neither, since what an
// experiment measured outlives them; every one needs a flag, because it
// compares its variations.
func Import(in Imported) (Experiment, error) {
	c := newCheck()
	if !keyPattern.MatchString(in.Key) {
		c.add("/key", "invalid_key",
			"an experiment key starts with a letter and has at most 128 letters, digits, dots, hyphens and underscores",
			map[string]any{"pattern": keyPattern.String()})
	}
	switch in.Status {
	case StatusDraft, StatusRunning, StatusStopped, StatusArchived:
	default:
		c.add("/status", "invalid_value", "a status is draft, running, stopped or archived", nil)
	}
	e := Experiment{
		ID: in.ID, Project: in.Project, Key: in.Key,
		Name: c.name(in.Name), Hypothesis: c.hypothesis(in.Hypothesis), Owner: c.owner(in.Owner), Tags: c.tags(in.Tags),
		FlagKey: in.FlagKey, UnitType: in.UnitType, TrafficAllocation: c.trafficAllocation(in.TrafficAllocation),
		InclusionSalt: in.Salts.Inclusion, VariationSalt: in.Salts.Variation, Status: in.Status,
		StartedAt: microseconds(in.StartedAt), StoppedAt: microseconds(in.StoppedAt), ArchivedAt: microseconds(in.ArchivedAt),
		CreatedBy: in.CreatedBy, CreatedAt: in.Now, UpdatedAt: in.Now, Version: 1,
	}
	if in.ExpectedEndAt != nil {
		d := day(*in.ExpectedEndAt)
		e.ExpectedEndAt = &d
	}
	c.salts(in.Salts)
	started := in.Status == StatusRunning || in.Status == StatusStopped || (in.Status == StatusArchived && in.StartedAt != nil)
	if started && e.Hypothesis == "" {
		c.add("/hypothesis", "required", "an experiment past its draft says what it expects to find", nil)
	}
	if (in.Status == StatusDraft || in.Status == StatusRunning || in.Status == StatusStopped) && !slices.Contains(in.DeclaredUnitTypes, in.UnitType) {
		c.add("/unit_type", "undeclared_unit_type", "the project has not declared this unit type", nil)
	}
	var flagVariations []string
	flagFound := false
	switch {
	case in.Flag == nil:
		c.add("/flag", "unknown_flag", "the project has no flag with this key", nil)
	case in.Flag.Archived && (in.Status == StatusDraft || in.Status == StatusRunning):
		c.add("/flag", "flag_archived", "an archived flag cannot be compared", nil)
		fallthrough
	default:
		e.Flag, e.FlagKey = in.Flag.ID, in.Flag.Key
		flagVariations, flagFound = in.Flag.Variations, true
	}
	e.Variations = c.weights(in.Variations, flagFound, flagVariations)
	e.Decision = c.history(in, e.Variations, started)
	if err := c.err(); err != nil {
		return Experiment{}, err
	}
	return e, nil
}

// salts refuses salts that are not 16 lower-case hexadecimal characters, and
// two that are the same.
func (c *check) salts(s Salts) {
	if !ValidSalt(s.Inclusion) {
		c.add("/inclusion_salt", "invalid_value", "a salt is 16 lower-case hexadecimal characters", nil)
	}
	switch {
	case !ValidSalt(s.Variation):
		c.add("/variation_salt", "invalid_value", "a salt is 16 lower-case hexadecimal characters", nil)
	case s.Variation == s.Inclusion:
		c.add("/variation_salt", "duplicate", "the two salts of an experiment differ", nil)
	}
}

// history refuses the timestamps and the decision the status of e cannot
// have, and the ones it lacks: what an experiment has been through is
// recorded in them, and a copy that invented a history would mislead whoever
// reads the results.
func (c *check) history(in Imported, variations []Weight, started bool) *Decision {
	forbidden := func(pointer string, present bool, why string) {
		if present {
			c.add(pointer, "invalid_value", why, nil)
		}
	}
	required := func(pointer string, present bool, why string) {
		if !present {
			c.add(pointer, "required", why, nil)
		}
	}
	ran := in.StartedAt != nil
	switch in.Status {
	case StatusDraft:
		forbidden("/started_at", ran, "a draft has not started")
		forbidden("/stopped_at", in.StoppedAt != nil, "a draft has not stopped")
		forbidden("/archived_at", in.ArchivedAt != nil, "a draft is not archived")
		forbidden("/decision", in.Decision != nil, "a draft has no decision")
	case StatusRunning:
		required("/started_at", ran, "a running experiment started")
		forbidden("/stopped_at", in.StoppedAt != nil, "a running experiment has not stopped")
		forbidden("/archived_at", in.ArchivedAt != nil, "a running experiment is not archived")
		forbidden("/decision", in.Decision != nil, "a running experiment has no decision")
	case StatusStopped:
		required("/started_at", ran, "a stopped experiment started")
		required("/stopped_at", in.StoppedAt != nil, "a stopped experiment stopped")
		forbidden("/archived_at", in.ArchivedAt != nil, "a stopped experiment is not archived")
		required("/decision", in.Decision != nil, "a stopped experiment has a decision")
	case StatusArchived:
		required("/archived_at", in.ArchivedAt != nil, "an archived experiment was archived")
		if ran {
			required("/stopped_at", in.StoppedAt != nil, "only a stopped experiment is archived after it started")
			required("/decision", in.Decision != nil, "only a stopped experiment is archived after it started")
		} else {
			forbidden("/stopped_at", in.StoppedAt != nil, "an experiment that never started never stopped")
			forbidden("/decision", in.Decision != nil, "an experiment that never started has no decision")
		}
	}
	if started {
		required("/expected_end_at", in.ExpectedEndAt != nil, "a started experiment says when it is expected to end")
	}
	var decision *Decision
	if in.Decision != nil && (in.Status == StatusStopped || (in.Status == StatusArchived && ran)) {
		d := c.decision("/decision", *in.Decision, variations)
		decision = &d
	}
	before := func(pointer string, t, then *time.Time, why string) {
		if t != nil && then != nil && t.Before(*then) {
			c.add(pointer, "invalid_value", why, nil)
		}
	}
	before("/stopped_at", in.StoppedAt, in.StartedAt, "an experiment stops after it starts")
	before("/archived_at", in.ArchivedAt, in.StoppedAt, "an experiment is archived after it stops")
	before("/archived_at", in.ArchivedAt, in.StartedAt, "an experiment is archived after it starts")
	return decision
}

// microseconds returns t in UTC to the microsecond, as the database keeps it,
// so that what an import writes is what a later export reads.
func microseconds(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Microsecond)
	return &u
}

// Desired is what a project document says an experiment that exists already
// should be.
type Desired struct {
	Name       string
	Hypothesis string
	// Owner is the member to own the experiment, nil to leave the owner.
	Owner *Owner
	Tags  []string
	// Flag is the flag FlagKey names, nil when the project has none.
	Flag              *FlagFacts
	FlagKey           string
	UnitType          string
	DeclaredUnitTypes []string
	Variations        []Weight
	TrafficAllocation int
	// Salts are the salts the document names, "" for one it leaves out,
	// which stays as it is.
	Salts         Salts
	Status        Status
	ExpectedEndAt *time.Time
	StartedAt     *time.Time
	StoppedAt     *time.Time
	ArchivedAt    *time.Time
	Decision      *Decision
}

// Reconcile returns the experiment e made what d says it is at now, its
// version incremented, and whether anything changed: a document that says
// what the experiment is changes nothing. It changes an experiment only as
// an ordinary edit of its status allows, and never moves it through its
// status: a draft takes its whole definition and its salts; a running
// experiment its name, hypothesis, owner, tags and end date, and a larger
// traffic allocation (traffic_allocation_decrease for a smaller one); a
// stopped one its name, hypothesis, owner and tags; an archived one nothing.
// What the status freezes, and its status, timestamps and decision whatever
// it is, the document may name only as they are: any other value is frozen,
// naming every such field at once. The rest is judged as an edit judges it.
func (e Experiment) Reconcile(d Desired, now time.Time) (Experiment, bool, error) {
	if err := e.frozenBy(d); err != nil {
		return e, false, err
	}
	if e.Status == StatusArchived {
		// Everything is frozen, so a document that gets here repeats it.
		return e, false, nil
	}
	ch := Change{DeclaredUnitTypes: d.DeclaredUnitTypes}
	if strings.TrimSpace(d.Name) != e.Name {
		ch.Name = &d.Name
	}
	if strings.TrimSpace(d.Hypothesis) != e.Hypothesis {
		ch.Hypothesis = &d.Hypothesis
	}
	if d.Owner != nil && d.Owner.Member != e.Owner {
		ch.Owner = d.Owner
	}
	if !slices.Equal(d.Tags, e.Tags) {
		ch.Tags = &d.Tags
	}
	if d.FlagKey != e.FlagKey {
		ch.FlagKey = &d.FlagKey
	}
	if d.UnitType != e.UnitType {
		ch.UnitType = &d.UnitType
	}
	if !slices.Equal(d.Variations, e.Variations) {
		ch.Variations = &d.Variations
	}
	if ch.FlagKey != nil || ch.Variations != nil {
		ch.Flag = d.Flag
	}
	if d.TrafficAllocation != e.TrafficAllocation {
		ch.TrafficAllocation = &d.TrafficAllocation
	}
	clearEnd := false
	if d.ExpectedEndAt != nil && !sameOptionalDay(d.ExpectedEndAt, e.ExpectedEndAt) {
		ch.ExpectedEndAt = d.ExpectedEndAt
	}
	if d.ExpectedEndAt == nil && e.ExpectedEndAt != nil {
		if e.StartedAt != nil {
			c := newCheck()
			c.add("/expected_end_at", "required", "a started experiment says when it is expected to end", nil)
			return e, false, c.err()
		}
		clearEnd = true
	}
	salts, err := e.saltsOf(d.Salts)
	if err != nil {
		return e, false, err
	}
	out, changed, err := e.Change(ch, now)
	if err != nil {
		return e, false, err
	}
	saltsChanged := salts != Salts{Inclusion: e.InclusionSalt, Variation: e.VariationSalt}
	if !changed && (saltsChanged || clearEnd) {
		out = e
		out.UpdatedAt, out.Version = now, e.Version+1
		changed = true
	}
	if changed {
		out.InclusionSalt, out.VariationSalt = salts.Inclusion, salts.Variation
		if clearEnd {
			out.ExpectedEndAt = nil
		}
	}
	return out, changed, nil
}

// saltsOf returns the salts e has once s, those a document names, are
// taken: one left out stays. They are judged as a draft's are.
func (e Experiment) saltsOf(s Salts) (Salts, error) {
	out := Salts{Inclusion: e.InclusionSalt, Variation: e.VariationSalt}
	if s.Inclusion != "" {
		out.Inclusion = s.Inclusion
	}
	if s.Variation != "" {
		out.Variation = s.Variation
	}
	if out == (Salts{Inclusion: e.InclusionSalt, Variation: e.VariationSalt}) {
		return out, nil
	}
	c := newCheck()
	c.salts(out)
	return out, c.err()
}

// frozenBy returns the frozen refusal for every field d names with a value
// that e's status does not let an import change, nil when there is none.
func (e Experiment) frozenBy(d Desired) error {
	editable := func(statuses ...Status) bool { return slices.Contains(statuses, e.Status) }
	draft, running, stopped := StatusDraft, StatusRunning, StatusStopped
	fields := []struct {
		pointer string
		differs bool
		// allowed is whether e's status lets an import change the field.
		allowed bool
	}{
		{"/name", strings.TrimSpace(d.Name) != e.Name, editable(draft, running, stopped)},
		{"/hypothesis", strings.TrimSpace(d.Hypothesis) != e.Hypothesis, editable(draft, running, stopped)},
		{"/owner", d.Owner != nil && d.Owner.Member != e.Owner, editable(draft, running, stopped)},
		{"/tags", !slices.Equal(d.Tags, e.Tags), editable(draft, running, stopped)},
		{"/flag", d.FlagKey != e.FlagKey, editable(draft)},
		{"/unit_type", d.UnitType != e.UnitType, editable(draft)},
		{"/variations", !slices.Equal(d.Variations, e.Variations), editable(draft)},
		{"/traffic_allocation", d.TrafficAllocation != e.TrafficAllocation, editable(draft, running)},
		{"/inclusion_salt", d.Salts.Inclusion != "" && d.Salts.Inclusion != e.InclusionSalt, editable(draft)},
		{"/variation_salt", d.Salts.Variation != "" && d.Salts.Variation != e.VariationSalt, editable(draft)},
		{"/status", d.Status != e.Status, false},
		{"/expected_end_at", !sameOptionalDay(d.ExpectedEndAt, e.ExpectedEndAt), editable(draft, running)},
		{"/started_at", !sameInstant(d.StartedAt, e.StartedAt), false},
		{"/stopped_at", !sameInstant(d.StoppedAt, e.StoppedAt), false},
		{"/archived_at", !sameInstant(d.ArchivedAt, e.ArchivedAt), false},
		{"/decision", !sameDecision(d.Decision, e.Decision), false},
	}
	var violations []apperrors.Violation
	for _, f := range fields {
		if f.differs && !f.allowed {
			violations = append(violations, apperrors.Violation{Pointer: f.pointer, Code: "frozen", Detail: e.frozenDetail()})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	err := apperrors.Conflict("frozen", "the experiment "+e.Key+" is "+string(e.Status)+": "+e.frozenDetail())
	err.Violations = violations
	return err
}

// frozenDetail says what an import may change of e, by its status.
func (e Experiment) frozenDetail() string {
	switch e.Status {
	case StatusDraft:
		return "an import changes a draft's definition and salts, and never moves it through its status"
	case StatusRunning:
		return "an import changes a running experiment's name, hypothesis, owner, tags and end date and grows its traffic allocation, and never moves it through its status"
	case StatusStopped:
		return "an import changes a stopped experiment's name, hypothesis, owner and tags, and never its decision or its status"
	default:
		return "an archived experiment does not change"
	}
}

// sameOptionalDay reports whether the day of a is b's, both absent counting
// as the same.
func sameOptionalDay(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return day(*a).Equal(day(*b))
}

// sameInstant reports whether a and b are the same instant to the
// microsecond, both absent counting as the same.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return microseconds(a).Equal(*microseconds(b))
}

func sameDecision(a, b *Decision) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Outcome == b.Outcome && a.WinnerVariation == b.WinnerVariation && strings.TrimSpace(a.Note) == strings.TrimSpace(b.Note)
}
