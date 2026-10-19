//go:build !windows

package store

import "os"

// syncDir fsyncs a directory so the entry of a file created in it survives a
// power loss (Linux and macOS; Windows keeps no such requirement and has no way
// to fsync a directory handle, see toolbody_windows.go).
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
