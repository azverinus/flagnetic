package management_test

import (
	"net/http"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// RFC 9110: If-Match compares entity tags strongly, so a weak tag matches
// nothing (412), `*` matches an entity that exists, and a list matches when it
// holds the current tag. A header that is no list of tags is a request that
// cannot be read (400).
func TestIfMatchIsReadAsTheRFCReadsItOnAVersionedWrite(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "f", Type: management.FlagTypeBoolean, Name: "F"})
	rename := func(header, name string) *management.UpdateFlagResponse {
		return do(admin.UpdateFlagWithResponse(t.Context(), p.Key, "f", &management.UpdateFlagParams{IfMatch: ptr(header)}, management.FlagPatch{Name: ptr(name)}))
	}

	for header, want := range map[string]int{`W/"1"`: http.StatusPreconditionFailed, `"7", W/"1"`: http.StatusPreconditionFailed, `"a b`: http.StatusBadRequest} {
		if res := rename(header, "No"); res.StatusCode() != want {
			t.Errorf("If-Match: %s = %d, want %d: %s", header, res.StatusCode(), want, res.Body)
		}
	}
	if res := rename(`"7", "1"`, "List"); res.JSON200 == nil || res.JSON200.Version != 2 {
		t.Errorf("a list holding the current version = %d: %s, want it applied", res.StatusCode(), res.Body)
	}
	if res := rename(`*`, "Star"); res.JSON200 == nil || res.JSON200.Version != 3 {
		t.Errorf("* on an existing flag = %d: %s, want it applied", res.StatusCode(), res.Body)
	}
}

func TestIfMatchIsReadAsTheRFCReadsItOnAnImport(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	tag := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true}).HTTPResponse.Header.Get("ETag")

	for header, want := range map[string]int{`W/` + tag: http.StatusPreconditionFailed, `"someone else's", W/` + tag: http.StatusPreconditionFailed, `"a b`: http.StatusBadRequest} {
		if res := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true, ifMatch: header}); res.StatusCode() != want {
			t.Errorf("If-Match: %s = %d, want %d: %s", header, res.StatusCode(), want, res.Body)
		}
	}
	for _, header := range []string{`"someone else's", ` + tag, `*`, tag} {
		if res := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true, ifMatch: header}); res.JSON200 == nil {
			t.Errorf("If-Match: %s = %d: %s, want it accepted", header, res.StatusCode(), res.Body)
		}
	}
}
