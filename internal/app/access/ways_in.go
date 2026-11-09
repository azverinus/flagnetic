package access

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// IdentityDirectory is what the access context asks of the identity context
// about the identities members hold.
type IdentityDirectory interface {
	// MemberIdentities returns the identities member holds, whether or not
	// their auth provider is enabled.
	MemberIdentities(ctx context.Context, member access.MemberID) ([]access.IdentityMethod, error)
}

// WaysInStore is what telling a member's ways in needs of the stored
// members.
type WaysInStore interface {
	// LockMember returns the member, locked until the unit of work ends, and
	// whether they hold a password; ErrNotFound when none has the identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
	// PasswordSetAt returns when the member set the password they hold, nil
	// when they hold none.
	PasswordSetAt(ctx context.Context, member access.MemberID) (*time.Time, error)
}

// WaysIn tells the ways a member holds to sign in. Every change that could
// remove one asks it under the member's lock, so two changes made at once
// each see the other's outcome and cannot together leave a member without.
type WaysIn struct {
	store      WaysInStore
	identities IdentityDirectory
	settings   AuthSettingsReader
}

// NewWaysIn returns the service; settings tell which ways in local login and
// enforcement leave usable.
func NewWaysIn(store WaysInStore, identities IdentityDirectory, settings AuthSettingsReader) *WaysIn {
	return &WaysIn{store: store, identities: identities, settings: settings}
}

// Lock locks the member until the unit of work ends and returns them;
// ErrNotFound when none has the identifier.
func (w *WaysIn) Lock(ctx context.Context, member access.MemberID) (access.Member, error) {
	m, _, err := w.store.LockMember(ctx, member)
	return m, err
}

// PasswordSetAt returns when the member set the password they hold, nil when
// they hold none.
func (w *WaysIn) PasswordSetAt(ctx context.Context, member access.MemberID) (*time.Time, error) {
	return w.store.PasswordSetAt(ctx, member)
}

// Methods returns the ways m holds to sign in, judged under the auth
// settings in force for their email.
func (w *WaysIn) Methods(ctx context.Context, m access.Member) (access.AuthMethods, error) {
	setAt, err := w.store.PasswordSetAt(ctx, m.ID)
	if err != nil {
		return access.AuthMethods{}, err
	}
	identities, err := w.identities.MemberIdentities(ctx, m.ID)
	if err != nil {
		return access.AuthMethods{}, err
	}
	settings, err := w.settings.AuthSettings(ctx)
	if err != nil {
		return access.AuthMethods{}, err
	}
	return access.AuthMethods{Status: m.Status, PasswordSetAt: setAt, Identities: identities}.UnderSettings(settings, m.Email), nil
}

// EnsureWayIn refuses, with ErrLastAuthMethod, removing the password
// (withoutPassword) and unlinking the identity withoutIdentity ("" for none)
// when no other usable way in of m would remain.
func (w *WaysIn) EnsureWayIn(ctx context.Context, m access.Member, withoutPassword bool, withoutIdentity string) error {
	methods, err := w.Methods(ctx, m)
	if err != nil {
		return err
	}
	if !methods.KeepsWayIn(withoutPassword, withoutIdentity) {
		return access.ErrLastAuthMethod
	}
	return nil
}

// PasswordRemovalStore is what removing a password needs.
type PasswordRemovalStore interface {
	WaysInStore
	ClearPasswordHash(ctx context.Context, member access.MemberID, at time.Time) error
	ConsumeInvitations(ctx context.Context, member access.MemberID, at time.Time) (int, error)
	ConsumePasswordResets(ctx context.Context, member access.MemberID, at time.Time) (int, error)
}

// RemoveMyPassword lets a signed-in member drop their password.
type RemoveMyPassword struct {
	tx    Transactor
	store PasswordRemovalStore
	ways  *WaysIn
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewRemoveMyPassword returns the handler.
func NewRemoveMyPassword(tx Transactor, store PasswordRemovalStore, ways *WaysIn, audit AuditLog, c clock.Clock, ids *id.Source) *RemoveMyPassword {
	return &RemoveMyPassword{tx: tx, store: store, ways: ways, audit: audit, clock: c, ids: ids}
}

// RemoveMyPasswordCommand is one removal.
type RemoveMyPasswordCommand struct {
	Caller  access.Caller
	Request governance.Request
}

// Handle removes the caller's password, ends their open invitations and
// password resets so no old link sets one again, and records
// password.removed, in one unit of work. It is refused ErrLastAuthMethod when
// the password is a usable way in and no identity at an enabled auth provider
// that enforcement allows remains; a member who holds no password changes
// nothing. Only a session removes a password, so a leaked
// token cannot lock its owner out of signing in with it.
func (h *RemoveMyPassword) Handle(ctx context.Context, cmd RemoveMyPasswordCommand) error {
	if cmd.Caller.Member == nil || cmd.Caller.Session == nil {
		return access.ErrSessionRequired
	}
	return h.tx.Within(ctx, func(ctx context.Context) error {
		m, holdsPassword, err := h.store.LockMember(ctx, cmd.Caller.Member.ID)
		if err != nil {
			return err
		}
		if !holdsPassword {
			return nil
		}
		if err = h.ways.EnsureWayIn(ctx, m, true, ""); err != nil {
			return err
		}
		now := h.clock.Now()
		if err = h.store.ClearPasswordHash(ctx, m.ID, now); err != nil {
			return err
		}
		if _, err = h.store.ConsumeInvitations(ctx, m.ID, now); err != nil {
			return err
		}
		if _, err = h.store.ConsumePasswordResets(ctx, m.ID, now); err != nil {
			return err
		}
		return recordAll(ctx, h.audit, h.ids, now, cmd.Request, governance.Record{
			Action: "password.removed", Entity: memberEntity(m), After: map[string]any{"email": m.Email},
		})
	})
}
