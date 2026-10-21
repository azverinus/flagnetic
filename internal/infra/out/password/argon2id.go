// Package password hashes members' passwords with argon2id.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/sync/semaphore"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Params are the argon2id parameters of a hash.
type Params struct {
	// MemoryKiB is the memory one hash takes.
	MemoryKiB uint32
	// Time is the number of passes over the memory.
	Time uint32
	// Threads is the parallelism.
	Threads uint8
	// SaltLength and KeyLength are in bytes.
	SaltLength uint32
	KeyLength  uint32
}

// DefaultParams cost about a tenth of a second on a server core: slow enough
// that a stolen hash resists guessing, fast enough for a sign-in.
var DefaultParams = Params{MemoryKiB: 64 * 1024, Time: 3, Threads: 2, SaltLength: 16, KeyLength: 32}

// Hasher hashes passwords with its parameters, at most a bounded number at
// once when it was given a bound.
type Hasher struct {
	params Params
	// slots bound the hashes and verifications running at once; nil for
	// none.
	slots *semaphore.Weighted
	wait  time.Duration
}

// NewHasher returns a hasher with params and no bound, for a command that
// hashes one password.
func NewHasher(params Params) *Hasher { return &Hasher{params: params} }

// SlotWait is how long a hash waits for a slot of a bounded hasher before it
// is answered busy, and BusyRetryAfter how long a client is then told to
// wait.
const (
	SlotWait       = 2 * time.Second
	BusyRetryAfter = time.Second
)

// NewBoundedHasher returns a hasher with params running at most slots
// hashes and verifications at once. Each takes its parameters' memory, 64
// MiB by default, so the bound keeps a burst of sign-ins from exhausting a
// replica's memory; one that waits longer than wait for a slot is answered
// busy.
func NewBoundedHasher(params Params, slots int, wait time.Duration) *Hasher {
	return &Hasher{params: params, slots: semaphore.NewWeighted(int64(slots)), wait: wait}
}

// ErrBusy answers a hash that found no slot in time: the replica is hashing
// as many passwords as it may, and the client retries shortly.
var ErrBusy = apperrors.Unavailable("busy", "too many sign-ins are being checked at once; retry shortly").WithRetryAfter(BusyRetryAfter)

// Occupy takes a slot as a hash would and returns its release, so work of
// the caller's own counts against the bound; a test holds slots this way to
// see the bound answer.
func (h *Hasher) Occupy(ctx context.Context) (release func(), err error) {
	if h.slots == nil {
		return func() {}, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.wait)
	defer cancel()
	if err := h.slots.Acquire(waitCtx, 1); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrBusy
	}
	return func() { h.slots.Release(1) }, nil
}

// Hash returns the PHC string of password under a fresh salt:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>, both in unpadded base64.
func (h *Hasher) Hash(ctx context.Context, password secret.String) (string, error) {
	release, err := h.Occupy(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("drawing a salt: %w", err)
	}
	p := h.params
	key := argon2.IDKey([]byte(password.Reveal()), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// dummyHash is verified when a sign-in names no member with a password, so
// an unknown email costs what a wrong password costs and its answer comes
// as late. It is a hash under DefaultParams of a password nobody knows.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$tQVcfxmY/balghkwoy1BJQ$" +
	"M/BbWCE6342c5sEzYt67mT6N4Qg5yCmn23tCV4NsQ18"

// errMalformedHash is a stored hash that is no argon2id PHC string.
var errMalformedHash = errors.New("password hash is not an argon2id PHC string")

// Verify reports whether password matches the PHC string encoded, and
// whether encoded was made under parameters other than the hasher's, so a
// successful sign-in rehashes it. An empty encoded is verified against a
// hash no password matches, at the same cost.
func (h *Hasher) Verify(ctx context.Context, password secret.String, encoded string) (ok, outdated bool, err error) {
	release, err := h.Occupy(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	if encoded == "" {
		encoded = dummyHash
	}
	p, salt, key, err := parsePHC(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password.Reveal()), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(key))) //nolint:gosec // a PHC key is far below 4 GiB
	ok = subtle.ConstantTimeCompare(got, key) == 1
	outdated = p.MemoryKiB != h.params.MemoryKiB || p.Time != h.params.Time || p.Threads != h.params.Threads ||
		uint32(len(salt)) != h.params.SaltLength || uint32(len(key)) != h.params.KeyLength //nolint:gosec // lengths of a PHC string
	return ok, outdated, nil
}

// parsePHC reads the parameters, salt and key of an argon2id PHC string.
func parsePHC(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errMalformedHash
	}
	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil || version != argon2.Version {
		return p, nil, nil, errMalformedHash
	}
	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &p.Threads); scanErr != nil ||
		p.MemoryKiB == 0 || p.Time == 0 || p.Threads == 0 {
		return p, nil, nil, errMalformedHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return p, nil, nil, errMalformedHash
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(key) == 0 {
		return p, nil, nil, errMalformedHash
	}
	return p, salt, key, nil
}
