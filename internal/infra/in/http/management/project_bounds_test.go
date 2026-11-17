package management_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A project has at most 20 environments, since each holds a configuration of
// every flag and is held by every replica and SDK.
func TestAProjectHasAtMostTwentyEnvironments(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	listed := do(admin.ListEnvironmentsWithResponse(t.Context(), p.Key))
	if listed.JSON200 == nil {
		t.Fatalf("list environments = %d %s", listed.StatusCode(), listed.Body)
	}
	for i := len(listed.JSON200.Items); i < 20; i++ {
		key := fmt.Sprintf("env%d", i)
		if res := do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: key, Name: key})); res.JSON201 == nil {
			t.Fatalf("environment %d = %d %s", i, res.StatusCode(), res.Body)
		}
	}
	res := do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "one_too_many", Name: "One too many"}))
	pr := assertProblem(t, "the 21st environment", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_environment")
	if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Code != "too_many" {
		t.Errorf("errors = %+v, want one too_many", pr.Errors)
	}
}

// A string value of a condition has at most 1,000 characters, so a ruleset
// every replica holds is not made large by one write.
func TestAConditionStringHasAtMostAThousandCharacters(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	declare(t, admin, p.Key, "plan")
	long := `"` + strings.Repeat("a", 1001) + `"`
	res := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{
		Key: "long", Name: "Long", Conditions: conditions(attr("plan", "eq", long))}))
	pr := assertProblem(t, "a long string", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_segment")
	if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Code != "too_long" {
		t.Errorf("errors = %+v, want one too_long", pr.Errors)
	}
}

// A document creates no more environments than a request does: the 21st is
// refused at its pointer and the project is not created.
func TestADocumentOfMoreThanTwentyEnvironmentsIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := projectKey()
	doc := mutate(documentFor(key), func(d map[string]any) {
		envs := d["environments"].([]any)
		for i := len(envs); i < 21; i++ {
			envs = append(envs, map[string]any{"key": fmt.Sprintf("env%d", i), "name": fmt.Sprintf("Env %d", i), "protected": false})
		}
		d["environments"] = envs
	})
	res := importJSON(t, admin, management.ImportNewProjectParams{}, doc)
	pr := assertProblem(t, "21 environments", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_document")
	var found []string
	if pr.Errors != nil {
		for _, v := range *pr.Errors {
			found = append(found, v.Pointer+" "+v.Code)
		}
	}
	if !slices.Equal(found, []string{"/environments/20/key too_many"}) {
		t.Errorf("errors = %v, want /environments/20/key too_many", found)
	}
	if hasProject(t, admin, key) {
		t.Error("a refused document created a project")
	}
}
