package flagnetic

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newEventID returns the event ID of one event: a lowercase UUIDv7 whose
// first 48 bits are at in Unix milliseconds, so the platform reads the
// event's time from the ID itself and drops a retry as a duplicate.
//
// crypto/rand.Read never fails on the platforms Go supports, so a caller
// gets an ID it can always send.
func newEventID(at time.Time) string {
	ms := at.UnixMilli()
	if ms < 0 {
		ms = 0
	}
	entropy := make([]byte, 10)
	_, _ = rand.Read(entropy)
	b := [16]byte{
		byte(ms >> 40), byte(ms >> 32), byte(ms >> 24),
		byte(ms >> 16), byte(ms >> 8), byte(ms),
		entropy[0]&0x0f | 0x70, // version 7
		entropy[1],
		entropy[2]&0x3f | 0x80, // the RFC 4122 variant
		entropy[3], entropy[4], entropy[5],
		entropy[6], entropy[7], entropy[8], entropy[9],
	}
	return formatUUID(b)
}

// formatUUID writes the canonical 8-4-4-4-12 form in lowercase hex.
func formatUUID(b [16]byte) string {
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
