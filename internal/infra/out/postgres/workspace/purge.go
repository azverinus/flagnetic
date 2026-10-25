package workspace

import (
	"context"
	"fmt"
	"slices"
)

// projectPurgeTables are the tables holding rows of a project without a
// foreign key to it, which deleting the project cannot cascade to: the IDs
// of the events received, kept apart from the raw events so a retry is
// stored once. Each is keyed by project_id first, so finding the projects
// it holds rows of costs one index probe per project.
var projectPurgeTables = []string{"evaluation_summary_dedup", "alias_dedup", "exposure_dedup"}

// ProjectPurgeTables returns the tables the project purge deletes a deleted
// project's rows from.
func ProjectPurgeTables() []string { return slices.Clone(projectPurgeTables) }

// TryLockProjectPurge takes the purge lock until the transaction ends,
// without waiting; false when another replica holds it. The key names the
// schema too, so instances sharing a database in separate schemas purge
// independently.
func (s *Store) TryLockProjectPurge(ctx context.Context) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext('flagnetic.workspace.project_purge@' || current_schema()))`).Scan(&held)
	return held, err
}

// PurgeDeletedProjects deletes at most limit rows, table after table, of
// projects that no longer exist. A table's projects are walked from its
// index one at a time rather than by scanning its rows, so a table of
// millions of rows of a few live projects costs a few probes.
func (s *Store) PurgeDeletedProjects(ctx context.Context, limit int) (int, error) {
	purged := 0
	for _, table := range projectPurgeTables {
		if purged == limit {
			break
		}
		tag, err := s.db(ctx).Exec(ctx, `
			WITH RECURSIVE held (project_id) AS (
				(SELECT project_id FROM `+table+` ORDER BY project_id LIMIT 1)
				UNION ALL
				SELECT (SELECT t.project_id FROM `+table+` t WHERE t.project_id > held.project_id ORDER BY t.project_id LIMIT 1)
				FROM held WHERE held.project_id IS NOT NULL
			)
			DELETE FROM `+table+` WHERE ctid = ANY (ARRAY(
				SELECT ctid FROM `+table+`
				WHERE project_id IN (
					SELECT project_id FROM held
					WHERE project_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = held.project_id))
				LIMIT $1))`, limit-purged)
		if err != nil {
			return purged, fmt.Errorf("purging deleted projects from %s: %w", table, err)
		}
		purged += int(tag.RowsAffected())
	}
	return purged, nil
}
