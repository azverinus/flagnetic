package access_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestAnInvitedMemberIsInvitedAndNoInstanceAdmin(t *testing.T) {
	m, err := access.NewInvitedMember(id.New[access.Member](newIDs()), " Grace@Example.com ", " Grace Hopper ", now)
	if err != nil {
		t.Fatal(err)
	}
	if m.Email != "Grace@Example.com" || m.DisplayName != "Grace Hopper" || m.Status != access.MemberInvited || m.InstanceAdmin ||
		!m.CreatedAt.Equal(now) {
		t.Errorf("invited = %+v", m)
	}
	for name, tc := range map[string]struct{ email, display, pointer string }{
		"malformed email": {"grace", "Grace", "/email"},
		"blank name":      {"grace@example.com", "  ", "/display_name"},
		"long name":       {"grace@example.com", strings.Repeat("n", 201), "/display_name"},
	} {
		_, err := access.NewInvitedMember(id.New[access.Member](newIDs()), tc.email, tc.display, now)
		if e, ok := apperrors.From(err); !ok || e.Code != "invalid_member" || e.Violations[0].Pointer != tc.pointer {
			t.Errorf("%s: err = %v, want invalid_member at %s", name, err, tc.pointer)
		}
	}
}

func TestAnInvitationSecretIsStoredAsItsHashAndLastsSevenDays(t *testing.T) {
	member := id.New[access.Member](newIDs())
	random := bytes.Repeat([]byte{7}, 32)
	inv, secret := access.NewInvitation(id.New[access.Invitation](newIDs()), member, now, bytes.NewReader(random))
	want := base64.RawURLEncoding.EncodeToString(random)
	if secret.Reveal() != want || len(want) != 43 {
		t.Fatalf("secret = %q, want the 32 random bytes in unpadded base64url", secret.Reveal())
	}
	if inv.Hash != sha256.Sum256(random) || inv.Member != member || !inv.CreatedAt.Equal(now) ||
		!inv.ExpiresAt.Equal(now.Add(7*24*time.Hour)) || inv.ConsumedAt != nil {
		t.Errorf("invitation = %+v", inv)
	}
	if h, ok := access.ParseInvitationSecret(secret.Reveal()); !ok || h != inv.Hash {
		t.Errorf("parsed = %x, %t; want the stored hash", h, ok)
	}
	for _, bad := range []string{"", "not base64url!", strings.Repeat("a", 42), want + "A", want[:42] + "="} {
		if _, ok := access.ParseInvitationSecret(bad); ok {
			t.Errorf("%q parsed as a secret", bad)
		}
	}
	other, otherSecret := access.NewInvitation(id.New[access.Invitation](newIDs()), member, now, rand.Reader)
	if otherSecret.Reveal() == secret.Reveal() || other.Hash == inv.Hash {
		t.Error("two invitations share a secret")
	}
}

func TestAnInvitationIsOpenUntilUsedOrExpired(t *testing.T) {
	inv, _ := access.NewInvitation(id.New[access.Invitation](newIDs()), id.New[access.Member](newIDs()), now, rand.Reader)
	invited := access.Member{Status: access.MemberInvited}
	if !inv.Open(now.Add(7*24*time.Hour-time.Minute), invited) {
		t.Error("closed a minute before it expires")
	}
	if inv.Open(now.Add(7*24*time.Hour), invited) {
		t.Error("open when it expires")
	}
	for _, s := range []access.MemberStatus{access.MemberActive, access.MemberDeactivated} {
		if inv.Open(now, access.Member{Status: s}) {
			t.Errorf("open for a member who is %s", s)
		}
	}
	used := now.Add(time.Minute)
	inv.ConsumedAt = &used
	if inv.Open(now.Add(time.Hour), invited) {
		t.Error("open once used")
	}
}

func TestAnInvitedEmailIsShownMasked(t *testing.T) {
	for email, want := range map[string]string{
		"grace@example.com": "g***@example.com",
		"G@example.com":     "G***@example.com",
		"élodie@example.fr": "é***@example.fr",
	} {
		if got := access.MaskEmail(email); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestAMemberJoiningIsActive(t *testing.T) {
	if got := (access.Member{Status: access.MemberInvited}).Joined(); got.Status != access.MemberActive {
		t.Errorf("joined = %s, want active", got.Status)
	}
}

func TestOnlyInvitationsLongPastUseArePurged(t *testing.T) {
	cut := access.InvitationCutoffsAt(now)
	if want := now.Add(-30 * 24 * time.Hour); !cut.EndedBy.Equal(want) {
		t.Errorf("cutoff = %v, want %v", cut.EndedBy, want)
	}
}
