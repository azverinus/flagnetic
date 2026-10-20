// Package id is the identifiers of every entity: UUIDv7 values minted by the
// application before the domain sees them, typed per entity.
//
// Each entity declares its identifier type in its domain package,
//
//	type ProjectID = id.Of[Project]
//
// so passing one entity's identifier where another's is expected does not
// compile. Application handlers mint identifiers from a Source they receive,
// which reads the application clock: identifiers are time-ordered, so index
// inserts stay local and keyset cursors follow creation order. The creation
// time of a row is therefore readable from its key, which is not a secret.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// Of is the identifier of an entity of type T. The zero value is no
// identifier.
type Of[T any] struct {
	b [16]byte
}

// New mints an identifier of T from src.
func New[T any](src *Source) Of[T] {
	return Of[T]{b: src.next()}
}

// Parse reads the canonical hyphenated form, in either case.
func Parse[T any](s string) (Of[T], error) {
	var out Of[T]
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return out, fmt.Errorf("identifier %q is not of the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", s)
	}
	hexDigits := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(out.b[:], []byte(hexDigits)); err != nil {
		return Of[T]{}, fmt.Errorf("identifier %q is not hexadecimal", s)
	}
	return out, nil
}

// MustParse is Parse for identifiers known to be well formed, such as those in
// tests; it panics on any other.
func MustParse[T any](s string) Of[T] {
	v, err := Parse[T](s)
	if err != nil {
		panic(err)
	}
	return v
}

// FromBytes returns the identifier held in b, as read from a uuid column.
func FromBytes[T any](b [16]byte) Of[T] {
	return Of[T]{b: b}
}

// Bytes returns the identifier's 16 bytes, as written to a uuid column.
func (i Of[T]) Bytes() [16]byte { return i.b }

// IsZero reports whether i is no identifier.
func (i Of[T]) IsZero() bool { return i.b == [16]byte{} }

// String returns the canonical form: lowercase hyphenated hexadecimal.
func (i Of[T]) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], i.b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], i.b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], i.b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], i.b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:], i.b[10:])
	return string(buf[:])
}

// Time returns the millisecond the identifier was minted at.
func (i Of[T]) Time() time.Time {
	var ms [8]byte
	copy(ms[2:], i.b[:6])
	return time.UnixMilli(int64(binary.BigEndian.Uint64(ms[:]))) //nolint:gosec // 48 bits always fit
}

// MarshalText writes the canonical form.
func (i Of[T]) MarshalText() ([]byte, error) { return []byte(i.String()), nil }

// UnmarshalText reads the canonical form.
func (i *Of[T]) UnmarshalText(b []byte) error {
	v, err := Parse[T](string(b))
	if err != nil {
		return err
	}
	*i = v
	return nil
}

// Source mints UUIDv7 values (RFC 9562) from a clock. Within one millisecond,
// and when the clock steps back, it increments the random part of the last
// value instead of drawing a new one, so one process's identifiers strictly
// increase. It is safe for concurrent use.
type Source struct {
	clock clock.Clock
	fill  func([]byte)

	mu     sync.Mutex
	lastMS uint64
	// randA is the 12-bit field after the version, randB the 62 bits after the
	// variant; together they are the counter incremented within a millisecond.
	randA uint16
	randB uint64
}

const (
	maxRandA = 1<<12 - 1
	maxRandB = 1<<62 - 1
)

// NewSource returns a source drawing its random bits from crypto/rand.
func NewSource(c clock.Clock) *Source {
	return &Source{clock: c, fill: func(b []byte) { _, _ = rand.Read(b) }}
}

// NewDeterministicSource returns a source for tests whose random bits start at
// zero in every millisecond, so the same clock readings always give the same
// identifiers. They are still valid UUIDv7 and still strictly increase.
func NewDeterministicSource(c clock.Clock) *Source {
	return &Source{clock: c, fill: func(b []byte) { clear(b) }}
}

func (s *Source) next() [16]byte {
	ms := uint64(max(s.clock.Now().UnixMilli(), 0)) //nolint:gosec // clamped to non-negative

	s.mu.Lock()
	if ms > s.lastMS {
		var r [10]byte
		s.fill(r[:])
		s.lastMS = ms
		s.randA = binary.BigEndian.Uint16(r[0:2]) & maxRandA
		s.randB = binary.BigEndian.Uint64(r[2:10]) & maxRandB
	} else {
		s.increment()
	}
	ms, randA, randB := s.lastMS, s.randA, s.randB
	s.mu.Unlock()

	var b [16]byte
	var msBytes [8]byte
	binary.BigEndian.PutUint64(msBytes[:], ms)
	copy(b[0:6], msBytes[2:])
	binary.BigEndian.PutUint16(b[6:8], 0x7000|randA)
	binary.BigEndian.PutUint64(b[8:16], 0x8000000000000000|randB)
	return b
}

// increment adds one to the 74-bit counter, carrying into the millisecond when
// it overflows so the order still holds.
func (s *Source) increment() {
	if s.randB < maxRandB {
		s.randB++
		return
	}
	s.randB = 0
	if s.randA < maxRandA {
		s.randA++
		return
	}
	s.randA = 0
	s.lastMS++
}
