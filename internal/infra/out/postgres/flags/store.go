// Package flags is the PostgreSQL repository of the flags context: flags,
// their configuration in each environment, and segments.
package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres/projectrow"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of flags, configurations and segments.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// LockProject locks project's row for the unit of work, or answers
// appflags.ErrNotFound when there is none.
func (s *Store) LockProject(ctx context.Context, project workspace.ProjectID) error {
	var one int
	err := s.db(ctx).QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, project.Bytes()).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return appflags.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locking a project: %w", err)
	}
	projectrow.MarkHeld(ctx, project)
	return nil
}

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// keyTaken returns already_exists for a unique violation of a key, err
// otherwise.
func keyTaken(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperrors.Conflict("already_exists", "the project has a "+what+" with this key already").WithCause(err)
	}
	return err
}

// variationDoc is a variation as the flags table stores it.
type variationDoc struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
}

func variationsDoc(vs []flags.Variation) ([]byte, error) {
	docs := make([]variationDoc, len(vs))
	for i, v := range vs {
		docs[i] = variationDoc(v)
	}
	return json.Marshal(docs)
}

// CreateFlag stores f.
func (s *Store) CreateFlag(ctx context.Context, f flags.Flag) error {
	variations, err := variationsDoc(f.Variations)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO flags (id, project_id, key, type, name, description, owner_id, tags, lifetime, expected_removal_at,
		    automatic_exposure, variations, archived_at, created_by, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		f.ID.Bytes(), f.Project.Bytes(), f.Key, string(f.Type), f.Name, f.Description, f.Owner.Bytes(), f.Tags,
		string(f.Lifetime), f.ExpectedRemovalAt, f.AutomaticExposure, variations, f.ArchivedAt, optionalMember(f.CreatedBy),
		f.CreatedAt, f.UpdatedAt, f.Version)
	return keyTaken(err, "flag")
}

// UpdateFlag stores f's definition.
func (s *Store) UpdateFlag(ctx context.Context, f flags.Flag) error {
	variations, err := variationsDoc(f.Variations)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		UPDATE flags SET name = $2, description = $3, owner_id = $4, tags = $5, lifetime = $6, expected_removal_at = $7,
		    automatic_exposure = $8, variations = $9, archived_at = $10, updated_at = $11, version = $12
		WHERE id = $1`,
		f.ID.Bytes(), f.Name, f.Description, f.Owner.Bytes(), f.Tags, string(f.Lifetime), f.ExpectedRemovalAt,
		f.AutomaticExposure, variations, f.ArchivedAt, f.UpdatedAt, f.Version)
	return err
}

func optionalMember(m *access.MemberID) any {
	if m == nil {
		return nil
	}
	return m.Bytes()
}

const flagColumns = `id, project_id, key, type, name, description, owner_id, tags, lifetime, expected_removal_at,
	automatic_exposure, variations, archived_at, created_by, created_at, updated_at, version`

// scanFlag reads a flag from row's leading columns, flagColumns, and the
// columns after them into extra.
func scanFlag(row pgx.Row, extra ...any) (flags.Flag, error) {
	var (
		f                   flags.Flag
		fk, pk, owner       [16]byte
		createdBy           *[16]byte
		typ, lifetime       string
		variations          []byte
		removal, archivedAt *time.Time
	)
	dest := []any{&fk, &pk, &f.Key, &typ, &f.Name, &f.Description, &owner, &f.Tags, &lifetime, &removal,
		&f.AutomaticExposure, &variations, &archivedAt, &createdBy, &f.CreatedAt, &f.UpdatedAt, &f.Version}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return flags.Flag{}, err
	}
	var docs []variationDoc
	if err := json.Unmarshal(variations, &docs); err != nil {
		return flags.Flag{}, fmt.Errorf("decoding variations: %w", err)
	}
	for _, d := range docs {
		f.Variations = append(f.Variations, flags.Variation(d))
	}
	f.ID, f.Project, f.Owner = id.FromBytes[flags.Flag](fk), id.FromBytes[workspace.Project](pk), id.FromBytes[access.Member](owner)
	f.Type, f.Lifetime = flags.Type(typ), flags.Lifetime(lifetime)
	f.ExpectedRemovalAt, f.ArchivedAt = utc(removal), utc(archivedAt)
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	if createdBy != nil {
		m := id.FromBytes[access.Member](*createdBy)
		f.CreatedBy = &m
	}
	if f.Tags == nil {
		f.Tags = []string{}
	}
	return f, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// FlagByKey returns project's flag with key, or appflags.ErrNotFound.
func (s *Store) FlagByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Flag, error) {
	f, err := scanFlag(s.db(ctx).QueryRow(ctx, `SELECT `+flagColumns+` FROM flags WHERE project_id = $1 AND key = $2`,
		project.Bytes(), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return f, appflags.ErrNotFound
	}
	if err != nil {
		return f, fmt.Errorf("reading a flag: %w", err)
	}
	return f, nil
}

// DeleteFlag deletes the flag with its configurations.
func (s *Store) DeleteFlag(ctx context.Context, flag flags.FlagID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM flags WHERE id = $1`, flag.Bytes())
	return err
}

// ProjectFlags returns every flag of project, archived ones included.
func (s *Store) ProjectFlags(ctx context.Context, project workspace.ProjectID) ([]flags.Flag, error) {
	return s.flags(ctx, `SELECT `+flagColumns+` FROM flags WHERE project_id = $1 ORDER BY key COLLATE "C"`, project.Bytes())
}

func (s *Store) flags(ctx context.Context, sql string, args ...any) ([]flags.Flag, error) {
	rows, err := s.db(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("listing flags: %w", err)
	}
	defer rows.Close()
	out := []flags.Flag{}
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UnarchivedFlags returns up to limit of project's flags that are not
// archived, each with its configuration in every environment by the
// environments' order, by key in byte order, those whose key is greater
// than after when it is not "": two queries whatever the limit.
func (s *Store) UnarchivedFlags(ctx context.Context, project workspace.ProjectID, after string, limit int) ([]appflags.FlagView, error) {
	fs, err := s.flags(ctx, `SELECT `+flagColumns+` FROM flags
		WHERE project_id = $1 AND archived_at IS NULL AND ($2 = '' OR key COLLATE "C" > $2)
		ORDER BY key COLLATE "C" LIMIT $3`, project.Bytes(), after, limit)
	if err != nil || len(fs) == 0 {
		return []appflags.FlagView{}, err
	}
	ids := make([][16]byte, len(fs))
	for i, f := range fs {
		ids[i] = f.ID.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, configurationSelect+`
		WHERE c.flag_id = ANY($1)
		ORDER BY e.sort_order, e.key COLLATE "C"`, ids)
	if err != nil {
		return nil, fmt.Errorf("reading configurations: %w", err)
	}
	defer rows.Close()
	configs := map[flags.FlagID][]flags.EnvironmentConfiguration{}
	for rows.Next() {
		ec, err := scanConfiguration(rows)
		if err != nil {
			return nil, err
		}
		configs[ec.Configuration.Flag] = append(configs[ec.Configuration.Flag], ec)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading configurations: %w", err)
	}
	out := make([]appflags.FlagView, len(fs))
	for i, f := range fs {
		out[i] = appflags.FlagView{Flag: f, Configurations: configs[f.ID]}
		if out[i].Configurations == nil {
			out[i].Configurations = []flags.EnvironmentConfiguration{}
		}
	}
	return out, nil
}
