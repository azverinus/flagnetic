package access

import (
	"context"
	"errors"
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ProviderAccessStore is what replacing the access an auth provider's group
// mappings made needs.
type ProviderAccessStore interface {
	MemberChanger
	GrantStore
	// ProviderGrants returns the grants the group mappings of the provider
	// with this slug made to member, in every project.
	ProviderGrants(ctx context.Context, member access.MemberID, provider string) ([]access.Grant, error)
}

// ProviderAccess makes the grants and the instance admin an auth provider's
// group mappings give a member, and takes away those it no longer gives. It
// acts in the unit of work its context carries, so the identity context makes
// it part of a sign-in or of replacing the mappings.
type ProviderAccess struct {
	store ProviderAccessStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewProviderAccess returns the service.
func NewProviderAccess(store ProviderAccessStore, audit AuditLog, c clock.Clock, ids *id.Source) *ProviderAccess {
	return &ProviderAccess{store: store, audit: audit, clock: c, ids: ids}
}

// KnownRole reports whether name is one of the built-in roles.
func (*ProviderAccess) KnownRole(name string) bool {
	_, ok := access.RoleNamed(access.RoleName(name))
	return ok
}

// ProviderRef names an auth provider: its identifier, which the audit trail
// names as the actor, and its slug, which grants and members keep.
type ProviderRef struct {
	ID   string
	Slug string
}

// ProviderRole is a role in a project.
type ProviderRole struct {
	Project workspace.ProjectID
	Role    access.RoleName
}

// ProviderAccessCommand is the access the provider's mappings give a member
// now.
type ProviderAccessCommand struct {
	Provider ProviderRef
	Member   access.MemberID
	// Grants are the roles in projects the mappings give, each once.
	Grants        []ProviderRole
	InstanceAdmin bool
	// Actor is who the audit trail names for the changes; the provider when
	// it is the zero actor. A SCIM group change names the SCIM token.
	Actor     governance.Actor
	RequestID string
}

// ProviderAccessResult is what replacing a member's access changed.
type ProviderAccessResult struct {
	// Granted counts the grants made and the promotions to instance admin,
	// Revoked the grants removed and the demotions.
	Granted, Revoked int
	// KeptInstanceAdmin is whether the member stayed instance admin although
	// the mappings no longer give it, because they are the last active
	// instance admin.
	KeptInstanceAdmin bool
}

// Replace makes the grants of the provider's mappings to the member those the
// command lists, removing the provider's others, and makes them instance
// admin or no longer, when the provider is the one that made them. Grants of
// other sources, and an instance admin made by hand or by another provider,
// are left alone, so each source owns what it granted. A deactivated member
// keeps none of the provider's grants and gains none. The last active
// instance admin is never demoted: that is reported, not refused. Every
// change is recorded for the command's actor, the provider by default. A member who loses
// grant.manage in a project loses the grant of each invitation they issued
// to it.
func (a *ProviderAccess) Replace(ctx context.Context, cmd ProviderAccessCommand) (ProviderAccessResult, error) {
	var res ProviderAccessResult
	if err := a.store.LockInstanceAdmins(ctx); err != nil {
		return res, err
	}
	member, _, err := a.store.LockMember(ctx, cmd.Member)
	if err != nil {
		return res, err
	}
	now := a.clock.Now()
	wanted, wantAdmin := cmd.Grants, cmd.InstanceAdmin
	if member.Status == access.MemberDeactivated {
		wanted, wantAdmin = nil, false
	}
	held, err := a.store.ProviderGrants(ctx, member.ID, cmd.Provider.Slug)
	if err != nil {
		return res, err
	}

	var records []governance.Record
	var lostIn []workspace.ProjectID
	for _, g := range held {
		if slices.Contains(wanted, ProviderRole{Project: g.Project, Role: g.Role}) {
			continue
		}
		if _, err = a.store.DeleteGrant(ctx, g.Project, g.ID); err != nil {
			return res, err
		}
		records = append(records, grantRecord("grant.deleted", ProjectRef{ID: g.Project}, g, member, true))
		lostIn = append(lostIn, g.Project)
		res.Revoked++
	}
	for _, w := range wanted {
		if slices.ContainsFunc(held, func(g access.Grant) bool { return g.Project == w.Project && g.Role == w.Role }) {
			continue
		}
		g := access.NewSSOGrant(id.New[access.Grant](a.ids), w.Project, member.ID, w.Role, cmd.Provider.Slug, now)
		if err = a.store.CreateGrant(ctx, g); err != nil {
			return res, err
		}
		records = append(records, grantRecord("grant.created", ProjectRef{ID: g.Project}, g, member, false))
		res.Granted++
	}

	after := member
	switch {
	case wantAdmin && !member.InstanceAdmin:
		after.InstanceAdmin, after.InstanceAdminProvider = true, cmd.Provider.Slug
	case !wantAdmin && member.InstanceAdmin && member.InstanceAdminProvider == cmd.Provider.Slug:
		after.InstanceAdmin, after.InstanceAdminProvider = false, ""
	}
	if after != member {
		err = keepAnInstanceAdmin(ctx, a.store, member, after)
		switch {
		case errors.Is(err, access.ErrLastInstanceAdmin):
			res.KeptInstanceAdmin, after = true, member
		case err != nil:
			return res, err
		default:
			if err = a.store.SaveMember(ctx, after, now); err != nil {
				return res, err
			}
			records = append(records, instanceAdminRecord(after, member, cmd.Provider.Slug))
			if after.InstanceAdmin {
				res.Granted++
			} else {
				res.Revoked++
			}
		}
	}

	// The invitations the member issued are judged as they now stand: after
	// the changes above.
	if member.InstanceAdmin && !after.InstanceAdmin {
		lostIn = []workspace.ProjectID{{}}
	}
	var judged []workspace.ProjectID
	for _, project := range lostIn {
		if slices.Contains(judged, project) {
			continue
		}
		judged = append(judged, project)
		dropped, dropErr := dropInvitationGrants(ctx, a.store, after, project)
		if dropErr != nil {
			return res, dropErr
		}
		records = append(records, dropped...)
	}
	req := governance.Request{Actor: cmd.Actor, ID: cmd.RequestID}
	if req.Actor.Kind == "" {
		req.Actor = governance.SSOActor(cmd.Provider.ID, cmd.Provider.Slug)
	}
	return res, recordAll(ctx, a.audit, a.ids, now, req, records...)
}

// instanceAdminRecord is the audit record of the provider promoting or
// demoting a member: promoted is the member as they are now, was as they were.
func instanceAdminRecord(now, was access.Member, provider string) governance.Record {
	if now.InstanceAdmin {
		return governance.Record{
			Action: "member.promoted", Entity: memberEntity(now),
			Before: map[string]any{"instance_admin": false},
			After:  map[string]any{"instance_admin": true, "source": access.GrantSSO, "provider": provider},
		}
	}
	return governance.Record{
		Action: "member.demoted", Entity: memberEntity(now),
		Before: map[string]any{"instance_admin": true, "source": access.GrantSSO, "provider": was.InstanceAdminProvider},
		After:  map[string]any{"instance_admin": false},
	}
}
