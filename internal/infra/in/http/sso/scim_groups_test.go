package sso_test

import (
	"net/http"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// patchGroup applies ops to group through the SCIM client c, failing the test
// unless it is answered 200.
func patchGroup(t *testing.T, c *scim.ClientWithResponses, group openapi_types.UUID, ops ...scim.PatchOperation) {
	t.Helper()
	res, err := c.PatchGroupWithApplicationScimPlusJSONBodyWithResponse(t.Context(), group, scim.PatchOp{Operations: ops})
	if err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("PATCH /Groups/%s: %v, %d: %s", group, err, res.StatusCode(), res.Body)
	}
}

func membersValue(members ...openapi_types.UUID) []map[string]any {
	out := make([]map[string]any, len(members))
	for i, m := range members {
		out[i] = map[string]any{"value": m.String()}
	}
	return out
}

func TestASignInAndTheProvidersSCIMGroupsKeepEachOthersGrants(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision, p.linkByEmail = false, false })
	alpha, beta, gamma := e.project(t), e.project(t), e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"), projectMapping("ops", beta, "viewer"))
	directory := e.scimClient(t, slug)

	address := uniqueEmail()
	created, err := directory.CreateUserWithApplicationScimPlusJSONBodyWithResponse(t.Context(), scim.User{UserName: &address, ExternalId: ptr("sub-lee")})
	if err != nil || created.ApplicationscimJSON201 == nil {
		t.Fatalf("provisioning: %v, %d: %s", err, created.StatusCode(), created.Body)
	}
	lee := *created.ApplicationscimJSON201.Id
	signIn := func(claims map[string]any) {
		t.Helper()
		all := map[string]any{"sub": "sub-lee", "email": address}
		for k, v := range claims {
			all[k] = v
		}
		idp.NextClaims(all)
		b := testkit.NewBrowser(t, e.srv.URL)
		if v := e.signIn(t, b, slug, "", e.opts()); v.URL.Path != "/" {
			t.Fatalf("the sign-in ended at %s", v.URL)
		}
	}

	group, err := directory.CreateGroupWithApplicationScimPlusJSONBodyWithResponse(t.Context(), scim.Group{
		DisplayName: ptr("eng"), Members: &[]scim.ResourceRef{{Value: lee.String()}},
	})
	if err != nil || group.ApplicationscimJSON201 == nil {
		t.Fatalf("creating the group: %v, %d: %s", err, group.StatusCode(), group.Body)
	}
	eng := *group.ApplicationscimJSON201.Id
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")

	// A sign-in claiming ops keeps what the SCIM group grants.
	signIn(map[string]any{"groups": []string{"ops"}})
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, beta, lee, management.GrantSourceSso, slug, "viewer")

	// Leaving the SCIM group keeps what the claim grants.
	patchGroup(t, directory, eng, scim.PatchOperation{Op: "remove", Path: ptr("members")})
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug)
	e.wantGrants(t, beta, lee, management.GrantSourceSso, slug, "viewer")

	// A sign-in without a groups claim changes nothing; an empty claim keeps
	// what the SCIM group grants and takes away what the claim did.
	patchGroup(t, directory, eng, scim.PatchOperation{Op: "add", Path: ptr("members"), Value: membersValue(lee)})
	signIn(nil)
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, beta, lee, management.GrantSourceSso, slug, "viewer")
	signIn(map[string]any{"groups": []string{}})
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, beta, lee, management.GrantSourceSso, slug)

	// A claim of eng keeps eng's grant while the SCIM group empties.
	signIn(map[string]any{"groups": []string{"eng"}})
	patchGroup(t, directory, eng, scim.PatchOperation{Op: "replace", Path: ptr("members"), Value: []any{}})
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")

	// Editing the mappings runs them from both: the claim (eng) and the SCIM
	// group, joined again, under its new name.
	patchGroup(t, directory, eng,
		scim.PatchOperation{Op: "replace", Path: ptr("displayName"), Value: "release"},
		scim.PatchOperation{Op: "add", Path: ptr("members"), Value: membersValue(lee)})
	run := e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"), projectMapping("release", gamma, "maintainer"))
	if run.Granted != 1 || run.Revoked != 0 {
		t.Errorf("the edit = %+v, want the release grant made", run)
	}
	e.wantGrants(t, alpha, lee, management.GrantSourceSso, slug, "developer")
	e.wantGrants(t, gamma, lee, management.GrantSourceSso, slug, "maintainer")
}
