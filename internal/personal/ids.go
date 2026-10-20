package personal

import (
	"crypto/rand"
	"io"
)

// idRandomLen is the number of random base32 characters in an id: 12 chars of
// 5 bits each = 60 random bits (about 1 in 1.15e18), so two ids never share
// more than the prefix and the random part carries no creation time.
const idRandomLen = 12

// newID builds a prefixed random id (prefix + 12 base32 characters).
func newID(prefix string) string {
	return prefix + randomBase32(idRandomLen)
}

// NewSessionID returns a fresh session id: "s_" + 12 random base32 characters,
// e.g. s_3k9m2q1p0v7r. The whole id is random (no local date, timestamp or
// sequence): every id is unique-looking from its first characters, so a
// shortened form such as the 8 characters gofer shows identifies one session
// instead of a day's worth of them. Ids created by older versions ("sess_" +
// date + timestamp + sequence) stay valid everywhere an id is read.
func NewSessionID() string { return newID("s_") }

// NewTurnID returns a fresh turn id ("t_" + 12 random characters); it doubles
// as the run id.
func NewTurnID() string { return newID("t_") }

const base32Alphabet = "0123456789abcdefghijklmnopqrstuv"

// randomBase32 returns n random characters of base32Alphabet (crypto/rand).
// Every byte contributes its low 5 bits, so the result is uniform over the
// alphabet; a failure of the system random source is fatal by design.
func randomBase32(n int) string {
	raw := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		panic(err)
	}
	buf := make([]byte, n)
	for i, b := range raw {
		buf[i] = base32Alphabet[int(b)&31]
	}
	return string(buf)
}
