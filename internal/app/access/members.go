package access

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MemberView is a member as an instance admin sees them.
type MemberView struct {
	access.Member
	// LastSignInAt is when the member last signed in; nil until they do.
	LastSignInAt *time.Time
	// ProjectCount is how many projects the member holds a grant in.
	ProjectCount int
}

// MemberFilter narrows the members listed: to one status when Status is
// not "", to those whose email or display name holds Q whatever its case
// when Q is not "".
type MemberFilter struct {
	Status access.MemberStatus
	Q      string
}

// MemberDirectory reads members as an instance admin sees them.
type MemberDirectory interface {
	// Members returns the members f selects, ordered by email, with their
	// project counts read in the same query.
	Members(ctx context.Context, f MemberFilter) ([]MemberView, error)
	// MemberView returns one member; ErrNotFound when none has the
	// identifier.
	MemberView(ctx context.Context, member access.MemberID) (MemberView, error)
}

// MemberChanger changes a member under the lock that keeps the instance an
// active instance admin.
type MemberChanger interface {
	MemberDirectory
	// LockInstanceAdmins serializes, until the unit of work ends, every
	// change that could leave the instance without an active instance
	// admin, so two of them made at once each see the other's outcome.
	LockInstanceAdmins(ctx context.Context) error
	// LockMember returns the member, locked until the unit of work ends,
	// and whether they hold a password; ErrNotFound when none has the
	// identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
	// OtherActiveInstanceAdmins counts the active instance admins other
	// than member.
	OtherActiveInstanceAdmins(ctx context.Context, member access.MemberID) (int, error)
	// SaveMember stores m's email, display name, status and instance
	// admin switch; an email another member holds is refused
	// already_exists.
	SaveMember(ctx context.Context, m access.Member, at time.Time) error
}

// ListMembers answers the instance's members.
type ListMembers struct{ store MemberDirectory }

// NewListMembers returns the query.
func NewListMembers(store MemberDirectory) *ListMembers { return &ListMembers{store: store} }

// Handle returns the members f selects, ordered by email.
func (h *ListMembers) Handle(ctx context.Context, f MemberFilter) ([]MemberView, error) {
	return h.store.Members(ctx, f)
}

// GetMember answers one member.
type GetMember struct{ store MemberDirectory }

// NewGetMember returns the query.
func NewGetMember(store MemberDirectory) *GetMember { return &GetMember{store: store} }

// Handle returns the member; ErrMemberNotFound when none has the
// identifier.
func (h *GetMember) Handle(ctx context.Context, member access.MemberID) (MemberView, error) {
	return viewOf(ctx, h.store, member)
}

func viewOf(ctx context.Context, store MemberDirectory, member access.MemberID) (MemberView, error) {
	v, err := store.MemberView(ctx, member)
	if errors.Is(err, ErrNotFound) {
		return MemberView{}, ErrMemberNotFound
	}
	return v, err
}

// changeMember runs change on member, locked, in a unit of work holding the
// instance admins' lock, and refuses its outcome when it would leave the
// instance without an active instance admin. change returns the member as
// changed and the audit records of the change; a member it returns
// unchanged is not stored and records nothing. The member is answered as
// the change left them.
func changeMember(ctx context.Context, tx Transactor, store MemberChanger, audit AuditLog, c clock.Clock, ids *id.Source,
	req governance.Request, member access.MemberID,
	change func(ctx context.Context, before access.Member, holdsPassword bool, now time.Time) (access.Member, []governance.Record, error),
) (MemberView, error) {
	var view MemberView
	err := tx.Within(ctx, func(ctx context.Context) error {
		if err := store.LockInstanceAdmins(ctx); err != nil {
			return err
		}
		before, holdsPassword, err := store.LockMember(ctx, member)
		if errors.Is(err, ErrNotFound) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		now := c.Now()
		after, records, err := change(ctx, before, holdsPassword, now)
		if err != nil {
			return err
		}
		if err = saveChanged(ctx, store, before, after, now); err != nil {
			return err
		}
		if err = recordAll(ctx, audit, ids, now, req, records...); err != nil {
			return err
		}
		view, err = viewOf(ctx, store, member)
		return err
	})
	return view, err
}

// saveChanged stores after unless it is before unchanged, once it leaves the
// instance an active instance admin.
func saveChanged(ctx context.Context, store MemberChanger, before, after access.Member, now time.Time) error {
	if after == before {
		return nil
	}
	if err := keepAnInstanceAdmin(ctx, store, before, after); err != nil {
		return err
	}
	return store.SaveMember(ctx, after, now)
}

// recordAll records an audit entry of each of records, made at now for
// req, in the unit of work ctx carries.
func recordAll(ctx context.Context, audit AuditLog, ids *id.Source, now time.Time, req governance.Request,
	records ...governance.Record,
) error {
	for _, r := range records {
		entry, err := governance.NewEntry(id.New[governance.Entry](ids), now, req, r)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// keepAnInstanceAdmin refuses changing before into after when it would
// leave the instance without an active instance admin; the others are
// counted only when it could.
func keepAnInstanceAdmin(ctx context.Context, store MemberChanger, before, after access.Member) error {
	if !before.IsActiveInstanceAdmin() || after.IsActiveInstanceAdmin() {
		return nil
	}
	others, err := store.OtherActiveInstanceAdmins(ctx, before.ID)
	if err != nil {
		return err
	}
	return access.KeepAnInstanceAdmin(before, after, others)
}

// UpdateMember edits a member for an instance admin.
type UpdateMember struct {
	tx    Transactor
	store MemberChanger
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewUpdateMember returns the handler.
func NewUpdateMember(tx Transactor, store MemberChanger, audit AuditLog, c clock.Clock, ids *id.Source) *UpdateMember {
	return &UpdateMember{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// UpdateMemberCommand is an edit of a member.
type UpdateMemberCommand struct {
	Member  access.MemberID
	Change  access.MemberChange
	Request governance.Request
}

// Handle applies the edit and records member.updated for a changed email
// or display name, and member.promoted or member.demoted for a changed
// instance admin switch; an edit changing nothing records nothing.
// Demoting the last active instance admin is refused last_instance_admin.
func (h *UpdateMember) Handle(ctx context.Context, cmd UpdateMemberCommand) (MemberView, error) {
	return changeMember(ctx, h.tx, h.store, h.audit, h.clock, h.ids, cmd.Request, cmd.Member,
		func(_ context.Context, before access.Member, _ bool, _ time.Time) (access.Member, []governance.Record, error) {
			after, err := before.Edited(cmd.Change)
			if err != nil {
				return access.Member{}, nil, err
			}
			return after, memberEdited(before, after), nil
		})
}

// memberEdited returns the audit records of editing before into after,
// each naming only the fields it changed.
func memberEdited(before, after access.Member) []governance.Record {
	var records []governance.Record
	was, is := map[string]any{}, map[string]any{}
	if before.Email != after.Email {
		was["email"], is["email"] = before.Email, after.Email
	}
	if before.DisplayName != after.DisplayName {
		was["display_name"], is["display_name"] = before.DisplayName, after.DisplayName
	}
	if len(is) > 0 {
		records = append(records, governance.Record{Action: "member.updated", Entity: memberEntity(after), Before: was, After: is})
	}
	if before.InstanceAdmin != after.InstanceAdmin {
		action := "member.demoted"
		if after.InstanceAdmin {
			action = "member.promoted"
		}
		records = append(records, governance.Record{
			Action: action, Entity: memberEntity(after),
			Before: map[string]any{"instance_admin": before.InstanceAdmin},
			After:  map[string]any{"instance_admin": after.InstanceAdmin},
		})
	}
	return records
}

// DeactivationStore is what offboarding a member needs.
type DeactivationStore interface {
	MemberChanger
	// RevokeMemberSessions ends every session of member not ended yet, and
	// returns how many it ended.
	RevokeMemberSessions(ctx context.Context, member access.MemberID, at time.Time) (int, error)
	// RevokePersonalTokens revokes every personal access token of member
	// not revoked yet, and returns how many it revoked. The project tokens
	// the member created belong to their projects and stay.
	RevokePersonalTokens(ctx context.Context, member access.MemberID, at time.Time) (int, error)
	InvitationStore
}

// DeactivateMember offboards a member.
type DeactivateMember struct {
	tx    Transactor
	store DeactivationStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewDeactivateMember returns the handler.
func NewDeactivateMember(tx Transactor, store DeactivationStore, audit AuditLog, c clock.Clock, ids *id.Source) *DeactivateMember {
	return &DeactivateMember{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle deactivates the member and, in the same unit of work, revokes
// every session and personal access token they hold and consumes their open
// invitations, recording member.deactivated with how many sessions and
// tokens it revoked. A deactivated member
// is left as they are; the last active instance admin is refused
// last_instance_admin.
func (h *DeactivateMember) Handle(ctx context.Context, member access.MemberID, req governance.Request) (MemberView, error) {
	return changeMember(ctx, h.tx, h.store, h.audit, h.clock, h.ids, req, member,
		func(ctx context.Context, before access.Member, _ bool, now time.Time) (access.Member, []governance.Record, error) {
			if before.Status == access.MemberDeactivated {
				return before, nil, nil
			}
			// A refusal of the outcome rolls the revocations back with it.
			after := before.Deactivated()
			sessions, err := h.store.RevokeMemberSessions(ctx, member, now)
			if err != nil {
				return access.Member{}, nil, err
			}
			tokens, err := h.store.RevokePersonalTokens(ctx, member, now)
			if err != nil {
				return access.Member{}, nil, err
			}
			if _, err := h.store.ConsumeInvitations(ctx, member, now); err != nil {
				return access.Member{}, nil, err
			}
			return after, []governance.Record{{
				Action: "member.deactivated", Entity: memberEntity(after),
				Before: map[string]any{"status": before.Status},
				After:  map[string]any{"status": after.Status, "sessions_revoked": sessions, "tokens_revoked": tokens},
			}}, nil
		})
}

// ReactivationStore is what bringing a member back needs.
type ReactivationStore interface {
	MemberChanger
	InvitationStore
}

// ReactivateMember brings a deactivated member back.
type ReactivateMember struct {
	tx     Transactor
	store  ReactivationStore
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewReactivateMember returns the handler; random draws invitation secrets.
func NewReactivateMember(tx Transactor, store ReactivationStore, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader,
) *ReactivateMember {
	return &ReactivateMember{tx: tx, store: store, audit: audit, clock: c, ids: ids, random: random}
}

// Handle makes a deactivated member active when they hold a password,
// recording member.reactivated. One who never set a password is invited
// again, with a fresh invitation, since the one they had was consumed when
// they were deactivated. What their deactivation revoked stays revoked. A
// member who is not deactivated is left as they are.
func (h *ReactivateMember) Handle(ctx context.Context, member access.MemberID, req governance.Request) (InvitedMember, error) {
	var issued *IssuedInvitation
	view, err := changeMember(ctx, h.tx, h.store, h.audit, h.clock, h.ids, req, member,
		func(ctx context.Context, before access.Member, holdsPassword bool, now time.Time) (access.Member, []governance.Record, error) {
			if before.Status != access.MemberDeactivated {
				return before, nil, nil
			}
			after := before.Reactivated(holdsPassword)
			record := governance.Record{
				Action: "member.reactivated", Entity: memberEntity(after),
				Before: map[string]any{"status": before.Status}, After: map[string]any{"status": after.Status},
			}
			if after.Status != access.MemberInvited {
				return after, []governance.Record{record}, nil
			}
			inv, err := issueInvitation(ctx, h.store, h.ids, h.random, member, now)
			if err != nil {
				return access.Member{}, nil, err
			}
			issued = &inv
			return after, []governance.Record{record, invitationRecord("member.invitation_reissued", after, inv, nil)}, nil
		})
	if err != nil {
		return InvitedMember{}, err
	}
	return InvitedMember{Member: view, Invitation: issued}, nil
}
