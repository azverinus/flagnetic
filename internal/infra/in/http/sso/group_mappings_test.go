package sso_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// The groups claim of a provider is read, by default, from `groups`; a Dex
// provider asks for it with the `groups` scope.
var dexScopes = []string{"openid", "email", "profile", "groups"}

// project creates a project through the instance admin's client and returns
// its key.
func (e env) project(t *testing.T) string {
	t.Helper()
	key := slug()
	res, err := e.admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Project " + key})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating a project: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return key
}

func projectMapping(group, project, role string) management.GroupMapping {
	return management.GroupMapping{Group: group, Project: &project, Role: &role}
}

func adminMapping(group string) management.GroupMapping {
	return management.GroupMapping{Group: group, InstanceAdmin: ptr(true)}
}

// mapGroups replaces the mappings of the provider slug as the instance admin
// and returns what running them did.
func (e env) mapGroups(t *testing.T, slug string, items ...management.GroupMapping) management.GroupMappingsRun {
	t.Helper()
	return mapGroupsAs(t, e.admin, slug, items...)
}

func mapGroupsAs(t *testing.T, c *management.ClientWithResponses, slug string, items ...management.GroupMapping) management.GroupMappingsRun {
	t.Helper()
	if items == nil {
		items = []management.GroupMapping{}
	}
	res, err := c.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), slug, management.GroupMappings{Items: items})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("replacing the group mappings of %s: %v, %d: %s", slug, err, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// grantsOf returns the grants member holds in project.
func (e env) grantsOf(t *testing.T, project string, member openapi_types.UUID) []management.Grant {
	t.Helper()
	res, err := e.admin.ListGrantsWithResponse(t.Context(), project)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("listing the grants of %s: %v, %d: %s", project, err, res.StatusCode(), res.Body)
	}
	var out []management.Grant
	for _, g := range res.JSON200.Items {
		if g.Member == member {
			out = append(out, g)
		}
	}
	return out
}

// wantGrants fails the test unless member holds exactly the roles in project
// from source, in any order.
func (e env) wantGrants(t *testing.T, project string, member openapi_types.UUID, source management.GrantSource, provider string, roles ...string) {
	t.Helper()
	var got []string
	for _, g := range e.grantsOf(t, project, member) {
		if g.Source != source {
			continue
		}
		if source == management.GrantSourceSso && (g.Provider == nil || *g.Provider != provider) {
			t.Errorf("an sso grant of %s names the provider %v, want %s", g.Role, g.Provider, provider)
		}
		if source == management.GrantSourceManual && g.Provider != nil {
			t.Errorf("a manual grant of %s names the provider %v", g.Role, *g.Provider)
		}
		got = append(got, string(g.Role))
	}
	slices.Sort(got)
	want := slices.Clone(roles)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s grants of %v in %s = %v, want %v", source, member, project, got, want)
	}
}

// entries returns the instance's audit entries of action made by actors of
// kind, newest first.
func (e env) entries(t *testing.T, action string, kind management.ActorKind) []management.AuditEntry {
	t.Helper()
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{
		Action: &action, ActorKind: &kind, Limit: ptr(200),
	})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	return res.JSON200.Items
}

// fieldsOf decodes an audit entry's before or after into a map.
func fieldsOf(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantProblem fails the test unless the response is an error with status and
// code.
func wantProblem(t *testing.T, what string, status int, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &p); status != wantStatus || err != nil || p.Code != wantCode {
		t.Errorf("%s: %d %s, want %d %s", what, status, body, wantStatus, wantCode)
	}
}

// forgedPerson signs a person in through the forging provider slug, the
// provider claiming claims on top of the subject and email, and fails the test
// unless the sign-in lands on /.
func (e env) forgedPerson(t *testing.T, idp *testkit.ForgingIDP, slug, email string, claims map[string]any) *testkit.Browser {
	t.Helper()
	all := map[string]any{"sub": "sub-" + email, "email": email}
	for k, v := range claims {
		all[k] = v
	}
	idp.NextClaims(all)
	b := testkit.NewBrowser(t, e.srv.URL)
	if v := e.signIn(t, b, slug, "", e.opts()); v.URL.Path != "/" {
		t.Fatalf("the sign-in of %s ended at %s", email, v.URL)
	}
	return b
}

func idOf(t *testing.T, b *testkit.Browser) openapi_types.UUID {
	t.Helper()
	me := signedIn(t, b)
	if me == nil || me.Member == nil {
		t.Fatal("the browser is signed in as nobody")
	}
	return me.Member.Id
}

// startFresh starts an instance on a database of its own, whose only instance
// admin is the bootstrap one: for a test that needs people nobody else signs
// in as, or an instance whose instance admins it counts.
func startFresh(t *testing.T) env {
	t.Helper()
	pool := testkit.FreshPool(t)
	e := start(t, testkit.WithPool(pool))
	e.admin = testkit.Client(t, e.srv, testkit.Bootstrap(t, pool))
	return e
}

func TestSigningInThroughDexGrantsWhatTheMappedGroupsMapTo(t *testing.T) {
	dex := testkit.StartDex(t)
	// Dex knows a few people only, whom other tests sign in too.
	e := startFresh(t)
	alpha, beta := e.project(t), e.project(t)
	slug := e.create(t, provider{
		slug: testkit.DexSlug(t), issuer: dex.Issuer, clientID: dex.ClientID, clientSecret: dex.ClientSecret,
		domains: []string{"example.com"}, autoProvision: true, scopes: dexScopes,
	})
	e.mapGroups(t, slug, projectMapping("engineering", alpha, "developer"), projectMapping("engineering", beta, "viewer"), adminMapping("admins"))
	ada, grace := dex.Users[0], dex.Users[1]
	o := e.opts()

	o.Login = testkit.DexLogin(ada)
	adaBrowser := testkit.NewBrowser(t, e.srv.URL)
	if v := e.signIn(t, adaBrowser, slug, "", o); v.URL.Path != "/" {
		t.Fatalf("Ada's sign-in ended at %s", v.URL)
	}
	adaID := idOf(t, adaBrowser)
	if me := signedIn(t, adaBrowser); !me.Member.InstanceAdmin {
		t.Error("the admins group did not make Ada an instance admin")
	}
	e.wantGrants(t, alpha, adaID, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, beta, adaID, management.GrantSourceSso, slug, "viewer")

	o.Login = testkit.DexLogin(grace)
	graceBrowser := testkit.NewBrowser(t, e.srv.URL)
	if v := e.signIn(t, graceBrowser, slug, "", o); v.URL.Path != "/" {
		t.Fatalf("Grace's sign-in ended at %s", v.URL)
	}
	graceID := idOf(t, graceBrowser)
	if me := signedIn(t, graceBrowser); me.Member.InstanceAdmin {
		t.Error("Grace is in no group that makes an instance admin")
	}
	e.wantGrants(t, alpha, graceID, management.GrantSourceSso, slug, "developer")

	t.Run("the grants are audited under the provider", func(t *testing.T) {
		created := e.entries(t, "grant.created", management.ActorKindSso)
		var forAda *management.AuditEntry
		for _, entry := range created {
			if after := fieldsOf(t, entry.After); after["email"] == ada.Email && after["role"] == "developer" {
				forAda = &entry
			}
		}
		if forAda == nil {
			t.Fatalf("no grant.created of Ada's developer role by the sso actor among %d entries", len(created))
		}
		after := fieldsOf(t, forAda.After)
		if forAda.Actor.Name != slug || after["source"] != "sso" || after["provider"] != slug {
			t.Errorf("the grant's actor = %+v, after = %v; want the provider %s", forAda.Actor, after, slug)
		}
		promoted := e.entries(t, "member.promoted", management.ActorKindSso)
		if len(promoted) == 0 || fieldsOf(t, promoted[0].After)["provider"] != slug {
			t.Errorf("member.promoted by the sso actor = %+v", promoted)
		}
	})

	t.Run("replacing the mappings runs them at once for everyone the provider knows", func(t *testing.T) {
		run := e.mapGroups(t, slug, projectMapping("engineering", alpha, "developer"))
		// Beta's viewer grant of both members, and Ada's instance admin, go
		// without anybody signing in.
		if run.Revoked != 3 || run.Granted != 0 || run.KeptInstanceAdmin != 0 {
			t.Errorf("run = %+v, want 3 revoked and nothing else", run)
		}
		e.wantGrants(t, beta, adaID, management.GrantSourceSso, slug)
		e.wantGrants(t, beta, graceID, management.GrantSourceSso, slug)
		e.wantGrants(t, alpha, adaID, management.GrantSourceSso, slug, "developer")
		if me := signedIn(t, adaBrowser); me.Member.InstanceAdmin {
			t.Error("Ada is still an instance admin after the mapping was removed")
		}
		updated := e.entries(t, "group_mappings.updated", management.ActorKindToken)
		if len(updated) == 0 || fieldsOf(t, updated[0].After)["revoked"] != float64(3) {
			t.Errorf("group_mappings.updated = %+v", updated)
		}

		run = e.mapGroups(t, slug, projectMapping("engineering", alpha, "developer"), projectMapping("engineering", beta, "owner"))
		if run.Granted != 2 || run.Revoked != 0 {
			t.Errorf("run = %+v, want the owner grants of the two members", run)
		}
		e.wantGrants(t, beta, graceID, management.GrantSourceSso, slug, "owner")

		if again := e.mapGroups(t, slug, projectMapping("engineering", alpha, "developer"), projectMapping("engineering", beta, "owner")); again != (management.GroupMappingsRun{}) {
			t.Errorf("running the same mappings again = %+v, want nothing changed", again)
		}
	})
}

func TestAGroupsClaimThatIsAbsentOrMalformedKeepsWhatTheProviderGranted(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	email := uniqueEmail()
	member := idOf(t, e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"eng"}}))
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")

	for name, claim := range map[string]any{
		"absent":                nil,
		"a single string":       "eng",
		"a number in the list":  []any{"eng", 7},
		"an object":             map[string]any{"eng": true},
		"a list holding a null": []any{"eng", nil},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(e.srv.Logs.Find(missingClaimLog))
			e.forgedPerson(t, idp, slug, email, map[string]any{"groups": claim})
			e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")
			if len(e.srv.Logs.Find(missingClaimLog)) != before+1 {
				t.Errorf("the claim was not logged as missing or malformed")
			}
		})
	}

	t.Run("an empty list is a claim: the person is in no group", func(t *testing.T) {
		e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{}})
		e.wantGrants(t, alpha, member, management.GrantSourceSso, slug)
	})
}

const missingClaimLog = "the groups claim is missing or is not a list of strings, so the member keeps the access the provider granted"

func TestAMemberLosesAGrantWhenTheirGroupIsGoneAndKeepsWhatWasGrantedByHand(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha, beta := e.project(t), e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"), projectMapping("ops", beta, "viewer"), projectMapping("leads", alpha, "owner"))
	email := uniqueEmail()
	member := idOf(t, e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"eng", "ops", "leads", "sales"}}))
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer", "owner")
	e.wantGrants(t, beta, member, management.GrantSourceSso, slug, "viewer")

	// A grant by hand of the same role sits beside the provider's.
	if res, err := e.admin.CreateGrantWithResponse(t.Context(), alpha, management.GrantCreate{Member: member, Role: "developer"}); err != nil || res.JSON201 == nil {
		t.Fatalf("granting by hand: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	e.wantGrants(t, alpha, member, management.GrantSourceManual, "", "developer")

	e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"eng"}})

	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, beta, member, management.GrantSourceSso, slug)
	e.wantGrants(t, alpha, member, management.GrantSourceManual, "", "developer")
	deleted := e.entries(t, "grant.deleted", management.ActorKindSso)
	if len(deleted) < 2 {
		t.Fatalf("grant.deleted by the sso actor: %d entries, want the owner and the viewer grants", len(deleted))
	}
	if before := fieldsOf(t, deleted[0].Before); before["source"] != "sso" || before["provider"] != slug {
		t.Errorf("the revocation records %v", before)
	}

	// Nothing changed, so nothing is recorded.
	count := len(e.entries(t, "grant.created", management.ActorKindSso))
	e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"eng"}})
	if n := len(e.entries(t, "grant.created", management.ActorKindSso)); n != count {
		t.Errorf("grant.created entries: %d, then %d after a sign-in that changed nothing", count, n)
	}
}

func TestAGrantAProviderMadeIsRemovedOnlyByTheProviderButAManualOneByHand(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	member := idOf(t, e.forgedPerson(t, idp, slug, uniqueEmail(), map[string]any{"groups": []any{"eng"}}))
	manual, err := e.admin.CreateGrantWithResponse(t.Context(), alpha, management.GrantCreate{Member: member, Role: "viewer"})
	if err != nil || manual.JSON201 == nil {
		t.Fatalf("granting by hand: %v, %d: %s", err, manual.StatusCode(), manual.Body)
	}
	var sso management.Grant
	for _, g := range e.grantsOf(t, alpha, member) {
		if g.Source == management.GrantSourceSso {
			sso = g
		}
	}

	res, err := e.admin.DeleteGrantWithResponse(t.Context(), alpha, sso.Id.String())
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "deleting an sso grant", res.StatusCode(), res.Body, http.StatusConflict, "sso_sourced")
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")

	if res, err := e.admin.DeleteGrantWithResponse(t.Context(), alpha, manual.JSON201.Id.String()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("deleting a manual grant: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")
}

func TestTwoProvidersKeepTheirOwnGrants(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	okta := e.forged(t, idp, nil)
	azure := e.forged(t, idp, func(p *provider) { p.linkByEmail = true })
	alpha := e.project(t)
	e.mapGroups(t, okta, projectMapping("eng", alpha, "developer"))
	e.mapGroups(t, azure, projectMapping("eng", alpha, "developer"))
	email := uniqueEmail()

	member := idOf(t, e.forgedPerson(t, idp, okta, email, map[string]any{"groups": []any{"eng"}}))
	e.forgedPerson(t, idp, azure, email, map[string]any{"sub": "azure-sub", "groups": []any{"eng"}})
	// Both granted the same role beside each other.
	if got := e.grantsOf(t, alpha, member); len(got) != 2 {
		t.Fatalf("grants = %+v, want one from each provider", got)
	}

	e.forgedPerson(t, idp, azure, email, map[string]any{"sub": "azure-sub", "groups": []any{}})

	e.wantGrants(t, alpha, member, management.GrantSourceSso, okta, "developer")
}

func TestAnInstanceAdminAMappingMadeIsDemotedOnlyByThatProvider(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	e.mapGroups(t, slug, adminMapping("it-admins"))
	email := uniqueEmail()
	b := e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"it-admins"}})
	member := idOf(t, b)
	if !signedIn(t, b).Member.InstanceAdmin {
		t.Fatal("the mapped group did not make the member an instance admin")
	}

	// By hand it is refused: the directory owns it.
	res, err := e.admin.UpdateMemberWithResponse(t.Context(), member.String(), management.MemberPatch{InstanceAdmin: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "demoting by hand", res.StatusCode(), res.Body, http.StatusConflict, "sso_sourced")

	t.Run("an instance admin made by hand is never taken away", func(t *testing.T) {
		handEmail := uniqueEmail()
		hand := idOf(t, e.forgedPerson(t, idp, slug, handEmail, map[string]any{"groups": []any{}}))
		if res, err := e.admin.UpdateMemberWithResponse(t.Context(), hand.String(), management.MemberPatch{InstanceAdmin: ptr(true)}); err != nil || res.JSON200 == nil {
			t.Fatalf("promoting by hand: %v, %d: %s", err, res.StatusCode(), res.Body)
		}
		e.forgedPerson(t, idp, slug, handEmail, map[string]any{"groups": []any{"it-admins"}})
		b := e.forgedPerson(t, idp, slug, handEmail, map[string]any{"groups": []any{}})
		if !signedIn(t, b).Member.InstanceAdmin {
			t.Error("losing the group took away an instance admin made by hand")
		}
	})

	t.Run("losing the group takes it away", func(t *testing.T) {
		b := e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{}})
		if signedIn(t, b).Member.InstanceAdmin {
			t.Error("an empty groups claim left the member an instance admin")
		}
		demoted := e.entries(t, "member.demoted", management.ActorKindSso)
		if len(demoted) == 0 || fieldsOf(t, demoted[0].Before)["provider"] != slug {
			t.Errorf("member.demoted by the sso actor = %+v", demoted)
		}
	})
}

func TestTheLastActiveInstanceAdminAMappingMadeIsKept(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startFresh(t)
	slug := e.forged(t, idp, nil)
	e.mapGroups(t, slug, adminMapping("it-admins"))
	email := uniqueEmail()
	b := e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"it-admins"}})

	// The mapped admin takes instance admin from the bootstrap one and is the
	// only active instance admin.
	bootstrap, err := e.admin.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Q: ptr(testkit.InstanceAdminEmail)})
	if err != nil || bootstrap.JSON200 == nil || len(bootstrap.JSON200.Items) != 1 {
		t.Fatalf("finding the bootstrap admin: %v, %d: %s", err, bootstrap.StatusCode(), bootstrap.Body)
	}
	if res, err := b.UpdateMemberWithResponse(t.Context(), bootstrap.JSON200.Items[0].Id.String(), management.MemberPatch{InstanceAdmin: ptr(false)}); err != nil || res.JSON200 == nil {
		t.Fatalf("demoting the bootstrap admin: %v, %d: %s", err, res.StatusCode(), res.Body)
	}

	// Losing the group on a sign-in keeps the member an instance admin.
	b = e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{}})
	if !signedIn(t, b).Member.InstanceAdmin {
		t.Fatal("losing the group took away the last active instance admin")
	}
	if len(e.srv.Logs.Find(keptAdminLog)) == 0 {
		t.Error("the instance admin kept was not logged")
	}

	// So does replacing the mappings, which says so.
	run := mapGroupsAs(t, b.ClientWithResponses, slug)
	if run.KeptInstanceAdmin != 1 || run.Revoked != 0 {
		t.Errorf("run = %+v, want the instance admin kept and nothing revoked", run)
	}
	if !signedIn(t, b).Member.InstanceAdmin {
		t.Error("replacing the mappings took away the last active instance admin")
	}
}

const keptAdminLog = "an auth provider's group mapping no longer makes a member instance admin, but they are the last active instance admin, so they stay"

func TestAProviderWhoseMappingsMadeAccessIsNotDeleted(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "viewer"))
	e.forgedPerson(t, idp, slug, uniqueEmail(), map[string]any{"groups": []any{"eng"}})

	res, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "deleting the provider", res.StatusCode(), res.Body, http.StatusConflict, "provider_in_use")
	var p struct {
		UsedBy []string `json:"used_by"`
	}
	if err = json.Unmarshal(res.Body, &p); err != nil || !slices.Contains(p.UsedBy, "grants") {
		t.Errorf("used_by = %v (%v), want it to name grants", p.UsedBy, err)
	}
}

func TestGroupMappingsAreListedInOrderAndValidated(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha, beta := e.project(t), e.project(t)

	empty, err := e.admin.GetAuthProviderGroupMappingsWithResponse(t.Context(), slug)
	if err != nil || empty.JSON200 == nil || empty.JSON200.Items == nil || len(empty.JSON200.Items) != 0 {
		t.Fatalf("a provider without mappings: %v, %d: %s", err, empty.StatusCode(), empty.Body)
	}

	e.mapGroups(t, slug, projectMapping("ops", beta, "viewer"), adminMapping("it"), projectMapping("eng", alpha, "owner"))
	got, err := e.admin.GetAuthProviderGroupMappingsWithResponse(t.Context(), slug)
	if err != nil || got.JSON200 == nil || len(got.JSON200.Items) != 3 {
		t.Fatalf("listing the mappings: %v, %d: %s", err, got.StatusCode(), got.Body)
	}
	items := got.JSON200.Items
	if items[0].Group != "ops" || *items[0].Project != beta || *items[0].Role != "viewer" || items[0].InstanceAdmin != nil ||
		items[1].Group != "it" || items[1].InstanceAdmin == nil || !*items[1].InstanceAdmin || items[1].Project != nil ||
		items[2].Group != "eng" || *items[2].Role != "owner" {
		t.Errorf("mappings = %+v, want them as replaced, in order", items)
	}

	for name, m := range map[string]management.GroupMapping{
		"an unknown project":           projectMapping("eng", "no-such-project", "viewer"),
		"a role outside the built-ins": projectMapping("eng", alpha, "admin"),
		"a project without a role":     {Group: "eng", Project: &alpha},
		"a role without a project":     {Group: "eng", Role: ptr("viewer")},
		"admin and a project":          {Group: "eng", Project: &alpha, Role: ptr("viewer"), InstanceAdmin: ptr(true)},
		"neither":                      {Group: "eng"},
		"a blank group":                projectMapping("  ", alpha, "viewer"),
	} {
		t.Run(name, func(t *testing.T) {
			res, err := e.admin.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), slug, management.GroupMappings{Items: []management.GroupMapping{m}})
			if err != nil {
				t.Fatal(err)
			}
			wantProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_group_mappings")
		})
	}
	if after, err := e.admin.GetAuthProviderGroupMappingsWithResponse(t.Context(), slug); err != nil || after.JSON200 == nil || len(after.JSON200.Items) != 3 {
		t.Errorf("after refused replacements: %v, %+v", err, after.JSON200)
	}

	t.Run("an unknown provider is not found", func(t *testing.T) {
		res, err := e.admin.GetAuthProviderGroupMappingsWithResponse(t.Context(), "no-such-provider")
		if err != nil || res.StatusCode() != http.StatusNotFound {
			t.Errorf("GET: %v, %d", err, res.StatusCode())
		}
		put, err := e.admin.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), "no-such-provider", management.GroupMappings{Items: []management.GroupMapping{}})
		if err != nil || put.StatusCode() != http.StatusNotFound {
			t.Errorf("PUT: %v, %d", err, put.StatusCode())
		}
	})

	t.Run("only an instance admin reads or replaces them", func(t *testing.T) {
		member := testkit.Client(t, e.srv, testkit.MemberToken(t, testkit.Pool(t)))
		if res, err := member.GetAuthProviderGroupMappingsWithResponse(t.Context(), slug); err != nil || res.StatusCode() != http.StatusForbidden {
			t.Errorf("GET: %v, %d", err, res.StatusCode())
		}
		put, err := member.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), slug, management.GroupMappings{Items: []management.GroupMapping{}})
		if err != nil || put.StatusCode() != http.StatusForbidden {
			t.Errorf("PUT: %v, %d", err, put.StatusCode())
		}
	})
}

func TestAMemberWhoLosesGrantManageThroughAMappingLosesTheGrantsOfTheirInvitations(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("leads", alpha, "owner"))
	email := uniqueEmail()
	b := e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{"leads"}})

	invitee := uniqueEmail()
	res, err := b.InviteProjectMemberWithResponse(t.Context(), alpha, management.ProjectInvitationCreate{Email: invitee, DisplayName: "Invitee", Role: "developer"})
	if err != nil || res.JSON201 == nil || res.JSON201.Invitation == nil {
		t.Fatalf("inviting: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	link := *res.JSON201.Invitation.Url
	testkit.RecordSecret(link[strings.LastIndex(link, "/")+1:])

	e.forgedPerson(t, idp, slug, email, map[string]any{"groups": []any{}})

	dropped := e.entries(t, "invitation.grant_revoked", management.ActorKindSso)
	if len(dropped) != 1 {
		t.Fatalf("invitation.grant_revoked by the sso actor: %d entries, want 1", len(dropped))
	}
	if before := fieldsOf(t, dropped[0].Before); before["email"] != invitee || before["role"] != "developer" {
		t.Errorf("the dropped grant = %v", before)
	}
}

func TestADeactivatedMemberKeepsNoGrantOfTheProviderAndGainsNone(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha, beta := e.project(t), e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	member := idOf(t, e.forgedPerson(t, idp, slug, uniqueEmail(), map[string]any{"groups": []any{"eng"}}))
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")
	if res, err := e.admin.DeactivateMemberWithResponse(t.Context(), member.String()); err != nil || res.JSON200 == nil {
		t.Fatalf("deactivating: %v, %d: %s", err, res.StatusCode(), res.Body)
	}

	run := e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"), projectMapping("eng", beta, "viewer"))

	if run.Revoked != 1 || run.Granted != 0 {
		t.Errorf("run = %+v, want the developer grant revoked and none made", run)
	}
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug)
	e.wantGrants(t, beta, member, management.GrantSourceSso, slug)
}

func TestPeopleSigningInForTheFirstTimeAtOnceAreAllGrantedWhatTheirGroupsMapTo(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	// Each person is held just before completing, so that every completion
	// then provisions a member, links them and applies their groups at the
	// same moment as the others.
	const people = 8
	browsers := make([]*testkit.Browser, people)
	completions := make([]string, people)
	for i := range people {
		email := uniqueEmail()
		idp.NextClaims(map[string]any{"sub": "sub-" + email, "email": email, "groups": []any{"eng"}})
		browsers[i] = testkit.NewBrowser(t, e.srv.URL)
		o := e.opts()
		o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
		held := e.signIn(t, browsers[i], slug, "", o)
		if !strings.HasPrefix(held.URL.Path, "/sso/complete/") {
			t.Fatalf("the sign-in reached %s before completing", held.URL)
		}
		completions[i] = held.URL.String()
	}

	ended := make([]testkit.Visit, people)
	var wg sync.WaitGroup
	for i := range people {
		wg.Go(func() { ended[i] = browsers[i].Visit(t, completions[i], e.opts()) })
	}
	wg.Wait()

	for i, v := range ended {
		if v.URL.Path != "/" {
			t.Errorf("sign-in %d ended at %s", i, v.URL)
			continue
		}
		e.wantGrants(t, alpha, idOf(t, browsers[i]), management.GrantSourceSso, slug, "developer")
	}
}
