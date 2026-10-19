//go:build !windows

package process

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

func processExists(pid int) bool {
	// pid_t is 32-bit: larger values would wrap (MaxInt64 becomes -1, i.e. every process).
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	// Signal the PID directly: os.FindProcess uses a pidfd on Linux and reports an
	// exited process as os.ErrProcessDone rather than ESRCH.
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// fallbackDirPath names the shared per-user fallback socket directory:
// $TMPDIR/suga-<uid>. It only computes the path (used by trustedSocketPaths
// too); permissions and ownership of the directory itself are enforced by
// ensureFallbackDir when the fallback is actually taken.
func fallbackDirPath() string {
	return filepath.Join(os.TempDir(), "suga-"+strconv.Itoa(os.Getuid()))
}

// ensureFallbackDir makes the fallback directory safe to use: it is created as
// 0700 when missing, and an existing one must be a directory, be owned by the
// current user and be no wider than 0700. A directory that fails the checks is
// refused instead of being reused or widened.
func ensureFallbackDir() error {
	dir := fallbackDirPath()
	err := os.Mkdir(dir, 0o700)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	// Already there: the checks below read the same directory that Mkdir saw,
	// so a racing swap of the path cannot slip past a validated reuse.
	info, statErr := os.Stat(dir)
	if statErr != nil {
		return statErr
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s is owned by uid %d, not the current user (uid %d)", dir, stat.Uid, os.Getuid())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is too open (mode %#o), 0700 or tighter is required", dir, perm)
	}
	return nil
}

// socketPathLimit is the longest usable sockaddr_un.sun_path value on goos: the kernel (and
// Go's syscall.SockaddrUnix) needs a trailing NUL inside the fixed-size buffer, so the field
// size (108 bytes on Linux, 104 on darwin and the BSDs) leaves one byte less for the path.
func socketPathLimit(goos string) int {
	if goos == "linux" {
		return 107
	}
	return 103
}

// validateSocketPath rejects a socket path too long to bind. Without this check the failure
// surfaces as an opaque "bind: invalid argument" at listen time.
func validateSocketPath(path string) error {
	limit := socketPathLimit(runtime.GOOS)
	if len(path) > limit {
		return fmt.Errorf("local session socket path is too long: %d bytes, %s allows at most %d bytes: %s; use a shorter SUGA_HOME", len(path), runtime.GOOS, limit, path)
	}
	return nil
}

// secureDir limits dir to the owner. Best effort, like the original chmod: a directory we
// cannot chmod (not ours) is left as is.
func secureDir(dir string) error {
	_ = os.Chmod(dir, 0o700)
	return nil
}
