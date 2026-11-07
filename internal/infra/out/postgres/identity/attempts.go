package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CreateAttempt stores a, and deletes the attempts that expired before now.
func (s *Store) CreateAttempt(ctx context.Context, a identity.Attempt, now time.Time) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sso_attempts WHERE expires_at <= $1`, now); err != nil {
		return fmt.Errorf("deleting expired sign-in attempts: %w", err)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO sso_attempts (id, provider_id, intent, state_hash, nonce, code_verifier, saml_request_id, cookie_hash, return_to,
		                          created_at, expires_at, member_id, session_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		a.ID.Bytes(), a.Provider.Bytes(), string(a.Intent), a.StateHash[:], a.Nonce, a.CodeVerifier, nonEmpty(a.RequestID),
		a.CookieHash[:], a.ReturnTo, a.CreatedAt, a.ExpiresAt, boundTo(a.Member.IsZero(), a.Member.Bytes()), boundTo(a.Session.IsZero(), a.Session.Bytes()))
	if err != nil {
		return fmt.Errorf("creating a sign-in attempt: %w", err)
	}
	return nil
}

const attemptColumns = `id, provider_id, intent, state_hash, nonce, code_verifier, saml_request_id, cookie_hash, return_to, created_at,
	expires_at, callback_at, completion_hash, subject, email, email_verified, display_name, groups, member_id, session_id`

func scanAttempt(row pgx.Row) (identity.Attempt, error) {
	var (
		a                     identity.Attempt
		attemptID, providerID [16]byte
		intent                string
		state, cookie         []byte
		completion            []byte
		subject, email, name  *string
		requestID             *string
		verified              *bool
		groups                []string
		member, session       *[16]byte
	)
	err := row.Scan(&attemptID, &providerID, &intent, &state, &a.Nonce, &a.CodeVerifier, &requestID, &cookie, &a.ReturnTo, &a.CreatedAt,
		&a.ExpiresAt, &a.CallbackAt, &completion, &subject, &email, &verified, &name, &groups, &member, &session)
	if err != nil {
		return a, err
	}
	if member != nil {
		a.Member = id.FromBytes[identity.Member](*member)
	}
	if session != nil {
		a.Session = id.FromBytes[identity.Session](*session)
	}
	a.ID, a.Provider, a.Intent = id.FromBytes[identity.Attempt](attemptID), id.FromBytes[identity.Provider](providerID), identity.Intent(intent)
	a.StateHash, a.CookieHash, a.RequestID = identity.SecretHash(state), identity.SecretHash(cookie), deref(requestID)
	a.CreatedAt, a.ExpiresAt = a.CreatedAt.UTC(), a.ExpiresAt.UTC()
	if completion != nil {
		h := identity.SecretHash(completion)
		a.CompletionHash = &h
		a.Claims = &identity.VerifiedClaims{
			Subject: *subject, Email: *email, EmailVerified: *verified, Name: *name, Groups: groups, HasGroups: groups != nil,
		}
	}
	return a, nil
}

// TakeCallback marks the live attempt with state's hash as answered at now
// and returns it; ErrNotFound when there is none, it was answered already or
// it expired.
func (s *Store) TakeCallback(ctx context.Context, state identity.SecretHash, now time.Time) (identity.Attempt, error) {
	a, err := scanAttempt(s.db(ctx).QueryRow(ctx, `
		UPDATE sso_attempts SET callback_at = $2
		WHERE state_hash = $1 AND callback_at IS NULL AND expires_at > $2
		RETURNING `+attemptColumns, state[:], now))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, appidentity.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("taking a sign-in attempt's callback: %w", err)
	}
	return a, nil
}

// StoreClaims stores the verified claims on the attempt and the hash of the
// token its browser completes with.
func (s *Store) StoreClaims(ctx context.Context, attempt identity.AttemptID, completion identity.SecretHash, c identity.VerifiedClaims) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sso_attempts SET completion_hash = $2, subject = $3, email = $4, email_verified = $5, display_name = $6, groups = $7
		WHERE id = $1`, attempt.Bytes(), completion[:], c.Subject, c.Email, c.EmailVerified, c.Name, groupsOf(c))
	if err != nil {
		return fmt.Errorf("storing a sign-in attempt's claims: %w", err)
	}
	return nil
}

// DeleteAttempt deletes the attempt.
func (s *Store) DeleteAttempt(ctx context.Context, attempt identity.AttemptID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sso_attempts WHERE id = $1`, attempt.Bytes()); err != nil {
		return fmt.Errorf("deleting a sign-in attempt: %w", err)
	}
	return nil
}

// TakeCompletion deletes the attempt completing with completion's hash and
// returns it; ErrNotFound when there is none.
func (s *Store) TakeCompletion(ctx context.Context, completion identity.SecretHash) (identity.Attempt, error) {
	a, err := scanAttempt(s.db(ctx).QueryRow(ctx, `
		DELETE FROM sso_attempts WHERE completion_hash = $1 RETURNING `+attemptColumns, completion[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, appidentity.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("taking a sign-in attempt to complete: %w", err)
	}
	return a, nil
}

// groupsOf is the groups column of the claims: NULL unless the provider
// claimed a list of strings, which may be empty.
func groupsOf(c identity.VerifiedClaims) []string {
	if !c.HasGroups {
		return nil
	}
	if c.Groups == nil {
		return []string{}
	}
	return c.Groups
}

// boundTo returns the identifier an attempt is bound to, nil when it is bound
// to none.
func boundTo(zero bool, b [16]byte) any {
	if zero {
		return nil
	}
	return b
}
