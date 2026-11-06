package scim_test

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestDiscoveryDocumentsAreRFCListResponsesBehindABearer(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))

	config := e.call(t, token, http.MethodGet, "/ServiceProviderConfig", "")
	doc := config.json(t)
	if config.status != http.StatusOK || !strings.HasPrefix(config.header.Get("Content-Type"), "application/scim+json") ||
		doc["patch"].(map[string]any)["supported"] != true || doc["bulk"].(map[string]any)["supported"] != false ||
		doc["filter"].(map[string]any)["maxResults"] != 100.0 || doc["sort"].(map[string]any)["supported"] != false {
		t.Errorf("ServiceProviderConfig = %d %s, want patch and filter supported, bulk and sort not", config.status, config.body)
	}

	for path, want := range map[string]string{
		"/ResourceTypes": "urn:ietf:params:scim:schemas:core:2.0:User",
		"/Schemas":       "urn:ietf:params:scim:schemas:core:2.0:User",
	} {
		r := e.call(t, token, http.MethodGet, path, "")
		doc := r.json(t)
		resources, _ := doc["Resources"].([]any)
		if r.status != http.StatusOK || doc["totalResults"] != 1.0 || len(resources) != 1 ||
			doc["schemas"].([]any)[0] != "urn:ietf:params:scim:api:messages:2.0:ListResponse" ||
			!strings.Contains(string(r.body), want) {
			t.Errorf("%s = %d %s, want a list response of the User resource", path, r.status, r.body)
		}
	}
}

func TestABearerThatIsNoLiveTokenIsRefusedAlike(t *testing.T) {
	c := testkit.Clock()
	e := start(t, testkit.WithClock(c))
	slug := e.provider(t)
	good := e.token(t, slug)
	expiring := e.createToken(t, management.ScimTokenCreate{Provider: slug, ExpiresAt: ptr(c.Now().Add(time.Hour))}).Value
	revoked := e.createToken(t, management.ScimTokenCreate{Provider: slug})
	if res, err := e.browser.RevokeScimTokenWithResponse(t.Context(), revoked.Token.Id.String()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoking: %v, %d", err, res.StatusCode())
	}
	personal := testkit.MemberToken(t, testkit.Pool(t))

	for name, token := range map[string]string{
		"no bearer": "", "a made up value": "fn_scim_" + strings.Repeat("a", 40), "a short value": "fn_scim_abc",
		"a personal access token": personal, "a revoked token": revoked.Value,
	} {
		r := e.call(t, token, http.MethodGet, "/ServiceProviderConfig", "")
		assertScimError(t, name, r, http.StatusUnauthorized, "invalidCredentials")
		if r.header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: WWW-Authenticate = %q, want Bearer", name, r.header.Get("WWW-Authenticate"))
		}
	}
	if r := e.call(t, expiring, http.MethodGet, "/ServiceProviderConfig", ""); r.status != http.StatusOK {
		t.Errorf("a token before its expiry = %d, want 200", r.status)
	}
	c.Advance(time.Hour)
	assertScimError(t, "an expired token", e.call(t, expiring, http.MethodGet, "/ServiceProviderConfig", ""), http.StatusUnauthorized, "invalidCredentials")
	if r := e.call(t, good, http.MethodGet, "/ServiceProviderConfig", ""); r.status != http.StatusOK {
		t.Errorf("a token that does not expire = %d, want 200", r.status)
	}
}

func TestADisabledProvidersTokenIsRefusedUntilItIsEnabledAgain(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	disable := func() {
		t.Helper()
		if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), slug); err != nil || res.JSON200 == nil {
			t.Fatalf("disabling: %v, %d: %s", err, res.StatusCode(), res.Body)
		}
	}
	disable()
	assertScimError(t, "a disabled provider's token", e.call(t, token, http.MethodGet, "/Users", ""), http.StatusUnauthorized, "invalidCredentials")
	if res, err := e.admin.EnableAuthProviderWithResponse(t.Context(), slug); err != nil || res.JSON200 == nil {
		t.Fatalf("enabling: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	if r := e.call(t, token, http.MethodGet, "/Users", ""); r.status != http.StatusOK {
		t.Errorf("the token of an enabled provider = %d, want 200: %s", r.status, r.body)
	}
}

func TestTheUseOfATokenIsRecordedAtMostOnceAMinute(t *testing.T) {
	c := testkit.Clock()
	e := start(t, testkit.WithClock(c))
	created := e.createToken(t, management.ScimTokenCreate{Provider: e.provider(t)})
	lastUsed := func() *time.Time {
		t.Helper()
		res, err := e.browser.ListScimTokensWithResponse(t.Context())
		if err != nil || res.JSON200 == nil {
			t.Fatalf("listing: %v", err)
		}
		for _, tok := range res.JSON200.Items {
			if tok.Id == created.Token.Id {
				return tok.LastUsedAt
			}
		}
		t.Fatal("the token is not listed")
		return nil
	}
	if lastUsed() != nil {
		t.Fatal("an unused token has a last use")
	}
	e.call(t, created.Value, http.MethodGet, "/Users", "")
	first := c.Now()
	if got := lastUsed(); got == nil || !got.Equal(first) {
		t.Fatalf("last use = %v, want %v", got, first)
	}
	c.Advance(59 * time.Second)
	e.call(t, created.Value, http.MethodGet, "/Users", "")
	if got := lastUsed(); !got.Equal(first) {
		t.Errorf("a use within the minute moved the last use to %v", got)
	}
	c.Advance(time.Second)
	e.call(t, created.Value, http.MethodGet, "/Users", "")
	if got := lastUsed(); !got.Equal(c.Now()) {
		t.Errorf("a use a minute later recorded %v, want %v", got, c.Now())
	}
}

func TestBadTokensCountAgainstTheAddressThrottleAndGoodOnesDoNot(t *testing.T) {
	// The tests of a package share the loopback address and its throttle, so
	// this one comes from an address of its own, which the server believes
	// its peer about.
	e := start(t, testkit.WithEdge(func(o *flaghttp.Options) {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	}))
	token := e.token(t, e.provider(t))
	from := "203.0.113.7"
	for range 105 {
		if r := e.callFrom(t, from, token, http.MethodGet, "/ServiceProviderConfig", ""); r.status != http.StatusOK {
			t.Fatalf("a good token = %d: %s", r.status, r.body)
		}
	}
	var last reply
	for range 101 {
		last = e.callFrom(t, from, "fn_scim_"+strings.Repeat("b", 40), http.MethodGet, "/Users", "")
	}
	assertScimError(t, "the hundred and first bad token", last, http.StatusTooManyRequests, "")
	if last.header.Get("Retry-After") == "" {
		t.Error("a throttled address is not told when to retry")
	}
	assertScimError(t, "a good token from a throttled address", e.callFrom(t, from, token, http.MethodGet, "/Users", ""), http.StatusTooManyRequests, "")
	if r := e.call(t, token, http.MethodGet, "/Users", ""); r.status != http.StatusOK {
		t.Errorf("a good token from another address = %d, want 200", r.status)
	}
}

func TestUnknownPathsAndMethodsAreSCIMErrorsAndABodyHasABound(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))

	assertScimError(t, "an unknown path", e.call(t, token, http.MethodGet, "/Bulk", ""), http.StatusNotFound, "")
	assertScimError(t, "an unknown path without a bearer", e.call(t, "", http.MethodGet, "/Bulk", ""), http.StatusNotFound, "")
	r := e.call(t, token, http.MethodPut, "/Schemas", "{}")
	assertScimError(t, "a method not served", r, http.StatusMethodNotAllowed, "")
	if allow := r.header.Get("Allow"); allow != "GET" {
		t.Errorf("Allow = %q, want GET", allow)
	}

	big := `{"userName":"` + email("example.com") + `","displayName":"` + strings.Repeat("x", 70<<10) + `"}`
	assertScimError(t, "a body over 64 KiB", e.call(t, token, http.MethodPost, "/Users", big), http.StatusRequestEntityTooLarge, "")
	assertScimError(t, "a body that is not JSON", e.call(t, token, http.MethodPost, "/Users", `{"userName":`), http.StatusBadRequest, "invalidSyntax")
	assertScimError(t, "an id that is none", e.call(t, token, http.MethodGet, "/Users/not-a-uuid", ""), http.StatusNotFound, "")
}
