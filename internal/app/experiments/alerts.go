package experiments

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// AlertView is an alert as it is read, with the key of its environment, nil
// for one about the experiment as a whole.
type AlertView struct {
	experiments.Alert
	EnvironmentKey *string
}

// AlertReader reads an experiment's alerts.
type AlertReader interface {
	Reader
	// Alerts returns experiment's alerts, the most recently raised first;
	// active, when not nil, keeps only the active (true) or the cleared
	// (false) ones.
	Alerts(ctx context.Context, experiment experiments.ExperimentID, active *bool) ([]AlertView, error)
}

// ListAlerts lists an experiment's alerts.
type ListAlerts struct{ store AlertReader }

// NewListAlerts returns the handler.
func NewListAlerts(store AlertReader) *ListAlerts { return &ListAlerts{store: store} }

// Handle returns the alerts of project's experiment keyed key, or not_found.
func (h *ListAlerts) Handle(ctx context.Context, project workspace.ProjectID, key string, active *bool) ([]AlertView, error) {
	e, err := h.store.ExperimentByKey(ctx, project, key)
	if err != nil {
		return nil, experimentNotFound(err)
	}
	return h.store.Alerts(ctx, e.ID, active)
}

// AlertStore reads and changes one alert.
type AlertStore interface {
	Reader
	// AlertOf returns experiment's alert alert, held until the unit of
	// work ends, or ErrNotFound.
	AlertOf(ctx context.Context, experiment experiments.ExperimentID, alert experiments.AlertID) (AlertView, error)
	// SaveAlert stores a, new or changed.
	SaveAlert(ctx context.Context, a experiments.Alert) error
}

// AcknowledgeAlertDeps are what AcknowledgeAlert runs on.
type AcknowledgeAlertDeps struct {
	Tx       Transactor
	Store    AlertStore
	Recorder Recorder
}

// AcknowledgeAlert hides an alert until it clears.
type AcknowledgeAlert struct{ d AcknowledgeAlertDeps }

// NewAcknowledgeAlert returns the handler.
func NewAcknowledgeAlert(d AcknowledgeAlertDeps) *AcknowledgeAlert { return &AcknowledgeAlert{d: d} }

// AcknowledgeAlertCommand acknowledges the alert Alert of the experiment
// keyed Key.
type AcknowledgeAlertCommand struct {
	Project workspace.ProjectID
	Key     string
	Alert   experiments.AlertID
	Request governance.Request
}

// errAlertNotFound answers an alert the experiment does not have.
var errAlertNotFound = apperrors.NotFound("not_found", "the experiment has no alert with this identifier")

// Handle acknowledges the alert and returns it. The first acknowledgement is
// recorded and kept; a later one changes and records nothing. An alert
// carries no version: acknowledging is the same whoever does it first, so
// no write is lost by two members doing it at once.
func (h *AcknowledgeAlert) Handle(ctx context.Context, cmd AcknowledgeAlertCommand) (AlertView, error) {
	now := h.d.Recorder.now()
	var out AlertView
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		e, err := h.d.Store.ExperimentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		before, err := h.d.Store.AlertOf(ctx, e.ID, cmd.Alert)
		if errors.Is(err, ErrNotFound) {
			return errAlertNotFound
		}
		if err != nil {
			return err
		}
		after, changed := before.Acknowledge(now)
		out = AlertView{Alert: after, EnvironmentKey: before.EnvironmentKey}
		if !changed {
			return nil
		}
		if err = h.d.Store.SaveAlert(ctx, after); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, alertRecord(e, after, "experiment.alert_acknowledged", &before.Alert))
	})
	return out, err
}

// SampleRatioReader reads the latest sample-ratio checks.
type SampleRatioReader interface {
	Reader
	// EnvironmentByKey returns project's environment keyed key, or
	// ErrNotFound.
	EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, error)
	// SampleRatioOf returns the latest sample-ratio check of experiment in
	// environment, nil before the first.
	SampleRatioOf(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID) (*SampleRatioState, error)
}

// GetSampleRatio reads an experiment's latest sample-ratio check in one
// environment.
type GetSampleRatio struct{ store SampleRatioReader }

// NewGetSampleRatio returns the handler.
func NewGetSampleRatio(store SampleRatioReader) *GetSampleRatio { return &GetSampleRatio{store: store} }

var errEnvironmentNotFound = apperrors.NotFound("not_found", "the project has no environment with this key")

// Handle returns the latest check of project's experiment keyed key in the
// environment keyed environment, nil before the first, or not_found.
func (h *GetSampleRatio) Handle(ctx context.Context, project workspace.ProjectID, key, environment string) (*SampleRatioState, error) {
	e, err := h.store.ExperimentByKey(ctx, project, key)
	if err != nil {
		return nil, experimentNotFound(err)
	}
	env, err := h.store.EnvironmentByKey(ctx, project, environment)
	if errors.Is(err, ErrNotFound) {
		return nil, errEnvironmentNotFound
	}
	if err != nil {
		return nil, err
	}
	return h.store.SampleRatioOf(ctx, e.ID, env)
}
