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

// GrantManual is a grant made by hand, by a member managing the project's
// grants.
const GrantManual GrantSource = "manual"

// Grant is one role held by one member in one project. A member may hold
// several in a project; their permissions there are the union.
type Grant struct {
	ID      GrantID
	Project workspace.ProjectID
	Member  MemberID
	Role    RoleName
	Source  GrantSource
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

// NewGrant returns a grant made by hand of a built-in role to a member who
// is not deactivated; an invited member may hold grants before they join.
func NewGrant(grantID GrantID, in NewGrantInput) (Grant, error) {
	if _, ok := RoleNamed(in.Role); !ok {
		return Grant{}, ErrUnknownRole("/role")
	}
	if in.Member.Status == MemberDeactivated {
		return Grant{}, ErrMemberDeactivated
	}
	return Grant{
		ID: grantID, Project: in.Project, Member: in.Member.ID, Role: in.Role, Source: GrantManual,
		CreatedBy: in.CreatedBy, CreatedAt: in.Now,
	}, nil
}

// ErrUnknownRole refuses a role that is none of the built-in ones, at
// pointer.
func ErrUnknownRole(pointer string) error {
	return apperrors.Invalid("invalid_grant", "the grant is not valid", apperrors.Violation{
		Pointer: pointer, Code: "unknown_role", Detail: "the role is none of owner, maintainer, developer and viewer",
	})
}

// CheckGrantorCap refuses handing out role, by making or removing a grant
// of it or creating a project token holding it, unless held, the
// permissions of whoever does it in the project, holds every permission of
// the role; role_not_allowed names the first missing. So managing access
// never escalates into more access than the manager has.
func CheckGrantorCap(held []Permission, role Role) error {
	for _, p := range role.Permissions {
		if !slices.Contains(held, p) {
			return apperrors.Invalid("role_not_allowed", "the role "+string(role.Name)+" holds the permission "+string(p)+
				", which the caller does not hold in the project").WithExtension("permission", string(p))
		}
	}
	return nil
}
