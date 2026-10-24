package experiments

import (
	"slices"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// errNotDraft refuses what only a draft allows: its salts are regenerated
// and it is deleted while no SDK has been given an assignment from them.
var errNotDraft = apperrors.Conflict("not_draft", "only a draft experiment allows this: it is edited in full, has its salts regenerated and is deleted")

// Change is an edit of a draft: a nil field stays as it is. The facts about
// the project are those the handler loaded for the fields the edit names.
type Change struct {
	// Key is refused whatever it holds: an experiment's key never changes.
	Key               *string
	Name              *string
	Hypothesis        *string
	Owner             *Owner
	Tags              *[]string
	FlagKey           *string
	UnitType          *string
	Variations        *[]Weight
	TrafficAllocation *int
	ExpectedEndAt     *time.Time
	// Flag is the flag the experiment compares once changed (the one
	// FlagKey names, else its current one), nil when the project has none
	// with that key. It is judged only when the edit names the flag or the
	// variations, so an archived flag never blocks an edit of the hypothesis.
	Flag *FlagFacts
	// DeclaredUnitTypes are the keys of the project's unit types.
	DeclaredUnitTypes []string
}

// Change returns the experiment with ch applied at now, its version
// incremented, and whether anything changed: an edit to the values the
// experiment already has changes nothing. A request breaking several rules is
// invalid_experiment with every field to fix. A draft is edited in full; a
// running experiment refuses a change to its split first (frozen), and its
// traffic allocation only grows (traffic_allocation_decrease, judged once the
// fields are); past running nothing is edited (not_draft), judged once the
// fields are.
func (e Experiment) Change(ch Change, now time.Time) (Experiment, bool, error) {
	if e.Status == StatusRunning {
		var err error
		if ch, err = e.frozen(ch); err != nil {
			return e, false, err
		}
	}
	c := newCheck()
	if ch.Key != nil {
		c.add("/key", "immutable", "an experiment's key never changes; create a new one instead", nil)
	}
	out := e
	if ch.Name != nil {
		out.Name = c.name(*ch.Name)
	}
	if ch.Hypothesis != nil {
		out.Hypothesis = c.hypothesis(*ch.Hypothesis)
		// Starting requires a hypothesis, so that an experiment says what it
		// tests for as long as it runs and after it stops; only a draft may
		// be without one.
		if out.Hypothesis == "" && e.Status != StatusDraft {
			c.add("/hypothesis", "required", "an experiment past its draft says what it expects to find", nil)
		}
	}
	if ch.Owner != nil && ch.Owner.Member != e.Owner {
		out.Owner = c.owner(*ch.Owner)
	}
	if ch.Tags != nil {
		out.Tags = c.tags(*ch.Tags)
	}
	if ch.TrafficAllocation != nil {
		out.TrafficAllocation = c.trafficAllocation(*ch.TrafficAllocation)
	}
	if ch.ExpectedEndAt != nil {
		d := day(*ch.ExpectedEndAt)
		out.ExpectedEndAt = &d
	}
	if ch.UnitType != nil && *ch.UnitType != e.UnitType {
		out.UnitType = *ch.UnitType
		if !slices.Contains(ch.DeclaredUnitTypes, out.UnitType) {
			c.add("/unit_type", "undeclared_unit_type", "the project has not declared this unit type", nil)
		}
	}
	flagChanged := ch.FlagKey != nil && *ch.FlagKey != e.FlagKey
	if ch.Variations != nil {
		out.Variations = *ch.Variations
	}
	if flagChanged || ch.Variations != nil {
		var flagVariations []string
		flagUsable := false
		switch {
		case ch.Flag == nil:
			c.add("/flag", "unknown_flag", "the project has no flag with this key", nil)
		case ch.Flag.Archived:
			c.add("/flag", "flag_archived", "an archived flag cannot be compared", nil)
		default:
			out.Flag, out.FlagKey = ch.Flag.ID, ch.Flag.Key
			flagVariations, flagUsable = ch.Flag.Variations, true
		}
		c.weights(out.Variations, flagUsable, flagVariations)
	}
	if err := c.err(); err != nil {
		return e, false, err
	}
	switch e.Status {
	case StatusDraft:
	case StatusRunning:
		if out.TrafficAllocation < e.TrafficAllocation {
			return e, false, errAllocationDecrease
		}
	default:
		return e, false, errNotDraft
	}
	if out.sameDefinition(e) {
		return e, false, nil
	}
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, true, nil
}

func (e Experiment) sameDefinition(o Experiment) bool {
	return e.Name == o.Name && e.Hypothesis == o.Hypothesis && e.Owner == o.Owner && slices.Equal(e.Tags, o.Tags) &&
		e.Flag == o.Flag && e.UnitType == o.UnitType && slices.Equal(e.Variations, o.Variations) &&
		e.TrafficAllocation == o.TrafficAllocation && sameDay(e.ExpectedEndAt, o.ExpectedEndAt)
}

func sameDay(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// CanDelete refuses to delete an experiment that is not a draft: once
// started it has exposures and a history that deleting would erase.
func (e Experiment) CanDelete() error {
	if e.Status != StatusDraft {
		return errNotDraft
	}
	return nil
}

// RegenerateSalts returns the draft with s as its salts at now, its version
// incremented. Past the draft its assignments exist, and moving every unit
// is not an edit (not_draft).
func (e Experiment) RegenerateSalts(s Salts, now time.Time) (Experiment, error) {
	if e.Status != StatusDraft {
		return e, errNotDraft
	}
	out := e
	out.InclusionSalt, out.VariationSalt = s.Inclusion, s.Variation
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, nil
}
