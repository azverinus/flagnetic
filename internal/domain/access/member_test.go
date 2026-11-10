package access_test

import (
	"strings"
	"testing"
	"time"

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

// Until an invitation is mailed, its link is held by whoever issued it, who
// can accept it as the person invited. What an invited member is given
// before they join is therefore given to whoever holds their link.
func TestAnInvitedMemberIsMadeInstanceAdminOnlyOnceTheyJoined(t *testing.T) {
	invited := access.Member{Email: "ada@example.com", DisplayName: "Ada", Status: access.MemberInvited}
	_, err := invited.Edited(access.MemberChange{InstanceAdmin: new(true)})
	if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindConflict || e.Code != "member_invited" {
		t.Errorf("promoting an invited member = %v, want the conflict member_invited", err)
	}
	if got, err := invited.Edited(access.MemberChange{DisplayName: new("Ada L"), InstanceAdmin: new(false)}); err != nil || got.DisplayName != "Ada L" {
		t.Errorf("renaming an invited member = %+v, %v", got, err)
	}
	// One invited again after a deactivation keeps what they held.
	was := access.Member{Email: "ada@example.com", DisplayName: "Ada", Status: access.MemberInvited, InstanceAdmin: true}
	if got, err := was.Edited(access.MemberChange{InstanceAdmin: new(true)}); err != nil || !got.InstanceAdmin {
		t.Errorf("leaving an invited instance admin as they are = %+v, %v", got, err)
	}
	if got, err := was.Edited(access.MemberChange{InstanceAdmin: new(false)}); err != nil || got.InstanceAdmin {
		t.Errorf("demoting an invited instance admin = %+v, %v", got, err)
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

func TestAProvisionedMemberIsActiveWithoutAPasswordAndNamedAfterTheirNameOrEmail(t *testing.T) {
	for _, tc := range []struct{ name, email, display, want string }{
		{"a name", "ada@example.com", " Ada Lovelace ", "Ada Lovelace"},
		{"no name", "ada@example.com", "", "ada"},
		{"a name over the bound", "ada@example.com", strings.Repeat("é", 300), strings.Repeat("é", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := access.NewProvisionedMember(access.MemberID{}, tc.email, tc.display, now)
			if err != nil || m.DisplayName != tc.want || m.Status != access.MemberActive || m.InstanceAdmin || !m.CreatedAt.Equal(now) {
				t.Errorf("NewProvisionedMember = %+v, %v; want an active member named %q", m, err, tc.want)
			}
		})
	}
	if _, err := access.NewProvisionedMember(access.MemberID{}, "not-an-email", "x", now); err == nil {
		t.Error("an address without an at sign was accepted")
	}
}

func TestAnInstanceAdminAProviderMadeIsDemotedOnlyByThatProvider(t *testing.T) {
	ids := newIDs()
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	admin, err := access.NewInstanceAdmin(id.New[access.Member](ids), "ira@example.com", "Ira", when)
	if err != nil {
		t.Fatal(err)
	}
	admin.InstanceAdminProvider = "okta"
	no, yes := false, true

	_, err = admin.Edited(access.MemberChange{InstanceAdmin: &no})
	if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindConflict || e.Code != "sso_sourced" {
		t.Errorf("demoting by hand = %v, want 409 sso_sourced", err)
	}
	same, err := admin.Edited(access.MemberChange{InstanceAdmin: &yes})
	if err != nil || same != admin {
		t.Errorf("promoting an instance admin = %+v, %v; want them unchanged", same, err)
	}

	admin.InstanceAdminProvider = ""
	demoted, err := admin.Edited(access.MemberChange{InstanceAdmin: &no})
	if err != nil || demoted.InstanceAdmin {
		t.Errorf("demoting an instance admin made by hand = %+v, %v", demoted, err)
	}
}

func provisioned(t *testing.T) access.Member {
	t.Helper()
	m, err := access.NewSCIMMember(id.New[access.Member](newIDs()), " Jo@Example.com ", " Jo Doe ", "okta", "ext-1", now)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAMemberASCIMTokenProvisionsIsActiveAndBelongsToItsProvider(t *testing.T) {
	m := provisioned(t)
	if m.Email != "Jo@Example.com" || m.DisplayName != "Jo Doe" || m.Status != access.MemberActive || m.InstanceAdmin ||
		m.Provider != "okta" || m.ExternalID != "ext-1" || !m.Provisioned() || !m.CreatedAt.Equal(now) {
		t.Errorf("member = %+v", m)
	}
	named, err := access.NewSCIMMember(id.New[access.Member](newIDs()), "jo@example.com", " ", "okta", "", now)
	if err != nil || named.DisplayName != "jo@example.com" || named.ExternalID != "" {
		t.Errorf("a member without a name = %+v, %v; want them named after their email", named, err)
	}
	for name, c := range map[string]struct{ email, display string }{
		"a bad email": {"nobody", "x"}, "a long name": {"jo@example.com", strings.Repeat("n", 201)},
	} {
		if _, err := access.NewSCIMMember(id.New[access.Member](newIDs()), c.email, c.display, "okta", "", now); !apperrors.IsKind(err, apperrors.KindInvalid) {
			t.Errorf("%s = %v, want an invalid refusal", name, err)
		}
	}
}

func TestOnlyAnActiveMemberNobodyProvisionsAndWhoIsNoInstanceAdminIsAdoptable(t *testing.T) {
	active := access.Member{Status: access.MemberActive}
	for name, c := range map[string]struct {
		m    access.Member
		want bool
	}{
		"active":                  {active, true},
		"an instance admin":       {access.Member{Status: access.MemberActive, InstanceAdmin: true}, false},
		"invited":                 {access.Member{Status: access.MemberInvited}, false},
		"deactivated":             {access.Member{Status: access.MemberDeactivated}, false},
		"provisioned already":     {access.Member{Status: access.MemberActive, Provider: "okta"}, false},
		"provisioned, same again": {access.Member{Status: access.MemberActive, Provider: "okta", ExternalID: "x"}, false},
	} {
		if got := c.m.Adoptable(); got != c.want {
			t.Errorf("%s: Adoptable = %v, want %v", name, got, c.want)
		}
	}
	adopted, err := active.Adopted(" New@Example.com ", "New Name", "okta", "ext-9")
	if err != nil || adopted.Email != "New@Example.com" || adopted.DisplayName != "New Name" || adopted.Provider != "okta" ||
		adopted.ExternalID != "ext-9" || adopted.Status != access.MemberActive {
		t.Errorf("Adopted = %+v, %v", adopted, err)
	}
	if e, ok := apperrors.From(access.ErrNotAdoptable); !ok || e.Kind != apperrors.KindConflict {
		t.Errorf("ErrNotAdoptable = %v", access.ErrNotAdoptable)
	}
}

func TestADirectorySyncsEmailAndNameWithinTheBoundsOfAnyMember(t *testing.T) {
	m := provisioned(t)
	email, name := " Moved@Example.com ", "  "
	synced, err := m.Synced(&email, &name)
	if err != nil || synced.Email != "Moved@Example.com" || synced.DisplayName != "Moved@Example.com" {
		t.Errorf("Synced = %+v, %v; want the email and, without a name, the email as the name", synced, err)
	}
	left, err := m.Synced(nil, nil)
	if err != nil || left != m {
		t.Errorf("Synced of nothing = %+v, %v; want the member as they are", left, err)
	}
	bad := "nobody"
	if _, err = m.Synced(&bad, nil); !apperrors.IsKind(err, apperrors.KindInvalid) {
		t.Errorf("Synced to a bad email = %v, want an invalid refusal", err)
	}
}

func TestAProvisionedMembersEmailAndNameAreRefusedToTheManagementAPI(t *testing.T) {
	m := provisioned(t)
	other, same := "someone-else@example.com", " Jo Doe "
	for name, change := range map[string]access.MemberChange{
		"another email": {Email: &other}, "another name": {DisplayName: &other},
	} {
		if _, err := m.Edited(change); !isProvisioned(err) {
			t.Errorf("Edited(%s) = %v, want member_provisioned", name, err)
		}
		if _, err := m.Profiled(access.ProfileChange{DisplayName: change.DisplayName}, []string{"en"}); change.DisplayName != nil && !isProvisioned(err) {
			t.Errorf("Profiled(%s) = %v, want member_provisioned", name, err)
		}
	}
	admin := true
	if got, err := m.Edited(access.MemberChange{Email: &m.Email, DisplayName: &same, InstanceAdmin: &admin}); err != nil || !got.InstanceAdmin {
		t.Errorf("Edited with what is so and a promotion = %+v, %v; want only the promotion", got, err)
	}
	locale := "en"
	if got, err := m.Profiled(access.ProfileChange{Locale: &locale}, []string{"en"}); err != nil || got.Locale != "en" {
		t.Errorf("Profiled with a locale = %+v, %v; the language is the member's own", got, err)
	}
	var plain access.Member
	plain.Email, plain.DisplayName = "a@example.com", "A"
	if _, err := plain.Edited(access.MemberChange{Email: &other}); err != nil {
		t.Errorf("Edited of a member nobody provisions = %v", err)
	}
}

func isProvisioned(err error) bool {
	e, ok := apperrors.From(err)
	return ok && e.Kind == apperrors.KindConflict && e.Code == "member_provisioned"
}
