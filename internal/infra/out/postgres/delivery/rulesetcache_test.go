package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func materializer(pool *pgxpool.Pool) *appdelivery.MaterializeRuleset {
	return appdelivery.NewMaterializeRuleset(appdelivery.MaterializeRulesetDeps{
		Snapshots: postgres.NewTransactor(pool), Environments: pgdelivery.NewStore(pool),
		Declarations: pgworkspace.NewStore(pool), Flags: pgflags.NewStore(pool),
	})
}

// A write waiting for what its environment served before it holds a
// connection of the replica's pool, as may every write queued behind it:
// the committed ruleset is read even when that pool has none left.
func TestTheCommittedRulesetIsReadWhileTheReplicasPoolIsExhausted(t *testing.T) {
	p, envs := project(t, "exhausted")
	cfg, err := pgxpool.ParseConfig(testkit.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	side, err := postgres.SidePool(pool, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(side.Close)
	cache := pgdelivery.NewRulesetCache(pgdelivery.RulesetCacheOptions{
		Load: materializer(pool), CommittedLoad: materializer(side), Clock: testkit.Clock(), Registerer: prometheus.NewRegistry(),
	})
	t.Cleanup(cache.Close)
	versions, err := pgdelivery.NewStore(testkit.Pool(t)).RulesetVersions(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}

	held, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rs, err := cache.CommittedRuleset(ctx, envs[0].ID, versions[envs[0].Key])
	if err != nil || rs.Version != versions[envs[0].Key] {
		t.Errorf("committed ruleset with the pool exhausted = version %d, %v; want version %d", rs.Version, err, versions[envs[0].Key])
	}
}
