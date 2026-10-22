package e2e

import (
	"context"
	"net/http"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// sdkKeys returns every SDK key of the seed environment.
func sdkKeys(ctx context.Context, m *Management) ([]management.SdkKey, error) {
	res, err := m.ListSdkKeysWithResponse(ctx, ProjectKey, EnvironmentKey)
	if err != nil {
		return nil, err
	}
	if res.JSON200 == nil {
		return nil, unexpected("list SDK keys", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items, nil
}

// Key is an SDK key of the seed whose value the harness holds.
type Key struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// ensureKeys returns a server key and a client key named KeyName. The keys
// held from an earlier seeding of this database are kept while they are
// active, so seeding again changes nothing; otherwise new ones are created,
// since a key's value is shown only once, and every other active key named
// KeyName is revoked afterwards, so a failed run leaves the earlier keys
// working and a database never holds more than one of each kind.
func ensureKeys(ctx context.Context, m *Management, held map[management.SdkKeyKind]Key) (map[management.SdkKeyKind]Key, error) {
	existing, err := sdkKeys(ctx, m)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, k := range existing {
		if k.Status == management.SdkKeyStatusActive {
			active[k.Id.String()] = true
		}
	}
	keys := map[management.SdkKeyKind]Key{}
	for _, kind := range []management.SdkKeyKind{management.SdkKeyKindServer, management.SdkKeyKindClient} {
		if h, ok := held[kind]; ok && active[h.ID] {
			keys[kind] = h
			continue
		}
		res, err := m.CreateSdkKeyWithResponse(ctx, ProjectKey, EnvironmentKey, management.SdkKeyCreate{Kind: string(kind), Name: KeyName})
		if err != nil {
			return nil, err
		}
		if res.JSON201 == nil {
			return nil, unexpected("create a "+string(kind)+" key", res.StatusCode(), res.Body)
		}
		keys[kind] = Key{ID: res.JSON201.Key.Id.String(), Value: res.JSON201.Value}
	}
	for _, k := range existing {
		if k.Name != KeyName || k.Status != management.SdkKeyStatusActive || k.Id.String() == keys[k.Kind].ID {
			continue
		}
		if err := revokeKey(ctx, m, k.Id.String()); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// revokeKey revokes the SDK key id; a key revoked already stays so.
func revokeKey(ctx context.Context, m *Management, id string) error {
	keys, err := sdkKeys(ctx, m)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.Id.String() == id && k.Status == management.SdkKeyStatusRevoked {
			return nil
		}
	}
	res, err := m.RevokeSdkKeyWithResponse(ctx, ProjectKey, EnvironmentKey, id)
	if err != nil {
		return err
	}
	if res.StatusCode() >= http.StatusMultipleChoices {
		return unexpected("revoke SDK key "+id, res.StatusCode(), res.Body)
	}
	return nil
}
