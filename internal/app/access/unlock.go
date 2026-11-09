package access

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// UnlockStore is what unlocking the instance needs.
type UnlockStore interface {
	AuthSettingsStore
	// ClearAllSignInFailures forgets every failed sign-in counted, per email and
	// per source, and returns how many emails were locked out at the moment
	// at, each counted once.
	ClearAllSignInFailures(ctx context.Context, at time.Time) (int, error)
}

// Unlock opens the instance again to everybody who signs in with a
// password, from the command line: what an operator does when a broken auth
// provider, or settings sending everyone to it, keep every member out.
type Unlock struct {
	tx    Transactor
	store UnlockStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewUnlock returns the handler.
func NewUnlock(tx Transactor, store UnlockStore, audit AuditLog, c clock.Clock, ids *id.Source) *Unlock {
	return &Unlock{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Unlocked is what an unlock undid.
type Unlocked struct {
	// LocalLoginWasDisabled is whether local login was off.
	LocalLoginWasDisabled bool
	// DomainsLifted counts the enforced domains lifted.
	DomainsLifted int
	// LockoutsCleared counts the emails that were locked out, alone or from a
	// source, whose failures are now forgotten.
	LockoutsCleared int
}

// Handle enables local login, lifts every enforced domain and forgets every
// failed sign-in, of emails and of sources alike, in one unit of work, and
// records auth_settings.unlocked with the settings before and after and the
// lockouts cleared. It records the same when there was nothing to undo, so
// the trail shows every use of the break-glass. Every replica applies the
// change once the notification reaches it.
func (h *Unlock) Handle(ctx context.Context, req governance.Request) (Unlocked, error) {
	var out Unlocked
	now := h.clock.Now()
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.LockAuthSettings(ctx)
		if err != nil {
			return err
		}
		after := before
		after.LocalLoginEnabled, after.SSOEnforcedDomains = true, nil
		if err = h.store.SaveAuthSettings(ctx, after); err != nil {
			return err
		}
		cleared, err := h.store.ClearAllSignInFailures(ctx, now)
		if err != nil {
			return err
		}
		out = Unlocked{LocalLoginWasDisabled: !before.LocalLoginEnabled, DomainsLifted: len(before.SSOEnforcedDomains), LockoutsCleared: cleared}
		record := authSettingsRecord(after)
		record["lockouts_cleared"] = cleared
		return recordAll(ctx, h.audit, h.ids, now, req, governance.Record{
			Action: "auth_settings.unlocked", Entity: authSettingsEntity,
			Before: authSettingsRecord(before), After: record,
		})
	})
	return out, err
}
