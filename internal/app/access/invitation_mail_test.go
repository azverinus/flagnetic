package access_test

import (
	"crypto/rand"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Without the address the console is served at, a mailed invitation could
// only carry the address the request claimed to come to, so it is not mailed
// and its link is shown instead; the same invitation with the address is
// mailed, its link never shown.
func TestAnInvitationIsNotMailedWhenTheInstanceHasNoBaseURL(t *testing.T) {
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
	invite := func(consoleURL, email string) appaccess.IssuedInvitation {
		mailer := appaccess.NewInvitationMailer(postman, store, store, consoleURL)
		res, err := appaccess.NewInviteMember(tx, store, testkit.Record(pool), c, ids, rand.Reader, mailer).Handle(t.Context(),
			appaccess.InviteMemberCommand{Email: email, DisplayName: "Ada", Request: governance.Request{Actor: cli}})
		if err != nil {
			t.Fatal(err)
		}
		testkit.RecordSecret(res.Invitation.Secret.Reveal())
		postman.Wait(t.Context())
		return *res.Invitation
	}

	shown := uniqueAddress()
	if inv := invite("", shown); inv.Mailed || inv.Secret.IsZero() {
		t.Errorf("without a base URL: mailed %v, secret shown %v; want the link shown", inv.Mailed, !inv.Secret.IsZero())
	}
	mailed := uniqueAddress()
	if inv := invite("https://flags.example.com", mailed); !inv.Mailed || !inv.Secret.IsZero() {
		t.Errorf("with a base URL: mailed %v, secret shown %v; want it mailed and its link kept from the issuer", inv.Mailed, !inv.Secret.IsZero())
	}
	mp.AwaitMails(t, mailed, 1)
	mp.AssertNoMail(t, shown)
}
