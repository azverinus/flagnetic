package mail_test

import (
	"context"
	"errors"
	"net"
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
