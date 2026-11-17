package secret_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
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

const (
	purpose = secret.Purpose("test")
	row     = secret.Binding("test row 1")
)

func seal(t *testing.T, k *secret.Keyring, p secret.Purpose, plaintext string) string {
	t.Helper()
	sealed, err := k.Seal(p, []byte(plaintext), row)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestASealedValueOpensUnderTheSameKeyAndPurpose(t *testing.T) {
	for _, plaintext := range []string{"", "x", "a client secret", strings.Repeat("long ", 1000)} {
		got, err := keyring.Open(purpose, seal(t, keyring, purpose, plaintext), row)
		if err != nil || string(got) != plaintext {
			t.Errorf("Open(Seal(%.20q)) = %.20q, %v", plaintext, got, err)
		}
	}
}

func TestASealedValueCarriesTheKeyVersionAndNotThePlaintext(t *testing.T) {
	sealed := seal(t, keyring, purpose, "a client secret")
	if !strings.HasPrefix(sealed, "v2:") {
		t.Errorf("sealed value %q does not start with its key version v2:", sealed)
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
		if got, err := keyring.Open(purpose, s, row); err != nil || string(got) != "same" {
			t.Errorf("Open = %q, %v", got, err)
		}
	}
}

func TestAValueSealedUnderAnotherPurposeDoesNotOpen(t *testing.T) {
	_, err := keyring.Open("other", seal(t, keyring, purpose, "a client secret"), row)
	if !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("err = %v, want ErrCannotOpen", err)
	}
}

func TestAValueSealedUnderAnotherSecretKeyDoesNotOpen(t *testing.T) {
	_, err := other.Open(purpose, seal(t, keyring, purpose, "a client secret"), row)
	if !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("err = %v, want ErrCannotOpen", err)
	}
}

func TestATamperedValueDoesNotOpen(t *testing.T) {
	sealed := seal(t, keyring, purpose, "a client secret")
	body, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, "v2:"))
	flip := func(i int) string {
		b := bytes.Clone(body)
		b[i] ^= 1
		return "v2:" + base64.RawURLEncoding.EncodeToString(b)
	}
	for name, v := range map[string]string{
		"a ciphertext byte flipped": flip(len(body) - 1),
		"a nonce byte flipped":      flip(0),
		"truncated":                 sealed[:len(sealed)-4],
		"empty":                     "",
		"no version":                strings.TrimPrefix(sealed, "v2:"),
		"another version":           "v9:" + strings.TrimPrefix(sealed, "v2:"),
		"not base64":                "v2:!!!",
		"the version alone":         "v2:",
		"shorter than a nonce":      "v2:" + base64.RawURLEncoding.EncodeToString(body[:5]),
	} {
		if _, err := keyring.Open(purpose, v, row); !errors.Is(err, secret.ErrCannotOpen) {
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

func TestAValueSealedForOneRowDoesNotOpenForAnother(t *testing.T) {
	sealed := seal(t, keyring, purpose, "a client secret")
	if _, err := keyring.Open(purpose, sealed, "test row 2"); !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("err = %v, want ErrCannotOpen", err)
	}
	if _, err := keyring.Open(purpose, sealed, ""); !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("no row: err = %v, want ErrCannotOpen", err)
	}
}

func TestAValueSealedBeforeRowsWereBoundStillOpens(t *testing.T) {
	// "v1:" is the form sealed values had before they were bound to a row.
	legacy := "v1:" + legacyBody(t, keyring, purpose, "a client secret")
	for _, r := range []secret.Binding{row, "another row", ""} {
		got, err := keyring.Open(purpose, legacy, r)
		if err != nil || string(got) != "a client secret" {
			t.Errorf("Open(legacy, %q) = %q, %v", r, got, err)
		}
	}
}

// legacyBody seals plaintext the way values were sealed before they were bound
// to a row: AES-256-GCM under the purpose's key, with no additional data.
func legacyBody(t *testing.T, _ *secret.Keyring, p secret.Purpose, plaintext string) string {
	t.Helper()
	key, err := hkdf.Key(sha256.New, []byte("0123456789abcdef0123456789abcdef"), nil, "flagnetic secret v1 "+string(p), 32)
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
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

func TestRebindingBindsAValueSealedBeforeRowsWereBound(t *testing.T) {
	legacy := "v1:" + legacyBody(t, keyring, purpose, "a client secret")
	bound, err := keyring.Rebind(purpose, legacy, row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bound, "v2:") {
		t.Fatalf("rebound value %q is not in the bound version", bound)
	}
	if got, err := keyring.Open(purpose, bound, row); err != nil || string(got) != "a client secret" {
		t.Errorf("Open(rebound) = %q, %v", got, err)
	}
	if _, err := keyring.Open(purpose, bound, "another row"); !errors.Is(err, secret.ErrCannotOpen) {
		t.Errorf("another row: err = %v, want ErrCannotOpen", err)
	}
}

func TestRebindingLeavesOtherValuesAsTheyAre(t *testing.T) {
	bound := seal(t, keyring, purpose, "a client secret")
	for name, v := range map[string]string{
		"already bound":   bound,
		"empty":           "",
		"does not open":   "v1:" + legacyBody(t, keyring, "other", "a client secret"),
		"another version": "v9:abc",
	} {
		got, err := keyring.Rebind(purpose, v, row)
		if err != nil || got != v {
			t.Errorf("%s: Rebind = %q, %v, want it unchanged", name, got, err)
		}
	}
}
