package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Canary returns the instance's canary, storing fresh as the canary at now
// when the instance has none yet. Replicas starting together all read the
// one canary the first of them stored.
func Canary(ctx context.Context, pool *pgxpool.Pool, fresh string, now time.Time) (string, error) {
	_, err := pool.Exec(ctx, `INSERT INTO secret_key_canary (id, sealed, created_at) VALUES (true, $1, $2) ON CONFLICT (id) DO NOTHING`, fresh, now)
	if err != nil {
		return "", err
	}
	var stored string
	err = pool.QueryRow(ctx, `SELECT sealed FROM secret_key_canary`).Scan(&stored)
	return stored, err
}
