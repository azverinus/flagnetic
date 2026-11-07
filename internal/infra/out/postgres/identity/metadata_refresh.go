package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

// refreshLock is the session advisory lock a replica holds for a round of the
// metadata refresh. The schema is part of its key, so instances sharing one
// database in separate schemas, as tests do, never wait on each other.
const refreshLock = `hashtextextended('saml_metadata_refresh@' || current_schema(), 0)`

// unlockBound bounds letting the refresh's lock go once a round ends, whatever
// became of the round's own context.
const unlockBound = 5 * time.Second

// TryLockMetadataRefresh takes the refresh's lock on a connection of its own,
// held across the round's fetches and units of work without keeping a
// transaction open, which would hold a connection and the providers' rows
// while an identity provider answers slowly.
func (s *Store) TryLockMetadataRefresh(ctx context.Context) (release func(), held bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquiring a connection for the metadata refresh's lock: %w", err)
	}
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+refreshLock+`)`).Scan(&held); err != nil || !held {
		conn.Release()
		if err != nil {
			err = fmt.Errorf("taking the metadata refresh's lock: %w", err)
		}
		return nil, false, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), unlockBound)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+refreshLock+`)`); err != nil {
			// A connection whose lock may still be held must not go back to the
			// pool: closing it lets the lock go.
			_ = conn.Hijack().Close(ctx)
			return
		}
		conn.Release()
	}, true, nil
}

// MetadataRefreshCandidates returns the enabled SAML providers whose metadata
// comes from a URL and was read at or before cutoff, by display name.
func (s *Store) MetadataRefreshCandidates(ctx context.Context, cutoff time.Time) ([]identity.Provider, error) {
	return s.list(ctx, `WHERE enabled AND kind = 'saml' AND saml_metadata_url IS NOT NULL AND saml_metadata_refreshed_at <= $1`, cutoff)
}

// UpdateIDPMetadata stores what a refresh read of p's identity provider: its
// certificates, single sign-on address, the time it was read, and the update
// time and version.
func (s *Store) UpdateIDPMetadata(ctx context.Context, p identity.Provider) error {
	c := p.SAML
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE auth_providers
		SET saml_idp_entity_id = $2, saml_idp_sso_url = $3, saml_idp_certificates = $4, saml_metadata_refreshed_at = $5,
		    updated_at = $6, version = $7
		WHERE id = $1`, p.ID.Bytes(), c.IDP.EntityID, c.IDP.SSOURL, c.IDP.Certificates, c.RefreshedAt, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("storing an identity provider's refreshed metadata: %w", err)
	}
	return nil
}
