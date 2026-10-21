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

func TestAMemberIsEditedWithinTheBoundsOfANewOne(t *testing.T) {
	m := access.Member{Email: "ada@example.com", DisplayName: "Ada", Status: access.MemberActive}
	got, err := m.Edited(access.MemberChange{Email: new(" Ada.L@Example.com "), DisplayName: new(" Ada Lovelace "), InstanceAdmin: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "Ada.L@Example.com" || got.DisplayName != "Ada Lovelace" || !got.InstanceAdmin || got.Status != access.MemberActive {
		t.Errorf("edited = %+v", got)
	}
	if same, err := m.Edited(access.MemberChange{}); err != nil || same != m {
		t.Errorf("an empty change = %+v, %v; want the member unchanged", same, err)
	}
	for name, tc := range map[string]struct {
		change  access.MemberChange
		pointer string
		code    string
	}{
		"malformed email": {access.MemberChange{Email: new("ada")}, "/email", "invalid_email"},
		"blank name":      {access.MemberChange{DisplayName: new("  ")}, "/display_name", "invalid_name"},
		"long name":       {access.MemberChange{DisplayName: new(strings.Repeat("n", 201))}, "/display_name", "too_long"},
	} {
		_, err := m.Edited(tc.change)
		e, ok := apperrors.From(err)
		if !ok || e.Code != "invalid_member" || len(e.Violations) != 1 || e.Violations[0].Pointer != tc.pointer ||
			e.Violations[0].Code != tc.code {
			t.Errorf("%s: err = %v, want invalid_member with %s at %s", name, err, tc.code, tc.pointer)
		}
	}
}

func TestTheLastActiveInstanceAdminIsNeverLost(t *testing.T) {
	admin := access.Member{Status: access.MemberActive, InstanceAdmin: true}
	demoted, deactivated := admin, admin.Deactivated()
	demoted.InstanceAdmin = false
	for name, after := range map[string]access.Member{"demoted": demoted, "deactivated": deactivated} {
		err := access.KeepAnInstanceAdmin(admin, after, 0)
		if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindConflict || e.Code != "last_instance_admin" {
			t.Errorf("the last one %s: err = %v, want conflict last_instance_admin", name, err)
		}
		if err := access.KeepAnInstanceAdmin(admin, after, 1); err != nil {
			t.Errorf("one of two %s: err = %v", name, err)
		}
	}
	renamed := admin
	renamed.DisplayName = "Renamed"
	if err := access.KeepAnInstanceAdmin(admin, renamed, 0); err != nil {
		t.Errorf("the last one renamed: err = %v", err)
	}
	invitedAdmin := access.Member{Status: access.MemberInvited, InstanceAdmin: true}
	if err := access.KeepAnInstanceAdmin(invitedAdmin, invitedAdmin.Deactivated(), 0); err != nil {
		t.Errorf("an invited instance admin deactivated: err = %v; only active ones keep the instance", err)
	}
}

func TestAReactivatedMemberIsActiveOnlyWithAPassword(t *testing.T) {
	m := access.Member{Status: access.MemberActive}.Deactivated()
	if m.Status != access.MemberDeactivated {
		t.Fatalf("deactivated status = %s", m.Status)
	}
	if got := m.Reactivated(true); got.Status != access.MemberActive {
		t.Errorf("with a password: %s, want active", got.Status)
	}
	if got := m.Reactivated(false); got.Status != access.MemberInvited {
		t.Errorf("without a password: %s, want invited", got.Status)
	}
}

func TestAMemberStatusIsParsedByName(t *testing.T) {
	for _, s := range []access.MemberStatus{access.MemberInvited, access.MemberActive, access.MemberDeactivated} {
		if got, ok := access.ParseMemberStatus(string(s)); !ok || got != s {
			t.Errorf("%s parsed as %q, %t", s, got, ok)
		}
	}
	for _, s := range []string{"", "gone", "Active"} {
		if _, ok := access.ParseMemberStatus(s); ok {
			t.Errorf("%q parsed as a status", s)
		}
	}
}

func TestAMemberRenamesThemselvesAndChoosesALanguage(t *testing.T) {
	offered := []string{"en", "ru"}
	m := access.Member{Email: "ada@example.com", DisplayName: "Ada", Status: access.MemberActive}
	got, err := m.Profiled(access.ProfileChange{DisplayName: new(" Ada Lovelace "), Locale: new("ru")}, offered)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Ada Lovelace" || got.Locale != "ru" || got.Email != m.Email || got.InstanceAdmin {
		t.Errorf("profiled = %+v", got)
	}
	if browser, err := got.Profiled(access.ProfileChange{Locale: new("")}, offered); err != nil || browser.Locale != "" {
		t.Errorf("following the browser = %+v, %v; want no locale", browser, err)
	}
	if same, err := got.Profiled(access.ProfileChange{}, offered); err != nil || same != got {
		t.Errorf("an empty change = %+v, %v; want the member unchanged", same, err)
	}
	for name, tc := range map[string]struct {
		change  access.ProfileChange
		pointer string
		code    string
	}{
		"blank name":       {access.ProfileChange{DisplayName: new(" ")}, "/display_name", "invalid_name"},
		"long name":        {access.ProfileChange{DisplayName: new(strings.Repeat("n", 201))}, "/display_name", "too_long"},
		"not offered":      {access.ProfileChange{Locale: new("de")}, "/locale", "unknown_locale"},
		"a region instead": {access.ProfileChange{Locale: new("ru-RU")}, "/locale", "unknown_locale"},
	} {
		_, err := m.Profiled(tc.change, offered)
		if code, pointer, violation := violationOf(err); code != "invalid_member" || pointer != tc.pointer || violation != tc.code {
			t.Errorf("%s: err = %v, want invalid_member with %s at %s", name, err, tc.code, tc.pointer)
		}
	}
}
