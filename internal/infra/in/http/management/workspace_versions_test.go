package management_test

import (
	"net/http"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// versioned is one kind of workspace entity, written through the API with
// the version a write names in If-Match (nil sends none).
type versioned struct {
	// create creates one and answers its status, body, ETag and version.
	create func() (int, []byte, string, int64)
	// get reads it.
	get func() (int, []byte, string, int64)
	// rename renames it to name, invalid when name is blank.
	rename func(ifMatch *string, name string) (int, []byte, string, int64)
	// remove deletes it; nil for an entity that is never deleted.
	remove func(ifMatch *string) (int, []byte)
	// invalid is the code refusing an invalid change of it.
	invalid string
	// action and entityID find its update audit entries.
	action   string
	entityID func() string
}

func TestWorkspaceEntityWritesNeedTheCurrentVersionInIfMatch(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	ctx := t.Context()
	p := createProject(t, admin, projectKey())

	var environmentID, unitTypeID, attributeID string
	entities := map[string]versioned{
		"project": {
			create: func() (int, []byte, string, int64) {
				r := do(admin.CreateProjectWithResponse(ctx, management.ProjectCreate{Key: projectKey(), Name: "Other"}))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON201, func(x management.Project) int64 { return x.Version })
			},
			get: func() (int, []byte, string, int64) {
				r := do(admin.GetProjectWithResponse(ctx, p.Key))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Project) int64 { return x.Version })
			},
			rename: func(ifMatch *string, name string) (int, []byte, string, int64) {
				r := do(admin.UpdateProjectWithResponse(ctx, p.Key, &management.UpdateProjectParams{IfMatch: ifMatch}, management.ProjectPatch{Name: &name}))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Project) int64 { return x.Version })
			},
			invalid:  "invalid_project",
			action:   "project.updated",
			entityID: func() string { return p.Id.String() },
		},
		"environment": {
			create: func() (int, []byte, string, int64) {
				r := do(admin.CreateEnvironmentWithResponse(ctx, p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
				if r.JSON201 != nil {
					environmentID = r.JSON201.Id.String()
				}
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON201, func(x management.Environment) int64 { return x.Version })
			},
			get: func() (int, []byte, string, int64) {
				r := do(admin.GetEnvironmentWithResponse(ctx, p.Key, "qa"))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Environment) int64 { return x.Version })
			},
			rename: func(ifMatch *string, name string) (int, []byte, string, int64) {
				r := do(admin.UpdateEnvironmentWithResponse(ctx, p.Key, "qa", &management.UpdateEnvironmentParams{IfMatch: ifMatch}, management.EnvironmentPatch{Name: &name}))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Environment) int64 { return x.Version })
			},
			remove: func(ifMatch *string) (int, []byte) {
				r := do(admin.DeleteEnvironmentWithResponse(ctx, p.Key, "qa", &management.DeleteEnvironmentParams{IfMatch: ifMatch}))
				return r.StatusCode(), r.Body
			},
			invalid:  "invalid_environment",
			action:   "environment.updated",
			entityID: func() string { return environmentID },
		},
		"unit type": {
			create: func() (int, []byte, string, int64) {
				r := do(admin.CreateUnitTypeWithResponse(ctx, p.Key, management.UnitTypeCreate{Key: "organization", Name: "Organization"}))
				if r.JSON201 != nil {
					unitTypeID = r.JSON201.Id.String()
				}
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON201, func(x management.UnitType) int64 { return x.Version })
			},
			get: func() (int, []byte, string, int64) {
				r := do(admin.GetUnitTypeWithResponse(ctx, p.Key, "organization"))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.UnitType) int64 { return x.Version })
			},
			rename: func(ifMatch *string, name string) (int, []byte, string, int64) {
				r := do(admin.UpdateUnitTypeWithResponse(ctx, p.Key, "organization", &management.UpdateUnitTypeParams{IfMatch: ifMatch}, management.UnitTypePatch{Name: &name}))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.UnitType) int64 { return x.Version })
			},
			remove: func(ifMatch *string) (int, []byte) {
				r := do(admin.DeleteUnitTypeWithResponse(ctx, p.Key, "organization", &management.DeleteUnitTypeParams{IfMatch: ifMatch}))
				return r.StatusCode(), r.Body
			},
			invalid:  "invalid_unit_type",
			action:   "unit_type.updated",
			entityID: func() string { return unitTypeID },
		},
		"attribute": {
			create: func() (int, []byte, string, int64) {
				r := do(admin.CreateAttributeWithResponse(ctx, p.Key, management.AttributeCreate{Key: "plan", Name: "Plan", Type: "string"}))
				if r.JSON201 != nil {
					attributeID = r.JSON201.Id.String()
				}
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON201, func(x management.Attribute) int64 { return x.Version })
			},
			get: func() (int, []byte, string, int64) {
				r := do(admin.GetAttributeWithResponse(ctx, p.Key, "plan"))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Attribute) int64 { return x.Version })
			},
			rename: func(ifMatch *string, name string) (int, []byte, string, int64) {
				r := do(admin.UpdateAttributeWithResponse(ctx, p.Key, "plan", &management.UpdateAttributeParams{IfMatch: ifMatch}, management.AttributePatch{Name: &name}))
				return r.StatusCode(), r.Body, r.HTTPResponse.Header.Get("ETag"), versionOf(r.JSON200, func(x management.Attribute) int64 { return x.Version })
			},
			remove: func(ifMatch *string) (int, []byte) {
				r := do(admin.DeleteAttributeWithResponse(ctx, p.Key, "plan", &management.DeleteAttributeParams{IfMatch: ifMatch}))
				return r.StatusCode(), r.Body
			},
			invalid:  "invalid_attribute",
			action:   "attribute.updated",
			entityID: func() string { return attributeID },
		},
	}

	for name, e := range entities {
		t.Run(name, func(t *testing.T) {
			if st, b, tag, v := e.create(); st != http.StatusCreated || tag != `"1"` || v != 1 {
				t.Fatalf("create = %d, ETag %s, version %d: %s; want 201 at version 1", st, tag, v, b)
			}
			if st, b, tag, v := e.get(); st != http.StatusOK || tag != `"1"` || v != 1 {
				t.Errorf("get = %d, ETag %s, version %d: %s; want version 1", st, tag, v, b)
			}

			status, body, _, _ := e.rename(nil, "Renamed")
			assertProblem(t, "no If-Match", status, body, http.StatusPreconditionRequired, "precondition_required")
			status, body, _, _ = e.rename(nil, " ")
			violationsOf(t, "an invalid change without If-Match", status, body, e.invalid)
			status, body, _, _ = e.rename(ptr(`W/"1"`), "Renamed")
			assertProblem(t, "a weak tag", status, body, http.StatusBadRequest, "invalid_request")

			if st, b, tag, v := e.rename(ptr("1"), "Renamed"); st != http.StatusOK || tag != `"2"` || v != 2 {
				t.Fatalf("a bare version = %d, ETag %s, version %d: %s; want version 2", st, tag, v, b)
			}
			status, body, _, _ = e.rename(ifMatch(1), "Again")
			if pr := assertProblem(t, "a stale version", status, body, http.StatusPreconditionFailed, "version_mismatch"); pr.CurrentVersion == nil || *pr.CurrentVersion != 2 {
				t.Errorf("current_version = %v, want 2", pr.CurrentVersion)
			}
			if st, b, tag, v := e.rename(ifMatch(2), "Renamed"); st != http.StatusOK || tag != `"2"` || v != 2 {
				t.Errorf("a change to nothing = %d, ETag %s, version %d: %s; want the entity as it is", st, tag, v, b)
			}
			if got := len(auditOf(t, srv, e.action, e.entityID())); got != 1 {
				t.Errorf("%s entries = %d, want one: refusals and no-ops record nothing", e.action, got)
			}
			if st, b, tag, v := e.get(); st != http.StatusOK || tag != `"2"` || v != 2 {
				t.Errorf("get after the change = %d, ETag %s, version %d: %s; want version 2", st, tag, v, b)
			}

			if e.remove == nil {
				return
			}
			status, body = e.remove(nil)
			assertProblem(t, "a deletion without If-Match", status, body, http.StatusPreconditionRequired, "precondition_required")
			status, body = e.remove(ifMatch(1))
			if pr := assertProblem(t, "a stale deletion", status, body, http.StatusPreconditionFailed, "version_mismatch"); pr.CurrentVersion == nil || *pr.CurrentVersion != 2 {
				t.Errorf("current_version = %v, want 2", pr.CurrentVersion)
			}
			if st, b := e.remove(ifMatch(2)); st != http.StatusNoContent {
				t.Errorf("a deletion of the current version = %d: %s", st, b)
			}
		})
	}
}

func TestListedWorkspaceEntitiesCarryTheirVersions(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	ctx := t.Context()
	p := createProject(t, admin, projectKey())
	if r := do(admin.UpdateEnvironmentWithResponse(ctx, p.Key, "staging", &management.UpdateEnvironmentParams{IfMatch: ifMatch(1)},
		management.EnvironmentPatch{Name: ptr("Stage")})); r.JSON200 == nil {
		t.Fatalf("rename = %d %s", r.StatusCode(), r.Body)
	}
	if r := do(admin.UpdateUnitTypeWithResponse(ctx, p.Key, "user", &management.UpdateUnitTypeParams{IfMatch: ifMatch(1)},
		management.UnitTypePatch{Name: ptr("Member")})); r.JSON200 == nil {
		t.Fatalf("rename = %d %s", r.StatusCode(), r.Body)
	}
	if r := do(admin.UpdateAttributeWithResponse(ctx, p.Key, "os", &management.UpdateAttributeParams{IfMatch: ifMatch(1)},
		management.AttributePatch{Name: ptr("Operating system")})); r.JSON200 == nil {
		t.Fatalf("rename = %d %s", r.StatusCode(), r.Body)
	}

	projects := do(admin.ListProjectsWithResponse(ctx))
	for _, x := range projects.JSON200.Items {
		if x.Key == p.Key && x.Version != 1 {
			t.Errorf("listed project at version %d, want 1", x.Version)
		}
	}
	for _, x := range do(admin.ListEnvironmentsWithResponse(ctx, p.Key)).JSON200.Items {
		if want := map[bool]int64{true: 2, false: 1}[x.Key == "staging"]; x.Version != want {
			t.Errorf("listed environment %s at version %d, want %d", x.Key, x.Version, want)
		}
	}
	for _, x := range do(admin.ListUnitTypesWithResponse(ctx, p.Key)).JSON200.Items {
		if want := map[bool]int64{true: 2, false: 1}[x.Key == "user"]; x.Version != want {
			t.Errorf("listed unit type %s at version %d, want %d", x.Key, x.Version, want)
		}
	}
	for _, x := range do(admin.ListAttributesWithResponse(ctx, p.Key)).JSON200.Items {
		if want := map[bool]int64{true: 2, false: 1}[x.Key == "os"]; x.Version != want {
			t.Errorf("listed attribute %s at version %d, want %d", x.Key, x.Version, want)
		}
	}
}

// versionOf returns the version of a decoded entity, 0 when the response
// carried none.
func versionOf[T any](x *T, version func(T) int64) int64 {
	if x == nil {
		return 0
	}
	return version(*x)
}
