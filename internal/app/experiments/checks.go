package experiments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CheckInterval is how often the checks run over every running experiment:
// often enough that a broken split is caught within the hour, rarely enough
// that a check reads the rollup's aggregates and not the raw events.
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

// SampleRatioState is the latest sample-ratio check of an experiment in one
// environment, when it ran, and whether a sample-ratio alert is active there.
type SampleRatioState struct {
	experiments.SampleRatio
	CheckedAt time.Time
	Active    bool
}

// CheckStore keeps the checks' results and alerts.
type CheckStore interface {
	// SaveSampleRatio stores r, checked at, as the latest check of
	// experiment in environment.
	SaveSampleRatio(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID,
		r experiments.SampleRatio, at time.Time) error
	// ActiveAlert returns the active alert of kind of experiment in
	// environment, nil for the experiment as a whole, held until the unit
	// of work ends; nil when there is none.
	ActiveAlert(ctx context.Context, experiment experiments.ExperimentID, environment *workspace.EnvironmentID,
		kind experiments.AlertKind) (*experiments.Alert, error)
	// SaveAlert stores a, new or changed.
	SaveAlert(ctx context.Context, a experiments.Alert) error
}

// sampleRatioCheck runs the sample-ratio check of a running experiment in
// the environments holding its rule, in one unit of work with whatever
// started it.
type sampleRatioCheck struct {
	holders  Holders
	counts   FirstExposures
	store    CheckStore
	recorder Recorder
}

// run checks e in every environment holding its rule as of now, req naming
// the request the check runs in.
func (c sampleRatioCheck) run(ctx context.Context, e experiments.Experiment, now time.Time, req governance.Request) error {
	envs, err := c.holders.HoldingEnvironments(ctx, e.Project, e.Key)
	if err != nil {
		return err
	}
	return c.in(ctx, e, envs, now, req)
}

// in checks e in envs at now: it stores each result as the environment's
// latest, raises a sample-ratio alert where none is active and the split is
// out of line, and clears the active one once the split is back in line.
func (c sampleRatioCheck) in(ctx context.Context, e experiments.Experiment, envs []workspace.EnvironmentID, now time.Time,
	req governance.Request,
) error {
	req.Actor = checksActor
	for _, env := range envs {
		counts, err := c.counts.FirstExposureCounts(ctx, env, e.ID)
		if err != nil {
			return err
		}
		r := experiments.CheckSampleRatio(e.Variations, counts)
		if err = c.store.SaveSampleRatio(ctx, e.ID, env, r, now); err != nil {
			return err
		}
		active, err := c.store.ActiveAlert(ctx, e.ID, &env, experiments.AlertKindSampleRatio)
		if err != nil {
			return err
		}
		switch r.AlertChange(active != nil) {
		case experiments.AlertRaise:
			a := experiments.RaiseAlert(id.New[experiments.Alert](c.recorder.ids), e.ID, &env,
				experiments.AlertKindSampleRatio, now, experiments.AlertDetails{SampleRatio: &r})
			if err = c.store.SaveAlert(ctx, a); err != nil {
				return err
			}
			err = c.recorder.record(ctx, now, req, alertRecord(e, a, "experiment.alert_raised", nil))
		case experiments.AlertClear:
			cleared := active.Clear(now)
			if err = c.store.SaveAlert(ctx, cleared); err != nil {
				return err
			}
			err = c.recorder.record(ctx, now, req, alertRecord(e, cleared, "experiment.alert_cleared", active))
		case experiments.AlertKeep:
		}
		if err != nil {
			return err
		}
	}
	return nil
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
	details := map[string]any{"sample_ratio": nil}
	if r := a.Details.SampleRatio; r != nil {
		details["sample_ratio"] = sampleRatioValues(*r)
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
	Projects       ProjectLocker
	Store          Reader
	Holders        Holders
	FirstExposures FirstExposures
	Checks         CheckStore
	Recorder       Recorder
}

// RunChecks runs the checks of every running experiment once.
type RunChecks struct{ d RunChecksDeps }

// NewRunChecks returns the handler.
func NewRunChecks(d RunChecksDeps) *RunChecks { return &RunChecks{d: d} }

// Handle checks every running experiment, each in a unit of work of its own,
// so one that fails or runs out of time rolls back alone and the others are
// still checked. A replica finding another one checking does nothing: a
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
	check := sampleRatioCheck{holders: h.d.Holders, counts: h.d.FirstExposures, store: h.d.Checks, recorder: h.d.Recorder}
	var failed []error
	for _, e := range running {
		if err := h.one(ctx, check, e); err != nil {
			if ctx.Err() != nil {
				return errors.Join(append(failed, err)...)
			}
			failed = append(failed, fmt.Errorf("checking experiment %s: %w", e.ID, err))
		}
	}
	return errors.Join(failed...)
}

// one checks e under its project's row lock, the lock a stop takes too: an
// experiment stopped since the round listed it is left to the check its stop
// took, which stays its last.
func (h *RunChecks) one(ctx context.Context, check sampleRatioCheck, listed experiments.Experiment) error {
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
