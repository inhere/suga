//go:build !windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// FreeDiskSpace returns the bytes a caller may still write on the volume that
// path lives on (the free space available to this user, which is what a
// temporary VACUUM file of the same size needs).
func FreeDiskSpace(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	dir := filepath.Dir(abs)
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
