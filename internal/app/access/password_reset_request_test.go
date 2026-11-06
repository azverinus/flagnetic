package access_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Without the address the console is served at, a mailed link could only carry
// the address the request claimed to come to, so nothing is issued or mailed;
// the same request with the address mails the member, showing the instance
// would have.
func TestNothingIsMailedWhenTheInstanceHasNoBaseURL(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	pool := testkit.FreshPool(t)
	store, tx := pgaccess.NewStore(pool), postgres.NewTransactor(pool)
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	c := testkit.Clock()
	ids := testkit.IDs(c)
	configure := appaccess.NewConfigureSMTP(appaccess.ConfigureSMTPDeps{
		Tx: tx, Store: store, Cache: nopCache{}, Sealer: keyring, Audit: testkit.Record(pool), Clock: c,
		IDs: ids, BaseURLSet: true,
	})
	if _, err := configure.Handle(t.Context(), smtpFor(mp), governance.Request{Actor: cli}); err != nil {
		t.Fatal(err)
	}
	renderer, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	logs := testkit.NewLogSink()
	background := appaccess.NewBackground(logs.Logger())
	postman := appaccess.NewPostman(appaccess.PostmanDeps{
		Settings: store, Opener: keyring, Mailer: mail.NewSender(renderer, testkit.MailpitRoots()), Counter: &counted{},
		After: postgres.AfterCommit{}, Clock: c, Logger: logs.Logger(), Background: background, DefaultLocale: "en",
	})
	request := func(consoleURL, email string) {
		appaccess.NewRequestPasswordReset(appaccess.RequestPasswordResetDeps{
			Tx: tx, Store: store, Settings: store, SMTP: store, Postman: postman, Background: background,
			Audit: testkit.Record(pool), Clock: c, IDs: ids, Random: rand.Reader, Logger: logs.Logger(), ConsoleURL: consoleURL,
		}).Handle(t.Context(), appaccess.RequestPasswordResetCommand{Email: email, RequestID: "req-" + rand.Text()})
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		background.Wait(ctx)
		if ctx.Err() != nil {
			t.Fatal("the request was still running after 10 seconds")
		}
	}
	m := testkit.NewMember(t, pool)

	request("", m.Email)
	mp.AssertNoMail(t, m.Email)

	request("https://flags.example.com", m.Email)
	mp.AwaitMails(t, m.Email, 1)
}
