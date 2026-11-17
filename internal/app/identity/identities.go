package identity

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	errIdentityNotFound  = apperrors.NotFound("not_found", "the member has no such identity")
	errMemberNotFound    = apperrors.NotFound("not_found", "the instance has no such member")
	errMemberProvisioned = apperrors.Conflict("member_provisioned",
		"the member is provisioned through this auth provider, which signs them in: change it in the directory")
	errProviderEnforced = apperrors.Conflict("provider_enforced",
		"the member's email domain is enforced to this identity's auth provider, their only way in: lift the enforcement first")
	errRecentSignInRequired = apperrors.Forbidden("recent_sign_in_required",
		"linking a sign-in method needs a session that signed in in the last 15 minutes: sign in again")
	errProviderUnusable = apperrors.BadGateway("provider_unreachable", "the auth provider could not be reached to link an identity")
)

// StartLink begins linking an identity to the member signed in.
type StartLink struct {
	d     SignInDeps
	start *StartSignIn
}

// NewStartLink returns the handler.
func NewStartLink(d SignInDeps) *StartLink { return &StartLink{d: d, start: NewStartSignIn(d)} }

// recentSignIn is how long after a session opened it may still link an
// identity. A way in that outlives the session, and the member's password,
// is not something an old session, which may have been hijacked, vouches for.
const recentSignIn = 15 * time.Minute

// StartLinkCommand asks to link an identity at the provider Slug to Member,
// signed in in Session, which opened at SessionOpenedAt, and to return to
// ReturnTo afterwards.
type StartLinkCommand struct {
	Slug            string
	ReturnTo        string
	Member          identity.MemberID
	Session         identity.SessionID
	SessionOpenedAt time.Time
}

// Handle creates an attempt bound to the member, their session and a new
// cookie, and answers the provider's authorization address. A disabled or
// unknown provider is not found; one the instance cannot reach is 502
// provider_unreachable; a session that opened more than 15 minutes ago is
// 403 recent_sign_in_required.
func (h *StartLink) Handle(ctx context.Context, cmd StartLinkCommand) (Started, error) {
	if h.d.now().Sub(cmd.SessionOpenedAt) > recentSignIn {
		return Started{}, errRecentSignInRequired
	}
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
		if enforcedErr := refuseEnforced(ctx, h.d.Members, p, m.Email); enforcedErr != nil {
			return enforcedErr
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
	var failure *SignInFailure
	switch {
	case errors.Is(err, ErrIdentityLinked):
		return SignedIn{}, fail(CodeIdentityLinked, err)
	case errors.As(err, &failure):
		return SignedIn{}, err
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
// signs in nowhere and needs none. The identity of a provisioned member at
// the provider that provisions them is the directory's, and is refused with
// the conflict member_provisioned; the identity at the provider the member's
// email domain is enforced to is the only way in enforcement leaves them, and
// is refused with the conflict provider_enforced. Unlinking what is no
// usable way in, such as an identity at a disabled provider, never leaves a
// member without one. When no identity of the member remains at the provider,
// what it brought them is taken back in the same unit of work, and
// identity.unlinked records it: see takeBack.
func (h *UnlinkIdentity) Handle(ctx context.Context, cmd UnlinkCommand) error {
	identityID, err := id.Parse[identity.Identity](cmd.Identity)
	if err != nil {
		return errIdentityNotFound
	}
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// Taking back what a provider brought may demote the member, which
		// takes the instance admins' lock; it is taken before the member's,
		// the order every change of access takes, so an unlink and a change
		// to the same member made at once never deadlock.
		if err := h.d.Members.LockInstanceAdmins(ctx); err != nil {
			return err
		}
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
		if m.Provider != "" && m.Provider == unlinking.ProviderSlug {
			return errMemberProvisioned
		}
		enforced, err := h.d.Members.EnforcedProvider(ctx, m.Email)
		if err != nil {
			return err
		}
		if enforced == unlinking.ProviderSlug {
			return errProviderEnforced
		}
		if unlinking.ProviderEnabled {
			if err = h.d.Members.EnsureWayIn(ctx, m, identityID); err != nil {
				return err
			}
		}
		if err = h.d.Identities.DeleteIdentity(ctx, identityID); err != nil {
			return err
		}
		taken, err := h.takeBack(ctx, m.ID, held, unlinking, cmd.Request.ID)
		if err != nil {
			return err
		}
		now := h.d.now()
		entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, cmd.Request, governance.Record{
			Action: "identity.unlinked",
			Entity: governance.Entity{Kind: "identity", ID: identityID.String(), Key: unlinking.ProviderSlug},
			Before: map[string]any{
				"provider": unlinking.ProviderSlug, "subject": unlinking.Subject, "member_id": m.ID.String(), "member_email": m.Email,
			},
			After: taken.values(),
		})
		if err != nil {
			return err
		}
		return h.d.Audit.Record(ctx, entry)
	})
}

// takenBack is what unlinking an identity took back from the member: what
// the provider brought them that no identity of theirs there vouches for any
// more.
type takenBack struct {
	// GroupClaimRemoved is whether the member had claimed groups at the
	// provider.
	GroupClaimRemoved bool
	// AccessRevoked counts the grants and the instance admin the provider's
	// mappings gave and no longer do; KeptInstanceAdmin is whether the member
	// stayed instance admin as the last active one.
	AccessRevoked     int
	KeptInstanceAdmin bool
	// SessionsRevoked counts the sessions opened through the provider.
	SessionsRevoked int
}

// values is what the audit log records; nothing when nothing was taken back.
func (t takenBack) values() map[string]any {
	if t == (takenBack{}) {
		return nil
	}
	return map[string]any{
		"group_claim_removed": t.GroupClaimRemoved, "access_revoked": t.AccessRevoked,
		"kept_instance_admin": t.KeptInstanceAdmin, "sessions_revoked": t.SessionsRevoked,
	}
}

// takeBack takes back what the provider of the identity brought member when
// no other identity of theirs remains there: the groups they claimed, the
// access the provider's mappings gave them for those groups (what the
// provider's SCIM groups give stays), and the sessions opened through it. It
// does nothing while another identity of the member is at the provider, whose
// identities the member held before the unlink.
func (h *UnlinkIdentity) takeBack(ctx context.Context, member identity.MemberID, held []LinkedIdentity, unlinked *LinkedIdentity, requestID string) (takenBack, error) {
	var taken takenBack
	if slices.ContainsFunc(held, func(i LinkedIdentity) bool { return i.ID != unlinked.ID && i.Provider == unlinked.Provider }) {
		return taken, nil
	}
	p, err := providerOf(ctx, h.d.Providers, unlinked.ProviderSlug)
	if err != nil {
		return taken, err
	}
	if taken.GroupClaimRemoved, err = h.d.Mappings.DeleteGroupClaim(ctx, p.ID, member); err != nil {
		return taken, err
	}
	mappings, err := h.d.Mappings.GroupMappings(ctx, p.ID)
	if err != nil {
		return taken, err
	}
	groups, err := h.d.Mappings.GroupsOf(ctx, p.ID, member)
	if err != nil {
		return taken, err
	}
	change, err := GroupMappingDeps{Access: h.d.Access, Logger: h.d.Logger}.replaceAccess(ctx, p, member, mappings, groups, governance.Actor{}, requestID)
	if err != nil {
		return taken, err
	}
	taken.AccessRevoked, taken.KeptInstanceAdmin = change.Revoked, change.KeptInstanceAdmin
	taken.SessionsRevoked, err = h.d.Members.RevokeProviderSessions(ctx, member, p.Slug)
	return taken, err
}
