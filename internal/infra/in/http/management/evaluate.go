package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/eval"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Evaluate evaluates an environment's flags for a context.
func (a *api) Evaluate(ctx context.Context, req EvaluateRequestObject) (EvaluateResponseObject, error) {
	b := req.Body
	q := appdelivery.EvaluateQuery{
		Project: projectID(ctx), Environment: req.Environment,
		Context: eval.Context{Units: valueOr(b.Context.Units, nil), Attributes: valueOr(b.Context.Attributes, nil)},
		Flags:   valueOr(b.Flags, nil),
	}
	if b.Draft != nil {
		q.Draft = &appdelivery.Draft{Flag: b.Draft.Flag, Configuration: configurationIn(b.Draft.Configuration)}
	}
	ev, err := a.h.Evaluate.Handle(ctx, q)
	if err != nil {
		return nil, underRequest(err)
	}
	out := Evaluate200JSONResponse{RulesetVersion: ev.RulesetVersion, Flags: make(map[string]EvaluationResult, len(ev.Results))}
	for key, r := range ev.Results {
		out.Flags[key] = evaluationOut(r)
	}
	return out, nil
}

// underRequest points the violations of a refused context or draft into
// the request body, where each was given.
func underRequest(err error) error {
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid {
		return err
	}
	switch e.Code {
	case "invalid_context":
		return e.Under("/context")
	case "invalid_configuration":
		return e.Under("/draft/configuration")
	}
	return err
}

func evaluationOut(r delivery.Result) EvaluationResult {
	out := EvaluationResult{
		Reason: string(r.Reason), Variation: optional(r.Variation), Prerequisite: optional(r.Prerequisite),
		Bucket: r.Bucket, Salt: optional(r.Salt), ErrorCode: optional(string(r.ErrorCode)),
		OverrideInfluenced: r.OverrideInfluenced,
	}
	if r.ErrorCode != eval.ErrorFlagNotFound {
		typ, exposure := FlagType(r.Type), r.AutomaticExposure
		out.Type, out.AutomaticExposure = &typ, &exposure
	}
	if r.Value != nil {
		out.Value = &r.Value
	}
	if rule, err := id.Parse[flags.Rule](r.RuleID); err == nil {
		u := openapi_types.UUID(rule.Bytes())
		out.RuleId = &u
	}
	return out
}
