package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	netmail "net/mail"
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
	exchange := &boundedExchange{ctx: ctx, dialer: &net.Dialer{Timeout: dialTimeout}}
	if server.Security == access.SMTPTLS {
		exchange.tls = tlsConfig
	}
	defer exchange.release()
	opts := []gomail.Option{
		gomail.WithPort(server.Port),
		gomail.WithTimeout(exchangeTimeout),
		gomail.WithTLSConfig(tlsConfig),
		gomail.WithHELO(heloName(server.From)),
		gomail.WithDialContextFunc(exchange.dial),
	}
	switch server.Security {
	case access.SMTPTLS:
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
	if server.Username != "" {
		opts = append(opts, gomail.WithSMTPAuthCustom(auth))
	}
	client, err := gomail.NewClient(server.Host, opts...)
	if err != nil {
		return fmt.Errorf("preparing the connection: %w", err)
	}
	err = client.DialAndSendWithContext(ctx, msg)
	if err != nil {
		return &appaccess.MailError{Failure: classify(err, auth.authenticating), Err: scrub(err, server)}
	}
	return nil
}

// boundedExchange connects to the server within dialTimeout and ends the
// whole exchange after it within exchangeTimeout, or when ctx ends if that
// comes first. The client library sets a fresh deadline before each of its
// steps and reads ctx only while connecting, so the connection it is given
// holds every deadline to the exchange's end and is closed when ctx ends.
type boundedExchange struct {
	ctx    context.Context
	dialer *net.Dialer
	// tls is set when the server speaks TLS from the first byte.
	tls *tls.Config
	// stop ends the watch on ctx once the exchange is over.
	stop func() bool
}

func (e *boundedExchange) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	raw, err := e.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	end := time.Now().Add(exchangeTimeout)
	if d, ok := e.ctx.Deadline(); ok && d.Before(end) {
		end = d
	}
	e.release()
	e.stop = context.AfterFunc(e.ctx, func() { _ = raw.Close() })
	conn := &boundedConn{Conn: raw, end: end}
	if err = conn.SetDeadline(end); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if e.tls == nil {
		return conn, nil
	}
	// The TLS layer sits on the bounded connection, and the client library
	// still sees a *tls.Conn, which is how it learns the connection is secure.
	secured := tls.Client(conn, e.tls)
	if err = secured.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return secured, nil
}

// release ends the watch on ctx.
func (e *boundedExchange) release() {
	if e.stop != nil {
		e.stop()
	}
}

// boundedConn never lets a deadline pass the end of the exchange.
type boundedConn struct {
	net.Conn
	end time.Time
}

func (c *boundedConn) SetDeadline(t time.Time) error { return c.Conn.SetDeadline(c.bound(t)) }

func (c *boundedConn) SetReadDeadline(t time.Time) error { return c.Conn.SetReadDeadline(c.bound(t)) }

func (c *boundedConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(c.bound(t)) }

func (c *boundedConn) bound(t time.Time) time.Time {
	if t.IsZero() || t.After(c.end) {
		return c.end
	}
	return t
}

// heloName is the name the client greets the server with: the sender's
// domain, which every server accepts, rather than the local host name.
func heloName(from string) string {
	if addr, err := netmail.ParseAddress(from); err == nil {
		if _, domain, ok := strings.Cut(addr.Address, "@"); ok && domain != "" {
			return domain
		}
	}
	return "localhost"
}

// classify names the step that failed. authenticating says the exchange of
// credentials had begun and not yet succeeded.
func classify(err error, authenticating bool) access.MailFailure {
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
	case errors.As(err, &network), isBrokenConnection(err):
		return access.MailFailureConnection
	case authenticating:
		return access.MailFailureAuthentication
	case errors.As(err, &sendErr), errors.As(err, &proto):
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
	// authenticating is set while the exchange of credentials is under way,
	// so a failure then is the server's answer to them; a failure after the
	// server accepted them is not.
	authenticating bool
}

var errNoMechanism = errors.New("the server offers no authentication mechanism this connection may use")

func (a *negotiatedAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	a.authenticating = true
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
	toServer, err := a.inner.Next(fromServer, more)
	if err == nil && !more {
		// The server's last answer, without a challenge, accepted them.
		a.authenticating = false
	}
	return toServer, err
}
