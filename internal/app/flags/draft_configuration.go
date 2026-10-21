package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ConfigurationDrafter reads a flag's configuration and what a configuration
// may name.
type ConfigurationDrafter interface {
	ConfigurationReader
	ReferentReader
}

// DraftConfiguration checks an unsaved configuration of one flag as
// replacing the saved one would, for the delivery context to evaluate it,
// and stores and records nothing.
type DraftConfiguration struct {
	declarations Declarations
	store        ConfigurationDrafter
	ids          *id.Source
}

// NewDraftConfiguration returns the handler, giving a draft's new rules
// identifiers from ids.
func NewDraftConfiguration(declarations Declarations, store ConfigurationDrafter, ids *id.Source) *DraftConfiguration {
	return &DraftConfiguration{declarations: declarations, store: store, ids: ids}
}

// Draft returns in as the configuration of project's flag keyed flag in
// the environment keyed environment it would replace the saved one with,
// or the refusal replacing it would get: not_found for either, archived
// for an archived flag, invalid_configuration with every field to fix. A
// new rule gets an identifier and a new rollout a salt, which no write ever
// sees. It reads in the unit of work or snapshot ctx carries, with no lock:
// nothing is written, so a write committed meanwhile only means the draft
// is checked against what was saved before it.
func (h *DraftConfiguration) Draft(ctx context.Context, project workspace.ProjectID, flag, environment string,
	in flags.ConfigurationInput,
) (flags.Configuration, error) {
	f, saved, err := configurationOf(ctx, h.store, project, flag, environment)
	if err != nil {
		return flags.Configuration{}, err
	}
	if err = f.CanConfigure(); err != nil {
		return flags.Configuration{}, err
	}
	r, err := referentsOf(ctx, h.store, h.declarations, project, saved.Configuration.Environment)
	if err != nil {
		return flags.Configuration{}, err
	}
	// Who wrote the draft and when is never shown: only what it serves is
	// evaluated.
	draft, _, err := saved.Configuration.Replace(f, in, r, mintFrom(h.ids), flags.Write{})
	return draft, err
}
