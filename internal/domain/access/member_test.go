package access_test

import (
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestAnInstanceAdminIsCreatedActive(t *testing.T) {
	m, err := access.NewInstanceAdmin(id.New[access.Member](newIDs()), " Ada@Example.com ", "Ada Lovelace", now)
	if err != nil {
		t.Fatal(err)
	}
	if m.Email != "Ada@Example.com" || m.DisplayName != "Ada Lovelace" || m.Status != access.MemberActive ||
		!m.InstanceAdmin || !m.CreatedAt.Equal(now) {
		t.Errorf("member = %+v", m)
	}
}

func TestAMemberWithoutANameIsNamedAfterTheirEmail(t *testing.T) {
	m, err := access.NewInstanceAdmin(id.New[access.Member](newIDs()), "ada@example.com", " ", now)
	if err != nil || m.DisplayName != "ada" {
		t.Errorf("display name = %q, %v; want the email's local part", m.DisplayName, err)
	}
}

func TestAMemberNeedsAnEmailAndANameWithinBounds(t *testing.T) {
	for name, tc := range map[string]struct{ email, display, pointer string }{
		"no email":       {"", "Ada", "/email"},
		"no at sign":     {"ada.example.com", "Ada", "/email"},
		"no local part":  {"@example.com", "Ada", "/email"},
		"no domain":      {"ada@", "Ada", "/email"},
		"two at signs":   {"ada@b@example.com", "Ada", "/email"},
		"a space inside": {"ada lovelace@example.com", "Ada", "/email"},
		"too long":       {strings.Repeat("a", 250) + "@example.com", "Ada", "/email"},
		"long name":      {"ada@example.com", strings.Repeat("n", 201), "/display_name"},
	} {
		_, err := access.NewInstanceAdmin(id.New[access.Member](newIDs()), tc.email, tc.display, now)
		e, ok := apperrors.From(err)
		if !ok || e.Kind != apperrors.KindInvalid || len(e.Violations) != 1 || e.Violations[0].Pointer != tc.pointer {
			t.Errorf("%s: err = %v, want invalid at %s", name, err, tc.pointer)
		}
	}
}
