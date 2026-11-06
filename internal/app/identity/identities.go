package identity

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	errIdentityNotFound = apperrors.NotFound("not_found", "the member has no such identity")
	errMemberNotFound   = apperrors.NotFound("not_found", "the instance has no such member")
	errProviderUnusable = apperrors.BadGateway("provider_unreachable", "the auth provider could not be reached to link an identity")
)

// StartLink begins linking an identity to the member signed in.
type StartLink struct{ start *StartSignIn }

// NewStartLink returns the handler.
func NewStartLink(d SignInDeps) *StartLink { return &StartLink{start: NewStartSignIn(d)} }

// StartLinkCommand asks to link an identity at the provider Slug to Member,
// signed in in Session, and to return to ReturnTo afterwards.
type StartLinkCommand struct {
	Slug     string
	ReturnTo string
	Member   identity.MemberID
	Session  identity.SessionID
}

// Handle creates an attempt bound to the member, their session and a new
// cookie, and answers the provider's authorization address. A disabled or
// unknown provider is not found; one the instance cannot reach is 502
// provider_unreachable.
func (h *StartLink) Handle(ctx context.Context, cmd StartLinkCommand) (Started, error) {
	started, err := h.start.start(ctx, cmd.Slug, cmd.ReturnTo, &linkBinding{member: cmd.Member, session: cmd.Session})
	var failure *SignInFailure
	if errors.As(err, &failure) {
		return Started{}, errProviderUnusable.WithCause(err)
	}
	return started, err
}

// link completes a link attempt: in the session that started it, the verified
// subject becomes an identity of the member who started it, whatever its
// email. A browser whose session is not that one links nothing.
func (h *CompleteSignIn) link(ctx context.Context, p identity.Provider, attempt identity.Attempt, cmd CompleteCommand) (SignedIn, error) {
	member, session, ok, err := h.d.Sessions.SessionOf(ctx, cmd.SessionCookie)
	if err != nil {
		return SignedIn{}, err
	}
	if !ok || member != attempt.Member || session != attempt.Session {
		return SignedIn{}, fail(CodeAttemptMismatch, errors.New("the session that started the link is not the one completing it"))
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		m, found, lockErr := h.d.Members.LockMember(ctx, member)
		if lockErr != nil || !found || m.Status != identity.MemberActive {
			// A member who cannot sign in holds no session, so this is a race
			// with their deactivation: nothing is linked.
			return cmp.Or(lockErr, errors.New("the member who started the link cannot sign in"))
		}
		claims := *attempt.Claims
		existing, lookupErr := h.d.Identities.IdentityBySubject(ctx, p.ID, claims.Subject)
		switch {
		case lookupErr == nil && existing.Member == member:
			// Linked already, by this very member: nothing changes.
			return nil
		case lookupErr == nil:
			return ErrIdentityLinked
		case !errors.Is(lookupErr, ErrNotFound):
			return lookupErr
		}
		now := h.d.now()
		linked := identity.Identity{
			ID: id.New[identity.Identity](h.d.IDs), Provider: p.ID, Subject: claims.Subject, Member: member, LinkedAt: now,
		}
		if createErr := h.d.Identities.CreateIdentity(ctx, linked); createErr != nil {
			return createErr
		}
		return h.recordLinked(ctx, now, p, linked, m, cmd.RequestID)
	})
	switch {
	case errors.Is(err, ErrIdentityLinked):
		return SignedIn{}, fail(CodeIdentityLinked, err)
	case err != nil:
		return SignedIn{}, fail(CodeSSOFailed, err)
	}
	return SignedIn{Kind: p.Kind, ReturnTo: attempt.ReturnTo, Linked: true}, nil
}

// MemberAuthMethods are the ways a member holds to sign in.
type MemberAuthMethods struct {
	// PasswordSetAt is when the member set the password they hold, nil when
	// they hold none.
	PasswordSetAt *time.Time
	Identities    []LinkedIdentity
}

// ListMemberAuthMethods answers a member's password status and identities.
type ListMemberAuthMethods struct{ d SignInDeps }

// NewListMemberAuthMethods returns the query.
func NewListMemberAuthMethods(d SignInDeps) *ListMemberAuthMethods {
	return &ListMemberAuthMethods{d: d}
}

// Handle returns the ways member holds to sign in, the identities oldest
// first; not found when no member has the identifier.
func (h *ListMemberAuthMethods) Handle(ctx context.Context, member identity.MemberID) (MemberAuthMethods, error) {
	if _, ok, err := h.d.Members.MemberByID(ctx, member); err != nil {
		return MemberAuthMethods{}, err
	} else if !ok {
		return MemberAuthMethods{}, errMemberNotFound
	}
	setAt, err := h.d.Members.PasswordSetAt(ctx, member)
	if err != nil {
		return MemberAuthMethods{}, err
	}
	identities, err := h.d.Identities.MemberIdentities(ctx, member)
	if err != nil {
		return MemberAuthMethods{}, err
	}
	if identities == nil {
		identities = []LinkedIdentity{}
	}
	return MemberAuthMethods{PasswordSetAt: setAt, Identities: identities}, nil
}

// UnlinkIdentity unlinks an identity from a member.
type UnlinkIdentity struct{ d SignInDeps }

// NewUnlinkIdentity returns the handler.
func NewUnlinkIdentity(d SignInDeps) *UnlinkIdentity { return &UnlinkIdentity{d: d} }

// UnlinkCommand unlinks the identity Identity from Member, on behalf of the
// actor Request names: the member themself, or an instance admin.
type UnlinkCommand struct {
	Member   identity.MemberID
	Identity string
	Request  governance.Request
}

// Handle deletes the member's identity and records identity.unlinked, in one
// unit of work that holds the member, so unlinking two identities at once
// cannot leave them without a way in. An identity that is not the member's,
// or whose identifier names none, is not found. An identity that is a usable
// way in, at an enabled provider, is refused with the conflict
// last_auth_method when no other usable way in remains; a deactivated member
// signs in nowhere and needs none.
func (h *UnlinkIdentity) Handle(ctx context.Context, cmd UnlinkCommand) error {
	identityID, err := id.Parse[identity.Identity](cmd.Identity)
	if err != nil {
		return errIdentityNotFound
	}
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		m, found, err := h.d.Members.LockMember(ctx, cmd.Member)
		if err != nil {
			return err
		}
		if !found {
			return errMemberNotFound
		}
		held, err := h.d.Identities.MemberIdentities(ctx, cmd.Member)
		if err != nil {
			return err
		}
		var unlinking *LinkedIdentity
		for i := range held {
			if held[i].ID == identityID {
				unlinking = &held[i]
			}
		}
		if unlinking == nil {
			return errIdentityNotFound
		}
		if unlinking.ProviderEnabled {
			if err = h.d.Members.EnsureWayIn(ctx, m, identityID); err != nil {
				return err
			}
		}
		if err = h.d.Identities.DeleteIdentity(ctx, identityID); err != nil {
			return err
		}
		now := h.d.now()
		entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, cmd.Request, governance.Record{
			Action: "identity.unlinked",
			Entity: governance.Entity{Kind: "identity", ID: identityID.String(), Key: unlinking.ProviderSlug},
			Before: map[string]any{
				"provider": unlinking.ProviderSlug, "subject": unlinking.Subject, "member_id": m.ID.String(), "member_email": m.Email,
			},
		})
		if err != nil {
			return err
		}
		return h.d.Audit.Record(ctx, entry)
	})
}
