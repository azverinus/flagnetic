package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ExperimentRuleStore reads and rewrites the configurations of a flag.
type ExperimentRuleStore interface {
	FlagByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Flag, error)
	// Configurations returns flag's configuration in every environment.
	Configurations(ctx context.Context, flag flags.FlagID) ([]flags.EnvironmentConfiguration, error)
	UpdateConfiguration(ctx context.Context, c flags.Configuration) error
}

// ApplyExperimentDecisionDeps are what ApplyExperimentDecision runs on.
type ApplyExperimentDecisionDeps struct {
	Store    ExperimentRuleStore
	Rulesets Rulesets
	Recorder Recorder
}

// ApplyExperimentDecision rewrites the rules serving an experiment as its
// stop decided.
type ApplyExperimentDecision struct{ d ApplyExperimentDecisionDeps }

// NewApplyExperimentDecision returns the handler.
func NewApplyExperimentDecision(d ApplyExperimentDecisionDeps) *ApplyExperimentDecision {
	return &ApplyExperimentDecision{d: d}
}

// ApplyDecision makes every rule of project's flag keyed flag serving the
// experiment keyed experiment serve the variation winner, or deletes it when
// winner is "", in every environment, as the member by (nil for a project
// token) at now in req. An experiment's rules are all its flag's, since a
// rule serving another flag's experiment is refused.
//
// Each configuration it changes is a configuration write of its own, at its
// next version, recorded as flag.configuration_updated with the experiment
// its stop decided, so a revert finds the version and later readers of the audit
// log see the write as any other; the environment's ruleset moves on with
// it. It runs in the unit of work ctx carries, under the project's lock its
// caller took, so the stop and every rewrite commit together.
func (h *ApplyExperimentDecision) ApplyDecision(ctx context.Context, project workspace.ProjectID, flag, experiment,
	winner string, by *access.MemberID, now time.Time, req governance.Request,
) error {
	f, err := h.d.Store.FlagByKey(ctx, project, flag)
	if err != nil {
		return err
	}
	configurations, err := h.d.Store.Configurations(ctx, f.ID)
	if err != nil {
		return err
	}
	for _, ec := range configurations {
		before := ec.Configuration
		after, changed := before.ApplyDecision(experiment, winner, flags.Write{By: by, Now: now})
		if !changed.Any {
			continue
		}
		if err := h.d.Store.UpdateConfiguration(ctx, after); err != nil {
			return err
		}
		if changed.Ruleset {
			if err := h.d.Rulesets.Environment(ctx, after.Environment); err != nil {
				return err
			}
		}
		values := configurationValues(after)
		values["experiment_stopped"] = experiment
		if err := h.d.Recorder.record(ctx, now, req, governance.Record{
			Project: project, Environment: after.Environment, Action: "flag.configuration_updated", Entity: flagEntity(f),
			Before: configurationValues(before), After: values,
		}); err != nil {
			return err
		}
	}
	return nil
}
