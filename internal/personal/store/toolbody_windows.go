//go:build windows

package store

// syncDir is a no-op on Windows: a directory cannot be opened for FlushFileBuffers,
// and NTFS journals the directory entry itself.
func syncDir(string) error { return nil }
