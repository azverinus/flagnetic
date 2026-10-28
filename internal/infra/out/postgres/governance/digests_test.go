package governance_test

import (
	"context"
	"testing"
	"time"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func awaiting(t *testing.T, s *pggovernance.Digests, slot time.Time) []string {
	t.Helper()
	projects, err := s.ProjectsAwaitingDigest(t.Context(), slot)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range projects {
		keys = append(keys, p.Key)
	}
	return keys
}

// A slot is handled once per project, is remembered per slot, and is
// forgotten only when it began before the cutoff.
func TestADigestSlotIsHandledOnceAndForgottenAfterTheCutoff(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	ws := pgworkspace.NewStore(pool)
	var projects []workspace.Project
	for _, key := range []string{"web", "api"} {
		p, err := workspace.NewProject(id.New[workspace.Project](ids), key, key, "", testkit.Start)
		if err != nil {
			t.Fatal(err)
		}
		if err = ws.CreateProject(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		projects = append(projects, p)
	}
	web := projects[0]
	s := pggovernance.NewDigests(pool)
	week := 7 * 24 * time.Hour
	old, recent := testkit.Start, testkit.Start.Add(6*week)

	if got := awaiting(t, s, recent); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("before any slot is handled, projects awaiting it = %v, want [api web] by key", got)
	}
	for _, slot := range []time.Time{old, recent} {
		first, err := s.RecordDigestSlot(t.Context(), web.ID, slot)
		if err != nil || !first {
			t.Fatalf("recording %s: %v, %v; want true", slot, first, err)
		}
	}
	if again, err := s.RecordDigestSlot(t.Context(), web.ID, recent); err != nil || again {
		t.Errorf("recording a handled slot again = %v, %v; want false", again, err)
	}
	if got := awaiting(t, s, recent); len(got) != 1 || got[0] != "api" {
		t.Errorf("after web handled the slot, projects awaiting it = %v, want [api]", got)
	}

	if err := s.PruneDigestSlots(t.Context(), recent.Add(-5*week)); err != nil {
		t.Fatal(err)
	}
	if got := awaiting(t, s, old); len(got) != 2 {
		t.Errorf("the slot five weeks before is pruned, so projects awaiting it = %v, want both", got)
	}
	if got := awaiting(t, s, recent); len(got) != 1 {
		t.Errorf("pruning forgot the recent slot: projects awaiting it = %v, want [api]", got)
	}
}

// Of two transactions asking for the digest lock at once, one holds it and
// the other is told so without waiting.
func TestOneReplicaHoldsTheDigestLock(t *testing.T) {
	pool := testkit.FreshPool(t)
	s := pggovernance.NewDigests(pool)
	tx := postgres.NewTransactor(pool)
	err := tx.Within(t.Context(), func(first context.Context) error {
		held, err := s.TryLockDigest(first)
		if err != nil || !held {
			t.Fatalf("first TryLockDigest = %v, %v", held, err)
		}
		return tx.Within(pgxtx.Outside(first), func(second context.Context) error {
			if held, err = s.TryLockDigest(second); err != nil || held {
				t.Errorf("second TryLockDigest = %v, %v, want false", held, err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

var _ appgovernance.DigestStore = (*pggovernance.Digests)(nil)
