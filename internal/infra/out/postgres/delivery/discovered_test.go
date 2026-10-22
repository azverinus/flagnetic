package delivery_test

import (
	"context"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Every read and write of discovered attributes reports a database it
// cannot reach rather than answer nothing, so a round of the recorder
// fails visibly and a member's request is answered unavailable.
func TestDiscoveredAttributesReportADatabaseTheyCannotReach(t *testing.T) {
	store := pgdelivery.NewStore(testkit.Pool(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	project := id.FromBytes[workspace.Project]([16]byte{7})
	keys := []string{"tier"}
	d := delivery.NewDiscoveredAttribute(project, delivery.Sighting{Key: "tier", Types: []delivery.ObservedType{"string"}, Count: 1})

	failures := map[string]error{
		"lock":   store.LockDiscoveredAttributes(ctx, project),
		"save":   store.SaveDiscoveredAttribute(ctx, d),
		"delete": store.DeleteDiscoveredAttribute(ctx, project, "tier"),
	}
	_, failures["declared"] = store.DeclaredKeys(ctx, project, keys)
	_, failures["read"] = store.DiscoveredAttributes(ctx, project, keys)
	_, failures["count"] = store.CountDiscoveredAttributes(ctx, project)
	_, failures["list"] = store.ListedDiscoveredAttributes(ctx, project)
	_, failures["read for update"] = store.DiscoveredAttributeForUpdate(ctx, project, "tier")
	for name, err := range failures {
		if err == nil {
			t.Errorf("%s: no error from a database not reached", name)
		}
	}
}
