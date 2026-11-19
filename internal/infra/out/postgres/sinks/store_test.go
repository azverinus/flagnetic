package sinks_test

import (
	"os"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// An update stores both sealed secrets: rebinding them to the row rewrites the
// previous one too, which a rotation still lets sign for a while.
func TestAnUpdateStoresTheRebindPreviousSecretToo(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	project, err := workspace.NewProject(id.New[workspace.Project](ids), "shop", "Shop", "", testkit.Start)
	if err != nil {
		t.Fatal(err)
	}
	if err = pgworkspace.NewStore(pool).CreateProject(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	store := pgsinks.NewStore(pool)
	expires := testkit.Start.Add(time.Hour)
	sink := sinks.Sink{
		ID: id.New[sinks.Sink](ids), Project: project.ID, SealedSecret: "current-1",
		Input: sinks.Input{
			Name: "Warehouse", Kind: sinks.KindWebhook, Config: sinks.Config{Webhook: &sinks.Webhook{URL: "https://r.example/hook"}},
			BatchSize: 500, FlushInterval: 5 * time.Second,
		},
		CreatedAt: testkit.Start, UpdatedAt: testkit.Start, Version: 1,
	}
	if err = store.Create(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	sink.PreviousSealedSecret, sink.PreviousSecretExpiresAt = "previous-1", &expires
	if err = store.RotateSecret(t.Context(), sink); err != nil {
		t.Fatal(err)
	}

	sink.SealedSecret, sink.PreviousSealedSecret, sink.Version = "current-2", "previous-2", 2
	if err = store.Update(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	got, err := store.Sink(t.Context(), project.ID, sink.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SealedSecret != "current-2" || got.PreviousSealedSecret != "previous-2" {
		t.Errorf("secrets %q and %q after the update, want current-2 and previous-2", got.SealedSecret, got.PreviousSealedSecret)
	}
	if got.PreviousSecretExpiresAt == nil || !got.PreviousSecretExpiresAt.Equal(expires) {
		t.Errorf("the previous secret expires %v, want %v: an update leaves the overlap's end alone", got.PreviousSecretExpiresAt, expires)
	}
}
