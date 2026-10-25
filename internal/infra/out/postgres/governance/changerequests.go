package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres/projectrow"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ChangeRequests is the repository of change requests.
type ChangeRequests struct {
	pool *pgxpool.Pool
}

// NewChangeRequests returns the repository on pool.
func NewChangeRequests(pool *pgxpool.Pool) *ChangeRequests { return &ChangeRequests{pool: pool} }

func (s *ChangeRequests) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// LockProject locks project's row for the unit of work, or answers
// appgovernance.ErrNotFound when there is none. It is the lock every write
// of a project takes, so a change request transition and a write to the
// entity it proposes for run one at a time.
func (s *ChangeRequests) LockProject(ctx context.Context, project workspace.ProjectID) error {
	var one int
	err := s.db(ctx).QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, project.Bytes()).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return appgovernance.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locking a project: %w", err)
	}
	projectrow.MarkHeld(ctx, project)
	return nil
}

// documents encodes what a change request holds as JSON: its base, its
// proposal and its diff.
func documents(cr governance.ChangeRequest) (base, proposed, diff []byte, err error) {
	if base, err = json.Marshal(cr.Base); err != nil {
		return nil, nil, nil, err
	}
	if proposed, err = json.Marshal(cr.Proposed); err != nil {
		return nil, nil, nil, err
	}
	ops := make([]map[string]any, len(cr.Diff))
	for i, op := range cr.Diff {
		ops[i] = appgovernance.PatchOperationValues(op)
	}
	diff, err = json.Marshal(ops)
	return base, proposed, diff, err
}

// CreateChangeRequest stores cr.
func (s *ChangeRequests) CreateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error {
	base, proposed, diff, err := documents(cr)
	if err != nil {
		return fmt.Errorf("encoding a change request: %w", err)
	}
	var flag, environment any
	if cr.Flag != nil {
		flag = cr.Flag.ID.Bytes()
	}
	if cr.Environment != nil {
		environment = cr.Environment.ID.Bytes()
	}
	var member, token any
	if cr.Author.Member != nil {
		member = cr.Author.Member.Bytes()
	}
	if cr.Author.Token != nil {
		token = cr.Author.Token.Bytes()
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO change_requests (id, project_id, kind, flag_id, environment_id, state, title, description, base,
		    base_version, proposed, diff, author_id, author_token_id, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		cr.ID.Bytes(), cr.Project.Bytes(), string(cr.Kind), flag, environment, string(cr.State), cr.Title, cr.Description,
		base, cr.BaseVersion, proposed, diff, member, token, cr.CreatedAt, cr.UpdatedAt, cr.Version)
	if err != nil {
		return fmt.Errorf("creating a change request: %w", err)
	}
	return nil
}

// UpdateChangeRequest stores what a transition changes of cr: its state,
// texts, base, proposal and diff. The entity it names and its author never
// change.
func (s *ChangeRequests) UpdateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error {
	base, proposed, diff, err := documents(cr)
	if err != nil {
		return fmt.Errorf("encoding a change request: %w", err)
	}
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE change_requests
		SET state = $3, title = $4, description = $5, base = $6, base_version = $7, proposed = $8, diff = $9,
		    updated_at = $10, version = $11
		WHERE project_id = $1 AND id = $2`,
		cr.Project.Bytes(), cr.ID.Bytes(), string(cr.State), cr.Title, cr.Description, base, cr.BaseVersion, proposed, diff,
		cr.UpdatedAt, cr.Version)
	if err != nil {
		return fmt.Errorf("updating a change request: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return appgovernance.ErrNotFound
	}
	return nil
}

// changeRequestColumns are a change request's with the keys of the flag and
// environment it names and its author's name as they are now.
const changeRequestColumns = `cr.id, cr.project_id, cr.kind, cr.flag_id, f.key, cr.environment_id, e.key, cr.state, cr.title,
	cr.description, cr.base, cr.base_version, cr.proposed, cr.diff, cr.author_id, cr.author_token_id,
	coalesce(m.display_name, t.name, ''), cr.created_at, cr.updated_at, cr.version`

const changeRequestFrom = ` FROM change_requests cr
	LEFT JOIN flags f ON f.id = cr.flag_id
	LEFT JOIN environments e ON e.id = cr.environment_id
	LEFT JOIN members m ON m.id = cr.author_id
	LEFT JOIN api_tokens t ON t.id = cr.author_token_id`

func scanChangeRequest(row pgx.Row) (governance.ChangeRequest, error) {
	var (
		cr                      governance.ChangeRequest
		crk, pk                 [16]byte
		fk, ek, member, token   *[16]byte
		flagKey, environmentKey *string
		kind, state             string
		base, proposed, diff    []byte
	)
	if err := row.Scan(&crk, &pk, &kind, &fk, &flagKey, &ek, &environmentKey, &state, &cr.Title, &cr.Description, &base,
		&cr.BaseVersion, &proposed, &diff, &member, &token, &cr.Author.Name, &cr.CreatedAt, &cr.UpdatedAt,
		&cr.Version); err != nil {
		return cr, err
	}
	cr.ID, cr.Project = id.FromBytes[governance.ChangeRequest](crk), id.FromBytes[workspace.Project](pk)
	cr.Kind, cr.State = governance.ChangeRequestKind(kind), governance.ChangeRequestState(state)
	if fk != nil {
		cr.Flag = &governance.FlagRef{ID: id.FromBytes[flags.Flag](*fk), Key: *flagKey}
	}
	if ek != nil {
		cr.Environment = &governance.EnvironmentRef{ID: id.FromBytes[workspace.Environment](*ek), Key: *environmentKey}
	}
	if member != nil {
		m := id.FromBytes[access.Member](*member)
		cr.Author.Member = &m
	}
	if token != nil {
		t := id.FromBytes[access.APIToken](*token)
		cr.Author.Token = &t
	}
	if err := json.Unmarshal(base, &cr.Base); err != nil {
		return cr, fmt.Errorf("decoding a change request's base: %w", err)
	}
	if err := json.Unmarshal(proposed, &cr.Proposed); err != nil {
		return cr, fmt.Errorf("decoding a change request's proposal: %w", err)
	}
	var ops []struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(diff, &ops); err != nil {
		return cr, fmt.Errorf("decoding a change request's diff: %w", err)
	}
	cr.Diff = make([]governance.PatchOperation, len(ops))
	for i, op := range ops {
		cr.Diff[i] = governance.PatchOperation{Op: op.Op, Path: op.Path, Value: op.Value}
	}
	cr.CreatedAt, cr.UpdatedAt = cr.CreatedAt.UTC(), cr.UpdatedAt.UTC()
	return cr, nil
}

// ChangeRequest returns project's change request with changeRequest, or
// appgovernance.ErrNotFound.
func (s *ChangeRequests) ChangeRequest(ctx context.Context, project workspace.ProjectID, changeRequest governance.ChangeRequestID,
) (governance.ChangeRequest, error) {
	cr, err := scanChangeRequest(s.db(ctx).QueryRow(ctx, `SELECT `+changeRequestColumns+changeRequestFrom+`
		WHERE cr.project_id = $1 AND cr.id = $2`, project.Bytes(), changeRequest.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return cr, appgovernance.ErrNotFound
	}
	if err != nil {
		return cr, fmt.Errorf("reading a change request: %w", err)
	}
	return cr, nil
}

// ChangeRequests returns at most limit change requests matching f, older
// than after (unless zero), newest first.
func (s *ChangeRequests) ChangeRequests(ctx context.Context, f appgovernance.ChangeRequestFilter, after governance.ChangeRequestID,
	limit int,
) ([]governance.ChangeRequest, error) {
	var cursor any
	if !after.IsZero() {
		cursor = after.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT `+changeRequestColumns+changeRequestFrom+`
		WHERE cr.project_id = $1
		  AND ($2 = '' OR f.key = $2)
		  AND ($3 = '' OR e.key = $3)
		  AND ($4 = '' OR cr.kind = $4)
		  AND ($5 = '' OR cr.state = $5)
		  AND ($6 = '' OR cr.author_id::text = $6 OR cr.author_token_id::text = $6)
		  AND ($7::uuid IS NULL OR cr.id < $7)
		ORDER BY cr.id DESC
		LIMIT $8`,
		f.Project.Bytes(), f.Flag, f.Environment, string(f.Kind), string(f.State), f.Author, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("listing change requests: %w", err)
	}
	defer rows.Close()
	out := []governance.ChangeRequest{}
	for rows.Next() {
		cr, err := scanChangeRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}
