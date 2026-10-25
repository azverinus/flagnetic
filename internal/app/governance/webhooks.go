package governance

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// WebhookStore keeps webhook subscriptions.
type WebhookStore interface {
	ProjectLocker
	// EnvironmentKeys returns the keys of project's environments.
	EnvironmentKeys(ctx context.Context, project workspace.ProjectID) ([]string, error)
	// Webhooks returns project's subscriptions, the oldest first.
	Webhooks(ctx context.Context, project workspace.ProjectID) ([]governance.Webhook, error)
	// Webhook returns project's subscription with webhook, or ErrNotFound.
	Webhook(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID) (governance.Webhook, error)
	CreateWebhook(ctx context.Context, w governance.Webhook) error
	// UpdateWebhook stores w in place of the subscription with its
	// identifier.
	UpdateWebhook(ctx context.Context, w governance.Webhook) error
	// DeleteWebhook deletes project's subscription with webhook, or answers
	// ErrNotFound.
	DeleteWebhook(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID) error
}

// Resolver resolves a target's host name to its addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Sealer seals a value under the instance's secret key.
type Sealer interface {
	Seal(purpose secret.Purpose, plaintext []byte) (string, error)
}

// WebhookDeps are what the webhook subscription use cases run on.
type WebhookDeps struct {
	Tx       Transactor
	Store    WebhookStore
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	Random   io.Reader
	Sealer   Sealer
	Resolver Resolver
	// Targets is what the operator allows a target to be.
	Targets governance.TargetPolicy
}

// resolveTimeout bounds how long a write waits for a target's name to
// resolve.
const resolveTimeout = 5 * time.Second

func (d WebhookDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// check returns in as CheckWebhook accepts it against project's
// environments as the store reads them.
func (d WebhookDeps) check(ctx context.Context, project workspace.ProjectID, in governance.WebhookInput) (governance.WebhookInput, error) {
	environments, err := d.Store.EnvironmentKeys(ctx, project)
	if err != nil {
		return in, err
	}
	return governance.CheckWebhook(in, environments, d.Targets)
}

// checkTarget refuses a target whose name resolves to an address the
// operator does not allow. It runs before the write's transaction, since it
// waits on the network. A name that does not resolve is accepted: the
// receiver may not be up yet, or the resolver briefly unreachable, and every
// connection checks the address it reaches again.
func (d WebhookDeps) checkTarget(ctx context.Context, url string) error {
	if d.Targets.AllowPrivateNetworks {
		return nil
	}
	host := governance.TargetHost(url)
	if addr, err := netip.ParseAddr(host); err == nil {
		return governance.CheckTargetAddresses([]netip.Addr{addr}, d.Targets)
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, _ := d.Resolver.LookupNetIP(ctx, "ip", host)
	return governance.CheckTargetAddresses(addrs, d.Targets)
}

// lockProject takes project's row lock, answering a project deleted since
// the request found it as unknown.
func (d WebhookDeps) lockProject(ctx context.Context, project workspace.ProjectID) error {
	err := d.Store.LockProject(ctx, project)
	if errors.Is(err, ErrNotFound) {
		return errProjectNotFound
	}
	return err
}

// record writes the audit entry of action on a subscription, made at now in
// req, with its values around the change.
func (d WebhookDeps) record(ctx context.Context, now time.Time, req governance.Request, action string,
	before, after *governance.Webhook,
) error {
	w := after
	if w == nil {
		w = before
	}
	r := governance.Record{
		Project: w.Project, Action: action, Entity: governance.Entity{Kind: "webhook", ID: w.ID.String(), Key: w.Name},
	}
	if before != nil {
		r.Before = webhookValues(*before)
	}
	if after != nil {
		r.After = webhookValues(*after)
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

// redacted stands for a subscription's secret in its audit values, so an
// auditor sees there is one and never reads it.
const redacted = "[redacted]"

// webhookValues returns what the audit log records of w.
func webhookValues(w governance.Webhook) map[string]any {
	var reason any
	if w.DisabledReason != "" {
		reason = string(w.DisabledReason)
	}
	return map[string]any{
		"id": w.ID.String(), "name": w.Name, "url": w.URL, "events": w.Events, "environments": w.Environments,
		"enabled": w.Enabled, "disabled_reason": reason, "secret": redacted,
	}
}

// ErrWebhookNotFound answers a subscription the project does not have.
var ErrWebhookNotFound = apperrors.NotFound("not_found", "the project has no webhook subscription with this identifier")

func webhookOf(ctx context.Context, store WebhookStore, project workspace.ProjectID, webhook governance.WebhookID,
) (governance.Webhook, error) {
	w, err := store.Webhook(ctx, project, webhook)
	if errors.Is(err, ErrNotFound) {
		return w, ErrWebhookNotFound
	}
	return w, err
}

// ListWebhookEventTypes answers the event catalogue.
type ListWebhookEventTypes struct{}

// NewListWebhookEventTypes returns the query.
func NewListWebhookEventTypes() *ListWebhookEventTypes { return &ListWebhookEventTypes{} }

// Handle returns every event type a subscription may name.
func (*ListWebhookEventTypes) Handle() []governance.EventType { return governance.EventCatalogue() }

// ListWebhooks answers a project's subscriptions.
type ListWebhooks struct{ store WebhookStore }

// NewListWebhooks returns the query.
func NewListWebhooks(store WebhookStore) *ListWebhooks { return &ListWebhooks{store: store} }

// Handle returns project's subscriptions, the oldest first.
func (h *ListWebhooks) Handle(ctx context.Context, project workspace.ProjectID) ([]governance.Webhook, error) {
	return h.store.Webhooks(ctx, project)
}

// GetWebhook answers one subscription.
type GetWebhook struct{ store WebhookStore }

// NewGetWebhook returns the query.
func NewGetWebhook(store WebhookStore) *GetWebhook { return &GetWebhook{store: store} }

// Handle returns project's subscription with webhook, or not_found.
func (h *GetWebhook) Handle(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID) (governance.Webhook, error) {
	return webhookOf(ctx, h.store, project, webhook)
}

// CreateWebhookCommand asks for a subscription of Project.
type CreateWebhookCommand struct {
	Project workspace.ProjectID
	Input   governance.WebhookInput
	// CreatedBy is the member or project token asking, nil for neither.
	CreatedBy *governance.Author
	Request   governance.Request
}

// CreatedWebhook is a subscription just made, with its secret, shown this
// once.
type CreatedWebhook struct {
	Webhook governance.Webhook
	Secret  secret.String
}

// CreateWebhook creates a webhook subscription.
type CreateWebhook struct{ d WebhookDeps }

// NewCreateWebhook returns the handler.
func NewCreateWebhook(d WebhookDeps) *CreateWebhook { return &CreateWebhook{d: d} }

// Handle checks the subscription and its target, draws its secret, stores
// it sealed and records webhook.created with the secret redacted.
func (h *CreateWebhook) Handle(ctx context.Context, cmd CreateWebhookCommand) (CreatedWebhook, error) {
	in, err := h.d.check(ctx, cmd.Project, cmd.Input)
	if err != nil {
		return CreatedWebhook{}, err
	}
	if err = h.d.checkTarget(ctx, in.URL); err != nil {
		return CreatedWebhook{}, err
	}
	value, err := governance.NewWebhookSecret(h.d.Random)
	if err != nil {
		return CreatedWebhook{}, err
	}
	sealed, err := h.d.Sealer.Seal(secret.PurposeWebhookSecret, []byte(value.Reveal()))
	if err != nil {
		return CreatedWebhook{}, err
	}
	now := h.d.now()
	var out CreatedWebhook
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		// Checked again under the lock: an environment may have gone since.
		in, err := h.d.check(ctx, cmd.Project, in)
		if err != nil {
			return err
		}
		w := governance.Webhook{
			ID: id.New[governance.Webhook](h.d.IDs), Project: cmd.Project, WebhookInput: in, SealedSecret: sealed,
			CreatedBy: cmd.CreatedBy, EnabledAt: now, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		if err := h.d.Store.CreateWebhook(ctx, w); err != nil {
			return err
		}
		if err := h.d.record(ctx, now, cmd.Request, "webhook.created", nil, &w); err != nil {
			return err
		}
		if w, err = webhookOf(ctx, h.d.Store, cmd.Project, w.ID); err != nil {
			return err
		}
		out = CreatedWebhook{Webhook: w, Secret: value}
		return nil
	})
	return out, err
}

// UpdateWebhookCommand is an edit of the subscription Webhook of Project,
// based on its version Expected.
type UpdateWebhookCommand struct {
	Project  workspace.ProjectID
	Webhook  governance.WebhookID
	Change   governance.WebhookChange
	Expected precondition.Version
	Request  governance.Request
}

// UpdateWebhook edits a webhook subscription.
type UpdateWebhook struct{ d WebhookDeps }

// NewUpdateWebhook returns the handler.
func NewUpdateWebhook(d WebhookDeps) *UpdateWebhook { return &UpdateWebhook{d: d} }

// Handle applies the change, checking a new target as creation does, and
// records webhook.updated; an edit that changes nothing stores and records
// nothing.
func (h *UpdateWebhook) Handle(ctx context.Context, cmd UpdateWebhookCommand) (governance.Webhook, error) {
	seen, err := webhookOf(ctx, h.d.Store, cmd.Project, cmd.Webhook)
	if err != nil {
		return seen, err
	}
	in, err := h.d.check(ctx, cmd.Project, seen.With(cmd.Change))
	if err != nil {
		return seen, err
	}
	if in.URL != seen.URL {
		if err = h.d.checkTarget(ctx, in.URL); err != nil {
			return seen, err
		}
	}
	now := h.d.now()
	var out governance.Webhook
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := webhookOf(ctx, h.d.Store, cmd.Project, cmd.Webhook)
		if err != nil {
			return err
		}
		in, err := h.d.check(ctx, cmd.Project, before.With(cmd.Change))
		if err != nil {
			return err
		}
		// The target is the one checked above when the change names one, and
		// otherwise the stored one, checked when it was written.
		if err := precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		out = before
		if in.Same(before.WebhookInput) {
			return nil
		}
		after := before
		after.WebhookInput = in
		if in.Enabled && !before.Enabled {
			after.EnabledAt = now
		}
		after.UpdatedAt, after.Version = now, before.Version+1
		if err := h.d.Store.UpdateWebhook(ctx, after); err != nil {
			return err
		}
		if err := h.d.record(ctx, now, cmd.Request, "webhook.updated", &before, &after); err != nil {
			return err
		}
		out, err = webhookOf(ctx, h.d.Store, cmd.Project, cmd.Webhook)
		return err
	})
	return out, err
}

// DeleteWebhookCommand asks for the subscription Webhook of Project to go.
type DeleteWebhookCommand struct {
	Project workspace.ProjectID
	Webhook governance.WebhookID
	Request governance.Request
}

// DeleteWebhook deletes a webhook subscription.
type DeleteWebhook struct{ d WebhookDeps }

// NewDeleteWebhook returns the handler.
func NewDeleteWebhook(d WebhookDeps) *DeleteWebhook { return &DeleteWebhook{d: d} }

// Handle deletes the subscription and records webhook.deleted.
func (h *DeleteWebhook) Handle(ctx context.Context, cmd DeleteWebhookCommand) error {
	now := h.d.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := webhookOf(ctx, h.d.Store, cmd.Project, cmd.Webhook)
		if err != nil {
			return err
		}
		if err := h.d.Store.DeleteWebhook(ctx, cmd.Project, cmd.Webhook); err != nil {
			return err
		}
		return h.d.record(ctx, now, cmd.Request, "webhook.deleted", &before, nil)
	})
}
