package sinks

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// CreateCommand asks for a sink of Project.
type CreateCommand struct {
	Project workspace.ProjectID
	Draft   sinks.Draft
	// CreatedBy is the member or project token asking, nil for neither.
	CreatedBy *governance.Author
	Request   governance.Request
}

// Created is a sink just made or just given a new secret. Secret is the
// webhook sink's secret, shown this once; it is zero for the other kinds,
// whose secret the member supplied.
type Created struct {
	Sink   sinks.Sink
	Secret secret.String
}

// Create creates a sink.
type Create struct{ d Deps }

// NewCreate returns the handler.
func NewCreate(d Deps) *Create { return &Create{d: d} }

// Handle checks the sink and its target, seals its secret, which a webhook
// sink has the server draw, stores the sink disabled and records sink.created
// with the secret redacted.
func (h *Create) Handle(ctx context.Context, cmd CreateCommand) (Created, error) {
	draft, err := h.d.check(ctx, cmd.Project, cmd.Draft)
	if err != nil {
		return Created{}, err
	}
	if err = h.d.checkTarget(ctx, draft.Kind, draft.Config); err != nil {
		return Created{}, err
	}
	var drawn secret.String
	plaintext := draft.Secret
	if draft.Kind == sinks.KindWebhook {
		if drawn, err = governance.NewWebhookSecret(h.d.Random); err != nil {
			return Created{}, err
		}
		plaintext = drawn.Reveal()
	}
	sealed, err := h.d.Sealer.Seal(draft.Kind.SecretPurpose(), []byte(plaintext))
	if err != nil {
		return Created{}, err
	}
	now := h.d.now()
	out := Created{Secret: drawn}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		out.Sink, txErr = h.create(ctx, cmd, draft, sealed, now)
		return txErr
	})
	return out, err
}

// create stores the sink under the project's lock, and records it.
func (h *Create) create(ctx context.Context, cmd CreateCommand, draft sinks.Draft, sealed string, now time.Time) (sinks.Sink, error) {
	if err := h.d.lockProject(ctx, cmd.Project); err != nil {
		return sinks.Sink{}, err
	}
	// Checked again under the lock: an environment may have gone since.
	draft, err := h.d.check(ctx, cmd.Project, draft)
	if err != nil {
		return sinks.Sink{}, err
	}
	s := sinks.Sink{
		ID: id.New[sinks.Sink](h.d.IDs), Project: cmd.Project, Input: draft.Input, SealedSecret: sealed,
		Warnings: []string{}, CreatedBy: cmd.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	// A sink starts disabled; enabling probes its target first.
	s.Enabled = false
	if err = h.d.Store.Create(ctx, s); err != nil {
		return s, err
	}
	if err = h.d.record(ctx, now, cmd.Request, "sink.created", nil, &s); err != nil {
		return s, err
	}
	return sinkOf(ctx, h.d.Store, cmd.Project, s.ID)
}

// UpdateCommand is an edit of the sink Sink of Project, based on its version
// Expected.
type UpdateCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	Change  sinks.Change
	// Secret is the ClickHouse password or object storage secret access key
	// the edit sent: empty or sinks.Masked keeps the stored one.
	Secret   string
	Expected precondition.Version
	Request  governance.Request
}

// Update edits a sink.
type Update struct{ d Deps }

// NewUpdate returns the handler.
func NewUpdate(d Deps) *Update { return &Update{d: d} }

// Handle applies the change, which replaces a configuration whole, checking
// a new target as creation does, and records sink.updated; an edit that
// changes nothing stores and records nothing.
func (h *Update) Handle(ctx context.Context, cmd UpdateCommand) (sinks.Sink, error) {
	seen, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return seen, err
	}
	draft, err := h.draft(ctx, cmd, seen)
	if err != nil {
		return seen, err
	}
	if !draft.Config.Equal(seen.Config) {
		if err = h.d.checkTarget(ctx, draft.Kind, draft.Config); err != nil {
			return seen, err
		}
	}
	var sealed string
	if !draft.KeepsSecret {
		if sealed, err = h.d.Sealer.Seal(draft.Kind.SecretPurpose(), []byte(draft.Secret)); err != nil {
			return seen, err
		}
	}
	now := h.d.now()
	var out sinks.Sink
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		out, txErr = h.update(ctx, cmd, sealed, now)
		return txErr
	})
	return out, err
}

// draft returns the edit applied to s and checked.
func (h *Update) draft(ctx context.Context, cmd UpdateCommand, s sinks.Sink) (sinks.Draft, error) {
	in, err := s.With(cmd.Change)
	if err != nil {
		return sinks.Draft{}, err
	}
	return h.d.check(ctx, cmd.Project, sinks.Draft{
		Input: in, Secret: cmd.Secret, KeepsSecret: sinks.KeepsSecret(cmd.Secret),
	}, keptEnvironments(s, cmd.Change)...)
}

// update applies cmd's change under the project's lock. The target is the
// one Handle checked when the change names one, and otherwise the stored
// one, checked when it was written.
func (h *Update) update(ctx context.Context, cmd UpdateCommand, sealed string, now time.Time) (sinks.Sink, error) {
	if err := h.d.lockProject(ctx, cmd.Project); err != nil {
		return sinks.Sink{}, err
	}
	before, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return before, err
	}
	draft, err := h.draft(ctx, cmd, before)
	if err != nil {
		return before, err
	}
	if err = precondition.Check(cmd.Expected, before.Version); err != nil {
		return before, err
	}
	if draft.KeepsSecret && draft.Same(before.Input) {
		return before, nil
	}
	after := before
	after.Input = draft.Input
	if !draft.KeepsSecret {
		after.SealedSecret = sealed
	}
	after.UpdatedAt, after.Version = now, before.Version+1
	if err = h.d.Store.Update(ctx, after); err != nil {
		return after, err
	}
	if err = h.d.record(ctx, now, cmd.Request, "sink.updated", &before, &after); err != nil {
		return after, err
	}
	return sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
}

// DeleteCommand asks for the sink Sink of Project to go, based on its
// version Expected.
type DeleteCommand struct {
	Project  workspace.ProjectID
	Sink     sinks.ID
	Expected precondition.Version
	Request  governance.Request
}

// Delete deletes a sink.
type Delete struct{ d Deps }

// NewDelete returns the handler.
func NewDelete(d Deps) *Delete { return &Delete{d: d} }

// Handle deletes the sink and records sink.deleted.
func (h *Delete) Handle(ctx context.Context, cmd DeleteCommand) error {
	now := h.d.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		if err = h.d.Store.Delete(ctx, cmd.Project, cmd.Sink); err != nil {
			return err
		}
		return h.d.record(ctx, now, cmd.Request, "sink.deleted", &before, nil)
	})
}

// RotateCommand asks for the webhook sink Sink of Project to be given a new
// secret.
type RotateCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	Request governance.Request
}

// RotateSecret issues a webhook sink a new secret.
type RotateSecret struct{ d Deps }

// NewRotateSecret returns the handler.
func NewRotateSecret(d Deps) *RotateSecret { return &RotateSecret{d: d} }

// Handle draws the sink a new secret, stores it sealed with the one it
// replaces signing beside it for the overlap, and records sink.secret_rotated
// with both redacted. The new secret is returned this once. A sink of
// another kind has none to rotate (no_secret_to_rotate).
func (h *RotateSecret) Handle(ctx context.Context, cmd RotateCommand) (Created, error) {
	value, err := governance.NewWebhookSecret(h.d.Random)
	if err != nil {
		return Created{}, err
	}
	sealed, err := h.d.Sealer.Seal(sinks.KindWebhook.SecretPurpose(), []byte(value.Reveal()))
	if err != nil {
		return Created{}, err
	}
	now := h.d.now()
	out := Created{Secret: value}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if txErr := h.d.lockProject(ctx, cmd.Project); txErr != nil {
			return txErr
		}
		before, txErr := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if txErr != nil {
			return txErr
		}
		if before.Kind != sinks.KindWebhook {
			return errNoSecretToRotate
		}
		after := before.RotateSecret(sealed, now, h.d.SecretOverlap)
		if txErr = h.d.Store.RotateSecret(ctx, after); txErr != nil {
			return txErr
		}
		if txErr = h.d.record(ctx, now, cmd.Request, "sink.secret_rotated", &before, &after); txErr != nil {
			return txErr
		}
		out.Sink, txErr = sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		return txErr
	})
	return out, err
}
