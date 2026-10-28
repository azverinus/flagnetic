package governance

import (
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// EventStaleFlags is the catalogue type of the stale-flag digest.
const EventStaleFlags = "report.stale_flags"

// DigestActor is who the weekly digest is attributed to.
var DigestActor = Actor{Kind: ActorSystem, ID: "stale_flag_digest", Name: "stale-flag digest"}

// DigestSlotWindow is how long after its start a digest slot is still
// served. A digest is a reminder, not a state change: one whose hour passed
// with no replica running is skipped rather than sent late, since "the
// latest slot not yet handled" has no bound, and a fresh install would be
// sent last Monday's digest on its first round.
const DigestSlotWindow = time.Hour

// DigestSlotsKept is how long a handled slot is remembered. A slot older
// than DigestSlotWindow is never served again, so a row is only needed
// while its slot may be the current one; five weeks leaves room to look
// back when something goes wrong.
const DigestSlotsKept = 5 * 7 * 24 * time.Hour

// DigestSlot is when the weekly digest is due: one hour of one weekday, in
// UTC.
type DigestSlot struct {
	Weekday time.Weekday
	Hour    int
}

// DefaultDigestSlot is Monday at 08:00 UTC.
var DefaultDigestSlot = DigestSlot{Weekday: time.Monday, Hour: 8}

// ParseDigestWeekday reads a weekday by its English name, in any case.
func ParseDigestWeekday(s string) (time.Weekday, bool) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.EqualFold(s, d.String()) {
			return d, true
		}
	}
	return 0, false
}

// Current returns the start, in UTC, of the latest slot at or before now,
// and whether now is still within its DigestSlotWindow. The start names the
// slot: it is what a handled slot is recorded under.
func (s DigestSlot) Current(now time.Time) (time.Time, bool) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, time.UTC)
	start = start.AddDate(0, 0, -((int(start.Weekday()) - int(s.Weekday) + 7) % 7))
	if start.After(now) {
		start = start.AddDate(0, 0, -7)
	}
	return start, now.Sub(start) < DigestSlotWindow
}

// StaleFlagItem is a stale flag as the digest names it and the API reads it:
// the flag, why it is stale, when it was last evaluated in each environment
// and, for an overdue experiment, which.
type StaleFlagItem struct {
	Key               string           `json:"key"`
	Name              string           `json:"name"`
	Owner             string           `json:"owner"`
	Lifetime          string           `json:"lifetime"`
	ExpectedRemovalAt *time.Time       `json:"expected_removal_at"`
	Reasons           []string         `json:"reasons"`
	LastEvaluated     []LastEvaluated  `json:"last_evaluated"`
	Experiment        *StaleExperiment `json:"experiment,omitempty"`
}

// LastEvaluated is when a flag was last evaluated in an environment, nil
// for never.
type LastEvaluated struct {
	Environment     string     `json:"environment"`
	LastEvaluatedAt *time.Time `json:"last_evaluated_at"`
}

// StaleExperiment is the running experiment past its expected end that
// makes a flag stale.
type StaleExperiment struct {
	Key string `json:"key"`
	// ExpectedEndAt is a calendar day, YYYY-MM-DD.
	ExpectedEndAt string `json:"expected_end_at"`
}

// StaleFlagsEvent returns the digest of project at at: its stale flags, as
// the list of the API reads them, empty when none is. It names the project
// and who asked, and has no environment, entity or audit entry.
func StaleFlagsEvent(eventID EventID, project workspace.ProjectID, projectKey string, actor Actor, at time.Time,
	items []StaleFlagItem,
) Event {
	if items == nil {
		items = []StaleFlagItem{}
	}
	return Event{
		ID: eventID, Type: EventStaleFlags, Project: project, ProjectKey: projectKey, OccurredAt: at, Actor: &actor,
		Data: map[string]any{"items": items},
	}
}
