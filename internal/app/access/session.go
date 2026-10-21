package access

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SessionStore finds and updates sessions.
type SessionStore interface {
	// SessionByHash returns the session stored under hash with its member;
	// ErrNotFound when none is.
	SessionByHash(ctx context.Context, hash access.SessionHash) (access.Session, access.Member, error)
	TouchSession(ctx context.Context, session access.SessionID, at time.Time) error
	RevokeSession(ctx context.Context, session access.SessionID, at time.Time) error
}

// ErrNoSession answers a cookie that is not one the server issued: it is no
// credential at all, dropped before any lookup.
var ErrNoSession = errors.New("access: not a session cookie")

// AuthenticateSession resolves a session cookie into the member signed in.
type AuthenticateSession struct {
	store    SessionStore
	clock    clock.Clock
	key      []byte
	timeouts access.SessionTimeouts
}

// NewAuthenticateSession returns the handler, which checks cookies' tags
// under key.
func NewAuthenticateSession(store SessionStore, c clock.Clock, key []byte) *AuthenticateSession {
	return &AuthenticateSession{store: store, clock: c, key: key, timeouts: access.DefaultSessionTimeouts}
}

// Handle returns the caller signed in to the session cookie names: ErrNoSession
// for a cookie the server did not issue, found without a lookup, and
// session_expired for a session that no longer signs anyone in. A live
// session's last request is written at most once a minute.
func (h *AuthenticateSession) Handle(ctx context.Context, cookie string) (access.Caller, error) {
	hash, ok := access.ParseSessionCookie(cookie, h.key)
	if !ok {
		return access.Caller{}, ErrNoSession
	}
	session, member, err := h.store.SessionByHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return access.Caller{}, access.ErrSessionExpired
	}
	if err != nil {
		return access.Caller{}, err
	}
	now := h.clock.Now()
	if !session.Live(now, member, h.timeouts) {
		return access.Caller{}, access.ErrSessionExpired
	}
	if session.NeedsTouch(now) {
		if err := h.store.TouchSession(ctx, session.ID, now); err != nil {
			return access.Caller{}, err
		}
		session.LastSeenAt = now
	}
	return access.SessionCaller(session, member), nil
}

// SignOut ends the session a caller is signed in to.
type SignOut struct {
	tx    Transactor
	store SessionStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewSignOut returns the handler.
func NewSignOut(tx Transactor, store SessionStore, audit AuditLog, c clock.Clock, ids *id.Source) *SignOut {
	return &SignOut{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle revokes the caller's session and records the sign-out. A caller
// presenting an API token has no session to end and is refused, so a script
// never believes it ended something.
func (h *SignOut) Handle(ctx context.Context, caller access.Caller, requestID string) error {
	if caller.Session == nil || caller.Member == nil {
		return access.ErrSessionRequired
	}
	now := h.clock.Now()
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, governance.Request{
		Actor: ActorOf(caller), ID: requestID,
	}, signInRecord("auth.signed_out", *caller.Member, nil))
	if err != nil {
		return err
	}
	return h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.RevokeSession(ctx, caller.Session.ID, now); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	})
}
