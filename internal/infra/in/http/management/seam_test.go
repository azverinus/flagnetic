package management_test

import (
	"net/http"
	"strings"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// No public operation is declared yet, so the seam is exercised with one of
// the test's own.
func TestAPublicOperationRefusesABadCredentialLikeAnyOther(t *testing.T) {
	op := access.Operation{Name: "readPublic", Requirement: access.RequirePublic, Read: true}
	handlers := management.Handlers{AuthenticateToken: appaccess.NewAuthenticateToken(pgaccess.NewStore(testkit.Pool(t)), appaccess.NewTokenUses(pgaccess.NewStore(testkit.Pool(t))), clock.System{})}
	srv := testkit.StartServer(t, testkit.WithRoute("GET /public", func(edge *flaghttp.Server) http.Handler {
		return management.SeamFor(edge, handlers, op, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}))

	for name, header := range map[string]string{
		"no credential": "",
		"a valid token": "Bearer " + testkit.InstanceAdminToken(t),
	} {
		h := map[string]string{}
		if header != "" {
			h["Authorization"] = header
		}
		if res := get(t, srv.URL+"/public", h); res.StatusCode != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204", name, res.StatusCode)
		}
	}
	for name, header := range map[string]string{
		"an unknown token":  "Bearer fn_pat_" + strings.Repeat("A", 40),
		"a malformed token": "Bearer nonsense",
		"another scheme":    "Basic YWRtaW46c2VjcmV0",
		"an empty attempt":  "Bearer ",
	} {
		assertRefused(t, name, get(t, srv.URL+"/public", map[string]string{"Authorization": header}), "invalid_token")
	}
}
