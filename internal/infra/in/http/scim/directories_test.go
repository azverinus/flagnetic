package scim_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

const (
	patchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	enterprise    = "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"
)

// The requests Microsoft Entra ID sends to provision one user, in the order
// it sends them, with the bodies it sends: every one a test of what a
// connector expects of this server.
func TestEntraIDProvisionsUpdatesAndDeprovisionsAUser(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	userName, objectID := email("example.com"), "5f2f4a7e-0c0a-4d6e-9d19-2b5c3d9a7e11"

	// 1. Does the user exist already? It looks it up by the unique attribute
	// it matches on (userName here, externalId for others).
	filter := url.QueryEscape(`userName eq "` + userName + `"`)
	if doc := e.call(t, token, http.MethodGet, "/Users?filter="+filter, "").json(t); doc["totalResults"] != 0.0 {
		t.Fatalf("the user exists before it is created: %v", doc)
	}

	// 2. It creates the user.
	created := e.call(t, token, http.MethodPost, "/Users", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User", "`+enterprise+`"],
		"externalId": "`+objectID+`",
		"userName": "`+userName+`",
		"active": true,
		"emails": [{"type": "work", "value": "`+userName+`", "primary": true}],
		"meta": {"resourceType": "User"},
		"name": {"formatted": "Jo Doe", "familyName": "Doe", "givenName": "Jo"},
		"displayName": "Jo Doe",
		"`+enterprise+`": {"department": "Engineering", "manager": {"value": "x"}}
	}`)
	user := created.json(t)
	member, _ := user["id"].(string)
	if created.status != http.StatusCreated || user["userName"] != userName || user["displayName"] != "Jo Doe" || user["externalId"] != objectID || user["active"] != true {
		t.Fatalf("create = %d: %s", created.status, created.body)
	}

	// 3. It reads the user back by the id the server gave it, and by the
	// external ID it matches on.
	if r := e.call(t, token, http.MethodGet, "/Users/"+member, ""); r.status != http.StatusOK || r.json(t)["userName"] != userName {
		t.Fatalf("get = %d: %s", r.status, r.body)
	}
	byExternal := url.QueryEscape(`externalId eq "` + objectID + `"`)
	if doc := e.call(t, token, http.MethodGet, "/Users?filter="+byExternal, "").json(t); doc["totalResults"] != 1.0 {
		t.Errorf("by externalId = %v, want the user", doc)
	}

	// 4. A change in the directory: name, work email and the given name, in
	// the capitalised operations it sends.
	moved := email("example.com")
	updated := e.call(t, token, http.MethodPatch, "/Users/"+member, `{
		"schemas": ["`+patchOpSchema+`"],
		"Operations": [
			{"op": "Replace", "path": "displayName", "value": "Jo Q. Doe"},
			{"op": "Add", "path": "name.givenName", "value": "Johnny"},
			{"op": "Replace", "path": "emails[type eq \"work\"].value", "value": "`+moved+`"},
			{"op": "Replace", "path": "userName", "value": "`+moved+`"}
		]
	}`)
	if doc := updated.json(t); updated.status != http.StatusOK || doc["displayName"] != "Jo Q. Doe" || doc["userName"] != moved {
		t.Fatalf("patch = %d: %s", updated.status, updated.body)
	}

	// 5. The user leaves the directory's scope: active becomes the string
	// "False", then "True" when they are back.
	off := e.call(t, token, http.MethodPatch, "/Users/"+member, `{"schemas": ["`+patchOpSchema+`"], "Operations": [{"op": "Replace", "path": "active", "value": "False"}]}`)
	if off.status != http.StatusOK || off.json(t)["active"] != false {
		t.Fatalf("patch active=False = %d: %s", off.status, off.body)
	}
	if m := e.member(t, member); m.Status != management.MemberStatusDeactivated || m.Email != moved || m.DisplayName != "Jo Q. Doe" {
		t.Errorf("member = %+v, want deactivated with the new email and name", m)
	}
	on := e.call(t, token, http.MethodPatch, "/Users/"+member, `{"schemas": ["`+patchOpSchema+`"], "Operations": [{"op": "Replace", "path": "active", "value": "True"}]}`)
	if on.status != http.StatusOK || on.json(t)["active"] != true {
		t.Fatalf("patch active=True = %d: %s", on.status, on.body)
	}

	// 6. It deletes the user.
	if r := e.call(t, token, http.MethodDelete, "/Users/"+member, ""); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", r.status, r.body)
	}
	if m := e.member(t, member); m.Status != management.MemberStatusDeactivated {
		t.Errorf("member after delete = %+v, want deactivated", m)
	}
}

// The requests Okta sends to provision one user: create after a lookup, a
// full update, and deactivation by a patch without a path.
func TestOktaProvisionsUpdatesAndDeprovisionsAUser(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	userName := email("example.com")

	// 1. It looks the user up.
	filter := url.QueryEscape(`userName eq "` + userName + `"`)
	if doc := e.call(t, token, http.MethodGet, "/Users?startIndex=1&count=100&filter="+filter, "").json(t); doc["totalResults"] != 0.0 {
		t.Fatalf("the user exists before it is created: %v", doc)
	}

	// 2. It creates the user.
	created := e.call(t, token, http.MethodPost, "/Users", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName": "`+userName+`",
		"name": {"givenName": "Jo", "familyName": "Doe"},
		"emails": [{"primary": true, "value": "`+userName+`", "type": "work"}],
		"displayName": "Jo Doe",
		"active": true,
		"password": "ignored-by-this-server"
	}`)
	member, _ := created.json(t)["id"].(string)
	if created.status != http.StatusCreated || created.json(t)["displayName"] != "Jo Doe" {
		t.Fatalf("create = %d: %s", created.status, created.body)
	}

	// 3. It looks the user up again, now by userName, and by id.
	if doc := e.call(t, token, http.MethodGet, "/Users?startIndex=1&count=1&filter="+filter, "").json(t); doc["totalResults"] != 1.0 {
		t.Errorf("by userName = %v, want the user", doc)
	}
	if r := e.call(t, token, http.MethodGet, "/Users/"+member, ""); r.status != http.StatusOK {
		t.Fatalf("get = %d: %s", r.status, r.body)
	}

	// 4. A profile change arrives as a full update.
	renamed := email("example.com")
	replaced := e.call(t, token, http.MethodPut, "/Users/"+member, `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"id": "`+member+`",
		"userName": "`+renamed+`",
		"name": {"givenName": "Josephine", "familyName": "Doe"},
		"emails": [{"primary": true, "value": "`+renamed+`", "type": "work"}],
		"displayName": "Josephine Doe",
		"active": true
	}`)
	if doc := replaced.json(t); replaced.status != http.StatusOK || doc["userName"] != renamed || doc["displayName"] != "Josephine Doe" || doc["id"] != member {
		t.Fatalf("put = %d: %s", replaced.status, replaced.body)
	}

	// 5. Deactivation and reactivation: a patch with no path and an object.
	for active, want := range map[bool]management.MemberStatus{false: management.MemberStatusDeactivated, true: management.MemberStatusActive} {
		value := "false"
		if active {
			value = "true"
		}
		r := e.call(t, token, http.MethodPatch, "/Users/"+member, `{
			"schemas": ["`+patchOpSchema+`"], "Operations": [{"op": "replace", "value": {"active": `+value+`}}]
		}`)
		if r.status != http.StatusOK || r.json(t)["active"] != active {
			t.Fatalf("patch active=%s = %d: %s", value, r.status, r.body)
		}
		if m := e.member(t, member); m.Status != want {
			t.Errorf("member after active=%s = %s, want %s", value, m.Status, want)
		}
	}
}
