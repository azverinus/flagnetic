package identity

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MappingStore keeps the group mappings of auth providers and the groups the
// members last claimed at them, and reads the groups a member is in at a
// provider: those they last claimed and the provider's SCIM groups they are
// members of.
type MappingStore interface {
	// GroupMappings returns the provider's mappings in order.
	GroupMappings(ctx context.Context, provider identity.ProviderID) ([]identity.GroupMapping, error)
	// ReplaceGroupMappings stores mappings as the provider's whole list.
	ReplaceGroupMappings(ctx context.Context, provider identity.ProviderID, mappings []identity.GroupMapping) error
	// StoreGroupClaim stores groups as what member claimed at provider at at,
	// replacing the claim before.
	StoreGroupClaim(ctx context.Context, provider identity.ProviderID, member identity.MemberID, groups []string, at time.Time) error
	// KnownGroups returns the groups of every member who claimed groups at the
	// provider or is a member of one of its SCIM groups: the union of both.
	KnownGroups(ctx context.Context, provider identity.ProviderID) ([]GroupClaim, error)
	// GroupsOf returns the groups member is in at provider: the union of what
	// they last claimed there and the names of its SCIM groups they are
	// members of.
	GroupsOf(ctx context.Context, provider identity.ProviderID, member identity.MemberID) ([]string, error)
}

// GroupClaim is the groups a member is in at a provider.
type GroupClaim struct {
	Member identity.MemberID
	Groups []string
}

// ProjectDirectory finds projects by key.
type ProjectDirectory interface {
	// ProjectIDs returns the identifier of each project with one of keys; a
	// key no project has is absent.
	ProjectIDs(ctx context.Context, keys []string) (map[string]workspace.ProjectID, error)
}

// ProviderAccess is what the identity context asks of the access context to
// give members what a provider's group mappings give them.
type ProviderAccess interface {
	// KnownRole reports whether name is a built-in role.
	KnownRole(name string) bool
	// ReplaceAccess makes the access the provider's mappings give the member
	// as listed, in the unit of work ctx carries.
	ReplaceAccess(ctx context.Context, cmd AccessCommand) (AccessChange, error)
}

// AccessCommand is the access a provider's mappings give a member now.
type AccessCommand struct {
	Provider      identity.Provider
	Member        identity.MemberID
	Grants        []identity.ProjectRole
	InstanceAdmin bool
	// Actor is who the audit trail names for the changes: the provider when
	// it is the zero actor, the SCIM token for a SCIM group change.
	Actor     governance.Actor
	RequestID string
}

// AccessChange is what replacing a member's access changed; see the access
// context for what each counts.
type AccessChange struct {
	Granted, Revoked  int
	KeptInstanceAdmin bool
}

// GroupMappingDeps are what the group mapping use cases run on.
type GroupMappingDeps struct {
	Tx        Transactor
	Providers ProviderStore
	Mappings  MappingStore
	Projects  ProjectDirectory
	Access    ProviderAccess
	Audit     AuditLog
	Clock     clock.Clock
	IDs       *id.Source
	Logger    *slog.Logger
}

func (d GroupMappingDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// replaceAccess gives member what the mappings give a person in groups at
// provider p, and logs when the last active instance admin was kept. The
// changes are recorded for actor, the provider when it is the zero actor.
func (d GroupMappingDeps) replaceAccess(ctx context.Context, p identity.Provider, member identity.MemberID,
	mappings []identity.GroupMapping, groups []string, actor governance.Actor, requestID string,
) (AccessChange, error) {
	entitled := identity.Entitle(mappings, groups)
	change, err := d.Access.ReplaceAccess(ctx, AccessCommand{
		Provider: p, Member: member, Grants: entitled.Grants, InstanceAdmin: entitled.InstanceAdmin, Actor: actor, RequestID: requestID,
	})
	if err == nil && change.KeptInstanceAdmin {
		d.Logger.WarnContext(ctx, "an auth provider's group mapping no longer makes a member instance admin, but they are the last active instance admin, so they stay",
			"provider", p.Slug, "member", member.String())
	}
	return change, err
}

// GetGroupMappings answers a provider's group mappings.
type GetGroupMappings struct {
	providers ProviderStore
	mappings  MappingStore
}

// NewGetGroupMappings returns the query.
func NewGetGroupMappings(providers ProviderStore, mappings MappingStore) *GetGroupMappings {
	return &GetGroupMappings{providers: providers, mappings: mappings}
}

// Handle returns the mappings of the provider with slug, in order; an unknown
// provider is not found.
func (h *GetGroupMappings) Handle(ctx context.Context, slug string) ([]identity.GroupMapping, error) {
	p, err := providerOf(ctx, h.providers, slug)
	if err != nil {
		return nil, err
	}
	return h.mappings.GroupMappings(ctx, p.ID)
}

// ReplaceGroupMappings replaces a provider's group mappings.
type ReplaceGroupMappings struct{ d GroupMappingDeps }

// NewReplaceGroupMappings returns the handler.
func NewReplaceGroupMappings(d GroupMappingDeps) *ReplaceGroupMappings {
	return &ReplaceGroupMappings{d: d}
}

// ReplaceGroupMappingsCommand replaces the mappings of the provider Slug with
// Items.
type ReplaceGroupMappingsCommand struct {
	Slug    string
	Items   []identity.GroupMappingInput
	Request governance.Request
}

// GroupMappingsRun is what replacing the mappings did to the members: the
// access made and removed, and the members kept as instance admin because
// they are the last active one.
type GroupMappingsRun struct {
	Granted, Revoked, KeptInstanceAdmin int
}

// Handle replaces the provider's mappings and, in the same unit of work, runs
// them again for every member who claimed groups at the provider or is in one
// of its SCIM groups, so a mapping removed stops granting at once. An unknown provider is not found;
// mappings that break a rule, or name a project no instance holds, are
// invalid_group_mappings. It records group_mappings.updated unless nothing
// changed.
func (h *ReplaceGroupMappings) Handle(ctx context.Context, cmd ReplaceGroupMappingsCommand) (GroupMappingsRun, error) {
	items, checkErr := identity.CheckGroupMappings(cmd.Items, h.d.Access.KnownRole)
	if checkErr != nil {
		return GroupMappingsRun{}, checkErr
	}
	var run GroupMappingsRun
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		p, err := lockProvider(ctx, h.d.Providers, cmd.Slug)
		if err != nil {
			return err
		}
		mappings, err := h.resolve(ctx, items)
		if err != nil {
			return err
		}
		before, err := h.d.Mappings.GroupMappings(ctx, p.ID)
		if err != nil {
			return err
		}
		if err = h.d.Mappings.ReplaceGroupMappings(ctx, p.ID, mappings); err != nil {
			return err
		}
		claims, err := h.d.Mappings.KnownGroups(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, c := range claims {
			change, replaceErr := h.d.replaceAccess(ctx, p, c.Member, mappings, c.Groups, governance.Actor{}, cmd.Request.ID)
			if replaceErr != nil {
				return replaceErr
			}
			run.Granted += change.Granted
			run.Revoked += change.Revoked
			if change.KeptInstanceAdmin {
				run.KeptInstanceAdmin++
			}
		}
		if sameMappings(before, mappings) && run == (GroupMappingsRun{}) {
			return nil
		}
		return h.record(ctx, p, before, mappings, run, cmd.Request)
	})
	return run, err
}

// resolve turns the checked mappings into stored ones, finding each project
// by its key; a key no project has is invalid_group_mappings at the mapping's
// project.
func (h *ReplaceGroupMappings) resolve(ctx context.Context, items []identity.GroupMappingInput) ([]identity.GroupMapping, error) {
	var keys []string
	for _, m := range items {
		if m.Project != "" {
			keys = append(keys, m.Project)
		}
	}
	projects := map[string]workspace.ProjectID{}
	if len(keys) > 0 {
		found, err := h.d.Projects.ProjectIDs(ctx, keys)
		if err != nil {
			return nil, err
		}
		projects = found
	}
	var unknown []apperrors.Violation
	out := make([]identity.GroupMapping, len(items))
	for i, m := range items {
		out[i] = identity.GroupMapping{Group: m.Group, ProjectKey: m.Project, Role: m.Role, InstanceAdmin: m.InstanceAdmin}
		if m.Project == "" {
			continue
		}
		project, ok := projects[m.Project]
		if !ok {
			unknown = append(unknown, apperrors.Violation{
				Pointer: "/items/" + strconv.Itoa(i) + "/project", Code: "unknown_project", Detail: "no project has this key",
			})
		}
		out[i].Project = project
	}
	if len(unknown) > 0 {
		return nil, apperrors.Invalid("invalid_group_mappings", "the group mappings break a rule; the violations name each field", unknown...)
	}
	return out, nil
}

func (h *ReplaceGroupMappings) record(ctx context.Context, p identity.Provider, before, after []identity.GroupMapping,
	run GroupMappingsRun, req governance.Request,
) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), h.d.now(), req, governance.Record{
		Action: "group_mappings.updated",
		Entity: governance.Entity{Kind: "auth_provider", ID: p.ID.String(), Key: p.Slug},
		Before: map[string]any{"items": mappingValues(before)},
		After: map[string]any{
			"items": mappingValues(after), "granted": run.Granted, "revoked": run.Revoked, "kept_instance_admin": run.KeptInstanceAdmin,
		},
	})
	if err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}

// mappingValues is what the audit log records of mappings: the project by its
// key.
func mappingValues(mappings []identity.GroupMapping) []map[string]any {
	out := make([]map[string]any, len(mappings))
	for i, m := range mappings {
		if m.InstanceAdmin {
			out[i] = map[string]any{"group": m.Group, "instance_admin": true}
			continue
		}
		out[i] = map[string]any{"group": m.Group, "project": m.ProjectKey, "role": m.Role}
	}
	return out
}

// sameMappings reports whether two lists of mappings are the same in the same
// order.
func sameMappings(a, b []identity.GroupMapping) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Group != b[i].Group || a[i].Project != b[i].Project || a[i].Role != b[i].Role || a[i].InstanceAdmin != b[i].InstanceAdmin {
			return false
		}
	}
	return true
}
