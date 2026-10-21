package management_test

import (
	"crypto/rand"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// memberOf reads the member m through c, failing unless it is answered.
func memberOf(t *testing.T, c *management.ClientWithResponses, m access.MemberID) management.Member {
	t.Helper()
	res := do(c.GetMemberWithResponse(t.Context(), m.String()))
	if res.JSON200 == nil {
		t.Fatalf("GET /members/%s = %d: %s", m, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// listedEmails returns the emails GET /members answers with params.
func listedEmails(t *testing.T, c *management.ClientWithResponses, params management.ListMembersParams) []string {
	t.Helper()
	res := do(c.ListMembersWithResponse(t.Context(), &params))
	if res.JSON200 == nil {
		t.Fatalf("GET /members = %d: %s", res.StatusCode(), res.Body)
	}
	var emails []string
	for _, m := range res.JSON200.Items {
		emails = append(emails, m.Email)
	}
	return emails
}

func TestTheInstancesMembersAreListedFilteredAndRead(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	unique := strings.ToLower(rand.Text()[:10])
	invited := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberInvited),
		testkit.WithDisplayName("Grace Hopper "+unique), testkit.WithEmail("grace-"+unique+"@example.com"))
	active := testkit.NewMember(t, pool, testkit.WithEmail("ada-"+unique+"@example.com"))

	all := listedEmails(t, admin, management.ListMembersParams{})
	if !slices.Contains(all, invited.Email) || !slices.Contains(all, active.Email) || !slices.Contains(all, testkit.InstanceAdminEmail) {
		t.Errorf("members = %v, want both and the instance admin", all)
	}
	if !slices.IsSortedFunc(all, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }) {
		t.Errorf("members = %v, want them ordered by email", all)
	}
	status := management.MemberStatusInvited
	if got := listedEmails(t, admin, management.ListMembersParams{Status: &status}); !slices.Contains(got, invited.Email) ||
		slices.Contains(got, active.Email) {
		t.Errorf("invited members = %v, want the invited one and not the active one", got)
	}
	for _, q := range []string{strings.ToUpper("grace-" + unique), "grace HOPPER " + unique} {
		if got := listedEmails(t, admin, management.ListMembersParams{Q: &q}); !slices.Equal(got, []string{invited.Email}) {
			t.Errorf("q=%q: members = %v, want only %s", q, got, invited.Email)
		}
	}
	pattern := "%" + unique
	if got := listedEmails(t, admin, management.ListMembersParams{Q: &pattern}); len(got) != 0 {
		t.Errorf("q=%q: members = %v, want none: %% matches itself", pattern, got)
	}
	bogus := management.MemberStatus("gone")
	res := do(admin.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Status: &bogus}))
	assertProblem(t, "unknown status", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")

	got := memberOf(t, admin, invited.ID)
	if got.Email != invited.Email || got.DisplayName != "Grace Hopper "+unique || got.Status != management.MemberStatusInvited ||
		got.InstanceAdmin || got.LastSignInAt != nil || got.ProjectCount != 0 || !got.CreatedAt.Equal(testkit.Start) {
		t.Errorf("invited member = %+v", got)
	}
	if got := memberOf(t, admin, active.ID); got.LastSignInAt != nil {
		t.Errorf("last sign-in before any = %v, want null", got.LastSignInAt)
	}
	signedIn(t, srv, active)
	if got := memberOf(t, admin, active.ID); got.LastSignInAt == nil {
		t.Error("last sign-in after signing in = null")
	}
	for _, unknown := range []string{"0198a3f0-0000-7000-8000-000000000000", "not-an-id"} {
		res := do(admin.GetMemberWithResponse(t.Context(), unknown))
		assertProblem(t, "member "+unknown, res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	}
}

func TestAnInstanceAdminEditsAMember(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberInvited))
	other := testkit.NewMember(t, pool)
	renamed := "renamed-" + strings.ToLower(rand.Text()[:10]) + "@example.com"

	res := do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), management.MemberPatch{
		DisplayName: ptr("  After  "), Email: ptr(renamed), InstanceAdmin: ptr(true),
	}))
	if !status(t, "edit", res.StatusCode(), res.Body, http.StatusOK) {
		return
	}
	if got := *res.JSON200; got.DisplayName != "After" || got.Email != renamed || !got.InstanceAdmin ||
		got.Status != management.MemberStatusInvited {
		t.Errorf("edited = %+v, want renamed, an instance admin, still invited", got)
	}
	res = do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), management.MemberPatch{InstanceAdmin: ptr(false)}))
	if status(t, "demote", res.StatusCode(), res.Body, http.StatusOK) {
		if got := *res.JSON200; got.DisplayName != "After" || got.Email != renamed || got.InstanceAdmin {
			t.Errorf("demoted = %+v, want the other fields as they were", got)
		}
	}
	if got := memberOf(t, admin, m.ID); got.Email != renamed || got.DisplayName != "After" || got.InstanceAdmin {
		t.Errorf("read back = %+v", got)
	}

	for _, taken := range []string{other.Email, strings.ToUpper(other.Email)} {
		refused := do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), management.MemberPatch{Email: ptr(taken)}))
		assertProblem(t, "email "+taken, refused.StatusCode(), refused.Body, http.StatusConflict, "already_exists")
	}
	for name, patch := range map[string]management.MemberPatch{
		"empty name":      {DisplayName: ptr("")},
		"blank name":      {DisplayName: ptr("   ")},
		"long name":       {DisplayName: ptr(strings.Repeat("n", 201))},
		"malformed email": {Email: ptr("not an email")},
	} {
		refused := do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), patch))
		p := assertProblem(t, name, refused.StatusCode(), refused.Body, http.StatusUnprocessableEntity, "invalid_member")
		if p.Errors == nil || len(*p.Errors) != 1 {
			t.Errorf("%s: violations = %+v, want one", name, p.Errors)
		}
	}
	if got := memberOf(t, admin, m.ID); got.Email != renamed || got.DisplayName != "After" {
		t.Errorf("after refused edits = %+v, want unchanged", got)
	}

	updated := auditOf(t, srv, "member.updated", m.ID.String())
	if len(updated) != 1 {
		t.Fatalf("member.updated entries = %d, want 1: an edit leaving the fields as they were records nothing", len(updated))
	}
	if after := updated[0].After.(map[string]any); after["display_name"] != "After" || after["email"] != renamed {
		t.Errorf("member.updated after = %v", after)
	}
	if n := len(auditOf(t, srv, "member.promoted", m.ID.String())); n != 1 {
		t.Errorf("member.promoted entries = %d, want 1", n)
	}
	if n := len(auditOf(t, srv, "member.demoted", m.ID.String())); n != 1 {
		t.Errorf("member.demoted entries = %d, want 1", n)
	}
	res = do(admin.UpdateMemberWithResponse(t.Context(), "0198a3f0-0000-7000-8000-000000000000", management.MemberPatch{}))
	assertProblem(t, "unknown member", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
}

func TestEverythingUnderMembersNeedsAnInstanceAdmin(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	target := testkit.NewMember(t, pool)
	caller := testkit.NewMember(t, pool)
	for name, c := range map[string]*management.ClientWithResponses{
		"a member's session": signedIn(t, srv, caller).ClientWithResponses,
		"a member's token": testkit.Client(t, srv, testkit.PersonalToken(t, pool, caller.ID, "everything",
			access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin)),
	} {
		id := target.ID.String()
		for op, res := range map[string]interface {
			StatusCode() int
		}{
			"list":       do(c.ListMembersWithResponse(t.Context(), nil)),
			"get":        do(c.GetMemberWithResponse(t.Context(), id)),
			"update":     do(c.UpdateMemberWithResponse(t.Context(), id, management.MemberPatch{InstanceAdmin: ptr(true)})),
			"deactivate": do(c.DeactivateMemberWithResponse(t.Context(), id)),
			"reactivate": do(c.ReactivateMemberWithResponse(t.Context(), id)),
		} {
			if res.StatusCode() != http.StatusForbidden {
				t.Errorf("%s, %s = %d, want 403", name, op, res.StatusCode())
			}
		}
	}
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	if got := memberOf(t, admin, target.ID); got.Status != management.MemberStatusActive || got.InstanceAdmin {
		t.Errorf("target = %+v, want unchanged", got)
	}

	adminID := memberIDByEmail(t, admin, testkit.InstanceAdminEmail)
	readOnly := testkit.Client(t, srv, testkit.PersonalToken(t, pool, adminID, "read only", access.ScopeRead))
	if res := do(readOnly.ListMembersWithResponse(t.Context(), nil)); res.StatusCode() != http.StatusOK {
		t.Errorf("an instance admin's read-only token lists = %d, want 200", res.StatusCode())
	}
	res := do(readOnly.DeactivateMemberWithResponse(t.Context(), target.ID.String()))
	assertProblem(t, "an instance admin's read-only token deactivates", res.StatusCode(), res.Body, http.StatusForbidden,
		"insufficient_scope")
}

// memberIDByEmail returns the identifier of the member with email, as c
// lists them.
func memberIDByEmail(t *testing.T, c *management.ClientWithResponses, email string) access.MemberID {
	t.Helper()
	res := do(c.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Q: &email}))
	if res.JSON200 == nil || len(res.JSON200.Items) != 1 {
		t.Fatalf("GET /members?q=%s = %d: %s", email, res.StatusCode(), res.Body)
	}
	return id.FromBytes[access.Member](res.JSON200.Items[0].Id)
}

func TestDeactivationRevokesEverySessionAndTokenAndReactivationRestoresSignIn(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, pool)
	browser := signedIn(t, srv, m)
	token := testkit.Client(t, srv, testkit.PersonalToken(t, pool, m.ID, "script"))
	cookie := browser.Cookie(management.SessionCookie)

	res := do(admin.DeactivateMemberWithResponse(t.Context(), m.ID.String()))
	if !status(t, "deactivate", res.StatusCode(), res.Body, http.StatusOK) {
		return
	}
	if res.JSON200.Status != management.MemberStatusDeactivated {
		t.Errorf("deactivated = %+v", res.JSON200)
	}
	wantSignedOut(t, browser, "the member's session")
	byToken := do(token.GetMeWithResponse(t.Context()))
	assertProblem(t, "the member's token", byToken.StatusCode(), byToken.Body, http.StatusUnauthorized, "token_revoked")
	if code, problem, _ := signInAs(t, testkit.NewBrowser(t, srv.URL), m.Email, m.Password); code != http.StatusUnauthorized ||
		problem != "invalid_credentials" {
		t.Errorf("sign-in once deactivated = %d %s, want 401 invalid_credentials like a wrong password", code, problem)
	}
	if again := do(admin.DeactivateMemberWithResponse(t.Context(), m.ID.String())); again.StatusCode() != http.StatusOK {
		t.Errorf("deactivating again = %d, want 200", again.StatusCode())
	}
	deactivated := auditOf(t, srv, "member.deactivated", m.ID.String())
	if len(deactivated) != 1 {
		t.Fatalf("member.deactivated entries = %d, want 1: deactivating again changes nothing", len(deactivated))
	}
	if after := deactivated[0].After.(map[string]any); after["status"] != "deactivated" ||
		after["sessions_revoked"] != float64(1) || after["tokens_revoked"] != float64(1) {
		t.Errorf("member.deactivated after = %v, want the status and one session and one token revoked", after)
	}

	res2 := do(admin.ReactivateMemberWithResponse(t.Context(), m.ID.String()))
	if !status(t, "reactivate", res2.StatusCode(), res2.Body, http.StatusOK) {
		return
	}
	if got := res2.JSON200.Member; got.Status != management.MemberStatusActive || res2.JSON200.Invitation != nil {
		t.Errorf("reactivated = %+v, want active and no invitation: they hold a password", got)
	}
	signedIn(t, srv, m)
	// The browser dropped the cookie it was told had ended; one kept elsewhere
	// still signs nobody in.
	kept := testkit.NewBrowser(t, srv.URL)
	kept.SetCookie(management.SessionCookie, cookie)
	wantSignedOut(t, kept, "the session revoked at deactivation")
	byToken = do(token.GetMeWithResponse(t.Context()))
	assertProblem(t, "the token revoked at deactivation", byToken.StatusCode(), byToken.Body, http.StatusUnauthorized, "token_revoked")
	if again := do(admin.ReactivateMemberWithResponse(t.Context(), m.ID.String())); again.StatusCode() != http.StatusOK {
		t.Errorf("reactivating an active member = %d, want 200", again.StatusCode())
	}
	if n := len(auditOf(t, srv, "member.reactivated", m.ID.String())); n != 1 {
		t.Errorf("member.reactivated entries = %d, want 1", n)
	}
}

func TestTheLastActiveInstanceAdminCannotBeDeactivated(t *testing.T) {
	srv, pool, admin := freshInstance(t, testkit.Clock())
	first := memberIDByEmail(t, admin, testkit.InstanceAdminEmail)
	// An instance admin who has not joined yet administers nothing.
	testkit.NewMember(t, pool, testkit.AsInstanceAdmin(), testkit.WithStatus(access.MemberInvited))

	res := do(admin.DeactivateMemberWithResponse(t.Context(), first.String()))
	assertProblem(t, "deactivating the last", res.StatusCode(), res.Body, http.StatusConflict, "last_instance_admin")
	demote := do(admin.UpdateMemberWithResponse(t.Context(), first.String(), management.MemberPatch{InstanceAdmin: ptr(false)}))
	assertProblem(t, "demoting the last", demote.StatusCode(), demote.Body, http.StatusConflict, "last_instance_admin")
	if got := memberOf(t, admin, first); got.Status != management.MemberStatusActive || !got.InstanceAdmin {
		t.Errorf("the last instance admin = %+v, want unchanged", got)
	}

	second := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
	if ok := do(admin.DeactivateMemberWithResponse(t.Context(), first.String())); ok.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating one of two = %d: %s", ok.StatusCode(), ok.Body)
	}
	other := signedIn(t, srv, second)
	res = do(other.DeactivateMemberWithResponse(t.Context(), second.ID.String()))
	assertProblem(t, "deactivating the one left", res.StatusCode(), res.Body, http.StatusConflict, "last_instance_admin")
}

func TestAMemberCountsTheProjectsTheyHoldAGrantIn(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, pool)
	a, b := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	if got := memberOf(t, admin, m.ID); got.ProjectCount != 0 {
		t.Errorf("before any grant: project count = %d, want 0", got.ProjectCount)
	}

	grant(t, admin, a.Key, m.ID, "developer")
	grant(t, admin, a.Key, m.ID, "viewer")
	second := grant(t, admin, b.Key, m.ID, "viewer")
	q := m.Email
	listed := do(admin.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Q: &q}))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].ProjectCount != 2 {
		t.Errorf("listed = %d %s, want two projects, however many grants", listed.StatusCode(), listed.Body)
	}
	do(admin.DeleteGrantWithResponse(t.Context(), b.Key, second.Id.String()))
	updated := do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), management.MemberPatch{DisplayName: ptr("Counted")}))
	if updated.JSON200 == nil || updated.JSON200.ProjectCount != 1 {
		t.Errorf("after a removal = %d %s, want one project", updated.StatusCode(), updated.Body)
	}
	// A deactivated member keeps their grants, so their count, but no longer
	// counts among the project's members.
	deactivated := do(admin.DeactivateMemberWithResponse(t.Context(), m.ID.String()))
	if deactivated.JSON200 == nil || deactivated.JSON200.ProjectCount != 1 {
		t.Errorf("deactivated = %d %s, want one project", deactivated.StatusCode(), deactivated.Body)
	}
	if got := do(admin.GetProjectWithResponse(t.Context(), a.Key)); got.JSON200 == nil || got.JSON200.MemberCount != 0 {
		t.Errorf("the project = %d %s, want no member counted", got.StatusCode(), got.Body)
	}
}

func TestAProjectCountsItsMembers(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	if p.MemberCount != 0 {
		t.Errorf("a new project counts %d members, want 0", p.MemberCount)
	}
	other := createProject(t, admin, projectKey())
	members := []testkit.Member{testkit.NewMember(t, pool), testkit.NewMember(t, pool), testkit.NewMember(t, pool)}
	for _, m := range members {
		grant(t, admin, p.Key, m.ID, "developer")
	}
	grant(t, admin, p.Key, members[0].ID, "owner")
	do(admin.DeactivateMemberWithResponse(t.Context(), members[2].ID.String()))

	if got := do(admin.UpdateProjectWithResponse(t.Context(), p.Key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: ptr("Counted")})); got.JSON200 == nil ||
		got.JSON200.MemberCount != 2 {
		t.Errorf("updated = %d %s, want two active members", got.StatusCode(), got.Body)
	}
	mine := do(testkit.Client(t, srv, testkit.PersonalToken(t, pool, members[0].ID, "lister")).ListProjectsWithResponse(t.Context()))
	if mine.JSON200 == nil || len(mine.JSON200.Items) != 1 || mine.JSON200.Items[0].MemberCount != 2 {
		t.Errorf("the member's projects = %d %s, want theirs alone, counting two", mine.StatusCode(), mine.Body)
	}
	if got := do(admin.GetProjectWithResponse(t.Context(), other.Key)); got.JSON200 == nil || got.JSON200.MemberCount != 0 {
		t.Errorf("another project = %d %s, want no member", got.StatusCode(), got.Body)
	}
}

func TestAnInvitedMemberCountsNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	invited := invite(t, admin, "Invited")
	m := id.FromBytes[access.Member](invited.Member.Id)
	grant(t, admin, p.Key, m, "developer")

	if got := memberOf(t, admin, m); got.ProjectCount != 0 {
		t.Errorf("an invited member with a grant counts %d projects, want 0", got.ProjectCount)
	}
	if got := do(admin.GetProjectWithResponse(t.Context(), p.Key)); got.JSON200 == nil || got.JSON200.MemberCount != 0 {
		t.Errorf("the project = %d %s, want the invited member not counted", got.StatusCode(), got.Body)
	}
}

func TestTheCountsCostOneQueryPerPage(t *testing.T) {
	pool, queries := testkit.TracedPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	for range 20 {
		m := testkit.NewMember(t, pool)
		testkit.Grant(t, pool, projectIDOf(createProject(t, admin, projectKey())), m.ID, access.RoleDeveloper)
	}
	queries.Reset()
	listed := do(admin.ListMembersWithResponse(t.Context(), nil))
	if listed.JSON200 == nil || len(listed.JSON200.Items) < 21 {
		t.Fatalf("GET /members = %d: %s", listed.StatusCode(), listed.Body)
	}
	if n := queries.Containing("project_count"); n != 1 {
		t.Errorf("queries counting projects = %d, want 1 for the page", n)
	}
	queries.Reset()
	projects := do(admin.ListProjectsWithResponse(t.Context()))
	if projects.JSON200 == nil || len(projects.JSON200.Items) < 20 {
		t.Fatalf("GET /projects = %d: %s", projects.StatusCode(), projects.Body)
	}
	if n := queries.Containing("member_count"); n != 1 {
		t.Errorf("queries counting members = %d, want 1 for the page", n)
	}
}
