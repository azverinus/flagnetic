// Package sinks is the PostgreSQL repository of a project's sinks.
package sinks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres/projectrow"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of sinks.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// LockProject locks project's row for the unit of work, the lock every write
// of a project takes, or answers appsinks.ErrNotFound when there is none.
func (s *Store) LockProject(ctx context.Context, project workspace.ProjectID) error {
	var one int
	err := s.db(ctx).QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, project.Bytes()).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return appsinks.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locking a project: %w", err)
	}
	projectrow.MarkHeld(ctx, project)
	return nil
}

// EnvironmentKeys returns the keys of project's environments.
func (s *Store) EnvironmentKeys(ctx context.Context, project workspace.ProjectID) ([]string, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT key FROM environments WHERE project_id = $1`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading environment keys: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading environment keys: %w", err)
	}
	return keys, nil
}

// sinkColumns are a sink's with its creator's name as it is now.
const sinkColumns = `s.id, s.project_id, s.name, s.kind, s.config, s.secret_sealed, s.previous_secret_sealed,
	s.previous_secret_expires_at, s.event_kinds, s.environments, s.batch_size, s.flush_interval_seconds, s.enabled,
	s.stopped_at, s.at_risk_since, s.warnings, s.created_by_member_id, s.created_by_token_id,
	coalesce(m.display_name, t.name, ''), s.created_at, s.updated_at, s.version`

const sinkFrom = ` FROM sinks s
	LEFT JOIN members m ON m.id = s.created_by_member_id
	LEFT JOIN api_tokens t ON t.id = s.created_by_token_id`

// clickHouseDoc, objectStorageDoc and webhookDoc are the configurations as
// stored: only the kind's own, and no secret.
type clickHouseDoc struct {
	URL           string            `json:"url"`
	Database      string            `json:"database"`
	TablePrefix   string            `json:"table_prefix"`
	Username      string            `json:"username"`
	TLSSkipVerify bool              `json:"tls_skip_verify"`
	CreateTables  bool              `json:"create_tables"`
	Settings      map[string]string `json:"settings"`
}

type objectStorageDoc struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"access_key_id"`
	PathStyle   bool   `json:"path_style"`
	Format      string `json:"format"`
	Compression string `json:"compression"`
}

type webhookDoc struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func encodeConfig(kind sinks.Kind, c sinks.Config) ([]byte, error) {
	var doc any
	switch {
	case kind == sinks.KindClickHouse && c.ClickHouse != nil:
		v := *c.ClickHouse
		doc = clickHouseDoc{v.URL, v.Database, v.TablePrefix, v.Username, v.TLSSkipVerify, v.CreateTables, v.Settings}
	case kind == sinks.KindObjectStorage && c.ObjectStorage != nil:
		v := *c.ObjectStorage
		doc = objectStorageDoc{v.Endpoint, v.Region, v.Bucket, v.Prefix, v.AccessKeyID, v.PathStyle, v.Format, v.Compression}
	case kind == sinks.KindWebhook && c.Webhook != nil:
		doc = webhookDoc{c.Webhook.URL, c.Webhook.Headers}
	default:
		return nil, fmt.Errorf("a %s sink holds the configuration of another kind", kind)
	}
	return json.Marshal(doc)
}

func decodeConfig(kind sinks.Kind, raw []byte) (c sinks.Config, err error) {
	switch kind {
	case sinks.KindClickHouse:
		var v clickHouseDoc
		if err = json.Unmarshal(raw, &v); err == nil {
			c.ClickHouse = &sinks.ClickHouse{
				URL: v.URL, Database: v.Database, TablePrefix: v.TablePrefix, Username: v.Username,
				TLSSkipVerify: v.TLSSkipVerify, CreateTables: v.CreateTables, Settings: v.Settings,
			}
		}
	case sinks.KindObjectStorage:
		var v objectStorageDoc
		if err = json.Unmarshal(raw, &v); err == nil {
			c.ObjectStorage = &sinks.ObjectStorage{
				Endpoint: v.Endpoint, Region: v.Region, Bucket: v.Bucket, Prefix: v.Prefix, AccessKeyID: v.AccessKeyID,
				PathStyle: v.PathStyle, Format: v.Format, Compression: v.Compression,
			}
		}
	case sinks.KindWebhook:
		var v webhookDoc
		if err = json.Unmarshal(raw, &v); err == nil {
			c.Webhook = &sinks.Webhook{URL: v.URL, Headers: v.Headers}
		}
	default:
		err = fmt.Errorf("a sink of kind %q", kind)
	}
	return c, err
}

func scanSink(row pgx.Row) (sinks.Sink, error) {
	var (
		s               sinks.Sink
		sk, pk          [16]byte
		kind            string
		config          []byte
		previous        *string
		previousExpires *time.Time
		eventKinds      []string
		flushSeconds    int
		stopped, atRisk *time.Time
		member, token   *[16]byte
		creatorName     string
	)
	if err := row.Scan(&sk, &pk, &s.Name, &kind, &config, &s.SealedSecret, &previous, &previousExpires, &eventKinds,
		&s.Environments, &s.BatchSize, &flushSeconds, &s.Enabled, &stopped, &atRisk, &s.Warnings, &member, &token,
		&creatorName, &s.CreatedAt, &s.UpdatedAt, &s.Version); err != nil {
		return s, err
	}
	s.ID, s.Project = id.FromBytes[sinks.Sink](sk), id.FromBytes[workspace.Project](pk)
	s.Kind = sinks.Kind(kind)
	var err error
	if s.Config, err = decodeConfig(s.Kind, config); err != nil {
		return s, fmt.Errorf("reading the configuration of sink %s: %w", s.ID, err)
	}
	for _, k := range eventKinds {
		s.EventKinds = append(s.EventKinds, sinks.EventKind(k))
	}
	s.FlushInterval = time.Duration(flushSeconds) * time.Second
	if previous != nil {
		s.PreviousSealedSecret = *previous
	}
	s.PreviousSecretExpiresAt, s.StoppedAt, s.AtRiskSince = utc(previousExpires), utc(stopped), utc(atRisk)
	switch {
	case member != nil:
		m := id.FromBytes[access.Member](*member)
		s.CreatedBy = &governance.Author{Member: &m, Name: creatorName}
	case token != nil:
		t := id.FromBytes[access.APIToken](*token)
		s.CreatedBy = &governance.Author{Token: &t, Name: creatorName}
	}
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// Sinks returns project's sinks, the oldest first.
func (s *Store) Sinks(ctx context.Context, project workspace.ProjectID) ([]sinks.Sink, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+sinkColumns+sinkFrom+` WHERE s.project_id = $1 ORDER BY s.id`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing sinks: %w", err)
	}
	defer rows.Close()
	out := []sinks.Sink{}
	for rows.Next() {
		v, err := scanSink(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a sink: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Sink returns project's sink with sink, or appsinks.ErrNotFound.
func (s *Store) Sink(ctx context.Context, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error) {
	v, err := scanSink(s.db(ctx).QueryRow(ctx, `SELECT `+sinkColumns+sinkFrom+`
		WHERE s.project_id = $1 AND s.id = $2`, project.Bytes(), sink.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, appsinks.ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("reading a sink: %w", err)
	}
	return v, nil
}

func kindNames(kinds []sinks.EventKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

// Create stores v.
func (s *Store) Create(ctx context.Context, v sinks.Sink) error {
	config, err := encodeConfig(v.Kind, v.Config)
	if err != nil {
		return err
	}
	var member, token any
	if v.CreatedBy != nil && v.CreatedBy.Member != nil {
		member = v.CreatedBy.Member.Bytes()
	}
	if v.CreatedBy != nil && v.CreatedBy.Token != nil {
		token = v.CreatedBy.Token.Bytes()
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO sinks (id, project_id, name, kind, config, secret_sealed, event_kinds, environments, batch_size,
		    flush_interval_seconds, enabled, warnings, created_by_member_id, created_by_token_id, created_at,
		    updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		v.ID.Bytes(), v.Project.Bytes(), v.Name, string(v.Kind), config, v.SealedSecret, kindNames(v.EventKinds),
		nonNil(v.Environments), v.BatchSize, int(v.FlushInterval/time.Second), v.Enabled, nonNil(v.Warnings), member,
		token, v.CreatedAt, v.UpdatedAt, v.Version)
	if err != nil {
		return fmt.Errorf("creating a sink: %w", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Update stores what an edit changes of v, which its caller read under the
// project's lock. Its creator and its webhook secret overlap are left to the
// operations that change them.
func (s *Store) Update(ctx context.Context, v sinks.Sink) error {
	config, err := encodeConfig(v.Kind, v.Config)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		UPDATE sinks
		SET name = $3, config = $4, secret_sealed = $5, event_kinds = $6, environments = $7, batch_size = $8,
		    flush_interval_seconds = $9, enabled = $10, updated_at = $11, version = $12
		WHERE project_id = $1 AND id = $2`,
		v.Project.Bytes(), v.ID.Bytes(), v.Name, config, v.SealedSecret, kindNames(v.EventKinds), nonNil(v.Environments),
		v.BatchSize, int(v.FlushInterval/time.Second), v.Enabled, v.UpdatedAt, v.Version)
	if err != nil {
		return fmt.Errorf("updating a sink: %w", err)
	}
	return nil
}

// Delete deletes project's sink with sink.
func (s *Store) Delete(ctx context.Context, project workspace.ProjectID, sink sinks.ID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sinks WHERE project_id = $1 AND id = $2`, project.Bytes(), sink.Bytes()); err != nil {
		return fmt.Errorf("deleting a sink: %w", err)
	}
	return nil
}

// RotateSecret stores v's secrets, which its caller read under the project's
// lock.
func (s *Store) RotateSecret(ctx context.Context, v sinks.Sink) error {
	var previous any
	if v.PreviousSealedSecret != "" {
		previous = v.PreviousSealedSecret
	}
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sinks
		SET secret_sealed = $3, previous_secret_sealed = $4, previous_secret_expires_at = $5, updated_at = $6, version = $7
		WHERE project_id = $1 AND id = $2`,
		v.Project.Bytes(), v.ID.Bytes(), v.SealedSecret, previous, v.PreviousSecretExpiresAt, v.UpdatedAt, v.Version)
	if err != nil {
		return fmt.Errorf("rotating a sink's secret: %w", err)
	}
	return nil
}
