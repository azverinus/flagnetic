package management_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A shared HTTP cache that reuses answers by URL must never serve one
// caller's /me, ruleset or refusal to another.
func TestEveryAnswerOfTheManagementAPIIsMarkedNoStore(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	me := do(admin.GetMeWithResponse(t.Context()))
	ruleset := do(admin.GetEnvironmentRulesetWithResponse(t.Context(), p.Key, "production"))
	refused := do(testkit.Client(t, srv, "").GetMeWithResponse(t.Context()))
	missing := do(admin.GetProjectWithResponse(t.Context(), "no-such-project"))
	for name, res := range map[string]*http.Response{
		"GET /me": me.HTTPResponse, "the ruleset": ruleset.HTTPResponse, "a refusal": refused.HTTPResponse, "a 404": missing.HTTPResponse,
	} {
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s answered %d with Cache-Control %q, want no-store", name, res.StatusCode, got)
		}
	}
	if me.StatusCode() != http.StatusOK || ruleset.StatusCode() != http.StatusOK || refused.StatusCode() != http.StatusUnauthorized || missing.StatusCode() != http.StatusNotFound {
		t.Errorf("statuses = %d %d %d %d, want 200 200 401 404", me.StatusCode(), ruleset.StatusCode(), refused.StatusCode(), missing.StatusCode())
	}

	// An address no route owns is the edge's own answer.
	res, err := http.Get(srv.URL + management.BaseURL + "/no/such/route")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("an unknown route answered %d with Cache-Control %q, want no-store", res.StatusCode, got)
	}
}

// Only the code listed here chooses its own Cache-Control: a handler that
// sets one overrides the boundary's no-store, so each is a decision to make
// on purpose. The change stream's document says no-cache.
func TestOnlyListedHandlersSetTheirOwnCacheControl(t *testing.T) {
	listed := []string{"api.go", "stream.go"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var setting []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".gen.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `"Cache-Control"`) {
			setting = append(setting, f)
		}
	}
	slices.Sort(setting)
	if !slices.Equal(setting, listed) {
		t.Errorf("files setting Cache-Control = %v, want %v; add a new one here only when its answer may be cached", setting, listed)
	}
}
