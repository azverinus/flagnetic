package governance

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Moved is what an audit entry moves: the change requests naming Flag, when
// it is set, and Environment, when it is set.
type Moved struct {
	Flag        flags.FlagID
	Environment workspace.EnvironmentID
}

// MovedBy returns what e moves, or false when e moves no change request.
//
// Every write that changes what a change request was based on records an
// entry, so the entry alone tells which change requests it moves:
//   - an entry of a flag naming an environment records a write to the
//     flag's configuration there, whichever write it was (a direct write, an
//     override, the kill switch, a copy, a revert, a salt, a rewrite by an
//     experiment's stop, another change request's application), and moves
//     the change requests on that configuration;
//   - archiving or deleting a flag ends what every one of its
//     configurations can be changed to, and moves the change requests on
//     any of them;
//   - deleting an environment moves every change request naming it.
//
// A flag's definition edit, its restoration, and every other entry move no
// configuration's change request.
func MovedBy(e Entry) (Moved, bool) {
	switch e.Entity.Kind {
	case "flag":
		flag, err := id.Parse[flags.Flag](e.Entity.ID)
		if err != nil {
			return Moved{}, false
		}
		switch {
		case !e.Environment.IsZero():
			return Moved{Flag: flag, Environment: e.Environment}, true
		case e.Action == "flag.archived" || e.Action == "flag.deleted":
			return Moved{Flag: flag}, true
		}
	case "environment":
		if e.Action == "environment.deleted" && !e.Environment.IsZero() {
			return Moved{Environment: e.Environment}, true
		}
	}
	return Moved{}, false
}

// Superseded is a change request before and after a write superseded it.
type Superseded struct {
	Before, After ChangeRequest
}

// Supersedes returns those of candidates e supersedes, superseded by e at
// its instant: naming what e moves, in a state a supersede is taken from
// (open or approved), and not the change request whose application wrote
// e, which is applied by that write rather than overtaken by it.
func Supersedes(e Entry, candidates []ChangeRequest) []Superseded {
	moved, ok := MovedBy(e)
	if !ok {
		return nil
	}
	var out []Superseded
	for _, cr := range candidates {
		if !moved.moves(cr) || cr.ID == e.ChangeRequest {
			continue
		}
		if after, err := cr.Supersede(e.ID, e.OccurredAt); err == nil {
			out = append(out, Superseded{Before: cr, After: after})
		}
	}
	return out
}

// moves reports whether cr names what m moves.
func (m Moved) moves(cr ChangeRequest) bool {
	if !m.Flag.IsZero() && (cr.Flag == nil || cr.Flag.ID != m.Flag) {
		return false
	}
	if !m.Environment.IsZero() && (cr.Environment == nil || cr.Environment.ID != m.Environment) {
		return false
	}
	return true
}

// Supersede returns cr superseded at now by the write recorded as entry,
// which moved what cr was based on: applying it would write over that
// change, so it can only be copied onto the new base.
func (cr ChangeRequest) Supersede(entry EntryID, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionSupersede, Taker{System: true})
	if err != nil {
		return cr, err
	}
	out := cr.moved(state, now)
	out.SupersededBy = entry
	return out, nil
}
