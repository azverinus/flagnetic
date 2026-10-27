package management

import (
	"context"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The instance's subscriptions are a project's addressed with no project:
// the same use cases run on a zero project, which they take for the
// instance.
var instance = workspace.ProjectID{}

// ListInstanceWebhooks answers the instance's webhook subscriptions, never
// their secrets.
func (a *api) ListInstanceWebhooks(ctx context.Context, _ ListInstanceWebhooksRequestObject) (ListInstanceWebhooksResponseObject, error) {
	list, err := a.h.ListWebhooks.Handle(ctx, instance)
	if err != nil {
		return nil, err
	}
	return ListInstanceWebhooks200JSONResponse{Items: each(list, webhookOut)}, nil
}

// CreateInstanceWebhook creates an instance subscription and answers its
// secret, this once.
func (a *api) CreateInstanceWebhook(ctx context.Context, req CreateInstanceWebhookRequestObject) (CreateInstanceWebhookResponseObject, error) {
	created, err := a.createWebhook(ctx, instance, *req.Body)
	if err != nil {
		return nil, err
	}
	return CreateInstanceWebhook201JSONResponse{
		Body: withSecret(created), Headers: CreateInstanceWebhook201ResponseHeaders{ETag: etag(created.Webhook.Version)},
	}, nil
}

// GetInstanceWebhook answers an instance subscription.
func (a *api) GetInstanceWebhook(ctx context.Context, req GetInstanceWebhookRequestObject) (GetInstanceWebhookResponseObject, error) {
	w, err := a.h.GetWebhook.Handle(ctx, instance, id.FromBytes[governance.Webhook](req.Webhook))
	if err != nil {
		return nil, err
	}
	return GetInstanceWebhook200JSONResponse{Body: webhookOut(w), Headers: GetInstanceWebhook200ResponseHeaders{ETag: etag(w.Version)}}, nil
}

// UpdateInstanceWebhook edits an instance subscription.
func (a *api) UpdateInstanceWebhook(ctx context.Context, req UpdateInstanceWebhookRequestObject) (UpdateInstanceWebhookResponseObject, error) {
	w, err := a.updateWebhook(ctx, instance, req.Webhook, req.Params.IfMatch, *req.Body)
	if err != nil {
		return nil, err
	}
	return UpdateInstanceWebhook200JSONResponse{
		Body: webhookOut(w), Headers: UpdateInstanceWebhook200ResponseHeaders{ETag: etag(w.Version)},
	}, nil
}

// DeleteInstanceWebhook deletes an instance subscription.
func (a *api) DeleteInstanceWebhook(ctx context.Context, req DeleteInstanceWebhookRequestObject) (DeleteInstanceWebhookResponseObject, error) {
	err := a.h.DeleteWebhook.Handle(ctx, appgovernance.DeleteWebhookCommand{
		Project: instance, Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteInstanceWebhook204Response{}, nil
}

// EnableInstanceWebhook enables an instance subscription.
func (a *api) EnableInstanceWebhook(ctx context.Context, req EnableInstanceWebhookRequestObject) (EnableInstanceWebhookResponseObject, error) {
	w, err := a.h.EnableWebhook.Handle(ctx, appgovernance.WebhookCommand{
		Project: instance, Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return EnableInstanceWebhook200JSONResponse{
		Body: webhookOut(w), Headers: EnableInstanceWebhook200ResponseHeaders{ETag: etag(w.Version)},
	}, nil
}

// RotateInstanceWebhookSecret issues an instance subscription a new secret
// and answers it, this once.
func (a *api) RotateInstanceWebhookSecret(ctx context.Context, req RotateInstanceWebhookSecretRequestObject) (RotateInstanceWebhookSecretResponseObject, error) {
	rotated, err := a.h.RotateWebhookSecret.Handle(ctx, appgovernance.WebhookCommand{
		Project: instance, Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RotateInstanceWebhookSecret200JSONResponse{
		Body: withSecret(rotated), Headers: RotateInstanceWebhookSecret200ResponseHeaders{ETag: etag(rotated.Webhook.Version)},
	}, nil
}

// ListInstanceWebhookDeliveries answers a page of an instance
// subscription's delivery log.
func (a *api) ListInstanceWebhookDeliveries(ctx context.Context, req ListInstanceWebhookDeliveriesRequestObject) (ListInstanceWebhookDeliveriesResponseObject, error) {
	list, next, err := a.listDeliveries(ctx, instance, req.Webhook, ListWebhookDeliveriesParams(req.Params))
	if err != nil {
		return nil, err
	}
	return ListInstanceWebhookDeliveries200JSONResponse(deliveryList(list, next)), nil
}

// GetInstanceWebhookDelivery answers one delivery of an instance
// subscription with its attempts.
func (a *api) GetInstanceWebhookDelivery(ctx context.Context, req GetInstanceWebhookDeliveryRequestObject) (GetInstanceWebhookDeliveryResponseObject, error) {
	d, err := a.h.GetWebhookDelivery.Handle(ctx, instance, id.FromBytes[governance.Webhook](req.Webhook),
		id.FromBytes[governance.Delivery](req.Delivery))
	if err != nil {
		return nil, err
	}
	return GetInstanceWebhookDelivery200JSONResponse(deliveryOut(d)), nil
}

// TestInstanceWebhook queues a ping naming no project for an instance
// subscription and answers its pending delivery.
func (a *api) TestInstanceWebhook(ctx context.Context, req TestInstanceWebhookRequestObject) (TestInstanceWebhookResponseObject, error) {
	d, err := a.h.TestWebhook.Handle(ctx, appgovernance.TestWebhookCommand{
		Project: instance, Webhook: id.FromBytes[governance.Webhook](req.Webhook), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return TestInstanceWebhook202JSONResponse(deliveryOut(d)), nil
}

// RedeliverInstanceWebhookDelivery queues an instance delivery's event
// again and answers the new delivery, pending.
func (a *api) RedeliverInstanceWebhookDelivery(ctx context.Context, req RedeliverInstanceWebhookDeliveryRequestObject) (RedeliverInstanceWebhookDeliveryResponseObject, error) {
	d, err := a.h.RedeliverWebhook.Handle(ctx, appgovernance.RedeliverCommand{
		Project: instance, Webhook: id.FromBytes[governance.Webhook](req.Webhook),
		Delivery: id.FromBytes[governance.Delivery](req.Delivery),
	})
	if err != nil {
		return nil, err
	}
	return RedeliverInstanceWebhookDelivery202JSONResponse(deliveryOut(d)), nil
}
