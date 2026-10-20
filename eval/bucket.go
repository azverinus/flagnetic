package eval

import (
	"encoding/binary"
	"math/bits"
)

// Buckets is the number of buckets a unit is hashed into. A percentage in
// basis points (0–10000) is compared against a bucket directly.
const Buckets = 10000

// Bucket computes the bucket of a unit for a salt:
//
//	murmur3_32(utf8("{salt}.{unit_id}"), seed 0) mod 10000
//
// on the unsigned hash, giving 0–9999. A unit is inside
// percentage p when Bucket < p. The formula matches ClickHouse
// murmurHash3_32, so any bucket can be recomputed in SQL:
//
//	murmurHash3_32(concat(salt, '.', unit_id)) % 10000
func Bucket(salt, unitID string) int {
	return int(Murmur3([]byte(BucketInput(salt, unitID))) % Buckets)
}

// BucketInput is the string hashed for a unit's bucket: "{salt}.{unit_id}".
func BucketInput(salt, unitID string) string {
	return salt + "." + unitID
}

// Murmur3 is MurmurHash3 x86 32-bit with seed 0, read as an unsigned
// integer. Implementations that hash UTF-16 or read the result as signed
// produce different buckets and fail the murmur3 vectors.
func Murmur3(data []byte) uint32 {
	const (
		c1 = 0xcc9e2d51
		c2 = 0x1b873593
	)
	var h uint32
	n := len(data)
	for len(data) >= 4 {
		k := binary.LittleEndian.Uint32(data)
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
		h = bits.RotateLeft32(h, 13)
		h = h*5 + 0xe6546b64
		data = data[4:]
	}
	var k uint32
	switch len(data) {
	case 3:
		k ^= uint32(data[2]) << 16
		fallthrough
	case 2:
		k ^= uint32(data[1]) << 8
		fallthrough
	case 1:
		k ^= uint32(data[0])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
	}
	h ^= uint32(n) //nolint:gosec // murmur3 mixes the length modulo 2^32 by definition
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}
