package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// LockSDKInventory holds, until the unit of work ends, the right to add rows
// of key to the inventory, so that replicas writing at once never take a key
// past its bound. The lock is advisory, keyed by the key, and touches no row
// a member's write needs.
func (s *Store) LockSDKInventory(ctx context.Context, key delivery.SDKKeyID) error {
	_, err := s.db(ctx).Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('sdk_inventory:' || $1::uuid::text, 0))`, key.Bytes())
	if err != nil {
		return fmt.Errorf("lock the SDK inventory: %w", err)
	}
	return nil
}

// InventoryOfKey returns the identities of the rows key holds.
func (s *Store) InventoryOfKey(ctx context.Context, key delivery.SDKKeyID) ([]delivery.InventoryIdentity, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT environment_id, sdk_name, sdk_version, platform, app_version
		FROM sdk_inventory WHERE sdk_key_id = $1`, key.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading the SDK inventory of a key: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.InventoryIdentity, error) {
		var (
			r   delivery.InventoryIdentity
			env [16]byte
		)
		scanErr := row.Scan(&env, &r.SDK.Name, &r.SDK.Version, &r.SDK.Platform, &r.AppVersion)
		r.Environment, r.Key = id.FromBytes[workspace.Environment](env), key
		return r, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading the SDK inventory of a key: %w", err)
	}
	return out, nil
}

// RecordInventory records a sighting of an SDK. The row's first time stays,
// its last time and ruleset version move on, unless it was seen less than
// delivery.InventoryInterval before with the same ruleset version: replicas
// seeing the same SDK then write it once a minute between them.
func (s *Store) RecordInventory(ctx context.Context, x delivery.InventorySighting) error {
	var version any
	if x.RulesetVersion != 0 {
		version = x.RulesetVersion
	}
	_, err := s.db(ctx).Exec(ctx, `INSERT INTO sdk_inventory AS i (environment_id, sdk_key_id, sdk_name, sdk_version, platform,
			app_version, first_seen_at, last_seen_at, last_ruleset_version)
		VALUES ($1, $2, $3, $4, $5, $6, $10, $7, $8)
		ON CONFLICT (environment_id, sdk_key_id, sdk_name, sdk_version, platform, app_version) DO UPDATE SET
			last_seen_at = greatest(i.last_seen_at, EXCLUDED.last_seen_at),
			first_seen_at = least(i.first_seen_at, EXCLUDED.first_seen_at),
			last_ruleset_version = coalesce(EXCLUDED.last_ruleset_version, i.last_ruleset_version)
		WHERE i.last_seen_at <= EXCLUDED.last_seen_at - $9::interval
			OR i.last_ruleset_version IS DISTINCT FROM coalesce(EXCLUDED.last_ruleset_version, i.last_ruleset_version)
			OR i.first_seen_at > EXCLUDED.first_seen_at`,
		x.Environment.Bytes(), x.Key.Bytes(), x.SDK.Name, x.SDK.Version, x.SDK.Platform, x.AppVersion, x.At, version,
		delivery.InventoryInterval, x.First)
	if err != nil {
		return fmt.Errorf("recording the SDK inventory: %w", err)
	}
	return nil
}

// Inventory returns the rows of environment with their keys, most recently
// seen first, then by SDK, version, platform, app version and key.
func (s *Store) Inventory(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.InventoryRow, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT i.sdk_key_id, i.sdk_name, i.sdk_version, i.platform, i.app_version,
			i.first_seen_at, i.last_seen_at, coalesce(i.last_ruleset_version, 0), k.name, k.key_id, k.kind
		FROM sdk_inventory i JOIN sdk_keys k ON k.id = i.sdk_key_id
		WHERE i.environment_id = $1
		ORDER BY i.last_seen_at DESC, i.sdk_name, i.sdk_version, i.platform, i.app_version, i.sdk_key_id`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading the SDK inventory: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.InventoryRow, error) {
		var (
			r    delivery.InventoryRow
			key  [16]byte
			kind string
		)
		scanErr := row.Scan(&key, &r.SDK.Name, &r.SDK.Version, &r.SDK.Platform, &r.AppVersion, &r.FirstSeen, &r.LastSeen,
			&r.LastRulesetVersion, &r.KeyName, &r.KeyID, &kind)
		r.Environment, r.Key, r.KeyKind = environment, id.FromBytes[delivery.SDKKey](key), delivery.SDKKeyKind(kind)
		return r, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading the SDK inventory: %w", err)
	}
	return out, nil
}

// inventoryPurgeBatch bounds the rows one statement of the purge deletes,
// so a purge after a long pause never holds a long lock.
const inventoryPurgeBatch = 10000

// PurgeInventory deletes the rows last seen before before, in batches, and
// returns how many.
func (s *Store) PurgeInventory(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.db(ctx).Exec(ctx, `DELETE FROM sdk_inventory WHERE ctid IN (
			SELECT ctid FROM sdk_inventory WHERE last_seen_at < $1 LIMIT $2)`, before, inventoryPurgeBatch)
		if err != nil {
			return total, fmt.Errorf("purging the SDK inventory: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < inventoryPurgeBatch {
			return total, nil
		}
	}
}
