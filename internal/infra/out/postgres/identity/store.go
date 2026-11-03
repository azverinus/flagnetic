// Package identity is the PostgreSQL repository of the identity context:
// auth providers.
package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of auth providers.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

var errSlugTaken = apperrors.Conflict("already_exists", "an auth provider with this slug already exists")

const providerColumns = `id, slug, kind, display_name, enabled, domains, link_by_email, auto_provision, oidc_issuer,
	oidc_client_id, oidc_client_secret_sealed, oidc_scopes, oidc_email_claim, oidc_name_claim, oidc_groups_claim,
	oidc_require_verified_email, saml_metadata_url, saml_metadata_xml, saml_idp_entity_id, saml_idp_sso_url,
	saml_idp_certificates, saml_metadata_refreshed_at, saml_sign_requests, saml_name_id_format, saml_email_attribute,
	saml_name_attribute, saml_groups_attribute, saml_sp_certificate, saml_sp_key_sealed, created_at, updated_at, version`

func scanProvider(row pgx.Row) (identity.Provider, error) {
	var (
		p                        identity.Provider
		pid                      [16]byte
		kind                     string
		issuer, clientID, sealed *string
		scopes                   []string
		emailClaim, nameClaim    *string
		groupsClaim              *string
		requireVerified          *bool
		samlURL, samlXML         *string
		samlEntity, samlSSO      *string
		samlCerts                []string
		samlRefreshed            *time.Time
		samlSign                 *bool
		samlFormat               *string
		samlEmail, samlName      *string
		samlGroups               *string
		samlSPCert, samlSPKey    *string
		createdAt, updatedAt     time.Time
	)
	if err := row.Scan(&pid, &p.Slug, &kind, &p.DisplayName, &p.Enabled, &p.Domains, &p.LinkByEmail, &p.AutoProvision,
		&issuer, &clientID, &sealed, &scopes, &emailClaim, &nameClaim, &groupsClaim, &requireVerified, &samlURL, &samlXML,
		&samlEntity, &samlSSO, &samlCerts, &samlRefreshed, &samlSign, &samlFormat, &samlEmail, &samlName, &samlGroups,
		&samlSPCert, &samlSPKey, &createdAt, &updatedAt, &p.Version); err != nil {
		return p, err
	}
	p.ID, p.Kind = id.FromBytes[identity.Provider](pid), identity.Kind(kind)
	p.CreatedAt, p.UpdatedAt = createdAt.UTC(), updatedAt.UTC()
	if p.Kind == identity.KindOIDC {
		p.OIDC = &identity.OIDC{
			Issuer: *issuer, ClientID: *clientID, SealedClientSecret: *sealed, Scopes: scopes,
			Claims:               identity.Claims{Email: *emailClaim, Name: *nameClaim, Groups: *groupsClaim},
			RequireVerifiedEmail: *requireVerified,
		}
	}
	if p.Kind == identity.KindSAML {
		p.SAML = &identity.SAML{
			MetadataURL: deref(samlURL), MetadataXML: deref(samlXML),
			IdP:         identity.IdPMetadata{EntityID: *samlEntity, SSOURL: *samlSSO, Certificates: samlCerts},
			RefreshedAt: samlRefreshed.UTC(), SignRequests: *samlSign, NameIDFormat: *samlFormat,
			Claims:        identity.Claims{Email: *samlEmail, Name: *samlName, Groups: *samlGroups},
			SPCertificate: *samlSPCert, SealedSPKey: *samlSPKey,
		}
	}
	return p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Store) list(ctx context.Context, where string) ([]identity.Provider, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+providerColumns+` FROM auth_providers `+where+` ORDER BY display_name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing auth providers: %w", err)
	}
	defer rows.Close()
	out := []identity.Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("reading an auth provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Providers returns every provider, ordered by display name.
func (s *Store) Providers(ctx context.Context) ([]identity.Provider, error) { return s.list(ctx, "") }

// EnabledProviders returns the enabled providers, ordered by display name.
func (s *Store) EnabledProviders(ctx context.Context) ([]identity.Provider, error) {
	return s.list(ctx, "WHERE enabled")
}

func (s *Store) one(ctx context.Context, suffix, slug string) (identity.Provider, error) {
	p, err := scanProvider(s.db(ctx).QueryRow(ctx, `SELECT `+providerColumns+` FROM auth_providers WHERE slug = $1`+suffix, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, appidentity.ErrNotFound
	}
	if err != nil {
		return p, fmt.Errorf("reading an auth provider: %w", err)
	}
	return p, nil
}

// Provider returns the provider with slug, or appidentity.ErrNotFound.
func (s *Store) Provider(ctx context.Context, slug string) (identity.Provider, error) {
	return s.one(ctx, "", slug)
}

// ProviderByID returns the provider with the identifier, or
// appidentity.ErrNotFound.
func (s *Store) ProviderByID(ctx context.Context, provider identity.ProviderID) (identity.Provider, error) {
	p, err := scanProvider(s.db(ctx).QueryRow(ctx, `SELECT `+providerColumns+` FROM auth_providers WHERE id = $1`, provider.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, appidentity.ErrNotFound
	}
	if err != nil {
		return p, fmt.Errorf("reading an auth provider: %w", err)
	}
	return p, nil
}

// LockProvider is Provider, locking the provider's row until the unit of work
// ends.
func (s *Store) LockProvider(ctx context.Context, slug string) (identity.Provider, error) {
	return s.one(ctx, " FOR UPDATE", slug)
}

// oidcArgs are a provider's OIDC columns as query arguments, nulls for a
// provider of another kind.
func oidcArgs(p identity.Provider) []any {
	o := p.OIDC
	if o == nil {
		return make([]any, 8)
	}
	return []any{o.Issuer, o.ClientID, o.SealedClientSecret, o.Scopes, o.Claims.Email, o.Claims.Name, o.Claims.Groups,
		o.RequireVerifiedEmail}
}

// samlArgs are a provider's SAML columns as query arguments, nulls for a
// provider of another kind.
func samlArgs(p identity.Provider) []any {
	c := p.SAML
	if c == nil {
		return make([]any, 13)
	}
	return []any{nonEmpty(c.MetadataURL), nonEmpty(c.MetadataXML), c.IdP.EntityID, c.IdP.SSOURL, c.IdP.Certificates, c.RefreshedAt,
		c.SignRequests, c.NameIDFormat, c.Claims.Email, c.Claims.Name, c.Claims.Groups, c.SPCertificate, c.SealedSPKey}
}

// CreateProvider stores p, answering a taken slug as a conflict.
func (s *Store) CreateProvider(ctx context.Context, p identity.Provider) error {
	args := append([]any{p.ID.Bytes(), p.Slug, string(p.Kind), p.DisplayName, p.Enabled, p.Domains, p.LinkByEmail,
		p.AutoProvision}, oidcArgs(p)...)
	args = append(args, samlArgs(p)...)
	args = append(args, p.CreatedAt, p.UpdatedAt, p.Version)
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO auth_providers (`+providerColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25,
		        $26, $27, $28, $29, $30, $31, $32)`, args...)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "auth_providers_slug_key" {
		return errSlugTaken.WithCause(err)
	}
	if err != nil {
		return fmt.Errorf("creating an auth provider: %w", err)
	}
	return nil
}

// UpdateProvider stores what an edit changes of p: everything but its slug,
// kind and enabled flag.
func (s *Store) UpdateProvider(ctx context.Context, p identity.Provider) error {
	args := append([]any{p.ID.Bytes(), p.DisplayName, p.Domains, p.LinkByEmail, p.AutoProvision}, oidcArgs(p)...)
	args = append(args, samlArgs(p)...)
	args = append(args, p.UpdatedAt, p.Version)
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE auth_providers
		SET display_name = $2, domains = $3, link_by_email = $4, auto_provision = $5, oidc_issuer = $6,
		    oidc_client_id = $7, oidc_client_secret_sealed = $8, oidc_scopes = $9, oidc_email_claim = $10,
		    oidc_name_claim = $11, oidc_groups_claim = $12, oidc_require_verified_email = $13,
		    saml_metadata_url = $14, saml_metadata_xml = $15, saml_idp_entity_id = $16, saml_idp_sso_url = $17,
		    saml_idp_certificates = $18, saml_metadata_refreshed_at = $19, saml_sign_requests = $20,
		    saml_name_id_format = $21, saml_email_attribute = $22, saml_name_attribute = $23,
		    saml_groups_attribute = $24, saml_sp_certificate = $25, saml_sp_key_sealed = $26, updated_at = $27,
		    version = $28
		WHERE id = $1`, args...)
	if err != nil {
		return fmt.Errorf("updating an auth provider: %w", err)
	}
	return nil
}

// SetProviderEnabled stores p's enabled flag, update time and version.
func (s *Store) SetProviderEnabled(ctx context.Context, p identity.Provider) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE auth_providers SET enabled = $2, updated_at = $3, version = $4 WHERE id = $1`,
		p.ID.Bytes(), p.Enabled, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("enabling or disabling an auth provider: %w", err)
	}
	return nil
}

// DeleteProvider deletes the provider with identifier.
func (s *Store) DeleteProvider(ctx context.Context, provider identity.ProviderID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM auth_providers WHERE id = $1`, provider.Bytes()); err != nil {
		return fmt.Errorf("deleting an auth provider: %w", err)
	}
	return nil
}
