package identity

import (
	"errors"
	"strings"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Member is the identity context's view of a member of the instance, which
// the access context owns.
type Member struct{}

// MemberID identifies a member.
type MemberID = id.Of[Member]

// MemberStatus is where a member stands: invited and not yet active, active,
// or deactivated.
type MemberStatus string

// The statuses a member has.
const (
	MemberInvited     MemberStatus = "invited"
	MemberActive      MemberStatus = "active"
	MemberDeactivated MemberStatus = "deactivated"
)

// KnownMember is a member the instance holds, as resolution sees them.
type KnownMember struct {
	ID     MemberID
	Status MemberStatus
}

// Refusals of a verified subject that matches no member who may sign in.
var (
	// ErrMemberNotFound refuses a subject no rule lets in.
	ErrMemberNotFound = errors.New("identity: no member matches the signed-in subject")
	// ErrMemberDeactivated refuses a subject whose member was deactivated.
	ErrMemberDeactivated = errors.New("identity: the member is deactivated")
)

// ResolveAction is what resolution decides to do for a verified subject.
type ResolveAction int

// The ways a verified subject becomes a member signing in.
const (
	// SignInLinked signs in the member the subject is already linked to.
	SignInLinked ResolveAction = iota + 1
	// LinkByEmail links the subject to the active member holding its email.
	LinkByEmail
	// Provision creates an active member for the subject and links it.
	Provision
)

// ResolveInput is what resolution judges.
type ResolveInput struct {
	Provider Provider
	Claims   VerifiedClaims
	// Linked is the member the subject is linked to at the provider, nil when
	// it is linked to none.
	Linked *KnownMember
	// ByEmail is the member holding Claims.Email, whatever its case; nil when
	// none does.
	ByEmail *KnownMember
}

// Resolution is the decision for a verified subject.
type Resolution struct {
	Action ResolveAction
	// Member is the member signing in or linked; zero for Provision.
	Member MemberID
}

// Resolve decides which member a verified subject signs in as, by the first
// rule that holds:
//
//  1. the subject is linked: that member, however the email reads now, and no
//     provider domain is asked for, since the link itself is the proof;
//  2. the email is not one the provider vouches for (in its domains, and
//     verified when the provider requires it): no member;
//  3. a member holds the email: a deactivated one is refused as such, an
//     active one is linked when the provider links by email, and an invited
//     one is not signed in (their invitation is how they arrive);
//  4. the provider provisions: a new member.
//
// Anything else is ErrMemberNotFound; a deactivated member is
// ErrMemberDeactivated.
func Resolve(in ResolveInput) (Resolution, error) {
	if l := in.Linked; l != nil {
		if l.Status == MemberDeactivated {
			return Resolution{}, ErrMemberDeactivated
		}
		if l.Status == MemberActive {
			return Resolution{Action: SignInLinked, Member: l.ID}, nil
		}
		return Resolution{}, ErrMemberNotFound
	}
	if !in.Provider.Vouches(in.Claims) {
		return Resolution{}, ErrMemberNotFound
	}
	if m := in.ByEmail; m != nil {
		switch {
		case m.Status == MemberDeactivated:
			return Resolution{}, ErrMemberDeactivated
		case m.Status == MemberActive && in.Provider.LinkByEmail:
			return Resolution{Action: LinkByEmail, Member: m.ID}, nil
		}
		return Resolution{}, ErrMemberNotFound
	}
	if in.Provider.AutoProvision {
		return Resolution{Action: Provision}, nil
	}
	return Resolution{}, ErrMemberNotFound
}

// Vouches reports whether the provider vouches for the person claims
// describe: their email is in one of its domains, and verified where the
// provider requires verification.
func (p Provider) Vouches(c VerifiedClaims) bool {
	if p.OIDC != nil && p.OIDC.RequireVerifiedEmail && !c.EmailVerified {
		return false
	}
	return p.VouchesForEmail(c.Email)
}

// VouchesForEmail reports whether email's domain is one of the provider's;
// a subdomain is not the domain.
func (p Provider) VouchesForEmail(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range p.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// MemberName is the display name a provisioned member gets: the provider's
// name claim, else the email.
func (c VerifiedClaims) MemberName() string {
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	return c.Email
}
