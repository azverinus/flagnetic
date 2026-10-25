package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListWebhookEventTypes answers the event catalogue.
func (a *api) ListWebhookEventTypes(context.Context, ListWebhookEventTypesRequestObject) (ListWebhookEventTypesResponseObject, error) {
	return ListWebhookEventTypes200JSONResponse{Items: each(a.h.ListWebhookEventTypes.Handle(), func(e governance.EventType) WebhookEventTypeInfo {
		return WebhookEventTypeInfo{Type: e.Type, Description: e.Description}
	})}, nil
}

// ListWebhooks answers the project's webhook subscriptions, never their
// secrets.
func (a *api) ListWebhooks(ctx context.Context, _ ListWebhooksRequestObject) (ListWebhooksResponseObject, error) {
	list, err := a.h.ListWebhooks.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListWebhooks200JSONResponse{Items: each(list, webhookOut)}, nil
}

// CreateWebhook creates a webhook subscription and answers its secret, this
// once.
func (a *api) CreateWebhook(ctx context.Context, req CreateWebhookRequestObject) (CreateWebhookResponseObject, error) {
	b := req.Body
	var creator *governance.Author
	if author := authorOf(ctx); author.Member != nil || author.Token != nil {
		creator = &author
	}
	created, err := a.h.CreateWebhook.Handle(ctx, appgovernance.CreateWebhookCommand{
		Project: projectID(ctx), CreatedBy: creator, Request: requestOf(ctx),
		Input: governance.WebhookInput{
			Name: b.Name, URL: b.Url, Events: b.Events, Environments: valueOr(b.Environments, nil),
			Enabled: valueOr(b.Enabled, true),
		},
	})
	if err != nil {
		return nil, err
	}
	w := webhookOut(created.Webhook)
	return CreateWebhook201JSONResponse{
		Body: WebhookWithSecret{
			Id: w.Id, Name: w.Name, Url: w.Url, Events: w.Events, Environments: w.Environments, Enabled: w.Enabled,
			DisabledReason: w.DisabledReason, CreatedBy: w.CreatedBy, LastSuccessAt: w.LastSuccessAt,
			ConsecutiveFailures: w.ConsecutiveFailures, PreviousSecretExpiresAt: w.PreviousSecretExpiresAt,
			CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Version: w.Version, Secret: created.Secret.Reveal(),
		},
		Headers: CreateWebhook201ResponseHeaders{ETag: etag(w.Version)},
	}, nil
}

// GetWebhook answers a webhook subscription.
func (a *api) GetWebhook(ctx context.Context, req GetWebhookRequestObject) (GetWebhookResponseObject, error) {
	w, err := a.h.GetWebhook.Handle(ctx, projectID(ctx), id.FromBytes[governance.Webhook](req.Webhook))
	if err != nil {
		return nil, err
	}
	return GetWebhook200JSONResponse{Body: webhookOut(w), Headers: GetWebhook200ResponseHeaders{ETag: etag(w.Version)}}, nil
}

// UpdateWebhook edits a webhook subscription.
func (a *api) UpdateWebhook(ctx context.Context, req UpdateWebhookRequestObject) (UpdateWebhookResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	w, err := a.h.UpdateWebhook.Handle(ctx, appgovernance.UpdateWebhookCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook), Expected: expected,
		Request: requestOf(ctx),
		Change: governance.WebhookChange{
			Name: b.Name, URL: b.Url, Events: b.Events, Environments: b.Environments, Enabled: b.Enabled,
		},
	})
	if err != nil {
		return nil, err
	}
	return UpdateWebhook200JSONResponse{Body: webhookOut(w), Headers: UpdateWebhook200ResponseHeaders{ETag: etag(w.Version)}}, nil
}

// DeleteWebhook deletes a webhook subscription.
func (a *api) DeleteWebhook(ctx context.Context, req DeleteWebhookRequestObject) (DeleteWebhookResponseObject, error) {
	err := a.h.DeleteWebhook.Handle(ctx, appgovernance.DeleteWebhookCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteWebhook204Response{}, nil
}

func webhookOut(w governance.Webhook) Webhook {
	out := Webhook{
		Id: openapi_types.UUID(w.ID.Bytes()), Name: w.Name, Url: w.URL, Events: w.Events, Environments: w.Environments,
		Enabled: w.Enabled, LastSuccessAt: w.LastSuccessAt, ConsecutiveFailures: w.ConsecutiveFailures,
		PreviousSecretExpiresAt: w.PreviousSecretExpiresAt, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		Version: w.Version,
	}
	if w.DisabledReason != "" {
		reason := WebhookDisabledReason(w.DisabledReason)
		out.DisabledReason = &reason
	}
	if c := w.CreatedBy; c != nil {
		switch {
		case c.Member != nil:
			out.CreatedBy = &WebhookCreator{Kind: WebhookCreatorKindMember, Id: openapi_types.UUID(c.Member.Bytes()), Name: c.Name}
		case c.Token != nil:
			out.CreatedBy = &WebhookCreator{Kind: WebhookCreatorKindToken, Id: openapi_types.UUID(c.Token.Bytes()), Name: c.Name}
		}
	}
	return out
}
