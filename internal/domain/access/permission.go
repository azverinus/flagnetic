// Package access is the domain of who may do what: members and the instance
// admin, API tokens and their scopes, the permission catalog, the built-in
// roles and how a caller is judged against what an operation requires.
package access

import "slices"

// Scope is what an API token is allowed to touch. Scopes narrow a token's
// permissions and never add to them.
type Scope string

// The scopes a token can carry.
const (
	ScopeRead             Scope = "read"
	ScopeFlagsWrite       Scope = "flags:write"
	ScopeExperimentsWrite Scope = "experiments:write"
	ScopeProjectAdmin     Scope = "project:admin"
	ScopeInstanceAdmin    Scope = "instance:admin"
)

// AllScopes returns every scope a token can carry, read first.
func AllScopes() []Scope {
	return []Scope{ScopeRead, ScopeFlagsWrite, ScopeExperimentsWrite, ScopeProjectAdmin, ScopeInstanceAdmin}
}

// Permission is one action a role allows, named entity.verb.
type Permission string

// PermissionSpec is one row of the permission catalog.
type PermissionSpec struct {
	Name Permission
	// Scope is the token scope the permission falls under: a token exercises
	// the permission only when it carries the scope.
	Scope Scope
	// MembersOnly marks a permission that changes who has access, which a
	// project token never exercises whatever its role, so a pipeline
	// credential cannot be turned into a way in.
	MembersOnly bool
}

// The permissions of the catalog.
const (
	// PermProjectRead covers every ordinary read in a project, its audit log
	// included.
	PermProjectRead Permission = "project.read"
	// PermProjectUpdate renames a project and changes its description.
	PermProjectUpdate Permission = "project.update"
	// PermEnvironmentManage creates, renames and deletes environments.
	PermEnvironmentManage Permission = "environment.manage"
	// PermDeclarationManage creates, renames and deletes unit types and
	// attributes.
	PermDeclarationManage Permission = "declaration.manage"
	// PermGrantManage makes and removes grants.
	PermGrantManage Permission = "grant.manage"
	// PermProjectTokenManage lists, creates and revokes project tokens.
	PermProjectTokenManage Permission = "project_token.manage"
	// PermFlagEdit creates flags and changes their definition.
	PermFlagEdit Permission = "flag.edit"
	// PermFlagArchive archives, restores and deletes flags.
	PermFlagArchive Permission = "flag.archive"
)

// catalog is every permission an operation can require. Each context adds
// its permissions with the operations that need them, and writes them into
// the built-in roles below.
var catalog = []PermissionSpec{
	{Name: PermProjectRead, Scope: ScopeRead},
	{Name: PermProjectUpdate, Scope: ScopeProjectAdmin},
	{Name: PermEnvironmentManage, Scope: ScopeProjectAdmin},
	// Unit types and attributes are what flags target, so changing them
	// falls under the scope of a pipeline that writes flags.
	{Name: PermDeclarationManage, Scope: ScopeFlagsWrite},
	{Name: PermGrantManage, Scope: ScopeProjectAdmin, MembersOnly: true},
	{Name: PermProjectTokenManage, Scope: ScopeProjectAdmin, MembersOnly: true},
	{Name: PermFlagEdit, Scope: ScopeFlagsWrite},
	{Name: PermFlagArchive, Scope: ScopeFlagsWrite},
}

// Catalog returns the permission catalog.
func Catalog() []PermissionSpec { return slices.Clone(catalog) }

// PermissionNamed returns the catalog's permission called name.
func PermissionNamed(name string) (PermissionSpec, bool) {
	for _, p := range catalog {
		if string(p.Name) == name {
			return p, true
		}
	}
	return PermissionSpec{}, false
}

// Requirement is what an operation requires of its caller: a catalog
// permission, judged in the operation's project, or one of the reserved
// values below.
type Requirement string

// The requirements an operation may declare besides a catalog permission.
const (
	// RequirePublic: anyone, without credentials; a credential presented is
	// still authenticated, and a bad one refused.
	RequirePublic Requirement = "public"
	// RequireMember: any credential acting for a member, whatever their
	// grants; a project token only where the operation is open to it.
	RequireMember Requirement = "member"
	// RequireInstanceAdmin: an instance admin, whose token must carry
	// instance:admin to write and read to read.
	RequireInstanceAdmin Requirement = "instance_admin"
)

// reserved reports whether r is one of the reserved values rather than a
// catalog permission.
func (r Requirement) reserved() bool {
	return r == RequirePublic || r == RequireMember || r == RequireInstanceAdmin
}

// IsRequirement reports whether r is a requirement an operation may declare:
// a catalog permission or one of the reserved values.
func IsRequirement(r Requirement) bool {
	if r.reserved() {
		return true
	}
	_, ok := PermissionNamed(string(r))
	return ok
}

// ScopeOf returns the scope a token needs for an operation requiring
// requirement, a read operation or not; "" when any token will do.
func ScopeOf(requirement Requirement, read bool) Scope {
	switch requirement {
	case RequirePublic, RequireMember:
		return ""
	case RequireInstanceAdmin:
		if read {
			return ScopeRead
		}
		return ScopeInstanceAdmin
	}
	p, _ := PermissionNamed(string(requirement))
	return p.Scope
}

// RoleName names a built-in role.
type RoleName string

// The built-in roles, fixed by the release so a name means the same on every
// instance.
const (
	RoleOwner      RoleName = "owner"
	RoleMaintainer RoleName = "maintainer"
	RoleDeveloper  RoleName = "developer"
	RoleViewer     RoleName = "viewer"
)

// Role is a fixed list of permissions.
type Role struct {
	Name        RoleName
	Permissions []Permission
}

// roles lists the built-in roles, the most powerful first.
var roles = []Role{
	{Name: RoleOwner, Permissions: []Permission{
		PermProjectRead, PermProjectUpdate, PermEnvironmentManage, PermDeclarationManage,
		PermGrantManage, PermProjectTokenManage, PermFlagEdit, PermFlagArchive,
	}},
	{Name: RoleMaintainer, Permissions: []Permission{
		PermProjectRead, PermEnvironmentManage, PermDeclarationManage, PermFlagEdit, PermFlagArchive,
	}},
	{Name: RoleDeveloper, Permissions: []Permission{PermProjectRead, PermFlagEdit}},
	{Name: RoleViewer, Permissions: []Permission{PermProjectRead}},
}

// Roles returns the built-in roles, the most powerful first.
func Roles() []Role {
	out := make([]Role, len(roles))
	for i, r := range roles {
		out[i] = Role{Name: r.Name, Permissions: slices.Clone(r.Permissions)}
	}
	return out
}

// RoleNamed returns the built-in role called name.
func RoleNamed(name RoleName) (Role, bool) {
	for _, r := range Roles() {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}
