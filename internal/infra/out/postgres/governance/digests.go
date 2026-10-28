package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Digests is the repository of the stale-flag digest slots each project has
// handled.
type Digests struct{ pool *pgxpool.Pool }

// NewDigests returns the repository on pool.
func NewDigests(pool *pgxpool.Pool) *Digests { return &Digests{pool: pool} }

var _ appgovernance.DigestStore = (*Digests)(nil)

func (s *Digests) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// TryLockDigest takes the digest lock until the transaction ends, without
// waiting; false when another replica holds it. The key names the schema
// too, so instances sharing a database in separate schemas run their rounds
// independently.
func (s *Digests) TryLockDigest(ctx context.Context) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext('flagnetic.governance.stale_flag_digest@' || current_schema()))`).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("taking the stale-flag digest lock: %w", err)
	}
	return held, nil
}

// PruneDigestSlots deletes the slots that began before cutoff.
func (s *Digests) PruneDigestSlots(ctx context.Context, cutoff time.Time) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM stale_flag_digests WHERE slot < $1`, cutoff); err != nil {
		return fmt.Errorf("pruning stale-flag digest slots: %w", err)
	}
	return nil
}

// ProjectsAwaitingDigest returns the projects with no row for slot, by key.
func (s *Digests) ProjectsAwaitingDigest(ctx context.Context, slot time.Time) ([]appgovernance.DigestProject, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT p.id, p.key FROM projects p
		WHERE NOT EXISTS (SELECT 1 FROM stale_flag_digests d WHERE d.project_id = p.id AND d.slot = $1)
		ORDER BY p.key COLLATE "C"`, slot)
	if err != nil {
		return nil, fmt.Errorf("listing the projects awaiting a digest: %w", err)
	}
	projects, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appgovernance.DigestProject, error) {
		var (
			p   appgovernance.DigestProject
			raw [16]byte
		)
		if scanErr := row.Scan(&raw, &p.Key); scanErr != nil {
			return p, scanErr
		}
		p.ID = id.FromBytes[workspace.Project](raw)
		return p, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the projects awaiting a digest: %w", err)
	}
	return projects, nil
}

// RecordDigestSlot marks slot handled by project; false when it already was.
func (s *Digests) RecordDigestSlot(ctx context.Context, project workspace.ProjectID, slot time.Time) (bool, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		INSERT INTO stale_flag_digests (project_id, slot) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		project.Bytes(), slot)
	if err != nil {
		return false, fmt.Errorf("recording a stale-flag digest slot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
