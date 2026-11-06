package identity_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

func scimType(err error) string {
	var e *identity.ScimError
	if errors.As(err, &e) {
		return e.Type
	}
	return ""
}

func TestAUserFilterIsEqualitiesJoinedByAnd(t *testing.T) {
	for raw, want := range map[string][]identity.UserCondition{
		``:                                 nil,
		`userName eq "jo@example.com"`:     {{identity.AttrEmail, "jo@example.com"}},
		`USERNAME EQ "Jo@Example.com"`:     {{identity.AttrEmail, "Jo@Example.com"}},
		`emails.value eq "jo@example.com"`: {{identity.AttrEmail, "jo@example.com"}},
		`externalId eq "ext-1"`:            {{identity.AttrExternalID, "ext-1"}},
		`displayName eq "Jo \"J\" Doe"`:    {{identity.AttrDisplayName, `Jo "J" Doe`}},
		`id eq "8f2d"`:                     {{identity.AttrID, "8f2d"}},
		`urn:ietf:params:scim:schemas:core:2.0:User:userName eq "jo@example.com"`: {{identity.AttrEmail, "jo@example.com"}},
		`userName eq "a@example.com" and externalId eq "x"`:                       {{identity.AttrEmail, "a@example.com"}, {identity.AttrExternalID, "x"}},
		`id eq "1" and displayName eq "n" and externalId eq "x"`:                  {{identity.AttrID, "1"}, {identity.AttrDisplayName, "n"}, {identity.AttrExternalID, "x"}},
	} {
		got, err := identity.ParseUserFilter(raw)
		if err != nil || !reflect.DeepEqual(got.Conditions, want) {
			t.Errorf("ParseUserFilter(%q) = %+v, %v; want %+v", raw, got.Conditions, err, want)
		}
	}
}

func TestAUserFilterBeyondEqAndAndIsInvalid(t *testing.T) {
	for _, raw := range []string{
		`userName co "jo"`, `userName ne "jo"`, `userName pr`, `userName eq "a" or userName eq "b"`, `not (userName eq "a")`,
		`active eq true`, `title eq "x"`, `userName eq`, `userName eq 5`, `emails[type eq "work"].value eq "a"`,
		`urn:other:schema:userName eq "x"`, `((`, `groups.value eq "x"`, `name.givenName eq "jo"`,
	} {
		if _, err := identity.ParseUserFilter(raw); scimType(err) != "invalidFilter" {
			t.Errorf("ParseUserFilter(%q) = %v, want invalidFilter", raw, err)
		}
	}
}

func TestAUserFilterIsBounded(t *testing.T) {
	long := `userName eq "` + string(make([]byte, 2000)) + `"`
	if _, err := identity.ParseUserFilter(long); scimType(err) != "invalidFilter" {
		t.Errorf("a 2000-byte filter = %v, want invalidFilter", err)
	}
}

func TestEmailBeatsEmailsAndNameFallsBack(t *testing.T) {
	s := func(v string) *string { return &v }
	for name, c := range map[string]struct {
		in    identity.UserAttributes
		email string
		ok    bool
		name  string
	}{
		"userName wins over the primary email": {identity.UserAttributes{
			UserName: s("jo@example.com"), Emails: []identity.Email{{Value: "other@example.com", Primary: true}},
		}, "jo@example.com", true, "jo@example.com"},
		"the primary email": {identity.UserAttributes{Emails: []identity.Email{{Value: "a@example.com"}, {Value: "b@example.com", Primary: true}}},
			"b@example.com", true, "b@example.com"},
		"the first email":      {identity.UserAttributes{Emails: []identity.Email{{Value: "a@example.com"}}}, "a@example.com", true, "a@example.com"},
		"neither":              {identity.UserAttributes{UserName: s("  ")}, "", false, ""},
		"displayName":          {identity.UserAttributes{UserName: s("jo@example.com"), DisplayName: s(" Jo Doe ")}, "jo@example.com", true, "Jo Doe"},
		"given and family":     {identity.UserAttributes{UserName: s("jo@example.com"), GivenName: s("Jo"), FamilyName: s("Doe")}, "jo@example.com", true, "Jo Doe"},
		"given alone":          {identity.UserAttributes{UserName: s("jo@example.com"), GivenName: s("Jo")}, "jo@example.com", true, "Jo"},
		"displayName beats it": {identity.UserAttributes{UserName: s("jo@example.com"), DisplayName: s("JD"), GivenName: s("Jo")}, "jo@example.com", true, "JD"},
	} {
		email, ok := c.in.EmailAddress()
		if email != c.email || ok != c.ok {
			t.Errorf("%s: email = %q, %v; want %q, %v", name, email, ok, c.email, c.ok)
		}
		if ok && c.in.Name(email) != c.name {
			t.Errorf("%s: name = %q, want %q", name, c.in.Name(email), c.name)
		}
	}
}

func TestPatchingAUser(t *testing.T) {
	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	op := func(op, path string, value any) identity.PatchOperation {
		return identity.PatchOperation{Op: op, Path: path, Value: value}
	}
	for name, c := range map[string]struct {
		ops  []identity.PatchOperation
		want identity.UserPatch
	}{
		"Okta deactivation without a path": {
			[]identity.PatchOperation{op("replace", "", map[string]any{"active": false})}, identity.UserPatch{Active: b(false)}},
		"Entra deactivation with a string boolean": {
			[]identity.PatchOperation{op("Replace", "active", "False")}, identity.UserPatch{Active: b(false)}},
		"Entra work email by a value path": {
			[]identity.PatchOperation{op("Replace", `emails[type eq "work"].value`, "jo@example.com")}, identity.UserPatch{Email: s("jo@example.com")}},
		"userName": {[]identity.PatchOperation{op("add", "userName", "jo@example.com")}, identity.UserPatch{Email: s("jo@example.com")}},
		"emails as an array": {
			[]identity.PatchOperation{op("replace", "emails", []any{map[string]any{"value": "jo@example.com", "primary": true}})},
			identity.UserPatch{Email: s("jo@example.com")}},
		"several attributes without a path": {
			[]identity.PatchOperation{op("replace", "", map[string]any{"displayName": "Jo", "externalId": "x", "active": true})},
			identity.UserPatch{DisplayName: s("Jo"), ExternalID: s("x"), Active: b(true)}},
		"urn prefixed key": {
			[]identity.PatchOperation{op("replace", "", map[string]any{"urn:ietf:params:scim:schemas:core:2.0:User:active": false})},
			identity.UserPatch{Active: b(false)}},
		"remove displayName": {[]identity.PatchOperation{op("remove", "displayName", nil)}, identity.UserPatch{RemoveDisplayName: true}},
		"remove externalId":  {[]identity.PatchOperation{op("remove", "externalId", nil)}, identity.UserPatch{RemoveExternalID: true}},
		"the later operation wins": {
			[]identity.PatchOperation{op("replace", "displayName", "A"), op("replace", "displayName", "B"), op("remove", "externalId", nil), op("add", "externalId", "x")},
			identity.UserPatch{DisplayName: s("B"), ExternalID: s("x")}},
		"name is accepted and changes nothing": {
			[]identity.PatchOperation{op("replace", "name.givenName", "Jo"), op("replace", "name", map[string]any{"familyName": "Doe"})}, identity.UserPatch{}},
	} {
		got, err := identity.ApplyUserPatch(c.ops)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: = %+v, %v; want %+v", name, got, err, c.want)
		}
	}
}

func TestPatchingAUserRefusesWhatItCannotDo(t *testing.T) {
	op := func(op, path string, value any) identity.PatchOperation {
		return identity.PatchOperation{Op: op, Path: path, Value: value}
	}
	for name, c := range map[string]struct {
		ops  []identity.PatchOperation
		want string
	}{
		"no operations":                  {nil, "invalidValue"},
		"an unknown op":                  {[]identity.PatchOperation{op("move", "active", true)}, "invalidSyntax"},
		"remove without a path":          {[]identity.PatchOperation{op("remove", "", nil)}, "noTarget"},
		"no path and no object":          {[]identity.PatchOperation{op("replace", "", "x")}, "invalidValue"},
		"active is a boolean":            {[]identity.PatchOperation{op("replace", "active", 5.0)}, "invalidValue"},
		"active is not removable":        {[]identity.PatchOperation{op("remove", "active", nil)}, "invalidValue"},
		"userName is not removable":      {[]identity.PatchOperation{op("remove", "userName", nil)}, "invalidValue"},
		"emails is not removable":        {[]identity.PatchOperation{op("remove", "emails", nil)}, "invalidValue"},
		"an email must be a string":      {[]identity.PatchOperation{op("replace", "userName", 7.0)}, "invalidValue"},
		"displayName must be a string":   {[]identity.PatchOperation{op("replace", "displayName", true)}, "invalidValue"},
		"externalId must be a string":    {[]identity.PatchOperation{op("replace", "externalId", 7.0)}, "invalidValue"},
		"groups are read only":           {[]identity.PatchOperation{op("add", "groups", []any{})}, "mutability"},
		"a group by a value path":        {[]identity.PatchOperation{op("remove", `groups[value eq "x"]`, nil)}, "mutability"},
		"id is read only":                {[]identity.PatchOperation{op("replace", "id", "x")}, "mutability"},
		"an unmapped attribute":          {[]identity.PatchOperation{op("replace", "title", "x")}, "noTarget"},
		"a sub attribute of emails":      {[]identity.PatchOperation{op("replace", `emails[type eq "work"].display`, "x")}, "noTarget"},
		"another schema":                 {[]identity.PatchOperation{op("replace", "urn:other:schema:2.0:User:active", true)}, "noTarget"},
		"a malformed path":               {[]identity.PatchOperation{op("replace", "emails[", "x")}, "invalidSyntax"},
		"a refused operation after a ok": {[]identity.PatchOperation{op("replace", "active", true), op("replace", "title", "x")}, "noTarget"},
	} {
		if _, err := identity.ApplyUserPatch(c.ops); scimType(err) != c.want {
			t.Errorf("%s: = %v, want %s", name, err, c.want)
		}
	}
}

func TestAnExternalIDHasNoSpaceAroundItAndIsBounded(t *testing.T) {
	for externalID, ok := range map[string]bool{"abc": true, "a b": true, "": true, " abc": false, "abc ": false, string(make([]byte, 300)): false} {
		if err := identity.CheckExternalID(externalID); (err == nil) != ok {
			t.Errorf("CheckExternalID(%.10q) = %v, want ok=%v", externalID, err, ok)
		}
	}
}
