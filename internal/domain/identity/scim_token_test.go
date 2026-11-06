package identity_test

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestANewSCIMTokenHasAPrefixedValueAndKeepsOnlyItsHashAndHint(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tok, v, err := identity.NewSCIMToken(identity.SCIMTokenID{}, identity.ProviderID{}, nil, now, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^fn_scim_[0-9A-Za-z]{40}$`).MatchString(v.Value.Reveal()) {
		t.Errorf("value %q is not fn_scim_ and 40 base62 characters", v.Value.Reveal())
	}
	if tok.Hint != v.Value.Reveal()[:12] || tok.Hash != v.Hash {
		t.Errorf("hint %q, hash matching = %v", tok.Hint, tok.Hash == v.Hash)
	}
	hash, err := identity.HashSCIMTokenValue(v.Value.Reveal())
	if err != nil || !hash.Equal(tok.Hash) {
		t.Errorf("hashing the value = %v, %v; want the token's hash", hash, err)
	}
}

func TestOnlyAWellFormedSCIMTokenValueIsHashed(t *testing.T) {
	for _, value := range []string{
		"", "fn_scim_", "fn_pat_" + string(bytes.Repeat([]byte("a"), 40)), "fn_scim_" + string(bytes.Repeat([]byte("a"), 39)),
		"fn_scim_" + string(bytes.Repeat([]byte("a"), 39)) + "-",
	} {
		if _, err := identity.HashSCIMTokenValue(value); !errors.Is(err, identity.ErrInvalidSCIMToken) {
			t.Errorf("HashSCIMTokenValue(%q) = %v, want ErrInvalidSCIMToken", value, err)
		}
	}
}

func TestASCIMTokenExpiresInTheFutureOnly(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, expiry := range []time.Time{now, now.Add(-time.Second)} {
		_, _, err := identity.NewSCIMToken(identity.SCIMTokenID{}, identity.ProviderID{}, &expiry, now, bytes.NewReader(make([]byte, 256)))
		if !apperrors.IsKind(err, apperrors.KindInvalid) {
			t.Errorf("expiry %s = %v, want an invalid refusal", expiry, err)
		}
	}
}

func TestASCIMTokenIsUsableUntilRevokedExpiredOrItsProviderDisabled(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	for name, c := range map[string]struct {
		tok     identity.SCIMToken
		enabled bool
		want    bool
	}{
		"fresh":              {identity.SCIMToken{}, true, true},
		"unexpired":          {identity.SCIMToken{ExpiresAt: &later}, true, true},
		"expired":            {identity.SCIMToken{ExpiresAt: &earlier}, true, false},
		"expiring right now": {identity.SCIMToken{ExpiresAt: &now}, true, false},
		"revoked":            {identity.SCIMToken{RevokedAt: &earlier}, true, false},
		"provider disabled":  {identity.SCIMToken{}, false, false},
	} {
		if got := c.tok.Usable(now, c.enabled); got != c.want {
			t.Errorf("%s: Usable = %v, want %v", name, got, c.want)
		}
	}
}

func TestASCIMTokensUseIsRecordedOnceAMinute(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-30*time.Second), now.Add(-time.Minute)
	for name, c := range map[string]struct {
		last *time.Time
		want bool
	}{"never used": {nil, true}, "used 30s ago": {&recent, false}, "used a minute ago": {&old, true}} {
		if got := (identity.SCIMToken{LastUsedAt: c.last}).UseToRecord(now); got != c.want {
			t.Errorf("%s: UseToRecord = %v, want %v", name, got, c.want)
		}
	}
}
