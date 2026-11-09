package sso_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// inviteTo invites email to project with role as the instance admin, or to
// the instance alone when project is "", and returns the invitation's secret
// and the invited member.
func (e env) inviteTo(t *testing.T, email, project, role string) (string, management.Member) {
	t.Helper()
	body := management.MemberInvite{Email: email, DisplayName: "Invited " + email}
	if project != "" {
		body.Grant = &management.InvitationGrantCreate{Project: project, Role: role}
	}
	res, err := e.admin.InviteMemberWithResponse(t.Context(), body)
	if err != nil || res.JSON201 == nil || res.JSON201.Invitation == nil || res.JSON201.Invitation.Url == nil {
		t.Fatalf("inviting %s: %v, %d: %s", email, err, res.StatusCode(), res.Body)
	}
	link := *res.JSON201.Invitation.Url
	secret := link[strings.LastIndex(link, "/")+1:]
	testkit.RecordSecret(secret)
	return secret, res.JSON201.Member
}

// statusOf returns the status of the member with the identifier.
func (e env) statusOf(t *testing.T, member fmt.Stringer) management.MemberStatus {
	t.Helper()
	res, err := e.admin.GetMemberWithResponse(t.Context(), member.String())
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET the member: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return res.JSON200.Status
}

func TestAnInvitedPersonSigningInThroughDexIsActivatedAndHoldsTheGrantTheirInvitationCarries(t *testing.T) {
	dex := testkit.StartDex(t)
	// Dex knows a few people only, whom other tests sign in too.
	e := startFresh(t)
	slug := e.create(t, provider{
		slug: testkit.DexSlug(t), issuer: dex.Issuer, clientID: dex.ClientID, clientSecret: dex.ClientSecret, domains: []string{"example.com"},
	})
	alpha := e.project(t)
	grace := dex.Users[1]
	secret, invited := e.inviteTo(t, grace.Email, alpha, "developer")
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.DexLogin(grace)

	v := e.signIn(t, b, slug, "/projects/"+alpha, o)

	if v.URL.Path != "/projects/"+alpha {
		t.Fatalf("the sign-in ended at %s (%d)", v.URL, v.Status)
	}
	me := signedIn(t, b)
	if me == nil || me.Member == nil || me.Member.Id != invited.Id {
		t.Fatalf("after signing in: %+v, want the invited member", me)
	}
	if got := e.statusOf(t, invited.Id); got != management.MemberStatusActive {
		t.Errorf("the invited member is %s after signing in, want active", got)
	}
	e.wantGrants(t, alpha, invited.Id, management.GrantSourceManual, "", "developer")
	accepted := e.audited(t, "member.invitation_accepted", "status", string(access.MemberActive))
	var mine []map[string]any
	for _, a := range accepted {
		if a["provider"] == slug {
			mine = append(mine, a)
		}
	}
	if len(mine) != 1 || mine[0]["method"] != "oidc" {
		t.Errorf("member.invitation_accepted through %s = %v, want one by oidc", slug, accepted)
	}
	if got := e.audited(t, "grant.created", "role", "developer"); len(got) != 1 {
		t.Errorf("grant.created = %v, want the invitation's grant", got)
	}
	if got := e.audited(t, "identity.linked", "member_email", grace.Email); len(got) != 1 || got[0]["provider"] != slug {
		t.Errorf("identity.linked = %v", got)
	}
	gone, err := testkit.NewBrowser(t, e.srv.URL).GetInvitationWithResponse(t.Context(), secret)
	if err != nil || gone.StatusCode() != http.StatusGone {
		t.Errorf("the invitation after signing in = %d, %v; want it gone", gone.StatusCode(), err)
	}

	// The identity the sign-in linked signs the member in from now on.
	again := testkit.NewBrowser(t, e.srv.URL)
	if v = e.signIn(t, again, slug, "", o); v.URL.Path != "/" {
		t.Errorf("the second sign-in ended at %s", v.URL)
	}
	e.wantGrants(t, alpha, invited.Id, management.GrantSourceManual, "", "developer")
}

func TestAnInvitedPersonOutsideTheProvidersDomainsIsNotActivated(t *testing.T) {
	dex := testkit.StartDex(t)
	e := startFresh(t)
	slug := e.create(t, provider{
		slug: testkit.DexSlug(t), issuer: dex.Issuer, clientID: dex.ClientID, clientSecret: dex.ClientSecret, domains: []string{"example.com"},
		linkByEmail: true, autoProvision: true,
	})
	alan := dex.Users[2] // alan@example.org
	secret, invited := e.inviteTo(t, alan.Email, "", "")
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.DexLogin(alan)

	v := e.signIn(t, b, slug, "", o)

	wantError(t, v, b, "member_not_found")
	if got := e.statusOf(t, invited.Id); got != management.MemberStatusInvited {
		t.Errorf("the invited member is %s, want invited still", got)
	}
	if open, err := testkit.NewBrowser(t, e.srv.URL).GetInvitationWithResponse(t.Context(), secret); err != nil || open.JSON200 == nil {
		t.Errorf("the invitation after the refusal = %d, %v; want it open still", open.StatusCode(), err)
	}
}

func TestAnExpiredInvitationStillActivatesThePersonButGrantsNothing(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	c := testkit.Clock()
	e := start(t, testkit.WithClock(c))
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision = false })
	alpha := e.project(t)
	email := uniqueEmail()
	_, invited := e.inviteTo(t, email, alpha, "viewer")
	c.Advance(access.InvitationLifetime + time.Hour)

	b := e.forgedPerson(t, idp, slug, email, map[string]any{"email_verified": true})

	if got := idOf(t, b); got != invited.Id {
		t.Errorf("signed in as %v, want the invited member %v", got, invited.Id)
	}
	if got := e.statusOf(t, invited.Id); got != management.MemberStatusActive {
		t.Errorf("the invited member is %s after signing in, want active", got)
	}
	e.wantGrants(t, alpha, invited.Id, management.GrantSourceManual, "")
	if got := e.audited(t, "member.invitation_accepted", "provider", slug); len(got) != 1 {
		t.Errorf("member.invitation_accepted = %v, want one", got)
	}
}

func TestAnInvitationWhoseInviterWasDeactivatedActivatesThePersonWithoutItsGrant(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("leads", alpha, "owner"))
	lead := e.forgedPerson(t, idp, slug, uniqueEmail(), map[string]any{"email_verified": true, "groups": []any{"leads"}})
	email := uniqueEmail()
	res, err := lead.InviteProjectMemberWithResponse(t.Context(), alpha, management.ProjectInvitationCreate{Email: email, DisplayName: "Invitee", Role: "developer"})
	if err != nil || res.JSON201 == nil || res.JSON201.Invitation == nil {
		t.Fatalf("inviting: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	link := *res.JSON201.Invitation.Url
	testkit.RecordSecret(link[strings.LastIndex(link, "/")+1:])
	if d, err := e.admin.DeactivateMemberWithResponse(t.Context(), idOf(t, lead).String()); err != nil || d.JSON200 == nil {
		t.Fatalf("deactivating the inviter: %v, %d: %s", err, d.StatusCode(), d.Body)
	}

	b := e.forgedPerson(t, idp, slug, email, map[string]any{"email_verified": true})

	e.wantGrants(t, alpha, idOf(t, b), management.GrantSourceManual, "")
	if got := e.statusOf(t, idOf(t, b)); got != management.MemberStatusActive {
		t.Errorf("the invited member is %s after signing in, want active", got)
	}
}

func TestAnInvitedMemberLinkedAtAProviderIsActivatedSigningInThroughIt(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email := uniqueEmail()
	member := idOf(t, e.forgedPerson(t, idp, slug, email, map[string]any{"email_verified": true}))
	// Reactivated while their only way in is at a disabled provider, the
	// member is invited again.
	if d, err := e.admin.DeactivateMemberWithResponse(t.Context(), member.String()); err != nil || d.JSON200 == nil {
		t.Fatalf("deactivating: %v, %d: %s", err, d.StatusCode(), d.Body)
	}
	if d, err := e.admin.DisableAuthProviderWithResponse(t.Context(), slug); err != nil || d.StatusCode() != http.StatusOK {
		t.Fatalf("disabling the provider: %v, %d: %s", err, d.StatusCode(), d.Body)
	}
	r, err := e.admin.ReactivateMemberWithResponse(t.Context(), member.String())
	if err != nil || r.JSON200 == nil || r.JSON200.Member.Status != management.MemberStatusInvited {
		t.Fatalf("reactivating: %v, %d: %s", err, r.StatusCode(), r.Body)
	}
	if r.JSON200.Invitation != nil && r.JSON200.Invitation.Url != nil {
		link := *r.JSON200.Invitation.Url
		testkit.RecordSecret(link[strings.LastIndex(link, "/")+1:])
	}
	if en, err := e.admin.EnableAuthProviderWithResponse(t.Context(), slug); err != nil || en.StatusCode() != http.StatusOK {
		t.Fatalf("enabling the provider: %v, %d: %s", err, en.StatusCode(), en.Body)
	}

	// Signing in under another address: the link, not the email, is the proof.
	b := e.forgedPerson(t, idp, slug, email, map[string]any{"email": "someone-else@elsewhere.org"})

	if got := idOf(t, b); got != member {
		t.Errorf("signed in as %v, want the linked member %v", got, member)
	}
	if got := e.statusOf(t, member); got != management.MemberStatusActive {
		t.Errorf("the member is %s after signing in, want active", got)
	}
	if got := e.audited(t, "identity.linked", "member_email", email); len(got) != 1 {
		t.Errorf("identity.linked = %d entries, want the first sign-in's alone", len(got))
	}
}

func TestAnInvitationAnswersTheProvidersThatMayAcceptIt(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	named := func(name string, domains ...string) func(*provider) {
		return func(p *provider) { noFallback(domains...)(p); p.displayName = name }
	}
	// Created out of the order of their names, which the answer follows.
	corp := e.forged(t, idp, named("Zeta SSO", "corp.example"))
	other := e.forged(t, idp, named("Alpha SSO", "corp.example", "example.com"))
	e.forged(t, idp, named("Beta SSO", "elsewhere.org"))
	disabled := e.forged(t, idp, named("Gamma SSO", "corp.example"))
	if d, err := e.admin.DisableAuthProviderWithResponse(t.Context(), disabled); err != nil || d.StatusCode() != http.StatusOK {
		t.Fatalf("disabling a provider: %v, %d", err, d.StatusCode())
	}
	anyone := testkit.NewBrowser(t, e.srv.URL)
	providersOf := func(secret string) []string {
		t.Helper()
		res, err := anyone.GetInvitationWithResponse(t.Context(), secret)
		if err != nil || res.JSON200 == nil {
			t.Fatalf("GET the invitation = %d: %v", res.StatusCode(), err)
		}
		slugs := []string{}
		for _, p := range res.JSON200.Providers {
			if p.Kind != management.AuthProviderKindOidc || p.DisplayName == "" {
				t.Errorf("a provider answers %+v", p)
			}
			slugs = append(slugs, p.Slug)
		}
		return slugs
	}
	carolEmail := "carol@corp.example"
	carol, invited := e.inviteTo(t, carolEmail, "", "")
	nobody, _ := e.inviteTo(t, "nobody@unknown.example", "", "")

	if got, want := providersOf(carol), []string{other, corp}; !slices.Equal(got, want) {
		t.Errorf("an invitation to corp.example offers %v, want %v, ordered by display name", got, want)
	}
	if got := providersOf(nobody); len(got) != 0 {
		t.Errorf("an invitation to a domain no provider holds offers %v", got)
	}

	enforce(t, e.admin, "corp.example", corp)
	if got := providersOf(carol); !slices.Equal(got, []string{corp}) {
		t.Errorf("an invitation to an enforced domain offers %v, want only %s", got, corp)
	}

	// Another provider vouching for the email does not accept it once its
	// domain is enforced; the enforced one does.
	idp.NextClaims(map[string]any{"sub": "sub-carol-other", "email": carolEmail, "email_verified": true})
	b := testkit.NewBrowser(t, e.srv.URL)
	v := e.signIn(t, b, other, "", e.opts())
	wantError(t, v, b, "sso_required")
	if v.Query("provider") != corp {
		t.Errorf("the refusal names %q, want %s", v.Query("provider"), corp)
	}
	if got := e.statusOf(t, invited.Id); got != management.MemberStatusInvited {
		t.Errorf("the invited member is %s after another provider's sign-in, want invited still", got)
	}
	if got := idOf(t, e.forgedPerson(t, idp, corp, carolEmail, map[string]any{"email_verified": true})); got != invited.Id {
		t.Errorf("signed in through the enforced provider as %v, want the invited member %v", got, invited.Id)
	}
}
