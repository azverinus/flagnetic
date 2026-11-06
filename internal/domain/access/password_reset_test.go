package access_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestAPasswordResetSecretIsStoredAsItsHashAndLastsAnHour(t *testing.T) {
	member, admin := id.New[access.Member](newIDs()), id.New[access.Member](newIDs())
	random := bytes.Repeat([]byte{9}, 32)
	reset, secret := access.NewPasswordReset(id.New[access.PasswordReset](newIDs()), member, admin, now, bytes.NewReader(random))
	want := base64.RawURLEncoding.EncodeToString(random)
	if secret.Reveal() != want || len(want) != 43 {
		t.Fatalf("secret = %q, want the 32 random bytes in unpadded base64url", secret.Reveal())
	}
	if reset.Hash != sha256.Sum256(random) || reset.Member != member || reset.IssuedBy != admin || !reset.CreatedAt.Equal(now) ||
		!reset.ExpiresAt.Equal(now.Add(time.Hour)) || reset.ConsumedAt != nil {
		t.Errorf("password reset = %+v", reset)
	}
	if h, ok := access.ParsePasswordResetSecret(secret.Reveal()); !ok || h != reset.Hash {
		t.Errorf("parsed = %x, %t; want the stored hash", h, ok)
	}
	for _, bad := range []string{"", "not base64url!", strings.Repeat("a", 42), want + "A", want[:42] + "="} {
		if _, ok := access.ParsePasswordResetSecret(bad); ok {
			t.Errorf("%q parsed as a secret", bad)
		}
	}
	other, otherSecret := access.NewPasswordReset(id.New[access.PasswordReset](newIDs()), member, access.MemberID{}, now, rand.Reader)
	if otherSecret.Reveal() == secret.Reveal() || other.Hash == reset.Hash || !other.IssuedBy.IsZero() {
		t.Error("two password resets share a secret, or the member's own names an issuer")
	}
}

func TestAPasswordResetIsOpenUntilUsedOrExpired(t *testing.T) {
	reset, _ := access.NewPasswordReset(id.New[access.PasswordReset](newIDs()), id.New[access.Member](newIDs()), access.MemberID{}, now, rand.Reader)
	active := access.Member{Status: access.MemberActive}
	if !reset.Open(now.Add(time.Hour-time.Minute), active) {
		t.Error("closed a minute before it expires")
	}
	if reset.Open(now.Add(time.Hour), active) {
		t.Error("open when it expires")
	}
	for _, s := range []access.MemberStatus{access.MemberInvited, access.MemberDeactivated} {
		if reset.Open(now, access.Member{Status: s}) {
			t.Errorf("open for a member who is %s", s)
		}
	}
	used := now.Add(time.Minute)
	reset.ConsumedAt = &used
	if reset.Open(now.Add(time.Minute), active) {
		t.Error("open once used")
	}
}

func TestAPasswordResetIsIssuedOnlyForAnActiveMember(t *testing.T) {
	if err := access.CheckPasswordResetIssuable(access.Member{Status: access.MemberActive}); err != nil {
		t.Errorf("active: %v", err)
	}
	if err := access.CheckPasswordResetIssuable(access.Member{Status: access.MemberInvited}); !errors.Is(err, access.ErrMemberNotActive) {
		t.Errorf("invited: %v, want member_not_active", err)
	}
	if err := access.CheckPasswordResetIssuable(access.Member{Status: access.MemberDeactivated}); !errors.Is(err, access.ErrMemberDeactivated) {
		t.Errorf("deactivated: %v, want member_deactivated", err)
	}
}

func TestOnlyPasswordResetsLongPastUseArePurged(t *testing.T) {
	cut := access.PasswordResetCutoffsAt(now)
	if want := now.Add(-30 * 24 * time.Hour); !cut.EndedBy.Equal(want) {
		t.Errorf("cutoff = %v, want %v", cut.EndedBy, want)
	}
}

func TestASelfServicePasswordResetIsDueOncePerInterval(t *testing.T) {
	if access.PasswordResetInterval != 5*time.Minute {
		t.Fatalf("interval = %v, want 5 minutes", access.PasswordResetInterval)
	}
	last := now
	for name, tc := range map[string]struct {
		at   time.Time
		want bool
	}{
		"right after":                 {last, false},
		"a second before the end":     {last.Add(access.PasswordResetInterval - time.Second), false},
		"when the interval has ended": {last.Add(access.PasswordResetInterval), true},
		"long after":                  {last.Add(time.Hour), true},
	} {
		if got := access.SelfServicePasswordResetDue(&last, tc.at); got != tc.want {
			t.Errorf("%s: due = %t, want %t", name, got, tc.want)
		}
	}
	if !access.SelfServicePasswordResetDue(nil, now) {
		t.Error("not due for a member who never asked")
	}
}

func TestOnlyAnActiveMemberHoldingAPasswordIsMailedAReset(t *testing.T) {
	for name, tc := range map[string]struct {
		status   access.MemberStatus
		password bool
		want     bool
	}{
		"active with a password":    {access.MemberActive, true, true},
		"active without a password": {access.MemberActive, false, false},
		"invited":                   {access.MemberInvited, false, false},
		"deactivated":               {access.MemberDeactivated, true, false},
	} {
		if got := access.CanAskForPasswordReset(access.Member{Status: tc.status}, tc.password); got != tc.want {
			t.Errorf("%s: %t, want %t", name, got, tc.want)
		}
	}
}
