package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// GetFlagConfiguration answers a flag's configuration in one environment.
func (a *api) GetFlagConfiguration(ctx context.Context, req GetFlagConfigurationRequestObject) (GetFlagConfigurationResponseObject, error) {
	ec, err := a.h.GetFlagConfiguration.Handle(ctx, projectID(ctx), req.Flag, req.Environment)
	if err != nil {
		return nil, err
	}
	return GetFlagConfiguration200JSONResponse{
		Body: configurationOut(ec), Headers: GetFlagConfiguration200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// PutFlagConfiguration replaces a flag's configuration in one environment.
func (a *api) PutFlagConfiguration(ctx context.Context, req PutFlagConfigurationRequestObject) (PutFlagConfigurationResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ec, err := a.h.ReplaceConfiguration.Handle(ctx, appflags.ReplaceConfigurationCommand{Configuration: cmd, Input: configurationIn(*req.Body)})
	if err != nil {
		return nil, err
	}
	return PutFlagConfiguration200JSONResponse{
		Body: configurationOut(ec), Headers: PutFlagConfiguration200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// KillFlag switches a flag off in one environment with the kill switch.
func (a *api) KillFlag(ctx context.Context, req KillFlagRequestObject) (KillFlagResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, nil)
	if err != nil {
		return nil, err
	}
	var reason *string
	if req.Body != nil {
		reason = req.Body.Reason
	}
	ec, killed, err := a.h.KillFlag.Handle(ctx, appflags.KillFlagCommand{Configuration: cmd, Reason: reason})
	if err != nil {
		return nil, err
	}
	return KillFlag200JSONResponse{
		Body:    KillResult{Changed: killed, Configuration: configurationOut(ec)},
		Headers: KillFlag200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// PutOverride pins a variation for one unit.
func (a *api) PutOverride(ctx context.Context, req PutOverrideRequestObject) (PutOverrideResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, nil)
	if err != nil {
		return nil, err
	}
	ec, err := a.h.SetOverride.Handle(ctx, appflags.SetOverrideCommand{Configuration: cmd, Override: flags.OverrideInput{
		UnitType: req.UnitType, UnitID: req.UnitId, Variation: req.Body.Variation, Note: req.Body.Note,
	}})
	if err != nil {
		return nil, err
	}
	return PutOverride200JSONResponse{
		Body: configurationOut(ec), Headers: PutOverride200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// DeleteOverride removes the override of one unit.
func (a *api) DeleteOverride(ctx context.Context, req DeleteOverrideRequestObject) (DeleteOverrideResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, nil)
	if err != nil {
		return nil, err
	}
	ec, err := a.h.RemoveOverride.Handle(ctx, appflags.RemoveOverrideCommand{Configuration: cmd, UnitType: req.UnitType, UnitID: req.UnitId})
	if err != nil {
		return nil, err
	}
	return DeleteOverride200JSONResponse{
		Body: configurationOut(ec), Headers: DeleteOverride200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// RegenerateRolloutSalt gives a rollout a new salt.
func (a *api) RegenerateRolloutSalt(ctx context.Context, req RegenerateRolloutSaltRequestObject) (RegenerateRolloutSaltResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	rule, err := id.Parse[flags.Rule](req.Rule)
	if err != nil {
		// What is not an identifier names no rule of any configuration.
		return nil, flags.ErrNoRule
	}
	ec, err := a.h.RegenerateSalt.Handle(ctx, appflags.RegenerateSaltCommand{Configuration: cmd, Rule: rule})
	if err != nil {
		return nil, err
	}
	return RegenerateRolloutSalt200JSONResponse{
		Body: configurationOut(ec), Headers: RegenerateRolloutSalt200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// RevertFlagConfiguration writes a flag's configuration in one environment
// again as an earlier version of it was.
func (a *api) RevertFlagConfiguration(ctx context.Context, req RevertFlagConfigurationRequestObject) (RevertFlagConfigurationResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ec, err := a.h.RevertConfiguration.Handle(ctx, appflags.RevertConfigurationCommand{Configuration: cmd, Version: req.Body.Version})
	if err != nil {
		return nil, err
	}
	return RevertFlagConfiguration200JSONResponse{
		Body: configurationOut(ec), Headers: RevertFlagConfiguration200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// CopyFlagConfiguration makes a flag's configuration in one environment
// serve what it serves in another.
func (a *api) CopyFlagConfiguration(ctx context.Context, req CopyFlagConfigurationRequestObject) (CopyFlagConfigurationResponseObject, error) {
	cmd, err := configurationCommand(ctx, req.Flag, req.Environment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	ec, err := a.h.CopyConfiguration.Handle(ctx, appflags.CopyConfigurationCommand{
		Configuration: cmd, From: req.Body.From, Overrides: valueOr(req.Body.IncludeOverrides, false),
	})
	if err != nil {
		return nil, err
	}
	return CopyFlagConfiguration200JSONResponse{
		Body: configurationOut(ec), Headers: CopyFlagConfiguration200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
	}, nil
}

// configurationCommand returns the command naming flag's configuration in
// environment of the request's project, based on the version header names,
// none when it is nil.
func configurationCommand(ctx context.Context, flag, environment string, header *IfMatch) (appflags.ConfigurationCommand, error) {
	expected, err := ifMatch(header)
	return appflags.ConfigurationCommand{
		Project: projectID(ctx), Flag: flag, Environment: environment, Expected: expected, Caller: callerMember(ctx),
		Request: requestOf(ctx),
	}, err
}

// configurationIn returns the configuration b gives whole.
func configurationIn(b FlagConfigurationPut) flags.ConfigurationInput {
	return flags.ConfigurationInput{
		On: b.On, OffVariation: b.OffVariation, Fallthrough: b.Fallthrough, Prerequisites: each(valueOr(b.Prerequisites, nil), prerequisiteIn),
		Overrides: each(valueOr(b.Overrides, nil), func(o OverrideInput) flags.OverrideInput {
			return flags.OverrideInput{UnitType: o.UnitType, UnitID: o.UnitId, Variation: o.Variation, Note: o.Note}
		}),
		Rules: each(valueOr(b.Rules, nil), ruleIn),
	}
}

func prerequisiteIn(p Prerequisite) flags.Prerequisite {
	return flags.Prerequisite{Flag: p.Flag, Variation: p.Variation}
}

func ruleIn(r RuleInput) flags.RuleInput {
	out := flags.RuleInput{
		Description: valueOf(r.Description), Conditions: conditionsIn(valueOr(r.Conditions, nil)),
		Serve: flags.ServeInput{
			Kind: r.Serve.Kind, Variation: valueOf(r.Serve.Variation), UnitType: valueOf(r.Serve.UnitType),
			Experiment: valueOf(r.Serve.Experiment),
			Percentage: r.Serve.Percentage, Salt: valueOf(r.Serve.Salt),
		},
	}
	if r.Id != nil {
		ruleID := id.FromBytes[flags.Rule](*r.Id)
		out.ID = &ruleID
	}
	return out
}

func configurationOut(ec flags.EnvironmentConfiguration) FlagConfiguration {
	c := ec.Configuration
	return FlagConfiguration{
		Environment: ec.Environment, On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: each(c.Prerequisites, func(p flags.Prerequisite) Prerequisite {
			return Prerequisite{Flag: p.Flag, Variation: p.Variation}
		}),
		Overrides: each(c.Overrides, func(o flags.Override) Override {
			return Override{UnitType: o.UnitType, UnitId: o.UnitID, Variation: o.Variation, Note: optional(o.Note), AddedBy: memberOut(o.AddedBy)}
		}),
		Rules:     each(c.Rules, ruleOut),
		UpdatedAt: c.UpdatedAt, UpdatedBy: memberOut(c.UpdatedBy), Version: c.Version,
	}
}

func ruleOut(r flags.Rule) Rule {
	serve := Serve{Kind: r.Serve.Kind, Variation: optional(r.Serve.Variation)}
	if r.Serve.Kind == flags.ServeExperiment {
		serve.Experiment = optional(r.Serve.Experiment)
	}
	if r.Serve.Kind == flags.ServeRollout {
		percentage := r.Serve.Percentage
		serve.UnitType, serve.Percentage, serve.Salt = optional(r.Serve.UnitType), &percentage, optional(r.Serve.Salt)
	}
	return Rule{
		Id: openapi_types.UUID(r.ID.Bytes()), Description: r.Description, Conditions: each(r.Conditions, conditionOut),
		Serve: serve,
	}
}

// memberOut returns m as the API names a member, nil for none.
func memberOut(m *access.MemberID) *openapi_types.UUID {
	if m == nil {
		return nil
	}
	u := openapi_types.UUID(m.Bytes())
	return &u
}
