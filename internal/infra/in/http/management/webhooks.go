package management

import (
	"context"
	"fmt"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
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

// createWebhook creates a subscription of project, the instance's for a
// zero project, from the body asked for.
func (a *api) createWebhook(ctx context.Context, project workspace.ProjectID, b WebhookCreate) (appgovernance.CreatedWebhook, error) {
	var creator *governance.Author
	if author := authorOf(ctx); author.Member != nil || author.Token != nil {
		creator = &author
	}
	return a.h.CreateWebhook.Handle(ctx, appgovernance.CreateWebhookCommand{
		Project: project, CreatedBy: creator, Request: requestOf(ctx),
		Input: governance.WebhookInput{
			Name: b.Name, URL: b.Url, Events: b.Events, Environments: valueOr(b.Environments, nil),
			Enabled: valueOr(b.Enabled, true),
		},
	})
}

// CreateWebhook creates a webhook subscription and answers its secret, this
// once.
func (a *api) CreateWebhook(ctx context.Context, req CreateWebhookRequestObject) (CreateWebhookResponseObject, error) {
	created, err := a.createWebhook(ctx, projectID(ctx), *req.Body)
	if err != nil {
		return nil, err
	}
	return CreateWebhook201JSONResponse{
		Body: withSecret(created), Headers: CreateWebhook201ResponseHeaders{ETag: etag(created.Webhook.Version)},
	}, nil
}

// withSecret answers a subscription with the secret just issued to it.
func withSecret(issued appgovernance.CreatedWebhook) WebhookWithSecret {
	w := webhookOut(issued.Webhook)
	return WebhookWithSecret{
		Id: w.Id, Name: w.Name, Url: w.Url, Events: w.Events, Environments: w.Environments, Enabled: w.Enabled,
		DisabledReason: w.DisabledReason, CreatedBy: w.CreatedBy, LastSuccessAt: w.LastSuccessAt,
		ConsecutiveFailures: w.ConsecutiveFailures, PreviousSecretExpiresAt: w.PreviousSecretExpiresAt,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Version: w.Version, Secret: issued.Secret.Reveal(),
	}
}

// EnableWebhook enables a webhook subscription.
func (a *api) EnableWebhook(ctx context.Context, req EnableWebhookRequestObject) (EnableWebhookResponseObject, error) {
	w, err := a.h.EnableWebhook.Handle(ctx, appgovernance.WebhookCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return EnableWebhook200JSONResponse{Body: webhookOut(w), Headers: EnableWebhook200ResponseHeaders{ETag: etag(w.Version)}}, nil
}

// RotateWebhookSecret issues a webhook subscription a new secret and
// answers it, this once.
func (a *api) RotateWebhookSecret(ctx context.Context, req RotateWebhookSecretRequestObject) (RotateWebhookSecretResponseObject, error) {
	rotated, err := a.h.RotateWebhookSecret.Handle(ctx, appgovernance.WebhookCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RotateWebhookSecret200JSONResponse{
		Body: withSecret(rotated), Headers: RotateWebhookSecret200ResponseHeaders{ETag: etag(rotated.Webhook.Version)},
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
	w, err := a.updateWebhook(ctx, projectID(ctx), req.Webhook, req.Params.IfMatch, *req.Body)
	if err != nil {
		return nil, err
	}
	return UpdateWebhook200JSONResponse{Body: webhookOut(w), Headers: UpdateWebhook200ResponseHeaders{ETag: etag(w.Version)}}, nil
}

// updateWebhook edits project's subscription webhook, the instance's for a
// zero project, based on the version ifMatchHeader names.
func (a *api) updateWebhook(ctx context.Context, project workspace.ProjectID, webhook WebhookID, ifMatchHeader *IfMatch,
	b WebhookPatch,
) (governance.Webhook, error) {
	expected, err := ifMatch(ifMatchHeader)
	if err != nil {
		return governance.Webhook{}, err
	}
	return a.h.UpdateWebhook.Handle(ctx, appgovernance.UpdateWebhookCommand{
		Project: project, Webhook: id.FromBytes[governance.Webhook](webhook), Expected: expected,
		Request: requestOf(ctx),
		Change: governance.WebhookChange{
			Name: b.Name, URL: b.Url, Events: b.Events, Environments: b.Environments, Enabled: b.Enabled,
		},
	})
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

// ListWebhookDeliveries answers a page of a subscription's delivery log.
func (a *api) ListWebhookDeliveries(ctx context.Context, req ListWebhookDeliveriesRequestObject) (ListWebhookDeliveriesResponseObject, error) {
	list, next, err := a.listDeliveries(ctx, projectID(ctx), req.Webhook, req.Params)
	if err != nil {
		return nil, err
	}
	return ListWebhookDeliveries200JSONResponse(deliveryList(list, next)), nil
}

// listDeliveries answers the page q asks for of the delivery log of
// project's subscription webhook, the instance's for a zero project.
func (a *api) listDeliveries(ctx context.Context, project workspace.ProjectID, webhook WebhookID,
	q ListWebhookDeliveriesParams,
) ([]governance.Delivery, governance.DeliveryID, error) {
	f := appgovernance.DeliveryFilter{Type: valueOf(q.EventType)}
	if q.Status != nil {
		f.Status = governance.DeliveryStatus(*q.Status)
		if !slices.Contains(governance.DeliveryStatuses, f.Status) {
			return nil, governance.DeliveryID{}, flaghttp.InvalidRequest("status is not a delivery status")
		}
	}
	if q.From != nil {
		f.From = *q.From
	}
	if q.To != nil {
		f.To = *q.To
	}
	page := appgovernance.DeliveryPage{Limit: defaultAuditLimit}
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > maxAuditLimit {
			return nil, governance.DeliveryID{}, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxAuditLimit))
		}
		page.Limit = *q.Limit
	}
	if q.Cursor != nil {
		after, err := id.Parse[governance.Delivery](*q.Cursor)
		if err != nil {
			return nil, governance.DeliveryID{}, flaghttp.InvalidRequest("cursor is not a next_cursor this API returned")
		}
		page.After = after
	}
	return a.h.ListWebhookDeliveries.Handle(ctx, project, id.FromBytes[governance.Webhook](webhook), f, page)
}

// deliveryList answers a page of deliveries, with the cursor of the next
// when next is not zero.
func deliveryList(list []governance.Delivery, next governance.DeliveryID) WebhookDeliveryList {
	out := WebhookDeliveryList{Items: each(list, deliveryOut)}
	if !next.IsZero() {
		cursor := next.String()
		out.NextCursor = &cursor
	}
	return out
}

// GetWebhookDelivery answers one delivery of a subscription with its
// attempts.
func (a *api) GetWebhookDelivery(ctx context.Context, req GetWebhookDeliveryRequestObject) (GetWebhookDeliveryResponseObject, error) {
	d, err := a.h.GetWebhookDelivery.Handle(ctx, projectID(ctx), id.FromBytes[governance.Webhook](req.Webhook),
		id.FromBytes[governance.Delivery](req.Delivery))
	if err != nil {
		return nil, err
	}
	return GetWebhookDelivery200JSONResponse(deliveryOut(d)), nil
}

// TestWebhook queues a ping for the subscription and answers its pending
// delivery.
func (a *api) TestWebhook(ctx context.Context, req TestWebhookRequestObject) (TestWebhookResponseObject, error) {
	d, err := a.h.TestWebhook.Handle(ctx, appgovernance.TestWebhookCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return TestWebhook202JSONResponse(deliveryOut(d)), nil
}

// RedeliverWebhookDelivery queues a delivery's event again and answers the
// new delivery, pending.
func (a *api) RedeliverWebhookDelivery(ctx context.Context, req RedeliverWebhookDeliveryRequestObject) (RedeliverWebhookDeliveryResponseObject, error) {
	d, err := a.h.RedeliverWebhook.Handle(ctx, appgovernance.RedeliverCommand{
		Project: projectID(ctx), Webhook: id.FromBytes[governance.Webhook](req.Webhook),
		Delivery: id.FromBytes[governance.Delivery](req.Delivery),
	})
	if err != nil {
		return nil, err
	}
	return RedeliverWebhookDelivery202JSONResponse(deliveryOut(d)), nil
}

func deliveryOut(d governance.Delivery) WebhookDelivery {
	return WebhookDelivery{
		Id: openapi_types.UUID(d.ID.Bytes()), EventId: openapi_types.UUID(d.Event.Bytes()), EventType: d.Type,
		Status: WebhookDeliveryStatus(d.Status), NextAttemptAt: d.NextAttemptAt, CreatedAt: d.CreatedAt,
		Attempts: each(d.Attempts, func(a governance.DeliveryAttempt) WebhookDeliveryAttempt {
			return WebhookDeliveryAttempt{
				N: a.N, At: a.At, StatusCode: a.StatusCode, LatencyMs: a.LatencyMS, ResponseSnippet: a.ResponseSnippet,
				Error: a.Error,
			}
		}),
	}
}
