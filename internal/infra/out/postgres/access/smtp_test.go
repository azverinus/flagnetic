package access_test

import (
	"errors"
	"strings"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestTheSMTPSettingsCacheTrustsItsCopyOnlyWhileListening(t *testing.T) {
	pool := testkit.FreshPool(t)
	store, cache := pgaccess.NewStore(pool), pgaccess.NewSMTPSettingsCache(pool)
	save := func(host string) {
		t.Helper()
		err := store.SaveSMTPSettings(t.Context(), access.SMTPSettings{
			Host: host, Port: 25, Security: access.SMTPNone, From: "flags@example.com", UpdatedAt: testkit.Start,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read := func(step, want string) {
		t.Helper()
		got, err := cache.SMTPSettings(t.Context())
		switch {
		case want == "" && errors.Is(err, appaccess.ErrSMTPNotConfigured):
		case err != nil:
			t.Errorf("%s: %v", step, err)
		case got.Host != want:
			t.Errorf("%s: host = %q, want %q", step, got.Host, want)
		}
	}

	read("fresh", "")
	save("one.example.com")
	read("before listening, a change unannounced", "one.example.com")

	cache.Listening(true)
	read("listening", "one.example.com")
	save("two.example.com")
	read("listening, before the notification arrives", "one.example.com")
	cache.Received("")
	read("after the notification", "two.example.com")

	if err := store.DeleteSMTPSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	read("a removal not yet announced", "two.example.com")
	cache.Forget()
	read("after this replica's own removal", "")
	read("and again from the copy", "")
}

func TestTheSMTPPasswordIsStoredSealedAndOnlyThisInstanceOpensIt(t *testing.T) {
	pool := testkit.FreshPool(t)
	c := testkit.Clock()
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	store := pgaccess.NewStore(pool)
	const password = "relay-password-9031"
	testkit.RecordSecret(password)
	_, err := appaccess.NewConfigureSMTP(appaccess.ConfigureSMTPDeps{
		Tx: postgres.NewTransactor(pool), Store: store, Cache: pgaccess.NewSMTPSettingsCache(pool), Sealer: keyring,
		Audit: testkit.Record(pool), Clock: c, IDs: testkit.IDs(c), BaseURLSet: true,
	}).Handle(t.Context(), access.SMTPChange{
		Host: "smtp.example.com", Port: 587, Security: access.SMTPStartTLS, Username: "relay", Password: new(password),
		From: "flags@example.com",
	}, governance.Request{Actor: governance.CLIActor("test")})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.SMTPSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !got.PasswordSet() || strings.Contains(got.PasswordSealed, password) || !strings.HasPrefix(got.PasswordSealed, "v1:") {
		t.Errorf("stored password %q, want it sealed", got.PasswordSealed)
	}
	opened, err := keyring.Open(secret.PurposeSMTPPassword, got.PasswordSealed)
	if err != nil || string(opened) != password {
		t.Errorf("the instance opens %q, %v; want the password", opened, err)
	}
	if _, err := keyring.Open(secret.PurposeWebhookSecret, got.PasswordSealed); err == nil {
		t.Error("the SMTP password opens as a webhook secret")
	}
}
