package scim_test

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// create provisions a user with body and returns what was answered.
func (e env) create(t *testing.T, token string, body map[string]any) reply {
	t.Helper()
	return e.call(t, token, http.MethodPost, "/Users", marshal(t, body))
}

// mustCreate provisions a user and returns its identifier.
func (e env) mustCreate(t *testing.T, token string, body map[string]any) string {
	t.Helper()
	r := e.create(t, token, body)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /Users = %d: %s", r.status, r.body)
	}
	return r.json(t)["id"].(string)
}

// member returns the member as an instance admin reads them.
func (e env) member(t *testing.T, member string) management.Member {
	t.Helper()
	res, err := e.admin.GetMemberWithResponse(t.Context(), member)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /members/%s = %v, %d: %s", member, err, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// audited returns the entries of action about member, newest first.
func (e env) audited(t *testing.T, action, member string) []management.AuditEntry {
	t.Helper()
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, EntityId: &member})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func userBody(userName string, more map[string]any) map[string]any {
	body := map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "userName": userName}
	for k, v := range more {
		body[k] = v
	}
	return body
}

func TestCreatingAUserProvisionsAnActiveMemberWithNoPassword(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	created := e.createToken(t, management.ScimTokenCreate{Provider: slug})
	address := email("example.com")

	r := e.create(t, created.Value, userBody(address, map[string]any{
		"externalId": "ext-1", "displayName": "Jo Doe", "active": true,
		"emails": []map[string]any{{"value": address, "primary": true, "type": "work"}},
	}))
	user := r.json(t)
	memberID, _ := user["id"].(string)
	if r.status != http.StatusCreated || r.header.Get("Location") != "/scim/v2/Users/"+memberID ||
		!strings.HasPrefix(r.header.Get("Content-Type"), "application/scim+json") {
		t.Fatalf("POST /Users = %d, Location %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if user["userName"] != address || user["displayName"] != "Jo Doe" || user["externalId"] != "ext-1" || user["active"] != true ||
		user["schemas"].([]any)[0] != "urn:ietf:params:scim:schemas:core:2.0:User" {
		t.Errorf("created user = %s, want %s named Jo Doe, externalId ext-1, active", r.body, address)
	}
	if emails := user["emails"].([]any); len(emails) != 1 || emails[0].(map[string]any)["value"] != address || emails[0].(map[string]any)["primary"] != true {
		t.Errorf("emails = %v, want the address as the primary one", user["emails"])
	}
	if groups, ok := user["groups"].([]any); !ok || len(groups) != 0 {
		t.Errorf("groups = %v, want an empty list", user["groups"])
	}
	if meta := user["meta"].(map[string]any); meta["resourceType"] != "User" || meta["location"] != "/scim/v2/Users/"+memberID {
		t.Errorf("meta = %v", meta)
	}

	m := e.member(t, memberID)
	if m.Email != address || m.Status != management.MemberStatusActive || m.Provider == nil || *m.Provider != slug || m.InstanceAdmin {
		t.Errorf("member = %+v, want active and provisioned by %s", m, slug)
	}
	// The provider's directory is the member's way in: there is no password.
	b := testkit.NewBrowser(t, e.srv.URL)
	res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: address, Password: "any-password-at-all-1234"})
	if err != nil || res.StatusCode() != http.StatusUnauthorized {
		t.Errorf("signing in with a password = %v, %d, want 401", err, res.StatusCode())
	}

	entries := e.audited(t, "member.provisioned", memberID)
	if len(entries) != 1 {
		t.Fatalf("%d member.provisioned entries, want 1", len(entries))
	}
	if a := entries[0].Actor; a.Kind != management.ActorKindScim || a.Id != created.Token.Id.String() || a.Name != created.Token.Hint {
		t.Errorf("actor = %+v, want the SCIM token %s named by its hint", a, created.Token.Hint)
	}
}

func TestAUsersNameIsItsDisplayNameElseItsGivenAndFamilyNameElseItsEmail(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	for name, c := range map[string]struct {
		body map[string]any
		want func(userName string) string
	}{
		"displayName":           {map[string]any{"displayName": "Jo D", "name": map[string]any{"givenName": "Jo", "familyName": "Doe"}}, func(string) string { return "Jo D" }},
		"given and family":      {map[string]any{"name": map[string]any{"givenName": "Jo", "familyName": "Doe"}}, func(string) string { return "Jo Doe" }},
		"the email":             {map[string]any{}, func(u string) string { return u }},
		"userName beats emails": {map[string]any{"emails": []map[string]any{{"value": "other@example.com", "primary": true}}}, func(u string) string { return u }},
	} {
		userName := email("example.com")
		r := e.create(t, token, userBody(userName, c.body))
		if got := r.json(t)["displayName"]; r.status != http.StatusCreated || got != c.want(userName) {
			t.Errorf("%s: displayName = %v (%d), want %q", name, got, r.status, c.want(userName))
		}
	}
	// With no userName the primary email is the member's email.
	primary := email("example.com")
	r := e.create(t, token, map[string]any{"emails": []map[string]any{
		{"value": email("example.com")}, {"value": primary, "primary": true},
	}})
	if r.status != http.StatusCreated || r.json(t)["userName"] != primary {
		t.Errorf("a user with emails alone = %d %s, want the primary email %s", r.status, r.body, primary)
	}
}

func TestCreatingAUserThatBreaksARuleIsRefused(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	taken := email("example.com")
	e.mustCreate(t, token, userBody(taken, nil))

	assertScimError(t, "a taken email", e.create(t, token, userBody(strings.ToUpper(taken), nil)), http.StatusConflict, "uniqueness")
	assertScimError(t, "no email", e.create(t, token, map[string]any{"displayName": "x"}), http.StatusBadRequest, "invalidValue")
	assertScimError(t, "an email that is none", e.create(t, token, userBody("not an email", nil)), http.StatusBadRequest, "invalidValue")
	assertScimError(t, "a name over the bound", e.create(t, token, userBody(email("example.com"), map[string]any{"displayName": strings.Repeat("x", 201)})),
		http.StatusBadRequest, "invalidValue")
	assertScimError(t, "an externalId with space around it", e.create(t, token, userBody(email("example.com"), map[string]any{"externalId": " x"})),
		http.StatusBadRequest, "invalidValue")
}

func TestAnExternalIDIsUniqueAmongAProvidersMembersAndFreeAtAnother(t *testing.T) {
	e := start(t)
	a, b := e.token(t, e.provider(t)), e.token(t, e.provider(t))
	e.mustCreate(t, a, userBody(email("example.com"), map[string]any{"externalId": "dup"}))

	assertScimError(t, "the same externalId at the provider", e.create(t, a, userBody(email("example.com"), map[string]any{"externalId": "dup"})),
		http.StatusConflict, "uniqueness")
	if r := e.create(t, b, userBody(email("example.com"), map[string]any{"externalId": "dup"})); r.status != http.StatusCreated {
		t.Errorf("the same externalId at another provider = %d, want 201: %s", r.status, r.body)
	}
	other := e.mustCreate(t, a, userBody(email("example.com"), nil))
	r := e.call(t, a, http.MethodPatch, "/Users/"+other, marshal(t, patch(op("replace", "externalId", "dup"))))
	assertScimError(t, "patching to a taken externalId", r, http.StatusConflict, "uniqueness")
}

func TestAnExistingMemberInTheProvidersDomainsIsAdopted(t *testing.T) {
	e := start(t)
	slug := e.provider(t, "example.com", "example.org")
	created := e.createToken(t, management.ScimTokenCreate{Provider: slug})
	existing := testkit.NewMember(t, testkit.Pool(t), testkit.WithEmail(email("example.org")), testkit.WithDisplayName("Old Name"))

	r := e.create(t, created.Value, userBody(strings.ToUpper(existing.Email), map[string]any{"externalId": "ext-adopt", "displayName": "New Name"}))
	if r.status != http.StatusCreated || r.json(t)["id"] != existing.ID.String() {
		t.Fatalf("POST /Users of a member the instance holds = %d: %s, want the member %s adopted", r.status, r.body, existing.ID)
	}
	m := e.member(t, existing.ID.String())
	if m.Provider == nil || *m.Provider != slug || m.DisplayName != "New Name" || m.Status != management.MemberStatusActive {
		t.Errorf("adopted member = %+v, want named by the directory and provisioned by %s", m, slug)
	}
	if got := e.audited(t, "member.adopted", existing.ID.String()); len(got) != 1 || got[0].Actor.Kind != management.ActorKindScim {
		t.Errorf("member.adopted entries = %+v, want one by the SCIM token", got)
	}
	if len(e.audited(t, "member.provisioned", existing.ID.String())) != 0 {
		t.Error("an adopted member was also recorded as provisioned")
	}
	// The member's password stays: adoption changes who decides their email,
	// name and status, not how they have been signing in.
	b := testkit.NewBrowser(t, e.srv.URL)
	if res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: m.Email, Password: existing.Password}); err != nil || res.JSON200 == nil {
		t.Errorf("signing in with the password an adopted member kept = %v, %d", err, res.StatusCode())
	}
}

func TestAMemberNoDirectoryMayTakeOverIsAUniquenessConflict(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	otherToken := e.token(t, e.provider(t, "example.net"))
	pool := testkit.Pool(t)

	elsewhere := testkit.NewMember(t, pool, testkit.WithEmail(email("example.net")))
	admin := testkit.NewMember(t, pool, testkit.AsInstanceAdmin(), testkit.WithEmail(email("example.com")))
	invited := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberInvited), testkit.WithEmail(email("example.com")))
	deactivated := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberDeactivated), testkit.WithEmail(email("example.com")))
	provisioned := email("example.com")
	e.mustCreate(t, otherToken, userBody(provisioned, nil)) // another provider's, and not in its domains: created, not adopted

	for name, address := range map[string]string{
		"an email outside the provider's domains": elsewhere.Email,
		"an instance admin":                       admin.Email,
		"an invited member":                       invited.Email,
		"a deactivated member":                    deactivated.Email,
		"a member another provider provisions":    provisioned,
	} {
		assertScimError(t, name, e.create(t, token, userBody(address, nil)), http.StatusConflict, "uniqueness")
	}
	for _, m := range []testkit.Member{elsewhere, admin, invited, deactivated} {
		if got := e.member(t, m.ID.String()); got.Provider != nil {
			t.Errorf("member %s was provisioned (%v) by a refused request", got.Email, *got.Provider)
		}
	}
}

func TestListingFiltersAndPagesOnlyTheProvidersUsers(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	other := e.token(t, e.provider(t))
	var users []map[string]any
	for i := range 3 {
		address := email("example.com")
		id := e.mustCreate(t, token, userBody(address, map[string]any{"externalId": "ext-" + random(), "displayName": "Person " + strconv.Itoa(i)}))
		users = append(users, map[string]any{"id": id, "userName": address, "displayName": "Person " + strconv.Itoa(i)})
	}
	e.mustCreate(t, other, userBody(email("example.com"), nil))

	list := func(query string) map[string]any {
		t.Helper()
		r := e.call(t, token, http.MethodGet, "/Users?"+query, "")
		if r.status != http.StatusOK {
			t.Fatalf("GET /Users?%s = %d: %s", query, r.status, r.body)
		}
		return r.json(t)
	}
	if doc := list(""); doc["totalResults"] != 3.0 || len(doc["Resources"].([]any)) != 3 || doc["startIndex"] != 1.0 || doc["itemsPerPage"] != 3.0 {
		t.Errorf("an unfiltered list = %v, want the provider's three", doc)
	}
	for name, c := range map[string]struct {
		filter string
		want   int
	}{
		"userName":                  {`userName eq "` + users[1]["userName"].(string) + `"`, 1},
		"userName in other case":    {`userName eq "` + strings.ToUpper(users[1]["userName"].(string)) + `"`, 1},
		"emails.value":              {`emails.value eq "` + users[0]["userName"].(string) + `"`, 1},
		"id":                        {`id eq "` + users[2]["id"].(string) + `"`, 1},
		"displayName":               {`displayName eq "Person 1"`, 1},
		"and":                       {`userName eq "` + users[0]["userName"].(string) + `" and displayName eq "Person 0"`, 1},
		"an and that excludes":      {`userName eq "` + users[0]["userName"].(string) + `" and displayName eq "Person 1"`, 0},
		"an unknown value":          {`userName eq "nobody@example.com"`, 0},
		"an id that is none":        {`id eq "not-a-uuid"`, 0},
		"a quote in the value":      {`displayName eq "Person \"1\""`, 0},
		"another provider's member": {`userName eq "` + email("example.com") + `"`, 0},
	} {
		if doc := list("filter=" + escape(c.filter)); doc["totalResults"] != float64(c.want) || len(doc["Resources"].([]any)) != c.want {
			t.Errorf("filter %s (%s) = %v, want %d", name, c.filter, doc, c.want)
		}
	}
	for name, filter := range map[string]string{
		"contains": `userName co "a"`, "or": `userName eq "a" or userName eq "b"`, "not": `not (userName eq "a")`,
		"present": `externalId pr`, "an unknown attribute": `nickName eq "x"`, "a malformed filter": `userName eq`,
		"a value path": `emails[type eq "work"].value eq "a"`,
	} {
		assertScimError(t, name, e.call(t, token, http.MethodGet, "/Users?filter="+escape(filter), ""), http.StatusBadRequest, "invalidFilter")
	}

	first, second := list("startIndex=1&count=2"), list("startIndex=3&count=2")
	if first["totalResults"] != 3.0 || len(first["Resources"].([]any)) != 2 || first["itemsPerPage"] != 2.0 ||
		second["startIndex"] != 3.0 || len(second["Resources"].([]any)) != 1 {
		t.Errorf("pages = %v and %v, want two users and then the third of three", first, second)
	}
	seen := map[any]bool{}
	for _, doc := range []map[string]any{first, second} {
		for _, u := range doc["Resources"].([]any) {
			seen[u.(map[string]any)["id"]] = true
		}
	}
	if len(seen) != 3 {
		t.Errorf("the pages hold %d distinct users, want 3", len(seen))
	}
	if doc := list("count=0"); doc["totalResults"] != 3.0 || len(doc["Resources"].([]any)) != 0 || doc["itemsPerPage"] != 0.0 {
		t.Errorf("count=0 = %v, want the total and no resource", doc)
	}
	if doc := list("startIndex=0&count=1000"); doc["startIndex"] != 1.0 || len(doc["Resources"].([]any)) != 3 {
		t.Errorf("startIndex=0&count=1000 = %v, want the first page", doc)
	}
	if doc := list("startIndex=50"); doc["totalResults"] != 3.0 || len(doc["Resources"].([]any)) != 0 {
		t.Errorf("a page past the end = %v, want none of three", doc)
	}
}

func TestReplacingAUserReplacesItsEmailNameAndExternalID(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	before := email("example.com")
	member := e.mustCreate(t, token, userBody(before, map[string]any{"displayName": "Before", "externalId": "ext-put"}))
	after := email("example.com")

	r := e.call(t, token, http.MethodPut, "/Users/"+member, marshal(t, userBody(after, map[string]any{
		"displayName": "After", "externalId": "ext-put-2", "active": true,
	})))
	doc := r.json(t)
	if r.status != http.StatusOK || doc["userName"] != after || doc["displayName"] != "After" || doc["externalId"] != "ext-put-2" || doc["id"] != member {
		t.Fatalf("PUT = %d %s, want the new email, name and externalId", r.status, r.body)
	}
	if m := e.member(t, member); m.Email != after || m.DisplayName != "After" {
		t.Errorf("member = %+v, want the replaced email and name", m)
	}
	updated := e.audited(t, "member.updated", member)
	if len(updated) != 1 || updated[0].Actor.Kind != management.ActorKindScim {
		t.Fatalf("member.updated entries = %+v, want one by the SCIM token", updated)
	}
	if was, _ := updated[0].Before.(map[string]any); was["email"] != before || was["display_name"] != "Before" || was["external_id"] != "ext-put" {
		t.Errorf("before = %v", updated[0].Before)
	}

	// Replacing with what is there records nothing; an absent externalId leaves it.
	e.call(t, token, http.MethodPut, "/Users/"+member, marshal(t, userBody(after, map[string]any{"displayName": "After"})))
	if again := e.audited(t, "member.updated", member); len(again) != 1 {
		t.Errorf("replacing a user with itself recorded %d member.updated entries, want 1", len(again))
	}
	if got := e.call(t, token, http.MethodGet, "/Users/"+member, "").json(t); got["externalId"] != "ext-put-2" {
		t.Errorf("externalId = %v after a PUT without one, want it kept", got["externalId"])
	}

	taken := email("example.com")
	e.mustCreate(t, token, userBody(taken, nil))
	assertScimError(t, "an email another member holds", e.call(t, token, http.MethodPut, "/Users/"+member, marshal(t, userBody(taken, nil))),
		http.StatusConflict, "uniqueness")
	assertScimError(t, "no email", e.call(t, token, http.MethodPut, "/Users/"+member, `{"displayName":"x"}`), http.StatusBadRequest, "invalidValue")
	assertScimError(t, "an unknown user", e.call(t, token, http.MethodPut, "/Users/"+newUUID(), marshal(t, userBody(after, nil))), http.StatusNotFound, "")
}

func TestPatchingAUserAddsReplacesAndRemoves(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	address := email("example.com")
	member := e.mustCreate(t, token, userBody(address, map[string]any{"displayName": "Has A Name", "externalId": "ext-patch"}))
	patchOf := func(ops ...map[string]any) reply {
		return e.call(t, token, http.MethodPatch, "/Users/"+member, marshal(t, patch(ops...)))
	}

	moved := email("example.com")
	if r := patchOf(op("replace", "userName", moved)); r.status != http.StatusOK || r.json(t)["userName"] != moved {
		t.Fatalf("replace userName = %d %s", r.status, r.body)
	}
	moved2 := email("example.com")
	if r := patchOf(op("replace", "emails", []map[string]any{{"value": moved2, "primary": true}})); r.json(t)["userName"] != moved2 {
		t.Fatalf("replace emails = %d %s", r.status, r.body)
	}
	if r := patchOf(op("remove", "displayName", nil)); r.json(t)["displayName"] != moved2 {
		t.Errorf("remove displayName = %s, want the email", r.body)
	}
	if r := patchOf(op("add", "displayName", "Added")); r.json(t)["displayName"] != "Added" {
		t.Errorf("add displayName = %s", r.body)
	}
	if r := patchOf(op("remove", "externalId", nil)); r.status != http.StatusOK || r.json(t)["externalId"] != nil {
		t.Errorf("remove externalId = %d %s, want none", r.status, r.body)
	}
	r := patchOf(op("replace", "", map[string]any{"displayName": "Many", "externalId": "ext-many"}))
	if doc := r.json(t); doc["displayName"] != "Many" || doc["externalId"] != "ext-many" {
		t.Errorf("replace without a path = %s", r.body)
	}
	if r := patchOf(op("replace", "name.givenName", "Jo"), op("replace", "title", "x")); r.status != http.StatusBadRequest {
		t.Errorf("an unmapped attribute after a mapped one = %d, want 400: %s", r.status, r.body)
	}
	if got := e.call(t, token, http.MethodGet, "/Users/"+member, "").json(t); got["displayName"] != "Many" {
		t.Errorf("a refused patch changed the user: %v", got)
	}

	for name, c := range map[string]struct {
		ops  []map[string]any
		want string
	}{
		"an unknown op":          {[]map[string]any{op("move", "active", true)}, "invalidSyntax"},
		"remove without a path":  {[]map[string]any{{"op": "remove"}}, "noTarget"},
		"active is a boolean":    {[]map[string]any{op("replace", "active", 5)}, "invalidValue"},
		"userName not removable": {[]map[string]any{op("remove", "userName", nil)}, "invalidValue"},
		"groups are read only":   {[]map[string]any{op("add", "groups", []any{})}, "mutability"},
		"an unmapped attribute":  {[]map[string]any{op("replace", "title", "x")}, "noTarget"},
		"a taken email":          {[]map[string]any{op("replace", "userName", address)}, ""},
	} {
		if c.want == "" {
			e.mustCreate(t, token, userBody(address, nil))
			c.want = "uniqueness"
			assertScimError(t, name, patchOf(c.ops...), http.StatusConflict, c.want)
			continue
		}
		assertScimError(t, name, patchOf(c.ops...), http.StatusBadRequest, c.want)
	}
	assertScimError(t, "no operations", e.call(t, token, http.MethodPatch, "/Users/"+member, `{"Operations":[]}`), http.StatusBadRequest, "invalidValue")
}

func TestDeactivatingAUserEndsItsTokensAndResetsAndReactivatingRestoresIt(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	member := e.mustCreate(t, token, userBody(email("example.com"), nil))
	memberID, err := id.Parse[access.Member](member)
	if err != nil {
		t.Fatal(err)
	}
	personal := testkit.Client(t, e.srv, testkit.PersonalToken(t, testkit.Pool(t), memberID, "script"))
	reset := do(e.admin.IssuePasswordResetWithResponse(t.Context(), member))
	if reset.JSON201 == nil {
		t.Fatalf("issuing a reset = %d: %s", reset.StatusCode(), reset.Body)
	}
	if res, err := personal.GetMeWithResponse(t.Context()); err != nil || res.JSON200 == nil {
		t.Fatalf("the member's token before = %v, %d", err, res.StatusCode())
	}

	// Okta's deactivation: no path, an object.
	r := e.call(t, token, http.MethodPatch, "/Users/"+member, marshal(t, patch(map[string]any{"op": "replace", "value": map[string]any{"active": false}})))
	if r.status != http.StatusOK || r.json(t)["active"] != false {
		t.Fatalf("PATCH active=false = %d: %s", r.status, r.body)
	}
	if m := e.member(t, member); m.Status != management.MemberStatusDeactivated {
		t.Errorf("member status = %s, want deactivated", m.Status)
	}
	res, err := personal.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "the member's token", res.StatusCode(), res.Body, http.StatusUnauthorized, "token_revoked")
	got := do(e.admin.GetPasswordResetWithResponse(t.Context(), secretOfLink(reset.JSON201.Url)))
	assertProblem(t, "the member's reset link", got.StatusCode(), got.Body, http.StatusGone, "reset_gone")
	if entries := e.audited(t, "member.deactivated", member); len(entries) != 1 || entries[0].Actor.Kind != management.ActorKindScim {
		t.Errorf("member.deactivated entries = %+v, want one by the SCIM token", entries)
	}

	// Deactivating again changes nothing.
	e.call(t, token, http.MethodPatch, "/Users/"+member, marshal(t, patch(op("replace", "active", "False"))))
	if len(e.audited(t, "member.deactivated", member)) != 1 {
		t.Error("deactivating a deactivated member recorded another entry")
	}

	r = e.call(t, token, http.MethodPatch, "/Users/"+member, marshal(t, patch(op("replace", "active", true))))
	if r.status != http.StatusOK || r.json(t)["active"] != true {
		t.Fatalf("PATCH active=true = %d: %s", r.status, r.body)
	}
	if m := e.member(t, member); m.Status != management.MemberStatusActive {
		t.Errorf("member status = %s, want active", m.Status)
	}
	if entries := e.audited(t, "member.reactivated", member); len(entries) != 1 {
		t.Errorf("%d member.reactivated entries, want 1", len(entries))
	}
	// What the deactivation revoked stays revoked.
	res, err = personal.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "the member's token after reactivation", res.StatusCode(), res.Body, http.StatusUnauthorized, "token_revoked")
}

func TestDeletingAUserDeactivatesItAndKeepsTheRow(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	member := e.mustCreate(t, token, userBody(email("example.com"), nil))

	r := e.call(t, token, http.MethodDelete, "/Users/"+member, "")
	if r.status != http.StatusNoContent || len(r.body) != 0 {
		t.Fatalf("DELETE = %d: %s", r.status, r.body)
	}
	if m := e.member(t, member); m.Status != management.MemberStatusDeactivated || m.Provider == nil {
		t.Errorf("member after DELETE = %+v, want deactivated and still provisioned", m)
	}
	if got := e.call(t, token, http.MethodGet, "/Users/"+member, "").json(t); got["active"] != false {
		t.Errorf("GET after DELETE = %v, want inactive", got)
	}
	if r := e.call(t, token, http.MethodDelete, "/Users/"+member, ""); r.status != http.StatusNoContent {
		t.Errorf("DELETE again = %d, want 204", r.status)
	}
	assertScimError(t, "an unknown user", e.call(t, token, http.MethodDelete, "/Users/"+newUUID(), ""), http.StatusNotFound, "")
}

func TestAProvidersTokenReachesOnlyItsOwnMembers(t *testing.T) {
	e := start(t)
	a, b := e.token(t, e.provider(t)), e.token(t, e.provider(t))
	ofA := e.mustCreate(t, a, userBody(email("example.com"), map[string]any{"externalId": "shared"}))
	address := e.call(t, a, http.MethodGet, "/Users/"+ofA, "").json(t)["userName"].(string)
	before := e.member(t, ofA)

	for name, r := range map[string]reply{
		"GET":    e.call(t, b, http.MethodGet, "/Users/"+ofA, ""),
		"PUT":    e.call(t, b, http.MethodPut, "/Users/"+ofA, marshal(t, userBody(email("example.com"), nil))),
		"PATCH":  e.call(t, b, http.MethodPatch, "/Users/"+ofA, marshal(t, patch(op("replace", "active", false)))),
		"DELETE": e.call(t, b, http.MethodDelete, "/Users/"+ofA, ""),
	} {
		assertScimError(t, name+" of another provider's user", r, http.StatusNotFound, "")
	}
	if after := e.member(t, ofA); !reflect.DeepEqual(after, before) {
		t.Errorf("member = %+v after another provider's attempts, was %+v", after, before)
	}
	for _, filter := range []string{`userName eq "` + address + `"`, `id eq "` + ofA + `"`, `externalId eq "shared"`} {
		if doc := e.call(t, b, http.MethodGet, "/Users?filter="+escape(filter), "").json(t); doc["totalResults"] != 0.0 {
			t.Errorf("another provider found the user with %s: %v", filter, doc)
		}
	}
	// The email belongs to a member B's directory does not provision.
	assertScimError(t, "creating another provider's email", e.create(t, b, userBody(address, nil)), http.StatusConflict, "uniqueness")
	// A member nobody provisions is no SCIM user either.
	byHand := testkit.NewMember(t, testkit.Pool(t))
	assertScimError(t, "a member made by hand", e.call(t, a, http.MethodGet, "/Users/"+byHand.ID.String(), ""), http.StatusNotFound, "")
}

func TestManagementRefusesToChangeWhatTheDirectoryDecides(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	address := email("example.com")
	member := e.mustCreate(t, token, userBody(address, map[string]any{"displayName": "Directory Name"}))

	for name, patch := range map[string]management.MemberPatch{
		"an email": {Email: ptr(email("example.com"))}, "a name": {DisplayName: ptr("By Hand")},
	} {
		res, err := e.admin.UpdateMemberWithResponse(t.Context(), member, patch)
		if err != nil {
			t.Fatal(err)
		}
		assertProblem(t, "editing "+name, res.StatusCode(), res.Body, http.StatusConflict, "member_provisioned")
	}
	// What is already so is no change.
	if res, err := e.admin.UpdateMemberWithResponse(t.Context(), member, management.MemberPatch{Email: &address, DisplayName: ptr("Directory Name")}); err != nil || res.JSON200 == nil {
		t.Errorf("editing nothing = %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	deactivate, err := e.admin.DeactivateMemberWithResponse(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "deactivating", deactivate.StatusCode(), deactivate.Body, http.StatusConflict, "member_provisioned")
	e.call(t, token, http.MethodDelete, "/Users/"+member, "")
	reactivate, err := e.admin.ReactivateMemberWithResponse(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "reactivating", reactivate.StatusCode(), reactivate.Body, http.StatusConflict, "member_provisioned")

	e.call(t, token, http.MethodPatch, "/Users/"+member, marshal(t, patch(op("replace", "active", true))))
	// Access granted by hand stays possible: the directory decides who the
	// member is, not what they may do.
	if res, err := e.admin.UpdateMemberWithResponse(t.Context(), member, management.MemberPatch{InstanceAdmin: ptr(true)}); err != nil || res.JSON200 == nil {
		t.Errorf("promoting a provisioned member = %v, %d: %s", err, res.StatusCode(), res.Body)
	}
}

func TestAProviderWithTokensOrProvisionedMembersIsNotDeleted(t *testing.T) {
	e := start(t)
	remove := func(slug string) *management.DeleteAuthProviderResponse {
		res, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), slug)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	withToken := e.provider(t)
	created := e.createToken(t, management.ScimTokenCreate{Provider: withToken})
	res := remove(withToken)
	p := assertProblem(t, "a provider with a token", res.StatusCode(), res.Body, http.StatusConflict, "provider_in_use")
	if p.UsedBy == nil || !strings.Contains(strings.Join(*p.UsedBy, ","), "SCIM tokens") {
		t.Errorf("used_by = %v, want SCIM tokens", p.UsedBy)
	}
	if r, err := e.browser.RevokeScimTokenWithResponse(t.Context(), created.Token.Id.String()); err != nil || r.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoking: %v, %d", err, r.StatusCode())
	}
	if res := remove(withToken); res.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting a provider whose tokens are revoked = %d, want 204: %s", res.StatusCode(), res.Body)
	}

	withMember := e.provider(t)
	token := e.token(t, withMember)
	e.mustCreate(t, token, userBody(email("example.com"), nil))
	list, _ := e.browser.ListScimTokensWithResponse(t.Context())
	for _, tok := range list.JSON200.Items {
		if tok.Provider == withMember {
			if r, err := e.browser.RevokeScimTokenWithResponse(t.Context(), tok.Id.String()); err != nil || r.StatusCode() != http.StatusNoContent {
				t.Fatalf("revoking: %v", err)
			}
		}
	}
	res = remove(withMember)
	p = assertProblem(t, "a provider with a provisioned member", res.StatusCode(), res.Body, http.StatusConflict, "provider_in_use")
	if p.UsedBy == nil || !strings.Contains(strings.Join(*p.UsedBy, ","), "provisioned members") {
		t.Errorf("used_by = %v, want provisioned members", p.UsedBy)
	}
}

// ----- Helpers of the bodies connectors send.

func op(name, path string, value any) map[string]any {
	o := map[string]any{"op": name, "path": path}
	if value != nil {
		o["value"] = value
	}
	return o
}

func patch(ops ...map[string]any) map[string]any {
	return map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, "Operations": ops}
}

var _ = scim.BaseURL
