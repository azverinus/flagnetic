package identity

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SCIMGroupRecord is a SCIM group as it is stored: its members ordered by
// their identifier.
type SCIMGroupRecord struct {
	ID       identity.SCIMGroupID
	Provider identity.ProviderID
	identity.GroupState
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GroupRef names a SCIM group a member is in.
type GroupRef struct {
	ID          identity.SCIMGroupID
	DisplayName string
}

// Refusals of a SCIM group write, which the SCIM adapter answers 409
// `uniqueness`.
var (
	// ErrGroupNameTaken refuses a display name another group of the provider
	// has.
	ErrGroupNameTaken = apperrors.Conflict("group_name_taken", "another group of this provider has this displayName")
	// ErrGroupExternalIDTaken refuses an external ID another group of the
	// provider holds.
	ErrGroupExternalIDTaken = apperrors.Conflict("group_external_id_taken", "another group of this provider holds this externalId")
)

// SCIMGroupStore keeps the SCIM groups of auth providers. A group of another
// provider is never found.
type SCIMGroupStore interface {
	// CreateSCIMGroup stores g; a taken name or external ID is
	// ErrGroupNameTaken or ErrGroupExternalIDTaken.
	CreateSCIMGroup(ctx context.Context, g SCIMGroupRecord) error
	// SCIMGroup returns the provider's group; identity.ErrGroupNotFound when
	// the provider holds none with the identifier.
	SCIMGroup(ctx context.Context, provider identity.ProviderID, group identity.SCIMGroupID) (SCIMGroupRecord, error)
	// SCIMGroups returns the page of the provider's groups satisfying the
	// filter, ordered by name, from the 1-based startIndex, count at most,
	// and how many satisfy it in all.
	SCIMGroups(ctx context.Context, provider identity.ProviderID, f identity.GroupFilter, startIndex, count int) ([]SCIMGroupRecord, int, error)
	// SaveSCIMGroup stores g in place of the group with its identifier, its
	// members replaced; refusals as CreateSCIMGroup's.
	SaveSCIMGroup(ctx context.Context, g SCIMGroupRecord) error
	// DeleteSCIMGroup removes the group and its memberships.
	DeleteSCIMGroup(ctx context.Context, group identity.SCIMGroupID) error
	// GroupsOfMembers returns the provider's groups each of members is in,
	// ordered by name; a member in none is absent.
	GroupsOfMembers(ctx context.Context, provider identity.ProviderID, members []identity.MemberID) (map[identity.MemberID][]GroupRef, error)
}

// SCIMGroupDeps are what the SCIM Groups use cases run on.
type SCIMGroupDeps struct {
	Tx        Transactor
	Providers ProviderStore
	Groups    SCIMGroupStore
	Members   SCIMMembers
	Mappings  MappingStore
	Access    ProviderAccess
	Audit     AuditLog
	Clock     clock.Clock
	IDs       *id.Source
	Logger    *slog.Logger
}

func (d SCIMGroupDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// ListSCIMGroups lists a provider's groups.
type ListSCIMGroups struct{ d SCIMGroupDeps }

// NewListSCIMGroups returns the handler.
func NewListSCIMGroups(d SCIMGroupDeps) *ListSCIMGroups { return &ListSCIMGroups{d: d} }

// GroupPage is a page of SCIM groups and how many match in all.
type GroupPage struct {
	Groups []SCIMGroupRecord
	Total  int
}

// Handle returns the page of the caller's provider's groups the filter
// selects. The filter is judged before anything is read: invalidFilter.
func (h *ListSCIMGroups) Handle(ctx context.Context, caller SCIMCaller, rawFilter string, startIndex, count int) (GroupPage, error) {
	f, err := identity.ParseGroupFilter(rawFilter)
	if err != nil {
		return GroupPage{}, err
	}
	groups, total, err := h.d.Groups.SCIMGroups(ctx, caller.Provider, f, max(startIndex, 1), count)
	return GroupPage{Groups: groups, Total: total}, err
}

// GetSCIMGroup reads one of a provider's groups.
type GetSCIMGroup struct{ d SCIMGroupDeps }

// NewGetSCIMGroup returns the handler.
func NewGetSCIMGroup(d SCIMGroupDeps) *GetSCIMGroup { return &GetSCIMGroup{d: d} }

// Handle returns the group; identity.ErrGroupNotFound when the caller's
// provider does not hold it.
func (h *GetSCIMGroup) Handle(ctx context.Context, caller SCIMCaller, group identity.SCIMGroupID) (SCIMGroupRecord, error) {
	return h.d.Groups.SCIMGroup(ctx, caller.Provider, group)
}

// CreateSCIMGroup creates a group of the caller's provider.
type CreateSCIMGroup struct{ d SCIMGroupDeps }

// NewCreateSCIMGroup returns the handler.
func NewCreateSCIMGroup(d SCIMGroupDeps) *CreateSCIMGroup { return &CreateSCIMGroup{d: d} }

// Handle creates the group with its members, who must be members the
// caller's provider provisions (invalidValue otherwise), runs the provider's
// group mappings for them, and records scim_group.created.
func (h *CreateSCIMGroup) Handle(ctx context.Context, req SCIMRequest, state identity.GroupState) (SCIMGroupRecord, error) {
	var out SCIMGroupRecord
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		p, err := lockProvider(ctx, h.d.Providers, req.Caller.ProviderSlug)
		if err != nil {
			return err
		}
		if err = h.d.checkJoining(ctx, req, nil, state.Members); err != nil {
			return err
		}
		now := h.d.now()
		out = SCIMGroupRecord{
			ID: id.New[identity.SCIMGroup](h.d.IDs), Provider: p.ID, GroupState: state, CreatedAt: now, UpdatedAt: now,
		}
		out.Members = sortedMembers(out.Members)
		if err = h.d.Groups.CreateSCIMGroup(ctx, out); err != nil {
			return err
		}
		if err = h.d.rerun(ctx, p, req, identity.TouchedMembers(identity.GroupState{}, out.GroupState)); err != nil {
			return err
		}
		return h.d.record(ctx, req, governance.Record{
			Action: "scim_group.created", Entity: groupEntity(out), After: groupValues(out.GroupState),
		})
	})
	return out, err
}

// ReplaceSCIMGroup replaces a group's name, external ID and members.
type ReplaceSCIMGroup struct{ d SCIMGroupDeps }

// NewReplaceSCIMGroup returns the handler.
func NewReplaceSCIMGroup(d SCIMGroupDeps) *ReplaceSCIMGroup { return &ReplaceSCIMGroup{d: d} }

// Handle makes the group state, keeping its external ID when keepExternalID
// (the request did not send one). Members who join must be provisioned by
// the caller's provider.
func (h *ReplaceSCIMGroup) Handle(ctx context.Context, req SCIMRequest, group identity.SCIMGroupID, state identity.GroupState, keepExternalID bool) (SCIMGroupRecord, error) {
	return h.d.change(ctx, req, group, func(before identity.GroupState) (identity.GroupState, error) {
		if keepExternalID {
			state.ExternalID = before.ExternalID
		}
		return state, nil
	})
}

// PatchSCIMGroup applies a PatchOp to a group.
type PatchSCIMGroup struct{ d SCIMGroupDeps }

// NewPatchSCIMGroup returns the handler.
func NewPatchSCIMGroup(d SCIMGroupDeps) *PatchSCIMGroup { return &PatchSCIMGroup{d: d} }

// Handle applies the operations, in order, to the group as one change; a
// patch that fails changes nothing.
func (h *PatchSCIMGroup) Handle(ctx context.Context, req SCIMRequest, group identity.SCIMGroupID, ops []identity.PatchOperation) (SCIMGroupRecord, error) {
	return h.d.change(ctx, req, group, func(before identity.GroupState) (identity.GroupState, error) {
		return identity.ApplyGroupPatch(before, group.String(), ops)
	})
}

// DeleteSCIMGroup removes a group.
type DeleteSCIMGroup struct{ d SCIMGroupDeps }

// NewDeleteSCIMGroup returns the handler.
func NewDeleteSCIMGroup(d SCIMGroupDeps) *DeleteSCIMGroup { return &DeleteSCIMGroup{d: d} }

// Handle removes the group, runs the provider's group mappings for its
// members, who are in it no longer, and records scim_group.deleted.
func (h *DeleteSCIMGroup) Handle(ctx context.Context, req SCIMRequest, group identity.SCIMGroupID) error {
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		p, err := lockProvider(ctx, h.d.Providers, req.Caller.ProviderSlug)
		if err != nil {
			return err
		}
		before, err := h.d.Groups.SCIMGroup(ctx, p.ID, group)
		if err != nil {
			return err
		}
		if err = h.d.Groups.DeleteSCIMGroup(ctx, group); err != nil {
			return err
		}
		if err = h.d.rerun(ctx, p, req, identity.TouchedMembers(before.GroupState, identity.GroupState{})); err != nil {
			return err
		}
		return h.d.record(ctx, req, governance.Record{
			Action: "scim_group.deleted", Entity: groupEntity(before), Before: groupValues(before.GroupState),
		})
	})
}

// change makes the group what apply turns its state into, in one unit of
// work: the members who join checked, the group stored, the provider's group
// mappings run for the members the change touches, and scim_group.updated
// recorded unless nothing changed.
func (d SCIMGroupDeps) change(ctx context.Context, req SCIMRequest, group identity.SCIMGroupID,
	apply func(before identity.GroupState) (identity.GroupState, error),
) (SCIMGroupRecord, error) {
	var out SCIMGroupRecord
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		// The provider is held for the whole change, as replacing its group
		// mappings and a sign-in holding a groups claim hold it: each runs the
		// mappings from the groups the others leave, never from groups one of
		// them is changing.
		p, err := lockProvider(ctx, d.Providers, req.Caller.ProviderSlug)
		if err != nil {
			return err
		}
		before, err := d.Groups.SCIMGroup(ctx, p.ID, group)
		if err != nil {
			return err
		}
		after, err := apply(before.GroupState)
		if err != nil {
			return err
		}
		after.Members = sortedMembers(after.Members)
		out = before
		out.GroupState = after
		if sameGroup(before.GroupState, after) {
			return nil
		}
		if err = d.checkJoining(ctx, req, before.Members, after.Members); err != nil {
			return err
		}
		out.UpdatedAt = d.now()
		if err = d.Groups.SaveSCIMGroup(ctx, out); err != nil {
			return err
		}
		if err = d.rerun(ctx, p, req, identity.TouchedMembers(before.GroupState, after)); err != nil {
			return err
		}
		b, a := groupChange(before.GroupState, after)
		return d.record(ctx, req, governance.Record{Action: "scim_group.updated", Entity: groupEntity(out), Before: b, After: a})
	})
	return out, err
}

// checkJoining refuses a member who joins (is in after and not in before)
// and whom the caller's provider does not provision: invalidValue, whether
// the member exists elsewhere or not.
func (d SCIMGroupDeps) checkJoining(ctx context.Context, req SCIMRequest, before, after []identity.MemberID) error {
	for _, m := range after {
		if slices.Contains(before, m) {
			continue
		}
		_, err := d.Members.Get(ctx, req.Caller.ProviderSlug, m)
		if errors.Is(err, identity.ErrUserNotFound) {
			return &identity.ScimError{Type: "invalidValue", Detail: "members: " + m.String() + " is not a User this provider provisions"}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// rerun runs the provider's group mappings for each member touched, from the
// groups each is in at the provider now, recording the access made and
// removed for the SCIM token.
func (d SCIMGroupDeps) rerun(ctx context.Context, p identity.Provider, req SCIMRequest, touched []identity.MemberID) error {
	if len(touched) == 0 {
		return nil
	}
	mappings, err := d.Mappings.GroupMappings(ctx, p.ID)
	if err != nil || len(mappings) == 0 {
		// Nothing is mapped, so nothing was granted and nothing is.
		return err
	}
	run := GroupMappingDeps{Access: d.Access, Logger: d.Logger}
	for _, m := range sortedMembers(touched) {
		groups, groupsErr := d.Mappings.GroupsOf(ctx, p.ID, m)
		if groupsErr != nil {
			return groupsErr
		}
		if _, err = run.replaceAccess(ctx, p, m, mappings, groups, req.governance().Actor, req.RequestID); err != nil {
			return err
		}
	}
	return nil
}

func (d SCIMGroupDeps) record(ctx context.Context, req SCIMRequest, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), d.now(), req.governance(), r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

func groupEntity(g SCIMGroupRecord) governance.Entity {
	return governance.Entity{Kind: "scim_group", ID: g.ID.String(), Key: g.DisplayName}
}

// groupValues is what the audit log records of a whole group.
func groupValues(g identity.GroupState) map[string]any {
	return map[string]any{"display_name": g.DisplayName, "external_id": nullable(g.ExternalID), "members": memberStrings(g.Members)}
}

// groupChange is what the audit log records of a change: the name and
// external ID when they changed, and the members who joined and left.
func groupChange(before, after identity.GroupState) (b, a map[string]any) {
	b, a = map[string]any{}, map[string]any{}
	if before.DisplayName != after.DisplayName {
		b["display_name"], a["display_name"] = before.DisplayName, after.DisplayName
	}
	if before.ExternalID != after.ExternalID {
		b["external_id"], a["external_id"] = nullable(before.ExternalID), nullable(after.ExternalID)
	}
	var joined, left []identity.MemberID
	for _, m := range after.Members {
		if !slices.Contains(before.Members, m) {
			joined = append(joined, m)
		}
	}
	for _, m := range before.Members {
		if !slices.Contains(after.Members, m) {
			left = append(left, m)
		}
	}
	if len(joined) > 0 {
		a["members_added"] = memberStrings(joined)
	}
	if len(left) > 0 {
		a["members_removed"] = memberStrings(left)
	}
	return b, a
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func memberStrings(members []identity.MemberID) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = m.String()
	}
	return out
}

func sortedMembers(members []identity.MemberID) []identity.MemberID {
	out := slices.Clone(members)
	slices.SortFunc(out, func(a, b identity.MemberID) int { return strings.Compare(a.String(), b.String()) })
	return out
}

func sameGroup(a, b identity.GroupState) bool {
	return a.DisplayName == b.DisplayName && a.ExternalID == b.ExternalID && slices.Equal(a.Members, b.Members)
}
