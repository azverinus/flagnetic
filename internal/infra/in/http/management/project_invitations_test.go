package management_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// manager is a member holding a role in a project, acting through a
// personal access token.
type manager struct {
	testkit.Member
	*management.ClientWithResponses
}

// memberIn creates a member holding role in project and returns them acting
// through a token of the scopes a member who is no instance admin holds, or
// of ss when given.
func memberIn(t *testing.T, srv *testkit.Server, project management.Project, role access.RoleName, ss ...access.Scope) manager {
	t.Helper()
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool, testkit.WithDisplayName("Owner "+projectKey()))
	testkit.Grant(t, pool, projectIDOf(project), m.ID, role)
	if len(ss) == 0 {
		ss = []access.Scope{access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin}
	}
	return manager{Member: m, ClientWithResponses: testkit.Client(t, srv, testkit.PersonalToken(t, pool, m.ID, "inviter", ss...))}
}

// inviteTo invites a new person to project with role through c and returns
// the answer and the email invited, failing the test unless it succeeds.
func inviteTo(t *testing.T, c *management.ClientWithResponses, project, role string) (management.MemberInvitation, string) {
	t.Helper()
	email := newEmail()
	return mustInviteTo(t, c, project, email, role), email
}

func mustInviteTo(t *testing.T, c *management.ClientWithResponses, project, email, role string) management.MemberInvitation {
	t.Helper()
	res := do(c.InviteProjectMemberWithResponse(t.Context(), project,
		management.ProjectInvitationCreate{Email: email, DisplayName: "Invitee", Role: role}))
	if res.JSON201 == nil {
		t.Fatalf("POST /projects/%s/invitations (%s) = %d: %s", project, role, res.StatusCode(), res.Body)
	}
	recordLink(t, res.JSON201)
	return *res.JSON201
}

// memberRef is the identifier u names.
func memberRef(u openapi_types.UUID) access.MemberID { return id.FromBytes[access.Member](u) }

func newEmail() string { return strings.ToLower(projectKey()) + "@example.com" }

// acceptedBy accepts the invitation link opens through a new browser and
// fails the test unless it succeeds.
func acceptedBy(t *testing.T, srv *testkit.Server, link string) {
	t.Helper()
	if res := accept(t, testkit.NewBrowser(t, srv.URL), link, newPassword); res.JSON200 == nil {
		t.Fatalf("accepting = %d: %s", res.StatusCode(), res.Body)
	}
}

// grantsOf returns the grants the member with email holds in project, as c
// lists them; none for a member who holds none.
func grantsOf(t *testing.T, c *management.ClientWithResponses, project, email string) []management.Grant {
	t.Helper()
	for _, m := range projectMembers(t, c, project) {
		if m.Email == email {
			return m.Grants
		}
	}
	return nil
}

// projectEntries returns the entries of action in the log of project, as c
// reads it, newest first.
func projectEntries(t *testing.T, c *management.ClientWithResponses, project, action string) []management.AuditEntry {
	t.Helper()
	res := do(c.ListProjectAuditEntriesWithResponse(t.Context(), project, &management.ListProjectAuditEntriesParams{Action: &action}))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s/audit = %d: %s", project, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// entriesAbout returns the entries of action in the log of project about the
// member with email.
func entriesAbout(t *testing.T, c *management.ClientWithResponses, project, action, email string) []management.AuditEntry {
	t.Helper()
	var out []management.AuditEntry
	for _, e := range projectEntries(t, c, project, action) {
		if e.Entity.Key == email {
			out = append(out, e)
		}
	}
	return out
}

// wantGrantRevoked fails the test unless the log of project holds one
// invitation.grant_revoked about email, naming the role and reason, and
// returns it.
func wantGrantRevoked(t *testing.T, c *management.ClientWithResponses, project, email, role, reason string) management.AuditEntry {
	t.Helper()
	entries := entriesAbout(t, c, project, "invitation.grant_revoked", email)
	if len(entries) != 1 {
		t.Fatalf("%d invitation.grant_revoked entries about %s, want 1", len(entries), email)
	}
	before, _ := entries[0].Before.(map[string]any)
	after, _ := entries[0].After.(map[string]any)
	if before["role"] != role || before["email"] != email || after["reason"] != reason {
		t.Errorf("invitation.grant_revoked = %v -> %v, want the %s grant of %s dropped for %s", before, after, role, email, reason)
	}
	return entries[0]
}

// wantNoGrantRevoked fails the test when the log of project holds an
// invitation.grant_revoked.
func wantNoGrantRevoked(t *testing.T, c *management.ClientWithResponses, project string) {
	t.Helper()
	if n := len(projectEntries(t, c, project, "invitation.grant_revoked")); n != 0 {
		t.Errorf("%d invitation.grant_revoked entries in %s, want none", n, project)
	}
}

// wantShownWithoutProject fails the test unless the page link opens shows
// neither project nor role.
func wantShownWithoutProject(t *testing.T, srv *testkit.Server, link string) {
	t.Helper()
	got := opened(t, srv, link)
	if got.JSON200 == nil {
		t.Fatalf("GET the invitation = %d: %s", got.StatusCode(), got.Body)
	}
	if got.JSON200.Project != nil || got.JSON200.Role != nil {
		t.Errorf("invitation = %s, want neither project nor role", got.Body)
	}
}

// wantActive fails the test unless the member with email accepted and holds
// no grant in project, and project's log records no grant.created about them.
func wantActiveWithoutGrant(t *testing.T, admin *management.ClientWithResponses, project, email string) {
	t.Helper()
	if m := memberOf(t, admin, memberIDByEmail(t, admin, email)); m.Status != management.MemberStatusActive {
		t.Errorf("%s is %s, want active", email, m.Status)
	}
	if g := grantsOf(t, admin, project, email); len(g) != 0 {
		t.Errorf("the dropped invitation granted %+v", g)
	}
	if n := len(entriesAbout(t, admin, project, "grant.created", email)); n != 0 {
		t.Errorf("%d grant.created entries about %s, want none", n, email)
	}
}

func TestAnOwnerInvitesToTheirProjectAndAcceptingGrantsTheRole(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)

	mi, email := inviteTo(t, owner.ClientWithResponses, p.Key, "developer")
	if mi.Member.Status != management.MemberStatusInvited || mi.Member.Email != email || mi.Member.InstanceAdmin || mi.Member.DisplayName != "Invitee" {
		t.Errorf("invited member = %+v, want an invited non-admin %s", mi.Member, email)
	}
	if !mi.Invitation.ExpiresAt.Equal(c.Now().Add(7 * 24 * time.Hour)) {
		t.Errorf("expires at %v, want in seven days", mi.Invitation.ExpiresAt)
	}
	link := *mi.Invitation.Url
	if !strings.HasPrefix(link, srv.URL+"/invitations/") || len(secretOf(link)) != 43 {
		t.Errorf("link = %q, want the console's /invitations/ and 43 base64url characters", link)
	}

	// Until it is accepted, the invitation grants nothing.
	if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
		t.Errorf("before accepting, the invited member holds %+v", g)
	}

	// The page shows the project, the role and who invited.
	got := opened(t, srv, link)
	if got.JSON200 == nil {
		t.Fatalf("GET the invitation = %d: %s", got.StatusCode(), got.Body)
	}
	shown := got.JSON200
	if shown.Project == nil || shown.Project.Key != p.Key || shown.Project.Name != p.Name || shown.Role == nil || *shown.Role != "developer" ||
		shown.InvitedBy == nil || *shown.InvitedBy != memberOf(t, admin, owner.ID).DisplayName {
		t.Errorf("invitation = %+v, want %s, developer and %s", shown, p.Key, memberOf(t, admin, owner.ID).DisplayName)
	}

	// Accepting activates the member and grants exactly that role, by hand,
	// made by the owner who invited.
	acceptedBy(t, srv, link)
	grants := grantsOf(t, admin, p.Key, email)
	if len(grants) != 1 || grants[0].Role != "developer" || grants[0].Source != management.GrantSourceManual ||
		grants[0].CreatedBy == nil || *grants[0].CreatedBy != owner.ID.Bytes() {
		t.Fatalf("grants after accepting = %+v, want one developer grant by hand, made by the owner", grants)
	}
	memberID := memberIDByEmail(t, admin, email)
	if m := memberOf(t, admin, memberID); m.Status != management.MemberStatusActive || m.ProjectCount != 1 {
		t.Errorf("member after accepting = %+v, want active in one project", m)
	}

	// The project's log records the invitation and the grant, both by the
	// owner; the acceptance is the member's own and belongs to the instance.
	invited := entriesAbout(t, admin, p.Key, "member.invited", email)
	if len(invited) != 1 || invited[0].Actor.MemberId == nil || *invited[0].Actor.MemberId != owner.ID.Bytes() {
		t.Errorf("member.invited = %+v, want one, by the owner", invited)
	} else if after, _ := invited[0].After.(map[string]any); after["role"] != "developer" || after["status"] != "invited" {
		t.Errorf("member.invited after = %v, want the developer role of an invited member", invited[0].After)
	}
	created := entriesAbout(t, admin, p.Key, "grant.created", email)
	if len(created) != 1 || created[0].Actor.Kind != management.ActorKindMember || created[0].Actor.MemberId == nil ||
		*created[0].Actor.MemberId != owner.ID.Bytes() {
		t.Fatalf("grant.created = %+v, want one, with the inviting owner as actor", created)
	}
	if after, _ := created[0].After.(map[string]any); after["role"] != "developer" || after["source"] != "manual" || after["email"] != email {
		t.Errorf("grant.created after = %v, want the developer grant by hand of %s", created[0].After, email)
	}
	if acc := auditOf(t, srv, "member.invitation_accepted", memberID.String()); len(acc) != 1 || acc[0].Actor.MemberId == nil ||
		acc[0].Actor.MemberId.String() != memberID.String() {
		t.Errorf("member.invitation_accepted = %+v, want one, by the member", acc)
	}
	wantGone(t, srv, link, "a used link")
}

func TestAnInvitationOfTheInstanceAloneStillShowsNoProject(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Instance Only")
	wantShownWithoutProject(t, srv, *mi.Invitation.Url)
}

func TestAnInstanceAdminsInvitationMayCarryAGrant(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())

	email := newEmail()
	res := do(admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{
		Email: email, DisplayName: "With Role", Grant: &management.InvitationGrantCreate{Project: p.Key, Role: "maintainer"},
	}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members with a grant = %d: %s", res.StatusCode(), res.Body)
	}
	recordLink(t, res.JSON201)
	got := opened(t, srv, *res.JSON201.Invitation.Url)
	if got.JSON200 == nil || got.JSON200.Project == nil || got.JSON200.Project.Key != p.Key || got.JSON200.Role == nil ||
		*got.JSON200.Role != "maintainer" {
		t.Fatalf("invitation = %s, want maintainer in %s", got.Body, p.Key)
	}
	acceptedBy(t, srv, *res.JSON201.Invitation.Url)
	grants := grantsOf(t, admin, p.Key, email)
	if len(grants) != 1 || grants[0].Role != "maintainer" || grants[0].CreatedBy == nil || *grants[0].CreatedBy != adminID {
		t.Errorf("grants = %+v, want one maintainer grant made by the instance admin", grants)
	}

	for name, tc := range map[string]struct {
		grant   management.InvitationGrantCreate
		pointer string
	}{
		"an unknown project": {management.InvitationGrantCreate{Project: "no-such-project", Role: "viewer"}, "/grant/project"},
		"an unknown role":    {management.InvitationGrantCreate{Project: p.Key, Role: "admin"}, "/grant/role"},
	} {
		other := newEmail()
		refused := do(admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: other, DisplayName: "Refused", Grant: &tc.grant}))
		pr := assertProblem(t, name, refused.StatusCode(), refused.Body, http.StatusUnprocessableEntity, "invalid_grant")
		if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != tc.pointer {
			t.Errorf("%s: violations = %+v, want one at %s", name, pr.Errors, tc.pointer)
		}
		if ids := listedEmails(t, admin, management.ListMembersParams{Q: &other}); len(ids) != 0 {
			t.Errorf("%s: the refused invitation created %v", name, ids)
		}
	}
}

func TestOnlyAnOwnerOfTheProjectInvitesToIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	pool := testkit.Pool(t)

	for name, c := range map[string]*management.ClientWithResponses{
		"a maintainer":              memberIn(t, srv, p, access.RoleMaintainer).ClientWithResponses,
		"a member holding no grant": testkit.Client(t, srv, testkit.MemberToken(t, pool, access.ScopeRead, access.ScopeProjectAdmin)),
		"an owner of another":       memberIn(t, srv, other, access.RoleOwner).ClientWithResponses,
		"an owner's project token":  pipeline(t, srv, p, "owner", management.ScopeProjectAdmin),
	} {
		email := newEmail()
		res := do(c.InviteProjectMemberWithResponse(t.Context(), p.Key,
			management.ProjectInvitationCreate{Email: email, DisplayName: "Refused", Role: "viewer"}))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
		if pr.Permission == nil || *pr.Permission != "grant.manage" {
			// A member who cannot even read the project is told which
			// permission it lacks first.
			if pr.Permission == nil || *pr.Permission != "project.read" {
				t.Errorf("%s: the refusal names %v, want grant.manage", name, pr.Permission)
			}
		}
		if got := listedEmails(t, admin, management.ListMembersParams{Q: &email}); len(got) != 0 {
			t.Errorf("%s: the refused invitation created %v", name, got)
		}
	}

	res := do(admin.InviteProjectMemberWithResponse(t.Context(), "no-such-project",
		management.ProjectInvitationCreate{Email: newEmail(), DisplayName: "Nowhere", Role: "viewer"}))
	assertProblem(t, "an unknown project", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	t.Run("an instance admin invites as an owner would", func(t *testing.T) {
		_, email := inviteTo(t, admin, p.Key, "owner")
		if m := listedEmails(t, admin, management.ListMembersParams{Q: &email}); len(m) != 1 {
			t.Errorf("members matching %s = %v, want the one invited", email, m)
		}
	})
}

func TestAnInvitationNeverCarriesARoleItsInviterDoesNotHold(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	// An owner whose token carries no flag scope holds no flag permission
	// through it, so cannot hand out a role that has any.
	narrow := memberIn(t, srv, p, access.RoleOwner, access.ScopeRead, access.ScopeProjectAdmin)

	email := newEmail()
	res := do(narrow.InviteProjectMemberWithResponse(t.Context(), p.Key,
		management.ProjectInvitationCreate{Email: email, DisplayName: "Too Much", Role: "developer"}))
	pr := assertProblem(t, "a role beyond the token", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "role_not_allowed")
	if pr.Permission == nil {
		t.Error("role_not_allowed does not name the permission lacking")
	}
	if got := listedEmails(t, admin, management.ListMembersParams{Q: &email}); len(got) != 0 {
		t.Errorf("the refused invitation created %v", got)
	}
	mustInviteTo(t, narrow.ClientWithResponses, p.Key, newEmail(), "viewer")

	unknown := do(narrow.InviteProjectMemberWithResponse(t.Context(), p.Key,
		management.ProjectInvitationCreate{Email: newEmail(), DisplayName: "Admin?", Role: "admin"}))
	assertProblem(t, "a role that is none of the built-in ones", unknown.StatusCode(), unknown.Body, http.StatusUnprocessableEntity, "invalid_grant")

	invalid := do(narrow.InviteProjectMemberWithResponse(t.Context(), p.Key,
		management.ProjectInvitationCreate{Email: "not-an-email", DisplayName: "Bad", Role: "viewer"}))
	assertProblem(t, "a malformed email", invalid.StatusCode(), invalid.Body, http.StatusUnprocessableEntity, "invalid_member")
}

func TestInvitingAMemberTheInstanceHasIsRefusedAndPointsAtGranting(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)

	refused := func(t *testing.T, email string) {
		t.Helper()
		res := do(owner.InviteProjectMemberWithResponse(t.Context(), p.Key,
			management.ProjectInvitationCreate{Email: strings.ToUpper(email), DisplayName: "Again", Role: "viewer"}))
		pr := assertProblem(t, email, res.StatusCode(), res.Body, http.StatusConflict, "already_exists")
		if pr.Detail == nil || !strings.Contains(*pr.Detail, "grant") {
			t.Errorf("the refusal %v does not point at granting", pr.Detail)
		}
		if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
			t.Errorf("the refusal granted %+v", g)
		}
	}

	t.Run("an active member", func(t *testing.T) {
		refused(t, testkit.NewMember(t, testkit.Pool(t)).Email)
	})
	t.Run("a member invited to the instance alone", func(t *testing.T) {
		mi := invite(t, admin, "Instance Only")
		refused(t, mi.Member.Email)
		// Their invitation stands.
		if got := opened(t, srv, *mi.Invitation.Url); got.JSON200 == nil {
			t.Errorf("their invitation = %d: %s", got.StatusCode(), got.Body)
		}
	})
	t.Run("a member invited to another project", func(t *testing.T) {
		mi, email := inviteTo(t, admin, other.Key, "developer")
		refused(t, email)
		// The earlier invitation is untouched and grants its own role.
		acceptedBy(t, srv, *mi.Invitation.Url)
		if g := grantsOf(t, admin, other.Key, email); len(g) != 1 || g[0].Role != "developer" {
			t.Errorf("grants in %s = %+v, want developer", other.Key, g)
		}
	})
	t.Run("a deactivated member", func(t *testing.T) {
		refused(t, testkit.NewMember(t, testkit.Pool(t), testkit.WithStatus(access.MemberDeactivated)).Email)
	})
}

func TestInvitingSomebodyToTheSameProjectAgainReplacesTheInvitation(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)

	first, email := inviteTo(t, owner.ClientWithResponses, p.Key, "viewer")
	second := mustInviteTo(t, owner.ClientWithResponses, p.Key, strings.ToUpper(email), "maintainer")
	if second.Member.Id != first.Member.Id {
		t.Fatalf("the second invitation made member %s, want %s again", second.Member.Id, first.Member.Id)
	}
	wantGone(t, srv, *first.Invitation.Url, "the first link")
	if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
		t.Errorf("the replaced link granted %+v", g)
	}

	acceptedBy(t, srv, *second.Invitation.Url)
	if g := grantsOf(t, admin, p.Key, email); len(g) != 1 || g[0].Role != "maintainer" {
		t.Errorf("grants = %+v, want the maintainer role of the second invitation alone", g)
	}
	reissued := entriesAbout(t, admin, p.Key, "member.invitation_reissued", email)
	if len(reissued) != 1 || reissued[0].Actor.MemberId == nil || *reissued[0].Actor.MemberId != owner.ID.Bytes() {
		t.Errorf("member.invitation_reissued = %+v, want one, by the owner", reissued)
	}
}

func TestAnInstanceAdminsReissueKeepsTheGrantAndItsInviter(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)
	mi, email := inviteTo(t, owner.ClientWithResponses, p.Key, "developer")

	link := reissue(t, admin, mi.Member)
	wantGone(t, srv, *mi.Invitation.Url, "the replaced link")
	got := opened(t, srv, link)
	if got.JSON200 == nil || got.JSON200.Project == nil || got.JSON200.Project.Key != p.Key || got.JSON200.Role == nil ||
		*got.JSON200.Role != "developer" {
		t.Fatalf("the reissued invitation = %s, want developer in %s", got.Body, p.Key)
	}
	acceptedBy(t, srv, link)
	grants := grantsOf(t, admin, p.Key, email)
	if len(grants) != 1 || grants[0].Role != "developer" || grants[0].CreatedBy == nil || *grants[0].CreatedBy != owner.ID.Bytes() {
		t.Errorf("grants = %+v, want the developer grant of the owner who invited, not of the instance admin who reissued", grants)
	}
}

func TestAnInvitationNoLongerAcceptableGrantsNothing(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	t.Run("expired", func(t *testing.T) {
		mi, email := inviteTo(t, admin, p.Key, "viewer")
		c.Advance(7*24*time.Hour + time.Minute)
		wantGone(t, srv, *mi.Invitation.Url, "an expired link")
		if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
			t.Errorf("an expired invitation granted %+v", g)
		}
	})
	t.Run("its member was deactivated", func(t *testing.T) {
		mi, email := inviteTo(t, admin, p.Key, "viewer")
		if res := do(admin.DeactivateMemberWithResponse(t.Context(), mi.Member.Id.String())); res.JSON200 == nil {
			t.Fatalf("deactivating = %d: %s", res.StatusCode(), res.Body)
		}
		wantGone(t, srv, *mi.Invitation.Url, "a deactivated member's link")
		if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
			t.Errorf("a deactivated member's invitation granted %+v", g)
		}
	})
}

func TestAnInvitationGrantsBesideWhatTheMemberWasGivenMeanwhile(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	t.Run("another role", func(t *testing.T) {
		mi, email := inviteTo(t, admin, p.Key, "owner")
		grant(t, admin, p.Key, memberRef(mi.Member.Id), "viewer")
		acceptedBy(t, srv, *mi.Invitation.Url)
		if g := rolesOf(grantsOf(t, admin, p.Key, email)); len(g) != 2 {
			t.Errorf("roles = %v, want the viewer given by hand and the owner invited with", g)
		}
	})
	t.Run("the same role", func(t *testing.T) {
		mi, email := inviteTo(t, admin, p.Key, "viewer")
		grant(t, admin, p.Key, memberRef(mi.Member.Id), "viewer")
		acceptedBy(t, srv, *mi.Invitation.Url)
		if g := rolesOf(grantsOf(t, admin, p.Key, email)); len(g) != 1 || g[0] != "viewer" {
			t.Errorf("roles = %v, want the one viewer grant", g)
		}
		if n := len(entriesAbout(t, admin, p.Key, "grant.created", email)); n != 1 {
			t.Errorf("%d grant.created entries, want the one of the grant made by hand", n)
		}
	})
}

func TestDeletingTheProjectDropsWhatItsInvitationsGranted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	mi, email := inviteTo(t, admin, p.Key, "viewer")
	// Deleting needs a session.
	operator, _ := testkit.NewInstanceAdmin(t, testkit.Pool(t))
	if res := deleteProject(t, signedIn(t, srv, operator).ClientWithResponses, p.Key, p.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("deleting the project = %d: %s", res.StatusCode(), res.Body)
	}
	wantShownWithoutProject(t, srv, *mi.Invitation.Url)
	acceptedBy(t, srv, *mi.Invitation.Url)
	if m := memberOf(t, admin, memberIDByEmail(t, admin, email)); m.Status != management.MemberStatusActive || m.ProjectCount != 0 {
		t.Errorf("member = %+v, want active in no project", m)
	}
}

func TestAnInvitationLosesItsGrantWhenItsInviterLosesGrantManage(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, elsewhere := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)
	testkit.Grant(t, testkit.Pool(t), projectIDOf(elsewhere), owner.ID, access.RoleOwner)
	other := memberIn(t, srv, p, access.RoleOwner)

	dropped, droppedEmail := inviteTo(t, owner.ClientWithResponses, p.Key, "developer")
	kept, keptEmail := inviteTo(t, other.ClientWithResponses, p.Key, "viewer")
	keptElsewhere, elsewhereEmail := inviteTo(t, owner.ClientWithResponses, elsewhere.Key, "viewer")

	// Losing a grant that is not the owner's changes nothing.
	extra := grant(t, admin, p.Key, owner.ID, "viewer")
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, extra.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the viewer grant = %d: %s", res.StatusCode(), res.Body)
	}
	wantNoGrantRevoked(t, admin, p.Key)

	// Removing the owner's own grant drops theirs in this project alone.
	ownerGrants := grantsOf(t, admin, p.Key, ownerEmail(t, admin, owner))
	if len(ownerGrants) != 1 {
		t.Fatalf("the owner's grants = %+v, want the one owner grant", ownerGrants)
	}
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, ownerGrants[0].Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the owner grant = %d: %s", res.StatusCode(), res.Body)
	}
	revoked := wantGrantRevoked(t, admin, p.Key, droppedEmail, "developer", "inviter_lost_permission")
	if removed := projectEntries(t, admin, p.Key, "grant.deleted"); len(removed) == 0 || revoked.Actor.Id != removed[0].Actor.Id || revoked.Actor.Kind != removed[0].Actor.Kind {
		t.Errorf("invitation.grant_revoked actor = %+v, want the actor who removed the grant", revoked.Actor)
	}
	if n := len(projectEntries(t, admin, p.Key, "invitation.grant_revoked")); n != 1 {
		t.Errorf("%d invitation.grant_revoked entries, want 1: the other owner's and the other project's are untouched", n)
	}

	// The invitation still activates the member and grants nothing.
	wantShownWithoutProject(t, srv, *dropped.Invitation.Url)
	acceptedBy(t, srv, *dropped.Invitation.Url)
	wantActiveWithoutGrant(t, admin, p.Key, droppedEmail)

	for name, tc := range map[string]struct {
		mi            management.MemberInvitation
		project, mail string
		role          string
	}{
		"another owner's":                         {kept, p.Key, keptEmail, "viewer"},
		"the same owner's, to a project they own": {keptElsewhere, elsewhere.Key, elsewhereEmail, "viewer"},
	} {
		t.Run(name, func(t *testing.T) {
			acceptedBy(t, srv, *tc.mi.Invitation.Url)
			if g := grantsOf(t, admin, tc.project, tc.mail); len(g) != 1 || string(g[0].Role) != tc.role {
				t.Errorf("grants = %+v, want %s", g, tc.role)
			}
		})
	}
}

// ownerEmail returns the email of m as the instance admin reads it.
func ownerEmail(t *testing.T, admin *management.ClientWithResponses, m manager) string {
	t.Helper()
	return memberOf(t, admin, m.ID).Email
}

func TestAnInvitationLosesItsGrantWhenItsInviterIsDeactivated(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)
	mi, email := inviteTo(t, owner.ClientWithResponses, p.Key, "developer")

	if res := do(admin.DeactivateMemberWithResponse(t.Context(), owner.ID.String())); res.JSON200 == nil {
		t.Fatalf("deactivating the owner = %d: %s", res.StatusCode(), res.Body)
	}
	wantGrantRevoked(t, admin, p.Key, email, "developer", "inviter_deactivated")
	wantShownWithoutProject(t, srv, *mi.Invitation.Url)

	// A reissue carries nothing forward either.
	link := reissue(t, admin, mi.Member)
	wantShownWithoutProject(t, srv, link)
	acceptedBy(t, srv, link)
	wantActiveWithoutGrant(t, admin, p.Key, email)

	// Reactivating the inviter does not give the grant back.
	if res := do(admin.ReactivateMemberWithResponse(t.Context(), owner.ID.String())); res.JSON200 == nil {
		t.Fatalf("reactivating the owner = %d: %s", res.StatusCode(), res.Body)
	}
	if g := grantsOf(t, admin, p.Key, email); len(g) != 0 {
		t.Errorf("reactivating the inviter granted %+v", g)
	}
}

func TestAnInstanceAdminsInvitationOutlivesTheirGrants(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	pool := testkit.Pool(t)
	inviter, token := testkit.NewInstanceAdmin(t, pool)
	c := testkit.Client(t, srv, token)
	ownerGrant := grant(t, admin, p.Key, inviter.ID, "owner")

	mi, email := inviteTo(t, c, p.Key, "developer")
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, ownerGrant.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the grant = %d: %s", res.StatusCode(), res.Body)
	}
	wantNoGrantRevoked(t, admin, p.Key)

	acceptedBy(t, srv, *mi.Invitation.Url)
	if g := grantsOf(t, admin, p.Key, email); len(g) != 1 || g[0].Role != "developer" || g[0].CreatedBy == nil || *g[0].CreatedBy != inviter.ID.Bytes() {
		t.Errorf("grants = %+v, want the developer grant made by the instance admin", g)
	}

	// An instance admin who is deactivated does lose it.
	again, againEmail := inviteTo(t, c, p.Key, "viewer")
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), inviter.ID.String())); res.JSON200 == nil {
		t.Fatalf("deactivating the instance admin = %d: %s", res.StatusCode(), res.Body)
	}
	wantGrantRevoked(t, admin, p.Key, againEmail, "viewer", "inviter_deactivated")
	wantShownWithoutProject(t, srv, *again.Invitation.Url)
}

func TestDemotingAnInstanceAdminDropsTheGrantsOfInvitationsToProjectsTheyDoNotManage(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, owned := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	pool := testkit.Pool(t)
	inviter, token := testkit.NewInstanceAdmin(t, pool)
	grant(t, admin, owned.Key, inviter.ID, "owner")
	c := testkit.Client(t, srv, token)

	lost, lostEmail := inviteTo(t, c, p.Key, "viewer")
	kept, keptEmail := inviteTo(t, c, owned.Key, "viewer")
	res := do(admin.UpdateMemberWithResponse(t.Context(), inviter.ID.String(), management.MemberPatch{InstanceAdmin: ptr(false)}))
	if res.JSON200 == nil {
		t.Fatalf("demoting = %d: %s", res.StatusCode(), res.Body)
	}
	wantGrantRevoked(t, admin, p.Key, lostEmail, "viewer", "inviter_lost_permission")
	wantNoGrantRevoked(t, admin, owned.Key)

	acceptedBy(t, srv, *lost.Invitation.Url)
	wantActiveWithoutGrant(t, admin, p.Key, lostEmail)
	acceptedBy(t, srv, *kept.Invitation.Url)
	if g := grantsOf(t, admin, owned.Key, keptEmail); len(g) != 1 {
		t.Errorf("grants = %+v, want the viewer grant of the project they still own", g)
	}
}

// invitedIn returns the members invited to project, as c lists them.
func invitedIn(t *testing.T, c *management.ClientWithResponses, project string) []management.ProjectInvitation {
	t.Helper()
	res := do(c.ListProjectInvitationsWithResponse(t.Context(), project))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s/invitations = %d: %s", project, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func TestAProjectListsTheMembersInvitedToItWithTheRoleTheirInvitationGrants(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	elsewhere := createProject(t, admin, projectKey())
	owner := memberIn(t, srv, p, access.RoleOwner)
	departing := memberIn(t, srv, p, access.RoleOwner)

	viewer, viewerEmail := inviteTo(t, owner.ClientWithResponses, p.Key, "viewer")
	_, maintainerEmail := inviteTo(t, owner.ClientWithResponses, p.Key, "developer")
	// Invited again with another role, the member is listed once, with it.
	maintainer := mustInviteTo(t, owner.ClientWithResponses, p.Key, maintainerEmail, "maintainer")

	// Neither an accepted invitation, nor one to another project or to the
	// instance alone, nor one whose grant was dropped, is listed.
	accepted, _ := inviteTo(t, admin, p.Key, "viewer")
	acceptedBy(t, srv, *accepted.Invitation.Url)
	inviteTo(t, admin, elsewhere.Key, "viewer")
	invite(t, admin, "Instance Only")
	_, droppedEmail := inviteTo(t, departing.ClientWithResponses, p.Key, "developer")
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), departing.ID.String())); res.JSON200 == nil {
		t.Fatalf("deactivating the inviter = %d: %s", res.StatusCode(), res.Body)
	}
	wantGrantRevoked(t, admin, p.Key, droppedEmail, "developer", "inviter_deactivated")

	type listed struct {
		member  openapi_types.UUID
		email   string
		role    management.RoleName
		expires time.Time
	}
	want := []listed{
		{viewer.Member.Id, viewerEmail, management.RoleNameViewer, viewer.Invitation.ExpiresAt},
		{maintainer.Member.Id, maintainerEmail, management.RoleNameMaintainer, maintainer.Invitation.ExpiresAt},
	}
	if want[1].email < want[0].email {
		want[0], want[1] = want[1], want[0]
	}
	check := func(when string) {
		t.Helper()
		got := invitedIn(t, owner.ClientWithResponses, p.Key)
		if len(got) != len(want) {
			t.Fatalf("%s, the invited members = %+v, want %d", when, got, len(want))
		}
		for i, w := range want {
			g := got[i]
			if g.Member != w.member || g.Email != w.email || g.DisplayName != "Invitee" || g.Role != w.role ||
				g.InvitedBy == nil || *g.InvitedBy != owner.ID.Bytes() || !g.ExpiresAt.Equal(w.expires) {
				t.Errorf("%s, invited member %d = %+v, want %s as %s, invited by the owner, until %v", when, i, g, w.email, w.role, w.expires)
			}
		}
	}
	check("while the links work")

	// An expired link leaves the member invited, to be invited again.
	c.Advance(7*24*time.Hour + time.Minute)
	check("once the links expired")
}
