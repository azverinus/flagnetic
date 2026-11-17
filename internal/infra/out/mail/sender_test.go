package mail_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
)

// A server that accepts the connection and then says nothing holds the
// sender only until the context ends, not for the client library's own
// timeouts, which it renews before each step.
func TestASilentServerHoldsTheSenderOnlyUntilTheContextEnds(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	r, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	sender := mail.NewSender(r, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = sender.Send(ctx, appaccess.MailServer{
		Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Security: access.SMTPNone, From: "flags@example.com",
	}, appaccess.Mail{
		Kind: access.MailTest, Locale: "en", To: appaccess.MailRecipient{Name: "Ada", Email: "ada@example.com"},
		Data: appaccess.TestMailData{}, At: started,
	})

	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("the send took %v against a context of 300ms", took)
	}
	var failed *appaccess.MailError
	if !errors.As(err, &failed) || failed.Failure != access.MailFailureConnection {
		t.Errorf("err = %v, want a connection failure", err)
	}
}

// captureServer is an SMTP server that accepts one mail without security and
// sends the message it received to the returned channel.
func captureServer(t *testing.T) (port int, received <-chan string) {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	out := make(chan string, 1)
	go func() {
		conn, acceptErr := l.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		rd := textproto.NewReader(bufio.NewReader(conn))
		say := func(s string) { _, _ = fmt.Fprintf(conn, "%s\r\n", s) }
		say("220 test ESMTP")
		for {
			line, readErr := rd.ReadLine()
			if readErr != nil {
				return
			}
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 test")
			case strings.HasPrefix(cmd, "DATA"):
				say("354 go ahead")
				body, dotErr := rd.ReadDotBytes()
				if dotErr != nil {
					return
				}
				out <- string(body)
				say("250 queued")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, out
}

// A display name holds any character: quotes, backslashes and line breaks
// come from members, SCIM and SSO, and must neither stop the mail nor start
// another header.
func TestADisplayNameWithSpecialCharactersIsMailed(t *testing.T) {
	names := []string{`Half "quote`, `Back\`, "Line\r\nBcc: evil@example.com", `Ada "Countess" \ Lovelace`}
	r, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	sender := mail.NewSender(r, nil)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			port, received := captureServer(t)
			err := sender.Send(t.Context(), appaccess.MailServer{
				Host: "127.0.0.1", Port: port, Security: access.SMTPNone, From: "flags@example.com",
			}, appaccess.Mail{
				Kind: access.MailTest, Locale: "en", To: appaccess.MailRecipient{Name: name, Email: "ada@example.com"},
				Data: appaccess.TestMailData{}, At: time.Now(),
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			msg, err := netmail.ReadMessage(strings.NewReader(<-received))
			if err != nil {
				t.Fatal(err)
			}
			if bcc := msg.Header.Get("Bcc"); bcc != "" {
				t.Errorf("a header was injected: Bcc = %q", bcc)
			}
			to, err := msg.Header.AddressList("To")
			if err != nil || len(to) != 1 {
				t.Fatalf("To = %v, %v, want one address", to, err)
			}
			if to[0].Address != "ada@example.com" {
				t.Errorf("address = %q", to[0].Address)
			}
			if to[0].Name != name {
				t.Errorf("name = %q, want %q", to[0].Name, name)
			}
		})
	}
}
