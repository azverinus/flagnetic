package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// do returns the response of a request through the generated client; a
// request that never got an answer is a broken test server, so it panics.
func do[R any](res R, err error) R {
	if err != nil {
		panic("management_test: request failed: " + err.Error())
	}
	return res
}

// status fails unless a response has the status wanted.
func status(t *testing.T, name string, got int, body []byte, want int) bool {
	t.Helper()
	if got != want {
		t.Errorf("%s: status = %d, want %d: %s", name, got, want, body)
		return false
	}
	return true
}

func ptr[T any](v T) *T { return &v }

func environmentKeys(t *testing.T, c *management.ClientWithResponses, project string) []string {
	t.Helper()
	res := do(c.ListEnvironmentsWithResponse(t.Context(), project))
	if res.JSON200 == nil {
		t.Fatalf("GET environments = %d %s", res.StatusCode(), res.Body)
	}
	var keys []string
	for _, e := range res.JSON200.Items {
		keys = append(keys, e.Key)
	}
	return keys
}

func TestEnvironmentLifecycle(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key

	created := do(admin.CreateEnvironmentWithResponse(t.Context(), p, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	if !status(t, "create", created.StatusCode(), created.Body, http.StatusCreated) {
		return
	}
	qa := *created.JSON201
	if qa.Key != "qa" || qa.Name != "QA" || qa.SortOrder != 4 {
		t.Errorf("created = %+v, want qa after the three defaults", qa)
	}
	got := do(admin.GetEnvironmentWithResponse(t.Context(), p, "qa"))
	if got.JSON200 == nil || *got.JSON200 != qa {
		t.Errorf("GET = %d %s, want %+v", got.StatusCode(), got.Body, qa)
	}
	renamed := do(admin.UpdateEnvironmentWithResponse(t.Context(), p, "qa", management.EnvironmentPatch{Name: ptr("Quality")}))
	if renamed.JSON200 == nil || renamed.JSON200.Name != "Quality" || renamed.JSON200.Key != "qa" || renamed.JSON200.SortOrder != 4 {
		t.Errorf("PATCH = %d %s", renamed.StatusCode(), renamed.Body)
	}
	if keys := environmentKeys(t, admin, p); !slices.Equal(keys, []string{"development", "staging", "production", "qa"}) {
		t.Errorf("environments = %v", keys)
	}
	deleted := do(admin.DeleteEnvironmentWithResponse(t.Context(), p, "qa"))
	status(t, "delete", deleted.StatusCode(), deleted.Body, http.StatusNoContent)
	gone := do(admin.GetEnvironmentWithResponse(t.Context(), p, "qa"))
	assertProblem(t, "GET deleted", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteEnvironmentWithResponse(t.Context(), p, "qa"))
	assertProblem(t, "delete again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	patchGone := do(admin.UpdateEnvironmentWithResponse(t.Context(), p, "qa", management.EnvironmentPatch{Name: ptr("x")}))
	assertProblem(t, "PATCH deleted", patchGone.StatusCode(), patchGone.Body, http.StatusNotFound, "not_found")
}

func TestEnvironmentKeysMustMatchThePatternBeUniqueAndStayImmutable(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	for _, key := range []string{"", "QA", "9qa", "q a"} {
		res := do(admin.CreateEnvironmentWithResponse(t.Context(), p, management.EnvironmentCreate{Key: key, Name: "QA"}))
		assertProblem(t, "key "+key, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_environment")
	}
	dup := do(admin.CreateEnvironmentWithResponse(t.Context(), p, management.EnvironmentCreate{Key: "staging", Name: "Again"}))
	assertProblem(t, "a key in use", dup.StatusCode(), dup.Body, http.StatusConflict, "already_exists")
	rekey := do(admin.UpdateEnvironmentWithResponse(t.Context(), p, "staging", management.EnvironmentPatch{Key: ptr("stage")}))
	assertProblem(t, "a new key", rekey.StatusCode(), rekey.Body, http.StatusUnprocessableEntity, "key_immutable")
	blank := do(admin.UpdateEnvironmentWithResponse(t.Context(), p, "staging", management.EnvironmentPatch{Name: ptr("")}))
	assertProblem(t, "a blank name", blank.StatusCode(), blank.Body, http.StatusUnprocessableEntity, "invalid_environment")
	if keys := environmentKeys(t, admin, p); !slices.Equal(keys, []string{"development", "staging", "production"}) {
		t.Errorf("environments = %v, want the defaults unchanged", keys)
	}
}

func TestTheLastEnvironmentCannotBeDeleted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	for _, key := range []string{"development", "staging"} {
		res := do(admin.DeleteEnvironmentWithResponse(t.Context(), p, key))
		status(t, "delete "+key, res.StatusCode(), res.Body, http.StatusNoContent)
	}
	last := do(admin.DeleteEnvironmentWithResponse(t.Context(), p, "production"))
	assertProblem(t, "the last", last.StatusCode(), last.Body, http.StatusConflict, "last_environment")
	if keys := environmentKeys(t, admin, p); !slices.Equal(keys, []string{"production"}) {
		t.Errorf("environments = %v, want production kept", keys)
	}
}

func TestTwoDeletionsAtOnceNeverRemoveTheLastEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	do(admin.DeleteEnvironmentWithResponse(t.Context(), p, "development"))

	statuses := make(chan int, 2)
	for _, key := range []string{"staging", "production"} {
		go func() { statuses <- do(admin.DeleteEnvironmentWithResponse(t.Context(), p, key)).StatusCode() }()
	}
	got := []int{<-statuses, <-statuses}
	slices.Sort(got)
	if want := []int{http.StatusNoContent, http.StatusConflict}; !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want one deletion and one last_environment", got)
	}
	if keys := environmentKeys(t, admin, p); len(keys) != 1 {
		t.Errorf("environments = %v, want one left", keys)
	}
}

func TestUnitTypeLifecycle(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key

	created := do(admin.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{
		Key: "organization", Name: "Organization", Description: ptr("A customer account."),
	}))
	if !status(t, "create", created.StatusCode(), created.Body, http.StatusCreated) {
		return
	}
	u := *created.JSON201
	if u.Key != "organization" || u.Name != "Organization" || u.Description != "A customer account." {
		t.Errorf("created = %+v", u)
	}
	got := do(admin.GetUnitTypeWithResponse(t.Context(), p, "organization"))
	if got.JSON200 == nil || *got.JSON200 != u {
		t.Errorf("GET = %d %s", got.StatusCode(), got.Body)
	}
	list := do(admin.ListUnitTypesWithResponse(t.Context(), p))
	var keys []string
	for _, x := range list.JSON200.Items {
		keys = append(keys, x.Key)
	}
	if !slices.Equal(keys, []string{"anonymous", "organization", "user"}) {
		t.Errorf("unit types = %v", keys)
	}
	patched := do(admin.UpdateUnitTypeWithResponse(t.Context(), p, "organization", management.UnitTypePatch{
		Name: ptr("Company"), Description: ptr(""),
	}))
	want := management.UnitType{Id: u.Id, Key: "organization", Name: "Company", Description: ""}
	if patched.JSON200 == nil || *patched.JSON200 != want {
		t.Errorf("PATCH = %d %s, want %+v", patched.StatusCode(), patched.Body, want)
	}
	deleted := do(admin.DeleteUnitTypeWithResponse(t.Context(), p, "organization"))
	status(t, "delete", deleted.StatusCode(), deleted.Body, http.StatusNoContent)
	gone := do(admin.GetUnitTypeWithResponse(t.Context(), p, "organization"))
	assertProblem(t, "GET deleted", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	patchGone := do(admin.UpdateUnitTypeWithResponse(t.Context(), p, "organization", management.UnitTypePatch{Name: ptr("x")}))
	assertProblem(t, "PATCH deleted", patchGone.StatusCode(), patchGone.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteUnitTypeWithResponse(t.Context(), p, "organization"))
	assertProblem(t, "delete again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
}

func TestAttributeLifecycle(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key

	for _, typ := range []management.AttributeType{"string", "number", "boolean", "semver", "date", "list"} {
		key := "a_" + string(typ)
		res := do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: key, Type: typ, Name: "A " + string(typ)}))
		if !status(t, "create "+key, res.StatusCode(), res.Body, http.StatusCreated) {
			continue
		}
		a := *res.JSON201
		if a.Key != key || a.Type != typ || a.Description != "" || a.Standard {
			t.Errorf("created = %+v", a)
		}
	}
	patched := do(admin.UpdateAttributeWithResponse(t.Context(), p, "a_string", management.AttributePatch{
		Name: ptr("Plan tier"), Description: ptr("The plan paid for."),
	}))
	if patched.JSON200 == nil || patched.JSON200.Name != "Plan tier" || patched.JSON200.Description != "The plan paid for." ||
		patched.JSON200.Type != "string" {
		t.Errorf("PATCH = %d %s", patched.StatusCode(), patched.Body)
	}
	got := do(admin.GetAttributeWithResponse(t.Context(), p, "a_string"))
	if got.JSON200 == nil || *got.JSON200 != *patched.JSON200 {
		t.Errorf("GET = %d %s", got.StatusCode(), got.Body)
	}
	list := do(admin.ListAttributesWithResponse(t.Context(), p))
	if list.JSON200 == nil || len(list.JSON200.Items) != 16 {
		t.Errorf("attributes = %d %s, want the ten standard and six declared", list.StatusCode(), list.Body)
	}
	deleted := do(admin.DeleteAttributeWithResponse(t.Context(), p, "a_string"))
	status(t, "delete", deleted.StatusCode(), deleted.Body, http.StatusNoContent)
	gone := do(admin.GetAttributeWithResponse(t.Context(), p, "a_string"))
	assertProblem(t, "GET deleted", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteAttributeWithResponse(t.Context(), p, "a_string"))
	assertProblem(t, "delete again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
}

func TestUnitTypeAndAttributeKeysAreUniqueTogether(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	conflict := func(name string, status int, body []byte) {
		t.Helper()
		assertProblem(t, name, status, body, http.StatusConflict, "already_exists")
	}
	unitType := func(key string) *management.CreateUnitTypeResponse {
		return do(admin.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{Key: key, Name: key}))
	}
	attribute := func(key string) *management.CreateAttributeResponse {
		return do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: key, Name: key, Type: "string"}))
	}
	r := unitType("app_version")
	conflict("a unit type named like a standard attribute", r.StatusCode(), r.Body)
	a := attribute("user")
	conflict("an attribute named like a default unit type", a.StatusCode(), a.Body)
	if device := unitType("device"); !status(t, "unit type device", device.StatusCode(), device.Body, http.StatusCreated) {
		return
	}
	a = attribute("device")
	conflict("an attribute named like a unit type", a.StatusCode(), a.Body)
	if tier := attribute("tier"); !status(t, "attribute tier", tier.StatusCode(), tier.Body, http.StatusCreated) {
		return
	}
	r = unitType("tier")
	conflict("a unit type named like an attribute", r.StatusCode(), r.Body)
	r = unitType("device")
	conflict("a unit type twice", r.StatusCode(), r.Body)

	other := createProject(t, admin, projectKey()).Key
	res := do(admin.CreateUnitTypeWithResponse(t.Context(), other, management.UnitTypeCreate{Key: "device", Name: "Device"}))
	status(t, "the same key in another project", res.StatusCode(), res.Body, http.StatusCreated)
}

func TestDeclarationKeysAndTypesAreValidatedAndImmutable(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	for _, key := range []string{"Device", "1st", "", "a b", "discovered"} {
		a := do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: key, Name: "X", Type: "string"}))
		assertProblem(t, "attribute key "+key, a.StatusCode(), a.Body, http.StatusUnprocessableEntity, "invalid_attribute")
	}
	for _, key := range []string{"Device", "1st", "", "a b"} {
		u := do(admin.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{Key: key, Name: "X"}))
		assertProblem(t, "unit type key "+key, u.StatusCode(), u.Body, http.StatusUnprocessableEntity, "invalid_unit_type")
	}
	json := do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: "blob", Name: "Blob", Type: "json"}))
	pr := assertProblem(t, "type json", json.StatusCode(), json.Body, http.StatusUnprocessableEntity, "invalid_attribute")
	if pr.Errors == nil || (*pr.Errors)[0].Pointer != "/type" {
		t.Errorf("violations = %+v, want /type", pr.Errors)
	}
	noName := do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: "tier", Name: "", Type: "string"}))
	assertProblem(t, "no name", noName.StatusCode(), noName.Body, http.StatusUnprocessableEntity, "invalid_attribute")

	created := do(admin.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: "tier", Name: "Tier", Type: "string"}))
	if !status(t, "create tier", created.StatusCode(), created.Body, http.StatusCreated) {
		return
	}
	for name, patch := range map[string]struct {
		body management.AttributePatch
		code string
	}{
		"a new key":          {management.AttributePatch{Key: ptr("plan")}, "key_immutable"},
		"the same type":      {management.AttributePatch{Type: ptr("string")}, "type_immutable"},
		"another type":       {management.AttributePatch{Type: ptr("number")}, "type_immutable"},
		"an empty name":      {management.AttributePatch{Name: ptr("")}, "invalid_attribute"},
		"a long description": {management.AttributePatch{Description: ptr(strings.Repeat("x", 2001))}, "invalid_attribute"},
	} {
		res := do(admin.UpdateAttributeWithResponse(t.Context(), p, "tier", patch.body))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, patch.code)
	}
	rekey := do(admin.UpdateUnitTypeWithResponse(t.Context(), p, "user", management.UnitTypePatch{Key: ptr("member")}))
	assertProblem(t, "a unit type's new key", rekey.StatusCode(), rekey.Body, http.StatusUnprocessableEntity, "key_immutable")
	got := do(admin.GetAttributeWithResponse(t.Context(), p, "tier"))
	if got.JSON200 == nil || *got.JSON200 != *created.JSON201 {
		t.Errorf("tier = %s, want it unchanged", got.Body)
	}
}

func TestStandardAttributesCannotBeDeletedOrRetyped(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	del := do(admin.DeleteAttributeWithResponse(t.Context(), p, "app_version"))
	assertProblem(t, "delete", del.StatusCode(), del.Body, http.StatusConflict, "standard_attribute")
	retype := do(admin.UpdateAttributeWithResponse(t.Context(), p, "app_version", management.AttributePatch{Type: ptr("string")}))
	assertProblem(t, "retype", retype.StatusCode(), retype.Body, http.StatusUnprocessableEntity, "type_immutable")
	renamed := do(admin.UpdateAttributeWithResponse(t.Context(), p, "app_version", management.AttributePatch{
		Name: ptr("Mobile app version"), Description: ptr("The application version."),
	}))
	if a := renamed.JSON200; a == nil || a.Key != "app_version" || a.Type != "semver" || a.Name != "Mobile app version" ||
		a.Description != "The application version." || !a.Standard {
		t.Errorf("renamed = %d %s", renamed.StatusCode(), renamed.Body)
	}
}

func TestDeclarationEndpointsOfAnUnknownProjectAreNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	const p = "no-such-project"
	ctx := t.Context()
	for name, res := range map[string]interface {
		StatusCode() int
	}{
		"list environments":  do(admin.ListEnvironmentsWithResponse(ctx, p)),
		"create environment": do(admin.CreateEnvironmentWithResponse(ctx, p, management.EnvironmentCreate{Key: "qa", Name: "QA"})),
		"get environment":    do(admin.GetEnvironmentWithResponse(ctx, p, "production")),
		"update environment": do(admin.UpdateEnvironmentWithResponse(ctx, p, "production", management.EnvironmentPatch{Name: ptr("x")})),
		"delete environment": do(admin.DeleteEnvironmentWithResponse(ctx, p, "production")),
		"list unit types":    do(admin.ListUnitTypesWithResponse(ctx, p)),
		"create unit type":   do(admin.CreateUnitTypeWithResponse(ctx, p, management.UnitTypeCreate{Key: "device", Name: "Device"})),
		"get unit type":      do(admin.GetUnitTypeWithResponse(ctx, p, "user")),
		"update unit type":   do(admin.UpdateUnitTypeWithResponse(ctx, p, "user", management.UnitTypePatch{Name: ptr("x")})),
		"delete unit type":   do(admin.DeleteUnitTypeWithResponse(ctx, p, "user")),
		"list attributes":    do(admin.ListAttributesWithResponse(ctx, p)),
		"create attribute":   do(admin.CreateAttributeWithResponse(ctx, p, management.AttributeCreate{Key: "tier", Name: "Tier", Type: "string"})),
		"get attribute":      do(admin.GetAttributeWithResponse(ctx, p, "os")),
		"update attribute":   do(admin.UpdateAttributeWithResponse(ctx, p, "os", management.AttributePatch{Name: ptr("x")})),
		"delete attribute":   do(admin.DeleteAttributeWithResponse(ctx, p, "os")),
	} {
		if res.StatusCode() != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, res.StatusCode())
		}
	}

	a, b := createProject(t, admin, projectKey()).Key, createProject(t, admin, projectKey()).Key
	if res := do(admin.CreateAttributeWithResponse(ctx, a, management.AttributeCreate{Key: "tier", Name: "Tier", Type: "string"})); res.StatusCode() != http.StatusCreated {
		t.Fatalf("create = %d %s", res.StatusCode(), res.Body)
	}
	other := do(admin.GetAttributeWithResponse(ctx, b, "tier"))
	assertProblem(t, "an attribute read through another project", other.StatusCode(), other.Body, http.StatusNotFound, "not_found")
}

func TestManagingEnvironmentsAndDeclarationsNeedsTheirPermissions(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	project := createProject(t, admin, projectKey())
	pool := testkit.Pool(t)
	viewer := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(project), access.RoleViewer))
	maintainer := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(project), access.RoleMaintainer))
	adminOnly := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(project), access.RoleMaintainer,
		access.ScopeRead, access.ScopeProjectAdmin))
	p, ctx := project.Key, t.Context()

	read := do(viewer.ListEnvironmentsWithResponse(ctx, p))
	status(t, "a viewer reads environments", read.StatusCode(), read.Body, http.StatusOK)
	env := do(viewer.CreateEnvironmentWithResponse(ctx, p, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	pr := assertProblem(t, "a viewer creates an environment", env.StatusCode(), env.Body, http.StatusForbidden, "forbidden")
	if pr.Permission == nil || *pr.Permission != "environment.manage" {
		t.Errorf("permission = %v, want environment.manage", pr.Permission)
	}
	unit := do(viewer.CreateUnitTypeWithResponse(ctx, p, management.UnitTypeCreate{Key: "device", Name: "Device"}))
	pr = assertProblem(t, "a viewer declares", unit.StatusCode(), unit.Body, http.StatusForbidden, "forbidden")
	if pr.Permission == nil || *pr.Permission != "declaration.manage" {
		t.Errorf("permission = %v, want declaration.manage", pr.Permission)
	}

	env = do(maintainer.CreateEnvironmentWithResponse(ctx, p, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	status(t, "a maintainer creates an environment", env.StatusCode(), env.Body, http.StatusCreated)
	unit = do(maintainer.CreateUnitTypeWithResponse(ctx, p, management.UnitTypeCreate{Key: "device", Name: "Device"}))
	status(t, "a maintainer declares", unit.StatusCode(), unit.Body, http.StatusCreated)
	unit = do(adminOnly.CreateUnitTypeWithResponse(ctx, p, management.UnitTypeCreate{Key: "team", Name: "Team"}))
	assertProblem(t, "declaring without flags:write", unit.StatusCode(), unit.Body, http.StatusForbidden, "insufficient_scope")
}

func TestEveryWorkspaceWriteRecordsAnAuditEntry(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))
	project := createProject(t, admin, "mobile")
	p, ctx := project.Key, t.Context()

	qa := do(admin.CreateEnvironmentWithResponse(ctx, p, management.EnvironmentCreate{Key: "qa", Name: "QA"})).JSON201
	do(admin.UpdateEnvironmentWithResponse(ctx, p, "qa", management.EnvironmentPatch{Name: ptr("Quality")}))
	do(admin.UpdateEnvironmentWithResponse(ctx, p, "qa", management.EnvironmentPatch{Name: ptr("Quality")}))
	do(admin.UpdateEnvironmentWithResponse(ctx, p, "qa", management.EnvironmentPatch{Key: ptr("test")}))
	do(admin.CreateEnvironmentWithResponse(ctx, p, management.EnvironmentCreate{Key: "qa", Name: "Again"}))
	do(admin.DeleteEnvironmentWithResponse(ctx, p, "qa"))
	device := do(admin.CreateUnitTypeWithResponse(ctx, p, management.UnitTypeCreate{Key: "device", Name: "Device"})).JSON201
	do(admin.UpdateUnitTypeWithResponse(ctx, p, "device", management.UnitTypePatch{Name: ptr("Phone")}))
	do(admin.DeleteUnitTypeWithResponse(ctx, p, "device"))
	tier := do(admin.CreateAttributeWithResponse(ctx, p, management.AttributeCreate{Key: "tier", Name: "Tier", Type: "string"})).JSON201
	do(admin.UpdateAttributeWithResponse(ctx, p, "tier", management.AttributePatch{Name: ptr("Plan tier")}))
	do(admin.UpdateAttributeWithResponse(ctx, p, "tier", management.AttributePatch{Type: ptr("number")}))
	do(admin.DeleteAttributeWithResponse(ctx, p, "app_version"))
	do(admin.DeleteAttributeWithResponse(ctx, p, "tier"))
	if qa == nil || device == nil || tier == nil {
		t.Fatal("a create failed")
	}

	entries := do(admin.ListProjectAuditEntriesWithResponse(ctx, p, &management.ListProjectAuditEntriesParams{})).JSON200
	if entries == nil {
		t.Fatal("the audit log was not listed")
	}
	type seen struct{ action, entity, environment, before, after string }
	var got []seen
	for _, e := range entries.Items {
		if e.Entity.Kind == "project" {
			continue
		}
		env := ""
		if e.EnvironmentId != nil {
			env = e.EnvironmentId.String()
		}
		got = append(got, seen{e.Action, e.Entity.Id + " " + e.Entity.Key, env, raw(e.Before), raw(e.After)})
	}
	qaID, deviceID, tierID := qa.Id.String(), device.Id.String(), tier.Id.String()
	want := []seen{
		{"attribute.deleted", tierID + " tier", "", `{"description":"","key":"tier","name":"Plan tier","standard":false,"type":"string"}`, ""},
		{"attribute.updated", tierID + " tier", "", `{"description":"","key":"tier","name":"Tier","standard":false,"type":"string"}`,
			`{"description":"","key":"tier","name":"Plan tier","standard":false,"type":"string"}`},
		{"attribute.created", tierID + " tier", "", "", `{"description":"","key":"tier","name":"Tier","standard":false,"type":"string"}`},
		{"unit_type.deleted", deviceID + " device", "", `{"description":"","key":"device","name":"Phone"}`, ""},
		{"unit_type.updated", deviceID + " device", "", `{"description":"","key":"device","name":"Device"}`, `{"description":"","key":"device","name":"Phone"}`},
		{"unit_type.created", deviceID + " device", "", "", `{"description":"","key":"device","name":"Device"}`},
		{"environment.deleted", qaID + " qa", qaID, `{"key":"qa","name":"Quality","sort_order":4}`, ""},
		{"environment.updated", qaID + " qa", qaID, `{"key":"qa","name":"QA","sort_order":4}`, `{"key":"qa","name":"Quality","sort_order":4}`},
		{"environment.created", qaID + " qa", qaID, "", `{"key":"qa","name":"QA","sort_order":4}`},
	}
	if !slices.Equal(got, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v\n(refused and empty changes record nothing)", got, want)
	}
}
