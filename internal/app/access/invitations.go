package access

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// InvitationStore keeps invitations.
type InvitationStore interface {
	CreateInvitation(ctx context.Context, inv access.Invitation) error
	// ConsumeInvitations ends every open invitation of member, and returns
	// how many it ended.
	ConsumeInvitations(ctx context.Context, member access.MemberID, at time.Time) (int, error)
}

// IssuedInvitation is an invitation just issued, with its secret, shown
// once to whoever issued it.
type IssuedInvitation struct {
	access.Invitation
	Secret secret.String
}

// issueInvitation consumes every open invitation of member and issues a
// fresh one, in the unit of work ctx carries: a member has one link at a
// time, and replacing it kills the old one.
func issueInvitation(ctx context.Context, store InvitationStore, ids *id.Source, random io.Reader, member access.MemberID,
	now time.Time,
) (IssuedInvitation, error) {
	if _, err := store.ConsumeInvitations(ctx, member, now); err != nil {
		return IssuedInvitation{}, err
	}
	inv, s := access.NewInvitation(id.New[access.Invitation](ids), member, now, random)
	if err := store.CreateInvitation(ctx, inv); err != nil {
		return IssuedInvitation{}, err
	}
	return IssuedInvitation{Invitation: inv, Secret: s}, nil
}

// InvitedMember is a member with the invitation just issued to them, if
// one was.
type InvitedMember struct {
	Member     MemberView
	Invitation *IssuedInvitation
}

// InviteStore is what inviting someone needs.
type InviteStore interface {
	InvitationStore
	MemberDirectory
	CreateMember(ctx context.Context, m access.Member, passwordHash string) error
}

// InviteMember invites someone to the instance by email.
type InviteMember struct {
	tx     Transactor
	store  InviteStore
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewInviteMember returns the handler; random draws invitation secrets.
func NewInviteMember(tx Transactor, store InviteStore, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader) *InviteMember {
	return &InviteMember{tx: tx, store: store, audit: audit, clock: c, ids: ids, random: random}
}

// InviteMemberCommand names the person to invite.
type InviteMemberCommand struct {
	Email       string
	DisplayName string
	Request     governance.Request
}

// Handle creates an invited member and their invitation, recording
// member.invited; an email the instance holds is refused already_exists.
func (h *InviteMember) Handle(ctx context.Context, cmd InviteMemberCommand) (InvitedMember, error) {
	now := h.clock.Now()
	m, err := access.NewInvitedMember(id.New[access.Member](h.ids), cmd.Email, cmd.DisplayName, now)
	if err != nil {
		return InvitedMember{}, err
	}
	var res InvitedMember
	err = h.tx.Within(ctx, func(ctx context.Context) error {
		if err = h.store.CreateMember(ctx, m, ""); err != nil {
			return err
		}
		var issued IssuedInvitation
		if issued, err = issueInvitation(ctx, h.store, h.ids, h.random, m.ID, now); err != nil {
			return err
		}
		res.Invitation = &issued
		if err = recordAll(ctx, h.audit, h.ids, now, cmd.Request, invitationRecord("member.invited", m, issued, map[string]any{
			"email": m.Email, "display_name": m.DisplayName, "status": m.Status, "instance_admin": m.InstanceAdmin,
		})); err != nil {
			return err
		}
		res.Member, err = viewOf(ctx, h.store, m.ID)
		return err
	})
	if err != nil {
		return InvitedMember{}, err
	}
	return res, nil
}

// invitationRecord is the audit record of action issuing inv to m, naming
// when it expires and never its secret.
func invitationRecord(action string, m access.Member, inv IssuedInvitation, after map[string]any) governance.Record {
	if after == nil {
		after = map[string]any{}
	}
	after["invitation_expires_at"] = inv.ExpiresAt.UTC().Format(time.RFC3339)
	return governance.Record{Action: action, Entity: memberEntity(m), After: after}
}

// ReissueStore is what reissuing an invitation needs.
type ReissueStore interface {
	InvitationStore
	MemberDirectory
	// LockMember returns the member, locked until the unit of work ends,
	// and whether they hold a password; ErrNotFound when none has the
	// identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
}

// ReissueInvitation replaces an invited member's link.
type ReissueInvitation struct {
	tx     Transactor
	store  ReissueStore
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewReissueInvitation returns the handler; random draws invitation secrets.
func NewReissueInvitation(tx Transactor, store ReissueStore, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader,
) *ReissueInvitation {
	return &ReissueInvitation{tx: tx, store: store, audit: audit, clock: c, ids: ids, random: random}
}

// Handle issues the member a fresh invitation, which kills every earlier
// one, recording member.invitation_reissued. A member who is not invited is
// refused member_not_invited.
func (h *ReissueInvitation) Handle(ctx context.Context, member access.MemberID, req governance.Request) (InvitedMember, error) {
	var res InvitedMember
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		m, _, err := h.store.LockMember(ctx, member)
		if errors.Is(err, ErrNotFound) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		if m.Status != access.MemberInvited {
			return access.ErrMemberNotInvited
		}
		now := h.clock.Now()
		issued, err := issueInvitation(ctx, h.store, h.ids, h.random, m.ID, now)
		if err != nil {
			return err
		}
		res.Invitation = &issued
		if err = recordAll(ctx, h.audit, h.ids, now, req, invitationRecord("member.invitation_reissued", m, issued, nil)); err != nil {
			return err
		}
		res.Member, err = viewOf(ctx, h.store, m.ID)
		return err
	})
	if err != nil {
		return InvitedMember{}, err
	}
	return res, nil
}

// InvitationReader finds invitations by the hash of their secret.
type InvitationReader interface {
	// InvitationByHash returns the invitation and its member; ErrNotFound
	// when no invitation has the hash.
	InvitationByHash(ctx context.Context, hash access.InvitationHash) (access.Invitation, access.Member, error)
}

// InvitationView is what the page an invitation opens shows.
type InvitationView struct {
	// MaskedEmail is the member's email with most of its local part hidden.
	MaskedEmail string
	DisplayName string
	ExpiresAt   time.Time
}

// GetInvitation answers what an invitation's link opens, before anyone is
// signed in.
type GetInvitation struct {
	store InvitationReader
	clock clock.Clock
}

// NewGetInvitation returns the query.
func NewGetInvitation(store InvitationReader, c clock.Clock) *GetInvitation {
	return &GetInvitation{store: store, clock: c}
}

// Handle returns the invitation the secret opens; invitation_gone when it
// opens none that can be accepted, whatever the reason.
func (h *GetInvitation) Handle(ctx context.Context, s secret.String) (InvitationView, error) {
	inv, m, err := openInvitation(ctx, h.store, s, h.clock.Now())
	if err != nil {
		return InvitationView{}, err
	}
	return InvitationView{MaskedEmail: access.MaskEmail(m.Email), DisplayName: m.DisplayName, ExpiresAt: inv.ExpiresAt}, nil
}

// openInvitation returns the invitation s opens and its member, when it can
// be accepted at now; ErrInvitationGone otherwise.
func openInvitation(ctx context.Context, store InvitationReader, s secret.String, now time.Time) (access.Invitation, access.Member, error) {
	hash, ok := access.ParseInvitationSecret(s.Reveal())
	if !ok {
		return access.Invitation{}, access.Member{}, access.ErrInvitationGone
	}
	inv, m, err := store.InvitationByHash(ctx, hash)
	switch {
	case errors.Is(err, ErrNotFound):
		return access.Invitation{}, access.Member{}, access.ErrInvitationGone
	case err != nil:
		return access.Invitation{}, access.Member{}, err
	case !inv.Open(now, m):
		return access.Invitation{}, access.Member{}, access.ErrInvitationGone
	}
	return inv, m, nil
}

// AcceptStore is what accepting an invitation needs.
type AcceptStore interface {
	InvitationReader
	InvitationStore
	// LockInvitationByHash returns the invitation and its member, both
	// locked until the unit of work ends; ErrNotFound when none has the
	// hash.
	LockInvitationByHash(ctx context.Context, hash access.InvitationHash) (access.Invitation, access.Member, error)
	SaveMember(ctx context.Context, m access.Member, at time.Time) error
	SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error
	RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error
	CreateSession(ctx context.Context, s access.Session) error
	// ReserveSourceAttempt counts a as a failure of its source alone,
	// before what it presents is checked, and returns what it found.
	ReserveSourceAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error)
	// ReleaseAttempt takes back the failure r reserved.
	ReleaseAttempt(ctx context.Context, r access.Reservation) error
}

// AcceptInvitation lets an invited person set their first password, and
// signs them in.
type AcceptInvitation struct {
	d AcceptInvitationDeps
}

// AcceptInvitationDeps are what accepting an invitation works with.
type AcceptInvitationDeps struct {
	Tx    Transactor
	Store AcceptStore
	// Settings give the password policy's minimum length and the timeouts
	// the session opens with.
	Settings AuthSettingsReader
	Hasher   PasswordHasher
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	// Random draws session cookies, which are signed with Key.
	Random io.Reader
	Key    []byte
}

// NewAcceptInvitation returns the handler.
func NewAcceptInvitation(d AcceptInvitationDeps) *AcceptInvitation { return &AcceptInvitation{d: d} }

// AcceptInvitationCommand is one attempt at accepting an invitation.
type AcceptInvitationCommand struct {
	Secret   secret.String
	Password secret.String
	// ClientIP and UserAgent describe the device signing in.
	ClientIP  netip.Addr
	UserAgent string
	RequestID string
}

// Handle sets the member's first password, makes them active, consumes
// their invitations and opens a session, recording
// member.invitation_accepted and auth.signed_in. The attempt counts as a
// failure of its source before the secret is looked up, so guessing secrets
// is throttled with guessing passwords; a throttled source is refused
// rate_limited before anything else. A secret that opens no invitation
// keeps its failure and is answered invitation_gone; one that does takes it
// back, whatever happens next. The password policy applies (weak_password),
// before any hash.
func (h *AcceptInvitation) Handle(ctx context.Context, cmd AcceptInvitationCommand) (SignInResult, error) {
	now := h.d.Clock.Now()
	reserved, err := h.d.Store.ReserveSourceAttempt(ctx, access.NewAttempt("", cmd.ClientIP, now))
	if err != nil {
		return SignInResult{}, err
	}
	if refusal := reserved.Refusal(); refusal != nil {
		return SignInResult{}, h.release(ctx, reserved, refusal)
	}
	inv, m, err := openInvitation(ctx, h.d.Store, cmd.Secret, now)
	if errors.Is(err, access.ErrInvitationGone) {
		return SignInResult{}, err
	}
	if err != nil {
		return SignInResult{}, h.release(ctx, reserved, err)
	}
	res, err := h.accept(ctx, cmd, inv, m, reserved)
	if err != nil {
		return SignInResult{}, h.release(ctx, reserved, err)
	}
	return res, nil
}

// release takes back the source's failure reserved for an attempt that
// guessed nothing, and returns err, with any error taking it back.
func (h *AcceptInvitation) release(ctx context.Context, reserved access.Reservation, err error) error {
	return errors.Join(err, h.d.Tx.Within(context.WithoutCancel(ctx), func(ctx context.Context) error {
		return h.d.Store.ReleaseAttempt(ctx, reserved)
	}))
}

// accept sets the password of m, whose invitation opened checks, and signs
// them in, taking back the source's failure in the same unit of work.
func (h *AcceptInvitation) accept(ctx context.Context, cmd AcceptInvitationCommand, opened access.Invitation, invited access.Member,
	reserved access.Reservation,
) (SignInResult, error) {
	settings, err := h.d.Settings.AuthSettings(ctx)
	if err != nil {
		return SignInResult{}, err
	}
	if err = access.CheckPassword(cmd.Password.Reveal(), invited.Email, settings.PasswordMinLength); err != nil {
		return SignInResult{}, err
	}
	hash, err := h.d.Hasher.Hash(ctx, cmd.Password)
	if err != nil {
		return SignInResult{}, err
	}
	now := h.d.Clock.Now()
	var res SignInResult
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		res, err = h.join(ctx, cmd, opened.Hash, hash, settings, reserved, now)
		return err
	})
	if err != nil {
		return SignInResult{}, err
	}
	return res, nil
}

// join makes the member whose invitation hash names active with the
// password hash stored for them, consumes their invitations and opens their
// session, in the unit of work ctx carries; invitation_gone when the
// invitation was accepted, replaced or consumed since it was read.
func (h *AcceptInvitation) join(ctx context.Context, cmd AcceptInvitationCommand, invitation access.InvitationHash, hash string,
	settings access.AuthSettings, reserved access.Reservation, now time.Time,
) (SignInResult, error) {
	inv, m, err := h.d.Store.LockInvitationByHash(ctx, invitation)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SignInResult{}, err
	}
	if err != nil || !inv.Open(now, m) {
		// Accepted, replaced or consumed since it was read.
		return SignInResult{}, access.ErrInvitationGone
	}
	joined := m.Joined()
	if err := h.d.Store.SaveMember(ctx, joined, now); err != nil {
		return SignInResult{}, err
	}
	if err := h.d.Store.SetPasswordHash(ctx, m.ID, hash, now); err != nil {
		return SignInResult{}, err
	}
	if _, err := h.d.Store.ConsumeInvitations(ctx, m.ID, now); err != nil {
		return SignInResult{}, err
	}
	if err := h.d.Store.ReleaseAttempt(ctx, reserved); err != nil {
		return SignInResult{}, err
	}
	session, cookie := access.NewSession(id.New[access.Session](h.d.IDs), access.NewSessionInput{
		Member: m.ID, Method: access.SignInLocal, ClientIP: cmd.ClientIP, UserAgent: cmd.UserAgent,
		Timeouts: settings.SessionTimeouts(), Now: now,
	}, h.d.Random, h.d.Key)
	if err := h.d.Store.RecordSignIn(ctx, m.ID, now); err != nil {
		return SignInResult{}, err
	}
	if err := h.d.Store.CreateSession(ctx, session); err != nil {
		return SignInResult{}, err
	}
	res := SignInResult{Caller: access.SessionCaller(session, joined), Cookie: cookie}
	return res, recordAll(ctx, h.d.Audit, h.d.IDs, now, governance.Request{Actor: memberActor(joined), ID: cmd.RequestID},
		governance.Record{
			Action: "member.invitation_accepted", Entity: memberEntity(joined),
			Before: map[string]any{"status": m.Status}, After: map[string]any{"status": joined.Status},
		},
		authRecord("auth.signed_in", joined, nil))
}
