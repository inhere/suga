package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
)

// TestGCCompressSpillCommand covers `suga gc --compress-spill` through the CLI:
// the dry run only reports (and still measures the compressed size), --apply
// rewrites the old spill file inside the maintenance window, and the ref keeps
// reading the same content afterwards. The ledger is an isolated temp SUGA_HOME.
func TestGCCompressSpillCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	sessionsDir, err := appconfig.ResolveSessionsDir()
	assert.Require(t, assert.NoErr(t, err))

	body := strings.Repeat("spilled output line\n", 300)
	writeSpill := func(t *testing.T, sid, stem string, age time.Duration) string {
		t.Helper()
		dir := personal.SessionOutputsDir(sessionsDir, sid)
		assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
		path := filepath.Join(dir, stem+".txt")
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(body), 0o600)))
		stamp := time.Now().Add(-age)
		if age > 0 {
			assert.Require(t, assert.NoErr(t, os.Chtimes(path, stamp, stamp)))
		}
		return path
	}
	oldPath := writeSpill(t, "sess_old", "aaaaaaaaaaaaaaaa", 30*24*time.Hour)
	writeSpill(t, "sess_new", "bbbbbbbbbbbbbbbb", 0)

	t.Run("dry run reports without changing files", func(t *testing.T) {
		out, err := runCLI(t, ctx, "gc", "--compress-spill")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "2 个文件")
		assert.StrContains(t, out, "未超额")
		assert.StrContains(t, out, "可压缩（mtime 早于 168h0m0s 且会话不活跃）：1 个")
		assert.StrContains(t, out, "out:sess_old:aaaaaaaaaaaaaaaa")
		assert.StrContains(t, out, "dry-run")
		// The file is untouched and still the plain form.
		_, err = os.Stat(oldPath)
		assert.NoErr(t, err)
	})

	t.Run("apply compresses the old file and keeps the ref readable", func(t *testing.T) {
		// The CLI takes the maintenance window for --apply: a registered suga
		// process must make it refuse instead of rewriting files underneath it.
		runDir := filepath.Join(home, "run")
		assert.Require(t, assert.NoErr(t, os.MkdirAll(runDir, 0o700)))
		// The pid must be alive: a stale record is ignored like a crashed chat.
		recordPath := filepath.Join(runDir, fmt.Sprintf("%d.json", os.Getpid()))
		record := fmt.Sprintf(`{"pid":%d,"session_id":"sess_live","socket":%q}`,
			os.Getpid(), filepath.Join(runDir, fmt.Sprintf("%d.sock", os.Getpid())))
		assert.Require(t, assert.NoErr(t, os.WriteFile(recordPath, []byte(record), 0o600)))
		_, err := runCLI(t, ctx, "gc", "--compress-spill", "--apply")
		assert.ErrMsgContains(t, err, "sess_live")
		_, err = os.Stat(oldPath)
		assert.NoErr(t, err)

		assert.Require(t, assert.NoErr(t, os.Remove(recordPath)))
		out, err := runCLI(t, ctx, "gc", "--compress-spill", "--apply")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "压缩完成：1 个文件")
		assert.StrContains(t, out, "ref 不变")
		_, err = os.Stat(oldPath)
		assert.True(t, os.IsNotExist(err), "the plain file should be gone")
		_, err = os.Stat(oldPath + ".gz")
		assert.NoErr(t, err)
		// The same ref reads the same content through the shared reader.
		r, err := personal.OpenOutput(sessionsDir, "out:sess_old:aaaaaaaaaaaaaaaa")
		assert.Require(t, assert.NoErr(t, err))
		got := make([]byte, 0, len(body))
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		assert.Require(t, assert.NoErr(t, r.Close()))
		assert.Eq(t, body, string(got))
	})

	t.Run("a configuration error is reported", func(t *testing.T) {
		_, err := runCLI(t, ctx, "gc", "--compress-spill", "extra")
		assert.ErrMsgContains(t, err, "usage")
	})
}
