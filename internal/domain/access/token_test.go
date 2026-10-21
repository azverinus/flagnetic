package access_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func newIDs() *id.Source { return id.NewDeterministicSource(clock.NewFake(now)) }

func TestAPersonalTokenValueIsThePrefixAndFortyBase62Characters(t *testing.T) {
	v := access.NewTokenValue(access.TokenPersonal, rand.Reader)
	if !regexp.MustCompile(`^fn_pat_[0-9A-Za-z]{40}$`).MatchString(v.Value.Reveal()) {
		t.Fatalf("value %q is not fn_pat_ and 40 base62 characters", v.Value.Reveal())
	}
	if v.Hint != v.Value.Reveal()[:len("fn_pat_")+4] {
		t.Errorf("hint = %q, want the prefix and the first four characters", v.Hint)
	}
	if v.Hash != access.TokenHash(sha256.Sum256([]byte(v.Value.Reveal()))) {
		t.Error("hash is not the SHA-256 of the whole value")
	}
}

func TestAProjectTokenValueHasItsOwnPrefix(t *testing.T) {
	v := access.NewTokenValue(access.TokenProject, rand.Reader)
	if !strings.HasPrefix(v.Value.Reveal(), "fn_prj_") {
		t.Errorf("value %q does not start with fn_prj_", v.Value.Reveal())
	}
}

// Bytes from 248 up would favour the first characters of the alphabet if
// they were reduced modulo 62, so they are skipped.
func TestTokenCharactersAreDrawnWithoutBias(t *testing.T) {
	random := bytes.NewReader(append(bytes.Repeat([]byte{255, 248}, 40), bytes.Repeat([]byte{61}, 64)...))
	v := access.NewTokenValue(access.TokenPersonal, random)
	if got := v.Value.Reveal(); got != "fn_pat_"+strings.Repeat("z", 40) {
		t.Errorf("value = %q, want every byte from 248 up skipped", got)
	}
}

func TestEveryTokenValueIsDifferent(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		v := access.NewTokenValue(access.TokenPersonal, rand.Reader).Value.Reveal()
		if seen[v] {
			t.Fatalf("value %q drawn twice", v)
		}
		seen[v] = true
	}
}

func TestATokenValueIsParsedToItsHash(t *testing.T) {
	v := access.NewTokenValue(access.TokenProject, rand.Reader)
	h, err := access.ParseTokenValue(v.Value.Reveal())
	if err != nil || h != v.Hash {
		t.Errorf("ParseTokenValue = %x, %v; want the value's hash", h, err)
	}
}

func TestAMalformedTokenValueIsAnInvalidToken(t *testing.T) {
	valid := access.NewTokenValue(access.TokenPersonal, rand.Reader).Value.Reveal()
	for name, value := range map[string]string{
		"empty":          "",
		"truncated":      valid[:len(valid)-1],
		"too long":       valid + "a",
		"unknown prefix": "fn_xyz_" + valid[7:],
		"not base62":     valid[:len(valid)-1] + "-",
		"no prefix":      valid[7:],
	} {
		_, err := access.ParseTokenValue(value)
		if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindUnauthenticated || e.Code != "invalid_token" {
			t.Errorf("%s: ParseTokenValue = %v, want unauthenticated invalid_token", name, err)
		}
	}
}

func TestAGeneratedPasswordIsTwentyFourBase62Characters(t *testing.T) {
	p := access.GeneratePassword("first@example.com", rand.Reader).Reveal()
	if !regexp.MustCompile(`^[0-9A-Za-z]{24}$`).MatchString(p) {
		t.Errorf("password %q is not 24 base62 characters", p)
	}
}

func personalToken(t *testing.T, expiresAt *time.Time) access.APIToken {
	t.Helper()
	ids := newIDs()
	tok, _, err := access.NewPersonalToken(id.New[access.APIToken](ids), access.NewPersonalTokenInput{
		Member: id.New[access.Member](ids), Name: "deploy", Scopes: []access.Scope{access.ScopeRead},
		ExpiresAt: expiresAt, Now: now,
	}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func refusalCode(err error) string {
	if e, ok := apperrors.From(err); ok && e.Kind == apperrors.KindUnauthenticated {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "not unauthenticated: " + err.Error()
}

func TestATokenIsRefusedOnceRevokedOrExpiredAndRevokedIsJudgedFirst(t *testing.T) {
	expiry := now.Add(time.Hour)
	revokedAt := now.Add(-time.Minute)
	active := access.MemberActive
	for name, tc := range map[string]struct {
		expiresAt *time.Time
		revokedAt *time.Time
		status    access.MemberStatus
		at        time.Time
		want      string
	}{
		"live":                       {expiresAt: &expiry, status: active, at: now, want: ""},
		"without expiry":             {status: active, at: now.AddDate(10, 0, 0), want: ""},
		"expired at its expiry":      {expiresAt: &expiry, status: active, at: expiry, want: "token_expired"},
		"revoked":                    {revokedAt: &revokedAt, status: active, at: now, want: "token_revoked"},
		"revoked and expired":        {expiresAt: &expiry, revokedAt: &revokedAt, status: active, at: expiry.Add(time.Hour), want: "token_revoked"},
		"of a deactivated member":    {expiresAt: &expiry, status: access.MemberDeactivated, at: expiry.Add(time.Hour), want: "token_revoked"},
		"of a member not yet active": {status: access.MemberInvited, at: now, want: "token_revoked"},
	} {
		tok := personalToken(t, tc.expiresAt)
		tok.RevokedAt = tc.revokedAt
		holder := &access.Member{Status: tc.status}
		if got := refusalCode(tok.Refusal(tc.at, holder)); got != tc.want {
			t.Errorf("%s: refusal = %q, want %q", name, got, tc.want)
		}
	}
}

func TestATokenCarriesReadWithEveryWriteScope(t *testing.T) {
	ids := newIDs()
	tok, _, err := access.NewPersonalToken(id.New[access.APIToken](ids), access.NewPersonalTokenInput{
		Member: id.New[access.Member](ids), Name: "ci", Scopes: []access.Scope{access.ScopeFlagsWrite}, Now: now,
	}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !tok.HasScope(access.ScopeRead) || !tok.HasScope(access.ScopeFlagsWrite) || tok.HasScope(access.ScopeProjectAdmin) {
		t.Errorf("scopes = %v, want flags:write and read", tok.Scopes)
	}
}

func TestATokenNeedsANameAndKnownScopes(t *testing.T) {
	ids := newIDs()
	for name, in := range map[string]access.NewPersonalTokenInput{
		"blank name":    {Name: "  ", Scopes: []access.Scope{access.ScopeRead}},
		"long name":     {Name: strings.Repeat("n", 201), Scopes: []access.Scope{access.ScopeRead}},
		"unknown scope": {Name: "ci", Scopes: []access.Scope{"flags:read"}},
	} {
		in.Member, in.Now = id.New[access.Member](ids), now
		_, _, err := access.NewPersonalToken(id.New[access.APIToken](ids), in, rand.Reader)
		if !apperrors.IsKind(err, apperrors.KindInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}
