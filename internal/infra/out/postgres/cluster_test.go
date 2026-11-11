package postgres_test

import (
	"bytes"
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func migrated(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatal(err)
	}
	return pool
}

// copyTable copies a table's rows between clusters as a logical dump and its
// restore do: the rows as they were, whatever the new cluster's counter reads.
func copyTable(t *testing.T, from, to *pgxpool.Pool, table string) {
	t.Helper()
	// A restore brings the dump's rows over those the migrations seeded.
	if _, err := to.Exec(t.Context(), "TRUNCATE "+pgx.Identifier{table}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	src, err := from.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Release()
	if _, err = src.Conn().PgConn().CopyTo(t.Context(), &dump, "COPY (SELECT * FROM "+pgx.Identifier{table}.Sanitize()+") TO STDOUT"); err != nil {
		t.Fatal(err)
	}
	dst, err := to.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Release()
	if _, err = dst.Conn().PgConn().CopyFrom(t.Context(), &dump, "COPY "+pgx.Identifier{table}.Sanitize()+" FROM STDIN"); err != nil {
		t.Fatal(err)
	}
}

// A logical dump moves the rows and the cursors of the streams reading them
// into a cluster that numbers its transactions from the start. Rows the new
// cluster receives must still come after every cursor: a stream goes on
// where it stopped, reading each new row once and no old one again.
func TestStreamsGoOnAfterALogicalRestoreIntoAnotherCluster(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	clock := testkit.Clock()
	alias := func(n byte) events.AliasRecord {
		var event [16]byte
		event[0] = n
		return events.AliasRecord{
			EventID:    id.FromBytes[delivery.Event](event),
			Alias:      events.Alias{FromUnitType: "visitor", FromUnitID: "a", ToUnitType: "user", ToUnitID: "b", OccurredAt: clock.Now()},
			ReceivedAt: clock.Now(),
		}
	}

	old := migrated(t, testkit.EmptyDatabase(t))
	// The old cluster has begun a great many transactions.
	if _, err := old.Exec(ctx, `UPDATE event_cluster SET xid_offset = 1 << 40`); err != nil {
		t.Fatal(err)
	}
	oldStore := pgevents.NewStore(old)
	if err := oldStore.SaveAliases(ctx, []events.AliasRecord{alias(1), alias(2)}); err != nil {
		t.Fatal(err)
	}
	// A transaction open anywhere on the server, in another package's tests,
	// holds the horizon back until it ends.
	var (
		rows   []events.AliasRow
		cursor events.Cursor
		err    error
	)
	deadline := time.Now().Add(time.Minute)
	for len(rows) != 2 && time.Now().Before(deadline) {
		if rows, cursor, err = oldStore.AliasesAfter(ctx, events.Cursor{}, 10); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) != 2 {
		t.Fatalf("the old cluster's rollup read %d aliases, want 2", len(rows))
	}
	if err = oldStore.MoveCursor(ctx, events.StreamAliases, cursor, clock.Now()); err != nil {
		t.Fatal(err)
	}

	fresh := migrated(t, testkit.OtherCluster(t))
	freshStore := pgevents.NewStore(fresh)
	if err = freshStore.CreateEventPartitions(ctx, clock.Now(), 0); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"event_cluster", "rollup_cursors", "alias_events"} {
		copyTable(t, old, fresh, table)
	}

	adopted, err := postgres.AdoptCluster(ctx, fresh)
	if err != nil || !adopted {
		t.Fatalf("adopting the cluster = %v, %v; want it adopted", adopted, err)
	}
	if again, againErr := postgres.AdoptCluster(ctx, fresh); againErr != nil || again {
		t.Fatalf("adopting it a second time = %v, %v; want nothing to do", again, againErr)
	}

	if err = freshStore.SaveAliases(ctx, []events.AliasRecord{alias(3)}); err != nil {
		t.Fatal(err)
	}
	at, err := freshStore.Cursor(ctx, events.StreamAliases)
	if err != nil {
		t.Fatal(err)
	}
	if at != cursor {
		t.Fatalf("the restored cursor = %+v, want %+v", at, cursor)
	}
	rows, next, err := freshStore.AliasesAfter(ctx, at, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("the stream read %d aliases after the restore, want the one received since", len(rows))
	}
	if next.XID <= cursor.XID {
		t.Errorf("the cursor moved to transaction %d, want it past %d", next.XID, cursor.XID)
	}
}

// upTo returns the migrations up to and including version.
func upTo(t *testing.T, version int64) fs.FS {
	t.Helper()
	files, err := fs.Glob(postgres.Migrations(), "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, f := range files {
		if v, err := goose.NumericComponent(f); err != nil || v > version {
			continue
		}
		b, err := fs.ReadFile(postgres.Migrations(), f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = &fstest.MapFile{Data: b}
	}
	return out
}

// A dump made before the database recorded its cluster, restored into a
// cluster whose counter is far behind the dump's, holds cursors no new row
// would pass: the migration that starts recording the cluster puts new rows
// after them, although the cluster it records is the one it runs on.
func TestStreamsGoOnAfterARestoreOfADumpFromBeforeTheClusterWasRecorded(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	clock := testkit.Clock()
	pool, err := pgxpool.New(ctx, testkit.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := postgres.Migrate(ctx, pool, upTo(t, 62)); err != nil {
		t.Fatal(err)
	}
	store := pgevents.NewStore(pool)
	restored := events.Cursor{XID: 1 << 40, Seq: 7}
	if err := store.MoveCursor(ctx, events.StreamAliases, restored, clock.Now()); err != nil {
		t.Fatal(err)
	}

	if _, err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEventPartitions(ctx, clock.Now(), 0); err != nil {
		t.Fatal(err)
	}
	var event [16]byte
	event[0] = 1
	if err := store.SaveAliases(ctx, []events.AliasRecord{{
		EventID:    id.FromBytes[delivery.Event](event),
		Alias:      events.Alias{FromUnitType: "visitor", FromUnitID: "a", ToUnitType: "user", ToUnitID: "b", OccurredAt: clock.Now()},
		ReceivedAt: clock.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.OldestUnread(ctx, events.StreamAliases); err != nil || !ok {
		t.Fatalf("a row received after the restore lies after the restored cursor = %v, %v; want true", ok, err)
	}
	if adopted, err := postgres.AdoptCluster(ctx, pool); err != nil || adopted {
		t.Fatalf("adopting the cluster = %v, %v; want nothing to do", adopted, err)
	}
}

// A replica that cannot tell which cluster the database was last used on
// does not start: serving events under a wrong offset would lose them.
func TestAdoptingTheClusterFailsWhereItCannotTellOrChange(t *testing.T) {
	t.Run("the database is unreachable", func(t *testing.T) {
		pool := migrated(t, testkit.EmptyDatabase(t))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := postgres.AdoptCluster(ctx, pool); err == nil {
			t.Error("adopting with a cancelled context succeeded")
		}
	})
	t.Run("the cluster was never recorded", func(t *testing.T) {
		pool := migrated(t, testkit.EmptyDatabase(t))
		if _, err := pool.Exec(t.Context(), `DROP TABLE event_cluster CASCADE`); err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.AdoptCluster(t.Context(), pool); err == nil {
			t.Error("adopting a database without the record of its cluster succeeded")
		}
	})
	t.Run("the greatest stored ID cannot be read", func(t *testing.T) {
		pool := migrated(t, testkit.EmptyDatabase(t))
		if _, err := pool.Exec(t.Context(), `UPDATE event_cluster SET system_identifier = system_identifier + 1;
			DROP FUNCTION event_xid_stored()`); err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.AdoptCluster(t.Context(), pool); err == nil {
			t.Error("adopting another cluster's database without the means to read its IDs succeeded")
		}
	})
}
