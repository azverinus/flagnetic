package management_test

import (
	"crypto/rand"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// projectKey returns a key no other test uses.
func projectKey() string { return "p" + strings.ToLower(rand.Text()[:12]) }

// createProject creates a project named after its key as c, an instance
// admin, with its production unprotected: most tests write production
// directly, and what protection refuses has tests of its own.
func createProject(t *testing.T, c *management.ClientWithResponses, key string) management.Project {
	t.Helper()
	p := createProjectAsCreated(t, c, key)
	setProtected(t, c, key, "production", false)
	return p
}

// createProjectAsCreated creates a project named after its key as c, an
// instance admin, with its production protected as every new project's is.
func createProjectAsCreated(t *testing.T, c *management.ClientWithResponses, key string) management.Project {
	t.Helper()
	res, err := c.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Project " + key})
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON201 == nil {
		t.Fatalf("POST /projects = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// problem decodes the problem document of a refused request.
func problem(t *testing.T, status int, body []byte) management.Problem {
	t.Helper()
	var p management.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("status %d, body %s: %v", status, body, err)
	}
	return p
}

// assertProblem fails unless the answer is status with code.
func assertProblem(t *testing.T, name string, status int, body []byte, wantStatus int, wantCode string) management.Problem {
	t.Helper()
	if status != wantStatus {
		t.Errorf("%s: status = %d, want %d: %s", name, status, wantStatus, body)
		return management.Problem{}
	}
	p := problem(t, status, body)
	if p.Code != wantCode {
		t.Errorf("%s: code = %q, want %q", name, p.Code, wantCode)
	}
	return p
}

func projectIDOf(p management.Project) workspace.ProjectID {
	return id.FromBytes[workspace.Project](p.Id)
}

func TestCreatingAProjectCreatesItsDefaults(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := projectKey()
	res, err := admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Mobile"})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusCreated || res.JSON201 == nil {
		t.Fatalf("POST /projects = %d: %s", res.StatusCode(), res.Body)
	}
	p := *res.JSON201
	if p.Key != key || p.Name != "Mobile" || p.Description != "" || p.Id == [16]byte{} {
		t.Errorf("project = %+v, want %s named Mobile", p, key)
	}

	envs := do(admin.ListEnvironmentsWithResponse(t.Context(), key)).JSON200
	if envs == nil {
		t.Fatal("no environments listed")
	}
	want := []management.Environment{{Key: "development", Name: "Development", SortOrder: 1},
		{Key: "staging", Name: "Staging", SortOrder: 2}, {Key: "production", Name: "Production", SortOrder: 3}}
	if !slices.EqualFunc(envs.Items, want, func(a, b management.Environment) bool {
		return a.Key == b.Key && a.Name == b.Name && a.SortOrder == b.SortOrder && a.Id != [16]byte{}
	}) {
		t.Errorf("environments = %+v, want %+v", envs.Items, want)
	}
	units := do(admin.ListUnitTypesWithResponse(t.Context(), key)).JSON200
	if units == nil {
		t.Fatal("no unit types listed")
	}
	var unitKeys []string
	for _, u := range units.Items {
		unitKeys = append(unitKeys, u.Key)
	}
	if want := []string{"anonymous", "user"}; !slices.Equal(unitKeys, want) {
		t.Errorf("unit types = %v, want %v", unitKeys, want)
	}
	attrs := do(admin.ListAttributesWithResponse(t.Context(), key)).JSON200
	if attrs == nil {
		t.Fatal("no attributes listed")
	}
	got := map[string]management.AttributeType{}
	for _, a := range attrs.Items {
		if !a.Standard {
			t.Errorf("attribute %s is not standard", a.Key)
		}
		got[a.Key] = a.Type
	}
	wantAttrs := map[string]management.AttributeType{
		"platform": "string", "os": "string", "app_id": "string", "sdk": "string", "locale": "string", "country": "string",
		"os_version": "semver", "app_version": "semver", "sdk_version": "semver", "build_number": "number",
	}
	if !maps.Equal(got, wantAttrs) {
		t.Errorf("attributes = %v, want %v", got, wantAttrs)
	}

	list, err := admin.ListProjectsWithResponse(t.Context())
	if err != nil || list.JSON200 == nil {
		t.Fatalf("GET /projects: %v %s", err, list.Body)
	}
	if !slices.ContainsFunc(list.JSON200.Items, func(q management.Project) bool { return q == p }) {
		t.Errorf("projects = %+v, want %+v among them", list.JSON200.Items, p)
	}
}

func TestProjectKeysMustMatchThePatternAndBeUnique(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	for _, key := range []string{"", "Mobile", "1mobile", "-mobile", "mobile app", "m" + strings.Repeat("x", 64)} {
		res, err := admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Mobile"})
		if err != nil {
			t.Fatal(err)
		}
		p := assertProblem(t, "key "+key, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_project")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/key" || (*p.Errors)[0].Code != "invalid_key" {
			t.Errorf("key %q: violations = %+v, want /key invalid_key", key, p.Errors)
		}
	}
	res, err := admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: " "})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "blank name", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_project")

	key := projectKey()
	createProject(t, admin, key)
	res, err = admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Again"})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a key in use", res.StatusCode(), res.Body, http.StatusConflict, "already_exists")
}

func TestProjectKeyIsImmutableAndNameIsEditable(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := projectKey()
	created := createProject(t, admin, key)

	renamed := "renamed"
	res, err := admin.UpdateProjectWithResponse(t.Context(), key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Key: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a new key", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "key_immutable")

	name, description := "Mobile apps", "iOS and Android"
	res, err = admin.UpdateProjectWithResponse(t.Context(), key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name, Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	want := management.Project{Id: created.Id, Key: key, Name: name, Description: description, Version: 2}
	if res.JSON200 == nil || *res.JSON200 != want {
		t.Fatalf("PATCH = %d %s, want %+v", res.StatusCode(), res.Body, want)
	}
	got, err := admin.GetProjectWithResponse(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON200 == nil || *got.JSON200 != want {
		t.Errorf("GET = %d %s, want %+v", got.StatusCode(), got.Body, want)
	}
}

func TestUnknownProjectIsNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	name := "x"
	get, err := admin.GetProjectWithResponse(t.Context(), "no-such-project")
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "GET", get.StatusCode(), get.Body, http.StatusNotFound, "not_found")
	patch, err := admin.UpdateProjectWithResponse(t.Context(), "no-such-project", &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "PATCH", patch.StatusCode(), patch.Body, http.StatusNotFound, "not_found")
	me, err := admin.GetProjectMeWithResponse(t.Context(), "No Such Project")
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "GET me", me.StatusCode(), me.Body, http.StatusNotFound, "not_found")
	audit := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), "no-such-project", &management.ListProjectAuditEntriesParams{}))
	assertProblem(t, "GET audit", audit.StatusCode(), audit.Body, http.StatusNotFound, "not_found")
}

func TestAnUnknownProjectIsNotFoundBeforeThePermissionIsJudged(t *testing.T) {
	srv := testkit.StartServer(t)
	stranger := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t), access.ScopeProjectAdmin))
	name := "x"
	res, err := stranger.UpdateProjectWithResponse(t.Context(), "no-such-project", &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "unknown", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	key := projectKey()
	createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), key)
	res, err = stranger.UpdateProjectWithResponse(t.Context(), key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	p := assertProblem(t, "known", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	if p.Permission == nil || *p.Permission != "project.update" {
		t.Errorf("permission = %v, want project.update named", p.Permission)
	}
}

func TestRefusalsAreCountedByOperationAndCode(t *testing.T) {
	srv := testkit.StartServer(t)
	key := projectKey()
	createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), key)
	stranger := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t)))
	for range 2 {
		res, err := stranger.GetProjectWithResponse(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		assertProblem(t, "GET", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	}
	res, err := stranger.GetProjectWithResponse(t.Context(), "no-such-project")
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "GET unknown", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	scrape := srv.Scrape(t)
	if got := testkit.Metric(t, scrape, `flagnetic_management_refusals_total{code="forbidden",operation="getProject"}`); got != 2 {
		t.Errorf("forbidden refusals of getProject = %v, want 2", got)
	}
	if got := testkit.Metric(t, scrape, `flagnetic_management_refusals_total{code="not_found",operation="getProject"}`); got != 1 {
		t.Errorf("not_found refusals of getProject = %v, want 1", got)
	}
}

func TestListProjectsRoundTripsThroughGeneratedClient(t *testing.T) {
	srv := testkit.StartServer(t)
	res, err := testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListProjectsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Items == nil {
		t.Fatalf("GET /projects = %d: %s, want an item list", res.StatusCode(), res.Body)
	}
}

func TestProjectsAreListedWhereTheCallerHoldsAGrant(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))
	a, b := createProject(t, admin, "alpha"), createProject(t, admin, "beta")

	all, err := admin.ListProjectsWithResponse(t.Context())
	if err != nil || all.JSON200 == nil {
		t.Fatalf("GET /projects: %v %s", err, all.Body)
	}
	if want := []management.Project{a, b}; !slices.Equal(all.JSON200.Items, want) {
		t.Errorf("an instance admin lists %+v, want %+v", all.JSON200.Items, want)
	}
	none, err := testkit.Client(t, srv, testkit.MemberToken(t, pool)).ListProjectsWithResponse(t.Context())
	if err != nil || none.JSON200 == nil {
		t.Fatalf("GET /projects: %v %s", err, none.Body)
	}
	if len(none.JSON200.Items) != 0 {
		t.Errorf("a member without a grant lists %+v, want none", none.JSON200.Items)
	}
	pipeline := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(a), access.RoleOwner))
	res, err := pipeline.ListProjectsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a project token", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
}

func TestAProjectTokenReachesOnlyItsProject(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mine, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	pipeline := testkit.Client(t, srv, testkit.ProjectToken(t, testkit.Pool(t), projectIDOf(mine), access.RoleViewer))

	res, err := pipeline.GetProjectWithResponse(t.Context(), mine.Key)
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil || *res.JSON200 != mine {
		t.Errorf("its own project = %d %s", res.StatusCode(), res.Body)
	}
	// An unknown project is answered like another one, so the token learns
	// nothing of which keys exist.
	for _, key := range []string{other.Key, "no-such-project"} {
		refused, getErr := pipeline.GetProjectWithResponse(t.Context(), key)
		if getErr != nil {
			t.Fatal(getErr)
		}
		assertProblem(t, key, refused.StatusCode(), refused.Body, http.StatusForbidden, "forbidden")
	}
	name := "x"
	patch, err := pipeline.UpdateProjectWithResponse(t.Context(), mine.Key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	p := assertProblem(t, "a viewer renaming", patch.StatusCode(), patch.Body, http.StatusForbidden, "forbidden")
	if p.Permission == nil || *p.Permission != "project.update" {
		t.Errorf("permission = %v, want project.update", p.Permission)
	}
}

func TestProjectMeAnswersGrantsAndPermissionsNarrowedByScopes(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	for name, tc := range map[string]struct {
		token string
		want  []string
	}{
		"an instance admin holds every permission": {testkit.InstanceAdminToken(t),
			[]string{"project.read", "project.update", "environment.manage", "environment.protect", "declaration.manage", "grant.manage", "project_token.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "webhook.manage"}},
		"a maintainer's project token, narrowed to read": {
			testkit.ProjectToken(t, testkit.Pool(t), projectIDOf(p), access.RoleMaintainer, access.ScopeRead),
			[]string{"project.read"}},
		"an owner's project token never changes who has access": {
			testkit.ProjectToken(t, testkit.Pool(t), projectIDOf(p), access.RoleOwner),
			[]string{"project.read", "project.update", "environment.manage", "declaration.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "webhook.manage"}},
	} {
		res, err := testkit.Client(t, srv, tc.token).GetProjectMeWithResponse(t.Context(), p.Key)
		if err != nil {
			t.Fatal(err)
		}
		if res.JSON200 == nil {
			t.Errorf("%s: GET me = %d %s", name, res.StatusCode(), res.Body)
			continue
		}
		if !slices.Equal(res.JSON200.Permissions, tc.want) || res.JSON200.Grants == nil || len(res.JSON200.Grants) != 0 {
			t.Errorf("%s: me = %+v, want permissions %v and no grant", name, res.JSON200, tc.want)
		}
	}
	stranger, err := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t))).GetProjectMeWithResponse(t.Context(), p.Key)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a member without a grant", stranger.StatusCode(), stranger.Body, http.StatusForbidden, "forbidden")
}

func TestEveryRoleIsListedWithItsPermissions(t *testing.T) {
	srv := testkit.StartServer(t)
	res, err := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t))).ListRolesWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("GET /roles = %d %s", res.StatusCode(), res.Body)
	}
	want := []management.Role{
		{Name: "owner", Permissions: []string{"project.read", "project.update", "environment.manage", "environment.protect", "declaration.manage", "grant.manage", "project_token.manage",
			"flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "webhook.manage"}},
		{Name: "maintainer", Permissions: []string{"project.read", "environment.manage", "declaration.manage", "flag.edit", "flag.archive",
			"flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "webhook.manage"}},
		{Name: "developer", Permissions: []string{"project.read", "flag.edit", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "experiment.edit", "change_request.propose"}},
		{Name: "viewer", Permissions: []string{"project.read"}},
	}
	if got := res.JSON200.Items; !slices.EqualFunc(got, want, func(a, b management.Role) bool {
		return a.Name == b.Name && slices.Equal(a.Permissions, b.Permissions)
	}) {
		t.Errorf("roles = %+v, want %+v", got, want)
	}
}

func TestEveryProjectWriteRecordsAnAuditEntry(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))
	created, err := admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: "mobile", Name: "Mobile"})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("POST /projects: %v %s", err, created.Body)
	}
	name, key := "Mobile apps", "renamed"
	for _, patch := range []management.ProjectPatch{{Key: &key}, {Name: &name}, {Name: &name}} {
		if _, patchErr := admin.UpdateProjectWithResponse(t.Context(), "mobile", &management.UpdateProjectParams{IfMatch: ifMatch(1)}, patch); patchErr != nil {
			t.Fatal(patchErr)
		}
	}
	dup, err := admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: "mobile", Name: "Again"})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := admin.UpdateProjectWithResponse(t.Context(), "mobile", &management.UpdateProjectParams{IfMatch: ifMatch(2)}, management.ProjectPatch{})
	if err != nil {
		t.Fatal(err)
	}

	entries := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), "mobile", &management.ListProjectAuditEntriesParams{})).JSON200
	if entries == nil {
		t.Fatal("the audit log was not listed")
	}
	type seen struct{ action, before, after string }
	var got []seen
	for _, e := range entries.Items {
		if e.Entity.Kind != "project" || e.Entity.Id != created.JSON201.Id.String() || e.Entity.Key != "mobile" ||
			e.EnvironmentId != nil || e.Actor.Kind != "token" || e.Actor.Name != "bootstrap" || e.RequestId == nil {
			t.Errorf("entry %s = %+v", e.Action, e)
		}
		got = append(got, seen{e.Action, raw(e.Before), raw(e.After)})
	}
	want := []seen{
		{"project.updated", `{"description":"","key":"mobile","name":"Mobile"}`, `{"description":"","key":"mobile","name":"Mobile apps"}`},
		{"project.created", ``, `{"description":"","key":"mobile","name":"Mobile"}`},
	}
	if !slices.Equal(got, want) {
		t.Errorf("entries = %+v, want %+v (a refused or empty change records nothing)", got, want)
	}
	if dup.StatusCode() != http.StatusConflict || renamed.StatusCode() != http.StatusOK {
		t.Errorf("duplicate = %d, empty patch = %d", dup.StatusCode(), renamed.StatusCode())
	}
}

// raw returns recorded values as JSON with sorted keys, "" for none.
func raw(v any) string {
	if v == nil {
		return ""
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "unencodable: " + err.Error()
	}
	return string(out)
}
