package password_test

import (
	"regexp"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/azverinus/flagnetic/internal/infra/out/password"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

func TestAPasswordIsHashedAsAnArgon2idPHCStringUnderAFreshSalt(t *testing.T) {
	h := password.NewHasher(password.DefaultParams)
	a, err := h.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291"))
	if err != nil {
		t.Fatal(err)
	}
	phc := regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !phc.MatchString(a) {
		t.Errorf("hash %q is not an argon2id PHC string with a 16-byte salt and a 32-byte key", a)
	}
	b, err := h.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291"))
	if err != nil || a == b {
		t.Errorf("hashing twice gave %q and %q, want different salts", a, b)
	}
}

func TestAPasswordVerifiesAgainstItsHashAndNoOther(t *testing.T) {
	h := password.NewHasher(password.DefaultParams)
	encoded, err := h.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, outdated, err := h.Verify(t.Context(), secret.New("lantern-quartz-meadow-2291"), encoded); !ok || outdated || err != nil {
		t.Errorf("right password: ok %v, outdated %v, err %v; want ok and current", ok, outdated, err)
	}
	if ok, _, err := h.Verify(t.Context(), secret.New("lantern-quartz-meadow-2292"), encoded); ok || err != nil {
		t.Errorf("wrong password: ok %v, err %v; want refused", ok, err)
	}
	if ok, _, err := h.Verify(t.Context(), secret.New(""), ""); ok || err != nil {
		t.Errorf("no hash: ok %v, err %v; want refused without an error", ok, err)
	}
	if _, _, err := h.Verify(t.Context(), secret.New("x"), "$2a$10$bcrypt"); err == nil {
		t.Error("a hash of another scheme verified without an error")
	}
}

func TestAHashUnderOlderParametersIsOutdated(t *testing.T) {
	older := password.NewHasher(password.Params{MemoryKiB: 19456, Time: 2, Threads: 1, SaltLength: 16, KeyLength: 32})
	encoded, err := older.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291"))
	if err != nil {
		t.Fatal(err)
	}
	ok, outdated, err := password.NewHasher(password.DefaultParams).Verify(t.Context(), secret.New("lantern-quartz-meadow-2291"), encoded)
	if !ok || !outdated || err != nil {
		t.Errorf("ok %v, outdated %v, err %v; want verified and outdated", ok, outdated, err)
	}
}

func TestABoundedHasherAnswersBusyWhileEverySlotIsHeld(t *testing.T) {
	slots := semaphore.NewWeighted(2)
	h := password.NewBoundedHasher(password.DefaultParams, slots, 50*time.Millisecond)
	if err := slots.Acquire(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := h.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291"))
	if e, ok := apperrors.From(err); !ok || e.Code != "busy" || e.Kind != apperrors.KindUnavailable || e.RetryAfter != time.Second {
		t.Fatalf("hash with every slot held: %v, want unavailable busy retrying after a second", err)
	}
	if waited := time.Since(start); waited < 50*time.Millisecond {
		t.Errorf("answered busy after %v, want after the wait for a slot", waited)
	}
	if _, _, err := h.Verify(t.Context(), secret.New("x"), ""); !apperrors.IsKind(err, apperrors.KindUnavailable) {
		t.Errorf("verify with every slot held: %v, want busy", err)
	}
	slots.Release(1)
	if _, err := h.Hash(t.Context(), secret.New("lantern-quartz-meadow-2291")); err != nil {
		t.Errorf("hash once a slot is free: %v", err)
	}
	slots.Release(1)
}
