package access

import (
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// GrantID identifies a grant.
type GrantID = id.Of[Grant]

// GrantSource is what made a grant. Each source owns only its own grants,
// so grants from several sources sit side by side.
type GrantSource string

// The sources of a grant.
const (
	// GrantManual is a grant made by hand, by a member managing the project's
	// grants.
	GrantManual GrantSource = "manual"
	// GrantSSO is a grant made by an auth provider's group mapping.
	GrantSSO GrantSource = "sso"
)

// ErrSSOSourced refuses removing by hand what a provider's group mapping
// granted: the directory owns it, and it goes when the person leaves the
// group or the mapping is removed.
var ErrSSOSourced = apperrors.Conflict("sso_sourced", "an auth provider's group mapping granted this; change the mapping or the directory instead")

// Grant is one role held by one member in one project. A member may hold
// several in a project; their permissions there are the union.
type Grant struct {
	ID      GrantID
	Project workspace.ProjectID
	Member  MemberID
	Role    RoleName
	Source  GrantSource
	// Provider is the slug of the auth provider whose group mapping made an
	// `sso` grant; "" for a manual one.
	Provider string
	// CreatedBy is the member who made the grant; zero when none did.
	CreatedBy MemberID
	CreatedAt time.Time
}

// NewGrantInput is what a grant is made of.
type NewGrantInput struct {
	Project   workspace.ProjectID
	Member    Member
	Role      RoleName
	CreatedBy MemberID
	Now       time.Time
}

// ErrMemberDeactivated refuses a grant to a member who is deactivated: they
// can use nothing it gives until they are reactivated.
var ErrMemberDeactivated = apperrors.Conflict("member_deactivated", "the member is deactivated")

// ErrMemberInvited refuses giving a member who has not accepted their
// invitation more than the invitation carries. Until an invitation is
// mailed its link is held by whoever issued it, who can accept it as the
// person invited: a role granted, or the instance admin switch set, before
// the person joins would be given to whoever holds their link.
var ErrMemberInvited = apperrors.Conflict("member_invited",
	"the member has not accepted their invitation yet; they are given this once they have joined")

// NewGrant returns a grant made by hand of a built-in role to a member who
// has joined and is not deactivated: an invited member is refused
// ErrMemberInvited, and holds only what their invitation grants once they
// accept it.
func NewGrant(grantID GrantID, in NewGrantInput) (Grant, error) {
	if _, ok := RoleNamed(in.Role); !ok {
		return Grant{}, UnknownRoleError("/role")
	}
	switch in.Member.Status {
	case MemberDeactivated:
		return Grant{}, ErrMemberDeactivated
	case MemberInvited:
		return Grant{}, ErrMemberInvited
	}
	return Grant{
		ID: grantID, Project: in.Project, Member: in.Member.ID, Role: in.Role, Source: GrantManual,
		CreatedBy: in.CreatedBy, CreatedAt: in.Now,
	}, nil
}

// UnknownRoleError refuses a role that is none of the built-in ones, at
// pointer.
func UnknownRoleError(pointer string) error {
	return apperrors.Invalid("invalid_grant", "the grant is not valid", apperrors.Violation{
		Pointer: pointer, Code: "unknown_role", Detail: "the role is none of owner, maintainer, developer and viewer",
	})
}

// CheckGrantorCap refuses handing out role, by making or removing a grant
// of it or by creating a project token holding it, when held, the
// permissions the caller holds in the project, lacks any permission of the
// role; role_not_allowed names the first one missing. So managing access
// never escalates into more access than the manager has. held are the
// effective permissions of the credential in use, narrowed by a token's
// scopes: a credential cannot hand out what it does not hold.
func CheckGrantorCap(held []Permission, role Role) error {
	for _, p := range role.Permissions {
		if !slices.Contains(held, p) {
			return apperrors.Invalid("role_not_allowed", "the role "+string(role.Name)+" holds the permission "+string(p)+
				", which the caller does not hold in the project").WithExtension("permission", string(p))
		}
	}
	return nil
}

// NewSSOGrant returns the grant of a built-in role in project that the group
// mappings of the auth provider with slug provider made to member.
func NewSSOGrant(grantID GrantID, project workspace.ProjectID, member MemberID, role RoleName, provider string, now time.Time) Grant {
	return Grant{ID: grantID, Project: project, Member: member, Role: role, Source: GrantSSO, Provider: provider, CreatedAt: now}
}
