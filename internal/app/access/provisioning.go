package access

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Provisioning is what the identity context asks of the access context to
// let an identity provider create, adopt, update and deprovision members
// through SCIM. Each method acts in the unit of work its context carries, so
// the identity context makes a request's changes in one transaction; members
// of another provider are never found.
type Provisioning struct {
	tx    Transactor
	store ProvisionedMemberStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewProvisioning returns the service.
func NewProvisioning(tx Transactor, store ProvisionedMemberStore, audit AuditLog, c clock.Clock, ids *id.Source) *Provisioning {
	return &Provisioning{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// MemberByEmail returns the member holding email whatever its case, with
// whether they hold a password; false when none does.
func (p *Provisioning) MemberByEmail(ctx context.Context, email string) (access.Member, bool, error) {
	m, _, err := p.store.MemberByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, false, nil
	}
	return m, err == nil, err
}

// Get returns the member of provider with the identifier;
// ErrProvisionedMemberNotFound when none is, whether the member exists
// elsewhere or not.
func (p *Provisioning) Get(ctx context.Context, provider string, member access.MemberID) (access.Member, error) {
	m, err := p.store.MemberByID(ctx, member)
	if errors.Is(err, ErrNotFound) || (err == nil && m.Provider != provider) {
		return access.Member{}, ErrProvisionedMemberNotFound
	}
	return m, err
}

// lock is Get, locking the member until the unit of work ends.
func (p *Provisioning) lock(ctx context.Context, provider string, member access.MemberID) (access.Member, error) {
	m, _, err := p.store.LockMember(ctx, member)
	if errors.Is(err, ErrNotFound) || (err == nil && m.Provider != provider) {
		return access.Member{}, ErrProvisionedMemberNotFound
	}
	return m, err
}

// List returns the page of provider's members that satisfy every condition,
// and how many satisfy them in all.
func (p *Provisioning) List(ctx context.Context, provider string, conds []ProvisionedCondition, startIndex, count int) ([]access.Member, int, error) {
	return p.store.ProvisionedMembers(ctx, provider, conds, startIndex, count)
}

// ProvisionCommand creates an active member without a password in provider.
type ProvisionCommand struct {
	Provider    ProviderRef
	Email       string
	DisplayName string
	// ExternalID is the provider's identifier of the person, "" for none.
	ExternalID string
	Request    governance.Request
}

// Provision creates the member and records member.provisioned. An email
// another member holds is refused already_exists, an external ID another
// member of the provider holds ErrExternalIDTaken.
func (p *Provisioning) Provision(ctx context.Context, cmd ProvisionCommand) (access.Member, error) {
	now := p.clock.Now()
	m, err := access.NewSCIMMember(id.New[access.Member](p.ids), cmd.Email, cmd.DisplayName, cmd.Provider.Slug, cmd.ExternalID, now)
	if err != nil {
		return access.Member{}, err
	}
	if err = p.store.CreateProvisionedMember(ctx, m, now); err != nil {
		return access.Member{}, err
	}
	return m, recordAll(ctx, p.audit, p.ids, now, cmd.Request, governance.Record{
		Action: "member.provisioned", Entity: memberEntity(m), After: provisionedValues(m),
	})
}

// AdoptCommand makes a member the instance already holds a member of provider.
type AdoptCommand struct {
	Provider    ProviderRef
	Member      access.MemberID
	Email       string
	DisplayName string
	ExternalID  string
	Request     governance.Request
}

// Adopt makes the member one of provider's, their email, name and external
// ID the directory's from now on, and records member.adopted.
func (p *Provisioning) Adopt(ctx context.Context, cmd AdoptCommand) (access.Member, error) {
	now := p.clock.Now()
	before, _, err := p.store.LockMember(ctx, cmd.Member)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, ErrProvisionedMemberNotFound
	}
	if err != nil {
		return access.Member{}, err
	}
	if !before.Adoptable() {
		return access.Member{}, access.ErrNotAdoptable
	}
	after, err := before.Adopted(cmd.Email, cmd.DisplayName, cmd.Provider.Slug, cmd.ExternalID)
	if err != nil {
		return access.Member{}, err
	}
	if err = p.store.SaveProvisionedMember(ctx, after, now); err != nil {
		return access.Member{}, err
	}
	return after, recordAll(ctx, p.audit, p.ids, now, cmd.Request, governance.Record{
		Action: "member.adopted", Entity: memberEntity(after), Before: provisionedValues(before), After: provisionedValues(after),
	})
}

// UpdateProvisionedCommand changes what the directory states of a member; a
// nil field is left as it is.
type UpdateProvisionedCommand struct {
	Provider    ProviderRef
	Member      access.MemberID
	Email       *string
	DisplayName *string
	// ExternalID replaces the external ID; ClearExternalID removes it.
	ExternalID      *string
	ClearExternalID bool
	Request         governance.Request
}

// Update changes the member's email, name and external ID and records
// member.updated with what changed; a request that changes nothing records
// nothing.
func (p *Provisioning) Update(ctx context.Context, cmd UpdateProvisionedCommand) (access.Member, error) {
	now := p.clock.Now()
	before, err := p.lock(ctx, cmd.Provider.Slug, cmd.Member)
	if err != nil {
		return access.Member{}, err
	}
	after, err := before.Synced(cmd.Email, cmd.DisplayName)
	if err != nil {
		return access.Member{}, err
	}
	switch {
	case cmd.ClearExternalID:
		after.ExternalID = ""
	case cmd.ExternalID != nil:
		after.ExternalID = *cmd.ExternalID
	}
	if after == before {
		return after, nil
	}
	if err = p.store.SaveProvisionedMember(ctx, after, now); err != nil {
		return access.Member{}, err
	}
	was, is := map[string]any{}, map[string]any{}
	for name, change := range map[string][2]string{
		"email": {before.Email, after.Email}, "display_name": {before.DisplayName, after.DisplayName},
		"external_id": {before.ExternalID, after.ExternalID},
	} {
		if change[0] != change[1] {
			was[name], is[name] = change[0], change[1]
		}
	}
	return after, recordAll(ctx, p.audit, p.ids, now, cmd.Request, governance.Record{
		Action: "member.updated", Entity: memberEntity(after), Before: was, After: is,
	})
}

// SetActiveCommand deactivates or reactivates a member of a provider.
type SetActiveCommand struct {
	Provider ProviderRef
	Member   access.MemberID
	Active   bool
	Request  governance.Request
}

// SetActive deactivates the member, ending their sessions, personal tokens,
// invitations and password resets, or reactivates them, recording
// member.deactivated or member.reactivated; a member already as asked is left
// as they are. The last active instance admin is not deactivated.
// Reactivated, a member of an identity provider is active: the provider is
// how they sign in.
func (p *Provisioning) SetActive(ctx context.Context, cmd SetActiveCommand) (access.Member, error) {
	if _, err := p.Get(ctx, cmd.Provider.Slug, cmd.Member); err != nil {
		return access.Member{}, err
	}
	var out access.Member
	_, err := changeMember(ctx, p.tx, p.store, p.audit, p.clock, p.ids, cmd.Request, cmd.Member,
		func(ctx context.Context, before access.Member, _ bool, now time.Time) (access.Member, []governance.Record, error) {
			if before.Provider != cmd.Provider.Slug {
				return access.Member{}, nil, ErrProvisionedMemberNotFound
			}
			out = before
			switch {
			case !cmd.Active:
				after, records, err := deactivateMember(ctx, p.store, before, now)
				out = after
				return after, records, err
			case before.Status == access.MemberDeactivated:
				after := before
				after.Status = access.MemberActive
				out = after
				return after, []governance.Record{{
					Action: "member.reactivated", Entity: memberEntity(after),
					Before: map[string]any{"status": before.Status}, After: map[string]any{"status": after.Status},
				}}, nil
			}
			return before, nil, nil
		})
	return out, err
}

// ProviderHoldsMembers reports whether provider has provisioned members, which
// keep it from being deleted.
func (p *Provisioning) ProviderHoldsMembers(ctx context.Context, provider string) (bool, error) {
	return p.store.ProviderProvisionedMembers(ctx, provider)
}

func provisionedValues(m access.Member) map[string]any {
	return map[string]any{
		"email": m.Email, "display_name": m.DisplayName, "status": m.Status, "provider": m.Provider, "external_id": m.ExternalID,
	}
}
