package identity_test

import (
	"context"
	"testing"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
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
	if _, err := store.KnownGroups(ctx, provider); err == nil {
		t.Error("KnownGroups succeeded")
	}
	if _, err := store.GroupsOf(ctx, provider, member); err == nil {
		t.Error("GroupsOf succeeded")
	}

	group := appidentity.SCIMGroupRecord{ID: id.New[identity.SCIMGroup](ids), Provider: provider}
	group.DisplayName, group.Members = "eng", []identity.MemberID{member}
	if err := store.CreateSCIMGroup(ctx, group); err == nil {
		t.Error("CreateSCIMGroup succeeded")
	}
	if err := store.SaveSCIMGroup(ctx, group); err == nil {
		t.Error("SaveSCIMGroup succeeded")
	}
	if err := store.DeleteSCIMGroup(ctx, group.ID); err == nil {
		t.Error("DeleteSCIMGroup succeeded")
	}
	if _, err := store.SCIMGroup(ctx, provider, group.ID); err == nil {
		t.Error("SCIMGroup succeeded")
	}
	if _, _, err := store.SCIMGroups(ctx, provider, identity.GroupFilter{}, 1, 10); err == nil {
		t.Error("SCIMGroups succeeded")
	}
	if _, err := store.GroupsOfMembers(ctx, provider, []identity.MemberID{member}); err == nil {
		t.Error("GroupsOfMembers succeeded")
	}
}
