package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// projectID returns the project the seam found for the request ctx belongs
// to.
func projectID(ctx context.Context) workspace.ProjectID { return projectOf(ctx).project.ID }

// ListEnvironments answers the project's environments by sort order.
func (a *api) ListEnvironments(ctx context.Context, _ ListEnvironmentsRequestObject) (ListEnvironmentsResponseObject, error) {
	envs, err := a.h.ListEnvironments.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListEnvironments200JSONResponse{Items: each(envs, environmentOut)}, nil
}

// CreateEnvironment appends an environment.
func (a *api) CreateEnvironment(ctx context.Context, req CreateEnvironmentRequestObject) (CreateEnvironmentResponseObject, error) {
	e, err := a.h.CreateEnvironment.Handle(ctx, appworkspace.CreateEnvironmentCommand{
		Project: projectID(ctx), Key: req.Body.Key, Name: req.Body.Name, Caller: callerMember(ctx), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateEnvironment201JSONResponse(environmentOut(e)), nil
}

// GetEnvironment answers one environment.
func (a *api) GetEnvironment(ctx context.Context, req GetEnvironmentRequestObject) (GetEnvironmentResponseObject, error) {
	e, err := a.h.GetEnvironment.Handle(ctx, projectID(ctx), req.Environment)
	if err != nil {
		return nil, err
	}
	return GetEnvironment200JSONResponse(environmentOut(e)), nil
}

// UpdateEnvironment renames an environment.
func (a *api) UpdateEnvironment(ctx context.Context, req UpdateEnvironmentRequestObject) (UpdateEnvironmentResponseObject, error) {
	e, err := a.h.UpdateEnvironment.Handle(ctx, appworkspace.UpdateEnvironmentCommand{
		Project: projectID(ctx), Key: req.Environment, Request: requestOf(ctx),
		Change: workspace.EnvironmentChange{Key: req.Body.Key, Name: req.Body.Name},
	})
	if err != nil {
		return nil, err
	}
	return UpdateEnvironment200JSONResponse(environmentOut(e)), nil
}

// DeleteEnvironment deletes an environment.
func (a *api) DeleteEnvironment(ctx context.Context, req DeleteEnvironmentRequestObject) (DeleteEnvironmentResponseObject, error) {
	err := a.h.DeleteEnvironment.Handle(ctx, appworkspace.DeleteEnvironmentCommand{
		Project: projectID(ctx), Key: req.Environment, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteEnvironment204Response{}, nil
}

// ListUnitTypes answers the project's unit types by key.
func (a *api) ListUnitTypes(ctx context.Context, _ ListUnitTypesRequestObject) (ListUnitTypesResponseObject, error) {
	units, err := a.h.ListUnitTypes.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListUnitTypes200JSONResponse{Items: each(units, unitTypeListItemOut)}, nil
}

// CreateUnitType declares a unit type.
func (a *api) CreateUnitType(ctx context.Context, req CreateUnitTypeRequestObject) (CreateUnitTypeResponseObject, error) {
	u, err := a.h.CreateUnitType.Handle(ctx, appworkspace.CreateUnitTypeCommand{
		Project: projectID(ctx), Key: req.Body.Key, Name: req.Body.Name, Description: valueOf(req.Body.Description),
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateUnitType201JSONResponse(unitTypeOut(u)), nil
}

// GetUnitType answers one unit type.
func (a *api) GetUnitType(ctx context.Context, req GetUnitTypeRequestObject) (GetUnitTypeResponseObject, error) {
	u, err := a.h.GetUnitType.Handle(ctx, projectID(ctx), req.UnitType)
	if err != nil {
		return nil, err
	}
	return GetUnitType200JSONResponse(unitTypeOut(u)), nil
}

// UpdateUnitType renames a unit type or changes its description.
func (a *api) UpdateUnitType(ctx context.Context, req UpdateUnitTypeRequestObject) (UpdateUnitTypeResponseObject, error) {
	u, err := a.h.UpdateUnitType.Handle(ctx, appworkspace.UpdateUnitTypeCommand{
		Project: projectID(ctx), Key: req.UnitType, Request: requestOf(ctx),
		Change: workspace.UnitTypeChange{Key: req.Body.Key, Name: req.Body.Name, Description: req.Body.Description},
	})
	if err != nil {
		return nil, err
	}
	return UpdateUnitType200JSONResponse(unitTypeOut(u)), nil
}

// DeleteUnitType deletes a unit type.
func (a *api) DeleteUnitType(ctx context.Context, req DeleteUnitTypeRequestObject) (DeleteUnitTypeResponseObject, error) {
	err := a.h.DeleteUnitType.Handle(ctx, appworkspace.DeleteUnitTypeCommand{
		Project: projectID(ctx), Key: req.UnitType, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteUnitType204Response{}, nil
}

// ListAttributes answers the project's attributes by key.
func (a *api) ListAttributes(ctx context.Context, _ ListAttributesRequestObject) (ListAttributesResponseObject, error) {
	attrs, err := a.h.ListAttributes.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListAttributes200JSONResponse{Items: each(attrs, attributeListItemOut)}, nil
}

// CreateAttribute declares an attribute.
func (a *api) CreateAttribute(ctx context.Context, req CreateAttributeRequestObject) (CreateAttributeResponseObject, error) {
	attr, err := a.h.CreateAttribute.Handle(ctx, appworkspace.CreateAttributeCommand{
		Project: projectID(ctx), Key: req.Body.Key, Name: req.Body.Name, Description: valueOf(req.Body.Description),
		Type: workspace.AttributeType(req.Body.Type), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateAttribute201JSONResponse(attributeOut(attr)), nil
}

// GetAttribute answers one attribute.
func (a *api) GetAttribute(ctx context.Context, req GetAttributeRequestObject) (GetAttributeResponseObject, error) {
	attr, err := a.h.GetAttribute.Handle(ctx, projectID(ctx), req.Attribute)
	if err != nil {
		return nil, err
	}
	return GetAttribute200JSONResponse(attributeOut(attr)), nil
}

// UpdateAttribute renames an attribute or changes its description.
func (a *api) UpdateAttribute(ctx context.Context, req UpdateAttributeRequestObject) (UpdateAttributeResponseObject, error) {
	change := workspace.AttributeChange{Key: req.Body.Key, Name: req.Body.Name, Description: req.Body.Description}
	if req.Body.Type != nil {
		t := workspace.AttributeType(*req.Body.Type)
		change.Type = &t
	}
	attr, err := a.h.UpdateAttribute.Handle(ctx, appworkspace.UpdateAttributeCommand{
		Project: projectID(ctx), Key: req.Attribute, Change: change, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateAttribute200JSONResponse(attributeOut(attr)), nil
}

// DeleteAttribute deletes a declared attribute.
func (a *api) DeleteAttribute(ctx context.Context, req DeleteAttributeRequestObject) (DeleteAttributeResponseObject, error) {
	err := a.h.DeleteAttribute.Handle(ctx, appworkspace.DeleteAttributeCommand{
		Project: projectID(ctx), Key: req.Attribute, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteAttribute204Response{}, nil
}

func each[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func valueOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func environmentOut(e workspace.Environment) Environment {
	return Environment{Id: openapi_types.UUID(e.ID.Bytes()), Key: e.Key, Name: e.Name, SortOrder: e.SortOrder}
}

func unitTypeOut(u workspace.UnitType) UnitType {
	return UnitType{Id: openapi_types.UUID(u.ID.Bytes()), Key: u.Key, Name: u.Name, Description: u.Description}
}

func unitTypeListItemOut(u appworkspace.Used[workspace.UnitType]) UnitTypeListItem {
	d := unitTypeOut(u.Declaration)
	return UnitTypeListItem{Id: d.Id, Key: d.Key, Name: d.Name, Description: d.Description, UsageCount: u.UsageCount}
}

func attributeListItemOut(a appworkspace.Used[workspace.Attribute]) AttributeListItem {
	d := attributeOut(a.Declaration)
	return AttributeListItem{
		Id: d.Id, Key: d.Key, Type: d.Type, Name: d.Name, Description: d.Description, Standard: d.Standard,
		UsageCount: a.UsageCount,
	}
}

func attributeOut(a workspace.Attribute) Attribute {
	return Attribute{
		Id: openapi_types.UUID(a.ID.Bytes()), Key: a.Key, Type: AttributeType(a.Type), Name: a.Name,
		Description: a.Description, Standard: a.Standard,
	}
}
