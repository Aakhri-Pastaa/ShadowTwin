package collectors

import (
	"crypto/rand"
	"encoding/hex"
)

// newUUID returns a random (version 4, variant 1) UUID in the canonical
// 8-4-4-4-12 hex form.
//
// We generate it ourselves from crypto/rand rather than pull in a dependency:
// the host agent runs with elevated privilege, and ADR-0002 commits us to owning
// and auditing every line of it, so a ~10-line UUID generator beats a third-party
// module here. crypto/rand failing means the OS CSPRNG is unavailable — an event
// with a predictable or empty ID would silently break idempotency, so we fail
// loud rather than emit one.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("host-agent: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10x

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
