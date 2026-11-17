package access_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
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
	if !got.PasswordSet() || strings.Contains(got.PasswordSealed, password) || !strings.HasPrefix(got.PasswordSealed, "v2:") {
		t.Errorf("stored password %q, want it sealed", got.PasswordSealed)
	}
	opened, err := keyring.Open(secret.PurposeSMTPPassword, got.PasswordSealed, "smtp_settings")
	if err != nil || string(opened) != password {
		t.Errorf("the instance opens %q, %v; want the password", opened, err)
	}
	if _, err := keyring.Open(secret.PurposeWebhookSecret, got.PasswordSealed, "smtp_settings"); err == nil {
		t.Error("the SMTP password opens as a webhook secret")
	}
}

func TestAnSMTPPasswordSealedBeforeRowsWereBoundIsBoundByTheNextWriteOfItsRow(t *testing.T) {
	pool := testkit.FreshPool(t)
	c := testkit.Clock()
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	store := pgaccess.NewStore(pool)
	const password = "relay-password-4417"
	testkit.RecordSecret(password)
	err := store.SaveSMTPSettings(t.Context(), access.SMTPSettings{
		Host: "smtp.example.com", Port: 587, Security: access.SMTPStartTLS, Username: "relay",
		PasswordSealed: unboundSeal(t, secret.PurposeSMTPPassword, password), From: "flags@example.com", UpdatedAt: testkit.Start,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = appaccess.NewConfigureSMTP(appaccess.ConfigureSMTPDeps{
		Tx: postgres.NewTransactor(pool), Store: store, Cache: pgaccess.NewSMTPSettingsCache(pool), Sealer: keyring,
		Audit: testkit.Record(pool), Clock: c, IDs: testkit.IDs(c), BaseURLSet: true,
	}).Handle(t.Context(), access.SMTPChange{
		Host: "smtp.example.com", Port: 587, Security: access.SMTPStartTLS, Username: "relay", From: "release@example.com",
	}, governance.Request{Actor: governance.CLIActor("test")})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.SMTPSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.PasswordSealed, "v2:") {
		t.Fatalf("stored password %q, want it sealed again, bound", got.PasswordSealed)
	}
	if opened, err := keyring.Open(secret.PurposeSMTPPassword, got.PasswordSealed, "smtp_settings"); err != nil || string(opened) != password {
		t.Errorf("the instance opens %q, %v; want the password", opened, err)
	}
	if _, err := keyring.Open(secret.PurposeSMTPPassword, got.PasswordSealed, "another row"); err == nil {
		t.Error("the rebound password opens for another row")
	}
}

// unboundSeal seals plaintext for purpose under the test secret key the way
// values were sealed before they were bound to a row: "v1:" and AES-256-GCM
// with no additional data.
func unboundSeal(t *testing.T, purpose secret.Purpose, plaintext string) string {
	t.Helper()
	key, err := hkdf.Key(sha256.New, []byte(testkit.SecretKey), nil, "flagnetic secret v1 "+string(purpose), 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	return "v1:" + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}
