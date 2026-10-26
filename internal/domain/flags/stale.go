package flags

import (
	"slices"
	"strings"
	"time"
)

// StaleReason is why a flag is stale.
type StaleReason string

// The reasons a flag is stale.
const (
	// StaleLifetimeExceeded is a temporary flag past its expected removal.
	StaleLifetimeExceeded StaleReason = "lifetime_exceeded"
	// StaleNotEvaluated is a flag no SDK evaluated within
	// NotEvaluatedWindow in any environment.
	StaleNotEvaluated StaleReason = "not_evaluated"
	// StaleOwnerInactive is a flag whose owner may no longer own it:
	// nobody is left to ask before deleting it.
	StaleOwnerInactive StaleReason = "owner_inactive"
	// StaleFinishedRollout is a flag serving the same variation to
	// everyone in every environment: its code can keep that one branch.
	StaleFinishedRollout StaleReason = "finished_rollout"
	// StaleExperimentOverdue is a flag a running experiment past its
	// expected end compares the variations of.
	StaleExperimentOverdue StaleReason = "experiment_overdue"
)

// StaleReasons are every reason, in the order a flag's reasons are listed.
var StaleReasons = []StaleReason{
	StaleLifetimeExceeded, StaleNotEvaluated, StaleOwnerInactive, StaleFinishedRollout, StaleExperimentOverdue,
}

// NotEvaluatedWindow is how long a flag no SDK evaluates stays fresh: a
// month covers the release cycles of the applications reading it, so code
// still deployed evaluates it within one.
const NotEvaluatedWindow = 30 * 24 * time.Hour

// OverdueExperiment is a running experiment on a flag, past its expected
// end.
type OverdueExperiment struct {
	Key string
	// ExpectedEndAt is a calendar day, in UTC at midnight.
	ExpectedEndAt time.Time
}

// StaleFacts are what a flag is judged stale from.
type StaleFacts struct {
	Flag Flag
	// Configurations are the flag's configurations in every environment of
	// its project.
	Configurations []Configuration
	// LastEvaluatedAt is the latest an SDK evaluated the flag in any
	// environment, by its summaries and exposures; nil for never.
	LastEvaluatedAt *time.Time
	// LastSummaryDay is the latest UTC day the flag's project received an
	// SDK's evaluation summary, nil for never.
	LastSummaryDay *time.Time
	// OwnerActive is whether the flag's owner may still own it.
	OwnerActive bool
	// OverdueExperiments are the running experiments on the flag past
	// their expected end.
	OverdueExperiments []OverdueExperiment
}

// Staleness is a flag judged stale or not.
type Staleness struct {
	// Reasons are why the flag is stale, in StaleReasons' order; empty for
	// a flag that is not.
	Reasons []StaleReason
	// Experiment is the overdue experiment named with
	// StaleExperimentOverdue, nil without that reason.
	Experiment *OverdueExperiment
}

// JudgeStale returns why the flag of f is stale at now. An archived flag
// never is: it is already out of every ruleset, on its way out.
func JudgeStale(f StaleFacts, now time.Time) Staleness {
	s := Staleness{Reasons: []StaleReason{}}
	if f.Flag.ArchivedAt != nil {
		return s
	}
	if f.Flag.Lifetime == Temporary && f.Flag.ExpectedRemovalAt != nil && f.Flag.ExpectedRemovalAt.Before(now) {
		s.Reasons = append(s.Reasons, StaleLifetimeExceeded)
	}
	if f.notEvaluated(now) {
		s.Reasons = append(s.Reasons, StaleNotEvaluated)
	}
	if !f.OwnerActive {
		s.Reasons = append(s.Reasons, StaleOwnerInactive)
	}
	if finishedRollout(f.Configurations) {
		s.Reasons = append(s.Reasons, StaleFinishedRollout)
	}
	if len(f.OverdueExperiments) > 0 {
		e := slices.MinFunc(f.OverdueExperiments, func(a, b OverdueExperiment) int {
			if c := a.ExpectedEndAt.Compare(b.ExpectedEndAt); c != 0 {
				return c
			}
			return strings.Compare(a.Key, b.Key)
		})
		s.Reasons, s.Experiment = append(s.Reasons, StaleExperimentOverdue), &e
	}
	return s
}

// notEvaluated reports whether no SDK evaluated the flag within the window
// before now. It is judged only when the project received an SDK's summary
// on a day of the window, since an application whose SDKs send none looks
// unevaluated whatever it reads; and only for a flag older than the window,
// since a new one may not be deployed yet.
func (f StaleFacts) notEvaluated(now time.Time) bool {
	since := now.Add(-NotEvaluatedWindow)
	if f.LastSummaryDay == nil || f.LastSummaryDay.Before(since.UTC().Truncate(24*time.Hour)) {
		return false
	}
	if f.Flag.CreatedAt.After(since) {
		return false
	}
	return f.LastEvaluatedAt == nil || f.LastEvaluatedAt.Before(since)
}

// finishedRollout reports whether configs serve one variation, the same in
// all, to everyone. A project without environments serves nothing.
func finishedRollout(configs []Configuration) bool {
	served := ""
	for _, c := range configs {
		v, ok := c.servedToEveryone()
		if !ok || (served != "" && v != served) {
			return false
		}
		served = v
	}
	return served != ""
}

// servedToEveryone returns the variation c serves to every context, and
// false when contexts may be served different ones. A configuration
// switched off does not count: a flag is created off, so a flag never
// rolled out is not a finished rollout. Neither do overrides or a
// prerequisite, which serve some contexts another variation. What is left
// serves one variation to everyone with no rule, or with a first rule
// without conditions serving a rollout to every unit, which no context
// gets past.
func (c Configuration) servedToEveryone() (string, bool) {
	if !c.On || len(c.Prerequisites) > 0 || len(c.Overrides) > 0 {
		return "", false
	}
	if len(c.Rules) == 0 {
		return c.Fallthrough, true
	}
	first := c.Rules[0]
	if len(first.Conditions) == 0 && first.Serve.Kind == ServeRollout && first.Serve.Percentage == maxPercentage {
		return first.Serve.Variation, true
	}
	return "", false
}
