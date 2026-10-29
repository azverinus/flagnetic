package experiments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CheckInterval is how often the checks run over every running experiment:
// often enough that a broken split is caught within the hour, rarely enough
// that the round stays small beside the traffic it judges.
const CheckInterval = 10 * time.Minute

// experimentCheckTimeout bounds the checks of one experiment, so one that
// hangs rolls back alone and leaves the round time for the others.
const experimentCheckTimeout = 30 * time.Second

// checksActor is who raises and clears alerts: the server itself, whether a
// round of the checks or a stop took the check.
var checksActor = governance.Actor{Kind: governance.ActorSystem, ID: "experiment_checks", Name: "experiment checks"}

// FirstExposures reads what the exposure rollup counted of an experiment.
type FirstExposures interface {
	// FirstExposureCounts returns the experiment's first exposures in
	// environment, summed over every day, by variation.
	FirstExposureCounts(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID) (map[string]int64, error)
}

// ExposureActivity reads what the exposures of an experiment came to beyond
// the first exposures.
type ExposureActivity interface {
	// ConflictTotals returns how many exposures of experiment in
	// environment named another variation than their unit's first one,
	// and how many units had one.
	ConflictTotals(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID) (total, units int64, err error)
	// LastExposureAt returns when the server last received, after after
	// and at or before at, an exposure of experiment in environment that
	// no override decided; nil when it did not. A zero after sets no lower
	// bound.
	LastExposureAt(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID, after, at time.Time,
	) (*time.Time, error)
}

// SampleRatioState is the latest sample-ratio check of an experiment in one
// environment, when it ran, whether a sample-ratio alert is active there, and
// whether it is the check the experiment keeps: the stop's own is not, until
// the final check completes it.
type SampleRatioState struct {
	experiments.SampleRatio
	CheckedAt time.Time
	Active    bool
	Final     bool
}

// CheckStore keeps the checks' results and alerts.
type CheckStore interface {
	// SaveSampleRatio stores r, checked at, as the latest check of
	// experiment in environment, final when nothing is left to wait for.
	SaveSampleRatio(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID,
		r experiments.SampleRatio, at time.Time, final bool) error
	// ActiveAlert returns the active alert of kind of experiment in
	// environment, nil for the experiment as a whole, held until the unit
	// of work ends; nil when there is none.
	ActiveAlert(ctx context.Context, experiment experiments.ExperimentID, environment *workspace.EnvironmentID,
		kind experiments.AlertKind) (*experiments.Alert, error)
	// SaveAlert stores a, new or changed.
	SaveAlert(ctx context.Context, a experiments.Alert) error
	// ActiveAlerts returns the active alerts of kind of experiment, in
	// every environment, held until the unit of work ends.
	ActiveAlerts(ctx context.Context, experiment experiments.ExperimentID, kind experiments.AlertKind) ([]experiments.Alert, error)
}

// checker runs the checks of an experiment in one unit of work with
// whatever started them, as the system: a round of the checks or a stop.
type checker struct {
	holders   Holders
	counts    FirstExposures
	exposures ExposureActivity
	store     CheckStore
	recorder  Recorder
}

// run takes every check of the running experiment e at now: the
// sample-ratio, no-exposures and conflicts checks in each environment
// holding its rule, then the past-expected-end check of the experiment as a
// whole. An environment no longer holding the rule awaits no exposure, so
// its no-exposures alert clears; its other alerts stay, as nothing checks
// them there any more.
func (c checker) run(ctx context.Context, e experiments.Experiment, now time.Time, req governance.Request) error {
	req.Actor = checksActor
	envs, err := c.holders.HoldingEnvironments(ctx, e.Project, e.Key)
	if err != nil {
		return err
	}
	counts := func(env workspace.EnvironmentID) (map[string]int64, error) {
		return c.counts.FirstExposureCounts(ctx, env, e.ID)
	}
	if err = c.sampleRatio(ctx, e, envs, now, true, counts, req); err != nil {
		return err
	}
	for _, env := range envs {
		if err = c.noExposures(ctx, e, env, now, req); err != nil {
			return err
		}
		if err = c.conflicts(ctx, e, env, now, req); err != nil {
			return err
		}
	}
	silent, err := c.store.ActiveAlerts(ctx, e.ID, experiments.AlertKindNoExposures)
	if err != nil {
		return err
	}
	for _, a := range silent {
		if a.Environment != nil && !slices.Contains(envs, *a.Environment) {
			if err = c.clear(ctx, e, a, now, req); err != nil {
				return err
			}
		}
	}
	active, err := c.store.ActiveAlert(ctx, e.ID, nil, experiments.AlertKindPastExpectedEnd)
	if err != nil {
		return err
	}
	details := e.PastExpectedEndDetails()
	return c.apply(ctx, e, nil, experiments.AlertKindPastExpectedEnd, active,
		experiments.ConditionChange(e.PastExpectedEnd(now), active != nil),
		experiments.AlertDetails{PastExpectedEnd: &details}, now, req)
}

// sampleRatio checks e in envs at now over the first exposures counts reads
// in each: it stores each result as the environment's latest, final or not,
// raises a sample-ratio alert where none is active and the split is out of
// line, and clears the active one once the split is back in line.
func (c checker) sampleRatio(ctx context.Context, e experiments.Experiment, envs []workspace.EnvironmentID, now time.Time, final bool,
	counts func(workspace.EnvironmentID) (map[string]int64, error), req governance.Request,
) error {
	req.Actor = checksActor
	for _, env := range envs {
		first, err := counts(env)
		if err != nil {
			return err
		}
		r := experiments.CheckSampleRatio(e.Variations, first)
		if err = c.store.SaveSampleRatio(ctx, e.ID, env, r, now, final); err != nil {
			return err
		}
		active, err := c.store.ActiveAlert(ctx, e.ID, &env, experiments.AlertKindSampleRatio)
		if err != nil {
			return err
		}
		err = c.apply(ctx, e, &env, experiments.AlertKindSampleRatio, active, r.AlertChange(active != nil),
			experiments.AlertDetails{SampleRatio: &r}, now, req)
		if err != nil {
			return err
		}
	}
	return nil
}

// noExposures raises the no-exposures alert of e in env once a day passed
// without an exposure there, and clears it with the next one.
func (c checker) noExposures(ctx context.Context, e experiments.Experiment, env workspace.EnvironmentID, now time.Time,
	req governance.Request,
) error {
	if e.StartedAt == nil {
		return nil
	}
	// The condition needs the last day alone: an exposure within it keeps
	// the alert down, and without one the alert is due once the experiment
	// ran a day. Reading only that day keeps the round from walking a busy
	// experiment's whole history every time in an environment that stays
	// silent; the latest exposure before it is read for the details of a
	// raise alone.
	last, err := c.exposures.LastExposureAt(ctx, env, e.ID, now.Add(-experiments.NoExposuresAfter), now)
	if err != nil {
		return err
	}
	found, holds := experiments.CheckNoExposures(*e.StartedAt, last, now)
	active, err := c.store.ActiveAlert(ctx, e.ID, &env, experiments.AlertKindNoExposures)
	if err != nil {
		return err
	}
	if holds && active == nil {
		if last, err = c.exposures.LastExposureAt(ctx, env, e.ID, time.Time{}, now); err != nil {
			return err
		}
		found, holds = experiments.CheckNoExposures(*e.StartedAt, last, now)
	}
	return c.apply(ctx, e, &env, experiments.AlertKindNoExposures, active, experiments.ConditionChange(holds, active != nil),
		experiments.AlertDetails{NoExposures: &found}, now, req)
}

// conflicts raises the conflicts alert of e in env once its conflicts reach
// the threshold, and clears it once they fall below it.
func (c checker) conflicts(ctx context.Context, e experiments.Experiment, env workspace.EnvironmentID, now time.Time,
	req governance.Request,
) error {
	total, units, err := c.exposures.ConflictTotals(ctx, env, e.ID)
	if err != nil {
		return err
	}
	counts, err := c.counts.FirstExposureCounts(ctx, env, e.ID)
	if err != nil {
		return err
	}
	var first int64
	for _, n := range counts {
		first += n
	}
	found := experiments.CheckExposureConflicts(total, units, first)
	active, err := c.store.ActiveAlert(ctx, e.ID, &env, experiments.AlertKindExposureConflicts)
	if err != nil {
		return err
	}
	return c.apply(ctx, e, &env, experiments.AlertKindExposureConflicts, active,
		experiments.ConditionChange(found.Exceeded(), active != nil), experiments.AlertDetails{ExposureConflicts: &found}, now, req)
}

// atStop clears what a stop ends: the experiment is no longer past its
// expected end, and awaits no exposure. The sample-ratio and conflicts
// alerts stay, since they are what its exposures came to.
func (c checker) atStop(ctx context.Context, e experiments.Experiment, now time.Time, req governance.Request) error {
	req.Actor = checksActor
	for _, kind := range []experiments.AlertKind{experiments.AlertKindPastExpectedEnd, experiments.AlertKindNoExposures} {
		active, err := c.store.ActiveAlerts(ctx, e.ID, kind)
		if err != nil {
			return err
		}
		for _, a := range active {
			if err = c.clear(ctx, e, a, now, req); err != nil {
				return err
			}
		}
	}
	return nil
}

// apply makes change to the alert of kind of e in env, nil for the
// experiment as a whole, active being the active one, nil when none is: a
// raise stores a new alert with details, a clear clears the active one.
func (c checker) apply(ctx context.Context, e experiments.Experiment, env *workspace.EnvironmentID, kind experiments.AlertKind,
	active *experiments.Alert, change experiments.AlertChange, details experiments.AlertDetails, now time.Time, req governance.Request,
) error {
	switch change {
	case experiments.AlertRaise:
		a := experiments.RaiseAlert(id.New[experiments.Alert](c.recorder.ids), e.ID, env, kind, now, details)
		if err := c.store.SaveAlert(ctx, a); err != nil {
			return err
		}
		return c.recorder.record(ctx, now, req, alertRecord(e, a, "experiment.alert_raised", nil))
	case experiments.AlertClear:
		return c.clear(ctx, e, *active, now, req)
	case experiments.AlertKeep:
	}
	return nil
}

func (c checker) clear(ctx context.Context, e experiments.Experiment, active experiments.Alert, now time.Time, req governance.Request) error {
	cleared := active.Clear(now)
	if err := c.store.SaveAlert(ctx, cleared); err != nil {
		return err
	}
	return c.recorder.record(ctx, now, req, alertRecord(e, cleared, "experiment.alert_cleared", &active))
}

// alertRecord is the audit record of action on alert a of e, before being the
// alert as it was, nil for a new one.
func alertRecord(e experiments.Experiment, a experiments.Alert, action string, before *experiments.Alert) governance.Record {
	r := governance.Record{Project: e.Project, Action: action, Entity: experimentEntity(e), After: alertValues(a)}
	if a.Environment != nil {
		r.Environment = *a.Environment
	}
	if before != nil {
		r.Before = alertValues(*before)
	}
	return r
}

// alertValues are what an audit entry records of an alert.
func alertValues(a experiments.Alert) map[string]any {
	var env any
	if a.Environment != nil {
		env = a.Environment.String()
	}
	details := map[string]any{"sample_ratio": nil, "no_exposures": nil, "exposure_conflicts": nil, "past_expected_end": nil}
	if r := a.Details.SampleRatio; r != nil {
		details["sample_ratio"] = sampleRatioValues(*r)
	}
	if r := a.Details.NoExposures; r != nil {
		details["no_exposures"] = map[string]any{"last_exposure_at": r.LastExposureAt, "since": r.Since}
	}
	if r := a.Details.ExposureConflicts; r != nil {
		details["exposure_conflicts"] = map[string]any{
			"conflicts": r.Conflicts, "conflicting_units": r.ConflictingUnits, "first_exposures": r.FirstExposures,
			"threshold": r.Threshold,
		}
	}
	if r := a.Details.PastExpectedEnd; r != nil {
		details["past_expected_end"] = map[string]any{"expected_end_at": r.ExpectedEndAt.Format(time.DateOnly)}
	}
	return map[string]any{
		"id": a.ID.String(), "kind": string(a.Kind), "environment": env, "raised_at": a.RaisedAt,
		"cleared_at": a.ClearedAt, "acknowledged_at": a.AcknowledgedAt, "details": details,
	}
}

func sampleRatioValues(r experiments.SampleRatio) map[string]any {
	variations := make([]map[string]any, len(r.Variations))
	for i, v := range r.Variations {
		variations[i] = map[string]any{
			"variation": v.Variation, "observed": v.Observed, "expected": v.Expected,
			"observed_share": v.ObservedShare, "expected_share": v.ExpectedShare,
		}
	}
	return map[string]any{"n": r.N, "chi_square": r.ChiSquare, "p_value": r.PValue, "variations": variations}
}

// RunningExperiments lists what the checks run over.
type RunningExperiments interface {
	// RunningExperiments returns the running experiments of every
	// project.
	RunningExperiments(ctx context.Context) ([]experiments.Experiment, error)
}

// PendingFinalChecks lists the experiments whose stop took a sample-ratio
// check that is not final yet.
type PendingFinalChecks interface {
	// PendingFinalExperiments returns the stopped or archived experiments
	// of every project with a sample-ratio result that is not final: an
	// experiment archived before its final check is still owed it.
	PendingFinalExperiments(ctx context.Context) ([]experiments.Experiment, error)
	// PendingFinalEnvironments returns the environments in which
	// experiment's sample-ratio result is not final: the ones its stop
	// checked.
	PendingFinalEnvironments(ctx context.Context, experiment experiments.ExperimentID) ([]workspace.EnvironmentID, error)
}

// FinalExposures reads the exposures a stopped experiment's final check is
// over.
type FinalExposures interface {
	// ExposuresReadThrough reports whether the exposure rollup has read
	// every exposure received at or before at, so that nothing accepted
	// by then still waits for it.
	ExposuresReadThrough(ctx context.Context, at time.Time) (bool, error)
	// FirstExposureCountsReceivedBy returns the first exposures of
	// experiment in environment among the exposures received at or before
	// by, by variation, whatever the rollup has read since.
	FirstExposureCountsReceivedBy(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID,
		by time.Time) (map[string]int64, error)
}

// ChecksLock keeps two replicas from running the checks at once.
type ChecksLock interface {
	// TryLockChecks takes the checks' lock for one round; held is false
	// when another replica holds it. release lets it go.
	TryLockChecks(ctx context.Context) (release func(), held bool, err error)
}

// RunChecksDeps are what RunChecks runs on.
type RunChecksDeps struct {
	Tx             Transactor
	Lock           ChecksLock
	Running        RunningExperiments
	Pending        PendingFinalChecks
	Final          FinalExposures
	Projects       ProjectLocker
	Store          Reader
	Holders        Holders
	FirstExposures FirstExposures
	Exposures      ExposureActivity
	Checks         CheckStore
	Recorder       Recorder
}

// RunChecks runs the checks of every running experiment once, and takes the
// final sample-ratio check of every stopped one that waits for it.
type RunChecks struct{ d RunChecksDeps }

// NewRunChecks returns the handler.
func NewRunChecks(d RunChecksDeps) *RunChecks { return &RunChecks{d: d} }

// Handle checks every running experiment, then completes the sample-ratio
// check of every stopped one that is pending, each in a unit of work of its
// own, so one that fails or runs out of time rolls back alone and the others
// are still checked. A replica finding another one checking does nothing: a
// round is for the whole instance, and running it twice would only repeat
// it.
func (h *RunChecks) Handle(ctx context.Context) error {
	release, held, err := h.d.Lock.TryLockChecks(ctx)
	if err != nil || !held {
		return err
	}
	defer release()
	running, err := h.d.Running.RunningExperiments(ctx)
	if err != nil {
		return err
	}
	check := checker{
		holders: h.d.Holders, counts: h.d.FirstExposures, exposures: h.d.Exposures, store: h.d.Checks, recorder: h.d.Recorder,
	}
	pending, err := h.d.Pending.PendingFinalExperiments(ctx)
	if err != nil {
		return err
	}
	var failed []error
	for _, e := range running {
		if err := h.one(ctx, check, e); err != nil {
			if ctx.Err() != nil {
				return errors.Join(append(failed, err)...)
			}
			failed = append(failed, fmt.Errorf("checking experiment %s: %w", e.ID, err))
		}
	}
	for _, e := range pending {
		if err := h.final(ctx, check, e); err != nil {
			if ctx.Err() != nil {
				return errors.Join(append(failed, err)...)
			}
			failed = append(failed, fmt.Errorf("completing the sample-ratio check of experiment %s: %w", e.ID, err))
		}
	}
	return errors.Join(failed...)
}

// one checks e under its project's row lock, the lock a stop takes too: an
// experiment stopped since the round listed it is left to the check its stop
// took, which stays its last.
func (h *RunChecks) one(ctx context.Context, check checker, listed experiments.Experiment) error {
	ctx, cancel := context.WithTimeout(ctx, experimentCheckTimeout)
	defer cancel()
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		err := h.d.Projects.LockProject(ctx, listed.Project)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		e, err := h.d.Store.ExperimentByKey(ctx, listed.Project, listed.Key)
		if errors.Is(err, ErrNotFound) || (err == nil && (e.ID != listed.ID || e.Status != experiments.StatusRunning)) {
			return nil
		}
		if err != nil {
			return err
		}
		return check.run(ctx, e, now, governance.Request{})
	})
}

// final completes the sample-ratio check of the stopped experiment listed,
// under its project's row lock, once the exposure rollup has read every
// exposure received at or before the stop. The check runs over exactly those
// exposures, read from the raw rows: the rollup's aggregates also hold what
// was received after the stop by now, which stays out. It runs in the
// environments the stop checked, is stored final at the round's time, and
// raises or clears the sample-ratio alert as a running check does. Before
// the rollup has caught up the stop's result stays as it is, for the next
// round.
func (h *RunChecks) final(ctx context.Context, check checker, listed experiments.Experiment) error {
	ctx, cancel := context.WithTimeout(ctx, experimentCheckTimeout)
	defer cancel()
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		err := h.d.Projects.LockProject(ctx, listed.Project)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		e, err := h.d.Store.ExperimentByKey(ctx, listed.Project, listed.Key)
		if errors.Is(err, ErrNotFound) || (err == nil && (e.ID != listed.ID || e.StoppedAt == nil)) {
			return nil
		}
		if err != nil {
			return err
		}
		envs, err := h.d.Pending.PendingFinalEnvironments(ctx, e.ID)
		if err != nil || len(envs) == 0 {
			return err
		}
		read, err := h.d.Final.ExposuresReadThrough(ctx, *e.StoppedAt)
		if err != nil || !read {
			return err
		}
		counts := func(env workspace.EnvironmentID) (map[string]int64, error) {
			return h.d.Final.FirstExposureCountsReceivedBy(ctx, env, e.ID, *e.StoppedAt)
		}
		return check.sampleRatio(ctx, e, envs, now, true, counts, governance.Request{})
	})
}
