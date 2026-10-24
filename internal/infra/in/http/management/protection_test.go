package management_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// protect sets the protection of project's environment as c, based on
// version.
func protect(t *testing.T, c *management.ClientWithResponses, project, environment string, version int64, protected bool,
) *management.SetEnvironmentProtectionResponse {
	t.Helper()
	return do(c.SetEnvironmentProtectionWithResponse(t.Context(), project, environment,
		&management.SetEnvironmentProtectionParams{IfMatch: ifMatch(version)}, management.EnvironmentProtection{Protected: protected}))
}

func TestANewProjectProtectsItsProductionEnvironmentAlone(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key

	res := do(admin.ListEnvironmentsWithResponse(t.Context(), p))
	if res.JSON200 == nil {
		t.Fatalf("GET environments = %d %s", res.StatusCode(), res.Body)
	}
	var protected []string
	for _, e := range res.JSON200.Items {
		if e.Protected {
			protected = append(protected, e.Key)
		}
	}
	if !slices.Equal(protected, []string{"production"}) {
		t.Errorf("protected = %v, want production alone", protected)
	}
	created := do(admin.CreateEnvironmentWithResponse(t.Context(), p, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	if created.JSON201 == nil || created.JSON201.Protected {
		t.Errorf("a created environment = %d %s, want it unprotected", created.StatusCode(), created.Body)
	}
}

func TestAnOwnerProtectsAndUnprotectsAnEnvironment(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	project := createProject(t, admin, projectKey())
	p := project.Key
	owner := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(project), access.RoleOwner))

	unprotected := protect(t, owner, p, "production", 1, false)
	if !status(t, "unprotect", unprotected.StatusCode(), unprotected.Body, http.StatusOK) {
		return
	}
	if e := unprotected.JSON200; e.Protected || e.Version != 2 || unprotected.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Errorf("unprotected = %+v, ETag %s, want version 2 unprotected", e, unprotected.HTTPResponse.Header.Get("ETag"))
	}
	again := protect(t, owner, p, "production", 2, false)
	if again.JSON200 == nil || again.JSON200.Protected || again.JSON200.Version != 2 {
		t.Errorf("unprotecting again = %d %s, want version 2 unchanged", again.StatusCode(), again.Body)
	}
	staging := protect(t, owner, p, "staging", 1, true)
	if staging.JSON200 == nil || !staging.JSON200.Protected || staging.JSON200.Version != 2 {
		t.Errorf("protecting staging = %d %s", staging.StatusCode(), staging.Body)
	}
	got := do(owner.GetEnvironmentWithResponse(t.Context(), p, "staging"))
	if got.JSON200 == nil || !got.JSON200.Protected {
		t.Errorf("GET staging = %d %s, want it protected", got.StatusCode(), got.Body)
	}
	renamed := do(owner.UpdateEnvironmentWithResponse(t.Context(), p, "staging", &management.UpdateEnvironmentParams{IfMatch: ifMatch(2)},
		management.EnvironmentPatch{Name: ptr("Pre-production")}))
	if renamed.JSON200 == nil || !renamed.JSON200.Protected || renamed.JSON200.Version != 3 {
		t.Errorf("renaming = %d %s, want the protection kept", renamed.StatusCode(), renamed.Body)
	}

	entries := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p, &management.ListProjectAuditEntriesParams{})).JSON200
	if entries == nil {
		t.Fatal("the audit log was not listed")
	}
	type seen struct{ action, entity, before, after string }
	var recorded []seen
	for _, e := range entries.Items {
		if e.Action == "environment.protected" || e.Action == "environment.unprotected" {
			if e.EnvironmentId == nil || e.EnvironmentId.String() != e.Entity.Id {
				t.Errorf("entry %s names environment %v, want %s", e.Action, e.EnvironmentId, e.Entity.Id)
			}
			recorded = append(recorded, seen{e.Action, e.Entity.Key, raw(e.Before), raw(e.After)})
		}
	}
	want := []seen{
		{"environment.protected", "staging", `{"key":"staging","name":"Staging","protected":false,"sort_order":2}`,
			`{"key":"staging","name":"Staging","protected":true,"sort_order":2}`},
		{"environment.unprotected", "production", `{"key":"production","name":"Production","protected":true,"sort_order":3}`,
			`{"key":"production","name":"Production","protected":false,"sort_order":3}`},
	}
	if !slices.Equal(recorded, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v\n(setting the protection an environment has records nothing)", recorded, want)
	}
}

func TestOnlyAMemberHoldingEnvironmentProtectChangesProtection(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	project := createProject(t, admin, projectKey())
	p := project.Key

	for name, token := range map[string]string{
		"a maintainer":             testkit.GrantedToken(t, pool, projectIDOf(project), access.RoleMaintainer),
		"an owner's project token": testkit.ProjectToken(t, pool, projectIDOf(project), access.RoleOwner),
	} {
		res := protect(t, testkit.Client(t, srv, token), p, "production", 1, false)
		if pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
			*pr.Permission != "environment.protect" {
			t.Errorf("%s: refusal = %+v, want it to name environment.protect", name, pr)
		}
	}
	if got := do(admin.GetEnvironmentWithResponse(t.Context(), p, "production")); got.JSON200 == nil || !got.JSON200.Protected {
		t.Errorf("production = %d %s, want it still protected", got.StatusCode(), got.Body)
	}
}

func TestProtectionIsJudgedAgainstTheEnvironmentsVersion(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key

	missing := do(admin.SetEnvironmentProtectionWithResponse(t.Context(), p, "production",
		&management.SetEnvironmentProtectionParams{}, management.EnvironmentProtection{Protected: false}))
	assertProblem(t, "without If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := protect(t, admin, p, "production", 7, false)
	assertProblem(t, "a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	absent := protect(t, admin, p, "absent", 1, true)
	assertProblem(t, "an absent environment", absent.StatusCode(), absent.Body, http.StatusNotFound, "not_found")
	if got := do(admin.GetEnvironmentWithResponse(t.Context(), p, "production")); got.JSON200 == nil || !got.JSON200.Protected ||
		got.JSON200.Version != 1 {
		t.Errorf("production = %d %s, want it untouched", got.StatusCode(), got.Body)
	}
}
