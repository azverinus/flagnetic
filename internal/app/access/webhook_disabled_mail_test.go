package access_test

import (
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Without the address the console is served at the mail could not link to the
// subscription, so an instance whose SMTP server outlived that setting mails
// nothing; the same notice with the address reaches the instance admin.
func TestADisabledSubscriptionIsNotMailedWhenTheInstanceHasNoBaseURL(t *testing.T) {
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
	admin, _ := testkit.NewInstanceAdmin(t, pool)
	notify := func(consoleURL string) {
		t.Helper()
		mailer := appaccess.NewWebhookDisabledMailer(postman, store, store, consoleURL)
		err := mailer.Notify(t.Context(), appaccess.DisabledWebhook{ID: "0192f6c4", Name: "Archive", Reason: "persistent_failure"})
		if err != nil {
			t.Fatal(err)
		}
		postman.Wait(t.Context())
	}

	notify("")
	mp.AssertNoMail(t, admin.Email)
	notify("https://flags.example.com")
	got := mp.AwaitMails(t, admin.Email, 1)
	if len(got) != 1 {
		t.Fatalf("%d mails to the instance admin, want one", len(got))
	}
}
