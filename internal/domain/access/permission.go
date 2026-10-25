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
	// MembersOnly marks a permission that changes who has access or whose
	// review a change needs, which a project token never exercises whatever
	// its role, so a pipeline credential cannot be turned into a way in or
	// around review.
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
	// PermEnvironmentProtect protects an environment, so its ruleset changes
	// only through approved change requests, or stops protecting it.
	PermEnvironmentProtect Permission = "environment.protect"
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
	// PermFlagConfigurationUpdate replaces a flag's configuration in an
	// environment.
	PermFlagConfigurationUpdate Permission = "flag_configuration.update"
	// PermFlagConfigurationKill switches a flag off in an environment with
	// the kill switch.
	PermFlagConfigurationKill Permission = "flag_configuration.kill"
	// PermFlagConfigurationRevert writes a flag's configuration in an
	// environment again as an earlier version had it. It is apart from
	// update so that whoever is on call may undo a change without being
	// allowed to write rules.
	PermFlagConfigurationRevert Permission = "flag_configuration.revert"
	// PermSegmentEdit creates segments and changes them.
	PermSegmentEdit Permission = "segment.edit"
	// PermSegmentDelete deletes segments.
	PermSegmentDelete Permission = "segment.delete"
	// PermSDKKeyManage creates, renames, rotates and revokes an
	// environment's SDK keys and edits their allowed origins. Reading them
	// is project.read: a listed key never carries its value.
	PermSDKKeyManage Permission = "sdk_key.manage"
	// PermExperimentEdit creates experiments, changes them, moves them
	// through their statuses and acknowledges their alerts.
	PermExperimentEdit Permission = "experiment.edit"
	// PermExperimentDelete deletes draft experiments.
	PermExperimentDelete Permission = "experiment.delete"
	// PermChangeRequestPropose proposes changes as change requests: creates
	// them, edits, rebases, opens and cancels its holder's own, and copies
	// any into a new draft.
	PermChangeRequestPropose Permission = "change_request.propose"
	// PermWebhookManage creates, reads, edits and deletes a project's
	// webhook subscriptions. Reading them needs it too: a subscription
	// says where a project's changes are sent.
	PermWebhookManage Permission = "webhook.manage"
)

// catalog is every permission an operation can require. Each context adds
// its permissions with the operations that need them, and writes them into
// the built-in roles below.
var catalog = []PermissionSpec{
	{Name: PermProjectRead, Scope: ScopeRead},
	{Name: PermProjectUpdate, Scope: ScopeProjectAdmin},
	{Name: PermEnvironmentManage, Scope: ScopeProjectAdmin},
	// Protection decides whose review a change to an environment needs, so
	// like access itself it stays with members: a pipeline holding it could
	// unprotect an environment, write it and protect it again.
	{Name: PermEnvironmentProtect, Scope: ScopeProjectAdmin, MembersOnly: true},
	// Unit types and attributes are what flags target, so changing them
	// falls under the scope of a pipeline that writes flags.
	{Name: PermDeclarationManage, Scope: ScopeFlagsWrite},
	{Name: PermGrantManage, Scope: ScopeProjectAdmin, MembersOnly: true},
	{Name: PermProjectTokenManage, Scope: ScopeProjectAdmin, MembersOnly: true},
	{Name: PermFlagEdit, Scope: ScopeFlagsWrite},
	{Name: PermFlagArchive, Scope: ScopeFlagsWrite},
	{Name: PermFlagConfigurationUpdate, Scope: ScopeFlagsWrite},
	{Name: PermFlagConfigurationKill, Scope: ScopeFlagsWrite},
	{Name: PermFlagConfigurationRevert, Scope: ScopeFlagsWrite},
	{Name: PermSegmentEdit, Scope: ScopeFlagsWrite},
	{Name: PermSegmentDelete, Scope: ScopeFlagsWrite},
	// A pipeline provisioning an environment gives its services keys, so a
	// project token may hold it: an SDK key reaches one environment's
	// delivery and never changes who has access to the project.
	{Name: PermSDKKeyManage, Scope: ScopeProjectAdmin},
	// An experiment is run by the pipeline that ships the flag it compares,
	// under a scope of its own so a token can be allowed to run experiments
	// and not to write rules, or the other way round.
	{Name: PermExperimentEdit, Scope: ScopeExperimentsWrite},
	{Name: PermExperimentDelete, Scope: ScopeExperimentsWrite},
	// Proposing changes nothing SDKs receive, but it is how a pipeline
	// writing flags reaches an environment whose changes are reviewed, so it
	// falls under the same scope; deciding stays with members.
	{Name: PermChangeRequestPropose, Scope: ScopeFlagsWrite},
	// A pipeline provisioning a project may subscribe its tools to the
	// project's changes; a subscription changes nothing SDKs receive and
	// nobody's access.
	{Name: PermWebhookManage, Scope: ScopeProjectAdmin},
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
		PermProjectRead, PermProjectUpdate, PermEnvironmentManage, PermEnvironmentProtect, PermDeclarationManage,
		PermGrantManage, PermProjectTokenManage, PermFlagEdit, PermFlagArchive, PermFlagConfigurationUpdate,
		PermFlagConfigurationKill, PermFlagConfigurationRevert, PermSegmentEdit, PermSegmentDelete, PermSDKKeyManage,
		PermExperimentEdit, PermExperimentDelete, PermChangeRequestPropose, PermWebhookManage,
	}},
	{Name: RoleMaintainer, Permissions: []Permission{
		PermProjectRead, PermEnvironmentManage, PermDeclarationManage, PermFlagEdit, PermFlagArchive,
		PermFlagConfigurationUpdate, PermFlagConfigurationKill, PermFlagConfigurationRevert, PermSegmentEdit, PermSegmentDelete,
		PermSDKKeyManage, PermExperimentEdit, PermExperimentDelete, PermChangeRequestPropose, PermWebhookManage,
	}},
	{Name: RoleDeveloper, Permissions: []Permission{
		PermProjectRead, PermFlagEdit, PermFlagConfigurationUpdate, PermFlagConfigurationKill, PermFlagConfigurationRevert,
		PermSegmentEdit, PermExperimentEdit, PermChangeRequestPropose,
	}},
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
