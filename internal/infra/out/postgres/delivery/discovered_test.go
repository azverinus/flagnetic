package delivery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
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

// pausingStore is the real store, whose first read of discovered
// attributes tells reading and then waits for resume, so a test can commit
// a member's change between a flush's read and its write.
type pausingStore struct {
	*pgdelivery.Store
	reading chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (p *pausingStore) DiscoveredAttributes(ctx context.Context, project workspace.ProjectID, keys []string) ([]delivery.DiscoveredAttribute, error) {
	stored, err := p.Store.DiscoveredAttributes(ctx, project, keys)
	p.once.Do(func() {
		close(p.reading)
		<-p.resume
	})
	return stored, err
}

// A change a member makes while a flush that read the attribute before it
// is under way holds: the flush writes its row back whole, so a change
// committed between would be undone unless the two are ordered.
func TestAMembersChangeToADiscoveredAttributeHoldsAgainstAFlushUnderWay(t *testing.T) {
	declared := func(_ context.Context, _ appdelivery.AttributeDeclaration) (workspace.Attribute, error) {
		return workspace.Attribute{}, nil
	}
	changes := map[string]func(tx appdelivery.Transactor, store *pgdelivery.Store, p workspace.ProjectID) error{
		"dismissal": func(tx appdelivery.Transactor, store *pgdelivery.Store, p workspace.ProjectID) error {
			return appdelivery.NewDismissDiscoveredAttribute(tx, store).Handle(context.Background(), p, "plan")
		},
		"declaration": func(tx appdelivery.Transactor, store *pgdelivery.Store, p workspace.ProjectID) error {
			_, err := appdelivery.NewDeclareDiscoveredAttribute(tx, store, declared).
				Handle(context.Background(), appdelivery.AttributeDeclaration{Project: p, Key: "plan", Type: workspace.TypeString})
			return err
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			pool := testkit.Pool(t)
			tx := postgres.NewTransactor(pool)
			store := pgdelivery.NewStore(pool)
			p, _ := project(t, "discovered-"+name)
			seen := []delivery.ObservedAttribute{{Key: "plan", Type: "string"}}

			first := appdelivery.NewDiscoveries(tx, store)
			first.Note(p.ID, testkit.Start, seen)
			if err := first.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}

			pausing := &pausingStore{Store: store, reading: make(chan struct{}), resume: make(chan struct{})}
			flush := appdelivery.NewDiscoveries(tx, pausing)
			flush.Note(p.ID, testkit.Start, seen)
			flushed := make(chan error, 1)
			go func() { flushed <- flush.Flush(context.Background()) }()
			<-pausing.reading

			changed := make(chan error, 1)
			go func() { changed <- change(tx, store, p.ID) }()
			// The change either waits for the flush, or commits before it
			// resumes: the second is what the flush must not undo.
			time.Sleep(300 * time.Millisecond)
			close(pausing.resume)
			if err := <-flushed; err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatal(err)
			}

			listed, err := store.ListedDiscoveredAttributes(t.Context(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 0 {
				t.Errorf("after a %s during a flush, listed = %v, want none", name, listed)
			}
		})
	}
}
