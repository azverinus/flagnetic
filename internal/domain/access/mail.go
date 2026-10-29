package access

import "slices"

// MailKind is what a mail is for, which decides its template.
type MailKind string

// The kinds of mail.
const (
	// MailTest is the mail an instance admin sends themselves to try the
	// SMTP settings.
	MailTest MailKind = "test"
)

// MailKinds are every kind of mail the instance sends; each has a template
// in every language of MailLocales.
var MailKinds = []MailKind{MailTest}

// MailLocales are the languages mail is written in, the console's.
var MailLocales = []string{"en", "ru"}

// MailFailure is the class of a failed send, the part an operator acts on.
type MailFailure string

// The classes of a failed send.
const (
	// MailFailureConnection means the server could not be reached, or the
	// connection broke or timed out.
	MailFailureConnection MailFailure = "connection"
	// MailFailureTLS means TLS could not be established, the server did not
	// offer STARTTLS, or its certificate was not trusted.
	MailFailureTLS MailFailure = "tls"
	// MailFailureAuthentication means the server refused the username and
	// password, or they could not be sent safely.
	MailFailureAuthentication MailFailure = "authentication"
	// MailFailureRejected means the server refused the sender, a recipient or
	// the mail.
	MailFailureRejected MailFailure = "rejected"
)

// MailLocale returns the language a mail is written in: the first of
// preferred that mail is written in, else fallback. A caller lists whose
// language counts in order, such as the recipient's, then for an invitation
// the inviter's, and passes the instance's default as the fallback.
func MailLocale(fallback string, preferred ...string) string {
	for _, l := range preferred {
		if slices.Contains(MailLocales, l) {
			return l
		}
	}
	return fallback
}
