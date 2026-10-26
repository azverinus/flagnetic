package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
		ops[i] = op.Values()
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
	var flag, environment, segment any
	if cr.Flag != nil {
		flag = cr.Flag.ID.Bytes()
	}
	if cr.Environment != nil {
		environment = cr.Environment.ID.Bytes()
	}
	if cr.Segment != nil {
		segment = cr.Segment.ID.Bytes()
	}
	var member, token any
	if cr.Author.Member != nil {
		member = cr.Author.Member.Bytes()
	}
	if cr.Author.Token != nil {
		token = cr.Author.Token.Bytes()
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO change_requests (id, project_id, kind, flag_id, environment_id, segment_id, state, title, description,
		    base, base_version, base_referrers, proposed, diff, author_id, author_token_id, created_at, updated_at, version,
		    apply_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		cr.ID.Bytes(), cr.Project.Bytes(), string(cr.Kind), flag, environment, segment, string(cr.State), cr.Title,
		cr.Description, base, cr.BaseVersion, referrersOf(cr), proposed, diff, member, token, cr.CreatedAt, cr.UpdatedAt,
		cr.Version, cr.ApplyAt)
	if err != nil {
		return fmt.Errorf("creating a change request: %w", err)
	}
	return nil
}

// referrersOf returns the flags cr's segment's definition was based on as
// named by, nil for a change request to anything else.
func referrersOf(cr governance.ChangeRequest) any {
	if cr.Segment == nil {
		return nil
	}
	out := make([][16]byte, len(cr.BaseReferrers))
	for i, f := range cr.BaseReferrers {
		out[i] = f.Bytes()
	}
	return out
}

// coversOf returns the environments cr's approval covers, nil for a change
// request that records none.
func coversOf(cr governance.ChangeRequest) any {
	if cr.ApprovalCovers == nil {
		return nil
	}
	out := make([][16]byte, len(cr.ApprovalCovers))
	for i, e := range cr.ApprovalCovers {
		out[i] = e.Bytes()
	}
	return out
}

// UpdateChangeRequest stores what a transition changes of cr: its state,
// texts, base, proposal and diff, the decision taken on it, what its
// approval covers, its application, its apply time and its schedule
// failure. The entity it names and its author never change.
func (s *ChangeRequests) UpdateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error {
	base, proposed, diff, err := documents(cr)
	if err != nil {
		return fmt.Errorf("encoding a change request: %w", err)
	}
	var (
		decision, decidedBy, decidedAt, comment     any
		appliedAt, appliedKind, appliedBy, appliedT any
		supersededBy                                any
	)
	if d, kind := decisionOf(cr); d != nil {
		decision, decidedBy, decidedAt, comment = kind, d.Member.Bytes(), d.At, d.Comment
	}
	if a := cr.Applied; a != nil {
		appliedAt = a.At
		switch {
		case a.System:
			appliedKind = "system"
		case a.By.Token != nil:
			appliedKind, appliedT = "token", a.By.Token.Bytes()
		case a.By.Member != nil:
			appliedKind, appliedBy = "member", a.By.Member.Bytes()
		}
	}
	if !cr.SupersededBy.IsZero() {
		supersededBy = cr.SupersededBy.Bytes()
	}
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE change_requests
		SET state = $3, title = $4, description = $5, base = $6, base_version = $7, proposed = $8, diff = $9,
		    updated_at = $10, version = $11, decision = $12, decided_by = $13, decided_at = $14, decision_comment = $15,
		    applied_at = $16, applied_by_kind = $17, applied_by = $18, applied_by_token = $19, superseded_by = $20,
		    base_referrers = $21, approval_covers = $22, apply_at = $23, schedule_failed_at = $24,
		    schedule_failure_code = $25, schedule_failure_message = $26, schedule_failures = $27, schedule_stopped = $28
		WHERE project_id = $1 AND id = $2`,
		append([]any{
			cr.Project.Bytes(), cr.ID.Bytes(), string(cr.State), cr.Title, cr.Description, base, cr.BaseVersion, proposed, diff,
			cr.UpdatedAt, cr.Version, decision, decidedBy, decidedAt, comment, appliedAt, appliedKind, appliedBy, appliedT,
			supersededBy, referrersOf(cr), coversOf(cr), cr.ApplyAt,
		}, failureArgs(cr.ScheduleFailure)...)...)
	if err != nil {
		return fmt.Errorf("updating a change request: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return appgovernance.ErrNotFound
	}
	return nil
}

// RecordScheduleFailure stores cr's schedule failure and nothing else, so
// recording why a scheduled application failed never moves the change
// request's version.
func (s *ChangeRequests) RecordScheduleFailure(ctx context.Context, cr governance.ChangeRequest) error {
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE change_requests
		SET schedule_failed_at = $3, schedule_failure_code = $4, schedule_failure_message = $5, schedule_failures = $6,
		    schedule_stopped = $7
		WHERE project_id = $1 AND id = $2`,
		append([]any{cr.Project.Bytes(), cr.ID.Bytes()}, failureArgs(cr.ScheduleFailure)...)...)
	if err != nil {
		return fmt.Errorf("recording a schedule failure: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return appgovernance.ErrNotFound
	}
	return nil
}

// failureArgs are the columns of f: when, code, message, attempts and
// whether it stopped the scheduler; none and zero attempts for nil.
func failureArgs(f *governance.ScheduleFailure) []any {
	if f == nil {
		return []any{nil, nil, nil, 0, false}
	}
	return []any{f.At, f.Code, f.Message, f.Attempts, f.Stopped}
}

// DueChangeRequests returns at most limit change requests of any project
// that are approved, scheduled at or before now and not stopped by their
// failures, earliest first. It reads without a lock: whoever applies one
// locks its project's row and reads it again.
func (s *ChangeRequests) DueChangeRequests(ctx context.Context, now time.Time, limit int,
) ([]appgovernance.DueChangeRequest, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT project_id, id FROM change_requests
		WHERE state = 'approved' AND apply_at IS NOT NULL AND NOT schedule_stopped AND apply_at <= $1
		ORDER BY apply_at, id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("reading the due change requests: %w", err)
	}
	defer rows.Close()
	var out []appgovernance.DueChangeRequest
	for rows.Next() {
		var project, cr [16]byte
		if err := rows.Scan(&project, &cr); err != nil {
			return nil, err
		}
		out = append(out, appgovernance.DueChangeRequest{
			Project: id.FromBytes[workspace.Project](project), ChangeRequest: id.FromBytes[governance.ChangeRequest](cr),
		})
	}
	return out, rows.Err()
}

// decisionOf returns the decision taken on cr and its kind, or nil.
func decisionOf(cr governance.ChangeRequest) (*governance.Decision, string) {
	switch {
	case cr.Approval != nil:
		return cr.Approval, "approved"
	case cr.Rejection != nil:
		return cr.Rejection, "rejected"
	}
	return nil, ""
}

// changeRequestColumns are a change request's with the keys of the flag,
// environment and segment it names and the names of its author, of the member who
// decided it and of whoever applied it as they are now.
const changeRequestColumns = `cr.id, cr.project_id, cr.kind, cr.flag_id, f.key, cr.environment_id, e.key, cr.state, cr.title,
	cr.description, cr.base, cr.base_version, cr.proposed, cr.diff, cr.author_id, cr.author_token_id,
	coalesce(m.display_name, t.name, ''), cr.decision, cr.decided_by, coalesce(dm.display_name, ''), cr.decided_at,
	cr.decision_comment, cr.applied_at, cr.applied_by_kind, cr.applied_by, cr.applied_by_token,
	coalesce(am.display_name, ap.name, ''), cr.superseded_by, cr.created_at, cr.updated_at, cr.version, cr.segment_id,
	s.key, cr.base_referrers, cr.approval_covers, cr.apply_at, cr.schedule_failed_at, cr.schedule_failure_code,
	cr.schedule_failure_message, cr.schedule_failures, cr.schedule_stopped`

const changeRequestFrom = ` FROM change_requests cr
	LEFT JOIN flags f ON f.id = cr.flag_id
	LEFT JOIN environments e ON e.id = cr.environment_id
	LEFT JOIN segments s ON s.id = cr.segment_id
	LEFT JOIN members m ON m.id = cr.author_id
	LEFT JOIN api_tokens t ON t.id = cr.author_token_id
	LEFT JOIN members dm ON dm.id = cr.decided_by
	LEFT JOIN members am ON am.id = cr.applied_by
	LEFT JOIN api_tokens ap ON ap.id = cr.applied_by_token`

func scanChangeRequest(row pgx.Row) (governance.ChangeRequest, error) {
	var (
		cr                             governance.ChangeRequest
		crk, pk                        [16]byte
		fk, ek, member, token          *[16]byte
		flagKey, environmentKey        *string
		kind, state                    string
		base, proposed, diff           []byte
		decision, comment, appliedKind *string
		decidedBy, appliedBy, appliedT *[16]byte
		supersededBy, sk               *[16]byte
		segmentKey                     *string
		referrers, covers              [][16]byte
		deciderName, applierName       string
		decidedAt, appliedAt           *time.Time
		applyAt, failedAt              *time.Time
		failureCode, failureMessage    *string
		failures                       int
		stopped                        bool
	)
	if err := row.Scan(&crk, &pk, &kind, &fk, &flagKey, &ek, &environmentKey, &state, &cr.Title, &cr.Description, &base,
		&cr.BaseVersion, &proposed, &diff, &member, &token, &cr.Author.Name, &decision, &decidedBy, &deciderName, &decidedAt,
		&comment, &appliedAt, &appliedKind, &appliedBy, &appliedT, &applierName, &supersededBy, &cr.CreatedAt, &cr.UpdatedAt,
		&cr.Version, &sk, &segmentKey, &referrers, &covers, &applyAt, &failedAt, &failureCode, &failureMessage, &failures,
		&stopped); err != nil {
		return cr, err
	}
	if applyAt != nil {
		at := applyAt.UTC()
		cr.ApplyAt = &at
	}
	if failedAt != nil {
		cr.ScheduleFailure = &governance.ScheduleFailure{
			At: failedAt.UTC(), Code: *failureCode, Message: *failureMessage, Attempts: failures, Stopped: stopped,
		}
	}
	if decision != nil {
		d := &governance.Decision{
			Member: id.FromBytes[access.Member](*decidedBy), Name: deciderName, At: decidedAt.UTC(), Comment: *comment,
		}
		if *decision == "approved" {
			cr.Approval = d
		} else {
			cr.Rejection = d
		}
	}
	if appliedAt != nil {
		a := &governance.Application{At: appliedAt.UTC(), System: *appliedKind == "system"}
		switch {
		case appliedBy != nil:
			m := id.FromBytes[access.Member](*appliedBy)
			a.By = governance.Author{Member: &m, Name: applierName}
		case appliedT != nil:
			t := id.FromBytes[access.APIToken](*appliedT)
			a.By = governance.Author{Token: &t, Name: applierName}
		}
		cr.Applied = a
	}
	if supersededBy != nil {
		cr.SupersededBy = id.FromBytes[governance.Entry](*supersededBy)
	}
	cr.ID, cr.Project = id.FromBytes[governance.ChangeRequest](crk), id.FromBytes[workspace.Project](pk)
	cr.Kind, cr.State = governance.ChangeRequestKind(kind), governance.ChangeRequestState(state)
	if fk != nil {
		cr.Flag = &governance.FlagRef{ID: id.FromBytes[flags.Flag](*fk), Key: *flagKey}
	}
	if ek != nil {
		cr.Environment = &governance.EnvironmentRef{ID: id.FromBytes[workspace.Environment](*ek), Key: *environmentKey}
	}
	if sk != nil {
		cr.Segment = &governance.SegmentRef{ID: id.FromBytes[flags.Segment](*sk), Key: *segmentKey}
	}
	for _, f := range referrers {
		cr.BaseReferrers = append(cr.BaseReferrers, id.FromBytes[flags.Flag](f))
	}
	if covers != nil {
		cr.ApprovalCovers = make([]workspace.EnvironmentID, len(covers))
		for i, e := range covers {
			cr.ApprovalCovers[i] = id.FromBytes[workspace.Environment](e)
		}
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
		  AND ($9 = '' OR cr.author_id IS NULL OR cr.author_id::text <> $9)
		  AND ($10 = '' OR s.key = $10)
		  AND ($11::boolean IS NULL OR (cr.apply_at IS NOT NULL) = $11)
		ORDER BY cr.id DESC
		LIMIT $8`,
		f.Project.Bytes(), f.Flag, f.Environment, string(f.Kind), string(f.State), f.Author, cursor, limit, f.NotAuthoredBy,
		f.Segment, f.Scheduled)
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

// Waiting returns project's open and approved change requests moved may
// move: those naming its flag, its deleted environment or its segment and, when it
// moved configurations, those to a segment's definition, which the flag may
// name. It reads under the project's row lock the writing transaction
// holds, so none is approved or applied meanwhile.
func (s *ChangeRequests) Waiting(ctx context.Context, project workspace.ProjectID, moved governance.Moved,
) ([]governance.ChangeRequest, error) {
	var flag, environment, segment any
	if !moved.Flag.IsZero() {
		flag = moved.Flag.Bytes()
	}
	// A write to a configuration moves change requests of its flag alone;
	// only deleting an environment moves every one naming it.
	if moved.EnvironmentDeleted {
		environment = moved.Environment.Bytes()
	}
	if !moved.Segment.IsZero() {
		segment = moved.Segment.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT `+changeRequestColumns+changeRequestFrom+`
		WHERE cr.project_id = $1 AND cr.state IN ('open', 'approved')
		  AND (cr.flag_id = $2 OR cr.environment_id = $3 OR cr.segment_id = $4 OR ($5 AND cr.segment_id IS NOT NULL))
		ORDER BY cr.id`,
		project.Bytes(), flag, environment, segment, moved.Configurations)
	if err != nil {
		return nil, fmt.Errorf("reading the waiting change requests: %w", err)
	}
	defer rows.Close()
	var out []governance.ChangeRequest
	for rows.Next() {
		cr, err := scanChangeRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}

// WaitingOn returns how many of the change requests on flag's
// configuration in environment are open and how many approved, and the
// earliest apply time among them that the scheduler will still act on: one
// whose failures stopped it is applied at no time.
func (s *ChangeRequests) WaitingOn(ctx context.Context, flag flags.FlagID, environment workspace.EnvironmentID,
) (governance.Waiting, error) {
	var w governance.Waiting
	err := s.db(ctx).QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'open'), count(*) FILTER (WHERE state = 'approved'),
		       min(apply_at) FILTER (WHERE NOT schedule_stopped)
		FROM change_requests
		WHERE flag_id = $1 AND environment_id = $2 AND state IN ('open', 'approved')`,
		flag.Bytes(), environment.Bytes()).Scan(&w.Open, &w.Approved, &w.NextApplyAt)
	if err != nil {
		return w, fmt.Errorf("counting the waiting change requests: %w", err)
	}
	if w.NextApplyAt != nil {
		at := w.NextApplyAt.UTC()
		w.NextApplyAt = &at
	}
	return w, nil
}
