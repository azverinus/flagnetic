package identity_test

import (
	"errors"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var someMember = id.Of[identity.Member]{}

func provider(link, provision, requireVerified bool) identity.Provider {
	return identity.Provider{
		Domains: []string{"example.com"}, LinkByEmail: link, AutoProvision: provision,
		OIDC: &identity.OIDC{RequireVerifiedEmail: requireVerified},
	}
}

func claims(email string, verified bool) identity.VerifiedClaims {
	return identity.VerifiedClaims{Subject: "sub-1", Email: email, EmailVerified: verified}
}

func known(status identity.MemberStatus) *identity.KnownMember {
	return &identity.KnownMember{ID: someMember, Status: status}
}

func TestResolutionFollowsItsRulesInOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      identity.ResolveInput
		want    identity.ResolveAction
		wantErr error
	}{
		{"a linked subject signs in", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@elsewhere.org", false), Linked: known(identity.MemberActive)}, identity.SignInLinked, nil},
		{"a linked deactivated member is refused", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Linked: known(identity.MemberDeactivated)}, 0, identity.ErrMemberDeactivated},
		{"a link to an invited member signs nobody in", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Linked: known(identity.MemberInvited)}, 0, identity.ErrMemberNotFound},
		{"a provisioned member is linked by the external ID, whatever their email", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@elsewhere.org", false), Provisioned: known(identity.MemberActive)}, identity.LinkProvisioned, nil},
		{"a linked subject beats a provisioned member", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@example.com", true), Linked: known(identity.MemberActive), Provisioned: known(identity.MemberActive)}, identity.SignInLinked, nil},
		{"a provisioned member beats the email's", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Provisioned: known(identity.MemberActive), ByEmail: known(identity.MemberActive)}, identity.LinkProvisioned, nil},
		{"a deactivated provisioned member is refused as such", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Provisioned: known(identity.MemberDeactivated)}, 0, identity.ErrMemberDeactivated},
		{"a verified email in the domains links an active member", identity.ResolveInput{Provider: provider(true, false, true), Claims: claims("X@Example.com", true), ByEmail: known(identity.MemberActive)}, identity.LinkByEmail, nil},
		{"linking by email off refuses an active member", identity.ResolveInput{Provider: provider(false, true, true), Claims: claims("x@example.com", true), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"an unverified email links nobody", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"an unverified email is fine where verification is not required", identity.ResolveInput{Provider: provider(true, false, false), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberActive)}, identity.LinkByEmail, nil},
		{"an email outside the domains links nobody", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@other.com", true), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"a subdomain is not the domain", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@mail.example.com", true)}, 0, identity.ErrMemberNotFound},
		{"a deactivated member holding the email is refused as such", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@example.com", true), ByEmail: known(identity.MemberDeactivated)}, 0, identity.ErrMemberDeactivated},
		{"a deactivated member outside the domains is not revealed", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@other.com", true), ByEmail: known(identity.MemberDeactivated)}, 0, identity.ErrMemberNotFound},
		{"an invited member is not signed in", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), ByEmail: known(identity.MemberInvited)}, 0, identity.ErrMemberNotFound},
		{"an unknown email is provisioned", identity.ResolveInput{Provider: provider(false, true, true), Claims: claims("x@example.com", true)}, identity.Provision, nil},
		{"an unknown email is not provisioned unless the provider provisions", identity.ResolveInput{Provider: provider(true, false, true), Claims: claims("x@example.com", true)}, 0, identity.ErrMemberNotFound},
		{"a malformed email is nobody's", identity.ResolveInput{Provider: provider(true, true, false), Claims: claims("example.com", true)}, 0, identity.ErrMemberNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := identity.Resolve(tc.in)
			if !errors.Is(err, tc.wantErr) || got.Action != tc.want {
				t.Errorf("Resolve = %v, %v; want action %v, error %v", got.Action, err, tc.want, tc.wantErr)
			}
		})
	}
}
