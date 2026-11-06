package scim_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// publicURL is the address the instance believes it is served at.
const publicURL = "https://flags.example.com"

func ptr[T any](v T) *T { return &v }

func random() string { return strings.ToLower(rand.Text()[:12]) }

// email returns an address of domain nobody holds.
func email(domain string) string { return "u" + random() + "@" + domain }

// env is an instance with an instance admin signed in, the identity
// providers and SCIM tokens the tests of a package share.
type env struct {
	srv *testkit.Server
	// admin is the instance admin's token; browser is their session, the only
	// credential that creates a SCIM token.
	admin   *management.ClientWithResponses
	browser *testkit.Browser
}

func start(t *testing.T, opts ...testkit.ServerOption) env {
	t.Helper()
	srv := testkit.StartServer(t, append([]testkit.ServerOption{testkit.WithBaseURL(publicURL)}, opts...)...)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, m.Email, m.Password)
	return env{srv: srv, admin: admin, browser: b}
}

// provider creates an enabled OIDC auth provider vouching for domains, and
// returns its slug.
func (e env) provider(t *testing.T, domains ...string) string {
	t.Helper()
	if len(domains) == 0 {
		domains = []string{"example.com"}
	}
	secret := "client-secret-" + rand.Text()
	testkit.RecordSecret(secret)
	slug := "p" + random()
	res, err := e.admin.CreateAuthProviderWithResponse(t.Context(), management.AuthProviderCreate{
		Slug: slug, Kind: management.AuthProviderKindOidc, DisplayName: "Corporate SSO", Domains: domains,
		Issuer: ptr("https://idp.example.com/realms/" + slug), ClientId: ptr("flagnetic"), ClientSecret: &secret,
	})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating the provider: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return slug
}

// token creates a SCIM token bound to provider and returns its value.
func (e env) token(t *testing.T, provider string) string {
	t.Helper()
	return e.createToken(t, management.ScimTokenCreate{Provider: provider}).Value
}

func (e env) createToken(t *testing.T, body management.ScimTokenCreate) management.CreatedScimToken {
	t.Helper()
	res, err := e.browser.CreateScimTokenWithResponse(t.Context(), body)
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating the SCIM token: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	testkit.RecordSecret(res.JSON201.Value)
	return *res.JSON201
}

// reply is what the SCIM API answered a raw request.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// json decodes the body into a map.
func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("answer %d is not a JSON object: %s", r.status, r.body)
	}
	return out
}

// call sends a request the way an identity provider's connector would:
// the exact body it sends, with the media type SCIM names.
func (e env) call(t *testing.T, token, method, path, body string) reply {
	t.Helper()
	return e.callFrom(t, "", token, method, path, body)
}

// callFrom is call from the address forwardedFor, when the server trusts its
// peer to say so; "" is the peer's own.
func (e env) callFrom(t *testing.T, forwardedFor, token, method, path, body string) reply {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+scim.BaseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: res.StatusCode, header: res.Header, body: raw}
}

// marshal returns v as the JSON a connector sends.
func marshal(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// assertScimError checks r is the SCIM error document of status and scimType.
func assertScimError(t *testing.T, what string, r reply, status int, scimType string) {
	t.Helper()
	if r.status != status {
		t.Errorf("%s: status = %d, want %d: %s", what, r.status, status, r.body)
		return
	}
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/scim+json") {
		t.Errorf("%s: content type = %q, want application/scim+json", what, ct)
	}
	doc := r.json(t)
	schemas, _ := doc["schemas"].([]any)
	if len(schemas) != 1 || schemas[0] != "urn:ietf:params:scim:api:messages:2.0:Error" || doc["status"] != itoa(status) {
		t.Errorf("%s: document = %s, want the SCIM error schema and status %d as a string", what, r.body, status)
	}
	if got, _ := doc["scimType"].(string); got != scimType {
		t.Errorf("%s: scimType = %q, want %q (%s)", what, got, scimType, r.body)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func escape(s string) string { return url.QueryEscape(s) }

func newUUID() string { return uuid.NewString() }

// secretOfLink returns the secret of a link an instance admin was given.
func secretOfLink(link string) string { return link[strings.LastIndex(link, "/")+1:] }

// do returns res, failing the test on err.
func do[R any](res R, err error) R {
	if err != nil {
		panic(err)
	}
	return res
}

// assertProblem checks a management API answer is the problem document of
// status and code.
func assertProblem(t *testing.T, what string, status int, body []byte, wantStatus int, wantCode string) management.Problem {
	t.Helper()
	var p management.Problem
	if status != wantStatus {
		t.Errorf("%s: status = %d, want %d: %s", what, status, wantStatus, body)
		return p
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Code != wantCode {
		t.Errorf("%s: problem = %s, want code %s", what, body, wantCode)
	}
	return p
}
