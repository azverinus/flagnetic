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
	b := req.Body
	in := flags.ConfigurationInput{
		On: b.On, OffVariation: b.OffVariation, Fallthrough: b.Fallthrough, Prerequisites: each(valueOr(b.Prerequisites, nil), prerequisiteIn),
		Overrides: each(valueOr(b.Overrides, nil), func(o OverrideInput) flags.OverrideInput {
			return flags.OverrideInput{UnitType: o.UnitType, UnitID: o.UnitId, Variation: o.Variation, Note: o.Note}
		}),
		Rules: each(valueOr(b.Rules, nil), ruleIn),
	}
	ec, err := a.h.ReplaceConfiguration.Handle(ctx, appflags.ReplaceConfigurationCommand{Configuration: cmd, Input: in})
	if err != nil {
		return nil, err
	}
	return PutFlagConfiguration200JSONResponse{
		Body: configurationOut(ec), Headers: PutFlagConfiguration200ResponseHeaders{ETag: etag(ec.Configuration.Version)},
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

func prerequisiteIn(p Prerequisite) flags.Prerequisite {
	return flags.Prerequisite{Flag: p.Flag, Variation: p.Variation}
}

func ruleIn(r RuleInput) flags.RuleInput {
	out := flags.RuleInput{
		Description: valueOf(r.Description), Conditions: conditionsIn(valueOr(r.Conditions, nil)),
		Serve: flags.ServeInput{
			Kind: r.Serve.Kind, Variation: valueOf(r.Serve.Variation), UnitType: valueOf(r.Serve.UnitType),
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
