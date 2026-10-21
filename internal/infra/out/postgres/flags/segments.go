package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// conditionDoc is a condition as segments and rules store it, the shape a
// ruleset carries it in.
type conditionDoc struct {
	Kind      string `json:"kind"`
	Attribute string `json:"attribute,omitempty"`
	Op        string `json:"op,omitempty"`
	Value     any    `json:"value,omitempty"`
	Segment   string `json:"segment,omitempty"`
	Flag      string `json:"flag,omitempty"`
	Variation string `json:"variation,omitempty"`
}

func conditionsDoc(cs []flags.Condition) ([]byte, error) {
	docs := make([]conditionDoc, len(cs))
	for i, c := range cs {
		docs[i] = conditionDoc(c)
	}
	return json.Marshal(docs)
}

func conditionsOf(doc []byte) ([]flags.Condition, error) {
	var docs []conditionDoc
	if err := json.Unmarshal(doc, &docs); err != nil {
		return nil, fmt.Errorf("decoding conditions: %w", err)
	}
	out := make([]flags.Condition, len(docs))
	for i, d := range docs {
		out[i] = flags.Condition(d)
	}
	return out, nil
}

// CreateSegment stores s.
func (s *Store) CreateSegment(ctx context.Context, seg flags.Segment) error {
	conditions, err := conditionsDoc(seg.Conditions)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO segments (id, project_id, key, name, description, private, conditions, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		seg.ID.Bytes(), seg.Project.Bytes(), seg.Key, seg.Name, seg.Description, seg.Private, conditions,
		seg.CreatedAt, seg.UpdatedAt, seg.Version)
	return keyTaken(err, "segment")
}

// UpdateSegment stores s's changes.
func (s *Store) UpdateSegment(ctx context.Context, seg flags.Segment) error {
	conditions, err := conditionsDoc(seg.Conditions)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		UPDATE segments SET name = $2, description = $3, private = $4, conditions = $5, updated_at = $6, version = $7
		WHERE id = $1`,
		seg.ID.Bytes(), seg.Name, seg.Description, seg.Private, conditions, seg.UpdatedAt, seg.Version)
	return err
}

// DeleteSegment deletes the segment.
func (s *Store) DeleteSegment(ctx context.Context, segment flags.SegmentID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM segments WHERE id = $1`, segment.Bytes())
	return err
}

const segmentColumns = `id, project_id, key, name, description, private, conditions, created_at, updated_at, version`

func scanSegment(row pgx.Row) (flags.Segment, error) {
	var (
		seg        flags.Segment
		sk, pk     [16]byte
		conditions []byte
	)
	if err := row.Scan(&sk, &pk, &seg.Key, &seg.Name, &seg.Description, &seg.Private, &conditions,
		&seg.CreatedAt, &seg.UpdatedAt, &seg.Version); err != nil {
		return flags.Segment{}, err
	}
	cs, err := conditionsOf(conditions)
	if err != nil {
		return flags.Segment{}, err
	}
	seg.ID, seg.Project, seg.Conditions = id.FromBytes[flags.Segment](sk), id.FromBytes[workspace.Project](pk), cs
	seg.CreatedAt, seg.UpdatedAt = seg.CreatedAt.UTC(), seg.UpdatedAt.UTC()
	return seg, nil
}

// SegmentByKey returns project's segment with key, or appflags.ErrNotFound.
func (s *Store) SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error) {
	seg, err := scanSegment(s.db(ctx).QueryRow(ctx, `SELECT `+segmentColumns+` FROM segments WHERE project_id = $1 AND key = $2`,
		project.Bytes(), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return seg, appflags.ErrNotFound
	}
	if err != nil {
		return seg, fmt.Errorf("reading a segment: %w", err)
	}
	return seg, nil
}

// Segments returns project's segments by key.
func (s *Store) Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+segmentColumns+` FROM segments WHERE project_id = $1 ORDER BY key COLLATE "C"`,
		project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing segments: %w", err)
	}
	defer rows.Close()
	out := []flags.Segment{}
	for rows.Next() {
		seg, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, seg)
	}
	return out, rows.Err()
}

// SegmentUsages returns one reference per flag and environment of project
// whose rules have a condition naming the segment key, archived flags
// included, by flag key and then environment order. Only a segment
// condition names a segment, so containment of the key alone finds both
// kinds, through the configurations' GIN index. A configuration stores rules
// only once its whole document can be replaced; until then no document has
// a rules key, and a segment has no usages.
func (s *Store) SegmentUsages(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT f.key, e.key
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		JOIN environments e ON e.id = c.environment_id
		WHERE f.project_id = $1
		  AND c.document @> jsonb_build_object('rules', jsonb_build_array(
		      jsonb_build_object('conditions', jsonb_build_array(jsonb_build_object('segment', $2::text)))))
		ORDER BY f.key COLLATE "C", e.sort_order, e.key COLLATE "C"`, project.Bytes(), key)
	if err != nil {
		return nil, fmt.Errorf("finding the usages of a segment: %w", err)
	}
	defer rows.Close()
	out := []flags.Reference{}
	for rows.Next() {
		r := flags.Reference{Kind: "flag"}
		if err := rows.Scan(&r.Key, &r.Environment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
