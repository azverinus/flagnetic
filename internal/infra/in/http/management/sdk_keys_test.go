package management_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// mustCreateSDKKey creates a key in environment of project as c, failing
// the test unless it is created.
func mustCreateSDKKey(t *testing.T, c *management.ClientWithResponses, project, environment string, body management.SdkKeyCreate,
) management.CreatedSdkKey {
	t.Helper()
	res := do(c.CreateSdkKeyWithResponse(t.Context(), project, environment, body))
	if res.JSON201 == nil {
		t.Fatalf("POST …/environments/%s/sdk-keys = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// sdkKeys lists the keys of environment of project as c.
func sdkKeys(t *testing.T, c *management.ClientWithResponses, project, environment string) []management.SdkKey {
	t.Helper()
	res := do(c.ListSdkKeysWithResponse(t.Context(), project, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET …/environments/%s/sdk-keys = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// sdkKeyNamed returns the listed key identified by key.
func sdkKeyNamed(t *testing.T, c *management.ClientWithResponses, project, environment, key string) management.SdkKey {
	t.Helper()
	for _, k := range sdkKeys(t, c, project, environment) {
		if k.Id.String() == key {
			return k
		}
	}
	t.Fatalf("no key %s in %s", key, environment)
	return management.SdkKey{}
}

// environmentID returns the identifier of environment of project.
func environmentID(t *testing.T, c *management.ClientWithResponses, project, environment string) openapi_types.UUID {
	t.Helper()
	res := do(c.GetEnvironmentWithResponse(t.Context(), project, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET …/environments/%s = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return res.JSON200.Id
}

func rotate(t *testing.T, c *management.ClientWithResponses, project, environment, key string, overlap *int64) *management.RotateSdkKeyResponse {
	t.Helper()
	return do(c.RotateSdkKeyWithResponse(t.Context(), project, environment, key, management.SdkKeyRotation{OverlapSeconds: overlap}))
}

func revoke(t *testing.T, c *management.ClientWithResponses, project, environment, key string) *management.RevokeSdkKeyResponse {
	t.Helper()
	return do(c.RevokeSdkKeyWithResponse(t.Context(), project, environment, key))
}

func TestAnSDKKeysValueIsShownOnceAndItsListingCarriesNone(t *testing.T) {
	srv, _, c := tokenServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	server := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "checkout-api"})
	c.Advance(time.Second)
	client := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{
		Kind: "client", Name: "storefront", AllowedOrigins: &[]string{"https://Shop.Example:443", "http://localhost:3000"},
	})
	for prefix, created := range map[string]management.CreatedSdkKey{"fn_srv_": server, "fn_cli_": client} {
		if !regexp.MustCompile(`^` + prefix + `[0-9A-Za-z]{40}$`).MatchString(created.Value) {
			t.Errorf("value %q is not %s and 40 base62 characters", created.Value, prefix)
		}
		if k := created.Key; k.KeyId != created.Value[len(prefix):len(prefix)+8] || k.Status != management.SdkKeyStatusActive ||
			k.LastUsedAt != nil || k.ExpiresAt != nil || k.RevokedAt != nil {
			t.Errorf("%s key = %+v, want active, its key ID the 8 characters after the prefix", prefix, k)
		}
	}
	if k := server.Key; k.Kind != management.SdkKeyKindServer || k.Name != "checkout-api" || len(k.AllowedOrigins) != 0 ||
		!k.CreatedAt.Equal(testkit.Start) {
		t.Errorf("server key = %+v", k)
	}
	if k := client.Key; k.Kind != management.SdkKeyKindClient ||
		!slices.Equal(k.AllowedOrigins, []string{"https://shop.example", "http://localhost:3000"}) {
		t.Errorf("client key = %+v, want its origins normalized", k)
	}

	listed := do(admin.ListSdkKeysWithResponse(t.Context(), p.Key, "staging"))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 2 || listed.JSON200.Items[0].Id != server.Key.Id ||
		listed.JSON200.Items[1].Id != client.Key.Id {
		t.Fatalf("listed = %d %s, want both keys, the oldest first", listed.StatusCode(), listed.Body)
	}
	for _, v := range []string{server.Value, client.Value} {
		if strings.Contains(string(listed.Body), v) || strings.Contains(string(listed.Body), v[7:]) {
			t.Errorf("the listing carries a key's value: %s", listed.Body)
		}
	}
	if got := sdkKeys(t, admin, p.Key, "production"); len(got) != 0 {
		t.Errorf("production lists %+v, want none of staging's keys", got)
	}
}

func TestAnSDKKeyIsRefusedWithEveryBrokenRuleAndInAnUnknownEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	res := do(admin.CreateSdkKeyWithResponse(t.Context(), p.Key, "staging", management.SdkKeyCreate{
		Kind: "mobile", Name: " ", AllowedOrigins: &[]string{"https://shop.example/cart"},
	}))
	if got := violationsOf(t, "a bad key", res.StatusCode(), res.Body, "invalid_sdk_key"); !slices.Equal(got,
		[]string{"/kind unknown_kind", "/name invalid_name", "/allowed_origins/0 invalid_origin"}) {
		t.Errorf("violations = %v", got)
	}
	res = do(admin.CreateSdkKeyWithResponse(t.Context(), p.Key, "staging", management.SdkKeyCreate{
		Kind: "server", Name: "api", AllowedOrigins: &[]string{"https://shop.example"},
	}))
	if got := violationsOf(t, "origins on a server key", res.StatusCode(), res.Body, "invalid_sdk_key"); !slices.Equal(got,
		[]string{"/allowed_origins server_key_origins"}) {
		t.Errorf("violations = %v", got)
	}
	res = do(admin.CreateSdkKeyWithResponse(t.Context(), p.Key, "qa", management.SdkKeyCreate{Kind: "server", Name: "api"}))
	assertProblem(t, "an unknown environment", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	if got := sdkKeys(t, admin, p.Key, "staging"); len(got) != 0 {
		t.Errorf("refused keys were stored: %+v", got)
	}
	listed := do(admin.ListSdkKeysWithResponse(t.Context(), p.Key, "qa"))
	assertProblem(t, "listing an unknown environment", listed.StatusCode(), listed.Body, http.StatusNotFound, "not_found")
}

func TestAnEnvironmentHoldsAtMostTenActiveKeysOfEachKind(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	server := management.SdkKeyCreate{Kind: "server", Name: "api"}
	var first management.CreatedSdkKey
	for i := range 10 {
		k := mustCreateSDKKey(t, admin, p.Key, "staging", server)
		if i == 0 {
			first = k
		}
	}
	res := do(admin.CreateSdkKeyWithResponse(t.Context(), p.Key, "staging", server))
	assertProblem(t, "an eleventh server key", res.StatusCode(), res.Body, http.StatusConflict, "sdk_key_limit_reached")
	mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "client", Name: "web"})
	mustCreateSDKKey(t, admin, p.Key, "production", server)

	// The replacement takes the rotated key's place, so a rotation at the
	// limit is accepted and leaves it reached.
	if r := rotate(t, admin, p.Key, "staging", first.Key.Id.String(), nil); r.JSON201 == nil {
		t.Fatalf("rotating at the limit = %d: %s", r.StatusCode(), r.Body)
	}
	res = do(admin.CreateSdkKeyWithResponse(t.Context(), p.Key, "staging", server))
	assertProblem(t, "a server key after a rotation", res.StatusCode(), res.Body, http.StatusConflict, "sdk_key_limit_reached")

	keys := sdkKeys(t, admin, p.Key, "staging")
	if r := revoke(t, admin, p.Key, "staging", keys[1].Id.String()); r.JSON200 == nil {
		t.Fatalf("revoking = %d: %s", r.StatusCode(), r.Body)
	}
	mustCreateSDKKey(t, admin, p.Key, "staging", server)
}

func TestARotatedKeyKeepsWorkingForTheOverlapThenExpires(t *testing.T) {
	srv, _, c := tokenServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{
		Kind: "client", Name: "storefront", AllowedOrigins: &[]string{"https://shop.example"},
	})

	r := rotate(t, admin, p.Key, "staging", k.Key.Id.String(), nil)
	if r.JSON201 == nil {
		t.Fatalf("rotating = %d: %s", r.StatusCode(), r.Body)
	}
	next := r.JSON201
	if next.Key.Id == k.Key.Id || next.Value == k.Value || next.Key.Kind != k.Key.Kind || next.Key.Name != "storefront" ||
		!slices.Equal(next.Key.AllowedOrigins, k.Key.AllowedOrigins) || next.Key.Status != management.SdkKeyStatusActive ||
		next.Key.ExpiresAt != nil || !strings.HasPrefix(next.Value, "fn_cli_") {
		t.Errorf("replacement = %+v, want a new active client key like %+v", next.Key, k.Key)
	}
	old := sdkKeyNamed(t, admin, p.Key, "staging", k.Key.Id.String())
	if old.Status != management.SdkKeyStatusRotating || old.ExpiresAt == nil || !old.ExpiresAt.Equal(c.Now().Add(7*24*time.Hour)) {
		t.Errorf("rotated = %+v, want rotating until 7 days from now", old)
	}
	c.Advance(7 * 24 * time.Hour)
	if old := sdkKeyNamed(t, admin, p.Key, "staging", k.Key.Id.String()); old.Status != management.SdkKeyStatusExpired {
		t.Errorf("after the overlap the rotated key is %s, want expired", old.Status)
	}

	hour := int64(3600)
	r = rotate(t, admin, p.Key, "staging", next.Key.Id.String(), &hour)
	if old := sdkKeyNamed(t, admin, p.Key, "staging", next.Key.Id.String()); r.JSON201 == nil || old.ExpiresAt == nil ||
		!old.ExpiresAt.Equal(c.Now().Add(time.Hour)) {
		t.Errorf("an hour's overlap = %d %s, rotated %+v", r.StatusCode(), r.Body, old)
	}
	zero := int64(0)
	third := r.JSON201.Key.Id.String()
	if immediate := rotate(t, admin, p.Key, "staging", third, &zero); immediate.JSON201 == nil {
		t.Errorf("no overlap = %d %s", immediate.StatusCode(), immediate.Body)
	} else if old := sdkKeyNamed(t, admin, p.Key, "staging", third); old.Status != management.SdkKeyStatusExpired {
		t.Errorf("without overlap the rotated key is %s, want expired at once", old.Status)
	}

	for name, key := range map[string]string{"an expired key": k.Key.Id.String(), "a rotating key": next.Key.Id.String()} {
		refused := rotate(t, admin, p.Key, "staging", key, nil)
		assertProblem(t, "rotating "+name, refused.StatusCode(), refused.Body, http.StatusConflict, "sdk_key_not_active")
	}
	revoked := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"})
	revoke(t, admin, p.Key, "staging", revoked.Key.Id.String())
	r = rotate(t, admin, p.Key, "staging", revoked.Key.Id.String(), nil)
	assertProblem(t, "rotating a revoked key", r.StatusCode(), r.Body, http.StatusConflict, "sdk_key_not_active")

	active := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"})
	for _, seconds := range []int64{-1, 7_776_001} {
		r := rotate(t, admin, p.Key, "staging", active.Key.Id.String(), &seconds)
		if got := violationsOf(t, "an overlap out of range", r.StatusCode(), r.Body, "invalid_rotation"); !slices.Equal(got,
			[]string{"/overlap_seconds out_of_range"}) {
			t.Errorf("overlap %d: violations %v", seconds, got)
		}
	}
	if k := sdkKeyNamed(t, admin, p.Key, "staging", active.Key.Id.String()); k.Status != management.SdkKeyStatusActive {
		t.Errorf("a refused rotation left the key %s", k.Status)
	}
}

func TestRevokingAKeySetsItsRevocationOnce(t *testing.T) {
	srv, _, c := tokenServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"})

	first := revoke(t, admin, p.Key, "staging", k.Key.Id.String())
	if first.JSON200 == nil || first.JSON200.Status != management.SdkKeyStatusRevoked || first.JSON200.RevokedAt == nil ||
		!first.JSON200.RevokedAt.Equal(c.Now()) {
		t.Fatalf("revoking = %d %s", first.StatusCode(), first.Body)
	}
	c.Advance(time.Hour)
	again := revoke(t, admin, p.Key, "staging", k.Key.Id.String())
	if again.JSON200 == nil || !again.JSON200.RevokedAt.Equal(*first.JSON200.RevokedAt) {
		t.Errorf("revoking again = %d %s, want the key unchanged", again.StatusCode(), again.Body)
	}
	revoked := "sdk_key.revoked"
	entries := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{Action: &revoked}))
	if got := actions(t, "revocations", entries); len(got) != 1 {
		t.Errorf("revocations recorded %v, want one", got)
	}
}

func TestAnUpdateRenamesAKeyAndReplacesItsRequiredAllowedOrigins(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	client := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{
		Kind: "client", Name: "storefront", AllowedOrigins: &[]string{"https://shop.example"},
	})
	key := client.Key.Id.String()
	patch := func(body string) *management.UpdateSdkKeyResponse {
		return do(admin.UpdateSdkKeyWithBodyWithResponse(t.Context(), p.Key, "staging", key, "application/json", strings.NewReader(body)))
	}

	for name, body := range map[string]string{"no allowed origins": `{"name": "shop"}`, "null allowed origins": `{"allowed_origins": null}`} {
		res := patch(body)
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_sdk_key"); !slices.Equal(got, []string{"/allowed_origins required"}) {
			t.Errorf("%s: violations %v", name, got)
		}
	}
	if k := sdkKeyNamed(t, admin, p.Key, "staging", key); k.Name != "storefront" || !slices.Equal(k.AllowedOrigins, []string{"https://shop.example"}) {
		t.Errorf("a refused update changed the key: %+v", k)
	}

	res := patch(`{"name": " shop ", "allowed_origins": ["HTTPS://WWW.Shop.Example", "http://localhost:80"]}`)
	if res.JSON200 == nil || res.JSON200.Name != "shop" ||
		!slices.Equal(res.JSON200.AllowedOrigins, []string{"https://www.shop.example", "http://localhost"}) {
		t.Errorf("updating = %d %s, want renamed with normalized origins", res.StatusCode(), res.Body)
	}
	res = patch(`{"allowed_origins": []}`)
	if res.JSON200 == nil || res.JSON200.Name != "shop" || len(res.JSON200.AllowedOrigins) != 0 {
		t.Errorf("lifting the restriction = %d %s", res.StatusCode(), res.Body)
	}
	res = patch(`{"allowed_origins": ["ftp://shop.example"]}`)
	if got := violationsOf(t, "a bad origin", res.StatusCode(), res.Body, "invalid_sdk_key"); !slices.Equal(got, []string{"/allowed_origins/0 invalid_origin"}) {
		t.Errorf("violations %v", got)
	}

	server := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"})
	res = do(admin.UpdateSdkKeyWithResponse(t.Context(), p.Key, "staging", server.Key.Id.String(),
		management.SdkKeyPatch{AllowedOrigins: []string{"https://shop.example"}}))
	if got := violationsOf(t, "origins on a server key", res.StatusCode(), res.Body, "invalid_sdk_key"); !slices.Equal(got,
		[]string{"/allowed_origins server_key_origins"}) {
		t.Errorf("violations %v", got)
	}

	// A change to the values the key has records nothing.
	res = patch(`{"name": "shop", "allowed_origins": []}`)
	updated := "sdk_key.updated"
	entries := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{Action: &updated}))
	if got := actions(t, "updates", entries); res.JSON200 == nil || len(got) != 2 {
		t.Errorf("an update to the same values = %d, updates recorded %v, want the two that changed something", res.StatusCode(), got)
	}
}

func TestSDKKeysOfAnotherEnvironmentOrProjectAreNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	k := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"})
	key := k.Key.Id.String()

	for name, call := range map[string]func() (int, []byte){
		"rotating through another environment": func() (int, []byte) {
			r := rotate(t, admin, p.Key, "production", key, nil)
			return r.StatusCode(), r.Body
		},
		"revoking through another project": func() (int, []byte) {
			r := revoke(t, admin, other.Key, "staging", key)
			return r.StatusCode(), r.Body
		},
		"updating through another environment": func() (int, []byte) {
			r := do(admin.UpdateSdkKeyWithResponse(t.Context(), p.Key, "development", key, management.SdkKeyPatch{AllowedOrigins: []string{}}))
			return r.StatusCode(), r.Body
		},
		"revoking an identifier that is none": func() (int, []byte) {
			r := revoke(t, admin, p.Key, "staging", "not-a-key")
			return r.StatusCode(), r.Body
		},
	} {
		status, body := call()
		assertProblem(t, name, status, body, http.StatusNotFound, "not_found")
	}
	if got := sdkKeyNamed(t, admin, p.Key, "staging", key); got.Status != management.SdkKeyStatusActive {
		t.Errorf("the key became %s", got.Status)
	}
}

func TestEverySDKKeyChangeIsAuditedWithoutItsValue(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "client", Name: "storefront"})
	key := k.Key.Id.String()
	week := int64(604_800)
	next := rotate(t, admin, p.Key, "staging", key, &week).JSON201
	do(admin.UpdateSdkKeyWithResponse(t.Context(), p.Key, "staging", key, management.SdkKeyPatch{AllowedOrigins: []string{"https://shop.example"}}))
	revoke(t, admin, p.Key, "staging", key)

	kind, id := "sdk_key", key
	res := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{EntityKind: &kind, EntityId: &id}))
	if got := actions(t, "the key's entries", res); !slices.Equal(got, []string{"sdk_key.revoked", "sdk_key.updated", "sdk_key.rotated", "sdk_key.created"}) {
		t.Fatalf("entries = %v", got)
	}
	for _, v := range []string{k.Value, next.Value} {
		if strings.Contains(string(res.Body), v[7:]) {
			t.Errorf("the audit log holds a key's value: %s", res.Body)
		}
	}
	staging := environmentID(t, admin, p.Key, "staging")
	for _, e := range res.JSON200.Items {
		if e.Entity.Key != k.Key.KeyId || e.EnvironmentId == nil || *e.EnvironmentId != staging {
			t.Errorf("%s: entity %+v in environment %v, want the key by its key ID in staging", e.Action, e.Entity, e.EnvironmentId)
		}
	}
	rotated := res.JSON200.Items[2]
	after, _ := json.Marshal(rotated.After)
	var detail struct {
		ReplacementID    string  `json:"replacement_id"`
		ReplacementKeyID string  `json:"replacement_key_id"`
		OverlapSeconds   int64   `json:"overlap_seconds"`
		ExpiresAt        *string `json:"expires_at"`
	}
	if err := json.Unmarshal(after, &detail); err != nil || detail.ReplacementID != next.Key.Id.String() ||
		detail.ReplacementKeyID != next.Key.KeyId || detail.OverlapSeconds != week || detail.ExpiresAt == nil {
		t.Errorf("sdk_key.rotated after = %s, want the replacement, the overlap and the expiry", after)
	}
}

func TestSDKKeysAreReadWithProjectReadAndManagedWithSDKKeyManageByTokensToo(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	create := func(c *management.ClientWithResponses) (int, []byte) {
		res := do(c.CreateSdkKeyWithResponse(t.Context(), p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "api"}))
		return res.StatusCode(), res.Body
	}

	viewer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleViewer))
	if res := do(viewer.ListSdkKeysWithResponse(t.Context(), p.Key, "staging")); res.JSON200 == nil {
		t.Errorf("a viewer listing = %d: %s", res.StatusCode(), res.Body)
	}
	status, body := create(viewer)
	if pr := assertProblem(t, "a viewer creating", status, body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
		*pr.Permission != "sdk_key.manage" {
		t.Errorf("the refusal names %v, want sdk_key.manage", pr.Permission)
	}
	provisioner := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(p), access.RoleMaintainer,
		access.ScopeRead, access.ScopeProjectAdmin))
	if created, createdBody := create(provisioner); created != http.StatusCreated {
		t.Errorf("a maintainer's project token with project:admin creating = %d: %s", created, createdBody)
	}
	flagsOnly := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(p), access.RoleOwner,
		access.ScopeRead, access.ScopeFlagsWrite))
	status, body = create(flagsOnly)
	assertProblem(t, "an owner's token without project:admin", status, body, http.StatusForbidden, "insufficient_scope")
}
