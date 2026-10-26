package flags

import (
	"context"
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/flags"
)

// AllConfigurations reads a flag's configurations.
type AllConfigurations interface {
	// Configurations returns the flag's configuration in every environment,
	// by the environments' order.
	Configurations(ctx context.Context, flag flags.FlagID) ([]flags.EnvironmentConfiguration, error)
}

// ExperimentHolders reads where an experiment's rules are: the rules
// belong to the flags context, which an experiment's change request is
// based on.
type ExperimentHolders struct{ store AllConfigurations }

// NewExperimentHolders returns the reader of store.
func NewExperimentHolders(store AllConfigurations) *ExperimentHolders {
	return &ExperimentHolders{store: store}
}

// HoldingConfigurations returns the configurations of flag with a rule
// serving the experiment keyed experiment, by the environments' order, read
// in the unit of work ctx carries. An experiment's rules are all its flag's,
// since a rule serving another flag's experiment is refused.
func (h *ExperimentHolders) HoldingConfigurations(ctx context.Context, flag flags.FlagID, experiment string,
) ([]flags.EnvironmentConfiguration, error) {
	all, err := h.store.Configurations(ctx, flag)
	if err != nil {
		return nil, err
	}
	out := []flags.EnvironmentConfiguration{}
	for _, ec := range all {
		if slices.ContainsFunc(ec.Configuration.Rules, func(r flags.Rule) bool {
			return r.Serve.Kind == flags.ServeExperiment && r.Serve.Experiment == experiment
		}) {
			out = append(out, ec)
		}
	}
	return out, nil
}
