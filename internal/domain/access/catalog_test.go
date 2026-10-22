package access_test

import (
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestTheCatalogAssignsEachPermissionItsScope(t *testing.T) {
	want := []access.PermissionSpec{
		{Name: "project.read", Scope: access.ScopeRead},
		{Name: "project.update", Scope: access.ScopeProjectAdmin},
		{Name: "environment.manage", Scope: access.ScopeProjectAdmin},
		{Name: "declaration.manage", Scope: access.ScopeFlagsWrite},
		{Name: "grant.manage", Scope: access.ScopeProjectAdmin, MembersOnly: true},
		{Name: "project_token.manage", Scope: access.ScopeProjectAdmin, MembersOnly: true},
		{Name: "flag.edit", Scope: access.ScopeFlagsWrite},
		{Name: "flag.archive", Scope: access.ScopeFlagsWrite},
		{Name: "flag_configuration.update", Scope: access.ScopeFlagsWrite},
		{Name: "flag_configuration.kill", Scope: access.ScopeFlagsWrite},
		{Name: "flag_configuration.revert", Scope: access.ScopeFlagsWrite},
		{Name: "segment.edit", Scope: access.ScopeFlagsWrite},
		{Name: "segment.delete", Scope: access.ScopeFlagsWrite},
		{Name: "sdk_key.manage", Scope: access.ScopeProjectAdmin},
	}
	if got := access.Catalog(); !slices.Equal(got, want) {
		t.Errorf("catalog = %+v, want %+v", got, want)
	}
}

func TestTheBuiltInRolesHoldThePermissionsRowByRow(t *testing.T) {
	want := map[access.RoleName][]access.Permission{
		access.RoleViewer:    {"project.read"},
		access.RoleDeveloper: {"project.read", "flag.edit", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit"},
		access.RoleMaintainer: {"project.read", "environment.manage", "declaration.manage", "flag.edit", "flag.archive",
			"flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage"},
		access.RoleOwner: {"project.read", "project.update", "environment.manage", "declaration.manage",
			"grant.manage", "project_token.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage"},
	}
	for _, r := range access.Roles() {
		got := slices.Clone(r.Permissions)
		slices.Sort(got)
		w := slices.Clone(want[r.Name])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s holds %v, want %v", r.Name, got, w)
		}
	}
}

func TestEffectivePermissionsAreTheUnionOfGrantsNarrowedByScopes(t *testing.T) {
	ids := newIDs()
	mobile, web := id.New[workspace.Project](ids), id.New[workspace.Project](ids)
	all := []access.Permission{"project.read", "project.update", "environment.manage", "declaration.manage",
		"grant.manage", "project_token.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage"}

	for name, tc := range map[string]struct {
		caller access.Caller
		grants []access.RoleName
		want   []access.Permission
	}{
		"an instance admin holds every permission without a grant": {sessionCaller(true), nil, all},
		"an instance admin's token is narrowed by its scopes": {tokenCaller(t, true, access.ScopeFlagsWrite), nil,
			[]access.Permission{"project.read", "declaration.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete"}},
		"a member without a grant holds nothing": {sessionCaller(false), nil, nil},
		"a viewer reads":                         {sessionCaller(false), []access.RoleName{access.RoleViewer}, []access.Permission{"project.read"}},
		"grants add up and never take away": {sessionCaller(false), []access.RoleName{access.RoleViewer, access.RoleMaintainer},
			[]access.Permission{"project.read", "environment.manage", "declaration.manage", "flag.edit", "flag.archive",
				"flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete",
				"sdk_key.manage"}},
		"an owner's read token reads": {tokenCaller(t, false), []access.RoleName{access.RoleOwner},
			[]access.Permission{"project.read"}},
		"an owner's admin token never holds a flags permission": {tokenCaller(t, false, access.ScopeProjectAdmin),
			[]access.RoleName{access.RoleOwner},
			[]access.Permission{"project.read", "project.update", "environment.manage", "grant.manage", "project_token.manage", "sdk_key.manage"}},
		"a project token holds its role but never changes who has access": {projectTokenCaller(mobile, access.RoleOwner), nil,
			[]access.Permission{"project.read", "project.update", "environment.manage", "declaration.manage", "flag.edit", "flag.archive",
				"flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete",
				"sdk_key.manage"}},
		"a project token is narrowed by its scopes": {projectTokenCaller(mobile, access.RoleMaintainer, access.ScopeRead), nil,
			[]access.Permission{"project.read"}},
	} {
		got := access.EffectivePermissions(tc.caller, mobile, tc.grants)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: permissions = %v, want %v", name, got, tc.want)
		}
	}
	if got := access.EffectivePermissions(projectTokenCaller(mobile, access.RoleOwner), web, nil); len(got) != 0 {
		t.Errorf("a project token holds %v in another project, want nothing", got)
	}
}
