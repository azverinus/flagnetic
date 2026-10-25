package postgres_test

import (
	"slices"
	"strings"
	"testing"

	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Every secret the server checks a presented value against (an API token, a
// session, an invitation, an SDK key) is stored only as its SHA-256. The
// schema holds each such column to exactly 32 bytes, so a write that stored
// the value itself, or a shorter digest, fails instead of weakening the
// store unnoticed.
func TestEveryStoredHashIsASHA256(t *testing.T) {
	pool := testkit.Pool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT c.table_name || '.' || c.column_name
		FROM information_schema.columns c
		JOIN pg_class t ON t.relname = c.table_name AND t.relnamespace = current_schema()::regnamespace
		WHERE c.table_schema = current_schema() AND c.data_type = 'bytea' AND c.column_name LIKE '%\_hash'
		  AND NOT t.relispartition
		  AND NOT EXISTS (
			SELECT 1 FROM pg_constraint k
			WHERE k.conrelid = t.oid AND k.contype = 'c'
			  AND pg_get_constraintdef(k.oid) LIKE '%length(' || c.column_name || ') = 32%')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var unchecked []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		unchecked = append(unchecked, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(unchecked) > 0 {
		t.Errorf("hash columns without a CHECK holding them to 32 bytes:\n%s", strings.Join(unchecked, "\n"))
	}
}

// An index the migrations create is named after its table and what it
// serves (sessions_member, flags_tags), with no _idx suffix, so a name read
// in a query plan or an error says where to look. Indexes that back a
// primary key, unique or exclusion constraint of their own table are named
// by PostgreSQL after the constraint, and indexes on a partition are named
// after the partition, so both are left alone. A foreign key also records
// the index it relies on, but that index belongs to the referenced table and
// is still checked.
func TestIndexNamesCarryNoTypeSuffix(t *testing.T) {
	pool := testkit.Pool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT i.relname, t.relname
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_class t ON t.oid = x.indrelid
		WHERE t.relnamespace = current_schema()::regnamespace
		  AND NOT t.relispartition
		  AND NOT EXISTS (
			SELECT 1 FROM pg_constraint k
			WHERE k.conindid = x.indexrelid AND k.conrelid = x.indrelid AND k.contype IN ('p', 'u', 'x'))`)
	if err != nil {
		t.Fatal(err)
	}
	var misnamed []string
	for rows.Next() {
		var index, table string
		if err := rows.Scan(&index, &table); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(index, "_idx") || !strings.HasPrefix(index, table+"_") {
			misnamed = append(misnamed, table+": "+index)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(misnamed)
	if len(misnamed) > 0 {
		t.Errorf("indexes not named <table>_<what> without an _idx suffix:\n%s", strings.Join(misnamed, "\n"))
	}
}

// Deleting a project removes what it holds by the cascades of its foreign
// keys, and the project purge removes, after the project has gone, the rows
// of the tables that have no foreign key to it. A table carrying a
// project_id must be one or the other, or be kept on purpose, so a table
// added later cannot be forgotten and keep a deleted project's rows forever.
func TestEveryTableOfAProjectGoesWithIt(t *testing.T) {
	// Kept after the project is gone, on purpose.
	kept := map[string]string{
		"audit_entries": "a deleted project's audit entries stay, read through the instance log by the project's id",
		// Raw events are partitioned by day: retention drops whole days
		// rather than deleting from every partition at once.
		"evaluation_summary_events": "dropped with its partitions by retention",
		"alias_events":              "dropped with its partitions by retention",
		"exposure_events":           "dropped with its partitions by retention",
	}
	pool := testkit.Pool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT c.relname
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		  AND a.attname = 'project_id' AND NOT a.attisdropped
		  AND NOT EXISTS (
			SELECT 1 FROM pg_constraint k
			WHERE k.conrelid = c.oid AND k.contype = 'f' AND k.confrelid = 'projects'::regclass
			  AND k.confdeltype = 'c' AND k.conkey = ARRAY[a.attnum])
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var stranded []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if _, ok := kept[table]; !ok && !slices.Contains(pgworkspace.ProjectPurgeTables(), table) {
			stranded = append(stranded, table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(stranded) > 0 {
		t.Errorf("tables with a project_id neither cascading from projects nor purged after a project's deletion:\n%s",
			strings.Join(stranded, "\n"))
	}
}
