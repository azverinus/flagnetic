package access

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MemberID identifies a member.
type MemberID = id.Of[Member]

// MemberStatus is where a member is in their life on the instance.
type MemberStatus string

// The member statuses. There is no deletion: an offboarded member is
// deactivated and keeps their history.
const (
	MemberInvited     MemberStatus = "invited"
	MemberActive      MemberStatus = "active"
	MemberDeactivated MemberStatus = "deactivated"
)

// ParseMemberStatus returns the status s names; false for none.
func ParseMemberStatus(s string) (MemberStatus, bool) {
	switch st := MemberStatus(s); st {
	case MemberInvited, MemberActive, MemberDeactivated:
		return st, true
	}
	return "", false
}

// Member is a person who uses the management API or the console.
type Member struct {
	ID MemberID
	// Email is unique on the instance whatever its case, and kept as given.
	Email       string
	DisplayName string
	Status      MemberStatus
	// InstanceAdmin holds every permission in every project.
	InstanceAdmin bool
	// Locale is the console language the member chose, or "" to follow the
	// browser.
	Locale    string
	CreatedAt time.Time
}

// Bounds of a member's fields.
const (
	maxEmailLength       = 254
	maxDisplayNameLength = 200
)

// NewInstanceAdmin returns an active instance admin. Without a display name
// the member is named after the local part of their email.
func NewInstanceAdmin(memberID MemberID, email, displayName string, now time.Time) (Member, error) {
	email = strings.TrimSpace(email)
	local, err := checkEmail(email)
	if err != nil {
		return Member{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = local
	}
	if err := checkDisplayName(displayName); err != nil {
		return Member{}, err
	}
	return Member{
		ID: memberID, Email: email, DisplayName: displayName,
		Status: MemberActive, InstanceAdmin: true, CreatedAt: now,
	}, nil
}

// checkEmail accepts one address of the form local@domain without spaces and
// returns its local part. Whether the address receives mail is not judged:
// that would take sending some.
func checkEmail(email string) (string, error) {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") ||
		len(email) > maxEmailLength || strings.ContainsFunc(email, unicode.IsSpace) {
		return "", invalidMember(apperrors.Violation{
			Pointer: "/email", Code: "invalid_email", Detail: "an email is one address of the form name@domain, at most 254 characters",
		})
	}
	return local, nil
}

// checkDisplayName accepts a trimmed display name of 1 to 200 characters.
func checkDisplayName(name string) error {
	switch {
	case name == "":
		return invalidMember(apperrors.Violation{Pointer: "/display_name", Code: "invalid_name", Detail: "a display name is not blank"})
	case utf8.RuneCountInString(name) > maxDisplayNameLength:
		return invalidMember(apperrors.Violation{
			Pointer: "/display_name", Code: "too_long", Detail: "a display name has at most 200 characters",
			Params: map[string]any{"max": maxDisplayNameLength},
		})
	}
	return nil
}

func invalidMember(v apperrors.Violation) error {
	return apperrors.Invalid("invalid_member", "the member is not valid", v)
}

// MemberChange is an edit of a member by an instance admin; a nil field is
// left as it is.
type MemberChange struct {
	Email         *string
	DisplayName   *string
	InstanceAdmin *bool
}

// Edited returns m with change applied, the email and display name trimmed
// and held to the bounds of a new member's. Whether another member holds the
// email is the store's to judge.
func (m Member) Edited(change MemberChange) (Member, error) {
	if change.Email != nil {
		email := strings.TrimSpace(*change.Email)
		if _, err := checkEmail(email); err != nil {
			return Member{}, err
		}
		m.Email = email
	}
	if change.DisplayName != nil {
		name := strings.TrimSpace(*change.DisplayName)
		if err := checkDisplayName(name); err != nil {
			return Member{}, err
		}
		m.DisplayName = name
	}
	if change.InstanceAdmin != nil {
		m.InstanceAdmin = *change.InstanceAdmin
	}
	return m, nil
}

// ProfileChange is a member's edit of themself; a nil field is left as it
// is.
type ProfileChange struct {
	DisplayName *string
	// Locale is the console language chosen; "" follows the browser.
	Locale *string
}

// Profiled returns m with change applied: the display name trimmed and held
// to the bounds of a new member's, the locale one of offered, the console
// languages, or "" to follow the browser. The email and the instance admin
// switch are an instance admin's to change, never the member's own.
func (m Member) Profiled(change ProfileChange, offered []string) (Member, error) {
	if change.DisplayName != nil {
		name := strings.TrimSpace(*change.DisplayName)
		if err := checkDisplayName(name); err != nil {
			return Member{}, err
		}
		m.DisplayName = name
	}
	if change.Locale != nil {
		if l := *change.Locale; l != "" && !slices.Contains(offered, l) {
			return Member{}, invalidMember(apperrors.Violation{
				Pointer: "/locale", Code: "unknown_locale",
				Detail: "a locale is one of the console's languages, " + strings.Join(offered, ", ") + ", or null to follow the browser",
				Params: map[string]any{"locales": offered},
			})
		}
		m.Locale = *change.Locale
	}
	return m, nil
}

// Deactivated returns m offboarded: they can no longer sign in, and every
// credential they held is revoked with them.
func (m Member) Deactivated() Member {
	m.Status = MemberDeactivated
	return m
}

// Reactivated returns m back on the instance: active when they hold a
// password to sign in with, invited otherwise, to set one through an
// invitation. Nothing revoked at their deactivation comes back.
func (m Member) Reactivated(holdsPassword bool) Member {
	m.Status = MemberInvited
	if holdsPassword {
		m.Status = MemberActive
	}
	return m
}

// IsActiveInstanceAdmin reports whether m can administer the instance now.
func (m Member) IsActiveInstanceAdmin() bool { return m.Status == MemberActive && m.InstanceAdmin }

// ErrLastInstanceAdmin refuses a change that would leave the instance
// without an active instance admin, whom nobody could replace through the
// API.
var ErrLastInstanceAdmin = apperrors.Conflict("last_instance_admin",
	"the instance's last active instance admin can be neither deactivated nor demoted")

// KeepAnInstanceAdmin refuses changing before into after when before is an
// active instance admin, after is not, and the instance has no other active
// instance admin, others counting them. The count must be read under the lock that
// serializes such changes, or two admins demoting each other at once would
// both pass.
func KeepAnInstanceAdmin(before, after Member, others int) error {
	if before.IsActiveInstanceAdmin() && !after.IsActiveInstanceAdmin() && others == 0 {
		return ErrLastInstanceAdmin
	}
	return nil
}
