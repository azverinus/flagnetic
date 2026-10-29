package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"slices"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
	"github.com/wneessen/go-mail/smtp"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
)

// The bounds of an exchange: connecting, then everything after it.
const (
	dialTimeout     = 10 * time.Second
	exchangeTimeout = 30 * time.Second
)

// Sender sends mail through an SMTP server with TLS 1.2 or later, verifying
// the server's certificate, and says which step failed as a class an
// operator can act on. A failure never carries the password or the mail.
type Sender struct {
	renderer *Renderer
	roots    *x509.CertPool
}

// NewSender returns a sender writing mail with renderer. Server certificates
// are verified against roots, the system's when nil.
func NewSender(renderer *Renderer, roots *x509.CertPool) *Sender {
	return &Sender{renderer: renderer, roots: roots}
}

var _ appaccess.Mailer = (*Sender)(nil)

// Send writes m and delivers it through server. It returns a
// *appaccess.MailError when the exchange fails.
func (s *Sender) Send(ctx context.Context, server appaccess.MailServer, m appaccess.Mail) error {
	rendered, err := s.renderer.Render(m)
	if err != nil {
		return err
	}
	msg := gomail.NewMsg()
	if err = msg.From(server.From); err != nil {
		return fmt.Errorf("the sender address: %w", err)
	}
	if err = msg.AddToFormat(m.To.Name, m.To.Email); err != nil {
		return fmt.Errorf("the recipient address: %w", err)
	}
	msg.Subject(rendered.Subject)
	msg.SetDateWithValue(m.At)
	msg.SetMessageID()
	msg.SetBodyString(gomail.TypeTextPlain, rendered.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, rendered.HTML)

	tlsConfig := &tls.Config{ServerName: server.Host, RootCAs: s.roots, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: dialTimeout}
	// The dial is bounded apart from the exchange.
	dial := dialer.DialContext
	opts := []gomail.Option{
		gomail.WithPort(server.Port),
		gomail.WithTimeout(exchangeTimeout),
		gomail.WithTLSConfig(tlsConfig),
		gomail.WithHELO(heloName(server.From)),
	}
	switch server.Security {
	case access.SMTPTLS:
		dial = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext
		opts = append(opts, gomail.WithSSL())
	case access.SMTPStartTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}
	auth := &negotiatedAuth{
		username: server.Username, password: server.Password.Reveal(), host: server.Host,
		insecureAllowed: server.Security == access.SMTPNone,
	}
	opts = append(opts, gomail.WithDialContextFunc(dial))
	if server.Username != "" {
		opts = append(opts, gomail.WithSMTPAuthCustom(auth))
	}
	client, err := gomail.NewClient(server.Host, opts...)
	if err != nil {
		return fmt.Errorf("preparing the connection: %w", err)
	}
	err = client.DialAndSendWithContext(ctx, msg)
	if err != nil {
		return &appaccess.MailError{Failure: classify(err, auth.started), Err: scrub(err, server)}
	}
	return nil
}

// heloName is the name the client greets the server with: the sender's
// domain, which every server accepts, rather than the local host name.
func heloName(from string) string {
	if _, domain, ok := strings.Cut(from, "@"); ok {
		if domain = strings.Trim(strings.TrimSpace(domain), "<>"); domain != "" {
			return domain
		}
	}
	return "localhost"
}

// classify names the step that failed. authStarted says the server's answer
// to the credentials was under way.
func classify(err error, authStarted bool) access.MailFailure {
	var (
		verify  *tls.CertificateVerificationError
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
		invalid x509.CertificateInvalidError
		record  tls.RecordHeaderError
		alert   tls.AlertError
		sendErr *gomail.SendError
		proto   *textproto.Error
		network net.Error
	)
	switch {
	case errors.As(err, &verify), errors.As(err, &unknown), errors.As(err, &host), errors.As(err, &invalid),
		errors.As(err, &record), errors.As(err, &alert),
		strings.Contains(err.Error(), "STARTTLS"):
		return access.MailFailureTLS
	case authStarted:
		return access.MailFailureAuthentication
	case errors.As(err, &sendErr) && !errors.As(err, &network) && !isBrokenConnection(err):
		return access.MailFailureRejected
	case errors.As(err, &proto):
		return access.MailFailureRejected
	default:
		return access.MailFailureConnection
	}
}

// isBrokenConnection reports whether err is the connection failing under an
// exchange rather than an answer from the server.
func isBrokenConnection(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

// scrub returns err without the password, should any step have echoed it.
func scrub(err error, server appaccess.MailServer) error {
	pw := server.Password.Reveal()
	if pw == "" || !strings.Contains(err.Error(), pw) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), pw, "[redacted]"))
}

// negotiatedAuth authenticates with the best mechanism the server offers
// that the connection may carry: PLAIN and LOGIN only over TLS or to this
// machine, CRAM-MD5 anywhere since it sends no password.
type negotiatedAuth struct {
	username, password, host string
	// insecureAllowed lets PLAIN and LOGIN cross an unencrypted connection;
	// the settings allow credentials over no security only to this machine.
	insecureAllowed bool

	inner smtp.Auth
	// started is set once the exchange of credentials begins, so a failure
	// from then on is the server's answer to them.
	started bool
}

var errNoMechanism = errors.New("the server offers no authentication mechanism this connection may use")

func (a *negotiatedAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	a.started = true
	secure := server.TLS || a.insecureAllowed
	switch {
	case secure && slices.Contains(server.Auth, "PLAIN"):
		a.inner = smtp.PlainAuth("", a.username, a.password, a.host, a.insecureAllowed)
	case secure && slices.Contains(server.Auth, "LOGIN"):
		a.inner = smtp.LoginAuth(a.username, a.password, a.host, a.insecureAllowed)
	case slices.Contains(server.Auth, "CRAM-MD5"):
		a.inner = smtp.CRAMMD5Auth(a.username, a.password)
	default:
		return "", nil, errNoMechanism
	}
	return a.inner.Start(server)
}

func (a *negotiatedAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	return a.inner.Next(fromServer, more)
}
