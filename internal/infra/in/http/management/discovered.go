package management

import (
	"context"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ListDiscoveredAttributes answers the attributes evaluated contexts carried
// that the project does not declare.
func (a *api) ListDiscoveredAttributes(ctx context.Context, _ ListDiscoveredAttributesRequestObject,
) (ListDiscoveredAttributesResponseObject, error) {
	found, err := a.h.ListDiscoveredAttributes.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListDiscoveredAttributes200JSONResponse{Items: each(found, discoveredOut)}, nil
}

func discoveredOut(d delivery.DiscoveredAttribute) DiscoveredAttribute {
	types := make([]string, len(d.Types))
	for i, t := range d.Types {
		types[i] = string(t)
	}
	return DiscoveredAttribute{Key: d.Key, ObservedTypes: types, FirstSeen: d.FirstSeen, LastSeen: d.LastSeen, Count: d.Count}
}

// DismissDiscoveredAttribute hides a discovered attribute until it is seen
// with a new type.
func (a *api) DismissDiscoveredAttribute(ctx context.Context, req DismissDiscoveredAttributeRequestObject,
) (DismissDiscoveredAttributeResponseObject, error) {
	if err := a.h.DismissDiscoveredAttribute.Handle(ctx, projectID(ctx), req.Attribute); err != nil {
		return nil, err
	}
	return DismissDiscoveredAttribute204Response{}, nil
}

// DeclareDiscoveredAttribute declares a discovered attribute with the type
// given.
func (a *api) DeclareDiscoveredAttribute(ctx context.Context, req DeclareDiscoveredAttributeRequestObject,
) (DeclareDiscoveredAttributeResponseObject, error) {
	attr, err := a.h.DeclareDiscoveredAttribute.Handle(ctx, appdelivery.AttributeDeclaration{
		Project: projectID(ctx), Key: req.Attribute, Name: valueOf(req.Body.Name), Description: valueOf(req.Body.Description),
		Type: workspace.AttributeType(req.Body.Type), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeclareDiscoveredAttribute201JSONResponse{
		Body: attributeOut(attr), Headers: DeclareDiscoveredAttribute201ResponseHeaders{ETag: etag(attr.Version)},
	}, nil
}
