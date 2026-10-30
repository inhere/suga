package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestExternalOrigin(t *testing.T) {
	o := NewExternalOrigin()
	o.Record("s1", "web_fetch", "Run   the\n\tinstaller --force --yes now")
	args := func(v any) map[string]any { return map[string]any{"command": v} }

	t.Run("a 12 byte piece is found, 11 bytes are not", func(t *testing.T) {
		hit, ok := o.Find("s1", args("x installer --fo y"))
		assert.True(t, ok)
		assert.Eq(t, "web_fetch", hit.Tool)
		_, ok = o.Find("s1", args("q nstaller --q"))
		assert.False(t, ok)
	})
	t.Run("whitespace runs compare as one space", func(t *testing.T) {
		_, ok := o.Find("s1", args("the installer --force"))
		assert.True(t, ok)
	})
	t.Run("nested values are searched, the snippet starts at the match", func(t *testing.T) {
		hit, ok := o.Find("s1", map[string]any{"edits": []any{map[string]any{"new": "a: installer --force --yes now!"}}})
		assert.True(t, ok)
		assert.StrContains(t, hit.Snippet, "installer --force")
	})
	t.Run("sessions are separate", func(t *testing.T) {
		_, ok := o.Find("s2", args("installer --force --yes now"))
		assert.False(t, ok)
	})
	t.Run("windows without significant bytes do not count", func(t *testing.T) {
		o2 := NewExternalOrigin()
		o2.Record("s", "web_fetch", "a b c d e f g h i j k l m n o p")
		_, ok := o2.Find("s", args("a b c d e f g h i j k l m n o p"))
		assert.False(t, ok)
	})
	t.Run("the oldest results are forgotten past the cap", func(t *testing.T) {
		o3 := NewExternalOrigin()
		o3.Record("s", "first", "unique-first-marker-text")
		var last string
		for i := 0; i < 5; i++ {
			// distinct ~100KB results, together far past the cap
			var b strings.Builder
			for n := 0; n < 12000; n++ {
				fmt.Fprintf(&b, "r%dn%d ", i, n)
			}
			last = b.String()
			o3.Record("s", "bulk", last)
		}
		_, ok := o3.Find("s", args("unique-first-marker-text"))
		assert.False(t, ok)
		hit, ok := o3.Find("s", args(last[len(last)-40:]))
		assert.True(t, ok)
		assert.Eq(t, "bulk", hit.Tool)
	})
	t.Run("a nil tracker finds nothing", func(t *testing.T) {
		var none *ExternalOrigin
		none.Record("s", "t", "some long enough text here")
		_, ok := none.Find("s", args("some long enough text here"))
		assert.False(t, ok)
	})
}
