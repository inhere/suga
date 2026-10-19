//go:build windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeDiskSpace returns the bytes a caller may still write on the volume that
// path lives on (the free space available to this user, which is what a
// temporary VACUUM file of the same size needs).
func FreeDiskSpace(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	// The directory has to exist for the Windows call; the DB file itself may
	// still be missing on a first run.
	dir := filepath.Dir(abs)
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
