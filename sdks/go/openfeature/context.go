package openfeature

import (
	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// attributeUnits is the evaluation-context attribute carrying additional
// unit type/ID pairs.
const attributeUnits = "units"

// context translates a flattened OpenFeature evaluation context into a
// flagnetic Context: the targeting key becomes the unit ID of p.unitType,
// the "units" attribute adds further unit type/ID pairs, and every other
// attribute becomes an attribute of the flag context.
func (p *Provider) context(flatCtx of.FlattenedContext) flagnetic.Context {
	units := map[string]string{}
	attributes := make(map[string]any, len(flatCtx))
	for k, v := range flatCtx {
		switch k {
		case of.TargetingKey:
			// Handled below: it names the unit ID, not an attribute.
		case attributeUnits:
			if m, ok := v.(map[string]any); ok {
				for unitType, unitID := range m {
					if id, ok := unitID.(string); ok {
						units[unitType] = id
					}
				}
			}
		default:
			attributes[k] = v
		}
	}
	if tk, ok := flatCtx[of.TargetingKey].(string); ok && tk != "" {
		units[p.unitType] = tk
	}
	ctx := flagnetic.Context{Attributes: attributes}
	if len(units) > 0 {
		ctx.Units = units
	}
	return ctx
}

// flatten turns an OpenFeature EvaluationContext (as Track receives it) into
// the same FlattenedContext shape the SDK hands FeatureProvider's
// evaluation methods, so Track shares p.context's translation.
func flatten(evalCtx of.EvaluationContext) of.FlattenedContext {
	flat := of.FlattenedContext(evalCtx.Attributes())
	if tk := evalCtx.TargetingKey(); tk != "" {
		flat[of.TargetingKey] = tk
	}
	return flat
}
