package access

import (
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
	if utf8.RuneCountInString(displayName) > maxDisplayNameLength {
		return Member{}, apperrors.Invalid("invalid_member", "the member is not valid", apperrors.Violation{
			Pointer: "/display_name", Code: "too_long", Detail: "a display name has at most 200 characters",
		})
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
		return "", apperrors.Invalid("invalid_member", "the member is not valid", apperrors.Violation{
			Pointer: "/email", Code: "invalid_email", Detail: "an email is one address of the form name@domain, at most 254 characters",
		})
	}
	return local, nil
}
