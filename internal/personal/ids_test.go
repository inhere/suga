package personal

import (
	"regexp"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestNewSessionIDFormat locks the random id format: a "sg_" prefix and 12
// base32 characters, so nothing but the prefix is shared by two ids (the
// 8-character form a client shows must identify one session).
func TestNewSessionIDFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^sg_[0-9a-v]{12}$`)
	t.Run("session id", func(t *testing.T) {
		id := NewSessionID()
		assert.Eq(t, 15, len(id))
		assert.Eq(t, true, pattern.MatchString(id))
		assert.NoErr(t, validateSessionID(id))
		assert.Eq(t, true, NewSessionID() != id)
	})
	t.Run("turn id", func(t *testing.T) {
		id := NewTurnID()
		assert.Eq(t, 14, len(id))
		assert.Eq(t, true, regexp.MustCompile(`^t_[0-9a-v]{12}$`).MatchString(id))
		assert.Eq(t, true, NewTurnID() != id)
	})
	t.Run("old ids stay valid", func(t *testing.T) {
		// Sessions and turns created by older versions keep working: reading an
		// id never assumes the new prefix.
		assert.NoErr(t, validateSessionID("sess_261020_1iu81hh7i8v0014"))
		assert.NoErr(t, validateSessionID("s_3k9m2q1p0v7r"))
		assert.NoErr(t, validateSessionID("t_3k9m2q1p0v7r"))
		assert.NoErr(t, validateSessionID("custom-id"))
	})
}

// TestNewIDUnique checks a large batch for duplicates: 1e5 ids of 60 random
// bits must all differ.
func TestNewIDUnique(t *testing.T) {
	const n = 100000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewSessionID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate session id %q after %d ids", id, i)
		}
		seen[id] = struct{}{}
	}
	assert.Eq(t, n, len(seen))
}

// TestNewIDShortenedFormIsDistinct: a shortened id such as the 8 characters
// gofer shows must tell the sessions created one after another apart (the old
// time-ordered ids shared their first 16 characters within one day).
func TestNewIDShortenedFormIsDistinct(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		short := NewSessionID()[:8]
		if _, dup := seen[short]; dup {
			t.Fatalf("shortened session id %q repeated after %d ids", short, i)
		}
		seen[short] = struct{}{}
	}
	assert.Eq(t, n, len(seen))
}
