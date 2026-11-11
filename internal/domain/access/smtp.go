package access

import (
	"net/mail"
	"net/netip"
	"strings"
	"time"
	"unicode"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// SMTPSecurity is how the connection to the SMTP server is secured.
type SMTPSecurity string

// The ways a connection to the SMTP server is secured.
const (
	// SMTPStartTLS upgrades a plain connection with STARTTLS, which the
	// server must offer.
	SMTPStartTLS SMTPSecurity = "starttls"
	// SMTPTLS speaks TLS from the first byte.
	SMTPTLS SMTPSecurity = "tls"
	// SMTPNone secures nothing.
	SMTPNone SMTPSecurity = "none"
)

// SMTPSettings are the mail server the instance sends its mail through, as
// stored.
type SMTPSettings struct {
	Host     string
	Port     int
	Security SMTPSecurity
	// Username is who to authenticate as; "" for no authentication.
	Username string
	// PasswordSealed is the password sealed under the instance's secret key;
	// "" when none is stored. It is never shown: only SMTPSettings.PasswordSet
	// is.
	PasswordSealed string
	// From is the sender of every mail: one address, optionally with a
	// display name.
	From      string
	UpdatedAt time.Time
}

// PasswordSet reports whether a password is stored.
func (s SMTPSettings) PasswordSet() bool { return s.PasswordSealed != "" }

// SMTPChange is a write of the SMTP settings. Password is write only: nil
// keeps the stored one.
type SMTPChange struct {
	Host     string
	Port     int
	Security SMTPSecurity
	Username string
	Password *string
	From     string
}

// Bounds of the SMTP settings.
const (
	maxSMTPHostLength     = 253
	maxSMTPUsernameLength = 256
	maxSMTPFromLength     = 320
	maxPort               = 65535
)

// Check returns the change with its host, username and sender trimmed, or
// invalid_smtp_settings naming each field that breaks a rule: a host of 1 to
// 253 characters without spaces, a port from 1 to 65535, a known security, a
// sender that is one address, a username on one line, a username and a
// password together (a password the change leaves out is the one stored in
// stored, zero when nothing is), a stored password kept only while the host,
// port, security and username it was given for stay as they are (otherwise
// the write would send it somewhere its owner never chose, so the change
// must give the password again), and credentials over no
// security only to this machine, since the client refuses to send them in
// the clear and nothing could ever be sent.
func (c SMTPChange) Check(stored SMTPSettings) (SMTPChange, error) {
	c.Host, c.Username, c.From = strings.TrimSpace(c.Host), strings.TrimSpace(c.Username), strings.TrimSpace(c.From)
	var vs []apperrors.Violation
	if c.Host == "" || len(c.Host) > maxSMTPHostLength || strings.ContainsFunc(c.Host, unicode.IsSpace) {
		vs = append(vs, apperrors.Violation{
			Pointer: "/host", Code: "invalid_host",
			Detail: "must be a host name or address of 1 to 253 characters without spaces",
		})
	}
	if c.Port < 1 || c.Port > maxPort {
		vs = append(vs, outOfRange("/port", "a port number", 1, maxPort))
	}
	switch c.Security {
	case SMTPStartTLS, SMTPTLS, SMTPNone:
	default:
		vs = append(vs, apperrors.Violation{
			Pointer: "/security", Code: "unknown_security", Detail: "must be starttls, tls or none",
			Params: map[string]any{"securities": []string{string(SMTPStartTLS), string(SMTPTLS), string(SMTPNone)}},
		})
	}
	if _, err := mail.ParseAddress(c.From); err != nil || len(c.From) > maxSMTPFromLength {
		vs = append(vs, apperrors.Violation{
			Pointer: "/from", Code: "invalid_sender",
			Detail: "must be one address, optionally with a display name, such as Flagnetic <flags@example.com>",
		})
	}
	if len(c.Username) > maxSMTPUsernameLength || strings.ContainsAny(c.Username, "\r\n\x00") {
		vs = append(vs, apperrors.Violation{
			Pointer: "/username", Code: "invalid_username", Detail: "must be at most 256 characters on one line",
		})
	}
	hasPassword := stored.PasswordSet()
	if c.Password != nil {
		hasPassword = *c.Password != ""
	}
	switch {
	case c.Username != "" && c.Password == nil && stored.PasswordSet() && c.targetMoved(stored):
		vs = append(vs, apperrors.Violation{
			Pointer: "/password", Code: "credentials_together",
			Detail: "a change of host, port, security or username needs the password again",
		})
	case c.Username != "" && !hasPassword:
		vs = append(vs, apperrors.Violation{
			Pointer: "/password", Code: "credentials_together", Detail: "a username needs a password",
		})
	case c.Username == "" && c.Password != nil && *c.Password != "":
		vs = append(vs, apperrors.Violation{
			Pointer: "/username", Code: "credentials_together", Detail: "a password needs a username",
		})
	case c.Username != "" && c.Security == SMTPNone && !isThisMachine(c.Host):
		vs = append(vs, apperrors.Violation{
			Pointer: "/security", Code: "insecure_credentials",
			Detail: "a username and password need starttls or tls, unless the host is this machine",
		})
	}
	if len(vs) > 0 {
		return SMTPChange{}, apperrors.Invalid("invalid_smtp_settings", "the SMTP settings are not valid", vs...)
	}
	return c, nil
}

// targetMoved reports whether c reaches, or authenticates as, someone other
// than stored does.
func (c SMTPChange) targetMoved(stored SMTPSettings) bool {
	return !strings.EqualFold(c.Host, stored.Host) || c.Port != stored.Port ||
		c.Security != stored.Security || c.Username != stored.Username
}

// isThisMachine reports whether host names this machine, the one place a
// password may travel unencrypted.
func isThisMachine(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}
