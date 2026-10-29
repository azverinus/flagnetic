package access_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func ptr[T any](v T) *T { return &v }

func smtpViolations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_smtp_settings" {
		t.Fatalf("error = %v, want invalid_smtp_settings", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestSMTPSettingsAreCheckedBeforeAnythingIsStored(t *testing.T) {
	valid := access.SMTPChange{
		Host: "smtp.example.com", Port: 587, Security: access.SMTPStartTLS, From: "Flagnetic <flags@example.com>",
	}
	for name, tc := range map[string]struct {
		change func(*access.SMTPChange)
		stored bool
		want   []string
	}{
		"valid, no credentials": {change: func(*access.SMTPChange) {}},
		"valid, credentials": {change: func(c *access.SMTPChange) {
			c.Username, c.Password = "relay", ptr("secret")
		}},
		"password kept": {stored: true, change: func(c *access.SMTPChange) { c.Username = "relay" }},
		"blank host":    {change: func(c *access.SMTPChange) { c.Host = "  " }, want: []string{"/host invalid_host"}},
		"host with space": {change: func(c *access.SMTPChange) { c.Host = "smtp example.com" },
			want: []string{"/host invalid_host"}},
		"host too long":  {change: func(c *access.SMTPChange) { c.Host = strings.Repeat("a", 254) }, want: []string{"/host invalid_host"}},
		"port zero":      {change: func(c *access.SMTPChange) { c.Port = 0 }, want: []string{"/port out_of_range"}},
		"port too large": {change: func(c *access.SMTPChange) { c.Port = 65536 }, want: []string{"/port out_of_range"}},
		"unknown security": {change: func(c *access.SMTPChange) { c.Security = "ssl" },
			want: []string{"/security unknown_security"}},
		"from without an address": {change: func(c *access.SMTPChange) { c.From = "Flagnetic" },
			want: []string{"/from invalid_sender"}},
		"from with two addresses": {change: func(c *access.SMTPChange) { c.From = "a@example.com, b@example.com" },
			want: []string{"/from invalid_sender"}},
		"username without a password": {change: func(c *access.SMTPChange) { c.Username = "relay" },
			want: []string{"/password credentials_together"}},
		"username with an emptied password": {stored: true, change: func(c *access.SMTPChange) {
			c.Username, c.Password = "relay", ptr("")
		}, want: []string{"/password credentials_together"}},
		"password without a username": {change: func(c *access.SMTPChange) { c.Password = ptr("secret") },
			want: []string{"/username credentials_together"}},
		"username across lines": {change: func(c *access.SMTPChange) {
			c.Username, c.Password = "relay\r\nBcc: x", ptr("secret")
		}, want: []string{"/username invalid_username"}},
		"credentials in the clear to another host": {change: func(c *access.SMTPChange) {
			c.Security, c.Username, c.Password = access.SMTPNone, "relay", ptr("secret")
		}, want: []string{"/security insecure_credentials"}},
		"credentials in the clear to this machine": {change: func(c *access.SMTPChange) {
			c.Host, c.Security, c.Username, c.Password = "127.0.0.1", access.SMTPNone, "relay", ptr("secret")
		}},
		"credentials in the clear to localhost": {change: func(c *access.SMTPChange) {
			c.Host, c.Security, c.Username, c.Password = "localhost", access.SMTPNone, "relay", ptr("secret")
		}},
		"credentials in the clear to ::1": {change: func(c *access.SMTPChange) {
			c.Host, c.Security, c.Username, c.Password = "::1", access.SMTPNone, "relay", ptr("secret")
		}},
		"every rule broken at once": {change: func(c *access.SMTPChange) { *c = access.SMTPChange{} },
			want: []string{"/host invalid_host", "/port out_of_range", "/security unknown_security", "/from invalid_sender"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			_, err := c.Check(tc.stored)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check = %v, want it accepted", err)
				}
				return
			}
			got := smtpViolations(t, err)
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("violations = %v, want %v", got, want)
			}
		})
	}
}

func TestCheckingSMTPSettingsTrimsWhatIsTyped(t *testing.T) {
	got, err := access.SMTPChange{
		Host: " smtp.example.com ", Port: 25, Security: access.SMTPNone, From: " flags@example.com ",
	}.Check(false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "smtp.example.com" || got.From != "flags@example.com" {
		t.Errorf("checked = %+v, want host and sender trimmed", got)
	}
}

func TestAStoredPasswordIsNotShownOnlyReported(t *testing.T) {
	s := access.SMTPSettings{}
	if s.PasswordSet() {
		t.Error("settings holding no password report one")
	}
	s.PasswordSealed = "v1:abc"
	if !s.PasswordSet() {
		t.Error("settings holding a sealed password do not report it")
	}
}
