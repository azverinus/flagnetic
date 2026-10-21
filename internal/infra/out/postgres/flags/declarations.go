package flags

import (
	"context"
	"fmt"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// DeclarationReferences returns what in project names the unit type or
// attribute keyed key: the segments whose conditions name it, by key, then
// one reference per flag and environment whose configuration names it in a
// rule's condition, a rollout or an override, archived flags included, by
// flag key and then environment order. A unit type and an attribute never
// share a key, so the key alone says which is meant, and a unit type is
// named as an attribute too, holding the unit's ID. Containment of the key
// finds every kind through the GIN indexes on segments' conditions and
// configurations' documents.
func (s *Store) DeclarationReferences(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT * FROM (
		    SELECT 0 AS rank, 'segment' AS kind, key, NULL::text AS environment, 0 AS sort_order FROM segments
		    WHERE project_id = $1 AND conditions @> jsonb_build_array(jsonb_build_object('attribute', $2::text))
		    UNION ALL
		    SELECT 1, 'flag', f.key, e.key, e.sort_order
		    FROM flag_configurations c
		    JOIN flags f ON f.id = c.flag_id
		    JOIN environments e ON e.id = c.environment_id
		    WHERE f.project_id = $1
		      AND (c.document @> jsonb_build_object('overrides', jsonb_build_array(jsonb_build_object('unit_type', $2::text)))
		        OR c.document @> jsonb_build_object('rules', jsonb_build_array(
		            jsonb_build_object('serve', jsonb_build_object('unit_type', $2::text))))
		        OR c.document @> jsonb_build_object('rules', jsonb_build_array(
		            jsonb_build_object('conditions', jsonb_build_array(jsonb_build_object('attribute', $2::text))))))
		) r
		ORDER BY rank, key COLLATE "C", sort_order, environment COLLATE "C"`, project.Bytes(), key)
	if err != nil {
		return nil, fmt.Errorf("finding the references of a declaration: %w", err)
	}
	defer rows.Close()
	out := []flags.Reference{}
	for rows.Next() {
		var (
			r           flags.Reference
			env         *string
			rank, order int
		)
		if err := rows.Scan(&rank, &r.Kind, &r.Key, &env, &order); err != nil {
			return nil, err
		}
		if env != nil {
			r.Environment = *env
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeclarationUsageCounts returns, by the key of each unit type or attribute
// of project something names, how many flags and segments name it: a flag,
// archived ones included, once whatever its environments and however many
// rules, rollouts and overrides name it there, as DeclarationReferences
// finds them; a segment once however many of its conditions do. One query
// answers every declaration of the project.
func (s *Store) DeclarationUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error) {
	return s.counts(ctx, `
		SELECT named.key, count(*)
		FROM (
		    SELECT c.flag_id AS user_id, n.key
		    FROM flag_configurations c
		    JOIN flags f ON f.id = c.flag_id
		    CROSS JOIN LATERAL (
		        SELECT o->>'unit_type' FROM jsonb_array_elements(c.document->'overrides') o
		        UNION ALL
		        SELECT r->'serve'->>'unit_type' FROM jsonb_array_elements(c.document->'rules') r
		        UNION ALL
		        SELECT cond->>'attribute' FROM jsonb_array_elements(c.document->'rules') r,
		            jsonb_array_elements(r->'conditions') cond
		    ) n(key)
		    WHERE f.project_id = $1 AND n.key IS NOT NULL
		    UNION
		    SELECT s.id, cond->>'attribute' FROM segments s, jsonb_array_elements(s.conditions) cond
		    WHERE s.project_id = $1
		) named
		GROUP BY named.key`, project)
}

// SegmentUsageCounts returns, by the key of each segment of project a rule
// names, how many flags' rules name it, archived flags included, each flag
// once whatever its environments, as SegmentUsages finds them. One query
// answers every segment of the project.
func (s *Store) SegmentUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error) {
	return s.counts(ctx, `
		SELECT cond->>'segment', count(DISTINCT c.flag_id)
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		CROSS JOIN LATERAL jsonb_array_elements(c.document->'rules') r
		CROSS JOIN LATERAL jsonb_array_elements(r->'conditions') cond
		WHERE f.project_id = $1 AND cond->>'segment' IS NOT NULL
		GROUP BY 1`, project)
}

// counts returns the key and count of each row sql answers for project.
func (s *Store) counts(ctx context.Context, sql string, project workspace.ProjectID) (map[string]int, error) {
	rows, err := s.db(ctx).Query(ctx, sql, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("counting usages: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			key string
			n   int
		)
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}
