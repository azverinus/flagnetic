package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// grantColumns are the columns scanGrant reads, prefixed by the grants
// table's alias g.
const grantColumns = `g.id, g.project_id, g.member_id, g.role, g.source, g.created_by, g.created_at`

// scanGrant returns a scan target of grantColumns and the grant it fills.
func scanGrant() ([]any, func() access.Grant) {
	var (
		g                          access.Grant
		grantID, projectID, member [16]byte
		role, source               string
		createdBy                  *[16]byte
	)
	dest := []any{&grantID, &projectID, &member, &role, &source, &createdBy, &g.CreatedAt}
	fill := func() access.Grant {
		g.ID, g.Project, g.Member = id.FromBytes[access.Grant](grantID), id.FromBytes[workspace.Project](projectID),
			id.FromBytes[access.Member](member)
		g.Role, g.Source = access.RoleName(role), access.GrantSource(source)
		if createdBy != nil {
			g.CreatedBy = id.FromBytes[access.Member](*createdBy)
		}
		return g
	}
	return dest, fill
}

func collectGrants(rows pgx.Rows, err error) ([]access.Grant, error) {
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	grants, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (access.Grant, error) {
		dest, grant := scanGrant()
		if scanErr := row.Scan(dest...); scanErr != nil {
			return access.Grant{}, scanErr
		}
		return grant(), nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	return grants, nil
}

// MemberGrants returns the grants member holds in project, oldest first.
func (s *Store) MemberGrants(ctx context.Context, member access.MemberID, project workspace.ProjectID) ([]access.Grant, error) {
	return collectGrants(s.db(ctx).Query(ctx, `
		SELECT `+grantColumns+` FROM grants g WHERE g.member_id = $1 AND g.project_id = $2
		ORDER BY g.created_at, g.id`, member.Bytes(), project.Bytes()))
}

// ProjectGrants returns every grant of project, oldest first.
func (s *Store) ProjectGrants(ctx context.Context, project workspace.ProjectID) ([]access.Grant, error) {
	return collectGrants(s.db(ctx).Query(ctx, `
		SELECT `+grantColumns+` FROM grants g WHERE g.project_id = $1 ORDER BY g.created_at, g.id`, project.Bytes()))
}

// GrantedProjects returns the projects member holds a grant in.
func (s *Store) GrantedProjects(ctx context.Context, member access.MemberID) ([]workspace.ProjectID, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT DISTINCT project_id FROM grants WHERE member_id = $1`, member.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing a member's projects: %w", err)
	}
	ids, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (workspace.ProjectID, error) {
		var p [16]byte
		scanErr := row.Scan(&p)
		return id.FromBytes[workspace.Project](p), scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("listing a member's projects: %w", err)
	}
	return ids, nil
}

// errAlreadyGranted refuses a grant the member holds already from the same
// source.
var errAlreadyGranted = apperrors.Conflict("already_granted", "the member holds this role from this source already")

// CreateGrant stores g; the same member, role and source in the project
// twice is refused already_granted.
func (s *Store) CreateGrant(ctx context.Context, g access.Grant) error {
	var createdBy any
	if !g.CreatedBy.IsZero() {
		createdBy = g.CreatedBy.Bytes()
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO grants (id, project_id, member_id, role, source, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		g.ID.Bytes(), g.Project.Bytes(), g.Member.Bytes(), string(g.Role), string(g.Source), createdBy, g.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errAlreadyGranted.WithCause(err)
	}
	return err
}

// DeleteGrant removes the grant of project with the identifier and returns
// it; ErrNotFound when the project holds none.
func (s *Store) DeleteGrant(ctx context.Context, project workspace.ProjectID, grant access.GrantID) (access.Grant, error) {
	dest, deleted := scanGrant()
	err := s.db(ctx).QueryRow(ctx, `
		DELETE FROM grants g WHERE g.project_id = $1 AND g.id = $2 RETURNING `+grantColumns,
		project.Bytes(), grant.Bytes()).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Grant{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Grant{}, fmt.Errorf("deleting a grant: %w", err)
	}
	return deleted(), nil
}

// LockGrant returns the grant of project with the identifier, locked until
// the transaction ends; ErrNotFound when the project holds none.
func (s *Store) LockGrant(ctx context.Context, project workspace.ProjectID, grant access.GrantID) (access.Grant, error) {
	dest, locked := scanGrant()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+grantColumns+` FROM grants g WHERE g.project_id = $1 AND g.id = $2 FOR UPDATE`,
		project.Bytes(), grant.Bytes()).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Grant{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Grant{}, fmt.Errorf("locking a grant: %w", err)
	}
	return locked(), nil
}

// ProjectMembers returns the members holding grants in project, ordered by
// email, each with their grants oldest first, in one query.
func (s *Store) ProjectMembers(ctx context.Context, project workspace.ProjectID) ([]appaccess.ProjectMember, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+memberColumns+`, `+grantColumns+`
		FROM grants g JOIN members m ON m.id = g.member_id
		WHERE g.project_id = $1
		ORDER BY lower(m.email) COLLATE "C", m.id, g.created_at, g.id`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing a project's members: %w", err)
	}
	defer rows.Close()
	var out []appaccess.ProjectMember
	for rows.Next() {
		memberDest, member := scanMember()
		grantDest, grant := scanGrant()
		if err := rows.Scan(append(memberDest, grantDest...)...); err != nil {
			return nil, fmt.Errorf("listing a project's members: %w", err)
		}
		m, g := member(), grant()
		if n := len(out); n == 0 || out[n-1].Member.ID != m.ID {
			out = append(out, appaccess.ProjectMember{Member: m})
		}
		out[len(out)-1].Grants = append(out[len(out)-1].Grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing a project's members: %w", err)
	}
	return out, nil
}

// ProjectMemberCounts returns how many active members hold a grant in each
// of projects, in one query.
func (s *Store) ProjectMemberCounts(ctx context.Context, projects []workspace.ProjectID) (map[workspace.ProjectID]int, error) {
	keys := make([][16]byte, len(projects))
	for i, p := range projects {
		keys[i] = p.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT g.project_id, count(DISTINCT g.member_id) AS member_count
		FROM grants g JOIN members m ON m.id = g.member_id AND m.status = 'active'
		WHERE g.project_id = ANY($1)
		GROUP BY g.project_id`, keys)
	if err != nil {
		return nil, fmt.Errorf("counting projects' members: %w", err)
	}
	counts := map[workspace.ProjectID]int{}
	var (
		project [16]byte
		n       int
	)
	if _, err := pgx.ForEachRow(rows, []any{&project, &n}, func() error {
		counts[id.FromBytes[workspace.Project](project)] = n
		return nil
	}); err != nil {
		return nil, fmt.Errorf("counting projects' members: %w", err)
	}
	return counts, nil
}
