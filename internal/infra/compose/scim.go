package compose

import (
	"context"
	"errors"
	"net/netip"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// scimMembers lets the identity context provision the members of the access
// context: the two share no types, so this is where one's are turned into
// the other's, and the access context's refusals into those a SCIM client is
// told.
type scimMembers struct{ svc *appaccess.Provisioning }

var _ appidentity.SCIMMembers = scimMembers{}

func provisionedOf(m access.Member) appidentity.ProvisionedMember {
	return appidentity.ProvisionedMember{
		ID: id.FromBytes[identity.Member](m.ID.Bytes()), Email: m.Email, DisplayName: m.DisplayName,
		Active: m.Status == access.MemberActive, ExternalID: m.ExternalID, CreatedAt: m.CreatedAt,
	}
}

func memberID(m identity.MemberID) access.MemberID { return id.FromBytes[access.Member](m.Bytes()) }

func providerRef(p appidentity.ProviderRef) appaccess.ProviderRef {
	return appaccess.ProviderRef{ID: p.ID.String(), Slug: p.Slug}
}

// scimRefusal turns what the access context refuses into what the identity
// context answers a SCIM client.
func scimRefusal(err error) error {
	var e *apperrors.Error
	switch {
	case errors.Is(err, appaccess.ErrProvisionedMemberNotFound):
		return identity.ErrUserNotFound
	case errors.Is(err, access.ErrNotAdoptable):
		return appidentity.ErrEmailTaken
	case errors.Is(err, appaccess.ErrExternalIDTaken):
		return appidentity.ErrExternalIDTaken
	case errors.As(err, &e) && e.Kind == apperrors.KindConflict && e.Code == "already_exists":
		return appidentity.ErrEmailTaken
	}
	return err
}

func (a scimMembers) Get(ctx context.Context, provider string, member identity.MemberID) (appidentity.ProvisionedMember, error) {
	m, err := a.svc.Get(ctx, provider, memberID(member))
	return provisionedOf(m), scimRefusal(err)
}

func (a scimMembers) List(ctx context.Context, provider string, f identity.UserFilter, startIndex, count int) ([]appidentity.ProvisionedMember, int, error) {
	conds := make([]appaccess.ProvisionedCondition, len(f.Conditions))
	for i, c := range f.Conditions {
		field := map[identity.UserAttribute]appaccess.ProvisionedField{
			identity.AttrEmail: appaccess.FieldEmail, identity.AttrExternalID: appaccess.FieldExternalID,
			identity.AttrDisplayName: appaccess.FieldDisplayName, identity.AttrID: appaccess.FieldID,
		}[c.Attribute]
		conds[i] = appaccess.ProvisionedCondition{Field: field, Value: c.Value}
	}
	members, total, err := a.svc.List(ctx, provider, conds, startIndex, count)
	out := make([]appidentity.ProvisionedMember, len(members))
	for i, m := range members {
		out[i] = provisionedOf(m)
	}
	return out, total, err
}

func (a scimMembers) ByEmail(ctx context.Context, email string) (appidentity.HeldMember, bool, error) {
	m, ok, err := a.svc.MemberByEmail(ctx, email)
	return appidentity.HeldMember{ID: id.FromBytes[identity.Member](m.ID.Bytes())}, ok, err
}

func (a scimMembers) Provision(ctx context.Context, cmd appidentity.ProvisionUser) (appidentity.ProvisionedMember, error) {
	m, err := a.svc.Provision(ctx, appaccess.ProvisionCommand{
		Provider: providerRef(cmd.Provider), Email: cmd.Email, DisplayName: cmd.DisplayName, ExternalID: cmd.ExternalID, Request: cmd.Request,
	})
	return provisionedOf(m), scimRefusal(err)
}

func (a scimMembers) Adopt(ctx context.Context, cmd appidentity.AdoptUser) (appidentity.ProvisionedMember, error) {
	m, err := a.svc.Adopt(ctx, appaccess.AdoptCommand{
		Provider: providerRef(cmd.Provider), Member: memberID(cmd.Member), Email: cmd.Email, DisplayName: cmd.DisplayName,
		ExternalID: cmd.ExternalID, Request: cmd.Request,
	})
	return provisionedOf(m), scimRefusal(err)
}

func (a scimMembers) Update(ctx context.Context, cmd appidentity.UpdateUser) (appidentity.ProvisionedMember, error) {
	m, err := a.svc.Update(ctx, appaccess.UpdateProvisionedCommand{
		Provider: providerRef(cmd.Provider), Member: memberID(cmd.Member), Email: cmd.Email, DisplayName: cmd.DisplayName,
		ExternalID: cmd.ExternalID, ClearExternalID: cmd.ClearExternalID, Request: cmd.Request,
	})
	return provisionedOf(m), scimRefusal(err)
}

func (a scimMembers) SetActive(ctx context.Context, cmd appidentity.SetUserActive) (appidentity.ProvisionedMember, error) {
	m, err := a.svc.SetActive(ctx, appaccess.SetActiveCommand{
		Provider: providerRef(cmd.Provider), Member: memberID(cmd.Member), Active: cmd.Active, Request: cmd.Request,
	})
	return provisionedOf(m), scimRefusal(err)
}

// providerProvisioned names "provisioned members" among what keeps a provider
// from being deleted while it holds members.
type providerProvisioned struct {
	providers appidentity.ProviderStore
	svc       *appaccess.Provisioning
}

var _ appidentity.ProviderReference = providerProvisioned{}

func (p providerProvisioned) ProviderUses(ctx context.Context, provider identity.ProviderID) ([]string, error) {
	pr, err := p.providers.ProviderByID(ctx, provider)
	if err != nil {
		return nil, err
	}
	held, err := p.svc.ProviderHoldsMembers(ctx, pr.Slug)
	if err != nil || !held {
		return nil, err
	}
	return []string{"provisioned members"}, nil
}

// sourceThrottle lets the identity context count the failed attempts of an
// address against the throttle the access context keeps.
type sourceThrottle struct{ svc *appaccess.SourceThrottle }

var _ appidentity.SourceThrottle = sourceThrottle{}

func (t sourceThrottle) Reserve(ctx context.Context, ip netip.Addr, at time.Time) (func(context.Context) error, error) {
	return t.svc.Reserve(ctx, ip, at)
}
