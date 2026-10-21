package access

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MemberSessionStore reads a member's sessions.
type MemberSessionStore interface {
	// MemberByID returns the member with the identifier; ErrNotFound when
	// none has it.
	MemberByID(ctx context.Context, member access.MemberID) (access.Member, error)
	// MemberSessions returns the sessions of member not yet revoked, the
	// newest first.
	MemberSessions(ctx context.Context, member access.MemberID) ([]access.Session, error)
}

// SessionRevoker revokes sessions.
type SessionRevoker interface {
	MemberSessionStore
	// RevokeSessions ends each of sessions not ended yet at at, and returns
	// those it ended.
	RevokeSessions(ctx context.Context, sessions []access.SessionID, at time.Time) ([]access.SessionID, error)
}

// ErrMemberNotFound answers an identifier no member has.
var ErrMemberNotFound = apperrors.NotFound("not_found", "no member has this identifier")

// ErrSessionNotFound answers a session that is not a live one of the member
// addressed: another member's, one that has ended, or none at all.
var ErrSessionNotFound = apperrors.NotFound("not_found", "no live session of the member has this identifier")

// SessionView is a live session as its member sees it.
type SessionView struct {
	access.Session
	// End is when the absolute timeout in force ends the session.
	End time.Time
	// Current marks the session the request is signed in to.
	Current bool
}

// ListSessions answers the devices a member is signed in on.
type ListSessions struct {
	store    MemberSessionStore
	clock    clock.Clock
	timeouts access.SessionTimeouts
}

// NewListSessions returns the query.
func NewListSessions(store MemberSessionStore, c clock.Clock) *ListSessions {
	return &ListSessions{store: store, clock: c, timeouts: access.DefaultSessionTimeouts}
}

// Handle returns the live sessions of member, the caller's own when member
// is zero, the newest first; the one caller is signed in to is marked
// current. An unknown member is ErrMemberNotFound.
func (h *ListSessions) Handle(ctx context.Context, caller access.Caller, member access.MemberID) ([]SessionView, error) {
	holder, err := holderOf(ctx, h.store, caller, member)
	if err != nil {
		return nil, err
	}
	sessions, err := h.store.MemberSessions(ctx, holder.ID)
	if err != nil {
		return nil, err
	}
	live := access.LiveSessions(sessions, holder, h.clock.Now(), h.timeouts)
	views := make([]SessionView, len(live))
	for i, s := range live {
		views[i] = SessionView{Session: s, End: s.End(h.timeouts), Current: caller.Session != nil && caller.Session.ID == s.ID}
	}
	return views, nil
}

// holderOf returns the member whose sessions are addressed: member, or the
// caller's own when it is zero.
func holderOf(ctx context.Context, store MemberSessionStore, caller access.Caller, member access.MemberID) (access.Member, error) {
	if member.IsZero() {
		if caller.Member == nil {
			return access.Member{}, access.ErrSessionRequired
		}
		return *caller.Member, nil
	}
	m, err := store.MemberByID(ctx, member)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, ErrMemberNotFound
	}
	return m, err
}

// RevokeSessionsCommand selects the sessions to revoke: every live one of
// Member, or of the caller when Member is zero, or only Session when given;
// the session the caller is signed in to is kept when KeepCurrent is set.
type RevokeSessionsCommand struct {
	Caller      access.Caller
	Member      access.MemberID
	Session     *access.SessionID
	KeepCurrent bool
	Request     governance.Request
}

// RevokeSessionsResult is what a revocation ended.
type RevokeSessionsResult struct {
	Revoked []access.SessionID
	// CurrentRevoked is whether the session the caller is signed in to was
	// among them, so its cookie is cleared.
	CurrentRevoked bool
}

// RevokeSessions signs a member out of some or all of their devices.
type RevokeSessions struct {
	tx       Transactor
	store    SessionRevoker
	audit    AuditLog
	clock    clock.Clock
	ids      *id.Source
	timeouts access.SessionTimeouts
}

// NewRevokeSessions returns the handler.
func NewRevokeSessions(tx Transactor, store SessionRevoker, audit AuditLog, c clock.Clock, ids *id.Source) *RevokeSessions {
	return &RevokeSessions{tx: tx, store: store, audit: audit, clock: c, ids: ids, timeouts: access.DefaultSessionTimeouts}
}

// Handle revokes the live sessions cmd selects and records one
// session.revoked entry naming them; revoking none records nothing. A
// member revokes their own sessions only from a session, so a leaked token
// cannot sign its owner out; a session the member does not hold live is
// ErrSessionNotFound.
func (h *RevokeSessions) Handle(ctx context.Context, cmd RevokeSessionsCommand) (RevokeSessionsResult, error) {
	if cmd.Member.IsZero() && cmd.Caller.Session == nil {
		return RevokeSessionsResult{}, access.ErrSessionRequired
	}
	var res RevokeSessionsResult
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		holder, err := holderOf(ctx, h.store, cmd.Caller, cmd.Member)
		if err != nil {
			return err
		}
		sessions, err := h.store.MemberSessions(ctx, holder.ID)
		if err != nil {
			return err
		}
		now := h.clock.Now()
		chosen, err := cmd.choose(access.LiveSessions(sessions, holder, now, h.timeouts))
		if err != nil || len(chosen) == 0 {
			return err
		}
		if res.Revoked, err = h.store.RevokeSessions(ctx, chosen, now); err != nil || len(res.Revoked) == 0 {
			return err
		}
		res.CurrentRevoked = cmd.Caller.Session != nil && slices.Contains(res.Revoked, cmd.Caller.Session.ID)
		entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, sessionsRevoked(holder, res.Revoked))
		if err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	})
	if err != nil {
		return RevokeSessionsResult{}, err
	}
	return res, nil
}

// choose returns the identifiers of the live sessions cmd selects.
func (cmd RevokeSessionsCommand) choose(live []access.Session) ([]access.SessionID, error) {
	var chosen []access.SessionID
	for _, s := range live {
		switch {
		case cmd.Session != nil && s.ID != *cmd.Session:
		case cmd.KeepCurrent && cmd.Caller.Session != nil && s.ID == cmd.Caller.Session.ID:
		default:
			chosen = append(chosen, s.ID)
		}
	}
	if cmd.Session != nil && len(chosen) == 0 {
		return nil, ErrSessionNotFound
	}
	return chosen, nil
}

// sessionsRevoked is the audit record of revoking sessions of holder, named
// in identifier order.
func sessionsRevoked(holder access.Member, sessions []access.SessionID) governance.Record {
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.String()
	}
	slices.SortFunc(names, strings.Compare)
	return governance.Record{
		Action: "session.revoked",
		Entity: memberEntity(holder),
		After:  map[string]any{"session_ids": names},
	}
}
