package management

import (
	"context"
	"fmt"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
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

// ListWebhookDeliveries answers a page of a subscription's delivery log.
func (a *api) ListWebhookDeliveries(ctx context.Context, req ListWebhookDeliveriesRequestObject) (ListWebhookDeliveriesResponseObject, error) {
	q := req.Params
	f := appgovernance.DeliveryFilter{Type: valueOf(q.EventType)}
	if q.Status != nil {
		f.Status = governance.DeliveryStatus(*q.Status)
		if !slices.Contains(governance.DeliveryStatuses, f.Status) {
			return nil, flaghttp.InvalidRequest("status is not a delivery status")
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
			return nil, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxAuditLimit))
		}
		page.Limit = *q.Limit
	}
	if q.Cursor != nil {
		after, err := id.Parse[governance.Delivery](*q.Cursor)
		if err != nil {
			return nil, flaghttp.InvalidRequest("cursor is not a next_cursor this API returned")
		}
		page.After = after
	}
	list, next, err := a.h.ListWebhookDeliveries.Handle(ctx, projectID(ctx), id.FromBytes[governance.Webhook](req.Webhook), f, page)
	if err != nil {
		return nil, err
	}
	out := WebhookDeliveryList{Items: each(list, deliveryOut)}
	if !next.IsZero() {
		cursor := next.String()
		out.NextCursor = &cursor
	}
	return ListWebhookDeliveries200JSONResponse(out), nil
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
