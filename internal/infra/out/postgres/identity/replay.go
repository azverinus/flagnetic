package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

// RecordAssertion remembers that the assertion with assertionID was accepted
// from provider, until expires, and reports whether it was new: false when it
// was accepted before and has not expired since. Assertions that expired
// before now are forgotten, so the table holds no more than the assertions
// still acceptable.
func (s *Store) RecordAssertion(ctx context.Context, provider identity.ProviderID, assertionID string, expires, now time.Time) (bool, error) {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM saml_replay WHERE expires_at <= $1`, now); err != nil {
		return false, fmt.Errorf("forgetting expired SAML assertions: %w", err)
	}
	tag, err := s.db(ctx).Exec(ctx, `
		INSERT INTO saml_replay (provider_id, assertion_id, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (provider_id, assertion_id) DO NOTHING`, provider.Bytes(), assertionID, expires)
	if err != nil {
		return false, fmt.Errorf("remembering a SAML assertion: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
