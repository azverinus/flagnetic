package identity_test

import (
	"errors"
	"slices"
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
		{"a link to an invited member activates them, however the email reads", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@elsewhere.org", false), Linked: known(identity.MemberInvited)}, identity.ActivateLinked, nil},
		{"a provisioned member is linked by the external ID, whatever their email", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@elsewhere.org", false), Provisioned: known(identity.MemberActive)}, identity.LinkProvisioned, nil},
		{"a linked subject beats a provisioned member", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@example.com", true), Linked: known(identity.MemberActive), Provisioned: known(identity.MemberActive)}, identity.SignInLinked, nil},
		{"a provisioned member beats the email's", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Provisioned: known(identity.MemberActive), ByEmail: known(identity.MemberActive)}, identity.LinkProvisioned, nil},
		{"a deactivated provisioned member is refused as such", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Provisioned: known(identity.MemberDeactivated)}, 0, identity.ErrMemberDeactivated},
		{"a provisioned member who is invited signs nobody in", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", true), Provisioned: known(identity.MemberInvited)}, 0, identity.ErrMemberNotFound},
		{"a verified email in the domains links an active member", identity.ResolveInput{Provider: provider(true, false, true), Claims: claims("X@Example.com", true), ByEmail: known(identity.MemberActive)}, identity.LinkByEmail, nil},
		{"linking by email off refuses an active member", identity.ResolveInput{Provider: provider(false, true, true), Claims: claims("x@example.com", true), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"an unverified email links nobody", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"an unverified email is fine where verification is not required", identity.ResolveInput{Provider: provider(true, false, false), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberActive)}, identity.LinkByEmail, nil},
		{"an email outside the domains links nobody", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@other.com", true), ByEmail: known(identity.MemberActive)}, 0, identity.ErrMemberNotFound},
		{"a subdomain is not the domain", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@mail.example.com", true)}, 0, identity.ErrMemberNotFound},
		{"a deactivated member holding the email is refused as such", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("x@example.com", true), ByEmail: known(identity.MemberDeactivated)}, 0, identity.ErrMemberDeactivated},
		{"a deactivated member outside the domains is not revealed", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@other.com", true), ByEmail: known(identity.MemberDeactivated)}, 0, identity.ErrMemberNotFound},
		{"a verified email in the domains activates an invited member, linking by email off", identity.ResolveInput{Provider: provider(false, false, true), Claims: claims("X@Example.com", true), ByEmail: known(identity.MemberInvited)}, identity.ActivateInvited, nil},
		{"an unverified email activates no invited member", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberInvited)}, 0, identity.ErrMemberNotFound},
		{"an unverified email activates an invited member where verification is not required", identity.ResolveInput{Provider: provider(false, false, false), Claims: claims("x@example.com", false), ByEmail: known(identity.MemberInvited)}, identity.ActivateInvited, nil},
		{"an email outside the domains activates no invited member", identity.ResolveInput{Provider: provider(true, true, true), Claims: claims("x@other.com", true), ByEmail: known(identity.MemberInvited)}, 0, identity.ErrMemberNotFound},
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

func TestTheProvidersAcceptingAnInvitationVouchForItsEmail(t *testing.T) {
	corp := identity.Provider{Slug: "corp", Domains: []string{"corp.example"}}
	both := identity.Provider{Slug: "both", Domains: []string{"example.com", "corp.example"}}
	other := identity.Provider{Slug: "other", Domains: []string{"elsewhere.org"}}
	slugs := func(ps []identity.Provider) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, p.Slug)
		}
		return out
	}
	for _, tc := range []struct {
		name, email, enforced string
		want                  []string
	}{
		{"those holding the domain, in their order", "Carol@Corp.Example", "", []string{"corp", "both"}},
		{"only the enforced provider", "carol@corp.example", "both", []string{"both"}},
		{"none for a domain no provider holds", "carol@unknown.example", "", []string{}},
		{"none for a subdomain", "carol@mail.corp.example", "", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := identity.InvitationProviders([]identity.Provider{corp, both, other}, tc.email, tc.enforced)
			if got == nil || !slices.Equal(slugs(got), tc.want) {
				t.Errorf("InvitationProviders(%s, %q) = %v, want %v", tc.email, tc.enforced, slugs(got), tc.want)
			}
		})
	}
}

// Emails are ASCII-case-insensitive byte strings. Unicode folding maps U+212A
// KELVIN SIGN to k, so an address written with it must not be taken for the
// ASCII one.
func TestEmailsFoldAsciiLettersOnly(t *testing.T) {
	const kelvin = "K"
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"Kate@Example.COM", "kate@example.com", true},
		{kelvin + "ate@example.com", "kate@example.com", false},
		{"kate@example.com", kelvin + "ate@example.com", false},
		{kelvin + "ate@example.com", kelvin + "ATE@example.com", true},
		{"Ünal@example.com", "ünal@example.com", false},
	} {
		if got := identity.SameEmail(tc.a, tc.b); got != tc.same {
			t.Errorf("SameEmail(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}

	p := identity.Provider{Domains: []string{"kelvin.example"}}
	for email, want := range map[string]bool{
		"x@kelvin.example": true, "x@KELVIN.example": true, "x@" + kelvin + "elvin.example": false, "x@kelvin.example" + kelvin: false,
	} {
		if got := p.VouchesForEmail(email); got != want {
			t.Errorf("VouchesForEmail(%q) = %v, want %v", email, got, want)
		}
	}

	// A claim whose address only Unicode folding equates with a member's
	// resolves to nobody: the store does not find the member, so a provider
	// that does not provision refuses it.
	in := identity.ResolveInput{Provider: provider(true, false, false), Claims: claims(kelvin+"ate@example.com", true)}
	if _, err := identity.Resolve(in); !errors.Is(err, identity.ErrMemberNotFound) {
		t.Errorf("a claim of a lookalike address = %v, want member_not_found", err)
	}
}
