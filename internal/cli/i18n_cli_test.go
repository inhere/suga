package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

func TestCLIEnglishUserFacingOutput(t *testing.T) {
	t.Setenv("SUGA_LANGUAGE", "en")

	t.Run("config set and unset", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/test\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "language", "en"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "Written")
		stdout.Reset()
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "unset", "language"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "Removed language")
	})

	t.Run("data usage title", func(t *testing.T) {
		var out bytes.Buffer
		err := writeDataUsage(&out, store.UsageReport{})
		assert.NoErr(t, err)
		assert.StrContains(t, out.String(), "Database file")
	})

	t.Run("undo no snapshot", func(t *testing.T) {
		assert.Eq(t, "No snapshot to roll back (this session changed no file with a tool, or the snapshots were cleaned by tools.snapshots).", renderUndoCheckpoints(nil))
	})

	t.Run("send image error", func(t *testing.T) {
		_, err := sendImageRefs(process.Record{SessionID: "s-en", PID: 7}, []string{"picture.png"})
		assert.ErrMsgContains(t, err, "does not support images")
	})

	t.Run("cwd mismatch", func(t *testing.T) {
		err := cwdMismatchError("s-en", t.TempDir()+"/missing", t.TempDir())
		assert.ErrMsgContains(t, err, "current working directory")
	})
}
