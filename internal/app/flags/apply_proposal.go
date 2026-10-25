package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ApplyProposal writes a flag's configuration in one environment as an
// approved change request proposes it.
type ApplyProposal struct{ d ReplaceConfigurationDeps }

// NewApplyProposal returns the handler.
func NewApplyProposal(d ReplaceConfigurationDeps) *ApplyProposal { return &ApplyProposal{d: d} }

// Apply writes proposed in place of the configuration of project's flag
// keyed flag in the environment keyed environment, based on its version
// baseVersion, in the unit of work ctx carries, which joins the change
// request's application; by is the member applying, nil for a project token
// or the system. It is recorded as a replace of the whole configuration is.
// The proposal keeps the rule identifiers and salts it was given, and is
// checked again as a replace is, since what it names may be gone since it
// was approved; a refusal rolls the application back. Whether the write may
// change a protected environment is the bypass ctx carries, as for every
// write.
func (h *ApplyProposal) Apply(ctx context.Context, project workspace.ProjectID, flag, environment string,
	proposed flags.ConfigurationInput, baseVersion int64, by *access.MemberID, req governance.Request,
) error {
	d := ConfigurationDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	c := ConfigurationCommand{
		Project: project, Flag: flag, Environment: environment, Expected: precondition.On(baseVersion), Caller: by, Request: req,
	}
	_, err := writeConfiguration(ctx, d, c, func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		r, err := referentsOf(ctx, h.d.Store, h.d.Declarations, project, before.Environment)
		if err != nil {
			return configurationChange{}, err
		}
		after, changed, err := before.Apply(f, proposed, r, h.d.Recorder.mint(), flags.Write{By: by, Now: now})
		if err != nil {
			return configurationChange{}, err
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return configurationChange{}, err
		}
		return configurationChange{after: after, changed: changed, action: replacedAction(after, changed)}, nil
	})
	return err
}
