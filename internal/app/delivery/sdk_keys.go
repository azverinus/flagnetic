package delivery

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// SDKKeyStore keeps environments' SDK keys.
type SDKKeyStore interface {
	// SDKKeyEnvironment returns the identifier of the environment of
	// project keyed key, locking its row for the unit of work when lock is
	// set, so the keys of one environment change one at a time; or
	// ErrNotFound.
	SDKKeyEnvironment(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.EnvironmentID, error)
	// SDKKeys returns every key of environment, the oldest first.
	SDKKeys(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.SDKKey, error)
	// SDKKey returns environment's key identified by key, locking its row
	// when lock is set, or ErrNotFound.
	SDKKey(ctx context.Context, environment workspace.EnvironmentID, key delivery.SDKKeyID, lock bool) (delivery.SDKKey, error)
	// ActiveSDKKeys counts environment's keys of kind neither rotated nor
	// revoked.
	ActiveSDKKeys(ctx context.Context, environment workspace.EnvironmentID, kind delivery.SDKKeyKind) (int, error)
	CreateSDKKey(ctx context.Context, k delivery.SDKKey) error
	// UpdateSDKKey stores a key's name, allowed origins, expiry and
	// revocation.
	UpdateSDKKey(ctx context.Context, k delivery.SDKKey) error
}

// SDKKeyState is a key with its status at the time it was read.
type SDKKeyState struct {
	delivery.SDKKey
	Status delivery.SDKKeyStatus
}

func stateAt(k delivery.SDKKey, t time.Time) SDKKeyState {
	return SDKKeyState{SDKKey: k, Status: k.StatusAt(t)}
}

// CreatedSDKKey is a key just made, with its value, shown this once.
type CreatedSDKKey struct {
	Key   SDKKeyState
	Value secret.String
}

// SDKKeyDeps are what the SDK key use cases run on.
type SDKKeyDeps struct {
	Tx    Transactor
	Store SDKKeyStore
	Audit AuditLog
	Clock clock.Clock
	IDs   *id.Source
	// Random draws key values.
	Random io.Reader
}

// SDKKeyRef names the environment keyed Environment of Project.
type SDKKeyRef struct {
	Project     workspace.ProjectID
	Environment string
}

var (
	// ErrSDKKeyNotFound answers a key the environment does not hold, or an
	// identifier that names none.
	ErrSDKKeyNotFound = apperrors.NotFound("not_found", "the environment has no SDK key with this identifier")
)

// environment returns the identifier of ref's environment, locked when
// lock is set.
func environment(ctx context.Context, store SDKKeyStore, ref SDKKeyRef, lock bool) (workspace.EnvironmentID, error) {
	e, err := store.SDKKeyEnvironment(ctx, ref.Project, ref.Environment, lock)
	if errors.Is(err, ErrNotFound) {
		return workspace.EnvironmentID{}, errEnvironmentNotFound
	}
	return e, err
}

// lockedKey returns the key of ref's environment identified by key with
// both rows locked.
func lockedKey(ctx context.Context, store SDKKeyStore, ref SDKKeyRef, key delivery.SDKKeyID) (delivery.SDKKey, error) {
	e, err := environment(ctx, store, ref, true)
	if err != nil {
		return delivery.SDKKey{}, err
	}
	k, err := store.SDKKey(ctx, e, key, true)
	if errors.Is(err, ErrNotFound) {
		return delivery.SDKKey{}, ErrSDKKeyNotFound
	}
	return k, err
}

// ListSDKKeys answers an environment's SDK keys.
type ListSDKKeys struct {
	store SDKKeyStore
	clock clock.Clock
}

// NewListSDKKeys returns the query.
func NewListSDKKeys(store SDKKeyStore, c clock.Clock) *ListSDKKeys {
	return &ListSDKKeys{store: store, clock: c}
}

// Handle returns every key of ref's environment, the oldest first, each
// with its status now.
func (h *ListSDKKeys) Handle(ctx context.Context, ref SDKKeyRef) ([]SDKKeyState, error) {
	e, err := environment(ctx, h.store, ref, false)
	if err != nil {
		return nil, err
	}
	keys, err := h.store.SDKKeys(ctx, e)
	if err != nil {
		return nil, err
	}
	now := h.clock.Now()
	out := make([]SDKKeyState, len(keys))
	for i, k := range keys {
		out[i] = stateAt(k, now)
	}
	return out, nil
}

// CreateSDKKeyCommand asks for a key of Kind in Ref's environment.
type CreateSDKKeyCommand struct {
	Ref            SDKKeyRef
	Kind           delivery.SDKKeyKind
	Name           string
	AllowedOrigins []string
	Request        governance.Request
}

// CreateSDKKey creates an SDK key for an environment.
type CreateSDKKey struct{ d SDKKeyDeps }

// NewCreateSDKKey returns the handler.
func NewCreateSDKKey(d SDKKeyDeps) *CreateSDKKey { return &CreateSDKKey{d: d} }

// Handle creates the key, unless its environment already holds the most
// active keys of its kind, and records sdk_key.created without its value.
func (h *CreateSDKKey) Handle(ctx context.Context, cmd CreateSDKKeyCommand) (CreatedSDKKey, error) {
	now := h.d.Clock.Now()
	var out CreatedSDKKey
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// The environment's row lock makes two creations count the active
		// keys one after the other, so neither passes the limit unseen.
		e, err := environment(ctx, h.d.Store, cmd.Ref, true)
		if err != nil {
			return err
		}
		k, value, err := delivery.NewSDKKey(id.New[delivery.SDKKey](h.d.IDs), delivery.NewSDKKeyInput{
			Project: cmd.Ref.Project, Environment: e, Kind: cmd.Kind, Name: cmd.Name, AllowedOrigins: cmd.AllowedOrigins, Now: now,
		}, h.d.Random)
		if err != nil {
			return err
		}
		active, err := h.d.Store.ActiveSDKKeys(ctx, e, k.Kind)
		if err != nil {
			return err
		}
		if err = delivery.CheckActiveSDKKeys(active); err != nil {
			return err
		}
		if err = h.d.Store.CreateSDKKey(ctx, k); err != nil {
			return err
		}
		out = CreatedSDKKey{Key: stateAt(k, now), Value: value}
		return h.d.record(ctx, now, cmd.Request, k, "sdk_key.created", nil, sdkKeyDetail(k))
	})
	return out, err
}

// UpdateSDKKeyCommand is a change to the key Key of Ref's environment.
type UpdateSDKKeyCommand struct {
	Ref     SDKKeyRef
	Key     delivery.SDKKeyID
	Change  delivery.SDKKeyChange
	Request governance.Request
}

// UpdateSDKKey renames an SDK key and replaces its allowed origins.
type UpdateSDKKey struct{ d SDKKeyDeps }

// NewUpdateSDKKey returns the handler.
func NewUpdateSDKKey(d SDKKeyDeps) *UpdateSDKKey { return &UpdateSDKKey{d: d} }

// Handle applies the change, to a rotated or revoked key too, and records
// sdk_key.updated; a change to the values the key has stores and records
// nothing.
func (h *UpdateSDKKey) Handle(ctx context.Context, cmd UpdateSDKKeyCommand) (SDKKeyState, error) {
	now := h.d.Clock.Now()
	var out SDKKeyState
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockedKey(ctx, h.d.Store, cmd.Ref, cmd.Key)
		if err != nil {
			return err
		}
		after, changed, err := before.Update(cmd.Change)
		if err != nil {
			return err
		}
		out = stateAt(after, now)
		if !changed {
			return nil
		}
		if err := h.d.Store.UpdateSDKKey(ctx, after); err != nil {
			return err
		}
		return h.d.record(ctx, now, cmd.Request, after, "sdk_key.updated", sdkKeyDetail(before), sdkKeyDetail(after))
	})
	return out, err
}

// RotateSDKKeyCommand asks for the key Key of Ref's environment to be
// replaced, the old one ending Overlap after the rotation.
type RotateSDKKeyCommand struct {
	Ref     SDKKeyRef
	Key     delivery.SDKKeyID
	Overlap time.Duration
	Request governance.Request
}

// RotateSDKKey replaces an SDK key, keeping the old one working for an
// overlap.
type RotateSDKKey struct{ d SDKKeyDeps }

// NewRotateSDKKey returns the handler.
func NewRotateSDKKey(d SDKKeyDeps) *RotateSDKKey { return &RotateSDKKey{d: d} }

// Handle sets the rotated key's expiry and creates its replacement in one
// unit of work, and records sdk_key.rotated with the replacement and the
// overlap. The replacement takes the rotated key's place among the active
// keys, so the limit is not judged.
func (h *RotateSDKKey) Handle(ctx context.Context, cmd RotateSDKKeyCommand) (CreatedSDKKey, error) {
	now := h.d.Clock.Now()
	var out CreatedSDKKey
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockedKey(ctx, h.d.Store, cmd.Ref, cmd.Key)
		if err != nil {
			return err
		}
		rotated, replacement, value, err := before.Rotate(id.New[delivery.SDKKey](h.d.IDs), cmd.Overlap, now, h.d.Random)
		if err != nil {
			return err
		}
		if err = h.d.Store.UpdateSDKKey(ctx, rotated); err != nil {
			return err
		}
		if err = h.d.Store.CreateSDKKey(ctx, replacement); err != nil {
			return err
		}
		out = CreatedSDKKey{Key: stateAt(replacement, now), Value: value}
		after := sdkKeyDetail(rotated)
		after["replacement_id"] = replacement.ID.String()
		after["replacement_key_id"] = replacement.KeyID
		after["overlap_seconds"] = int64(cmd.Overlap / time.Second)
		return h.d.record(ctx, now, cmd.Request, rotated, "sdk_key.rotated", sdkKeyDetail(before), after)
	})
	return out, err
}

// RevokeSDKKeyCommand names the key Key of Ref's environment to revoke.
type RevokeSDKKeyCommand struct {
	Ref     SDKKeyRef
	Key     delivery.SDKKeyID
	Request governance.Request
}

// RevokeSDKKey revokes an SDK key.
type RevokeSDKKey struct{ d SDKKeyDeps }

// NewRevokeSDKKey returns the handler.
func NewRevokeSDKKey(d SDKKeyDeps) *RevokeSDKKey { return &RevokeSDKKey{d: d} }

// Handle revokes the key and records sdk_key.revoked; a revoked key is
// answered as it is, and nothing is recorded.
func (h *RevokeSDKKey) Handle(ctx context.Context, cmd RevokeSDKKeyCommand) (SDKKeyState, error) {
	now := h.d.Clock.Now()
	var out SDKKeyState
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockedKey(ctx, h.d.Store, cmd.Ref, cmd.Key)
		if err != nil {
			return err
		}
		after, changed := before.Revoke(now)
		out = stateAt(after, now)
		if !changed {
			return nil
		}
		if err := h.d.Store.UpdateSDKKey(ctx, after); err != nil {
			return err
		}
		return h.d.record(ctx, now, cmd.Request, after, "sdk_key.revoked", sdkKeyDetail(before), sdkKeyDetail(after))
	})
	return out, err
}

// record writes the audit entry of action on k, made at now in req.
func (d SDKKeyDeps) record(ctx context.Context, now time.Time, req governance.Request, k delivery.SDKKey, action string,
	before, after map[string]any,
) error {
	r := governance.Record{
		Project: k.Project, Environment: k.Environment, Action: action,
		Entity: governance.Entity{Kind: "sdk_key", ID: k.ID.String(), Key: k.KeyID},
		After:  after,
	}
	if before != nil {
		r.Before = before
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

// sdkKeyDetail is what the audit log keeps of a key: its key ID, never its
// value or hash.
func sdkKeyDetail(k delivery.SDKKey) map[string]any {
	return map[string]any{
		"kind": k.Kind, "name": k.Name, "key_id": k.KeyID, "allowed_origins": k.AllowedOrigins,
		"expires_at": k.ExpiresAt, "revoked_at": k.RevokedAt,
	}
}
