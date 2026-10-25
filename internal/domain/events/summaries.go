package events

import (
	"fmt"
	"math"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// StreamEvaluationSummaries rolls evaluation summaries up into evaluations
// per day and when each flag was last evaluated.
const StreamEvaluationSummaries Stream = "evaluation_summaries"

// SummaryRow is a stored evaluation summary row as the summary rollup reads
// it: one flag, variation and reason of a summary.
type SummaryRow struct {
	Project     workspace.ProjectID
	Environment workspace.EnvironmentID
	// Flag is the project's flag keyed as the row names it, the zero ID
	// when the project has none any more.
	Flag   flags.FlagID
	Source delivery.SummarySource
	// First marks the first row of a summary an SDK reported, which counts
	// the summary as one event received however many rows it has.
	First bool
	// Variation is "" when no variation was served.
	Variation  string
	Reason     string
	Count      int64
	PeriodEnd  time.Time
	ReceivedAt time.Time
}

// FlagEnvironment names one flag in one environment.
type FlagEnvironment struct {
	Flag        flags.FlagID
	Environment workspace.EnvironmentID
}

// EvaluationDay names the evaluations of a flag in an environment that
// served one variation for one reason, counted by one source on one UTC day
// of receipt.
type EvaluationDay struct {
	Environment workspace.EnvironmentID
	Flag        flags.FlagID
	// Variation is "" when no variation was served.
	Variation string
	Reason    string
	Source    delivery.SummarySource
	Day       time.Time
}

// SummaryRollup is what one batch of summary rows changes.
type SummaryRollup struct {
	// Days are the evaluations to add per day, each saturating.
	Days map[EvaluationDay]int64
	// LastEvaluated are the latest evaluations SDKs reported in the batch,
	// per flag and environment.
	LastEvaluated map[FlagEnvironment]time.Time
	// Events are the summaries SDKs reported, per project and day.
	Events map[EventDay]int64
}

// SaturatingAdd returns a+b for counts that are never negative, or the
// largest int64 when the sum would pass it: an SDK may report any count up to
// it, and a counter that stops growing is better than a rollup that fails
// and holds every project's summaries back.
func SaturatingAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// RollUpSummaries adds a batch of summary rows up per flag, variation,
// reason, source and day of receipt, and finds the latest evaluation of each
// flag. Remote evaluations the server answered are counted as a series of
// their own and never move when a flag was last evaluated: a client SDK is
// sent every flag whether its application reads it or not, so they would
// keep every flag of the project evaluated for good. A flag deleted since
// its rows were stored is left out of both; its summaries were still
// received.
func RollUpSummaries(rows []SummaryRow) SummaryRollup {
	out := SummaryRollup{
		Days: map[EvaluationDay]int64{}, LastEvaluated: map[FlagEnvironment]time.Time{}, Events: map[EventDay]int64{},
	}
	for _, r := range rows {
		if r.First {
			out.Events[EventDay{Project: r.Project, Day: day(r.ReceivedAt), Type: EventEvaluationSummary}]++
		}
		if r.Flag.IsZero() {
			continue
		}
		k := EvaluationDay{
			Environment: r.Environment, Flag: r.Flag, Variation: r.Variation, Reason: r.Reason, Source: r.Source,
			Day: day(r.ReceivedAt),
		}
		out.Days[k] = SaturatingAdd(out.Days[k], r.Count)
		if r.Source == delivery.SourceSDK {
			later(out.LastEvaluated, FlagEnvironment{Flag: r.Flag, Environment: r.Environment}, r.PeriodEnd, r.ReceivedAt)
		}
	}
	return out
}

// LastExposed returns the latest exposure of each flag in each environment
// the rows name: an SDK recording an exposure evaluated the flag, override or
// not.
func LastExposed(rows []ExposureRow) map[FlagEnvironment]time.Time {
	out := map[FlagEnvironment]time.Time{}
	for _, r := range rows {
		if r.Flag.IsZero() {
			continue
		}
		later(out, FlagEnvironment{Flag: r.Flag, Environment: r.Unit.Environment}, r.OccurredAt, r.ReceivedAt)
	}
	return out
}

// later keeps in last the later of what it holds for k and at, at taken as
// receivedAt when it is after it: nothing was evaluated after the server
// received the report of it, whatever the device's clock said.
func later(last map[FlagEnvironment]time.Time, k FlagEnvironment, at, receivedAt time.Time) {
	if at.After(receivedAt) {
		at = receivedAt
	}
	if prev, ok := last[k]; !ok || at.After(prev) {
		last[k] = at.UTC()
	}
}

// The days counts per day, of evaluations or of events received, are read
// over: a month by default, and a quarter at most, which bounds the rows one
// request reads.
const (
	DefaultDaysRead = 30
	MaxDaysRead     = 90
)

// RangeError is a range of days that cannot be read.
type RangeError struct{ Reason string }

func (e *RangeError) Error() string { return e.Reason }

// DaysRead returns the first and last UTC day counts per day are read over,
// both included: to's day, else today's, and from's day, else the day
// DefaultDaysRead before the last. A first day after the last, or more
// than MaxDaysRead, is a RangeError.
func DaysRead(from, to *time.Time, today time.Time) (first, last time.Time, err error) {
	last = day(today)
	if to != nil {
		last = day(*to)
	}
	first = last.AddDate(0, 0, 1-DefaultDaysRead)
	if from != nil {
		first = day(*from)
	}
	if first.After(last) {
		return time.Time{}, time.Time{}, &RangeError{Reason: "from is after to"}
	}
	if first.AddDate(0, 0, MaxDaysRead).Before(last.AddDate(0, 0, 1)) {
		return time.Time{}, time.Time{}, &RangeError{Reason: fmt.Sprintf("from and to span more than %d days", MaxDaysRead)}
	}
	return first, last, nil
}

// FlagEvaluationDay is a flag's evaluations in an environment on one UTC day
// that served one variation for one reason, counted by one source.
type FlagEvaluationDay struct {
	Day time.Time
	// Variation is "" when no variation was served.
	Variation   string
	Reason      string
	Source      delivery.SummarySource
	Evaluations int64
}
