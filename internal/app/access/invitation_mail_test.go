package access_test

import (
	"crypto/rand"
	"strings"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
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
		mailer := appaccess.NewInvitationMailer(postman, store, store, store, pgidentity.NewStore(pool), consoleURL)
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

// A person whose email domain is enforced cannot accept with a password, so
// the mail, whether it carries a fresh invitation or a re-issued one, tells
// them to sign in through the provider by its display name and offers no
// password; the variant follows the auth settings at the moment of sending, so
// the same person invited before the domain was enforced is offered the
// provider on a re-issue.
func TestAnInvitationToAnEnforcedDomainOffersOnlyItsProvider(t *testing.T) {
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
	postman := appaccess.NewPostman(appaccess.PostmanDeps{
		Settings: store, Opener: keyring, Mailer: mail.NewSender(renderer, testkit.MailpitRoots()), Counter: &counted{},
		After: postgres.AfterCommit{}, Clock: c, Logger: logs.Logger(), Background: appaccess.NewBackground(logs.Logger()), DefaultLocale: "en",
	})
	mailer := appaccess.NewInvitationMailer(postman, store, store, store, pgidentity.NewStore(pool), "https://flags.example.com")

	domain := strings.ToLower(rand.Text()) + ".example"
	in, err := identity.CheckProviderInput(identity.ProviderInput{
		Slug: "corp", Kind: identity.KindOIDC, DisplayName: "Corp Login", Enabled: true, Domains: []string{domain},
		OIDC: identity.OIDCInput{Issuer: "https://idp.invalid", ClientID: "flagnetic", ClientSecret: secret.New("unused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw [16]byte
	copy(raw[:], "corp-provider-id")
	if err = pgidentity.NewStore(pool).CreateProvider(t.Context(), identity.New(id.FromBytes[identity.Provider](raw), in, "sealed", c.Now())); err != nil {
		t.Fatal(err)
	}

	email := "ada@" + domain
	invite := appaccess.NewInviteMember(tx, store, testkit.Record(pool), c, ids, rand.Reader, mailer)
	res, err := invite.Handle(t.Context(), appaccess.InviteMemberCommand{Email: email, DisplayName: "Ada", Request: governance.Request{Actor: cli}})
	if err != nil {
		t.Fatal(err)
	}
	postman.Wait(t.Context())
	first := mp.AwaitMails(t, email, 1)[0]
	if strings.Contains(first.Text, "Corp Login") || !strings.Contains(first.Text, "choose a password") {
		t.Errorf("before the domain is enforced the mail should offer a password:\n%s", first.Text)
	}

	s := access.DefaultAuthSettings
	s.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: domain, Provider: "corp"}}
	if err = store.SaveAuthSettings(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	reissue := appaccess.NewReissueInvitation(tx, store, testkit.Record(pool), c, ids, rand.Reader, mailer)
	if _, err = reissue.Handle(t.Context(), res.Member.ID, governance.Request{Actor: cli}); err != nil {
		t.Fatal(err)
	}
	postman.Wait(t.Context())
	second := mp.AwaitMails(t, email, 2)[1]
	for name, part := range map[string]string{"text": second.Text, "HTML": second.HTML} {
		if !strings.Contains(part, "Corp Login") {
			t.Errorf("the enforced domain's mail %s does not name its provider:\n%s", name, part)
		}
		if strings.Contains(part, "choose a password") {
			t.Errorf("the enforced domain's mail %s offers a password:\n%s", name, part)
		}
	}
}
