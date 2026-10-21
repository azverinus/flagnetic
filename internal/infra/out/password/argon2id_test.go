package password_test

import (
	"regexp"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

func TestAPasswordIsHashedAsAnArgon2idPHCStringUnderAFreshSalt(t *testing.T) {
	h := password.NewHasher(password.DefaultParams)
	a, err := h.Hash(t.Context(), secret.New("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	phc := regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !phc.MatchString(a) {
		t.Errorf("hash %q is not an argon2id PHC string with a 16-byte salt and a 32-byte key", a)
	}
	b, err := h.Hash(t.Context(), secret.New("correct horse battery staple"))
	if err != nil || a == b {
		t.Errorf("hashing twice gave %q and %q, want different salts", a, b)
	}
}
