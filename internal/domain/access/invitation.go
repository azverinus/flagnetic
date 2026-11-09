package access

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// InvitationID identifies an invitation.
type InvitationID = id.Of[Invitation]

// InvitationHash is the SHA-256 of an invitation's secret, the only form in
// which the secret is stored.
type InvitationHash [sha256.Size]byte

// InvitationLifetime is how long an invitation can be accepted: long enough
// to reach someone through whoever passes the link on, short enough that a
// link lying forgotten in a chat stops working.
const InvitationLifetime = 7 * 24 * time.Hour

// InvitationRetention is how long an invitation is kept once it can no
// longer be accepted before the retention worker deletes it, so the table
// does not grow forever while a recent link can still be looked into. Each
// member's latest invitation is kept regardless, as the record of their last
// link.
const InvitationRetention = 30 * 24 * time.Hour

const invitationSecretLength = 32

// Invitation is the one-time link that lets an invited member set their
// first password; signing in through an auth provider that vouches for their
// email accepts it too.
type Invitation struct {
	ID        InvitationID
	Member    MemberID
	Hash      InvitationHash
	CreatedAt time.Time
	ExpiresAt time.Time
	// ConsumedAt is when the invitation stopped being acceptable before it
	// expired: accepted, replaced by another, or consumed when its member was
	// deactivated. Nil while it is open.
	ConsumedAt *time.Time
	// InvitedBy is the member who issued the invitation; zero when none
	// did, such as one issued again to a member reactivated.
	InvitedBy MemberID
	// Grant is the role in a project that accepting makes; nil when the
	// invitation brings the member to the instance alone, or once it was
	// dropped.
	Grant *InvitationGrant
}

// InvitationGrant is the role in a project that an invitation, once
// accepted, makes the member hold.
type InvitationGrant struct {
	Project workspace.ProjectID
	Role    RoleName
}

// NewInvitationGrant returns the grant of role in project; role is one of
// the built-in ones, or the refusal names pointer, where the request holds it.
func NewInvitationGrant(project workspace.ProjectID, role RoleName, pointer string) (InvitationGrant, error) {
	if _, ok := RoleNamed(role); !ok {
		return InvitationGrant{}, UnknownRoleError(pointer)
	}
	return InvitationGrant{Project: project, Role: role}, nil
}

// UnknownProjectError refuses an invitation's grant in a project the instance
// does not hold, at pointer.
func UnknownProjectError(pointer string) error {
	return apperrors.Invalid("invalid_grant", "the grant is not valid", apperrors.Violation{
		Pointer: pointer, Code: "unknown_project", Detail: "no project has this key",
	})
}

// IssuedBy returns i as issued by inviter, bringing grant (none when nil)
// with it.
func (i Invitation) IssuedBy(inviter MemberID, grant *InvitationGrant) Invitation {
	i.InvitedBy, i.Grant = inviter, grant
	return i
}

// WithoutGrant returns i with its grant dropped: it still activates its
// member, and grants nothing.
func (i Invitation) WithoutGrant() Invitation {
	i.Grant = nil
	return i
}

// GrantDropReason is why an invitation's grant was dropped.
type GrantDropReason string

// The reasons an invitation's grant is dropped.
const (
	// GrantDropInviterLostPermission is an inviter who no longer holds
	// grant.manage in the project.
	GrantDropInviterLostPermission GrantDropReason = "inviter_lost_permission"
	// GrantDropInviterDeactivated is an inviter who was deactivated.
	GrantDropInviterDeactivated GrantDropReason = "inviter_deactivated"
)

// GrantDrop reports whether the grant of an invitation issued by inviter to a
// project must be dropped, and why, given held, the grants inviter holds in
// that project. An invitation never grants more than its inviter may hand
// out, so it is dropped once its inviter is deactivated or no longer holds
// grant.manage there; an instance admin holds it in every project, so
// their invitations are not dropped for lack of a grant. The same rule
// decides whether an invitation issued again keeps its grant.
func GrantDrop(inviter Member, held []Grant) (GrantDropReason, bool) {
	switch {
	case inviter.Status == MemberDeactivated:
		return GrantDropInviterDeactivated, true
	case inviter.InstanceAdmin:
		return "", false
	}
	for _, g := range held {
		if role, ok := RoleNamed(g.Role); ok && slices.Contains(role.Permissions, PermGrantManage) {
			return "", false
		}
	}
	return GrantDropInviterLostPermission, true
}

// NewInvitedMember returns a member invited to the instance, who holds no
// password until they accept their invitation and is no instance admin.
func NewInvitedMember(memberID MemberID, email, displayName string, now time.Time) (Member, error) {
	email, displayName = strings.TrimSpace(email), strings.TrimSpace(displayName)
	if _, err := checkEmail(email); err != nil {
		return Member{}, err
	}
	if err := checkDisplayName(displayName); err != nil {
		return Member{}, err
	}
	return Member{ID: memberID, Email: email, DisplayName: displayName, Status: MemberInvited, CreatedAt: now}, nil
}

// Joined returns m once they have set their first password: active.
func (m Member) Joined() Member {
	m.Status = MemberActive
	return m
}

// NewInvitation issues an invitation of member and returns it with its
// secret: 32 bytes drawn from random in unpadded base64url, shown once to
// whoever issued it and never stored.
func NewInvitation(invitationID InvitationID, member MemberID, now time.Time, random io.Reader) (Invitation, secret.String) {
	raw := make([]byte, invitationSecretLength)
	if _, err := io.ReadFull(random, raw); err != nil {
		// The system's randomness never fails; an exhausted test source is
		// a programming error.
		panic("access: reading randomness: " + err.Error())
	}
	return Invitation{
		ID: invitationID, Member: member, Hash: sha256.Sum256(raw), CreatedAt: now, ExpiresAt: now.Add(InvitationLifetime),
	}, secret.New(base64.RawURLEncoding.EncodeToString(raw))
}

// ParseInvitationSecret returns the hash an invitation's secret is stored
// under; false for a value no invitation was ever issued with, which needs
// no lookup.
func ParseInvitationSecret(value string) (InvitationHash, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(raw) != invitationSecretLength {
		return InvitationHash{}, false
	}
	return sha256.Sum256(raw), true
}

// Open reports whether the invitation can be accepted at now by holder, its
// member: not consumed, not expired, and holder still invited.
func (i Invitation) Open(now time.Time, holder Member) bool {
	return i.ConsumedAt == nil && now.Before(i.ExpiresAt) && holder.Status == MemberInvited
}

// ErrInvitationGone answers an invitation that cannot be accepted, whatever
// the reason, so a link tells nobody whether it ever existed.
var ErrInvitationGone = apperrors.Gone("invitation_gone",
	"this invitation was never issued, has been used or replaced, or has expired")

// ErrMemberNotInvited refuses reissuing the invitation of a member who has
// joined or was deactivated: there is no first password left to set.
var ErrMemberNotInvited = apperrors.Conflict("member_not_invited", "only an invited member has an invitation to reissue")

// MaskEmail returns email as the page an invitation opens shows it: its
// first character and its domain, enough for the invited person to
// recognise their address, too little for whoever finds a stray link to
// learn it.
func MaskEmail(email string) string {
	local, domain, _ := strings.Cut(email, "@")
	first, _ := utf8.DecodeRuneInString(local)
	return string(first) + "***@" + domain
}

// InvitationCutoffs select the invitations the retention worker purges.
type InvitationCutoffs struct {
	// EndedBy purges an invitation consumed, or expired, at or before it.
	EndedBy time.Time
}

// InvitationCutoffsAt returns the cutoffs at now.
func InvitationCutoffsAt(now time.Time) InvitationCutoffs {
	return InvitationCutoffs{EndedBy: now.Add(-InvitationRetention)}
}
