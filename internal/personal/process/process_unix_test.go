//go:build !windows

package process

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestProcessExistsUnix(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatal("current process should exist")
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run true: %v", err)
	}
	if processExists(cmd.Process.Pid) {
		t.Fatalf("exited child %d should not exist", cmd.Process.Pid)
	}
	for _, pid := range []int{0, -1, math.MaxInt32 + 1, math.MaxInt64} {
		if processExists(pid) {
			t.Fatalf("invalid pid %d should not exist", pid)
		}
	}
}

// TestValidateSocketPathUnix pins the AF_UNIX path limit: sockaddr_un.sun_path holds 108 bytes
// on Linux and 104 on darwin/BSD, one of which goes to the trailing NUL.
func TestValidateSocketPathUnix(t *testing.T) {
	limit := 107
	if runtime.GOOS != "linux" {
		limit = 103
	}
	t.Run("length at the limit is accepted", func(t *testing.T) {
		path := "/" + strings.Repeat("d", limit-1)
		assert.Eq(t, limit, len(path))
		assert.NoErr(t, validateSocketPath(path))
	})
	t.Run("length over the limit is rejected", func(t *testing.T) {
		path := "/" + strings.Repeat("d", limit)
		assert.Eq(t, limit+1, len(path))
		err := validateSocketPath(path)
		// The message names the limit, the path and the way out: chat prints it to the user.
		assert.Err(t, err)
		assert.ErrSubMsg(t, err, strconv.Itoa(limit))
		assert.ErrSubMsg(t, err, "SUGA_HOME")
	})
}

// TestStartOverlongHomeFallsBack checks the registration path: a home too long
// for AF_UNIX now starts via the per-user fallback socket (the record itself
// still lives in the home); removing the fallback branch breaks this test.
func TestStartOverlongHomeFallsBack(t *testing.T) {
	relocate(t)
	root := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	server, err := Start(root, "sess-long-path", "long", t.TempDir(), func(string, string, string) (Delivery, error) { return Delivery{}, nil })
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	record := server.Record()
	assert.Eq(t, fallbackSocketPath(root, record.PID), record.Socket)
	entries, dirErr := os.ReadDir(root)
	assert.Require(t, assert.NoErr(t, dirErr))
	assert.Len(t, entries, 1)
}

// TestStartFailsWhenFallbackDirIsUnusable: a fallback directory that exists but
// is too open is refused; Start keeps the original length error and names why
// the fallback was not taken, instead of widening or reusing the directory.
func TestStartFailsWhenFallbackDirIsUnusable(t *testing.T) {
	relocate(t)
	assert.Require(t, assert.NoErr(t, os.MkdirAll(fallbackDirPath(), 0o755)))
	root := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	_, err := Start(root, "sess-wide-fallback", "wide", t.TempDir(), func(string, string, string) (Delivery, error) { return Delivery{}, nil })
	assert.ErrSubMsg(t, err, "too long")
	assert.ErrSubMsg(t, err, "not usable")
	assert.ErrSubMsg(t, err, "too open")
	assert.Eq(t, "0755", strconv.FormatInt(int64(mustPerm(t, fallbackDirPath())), 8))
}

// mustPerm reads dir's octal permission bits.
func mustPerm(t *testing.T, dir string) int64 {
	t.Helper()
	info, err := os.Stat(dir)
	assert.Require(t, assert.NoErr(t, err))
	return int64(info.Mode().Perm())
}

// TestFallbackDirRejectsForeignOwner covers the ownership half of
// ensureFallbackDir; only a privileged runner can chown, so it pins the check
// where it can be exercised (the Linux container runs the tests as root).
func TestFallbackDirRejectsForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to give the fallback directory to another uid")
	}
	relocate(t)
	dir := fallbackDirPath()
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
	assert.Require(t, assert.NoErr(t, os.Chown(dir, os.Getuid()+1, -1)))
	err := ensureFallbackDir()
	assert.ErrSubMsg(t, err, "not the current user")
}

// TestFallbackDirRejectsPlainFile pins the is-a-directory half of the check.
func TestFallbackDirRejectsPlainFile(t *testing.T) {
	relocate(t)
	assert.Require(t, assert.NoErr(t, os.WriteFile(fallbackDirPath(), nil, 0o600)))
	err := ensureFallbackDir()
	assert.ErrSubMsg(t, err, "not a directory")
}
