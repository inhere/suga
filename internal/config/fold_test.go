package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestReadFoldSettings(t *testing.T) {
	t.Run("defaults: size trigger off, keep 5", func(t *testing.T) {
		loadYAML(t, "")
		s, err := ReadFoldSettings()
		assert.NoErr(t, err)
		assert.Eq(t, FoldSettings{Every: 0, Keep: 5, At: 0}, s)
	})
	t.Run("configured on", func(t *testing.T) {
		loadYAML(t, "context:\n  fold_every: 10\n  fold_keep: 2\n")
		s, err := ReadFoldSettings()
		assert.NoErr(t, err)
		assert.Eq(t, FoldSettings{Every: 10, Keep: 2, At: 0}, s)
	})
	t.Run("size trigger off", func(t *testing.T) {
		loadYAML(t, "context:\n  fold_every: 4\n  fold_at: 0\n")
		s, err := ReadFoldSettings()
		assert.NoErr(t, err)
		assert.Eq(t, FoldSettings{Every: 4, Keep: 5, At: 0}, s)
	})
	t.Run("invalid values", func(t *testing.T) {
		for doc, want := range map[string]string{
			"context:\n  fold_every: -1\n": "context.fold_every must be an integer >= 0",
			"context:\n  fold_keep: 0\n":   "context.fold_keep must be an integer >= 1",
			"context:\n  fold_keep: x\n":   "context.fold_keep must be an integer >= 1",
			"context:\n  fold_at: 1\n":     "context.fold_at must be a number >= 0 and < 1",
			"context:\n  fold_at: -0.1\n":  "context.fold_at must be a number >= 0 and < 1",
			"context:\n  fold_at: half\n":  "context.fold_at must be a number >= 0 and < 1",
		} {
			loadYAML(t, doc)
			_, err := ReadFoldSettings()
			assert.ErrMsgContains(t, err, want)
		}
	})
}
