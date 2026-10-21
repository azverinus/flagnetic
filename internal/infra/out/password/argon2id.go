// Package password hashes members' passwords with argon2id.
package password

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/argon2"

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

// Hasher hashes passwords with its parameters.
type Hasher struct {
	params Params
}

// NewHasher returns a hasher with params.
func NewHasher(params Params) *Hasher { return &Hasher{params: params} }

// Hash returns the PHC string of password under a fresh salt:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>, both in unpadded base64.
func (h *Hasher) Hash(_ context.Context, password secret.String) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("drawing a salt: %w", err)
	}
	p := h.params
	key := argon2.IDKey([]byte(password.Reveal()), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
