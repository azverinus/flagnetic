package management_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestMeShowsTheBootstrapAdminAndTheTokenInUse(t *testing.T) {
	srv := testkit.StartServer(t)
	res, err := testkit.Client(t, srv, testkit.InstanceAdminToken(t)).GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("GET /me = %d: %s", res.StatusCode(), res.Body)
	}
	me := res.JSON200
	if m := me.Member; m == nil || m.Email != testkit.InstanceAdminEmail || m.DisplayName != testkit.InstanceAdminName ||
		m.Status != management.MemberStatusActive || !m.InstanceAdmin || m.Locale != nil || m.Id == [16]byte{} {
		t.Errorf("member = %+v, want the active bootstrap instance admin", me.Member)
	}
	c := me.Credential
	if c.Method != management.CredentialMethodToken || c.TokenId == nil || *c.TokenId == [16]byte{} ||
		c.TokenName == nil || *c.TokenName != "bootstrap" {
		t.Errorf("credential = %+v, want the bootstrap token", c)
	}
	if me.ClientIp != "127.0.0.1" {
		t.Errorf("client_ip = %q, want 127.0.0.1", me.ClientIp)
	}
}

func TestATokenInTheQueryStringIsNotAccepted(t *testing.T) {
	srv := testkit.StartServer(t)
	tok := testkit.InstanceAdminToken(t)
	for _, param := range []string{"access_token", "token"} {
		res := get(t, srv.URL+"/api/v1/me?"+param+"="+tok, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("?%s=: status = %d, want 401", param, res.StatusCode)
		}
	}
}

func TestManagementAPIRefusesARequestWithoutCredentials(t *testing.T) {
	srv := testkit.StartServer(t)
	res, err := testkit.Client(t, srv, "").GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode())
	}
	if got := res.HTTPResponse.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", got)
	}
	p := res.ApplicationproblemJSON401
	if p == nil || p.Code != "unauthenticated" || p.RequestId != res.HTTPResponse.Header.Get("X-Request-ID") {
		t.Errorf("problem = %+v, want unauthenticated with the request's ID", p)
	}
}

func TestManagementAPIRefusesAMalformedOrUnknownToken(t *testing.T) {
	srv := testkit.StartServer(t)
	tok := testkit.InstanceAdminToken(t)
	for name, header := range map[string]string{
		"unknown token":   "Bearer fn_pat_" + strings.Repeat("0", 40),
		"malformed token": "Bearer not-a-token",
		"truncated token": "Bearer " + tok[:len(tok)-1],
		"empty bearer":    "Bearer ",
		"other scheme":    "Basic " + tok,
		"empty header":    "",
	} {
		res := get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": header})
		assertRefused(t, name, res, "invalid_token")
	}
}

func TestAnExpiredOrRevokedTokenIsRefusedWithItsCode(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := me(t, srv, testkit.Bootstrap(t, pool))
	memberID := id.MustParse[access.Member](admin.Member.Id.String())

	store := pgaccess.NewStore(pool)
	past := time.Now().Add(-time.Minute)
	newToken := func(expiresAt, revokedAt *time.Time) string {
		ids := id.NewSource(testkit.Clock())
		tok, v, err := access.NewPersonalToken(id.New[access.APIToken](ids), access.NewPersonalTokenInput{
			Member: memberID, Name: "script", ExpiresAt: expiresAt, Now: past.Add(-time.Hour),
		}, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tok.RevokedAt = revokedAt
		if err := store.CreateToken(context.Background(), tok); err != nil {
			t.Fatal(err)
		}
		return v.Value.Reveal()
	}
	for name, tc := range map[string]struct {
		token string
		code  string
	}{
		"expired":             {newToken(&past, nil), "token_expired"},
		"revoked":             {newToken(nil, &past), "token_revoked"},
		"revoked and expired": {newToken(&past, &past), "token_revoked"},
	} {
		res := get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": "Bearer " + tc.token})
		assertRefused(t, name, res, tc.code)
	}
	scrape := srv.Scrape(t)
	if got := testkit.Metric(t, scrape, `flagnetic_management_refusals_total{code="token_revoked",operation="getMe"}`); got != 2 {
		t.Errorf("token_revoked refusals counted = %v, want 2", got)
	}
}

func me(t *testing.T, srv *testkit.Server, token string) *management.Me {
	t.Helper()
	res, err := testkit.Client(t, srv, token).GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("GET /me = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200
}

type response struct {
	StatusCode int
	Header     http.Header
	problem    *management.Problem
}

func get(t *testing.T, url string, header map[string]string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := management.ParseGetMeResponse(res)
	if err != nil {
		t.Fatal(err)
	}
	return response{StatusCode: res.StatusCode, Header: res.Header, problem: parsed.ApplicationproblemJSON401}
}

func assertRefused(t *testing.T, name string, res response, code string) {
	t.Helper()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("%s: status = %d, want 401", name, res.StatusCode)
		return
	}
	want := `Bearer error="invalid_token", error_description="` + code + `"`
	if got := res.Header.Get("WWW-Authenticate"); got != want {
		t.Errorf("%s: WWW-Authenticate = %q, want %q", name, got, want)
	}
	if res.problem == nil || res.problem.Code != code || res.problem.RequestId != res.Header.Get("X-Request-ID") {
		t.Errorf("%s: problem = %+v, want %s with the request's ID", name, res.problem, code)
	}
}
