package secret_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/shared/secret"
)

var (
	keyring = secret.NewKeyring(secret.New("0123456789abcdef0123456789abcdef"))
	other   = secret.NewKeyring(secret.New("fedcba9876543210fedcba9876543210"))
)

const purpose = secret.Purpose("test")

func seal(t *testing.T, k *secret.Keyring, p secret.Purpose, plaintext string) string {
	t.Helper()
	sealed, err := k.Seal(p, []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestASealedValueOpensUnderTheSameKeyAndPurpose(t *testing.T) {
	for _, plaintext := range []string{"", "x", "a client secret", strings.Repeat("long ", 1000)} {
		got, err := keyring.Open(purpose, seal(t, keyring, purpose, plaintext))
		if err != nil || string(got) != plaintext {
			t.Errorf("Open(Seal(%.20q)) = %.20q, %v", plaintext, got, err)
		}
	}
}

func TestASealedValueCarriesTheKeyVersionAndNotThePlaintext(t *testing.T) {
	sealed := seal(t, keyring, purpose, "a client secret")
	if !strings.HasPrefix(sealed, "v1:") {
		t.Errorf("sealed value %q does not start with its key version v1:", sealed)
	}
	if strings.Contains(sealed, "a client secret") || strings.Contains(sealed, base64.RawURLEncoding.EncodeToString([]byte("a client secret"))) {
		t.Errorf("sealed value %q carries the plaintext", sealed)
	}
}

func TestEverySealTakesAFreshNonce(t *testing.T) {
	a, b := seal(t, keyring, purpose, "same"), seal(t, keyring, purpose, "same")
	if a == b {
		t.Fatal("sealing the same value twice gave the same output")
	}
	for _, s := range []string{a, b} {
		if got, err := keyring.Open(purpose, s); err != nil || string(got) != "same" {
			t.Errorf("Open = %q, %v", got, err)
		}
	}
}

func TestAValueSealedUnderAnotherPurposeDoesNotOpen(t *testing.T) {
	_, err := keyring.Open("other", seal(t, keyring, purpose, "a client secret"))
	if !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("err = %v, want ErrCannotOpen", err)
	}
}

func TestAValueSealedUnderAnotherSecretKeyDoesNotOpen(t *testing.T) {
	_, err := other.Open(purpose, seal(t, keyring, purpose, "a client secret"))
	if !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("err = %v, want ErrCannotOpen", err)
	}
}

func TestATamperedValueDoesNotOpen(t *testing.T) {
	sealed := seal(t, keyring, purpose, "a client secret")
	body, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	flip := func(i int) string {
		b := bytes.Clone(body)
		b[i] ^= 1
		return "v1:" + base64.RawURLEncoding.EncodeToString(b)
	}
	for name, v := range map[string]string{
		"a ciphertext byte flipped": flip(len(body) - 1),
		"a nonce byte flipped":      flip(0),
		"truncated":                 sealed[:len(sealed)-4],
		"empty":                     "",
		"no version":                strings.TrimPrefix(sealed, "v1:"),
		"another version":           "v2:" + strings.TrimPrefix(sealed, "v1:"),
		"not base64":                "v1:!!!",
		"the version alone":         "v1:",
		"shorter than a nonce":      "v1:" + base64.RawURLEncoding.EncodeToString(body[:5]),
	} {
		if _, err := keyring.Open(purpose, v); !errors.Is(err, secret.ErrCannotOpen) {
			t.Errorf("%s: err = %v, want ErrCannotOpen", name, err)
		}
	}
}

func TestAKeyIsDerivedPerUseFromTheSecretKey(t *testing.T) {
	a := keyring.Key(secret.UseSessionCookie)
	if len(a) != 32 {
		t.Fatalf("key of %d bytes, want 32", len(a))
	}
	if !bytes.Equal(a, secret.NewKeyring(secret.New("0123456789abcdef0123456789abcdef")).Key(secret.UseSessionCookie)) {
		t.Error("the same secret key derived another key")
	}
	if bytes.Equal(a, other.Key(secret.UseSessionCookie)) || bytes.Equal(a, keyring.Key("another use")) {
		t.Error("another secret key or another use derived the same key")
	}
}

func TestTheCanaryOpensOnlyUnderItsSecretKey(t *testing.T) {
	canary, err := keyring.NewCanary()
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.CheckCanary(canary); err != nil {
		t.Errorf("CheckCanary under its key = %v", err)
	}
	if err := other.CheckCanary(canary); !errors.Is(err, secret.ErrSecretKeyChanged) {
		t.Errorf("CheckCanary under another key = %v, want ErrSecretKeyChanged", err)
	}
	again, _ := keyring.NewCanary()
	if again == canary {
		t.Error("two canaries are the same")
	}
}
