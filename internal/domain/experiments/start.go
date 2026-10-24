package experiments

import (
	"slices"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// errNotDraftToStart refuses to start what is past its draft: a running
// experiment runs already, and a stopped one is rerun as a clone with new
// salts, never restarted on assignments units were already given.
var errNotDraftToStart = apperrors.Conflict("not_draft", "only a draft experiment starts; a stopped one is rerun as a clone")

// errAllocationDecrease refuses to shrink a running experiment's traffic
// allocation: the units it would leave out have been assigned and exposed,
// and dropping them would bias what the comparison counts from then on.
var errAllocationDecrease = apperrors.Conflict("traffic_allocation_decrease",
	"a running experiment's traffic allocation only grows: shrinking it would take out units already assigned")

// StartFacts are what a start is judged against: the experiment's flag and
// the project's unit types as they are now, since either may have changed
// since the draft was written.
type StartFacts struct {
	Flag FlagFacts
	// DeclaredUnitTypes are the keys of the project's unit types.
	DeclaredUnitTypes []string
}

// Start returns the draft running from now, its version incremented. Only a
// draft starts (not_draft). A draft that is not ready is
// experiment_not_ready with every precondition it fails: its flag is not
// archived, its unit type is declared, its weights are valid against the
// flag, it has a hypothesis and an expected end date, so a running
// experiment always says what it tests and when it should be over.
func (e Experiment) Start(f StartFacts, now time.Time) (Experiment, error) {
	if e.Status != StatusDraft {
		return e, errNotDraftToStart
	}
	c := newCheck()
	if f.Flag.Archived {
		c.add("/flag", "flag_archived", "an archived flag cannot be compared", nil)
	}
	if !slices.Contains(f.DeclaredUnitTypes, e.UnitType) {
		c.add("/unit_type", "undeclared_unit_type", "the project has not declared this unit type", nil)
	}
	c.weights(e.Variations, !f.Flag.Archived, f.Flag.Variations)
	if e.Hypothesis == "" {
		c.add("/hypothesis", "required", "a running experiment says what it expects to find", nil)
	}
	if e.ExpectedEndAt == nil {
		c.add("/expected_end_at", "required", "a running experiment says when it is expected to end", nil)
	}
	if len(c.violations) > 0 {
		return e, apperrors.Invalid("experiment_not_ready", "the experiment is not ready to start", c.violations...)
	}
	out := e
	out.Status, out.StartedAt = StatusRunning, &now
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, nil
}

// frozen returns ch without the fields of the split it names with the
// values the running experiment has, or frozen naming every one it would
// change: the flag, the unit type and the weights decide which variation
// each unit gets, so changing any of them while the experiment runs would
// mix two comparisons in one.
func (e Experiment) frozen(ch Change) (Change, error) {
	var violations []apperrors.Violation
	freeze := func(pointer string) {
		violations = append(violations, apperrors.Violation{Pointer: pointer, Code: "frozen",
			Detail: "a running experiment's flag, unit type and weights do not change"})
	}
	if ch.FlagKey != nil && *ch.FlagKey != e.FlagKey {
		freeze("/flag")
	}
	if ch.UnitType != nil && *ch.UnitType != e.UnitType {
		freeze("/unit_type")
	}
	if ch.Variations != nil && !slices.Equal(*ch.Variations, e.Variations) {
		freeze("/variations")
	}
	if len(violations) > 0 {
		err := apperrors.Conflict("frozen", "a running experiment's flag, unit type and weights do not change")
		err.Violations = violations
		return ch, err
	}
	ch.FlagKey, ch.UnitType, ch.Variations, ch.Flag = nil, nil, nil, nil
	return ch, nil
}
