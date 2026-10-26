package governance

import (
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Moved is what an audit entry moves.
type Moved struct {
	// Flag and Environment name the configuration a write changed, every
	// configuration of the flag when Environment is zero.
	Flag        flags.FlagID
	Environment workspace.EnvironmentID
	// Configurations reports that the write changed the configurations
	// Flag and Environment name.
	Configurations bool
	// Definition reports that the write changed the definition of Flag or
	// of Segment.
	Segment    flags.SegmentID
	Definition bool
	// EnvironmentDeleted reports that Environment was deleted.
	EnvironmentDeleted bool
}

// MovedBy returns what e moves, or false when e moves no change request.
//
// Every write that changes what a change request was based on records an
// entry, so the entry alone tells which change requests it moves:
//   - an entry of a flag naming an environment records a write to the
//     flag's configuration there, whichever write it was (a direct write, an
//     override, the kill switch, a copy, a revert, a salt, a rewrite by an
//     experiment's stop, another change request's application);
//   - every other entry of a flag records a write to its definition, and
//     archiving or deleting it also ends what every one of its
//     configurations can be changed to;
//   - every entry of a segment records a write to its definition;
//   - deleting an environment moves every change request naming it.
//
// Creating an entity moves nothing, since nothing can be proposed for it
// before; every other entry moves no change request.
func MovedBy(e Entry) (Moved, bool) {
	switch e.Entity.Kind {
	case "flag":
		flag, err := id.Parse[flags.Flag](e.Entity.ID)
		if err != nil || e.Action == "flag.created" {
			return Moved{}, false
		}
		switch {
		case !e.Environment.IsZero():
			return Moved{Flag: flag, Environment: e.Environment, Configurations: true}, true
		case e.Action == "flag.archived" || e.Action == "flag.deleted":
			return Moved{Flag: flag, Configurations: true, Definition: true}, true
		}
		return Moved{Flag: flag, Definition: true}, true
	case "segment":
		segment, err := id.Parse[flags.Segment](e.Entity.ID)
		if err != nil || e.Action == "segment.created" {
			return Moved{}, false
		}
		return Moved{Segment: segment, Definition: true}, true
	case "environment":
		if e.Action == "environment.deleted" && !e.Environment.IsZero() {
			return Moved{Environment: e.Environment, EnvironmentDeleted: true}, true
		}
	}
	return Moved{}, false
}

// Referrers are the flags whose configurations name a segment now, in some
// environment, by segment.
type Referrers map[flags.SegmentID][]flags.FlagID

// NeedsReferrers reports whether which change requests m moves depends on
// which flags name cr's segment now: a write to a flag's configurations
// moves a segment's definition its configurations name.
func (m Moved) NeedsReferrers(cr ChangeRequest) bool {
	return m.Configurations && cr.Kind == KindDefinition && cr.Segment != nil
}

// Superseded is a change request before and after a write superseded it.
type Superseded struct {
	Before, After ChangeRequest
}

// Supersedes returns those of candidates e supersedes, superseded by e at
// its instant: moved by e, in a state a supersede is taken from (open or
// approved), and not the change request whose application wrote e, which
// is applied by that write rather than overtaken by it. referrers are the
// flags naming the segments of the candidates now, which a write to a
// configuration is judged against.
func Supersedes(e Entry, candidates []ChangeRequest, referrers Referrers) []Superseded {
	moved, ok := MovedBy(e)
	if !ok {
		return nil
	}
	var out []Superseded
	for _, cr := range candidates {
		if !moved.moves(cr, referrers) || cr.ID == e.ChangeRequest {
			continue
		}
		if after, err := cr.Supersede(e.ID, e.OccurredAt); err == nil {
			out = append(out, Superseded{Before: cr, After: after})
		}
	}
	return out
}

// moves reports whether m moves what cr was based on:
//   - a configuration's change request, by a write to that configuration
//     or the deletion of its environment;
//   - a flag's definition, by any write to the flag, its configurations
//     included, since the definition was reviewed with them;
//   - a segment's definition, by a write to the segment, or to the
//     configurations of a flag that named it when the change request was
//     based on it or names it now: the configurations the definition was
//     reviewed with, and those it would now change what they serve.
func (m Moved) moves(cr ChangeRequest, referrers Referrers) bool {
	switch {
	case cr.Kind == KindFlagConfiguration && cr.Flag != nil && cr.Environment != nil:
		if m.EnvironmentDeleted {
			return cr.Environment.ID == m.Environment
		}
		return m.Configurations && cr.Flag.ID == m.Flag && (m.Environment.IsZero() || cr.Environment.ID == m.Environment)
	case cr.Kind == KindDefinition && cr.Flag != nil:
		return (m.Configurations || m.Definition) && cr.Flag.ID == m.Flag
	case cr.Kind == KindDefinition && cr.Segment != nil:
		if m.Definition && cr.Segment.ID == m.Segment {
			return true
		}
		return m.Configurations &&
			(slices.Contains(cr.BaseReferrers, m.Flag) || slices.Contains(referrers[cr.Segment.ID], m.Flag))
	}
	return false
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
