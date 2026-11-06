package identity_test

import (
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func knownRole(role string) bool {
	return slices.Contains([]string{"owner", "maintainer", "developer", "viewer"}, role)
}

func TestWellFormedGroupMappingsAreKeptInOrder(t *testing.T) {
	in := []identity.GroupMappingInput{
		{Group: "eng", Project: "alpha", Role: "developer"},
		{Group: "it-admins", InstanceAdmin: true},
		{Group: "eng", Project: "beta", Role: "viewer"},
	}
	got, err := identity.CheckGroupMappings(in, knownRole)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, in) {
		t.Errorf("checked = %+v, want %+v", got, in)
	}
}

func TestAnEmptyListOfGroupMappingsIsWellFormed(t *testing.T) {
	if _, err := identity.CheckGroupMappings(nil, knownRole); err != nil {
		t.Fatal(err)
	}
}

func TestAMalformedGroupMappingIsNamedByItsPosition(t *testing.T) {
	for name, tc := range map[string]struct {
		in      identity.GroupMappingInput
		pointer string
		code    string
	}{
		"a blank group":                {identity.GroupMappingInput{Group: " \t", Project: "alpha", Role: "viewer"}, "/items/0/group", "blank"},
		"an empty group":               {identity.GroupMappingInput{Group: "", InstanceAdmin: true}, "/items/0/group", "blank"},
		"neither a role nor admin":     {identity.GroupMappingInput{Group: "eng"}, "/items/0/role", "required"},
		"a project without a role":     {identity.GroupMappingInput{Group: "eng", Project: "alpha"}, "/items/0/role", "required"},
		"a role without a project":     {identity.GroupMappingInput{Group: "eng", Role: "viewer"}, "/items/0/project", "required"},
		"a role outside the built-in":  {identity.GroupMappingInput{Group: "eng", Project: "alpha", Role: "admin"}, "/items/0/role", "unknown_role"},
		"admin beside a project":       {identity.GroupMappingInput{Group: "eng", Project: "alpha", Role: "viewer", InstanceAdmin: true}, "/items/0/instance_admin", "exclusive"},
		"admin beside a role":          {identity.GroupMappingInput{Group: "eng", Role: "viewer", InstanceAdmin: true}, "/items/0/instance_admin", "exclusive"},
		"admin beside a project alone": {identity.GroupMappingInput{Group: "eng", Project: "alpha", InstanceAdmin: true}, "/items/0/instance_admin", "exclusive"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := identity.CheckGroupMappings([]identity.GroupMappingInput{tc.in}, knownRole)
			code, pointers := problemOf(t, err)
			if code != "invalid_group_mappings" || !slices.Contains(pointers, tc.pointer) {
				t.Errorf("code %q, pointers %v; want invalid_group_mappings at %s", code, pointers, tc.pointer)
			}
		})
	}
}

func TestEveryMalformedGroupMappingIsNamedAtOnce(t *testing.T) {
	_, err := identity.CheckGroupMappings([]identity.GroupMappingInput{
		{Group: "eng", Project: "alpha", Role: "viewer"},
		{Group: ""},
		{Group: "ops", Project: "beta", Role: "nope"},
	}, knownRole)
	_, pointers := problemOf(t, err)
	for _, want := range []string{"/items/1/group", "/items/2/role"} {
		if !slices.Contains(pointers, want) {
			t.Errorf("pointers %v miss %s", pointers, want)
		}
	}
}

var ids = id.NewSource(clock.System{})

func projectID() workspace.ProjectID { return id.New[workspace.Project](ids) }

func TestGroupsMapToTheUnionOfTheirGrantsAndInstanceAdmin(t *testing.T) {
	alpha, beta := projectID(), projectID()
	mappings := []identity.GroupMapping{
		{Group: "eng", Project: alpha, Role: "developer"},
		{Group: "leads", Project: alpha, Role: "owner"},
		{Group: "ops", Project: beta, Role: "viewer"},
		{Group: "eng", Project: beta, Role: "viewer"},
		{Group: "it-admins", InstanceAdmin: true},
	}

	got := identity.Entitle(mappings, []string{"eng", "leads", "sales"})
	want := []identity.ProjectRole{{Project: alpha, Role: "developer"}, {Project: alpha, Role: "owner"}, {Project: beta, Role: "viewer"}}
	if !slices.Equal(got.Grants, want) || got.InstanceAdmin {
		t.Errorf("entitled = %+v, want %v and no instance admin", got, want)
	}

	got = identity.Entitle(mappings, []string{"it-admins"})
	if len(got.Grants) != 0 || !got.InstanceAdmin {
		t.Errorf("entitled = %+v, want instance admin alone", got)
	}
}

func TestGroupsMatchExactlyAndNoGroupsGrantNothing(t *testing.T) {
	mappings := []identity.GroupMapping{{Group: "Eng", Project: projectID(), Role: "viewer"}}
	for _, groups := range [][]string{nil, {}, {"eng"}, {"Eng "}, {"ENG"}} {
		if got := identity.Entitle(mappings, groups); len(got.Grants) != 0 || got.InstanceAdmin {
			t.Errorf("groups %q entitle %+v", groups, got)
		}
	}
}

func TestAGroupsClaimIsAListOfStrings(t *testing.T) {
	for name, tc := range map[string]struct {
		in   any
		want []string
		ok   bool
	}{
		"strings":           {[]any{"eng", "ops"}, []string{"eng", "ops"}, true},
		"typed strings":     {[]string{"eng"}, []string{"eng"}, true},
		"empty":             {[]any{}, []string{}, true},
		"a single string":   {"eng", nil, false},
		"a number inside":   {[]any{"eng", 7.0}, nil, false},
		"a null inside":     {[]any{"eng", nil}, nil, false},
		"an object":         {map[string]any{"eng": true}, nil, false},
		"an absent claim":   {nil, nil, false},
		"a boolean":         {true, nil, false},
		"a list of objects": {[]any{map[string]any{"name": "eng"}}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := identity.ParseGroupsClaim(tc.in)
			if ok != tc.ok || !slices.Equal(got, tc.want) {
				t.Errorf("ParseGroupsClaim(%#v) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
			if ok && got == nil {
				t.Error("a list of strings parsed to nil, which reads as no claim")
			}
		})
	}
}
