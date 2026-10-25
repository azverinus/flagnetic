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
	switch e.Status {
	case StatusDraft:
	case StatusArchived:
		return e, errArchived
	default:
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

// frozen returns ch without the fields the experiment's status freezes
// that it names with the values the experiment has, or frozen naming every
// one it would change. While it runs, the flag, the unit type and the
// weights decide which variation each unit gets, so changing any of them
// would mix two comparisons in one. Once stopped, the traffic allocation
// and the expected end date are part of what was measured too, and only
// the description and the decision's note still change.
func (e Experiment) frozen(ch Change) (Change, error) {
	detail := "a running experiment's flag, unit type and weights do not change"
	if e.Status == StatusStopped {
		detail = "a stopped experiment changes only its name, hypothesis, owner and tags; its decision note changes through its decision"
	}
	var violations []apperrors.Violation
	freeze := func(pointer string) {
		violations = append(violations, apperrors.Violation{Pointer: pointer, Code: "frozen", Detail: detail})
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
	if e.Status == StatusStopped {
		if ch.TrafficAllocation != nil && *ch.TrafficAllocation != e.TrafficAllocation {
			freeze("/traffic_allocation")
		}
		if ch.ExpectedEndAt != nil && (e.ExpectedEndAt == nil || !day(*ch.ExpectedEndAt).Equal(*e.ExpectedEndAt)) {
			freeze("/expected_end_at")
		}
		ch.TrafficAllocation, ch.ExpectedEndAt = nil, nil
	}
	if len(violations) > 0 {
		err := apperrors.Conflict("frozen", detail)
		err.Violations = violations
		return ch, err
	}
	ch.FlagKey, ch.UnitType, ch.Variations, ch.Flag = nil, nil, nil, nil
	return ch, nil
}
