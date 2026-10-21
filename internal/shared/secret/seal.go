package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// Purpose names what a sealed value is for. Each purpose seals under a key
// of its own, so a value sealed for one purpose never opens as another.
type Purpose string

// PurposeCanary seals the canary.
const PurposeCanary Purpose = "canary"

// Use names a key derived from the secret key for something other than
// sealing.
type Use string

// UseSessionCookie keys the HMAC signing session cookies.
const UseSessionCookie Use = "session cookie"

// keyVersion prefixes every sealed value, so values sealed under a later
// scheme or key can sit beside these and be told apart.
const keyVersion = "v1"

// canaryLength is the size of the canary's random plaintext.
const canaryLength = 32

// ErrCannotOpen is every failure to open a sealed value alike: another key,
// another purpose, a tampered value or no sealed value at all, since telling
// them apart would help whoever tampers.
var ErrCannotOpen = errors.New("the sealed value cannot be opened")

// ErrSecretKeyChanged is a secret key that does not open the canary the
// instance stored: not the key the instance was set up with.
var ErrSecretKeyChanged = errors.New("FLAGNETIC_SECRET_KEY does not open this instance's canary: " +
	"it is not the key the instance was set up with, and what the instance sealed cannot be read without that key; " +
	"start with the original key")

// MinKeyLength is the fewest bytes a secret key may have. Every key the
// keyring uses is derived from the secret key, and derivation adds no
// strength: a short key would make sealed secrets and session signatures
// guessable.
const MinKeyLength = 32

// Keyring seals and opens values, and derives keys, under the instance's
// secret key.
type Keyring struct {
	secretKey String
}

// NewKeyring returns the keyring of secretKey.
func NewKeyring(secretKey String) *Keyring { return &Keyring{secretKey: secretKey} }

// derive returns a 32-byte key for label by HKDF-SHA256 from the secret key.
func (k *Keyring) derive(label string) []byte {
	key, err := hkdf.Key(sha256.New, []byte(k.secretKey.Reveal()), nil, label, 32)
	if err != nil {
		panic("secret: deriving a key: " + err.Error()) // only a length beyond 8160 bytes fails
	}
	return key
}

// Key returns the key derived for use, the same for the same secret key.
func (k *Keyring) Key(use Use) []byte { return k.derive("flagnetic " + string(use)) }

func (k *Keyring) aead(purpose Purpose) cipher.AEAD {
	block, err := aes.NewCipher(k.derive("flagnetic secret " + keyVersion + " " + string(purpose)))
	if err != nil {
		panic("secret: " + err.Error()) // a 32-byte key is always valid
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic("secret: " + err.Error()) // AES has the block size GCM needs
	}
	return gcm
}

// Seal encrypts plaintext for purpose with AES-256-GCM under a fresh random
// nonce, and returns "v1:" and the base64url of the nonce and the ciphertext.
func (k *Keyring) Seal(purpose Purpose, plaintext []byte) (string, error) {
	gcm := k.aead(purpose)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return keyVersion + ":" + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

// Open returns the plaintext of a value sealed for purpose, or ErrCannotOpen.
func (k *Keyring) Open(purpose Purpose, sealed string) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, keyVersion+":")
	if !ok {
		return nil, ErrCannotOpen
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	gcm := k.aead(purpose)
	if err != nil || len(raw) < gcm.NonceSize() {
		return nil, ErrCannotOpen
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, ErrCannotOpen
	}
	return plaintext, nil
}

// NewCanary returns a new canary: random bytes sealed under the secret key,
// stored once by the instance and opened on every start.
func (k *Keyring) NewCanary() (string, error) {
	b := make([]byte, canaryLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return k.Seal(PurposeCanary, b)
}

// CheckCanary returns ErrSecretKeyChanged unless the secret key opens
// canary.
func (k *Keyring) CheckCanary(canary string) error {
	if _, err := k.Open(PurposeCanary, canary); err != nil {
		return ErrSecretKeyChanged
	}
	return nil
}
