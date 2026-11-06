package compose

import (
	"context"
	"errors"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// providerAccess lets the identity context give members what a provider's
// group mappings map to: the two contexts share no types, so this is where
// one's are turned into the other's.
type providerAccess struct{ svc *appaccess.ProviderAccess }

var _ appidentity.ProviderAccess = providerAccess{}

func (a providerAccess) KnownRole(name string) bool { return a.svc.KnownRole(name) }

func (a providerAccess) ReplaceAccess(ctx context.Context, cmd appidentity.AccessCommand) (appidentity.AccessChange, error) {
	grants := make([]appaccess.ProviderRole, len(cmd.Grants))
	for i, g := range cmd.Grants {
		grants[i] = appaccess.ProviderRole{Project: g.Project, Role: access.RoleName(g.Role)}
	}
	res, err := a.svc.Replace(ctx, appaccess.ProviderAccessCommand{
		Provider: appaccess.ProviderRef{ID: cmd.Provider.ID.String(), Slug: cmd.Provider.Slug},
		Member:   id.FromBytes[access.Member](cmd.Member.Bytes()), Grants: grants, InstanceAdmin: cmd.InstanceAdmin, RequestID: cmd.RequestID,
	})
	return appidentity.AccessChange{Granted: res.Granted, Revoked: res.Revoked, KeptInstanceAdmin: res.KeptInstanceAdmin}, err
}

// providerGrants names "grants" among what keeps a provider from being
// deleted while its group mappings made a grant or an instance admin.
type providerGrants struct {
	providers appidentity.ProviderStore
	access    *pgaccess.Store
}

var _ appidentity.ProviderReference = providerGrants{}

func (g providerGrants) ProviderUses(ctx context.Context, provider identity.ProviderID) ([]string, error) {
	p, err := g.providers.ProviderByID(ctx, provider)
	if err != nil {
		return nil, err
	}
	made, err := g.access.ProviderMadeAccess(ctx, p.Slug)
	if err != nil || !made {
		return nil, err
	}
	return []string{"grants"}, nil
}

// projectDirectory finds the projects group mappings name.
type projectDirectory struct{ ws *pgworkspace.Store }

var _ appidentity.ProjectDirectory = projectDirectory{}

func (d projectDirectory) ProjectIDs(ctx context.Context, keys []string) (map[string]workspace.ProjectID, error) {
	out := make(map[string]workspace.ProjectID, len(keys))
	for _, key := range keys {
		if _, seen := out[key]; seen {
			continue
		}
		p, err := d.ws.ProjectByKey(ctx, key)
		if errors.Is(err, appworkspace.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[key] = p.ID
	}
	return out, nil
}
