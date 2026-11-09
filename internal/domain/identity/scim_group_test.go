package identity_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestAGroupFilterIsEqualitiesOnDisplayNameExternalIDAndID(t *testing.T) {
	for raw, want := range map[string][]identity.GroupCondition{
		``:                                 nil,
		`displayName eq "Engineering"`:     {{identity.GroupDisplayName, "Engineering"}},
		`DISPLAYNAME EQ "a \"b\""`:         {{identity.GroupDisplayName, `a "b"`}},
		`externalId eq "ext-1"`:            {{identity.GroupExternalID, "ext-1"}},
		`id eq "8f2d"`:                     {{identity.GroupID, "8f2d"}},
		`displayName eq "a" and id eq "1"`: {{identity.GroupDisplayName, "a"}, {identity.GroupID, "1"}},
		`urn:ietf:params:scim:schemas:core:2.0:Group:displayName eq "a"`: {{identity.GroupDisplayName, "a"}},
	} {
		got, err := identity.ParseGroupFilter(raw)
		if err != nil || !reflect.DeepEqual(got.Conditions, want) {
			t.Errorf("ParseGroupFilter(%q) = %+v, %v; want %+v", raw, got.Conditions, err, want)
		}
	}
	for _, raw := range []string{
		`userName eq "jo@example.com"`, `emails.value eq "a"`, `members.value eq "x"`, `displayName co "a"`,
		`displayName eq "a" or id eq "b"`, `urn:ietf:params:scim:schemas:core:2.0:User:displayName eq "a"`, `((`,
	} {
		if _, err := identity.ParseGroupFilter(raw); scimType(err) != "invalidFilter" {
			t.Errorf("ParseGroupFilter(%q) = %v, want invalidFilter", raw, err)
		}
	}
}

func memberIDs(n int) []identity.MemberID {
	src := id.NewSource(clock.System{})
	out := make([]identity.MemberID, n)
	for i := range out {
		out[i] = id.New[identity.Member](src)
	}
	return out
}

func refs(members ...identity.MemberID) []any {
	out := make([]any, len(members))
	for i, m := range members {
		out[i] = map[string]any{"value": m.String(), "display": "someone"}
	}
	return out
}

func TestPatchingAGroupsMembersInEveryShapeTheDirectoriesSend(t *testing.T) {
	m := memberIDs(4)
	base := identity.GroupState{DisplayName: "eng", ExternalID: "ext", Members: []identity.MemberID{m[0], m[1]}}
	for name, c := range map[string]struct {
		ops  []identity.PatchOperation
		want []identity.MemberID
	}{
		"add appends":                {[]identity.PatchOperation{{Op: "add", Path: "members", Value: refs(m[2])}}, []identity.MemberID{m[0], m[1], m[2]}},
		"Entra's capitalised Add":    {[]identity.PatchOperation{{Op: "Add", Path: "members", Value: refs(m[2], m[3])}}, []identity.MemberID{m[0], m[1], m[2], m[3]}},
		"adding a member twice":      {[]identity.PatchOperation{{Op: "add", Path: "members", Value: refs(m[0], m[2], m[2])}}, []identity.MemberID{m[0], m[1], m[2]}},
		"Entra's Remove with values": {[]identity.PatchOperation{{Op: "Remove", Path: "members", Value: refs(m[0])}}, []identity.MemberID{m[1]}},
		"Okta's removal by filter":   {[]identity.PatchOperation{{Op: "remove", Path: `members[value eq "` + m[1].String() + `"]`}}, []identity.MemberID{m[0]}},
		"removal of a non-member":    {[]identity.PatchOperation{{Op: "remove", Path: `members[value eq "` + m[3].String() + `"]`}}, []identity.MemberID{m[0], m[1]}},
		"remove without a value":     {[]identity.PatchOperation{{Op: "remove", Path: "members"}}, nil},
		"replace sets the list":      {[]identity.PatchOperation{{Op: "replace", Path: "members", Value: refs(m[3])}}, []identity.MemberID{m[3]}},
		"replace with none":          {[]identity.PatchOperation{{Op: "replace", Path: "members", Value: []any{}}}, nil},
		"path-less add of members":   {[]identity.PatchOperation{{Op: "add", Value: map[string]any{"members": refs(m[2])}}}, []identity.MemberID{m[0], m[1], m[2]}},
		"in order": {[]identity.PatchOperation{
			{Op: "remove", Path: "members"}, {Op: "add", Path: "members", Value: refs(m[3])}, {Op: "add", Path: "members", Value: refs(m[2])},
		}, []identity.MemberID{m[3], m[2]}},
	} {
		got, err := identity.ApplyGroupPatch(base, uuidOf(m[0]), c.ops)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.Equal(got.Members, c.want) || got.DisplayName != "eng" || got.ExternalID != "ext" {
			t.Errorf("%s: %+v, want members %v", name, got, c.want)
		}
	}
	if !slices.Equal(base.Members, []identity.MemberID{m[0], m[1]}) {
		t.Errorf("the patched state changed the state it started from: %v", base.Members)
	}
}

// uuidOf stands for the group's own id, which Okta repeats in a rename.
func uuidOf(m identity.MemberID) string { return m.String() }

func TestPatchingAGroupsNameAndExternalID(t *testing.T) {
	base := identity.GroupState{DisplayName: "eng", ExternalID: "ext"}
	self := "11111111-2222-3333-4444-555555555555"
	for name, c := range map[string]struct {
		ops       []identity.PatchOperation
		name, ext string
	}{
		"Entra's rename":          {[]identity.PatchOperation{{Op: "Replace", Path: "displayName", Value: "ops"}}, "ops", "ext"},
		"Okta's path-less rename": {[]identity.PatchOperation{{Op: "replace", Value: map[string]any{"id": self, "displayName": "ops"}}}, "ops", "ext"},
		"externalId replaced":     {[]identity.PatchOperation{{Op: "replace", Path: "externalId", Value: "new"}}, "eng", "new"},
		"externalId removed":      {[]identity.PatchOperation{{Op: "remove", Path: "externalId"}}, "eng", ""},
		"externalId blanked":      {[]identity.PatchOperation{{Op: "replace", Path: "externalId", Value: " "}}, "eng", ""},
		"externalId nulled":       {[]identity.PatchOperation{{Op: "replace", Path: "externalId", Value: nil}}, "eng", ""},
		"the name kept as sent":   {[]identity.PatchOperation{{Op: "add", Path: "displayName", Value: "  ops "}}, "  ops ", "ext"},
		"schema-prefixed path":    {[]identity.PatchOperation{{Op: "replace", Path: "urn:ietf:params:scim:schemas:core:2.0:Group:displayName", Value: "ops"}}, "ops", "ext"},
	} {
		got, err := identity.ApplyGroupPatch(base, self, c.ops)
		if err != nil || got.DisplayName != c.name || got.ExternalID != c.ext {
			t.Errorf("%s: %+v, %v; want %q, %q", name, got, err, c.name, c.ext)
		}
	}
}

func TestPatchingAGroupRefusesWhatItCannotDo(t *testing.T) {
	self := "11111111-2222-3333-4444-555555555555"
	base := identity.GroupState{DisplayName: "eng"}
	for name, c := range map[string]struct {
		ops  []identity.PatchOperation
		want string
	}{
		"no operation":                    {nil, "invalidValue"},
		"an unknown op":                   {[]identity.PatchOperation{{Op: "move", Path: "members"}}, "invalidSyntax"},
		"removing the name":               {[]identity.PatchOperation{{Op: "remove", Path: "displayName"}}, "invalidValue"},
		"a blank name":                    {[]identity.PatchOperation{{Op: "replace", Path: "displayName", Value: "  "}}, "invalidValue"},
		"a name that is no string":        {[]identity.PatchOperation{{Op: "replace", Path: "displayName", Value: 5}}, "invalidValue"},
		"a member that is no object":      {[]identity.PatchOperation{{Op: "add", Path: "members", Value: []any{"x"}}}, "invalidValue"},
		"a member that is no id":          {[]identity.PatchOperation{{Op: "add", Path: "members", Value: []any{map[string]any{"value": "jo"}}}}, "invalidValue"},
		"members that are no list":        {[]identity.PatchOperation{{Op: "add", Path: "members", Value: "x"}}, "invalidValue"},
		"adding by filter":                {[]identity.PatchOperation{{Op: "add", Path: `members[value eq "` + self + `"]`}}, "invalidValue"},
		"a filter on something else":      {[]identity.PatchOperation{{Op: "remove", Path: `members[display eq "x"]`}}, "invalidFilter"},
		"a path that does not parse":      {[]identity.PatchOperation{{Op: "remove", Path: `members[`}}, "invalidSyntax"},
		"an unmapped attribute":           {[]identity.PatchOperation{{Op: "replace", Path: "description", Value: "x"}}, "noTarget"},
		"another schema":                  {[]identity.PatchOperation{{Op: "replace", Path: "urn:other:displayName", Value: "x"}}, "noTarget"},
		"the id":                          {[]identity.PatchOperation{{Op: "replace", Path: "id", Value: self}}, "mutability"},
		"another id in a path-less value": {[]identity.PatchOperation{{Op: "replace", Value: map[string]any{"id": "other"}}}, "mutability"},
		"an externalId no string":         {[]identity.PatchOperation{{Op: "replace", Path: "externalId", Value: 5}}, "invalidValue"},
		"a member value no string":        {[]identity.PatchOperation{{Op: "remove", Path: `members[value eq 5]`}}, "invalidFilter"},
		"a member's sub-attribute":        {[]identity.PatchOperation{{Op: "add", Path: "members.display", Value: "x"}}, "noTarget"},
		"meta":                            {[]identity.PatchOperation{{Op: "replace", Path: "meta", Value: map[string]any{}}}, "mutability"},
		"a path-less remove":              {[]identity.PatchOperation{{Op: "remove"}}, "noTarget"},
		"a path-less value no object":     {[]identity.PatchOperation{{Op: "replace", Value: "x"}}, "invalidValue"},
		"an externalId too long":          {[]identity.PatchOperation{{Op: "replace", Path: "externalId", Value: string(make([]rune, 300))}}, "invalidValue"},
	} {
		if _, err := identity.ApplyGroupPatch(base, self, c.ops); scimType(err) != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
}

func TestAGroupStateIsChecked(t *testing.T) {
	m := memberIDs(1)
	// The name is kept as the directory sent it: group mappings and filters
	// match it exactly.
	got, err := identity.NewGroupState("  eng ", " ", []string{m[0].String(), m[0].String()})
	if err != nil || got.DisplayName != "  eng " || got.ExternalID != "" || !slices.Equal(got.Members, m) {
		t.Errorf("NewGroupState = %+v, %v; want \"  eng \", no externalId, one member", got, err)
	}
	for name, c := range map[string]struct {
		name, ext string
		members   []string
	}{
		"no name":             {"", "", nil},
		"an id that is not":   {"eng", "", []string{"jo"}},
		"a long name":         {string(make([]rune, 300)), "", nil},
		"a padded externalId": {"eng", " x", nil},
	} {
		if _, err := identity.NewGroupState(c.name, c.ext, c.members); scimType(err) != "invalidValue" {
			t.Errorf("%s: %v, want invalidValue", name, err)
		}
	}
}

func TestTheMembersAChangeTouches(t *testing.T) {
	m := memberIDs(4)
	before := identity.GroupState{DisplayName: "eng", Members: []identity.MemberID{m[0], m[1]}}
	after := identity.GroupState{DisplayName: "eng", Members: []identity.MemberID{m[1], m[2]}}
	if got := identity.TouchedMembers(before, after); !sameSet(got, []identity.MemberID{m[0], m[2]}) {
		t.Errorf("membership change touches %v, want the one removed and the one added", got)
	}
	renamed := identity.GroupState{DisplayName: "ops", Members: []identity.MemberID{m[1], m[2]}}
	if got := identity.TouchedMembers(before, renamed); !sameSet(got, m[:3]) {
		t.Errorf("a rename touches %v, want every member before and after", got)
	}
	if got := identity.TouchedMembers(before, before); len(got) != 0 {
		t.Errorf("no change touches %v", got)
	}
	ext := before
	ext.ExternalID = "new"
	if got := identity.TouchedMembers(before, ext); len(got) != 0 {
		t.Errorf("an externalId change touches %v, want none: mappings name groups by name", got)
	}
}

func sameSet(a, b []identity.MemberID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
