package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SDKKeysChannel is the notification channel on which a change to whether
// an SDK key is accepted (its expiry, revocation or allowed origins) and
// its deletion are announced with the key's identifier, by a trigger on the
// keys' table, so a deletion cascading from an environment or a project is
// announced too.
const SDKKeysChannel = "sdk_keys"

// sdkKeyColumns are the columns scanSDKKey reads, prefixed by the sdk_keys
// table's alias k and the environments table's alias e. They name the hash
// of a key's value, never a value.
//
//nolint:gosec // column names, not a credential
const sdkKeyColumns = `k.id, e.project_id, k.environment_id, k.kind, k.name, k.key_id, k.key_hash, k.allowed_origins,
	k.created_at, k.last_used_at, k.expires_at, k.revoked_at`

func scanSDKKey(row pgx.Row) (delivery.SDKKey, error) {
	var (
		k                         delivery.SDKKey
		key, project, environment [16]byte
		kind                      string
		hash                      []byte
	)
	if err := row.Scan(&key, &project, &environment, &kind, &k.Name, &k.KeyID, &hash, &k.AllowedOrigins,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
		return delivery.SDKKey{}, err
	}
	k.ID, k.Project = id.FromBytes[delivery.SDKKey](key), id.FromBytes[workspace.Project](project)
	k.Environment, k.Kind = id.FromBytes[workspace.Environment](environment), delivery.SDKKeyKind(kind)
	k.Hash = delivery.SDKKeyHash(hash)
	if k.AllowedOrigins == nil {
		k.AllowedOrigins = []string{}
	}
	return k, nil
}

// SDKKeyEnvironment returns the identifier of the environment of project
// keyed key, its row locked against other key changes when lock is set, or
// appdelivery.ErrNotFound. The lock is FOR NO KEY UPDATE, which a ruleset
// bump of the environment also takes, so it is held only for the short
// unit of work of a key change.
func (s *Store) SDKKeyEnvironment(ctx context.Context, project workspace.ProjectID, key string, lock bool,
) (workspace.EnvironmentID, error) {
	q := `SELECT id FROM environments WHERE project_id = $1 AND key = $2`
	if lock {
		q += ` FOR NO KEY UPDATE`
	}
	var environment [16]byte
	err := s.db(ctx).QueryRow(ctx, q, project.Bytes(), key).Scan(&environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.EnvironmentID{}, appdelivery.ErrNotFound
	}
	if err != nil {
		return workspace.EnvironmentID{}, fmt.Errorf("reading an SDK key's environment: %w", err)
	}
	return id.FromBytes[workspace.Environment](environment), nil
}

// SDKKeys returns every key of environment, the oldest first.
func (s *Store) SDKKeys(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.SDKKey, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+sdkKeyColumns+` FROM sdk_keys k JOIN environments e ON e.id = k.environment_id
		WHERE k.environment_id = $1 ORDER BY k.created_at, k.id`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing SDK keys: %w", err)
	}
	keys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.SDKKey, error) { return scanSDKKey(row) })
	if err != nil {
		return nil, fmt.Errorf("listing SDK keys: %w", err)
	}
	return keys, nil
}

// SDKKey returns environment's key identified by key, its row locked when
// lock is set, or appdelivery.ErrNotFound.
func (s *Store) SDKKey(ctx context.Context, environment workspace.EnvironmentID, key delivery.SDKKeyID, lock bool,
) (delivery.SDKKey, error) {
	q := `SELECT ` + sdkKeyColumns + ` FROM sdk_keys k JOIN environments e ON e.id = k.environment_id
		WHERE k.environment_id = $1 AND k.id = $2`
	if lock {
		q += ` FOR UPDATE OF k`
	}
	k, err := scanSDKKey(s.db(ctx).QueryRow(ctx, q, environment.Bytes(), key.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.SDKKey{}, appdelivery.ErrNotFound
	}
	if err != nil {
		return delivery.SDKKey{}, fmt.Errorf("reading an SDK key: %w", err)
	}
	return k, nil
}

// ActiveSDKKeys counts environment's keys of kind neither rotated nor
// revoked.
func (s *Store) ActiveSDKKeys(ctx context.Context, environment workspace.EnvironmentID, kind delivery.SDKKeyKind) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx, `
		SELECT count(*) FROM sdk_keys
		WHERE environment_id = $1 AND kind = $2 AND revoked_at IS NULL AND expires_at IS NULL`,
		environment.Bytes(), string(kind)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting active SDK keys: %w", err)
	}
	return n, nil
}

// CreateSDKKey stores a new key.
func (s *Store) CreateSDKKey(ctx context.Context, k delivery.SDKKey) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO sdk_keys (id, environment_id, kind, name, key_id, key_hash, allowed_origins, created_at,
			last_used_at, expires_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		k.ID.Bytes(), k.Environment.Bytes(), string(k.Kind), k.Name, k.KeyID, k.Hash[:], origins(k.AllowedOrigins), k.CreatedAt,
		k.LastUsedAt, k.ExpiresAt, k.RevokedAt)
	if err != nil {
		return fmt.Errorf("creating an SDK key: %w", err)
	}
	return nil
}

// UpdateSDKKey stores a key's name, allowed origins, expiry and revocation.
func (s *Store) UpdateSDKKey(ctx context.Context, k delivery.SDKKey) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sdk_keys SET name = $2, allowed_origins = $3, expires_at = $4, revoked_at = $5 WHERE id = $1`,
		k.ID.Bytes(), k.Name, origins(k.AllowedOrigins), k.ExpiresAt, k.RevokedAt)
	if err != nil {
		return fmt.Errorf("updating an SDK key: %w", err)
	}
	return nil
}

// origins is a key's allowed origins as the column takes them: never null.
func origins(o []string) []string {
	if o == nil {
		return []string{}
	}
	return o
}

// SDKKeyByHash returns the key whose value hashes to hash, or
// appdelivery.ErrNotFound.
func (s *Store) SDKKeyByHash(ctx context.Context, hash delivery.SDKKeyHash) (delivery.SDKKey, error) {
	k, err := scanSDKKey(s.db(ctx).QueryRow(ctx, `
		SELECT `+sdkKeyColumns+` FROM sdk_keys k JOIN environments e ON e.id = k.environment_id
		WHERE k.key_hash = $1`, hash[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.SDKKey{}, appdelivery.ErrNotFound
	}
	if err != nil {
		return delivery.SDKKey{}, fmt.Errorf("looking up an SDK key: %w", err)
	}
	return k, nil
}

// RecordSDKKeyUse records that key was used at at, unless a use less than
// delivery.SDKKeyUseInterval before at is recorded already.
func (s *Store) RecordSDKKeyUse(ctx context.Context, key delivery.SDKKeyID, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sdk_keys SET last_used_at = $2
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at <= $2::timestamptz - make_interval(secs => $3))`,
		key.Bytes(), at, delivery.SDKKeyUseInterval.Seconds())
	if err != nil {
		return fmt.Errorf("recording an SDK key's use: %w", err)
	}
	return nil
}
