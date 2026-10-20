// These tests write and read a table directly, unlike every other test in
// the repository: what is under test here is the database itself, and there
// is no repository or API in front of it to observe it through.
package pgtest_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/pkg/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(ctx context.Context, pool *pgxpool.Pool) error {
		_, err := pool.Exec(ctx, `CREATE TABLE greeting (word text NOT NULL)`)
		return err
	}))
}

func TestThePackageSchemaIsSetUpOnce(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Pool(t)
	if _, err := pool.Exec(ctx, `INSERT INTO greeting VALUES ('hello')`); err != nil {
		t.Fatalf("the set-up table is not on the package pool's search path: %v", err)
	}
	if pgtest.Pool(t) != pool {
		t.Error("Pool returned another pool for the same package")
	}
}

func TestThePackageURLPointsAtThePackageSchema(t *testing.T) {
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `SELECT word FROM greeting`); err != nil {
		t.Errorf("the package URL does not reach the set-up schema: %v", err)
	}
}

func TestNewSchemaIsEmptyAndSeparate(t *testing.T) {
	ctx := t.Context()
	url := pgtest.NewSchema(t)
	if !strings.Contains(url, "search_path=") {
		t.Errorf("URL %q sets no search_path", url)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `SELECT 1 FROM greeting`); err == nil {
		t.Error("a new schema sees the package schema's table")
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE greeting (word text)`); err != nil {
		t.Errorf("cannot create a table in the new schema: %v", err)
	}
	if other := pgtest.NewSchema(t); other == url {
		t.Error("two new schemas share a URL")
	}
}
