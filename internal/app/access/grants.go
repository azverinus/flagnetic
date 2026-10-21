package access

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// GrantStore keeps the grants of projects.
type GrantStore interface {
	// LockMember returns the member, locked until the unit of work ends,
	// and whether they hold a password; ErrNotFound when none has the
	// identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
	// ProjectGrants returns every grant of project, oldest first.
	ProjectGrants(ctx context.Context, project workspace.ProjectID) ([]access.Grant, error)
	// CreateGrant stores g; one the member holds already from the same
	// source is refused already_granted.
	CreateGrant(ctx context.Context, g access.Grant) error
	// LockGrant returns the grant of project with the identifier, locked
	// until the unit of work ends; ErrNotFound when the project holds none.
	LockGrant(ctx context.Context, project workspace.ProjectID, grant access.GrantID) (access.Grant, error)
	// DeleteGrant removes the grant of project with the identifier.
	DeleteGrant(ctx context.Context, project workspace.ProjectID, grant access.GrantID) (access.Grant, error)
}

// ErrGrantNotFound answers an identifier no grant of the project has.
var ErrGrantNotFound = apperrors.NotFound("not_found", "no grant of the project has this identifier")

// ProjectRef is the project a command addresses, as the request found it,
// with what the caller holds there.
type ProjectRef struct {
	ID  workspace.ProjectID
	Key string
	// Held are the caller's effective permissions in the project, which
	// cap the roles they may hand out.
	Held []access.Permission
}

// ListGrants answers the grants of a project.
type ListGrants struct{ store GrantStore }

// NewListGrants returns the query.
func NewListGrants(store GrantStore) *ListGrants { return &ListGrants{store: store} }

// Handle returns every grant of project, oldest first.
func (h *ListGrants) Handle(ctx context.Context, project workspace.ProjectID) ([]access.Grant, error) {
	return h.store.ProjectGrants(ctx, project)
}

// CreateGrantCommand grants Member the role Role in Project.
type CreateGrantCommand struct {
	Caller  access.Caller
	Project ProjectRef
	Member  access.MemberID
	Role    access.RoleName
	Request governance.Request
}

// CreateGrant grants a member a role in a project.
type CreateGrant struct {
	tx    Transactor
	store GrantStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewCreateGrant returns the handler.
func NewCreateGrant(tx Transactor, store GrantStore, audit AuditLog, c clock.Clock, ids *id.Source) *CreateGrant {
	return &CreateGrant{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle makes the grant and records grant.created in the project. A role
// holding a permission the caller lacks in the project is refused
// role_not_allowed, an unknown member or role invalid_grant, a deactivated
// member member_deactivated, a grant the member holds already
// already_granted.
func (h *CreateGrant) Handle(ctx context.Context, cmd CreateGrantCommand) (access.Grant, error) {
	role, ok := access.RoleNamed(cmd.Role)
	if !ok {
		return access.Grant{}, access.ErrUnknownRole("/role")
	}
	if err := access.CheckGrantorCap(cmd.Project.Held, role); err != nil {
		return access.Grant{}, err
	}
	var grant access.Grant
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		// Locked, so a deactivation made meanwhile is seen before the grant
		// is stored, or waits for it.
		member, _, err := h.store.LockMember(ctx, cmd.Member)
		if errors.Is(err, ErrNotFound) {
			return errUnknownMember
		}
		if err != nil {
			return err
		}
		now := h.clock.Now()
		grant, err = access.NewGrant(id.New[access.Grant](h.ids), access.NewGrantInput{
			Project: cmd.Project.ID, Member: member, Role: cmd.Role, CreatedBy: creatorOf(cmd.Caller), Now: now,
		})
		if err != nil {
			return err
		}
		if err = h.store.CreateGrant(ctx, grant); err != nil {
			return err
		}
		return recordAll(ctx, h.audit, h.ids, now, cmd.Request, grantRecord("grant.created", cmd.Project, grant, member, false))
	})
	return grant, err
}

// errUnknownMember refuses a grant to a member the instance does not hold.
var errUnknownMember = apperrors.Invalid("invalid_grant", "the grant is not valid", apperrors.Violation{
	Pointer: "/member", Code: "unknown_member", Detail: "no member has this identifier",
})

// creatorOf returns the member making a change as caller, zero for a
// credential acting for none.
func creatorOf(caller access.Caller) access.MemberID {
	if caller.Member == nil {
		return access.MemberID{}
	}
	return caller.Member.ID
}

// DeleteGrantCommand removes the grant Grant of Project.
type DeleteGrantCommand struct {
	Project ProjectRef
	Grant   access.GrantID
	Request governance.Request
}

// DeleteGrant removes one grant, leaving the member's others.
type DeleteGrant struct {
	tx    Transactor
	store GrantStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewDeleteGrant returns the handler.
func NewDeleteGrant(tx Transactor, store GrantStore, audit AuditLog, c clock.Clock, ids *id.Source) *DeleteGrant {
	return &DeleteGrant{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle removes the grant and records grant.deleted in the project. The
// grant of a role holding a permission the caller lacks in the project is
// refused role_not_allowed, so nobody narrows access beyond their own
// reach; an unknown grant is ErrGrantNotFound. Removing the last owner
// grant is allowed: an instance admin can always step in.
func (h *DeleteGrant) Handle(ctx context.Context, cmd DeleteGrantCommand) error {
	return h.tx.Within(ctx, func(ctx context.Context) error {
		grant, err := h.store.LockGrant(ctx, cmd.Project.ID, cmd.Grant)
		if errors.Is(err, ErrNotFound) {
			return ErrGrantNotFound
		}
		if err != nil {
			return err
		}
		role, _ := access.RoleNamed(grant.Role)
		if err = access.CheckGrantorCap(cmd.Project.Held, role); err != nil {
			return err
		}
		member, _, err := h.store.LockMember(ctx, grant.Member)
		if err != nil {
			return err
		}
		if _, err = h.store.DeleteGrant(ctx, cmd.Project.ID, cmd.Grant); err != nil {
			return err
		}
		return recordAll(ctx, h.audit, h.ids, h.clock.Now(), cmd.Request, grantRecord("grant.deleted", cmd.Project, grant, member, true))
	})
}

// grantRecord is the audit record of action on g of member in project,
// keyed by the member's email; the grant's values are before the change
// when it removed the grant, after it otherwise.
func grantRecord(action string, project ProjectRef, g access.Grant, member access.Member, removed bool) governance.Record {
	values := map[string]any{"member": g.Member.String(), "email": member.Email, "role": g.Role, "source": g.Source}
	r := governance.Record{
		Project: project.ID, Action: action,
		Entity: governance.Entity{Kind: "grant", ID: g.ID.String(), Key: member.Email},
	}
	if removed {
		r.Before = values
	} else {
		r.After = values
	}
	return r
}

// ProjectMember is a member holding grants in a project, with those grants.
type ProjectMember struct {
	Member access.Member
	Grants []access.Grant
}

// ProjectMemberDirectory reads who holds grants in projects.
type ProjectMemberDirectory interface {
	// ProjectMembers returns the members holding grants in project, ordered
	// by email, each with their grants there oldest first, read in one
	// query.
	ProjectMembers(ctx context.Context, project workspace.ProjectID) ([]ProjectMember, error)
	// ProjectMemberCounts returns how many active members hold a grant in
	// each of projects, read in one query; a project none holds is absent.
	ProjectMemberCounts(ctx context.Context, projects []workspace.ProjectID) (map[workspace.ProjectID]int, error)
}

// ListProjectMembers answers who holds grants in a project.
type ListProjectMembers struct{ store ProjectMemberDirectory }

// NewListProjectMembers returns the query.
func NewListProjectMembers(store ProjectMemberDirectory) *ListProjectMembers {
	return &ListProjectMembers{store: store}
}

// Handle returns the members holding grants in project with their grants.
func (h *ListProjectMembers) Handle(ctx context.Context, project workspace.ProjectID) ([]ProjectMember, error) {
	return h.store.ProjectMembers(ctx, project)
}

// CountProjectMembers answers how many members work in projects.
type CountProjectMembers struct{ store ProjectMemberDirectory }

// NewCountProjectMembers returns the query.
func NewCountProjectMembers(store ProjectMemberDirectory) *CountProjectMembers {
	return &CountProjectMembers{store: store}
}

// Handle returns how many active members hold a grant in each of projects,
// whatever their number, at the cost of one query; a project none holds is
// absent.
func (h *CountProjectMembers) Handle(ctx context.Context, projects []workspace.ProjectID) (map[workspace.ProjectID]int, error) {
	return h.store.ProjectMemberCounts(ctx, projects)
}
