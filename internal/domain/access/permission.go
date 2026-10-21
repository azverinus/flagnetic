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

// catalog is every permission an operation can require. Each context adds
// its permissions with the operations that need them, and writes them into
// the built-in roles below.
var catalog = []PermissionSpec{}

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

// The requirements an operation may declare besides a catalog permission.
const (
	// RequirePublic: anyone, without credentials.
	RequirePublic = "public"
	// RequireMember: any credential acting for a member, whatever their
	// grants; a project token only to read who it is.
	RequireMember = "member"
	// RequireInstanceAdmin: an instance admin, whose token must carry
	// instance:admin to write and read to read.
	RequireInstanceAdmin = "instance_admin"
)

// IsRequirement reports whether r is a requirement an operation may declare:
// a catalog permission or one of the reserved values.
func IsRequirement(r string) bool {
	switch r {
	case RequirePublic, RequireMember, RequireInstanceAdmin:
		return true
	}
	_, ok := PermissionNamed(r)
	return ok
}

// ScopeOf returns the scope a token needs for an operation requiring
// requirement, a read operation or not; "" when any token will do.
func ScopeOf(requirement string, read bool) Scope {
	switch requirement {
	case RequirePublic, RequireMember:
		return ""
	case RequireInstanceAdmin:
		if read {
			return ScopeRead
		}
		return ScopeInstanceAdmin
	}
	p, _ := PermissionNamed(requirement)
	return p.Scope
}

// RoleName names a built-in role.
type RoleName string

// The built-in roles. There are no others: a grant names one of these.
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
	{Name: RoleOwner},
	{Name: RoleMaintainer},
	{Name: RoleDeveloper},
	{Name: RoleViewer},
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
