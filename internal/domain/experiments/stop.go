package experiments

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Outcome is what a stop decided about the experiment's rules.
type Outcome string

// The outcomes of a stop.
const (
	// OutcomeWinner serves the winning variation where the experiment was
	// served, each holding rule keeping its identifier and conditions.
	OutcomeWinner Outcome = "winner"
	// OutcomeRemoved deletes the experiment's rules, so the flag goes back
	// to its other rules.
	OutcomeRemoved Outcome = "removed"
)

// maxNoteLength bounds a decision's note.
const maxNoteLength = 2000

// Decision is what a stop decided: an outcome, the winning variation for a
// winner ("" otherwise), and a note saying why ("" for none).
type Decision struct {
	Outcome         Outcome
	WinnerVariation string
	Note            string
}

// errArchived refuses every write of an archived experiment: archiving is
// the end of its life, kept for its history alone.
var errArchived = apperrors.Conflict("archived", "an archived experiment does not change")

// errNotRunning refuses to stop what is not running: a draft has nothing to
// decide, and a stopped experiment has decided already.
var errNotRunning = apperrors.Conflict("not_running", "only a running experiment stops")

// errRunning refuses to archive a running experiment: it stops with a
// decision first, so its rules never go on serving an archived experiment.
var errRunning = apperrors.Conflict("running", "a running experiment is stopped with a decision before it is archived")

// errNotStopped refuses a decision's note before there is a decision.
var errNotStopped = apperrors.Conflict("not_stopped", "only a stopped experiment has a decision to note")

// Stop returns the running experiment stopped at now with d, its version
// incremented. Only a running experiment stops (not_running; archived for
// an archived one). A winner is one of the experiment's variations, and a
// removal names none, so that what replaces the experiment's rules is
// always something the comparison measured; invalid_decision otherwise.
func (e Experiment) Stop(d Decision, now time.Time) (Experiment, error) {
	switch e.Status {
	case StatusRunning:
	case StatusArchived:
		return e, errArchived
	default:
		return e, errNotRunning
	}
	c := newCheck()
	d.Note = c.note(d.Note)
	switch d.Outcome {
	case OutcomeWinner:
		switch {
		case d.WinnerVariation == "":
			c.add("/winner_variation", "required", "a winner names the variation that won", nil)
		case !slices.ContainsFunc(e.Variations, func(w Weight) bool { return w.Variation == d.WinnerVariation }):
			c.add("/winner_variation", "unknown_variation", "the winner is one of the variations the experiment compares", nil)
		}
	case OutcomeRemoved:
		if d.WinnerVariation != "" {
			c.add("/winner_variation", "invalid_value", "a removal names no winner", nil)
		}
	default:
		c.add("/outcome", "invalid_value", "an outcome is winner or removed", nil)
	}
	if len(c.violations) > 0 {
		return e, apperrors.Invalid("invalid_decision", "the decision is not valid", c.violations...)
	}
	out := e
	out.Status, out.StoppedAt, out.Decision = StatusStopped, &now, &d
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, nil
}

// SetDecisionNote returns the stopped experiment whose decision has note at
// now, its version incremented, and whether anything changed: the same note
// changes nothing, and "" clears it. Only a stopped experiment has a
// decision (not_stopped; archived for an archived one).
func (e Experiment) SetDecisionNote(note string, now time.Time) (Experiment, bool, error) {
	switch e.Status {
	case StatusStopped:
	case StatusArchived:
		return e, false, errArchived
	default:
		return e, false, errNotStopped
	}
	c := newCheck()
	note = c.note(note)
	if len(c.violations) > 0 {
		return e, false, apperrors.Invalid("invalid_decision", "the decision is not valid", c.violations...)
	}
	if e.Decision != nil && e.Decision.Note == note {
		return e, false, nil
	}
	out := e
	d := Decision{}
	if e.Decision != nil {
		d = *e.Decision
	}
	d.Note = note
	out.Decision = &d
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, true, nil
}

// Archive returns the draft or stopped experiment archived at now, its
// version incremented: it is hidden from the list and never changes again.
// A running experiment is stopped first (running), and an archived one is
// archived already (archived).
func (e Experiment) Archive(now time.Time) (Experiment, error) {
	switch e.Status {
	case StatusDraft, StatusStopped:
	case StatusRunning:
		return e, errRunning
	default:
		return e, errArchived
	}
	out := e
	out.Status, out.ArchivedAt = StatusArchived, &now
	out.UpdatedAt, out.Version = now, e.Version+1
	return out, nil
}

func (c *check) note(n string) string {
	n = strings.TrimSpace(n)
	if utf8.RuneCountInString(n) > maxNoteLength {
		c.add("/note", "too_long", "a note has at most 2,000 characters", map[string]any{"max": maxNoteLength})
	}
	return n
}
