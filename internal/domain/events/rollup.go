package events

import (
	"bytes"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Stream names a rollup: a reader of one raw event table moving a cursor of
// its own.
type Stream string

// StreamExposures rolls exposures up into first exposures and daily counts.
const StreamExposures Stream = "exposures"

// Cursor is a place in a raw event table, which is read in the order of the
// inserting transaction's ID, then of each row's sequence number. The zero
// Cursor is before every row.
type Cursor struct {
	XID uint64
	Seq int64
}

// UnitKey names one unit's exposures to an experiment in an environment.
type UnitKey struct {
	Environment workspace.EnvironmentID
	Experiment  experiments.ExperimentID
	UnitType    string
	UnitID      string
}

// ExposureRow is a stored exposure as the exposure rollup reads it.
type ExposureRow struct {
	Project workspace.ProjectID
	Unit    UnitKey
	// Flag is the experiment's flag, the zero ID when the experiment is
	// gone.
	Flag       flags.FlagID
	EventID    delivery.EventID
	Variation  string
	Override   bool
	OccurredAt time.Time
	ReceivedAt time.Time
}

// Seen is what a first exposure keeps of a unit's exposures with one
// variation: how many there were and which of them occurred last.
type Seen struct {
	Count  int64
	LastAt time.Time
	LastID delivery.EventID
}

// FirstExposure is a unit's non-override exposure to an experiment with the
// earliest occurrence, ties broken by the smaller event ID, and what the
// unit's other non-override exposures were. It depends only on which
// exposures the unit had, never on the order they arrived in, so a late
// earlier exposure replaces it and two replicas agree.
type FirstExposure struct {
	EventID    delivery.EventID
	OccurredAt time.Time
	Variation  string
	// Seen are the unit's exposures by variation, the first's included.
	Seen map[string]Seen
}

// before reports whether an exposure at at with ID id occurred before one at
// other with ID otherID.
func before(at time.Time, id delivery.EventID, other time.Time, otherID delivery.EventID) bool {
	if !at.Equal(other) {
		return at.Before(other)
	}
	a, b := id.Bytes(), otherID.Bytes()
	return bytes.Compare(a[:], b[:]) < 0
}

// Conflicts is how many of the unit's exposures named another variation than
// its first exposure's.
func (f FirstExposure) Conflicts() int64 {
	var n int64
	for v, s := range f.Seen {
		if v != f.Variation {
			n += s.Count
		}
	}
	return n
}

// Conflicting returns the variation other than the first's that the unit was
// last exposed to, by occurrence, and when; ok is false without a conflict.
func (f FirstExposure) Conflicting() (variation string, at time.Time, ok bool) {
	var last Seen
	for v, s := range f.Seen {
		if v == f.Variation {
			continue
		}
		// Ties in time go to the larger event ID, the later of the two.
		if !ok || before(last.LastAt, last.LastID, s.LastAt, s.LastID) {
			variation, last, ok = v, s, true
		}
	}
	return variation, last.LastAt, ok
}

// fold adds the exposure r to f, nil when the unit had none, and returns the
// result.
func fold(f *FirstExposure, r ExposureRow) FirstExposure {
	if f == nil {
		return FirstExposure{
			EventID: r.EventID, OccurredAt: r.OccurredAt, Variation: r.Variation,
			Seen: map[string]Seen{r.Variation: {Count: 1, LastAt: r.OccurredAt, LastID: r.EventID}},
		}
	}
	out := FirstExposure{EventID: f.EventID, OccurredAt: f.OccurredAt, Variation: f.Variation, Seen: make(map[string]Seen, len(f.Seen)+1)}
	for v, s := range f.Seen {
		out.Seen[v] = s
	}
	s := out.Seen[r.Variation]
	if s.Count == 0 || before(s.LastAt, s.LastID, r.OccurredAt, r.EventID) {
		s.LastAt, s.LastID = r.OccurredAt, r.EventID
	}
	s.Count++
	out.Seen[r.Variation] = s
	if before(r.OccurredAt, r.EventID, out.OccurredAt, out.EventID) {
		out.EventID, out.OccurredAt, out.Variation = r.EventID, r.OccurredAt, r.Variation
	}
	return out
}

// ExposureDay names an experiment's exposures of one variation on one UTC
// day in an environment.
type ExposureDay struct {
	Environment workspace.EnvironmentID
	Experiment  experiments.ExperimentID
	Variation   string
	Day         time.Time
}

// ExposureDayCounts are the exposure counts of a variation on a day:
// exposures and overridden exposures by the day they were received, first
// exposures by the day the first exposure occurred, so a replacement moves
// its count to its own day.
type ExposureDayCounts struct {
	Exposures      int64
	Overridden     int64
	FirstExposures int64
}

// EventDay names the events of one type a project received on one UTC day.
type EventDay struct {
	Project workspace.ProjectID
	Day     time.Time
	Type    EventType
}

// ExposureRollup is what one batch of exposures changes.
type ExposureRollup struct {
	// Records are the first exposures of the units the batch named, as
	// they are after it.
	Records map[UnitKey]FirstExposure
	// Days are the changes to the exposure counts per day; a replaced
	// first exposure takes one off its old variation and day.
	Days map[ExposureDay]ExposureDayCounts
	// Events are the events received per project and day.
	Events map[EventDay]int64
}

// CountExposureEvents returns the exposures rows hold per project and UTC
// day of receipt.
func CountExposureEvents(rows []ExposureRow) map[EventDay]int64 {
	out := map[EventDay]int64{}
	for _, r := range rows {
		out[EventDay{Project: r.Project, Day: day(r.ReceivedAt), Type: EventExposure}]++
	}
	return out
}

// day returns the UTC day of at.
func day(at time.Time) time.Time { return at.UTC().Truncate(24 * time.Hour) }

// RollUpExposures applies a batch of exposures to the first exposures stored
// of the units it names. Override exposures are counted as overridden and
// never touch a first exposure: an override decides the variation whatever
// the assignment, so it says nothing of how units were split. Whatever order
// the exposures come in, and however they are split into batches applied one
// after the other, the records and the summed counts come out the same.
func RollUpExposures(stored map[UnitKey]FirstExposure, rows []ExposureRow) ExposureRollup {
	out := ExposureRollup{
		Records: map[UnitKey]FirstExposure{},
		Days:    map[ExposureDay]ExposureDayCounts{},
		Events:  CountExposureEvents(rows),
	}
	add := func(k ExposureDay, c ExposureDayCounts) {
		sum := out.Days[k]
		sum.Exposures += c.Exposures
		sum.Overridden += c.Overridden
		sum.FirstExposures += c.FirstExposures
		out.Days[k] = sum
	}
	dayOf := func(u UnitKey, variation string, at time.Time) ExposureDay {
		return ExposureDay{Environment: u.Environment, Experiment: u.Experiment, Variation: variation, Day: day(at)}
	}
	for _, r := range rows {
		if r.Override {
			add(dayOf(r.Unit, r.Variation, r.ReceivedAt), ExposureDayCounts{Overridden: 1})
			continue
		}
		add(dayOf(r.Unit, r.Variation, r.ReceivedAt), ExposureDayCounts{Exposures: 1})
		var prior *FirstExposure
		if f, ok := out.Records[r.Unit]; ok {
			prior = &f
		} else if f, ok := stored[r.Unit]; ok {
			prior = &f
		}
		next := fold(prior, r)
		out.Records[r.Unit] = next
		if prior != nil && prior.EventID == next.EventID {
			continue
		}
		if prior != nil {
			add(dayOf(r.Unit, prior.Variation, prior.OccurredAt), ExposureDayCounts{FirstExposures: -1})
		}
		add(dayOf(r.Unit, next.Variation, next.OccurredAt), ExposureDayCounts{FirstExposures: 1})
	}
	for k, c := range out.Days {
		if c == (ExposureDayCounts{}) {
			delete(out.Days, k)
		}
	}
	return out
}

// VariationExposures are an experiment's exposure counts of one variation,
// summed over days, with its share of first exposures and the share its
// weight asks for.
type VariationExposures struct {
	Variation string
	ExposureDayCounts
	ObservedShare float64
	ExpectedShare float64
}

// VariationExposuresOf returns the counts of each variation of an
// experiment weighed by weights, in their order and listed even without an
// exposure, then of every other variation counts names by key: override
// exposures may name a variation the experiment does not weigh, which
// expects no share.
func VariationExposuresOf(weights []experiments.Weight, counts map[string]ExposureDayCounts) []VariationExposures {
	var first int64
	for _, c := range counts {
		first += c.FirstExposures
	}
	out := make([]VariationExposures, 0, len(weights))
	weighed := map[string]bool{}
	entry := func(variation string, expected float64) VariationExposures {
		v := VariationExposures{Variation: variation, ExposureDayCounts: counts[variation], ExpectedShare: expected}
		if first > 0 {
			v.ObservedShare = float64(v.FirstExposures) / float64(first)
		}
		return v
	}
	for _, w := range weights {
		weighed[w.Variation] = true
		out = append(out, entry(w.Variation, float64(w.Weight)/experiments.TotalWeight))
	}
	var others []string
	for v := range counts {
		if !weighed[v] {
			others = append(others, v)
		}
	}
	slices.Sort(others)
	for _, v := range others {
		out = append(out, entry(v, 0))
	}
	return out
}

// ExposureTimelineDay is an experiment's exposure counts of one variation on
// one UTC day.
type ExposureTimelineDay struct {
	Day       time.Time
	Variation string
	ExposureDayCounts
}

// Conflict is a unit exposed to another variation than its first exposure's:
// the first exposure's variation, and the other variation it was last
// exposed to, by occurrence, and when.
type Conflict struct {
	UnitType             string
	UnitID               string
	FirstVariation       string
	ConflictingVariation string
	ConflictingAt        time.Time
}
