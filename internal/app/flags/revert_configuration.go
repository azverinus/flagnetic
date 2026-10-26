package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ConfigurationHistory finds the configurations the audit log recorded.
type ConfigurationHistory interface {
	// RecordedVersion returns the values the newest entry of project
	// recording entity in environment holds for it at version, before or
	// after its change, and whether an entry does.
	RecordedVersion(ctx context.Context, project workspace.ProjectID, entity governance.Entity,
		environment workspace.EnvironmentID, version int64) ([]byte, bool, error)
}

// RevertConfigurationDeps are what RevertConfiguration runs on.
type RevertConfigurationDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Declarations Declarations
	Store        ConfigurationReplacer
	History      ConfigurationHistory
	Rulesets     Rulesets
	Recorder     Recorder
}

// RevertConfiguration writes a flag's configuration in one environment again
// as an earlier version of it was.
type RevertConfiguration struct{ d RevertConfigurationDeps }

// NewRevertConfiguration returns the handler.
func NewRevertConfiguration(d RevertConfigurationDeps) *RevertConfiguration {
	return &RevertConfiguration{d: d}
}

// RevertConfigurationCommand reverts the configuration Configuration names
// to its version Version.
type RevertConfigurationCommand struct {
	Configuration ConfigurationCommand
	Version       int64
}

// errVersionNotFound answers a version the configuration's history in its
// environment does not hold.
var errVersionNotFound = apperrors.NotFound("version_not_found",
	"the flag's configuration in this environment has no such version in its history")

// Handle reverts the configuration and returns it as it is after. The
// version is looked up only among the audit entries of this flag, by its
// identifier, in this environment, so a flag or environment created again
// with a key once used never inherits an older one's history. The recorded
// document is written whole and validated as a replace is, against what
// the project has now; then the version the write was based on is judged.
func (h *RevertConfiguration) Handle(ctx context.Context, cmd RevertConfigurationCommand) (flags.EnvironmentConfiguration, error) {
	d := ConfigurationDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	c := cmd.Configuration
	return writeConfiguration(ctx, d, c, func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		recorded, err := h.recorded(ctx, c.Project, f, before, cmd.Version)
		if err != nil {
			return configurationChange{}, err
		}
		r, err := referentsOf(ctx, h.d.Store, h.d.Declarations, c.Project, before.Environment)
		if err != nil {
			return configurationChange{}, err
		}
		after, changed, err := before.Revert(f, recorded, r, flags.Write{By: c.Caller, Now: now})
		if err != nil {
			return configurationChange{}, err
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return configurationChange{}, err
		}
		return configurationChange{after: after, changed: changed, action: "flag.reverted",
			extra: map[string]any{"reverted_to": cmd.Version}}, nil
	})
}

// ProposeRevert returns project's flag keyed flag, its saved configuration
// in the environment keyed environment, and what reverting it to version
// would write, with the refusals a revert gets: not_found, archived,
// version_not_found, experiment_rules_changed, invalid_configuration. It
// writes nothing: a change request proposes the revert, and fills what it
// proposes from it.
func (h *RevertConfiguration) ProposeRevert(ctx context.Context, project workspace.ProjectID, flag, environment string,
	version int64,
) (flags.Flag, flags.EnvironmentConfiguration, flags.Configuration, error) {
	f, saved, err := configurationOf(ctx, h.d.Store, project, flag, environment)
	if err != nil {
		return f, saved, flags.Configuration{}, err
	}
	if err = f.CanConfigure(); err != nil {
		return f, saved, flags.Configuration{}, err
	}
	recorded, err := h.recorded(ctx, project, f, saved.Configuration, version)
	if err != nil {
		return f, saved, flags.Configuration{}, err
	}
	r, err := referentsOf(ctx, h.d.Store, h.d.Declarations, project, saved.Configuration.Environment)
	if err != nil {
		return f, saved, flags.Configuration{}, err
	}
	proposed, _, err := saved.Configuration.Revert(f, recorded, r, flags.Write{})
	return f, saved, proposed, err
}

// ApplyRevert reverts the configuration of project's flag keyed flag in the
// environment keyed environment to version, based on its version
// baseVersion, in the unit of work ctx carries, which joins the change
// request's application: as a direct revert is, written whole and audited
// flag.reverted. by is the member applying, nil for a project token or the
// system; req is who makes the change. A refusal rolls the application
// back.
func (h *RevertConfiguration) ApplyRevert(ctx context.Context, project workspace.ProjectID, flag, environment string,
	version, baseVersion int64, by *access.MemberID, req governance.Request,
) error {
	_, err := h.Handle(ctx, RevertConfigurationCommand{Version: version, Configuration: ConfigurationCommand{
		Project: project, Flag: flag, Environment: environment, Expected: precondition.On(baseVersion), Caller: by, Request: req,
	}})
	return err
}

// recorded returns f's configuration current was at version: current itself
// at its own version, otherwise as the audit log recorded it.
func (h *RevertConfiguration) recorded(ctx context.Context, project workspace.ProjectID, f flags.Flag,
	current flags.Configuration, version int64,
) (flags.Configuration, error) {
	if version == current.Version {
		return current, nil
	}
	values, ok, err := h.d.History.RecordedVersion(ctx, project, flagEntity(f), current.Environment, version)
	if err != nil {
		return flags.Configuration{}, err
	}
	if !ok {
		return flags.Configuration{}, errVersionNotFound
	}
	recorded, err := configurationOfValues(values)
	if err != nil {
		return flags.Configuration{}, fmt.Errorf("reading version %d of %s's configuration: %w", version, f.Key, err)
	}
	recorded.Flag, recorded.Environment = f.ID, current.Environment
	return recorded, nil
}

// configurationRecord reads what configurationValues writes into an audit
// entry.
type configurationRecord struct {
	On            bool   `json:"on"`
	OffVariation  string `json:"off_variation"`
	Fallthrough   string `json:"fallthrough"`
	Prerequisites []struct {
		Flag      string `json:"flag"`
		Variation string `json:"variation"`
	} `json:"prerequisites"`
	Overrides []struct {
		UnitType  string           `json:"unit_type"`
		UnitID    string           `json:"unit_id"`
		Variation string           `json:"variation"`
		Note      string           `json:"note"`
		AddedBy   *access.MemberID `json:"added_by"`
	} `json:"overrides"`
	Rules []struct {
		ID          flags.RuleID `json:"id"`
		Description string       `json:"description"`
		Conditions  []struct {
			Kind      string `json:"kind"`
			Attribute string `json:"attribute"`
			Op        string `json:"op"`
			Value     any    `json:"value"`
			Segment   string `json:"segment"`
			Flag      string `json:"flag"`
			Variation string `json:"variation"`
		} `json:"conditions"`
		Serve struct {
			Kind       string `json:"kind"`
			Variation  string `json:"variation"`
			Experiment string `json:"experiment"`
			UnitType   string `json:"unit_type"`
			Percentage int    `json:"percentage"`
			Salt       string `json:"salt"`
		} `json:"serve"`
	} `json:"rules"`
}

// configurationOfValues returns the configuration an audit entry recorded
// as values.
func configurationOfValues(values []byte) (flags.Configuration, error) {
	var rec configurationRecord
	if err := json.Unmarshal(values, &rec); err != nil {
		return flags.Configuration{}, err
	}
	c := flags.Configuration{
		On: rec.On, OffVariation: rec.OffVariation, Fallthrough: rec.Fallthrough,
		Prerequisites: make([]flags.Prerequisite, len(rec.Prerequisites)), Overrides: make([]flags.Override, len(rec.Overrides)),
		Rules: make([]flags.Rule, len(rec.Rules)),
	}
	for i, p := range rec.Prerequisites {
		c.Prerequisites[i] = flags.Prerequisite(p)
	}
	for i, o := range rec.Overrides {
		c.Overrides[i] = flags.Override(o)
	}
	for i, r := range rec.Rules {
		conditions := make([]flags.Condition, len(r.Conditions))
		for j, cond := range r.Conditions {
			conditions[j] = flags.Condition(cond)
		}
		c.Rules[i] = flags.Rule{ID: r.ID, Description: r.Description, Conditions: conditions, Serve: flags.Serve(r.Serve)}
	}
	return c, nil
}
