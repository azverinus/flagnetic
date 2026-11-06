package identity_test

import (
	"context"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A store that cannot reach the database says so for every operation, and
// changes nothing.
func TestGroupMappingStoreOperationsFailWhenTheDatabaseCannotBeReached(t *testing.T) {
	store := pgidentity.NewStore(testkit.Pool(t))
	ids := id.NewSource(testkit.Clock())
	provider, member := id.New[identity.Provider](ids), id.New[identity.Member](ids)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.GroupMappings(ctx, provider); err == nil {
		t.Error("GroupMappings succeeded")
	}
	if err := store.ReplaceGroupMappings(ctx, provider, []identity.GroupMapping{{Group: "eng", InstanceAdmin: true}}); err == nil {
		t.Error("ReplaceGroupMappings succeeded")
	}
	if err := store.StoreGroupClaim(ctx, provider, member, nil, testkit.Start); err == nil {
		t.Error("StoreGroupClaim succeeded")
	}
	if _, err := store.GroupClaims(ctx, provider); err == nil {
		t.Error("GroupClaims succeeded")
	}
}
