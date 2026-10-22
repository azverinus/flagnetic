package testkit

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// RestorePoint is what an environment held when it was taken, to take the
// environment back there as restoring a backup, or failing over to a
// replica that lost recent commits, would.
type RestorePoint struct {
	pool        *pgxpool.Pool
	environment workspace.EnvironmentID
	version     int64
	// configurations are the flag configurations in the environment, as
	// their rows were.
	configurations []configurationRow
}

type configurationRow struct {
	flag     [16]byte
	document []byte
	version  int64
	updated  any
}

// TakeRestorePoint records what environment holds now: its ruleset version
// and its flags' configurations.
//
// It reads tables, unlike any test: no API answers what a database restore
// brings back, and this is a fault the test kit injects, not an observation.
func TakeRestorePoint(t testing.TB, pool *pgxpool.Pool, environment workspace.EnvironmentID) *RestorePoint {
	t.Helper()
	p := &RestorePoint{pool: pool, environment: environment}
	if err := pool.QueryRow(t.Context(), `SELECT ruleset_version FROM environments WHERE id = $1`,
		environment.Bytes()).Scan(&p.version); err != nil {
		t.Fatalf("taking a restore point: %v", err)
	}
	rows, err := pool.Query(t.Context(), `SELECT flag_id, document, version, updated_at FROM flag_configurations
		WHERE environment_id = $1`, environment.Bytes())
	if err != nil {
		t.Fatalf("taking a restore point: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c configurationRow
		if err := rows.Scan(&c.flag, &c.document, &c.version, &c.updated); err != nil {
			t.Fatalf("taking a restore point: %v", err)
		}
		p.configurations = append(p.configurations, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("taking a restore point: %v", err)
	}
	return p
}

// Restore takes the environment back to the restore point, losing every
// change to its ruleset version and its flags' configurations since, and
// then restarts the listeners of replicas, as a database coming back from a
// restore or a failover makes every replica reconnect. Nothing is notified:
// a restored database announces nothing of what it lost.
func (p *RestorePoint) Restore(t testing.TB, replicas ...*Server) {
	t.Helper()
	ctx := t.Context()
	if _, err := p.pool.Exec(ctx, `UPDATE environments SET ruleset_version = $2 WHERE id = $1`,
		p.environment.Bytes(), p.version); err != nil {
		t.Fatalf("restoring: %v", err)
	}
	for _, c := range p.configurations {
		if _, err := p.pool.Exec(ctx, `UPDATE flag_configurations SET document = $3, version = $4, updated_at = $5
			WHERE flag_id = $1 AND environment_id = $2`, c.flag, p.environment.Bytes(), c.document, c.version, c.updated); err != nil {
			t.Fatalf("restoring: %v", err)
		}
	}
	for _, r := range replicas {
		r.RestartListeners(t)
	}
}
