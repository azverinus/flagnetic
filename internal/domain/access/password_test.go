package access_test

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestThePasswordPolicyRefusesShortLongPersonalAndCommonPasswords(t *testing.T) {
	const email = "marguerite.duras@example.com"
	for password, rule := range map[string]string{
		"short-pw-11":                  "length",
		strings.Repeat("x", 129):       "length",
		strings.Repeat("ж", 129):       "length",
		strings.Repeat("ж", 11):        "length",
		"Marguerite.Duras":             "email",
		"xx-MARGUERITE.DURAS-password": "email",
		"password1234":                 "common",
		"QWERTYUIOPASDFGHJKL":          "common",
		"Correct Horse Battery Staple": "common",
	} {
		err := access.CheckPassword(password, email, access.MinPasswordLength)
		e, ok := apperrors.From(err)
		if !ok || e.Kind != apperrors.KindInvalid || e.Code != "weak_password" || len(e.Violations) != 1 {
			t.Errorf("%.20q: err = %v, want weak_password", password, err)
			continue
		}
		v := e.Violations[0]
		if v.Pointer != "/password" || v.Code != "weak_password" || v.Params["rule"] != rule {
			t.Errorf("%.20q: violation = %+v, want rule %s at /password", password, v, rule)
		}
		if rule == "length" && (v.Params["min"] != 12 || v.Params["max"] != 128) {
			t.Errorf("%.20q: params = %v, want the bounds 12 and 128", password, v.Params)
		}
	}
	for _, password := range []string{strings.Repeat("ж", 128), strings.Repeat("ж", 12), "lantern-quartz-meadow-2291"} {
		if err := access.CheckPassword(password, email, access.MinPasswordLength); err != nil {
			t.Errorf("%.20q: err = %v, want accepted", password, err)
		}
	}
}

func TestThePasswordMinimumIsNeverBelowTwelve(t *testing.T) {
	if err := access.CheckPassword("eleven-char", "a@example.com", 8); err == nil {
		t.Error("an 11-character password passed with a minimum of 8, want the floor of 12 to hold")
	}
	err := access.CheckPassword("lantern-quartz-1", "a@example.com", 20)
	if e, _ := apperrors.From(err); e == nil || e.Violations[0].Params["min"] != 20 {
		t.Errorf("err = %v, want length refused with the minimum of 20", err)
	}
}

func TestAGeneratedPasswordPassesThePolicy(t *testing.T) {
	// A one-letter local part is in most random passwords; the generator
	// draws again until the policy accepts one.
	for range 50 {
		p := access.GeneratePassword("a@example.com", rand.Reader)
		if err := access.CheckPassword(p.Reveal(), "a@example.com", access.MinPasswordLength); err != nil || len(p.Reveal()) != 24 {
			t.Fatalf("generated %d characters: %v", len(p.Reveal()), err)
		}
	}
}

func TestThePasswordPolicyNamesTheFieldItRefuses(t *testing.T) {
	err := access.CheckPasswordAt("/new_password", "short", "ada@example.com", access.MinPasswordLength)
	if code, pointer, violation := violationOf(err); code != "weak_password" || pointer != "/new_password" || violation != "weak_password" {
		t.Errorf("err = %v, want weak_password at /new_password", err)
	}
	if access.PasswordRule(err) != access.PasswordRuleLength {
		t.Errorf("rule = %q, want length", access.PasswordRule(err))
	}
}
