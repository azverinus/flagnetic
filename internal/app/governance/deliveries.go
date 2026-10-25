package governance

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// FanOutStore is what the fan-out reads and writes.
type FanOutStore interface {
	// ClaimEvents locks up to limit of the oldest events of the outbox for
	// the unit of work ctx carries, skipping those another one holds, and
	// returns them oldest first.
	ClaimEvents(ctx context.Context, limit int) ([]governance.QueuedEvent, error)
	// EnabledWebhooks returns the enabled subscriptions of projects, a zero
	// project for the instance's.
	EnabledWebhooks(ctx context.Context, projects []workspace.ProjectID) ([]governance.Webhook, error)
	CreateDeliveries(ctx context.Context, deliveries []governance.Delivery) error
	// DeleteEvents deletes the events with ids from the outbox.
	DeleteEvents(ctx context.Context, ids []governance.EventID) error
	// OutboxDepth returns how many events wait in the outbox.
	OutboxDepth(ctx context.Context) (int, error)
}

// fanOutBatch bounds the events one transaction of the fan-out claims, so a
// backlog is worked through in transactions short enough to hold no lock
// for long.
const fanOutBatch = 200

// FanOutReport is what one round of the fan-out did.
type FanOutReport struct {
	Events, Deliveries int
	// Depth is how many events still wait in the outbox after the round.
	Depth int
}

// RunFanOut turns the events waiting in the outbox into deliveries.
type RunFanOut struct {
	tx    Transactor
	store FanOutStore
	clock clock.Clock
	ids   *id.Source
}

// NewRunFanOut returns the use case.
func NewRunFanOut(tx Transactor, store FanOutStore, c clock.Clock, ids *id.Source) *RunFanOut {
	return &RunFanOut{tx: tx, store: store, clock: c, ids: ids}
}

// Handle fans out the outbox in batches until a batch comes back short or
// ctx ends. Each batch is one transaction: claim its events, skipping those
// another replica holds, write one pending delivery per subscription that
// wants each, and delete the events. A batch that fails leaves its events
// for the next round, so no event is lost and none is fanned out twice.
func (h *RunFanOut) Handle(ctx context.Context) (FanOutReport, error) {
	var report FanOutReport
	for ctx.Err() == nil {
		var events, deliveries int
		err := h.tx.Within(ctx, func(ctx context.Context) error {
			var err error
			events, deliveries, err = h.batch(ctx)
			return err
		})
		if err != nil {
			return report, err
		}
		report.Events += events
		report.Deliveries += deliveries
		if events < fanOutBatch {
			break
		}
	}
	depth, err := h.store.OutboxDepth(ctx)
	report.Depth = depth
	return report, err
}

func (h *RunFanOut) batch(ctx context.Context) (int, int, error) {
	events, err := h.store.ClaimEvents(ctx, fanOutBatch)
	if err != nil || len(events) == 0 {
		return 0, 0, err
	}
	var projects []workspace.ProjectID
	ids := make([]governance.EventID, len(events))
	for i, e := range events {
		ids[i] = e.ID
		projects = append(projects, e.Project)
	}
	webhooks, err := h.store.EnabledWebhooks(ctx, projects)
	if err != nil {
		return 0, 0, err
	}
	now := h.clock.Now().UTC().Truncate(time.Microsecond)
	var deliveries []governance.Delivery
	for _, e := range events {
		for _, w := range webhooks {
			if w.Wants(e) {
				deliveries = append(deliveries, governance.NewDelivery(id.New[governance.Delivery](h.ids), w.ID, e, now))
			}
		}
	}
	if len(deliveries) > 0 {
		if err = h.store.CreateDeliveries(ctx, deliveries); err != nil {
			return 0, 0, err
		}
	}
	return len(events), len(deliveries), h.store.DeleteEvents(ctx, ids)
}

// DeliveryFilter selects a subscription's deliveries; every field set
// narrows the selection.
type DeliveryFilter struct {
	Status governance.DeliveryStatus
	Type   string
	// From and To bound when a delivery was made, both included;
	// zero for none.
	From, To time.Time
}

// DeliveryReader reads a subscription's delivery log.
type DeliveryReader interface {
	// Deliveries returns at most limit of webhook's deliveries matching f,
	// older than after (unless zero), newest first.
	Deliveries(ctx context.Context, webhook governance.WebhookID, f DeliveryFilter, after governance.DeliveryID, limit int,
	) ([]governance.Delivery, error)
	// Delivery returns webhook's delivery with delivery, or ErrNotFound.
	Delivery(ctx context.Context, webhook governance.WebhookID, delivery governance.DeliveryID) (governance.Delivery, error)
}

// DeliveryPage asks for one page of a delivery log, newest first.
type DeliveryPage struct {
	// After is the last delivery of the previous page, zero for the first.
	After governance.DeliveryID
	Limit int
}

// ListDeliveries reads a subscription's delivery log a page at a time.
type ListDeliveries struct {
	webhooks   WebhookStore
	deliveries DeliveryReader
}

// NewListDeliveries returns the query.
func NewListDeliveries(webhooks WebhookStore, deliveries DeliveryReader) *ListDeliveries {
	return &ListDeliveries{webhooks: webhooks, deliveries: deliveries}
}

// Handle returns one page of the deliveries of project's subscription
// webhook matching f, newest first, and the last delivery of the page when
// another follows (zero otherwise). Paging by identifier, a delivery made
// meanwhile never shifts a page.
func (h *ListDeliveries) Handle(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID,
	f DeliveryFilter, p DeliveryPage,
) ([]governance.Delivery, governance.DeliveryID, error) {
	if _, err := webhookOf(ctx, h.webhooks, project, webhook); err != nil {
		return nil, governance.DeliveryID{}, err
	}
	list, err := h.deliveries.Deliveries(ctx, webhook, f, p.After, p.Limit+1)
	if err != nil || len(list) <= p.Limit {
		return list, governance.DeliveryID{}, err
	}
	list = list[:p.Limit]
	return list, list[p.Limit-1].ID, nil
}

// GetDelivery reads one delivery of a subscription.
type GetDelivery struct {
	webhooks   WebhookStore
	deliveries DeliveryReader
}

// NewGetDelivery returns the query.
func NewGetDelivery(webhooks WebhookStore, deliveries DeliveryReader) *GetDelivery {
	return &GetDelivery{webhooks: webhooks, deliveries: deliveries}
}

// ErrDeliveryNotFound answers a delivery the subscription did not make.
var ErrDeliveryNotFound = apperrors.NotFound("not_found", "the webhook subscription has no delivery with this identifier")

// Handle returns the delivery of project's subscription webhook with
// delivery, or not_found.
func (h *GetDelivery) Handle(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID,
	delivery governance.DeliveryID,
) (governance.Delivery, error) {
	if _, err := webhookOf(ctx, h.webhooks, project, webhook); err != nil {
		return governance.Delivery{}, err
	}
	d, err := h.deliveries.Delivery(ctx, webhook, delivery)
	if errors.Is(err, ErrNotFound) {
		return d, ErrDeliveryNotFound
	}
	return d, err
}
