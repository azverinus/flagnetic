package access

import (
	_ "embed"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Password length bounds, in code points. MinPasswordLength is the floor of
// the minimum an instance admin may set: they may raise it, never lower it.
// MaxPasswordLength is fixed, and checked before any hash, so a huge
// password cannot cost one.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

// The rules of the password policy, named in a refusal so a client explains
// which one a password broke.
const (
	PasswordRuleLength = "length"
	PasswordRuleEmail  = "email"
	PasswordRuleCommon = "common"
)

//go:embed common-passwords.txt
var commonPasswordList string

// commonPasswords are the common passwords long enough to pass the length
// rule, in lower case.
var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for line := range strings.SplitSeq(commonPasswordList, "\n") {
		if line != "" && !strings.HasPrefix(line, "# ") {
			m[line] = true
		}
	}
	return m
}()

// CheckPassword refuses, with weak_password, a password for the member with
// email that has fewer code points than minLength (never below 12) or more
// than 128, contains the email's local part whatever its case, or is a
// common password. There are no composition rules: length is what makes a
// password hard to guess.
func CheckPassword(password, email string, minLength int) error {
	return CheckPasswordAt("/password", password, email, minLength)
}

// CheckPasswordAt is CheckPassword for a password sent at pointer in the
// request, which its refusal names, such as /new_password beside the
// current one.
func CheckPasswordAt(pointer, password, email string, minLength int) error {
	minLength = max(minLength, MinPasswordLength)
	if n := utf8.RuneCountInString(password); n < minLength || n > MaxPasswordLength {
		return weakPassword(pointer, PasswordRuleLength, "a password has "+strconv.Itoa(minLength)+" to 128 characters",
			map[string]any{"rule": PasswordRuleLength, "min": minLength, "max": MaxPasswordLength})
	}
	lower := strings.ToLower(password)
	if local, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@"); local != "" && strings.Contains(lower, local) {
		return weakPassword(pointer, PasswordRuleEmail, "a password does not contain the part of the email before the @", nil)
	}
	if commonPasswords[lower] {
		return weakPassword(pointer, PasswordRuleCommon, "the password is on the list of common passwords", nil)
	}
	return nil
}

func weakPassword(pointer, rule, detail string, params map[string]any) error {
	if params == nil {
		params = map[string]any{"rule": rule}
	}
	return apperrors.Invalid("weak_password", "the password policy refuses this password: "+detail, apperrors.Violation{
		Pointer: pointer, Code: "weak_password", Detail: detail, Params: params,
	})
}

// PasswordRule returns the rule of the password policy err reports broken,
// or "" when err is no refusal of the policy.
func PasswordRule(err error) string {
	e, ok := apperrors.From(err)
	if !ok || e.Code != "weak_password" || len(e.Violations) == 0 {
		return ""
	}
	rule, _ := e.Violations[0].Params["rule"].(string)
	return rule
}

const generatedPasswordLength = 24

// GeneratePassword draws a password of 24 base62 characters, about 143
// bits, for a member created without one. It draws again until the policy
// accepts it for email, which a short local part can make necessary.
func GeneratePassword(email string, random io.Reader) secret.String {
	for {
		p := secret.Base62(random, generatedPasswordLength)
		if CheckPassword(p, email, MinPasswordLength) == nil {
			return secret.New(p)
		}
	}
}
