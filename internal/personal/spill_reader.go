package personal

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// gzSuffix is the compressed form of a spill file, <stem>.txt.gz. Compressing
// an output does not change its ref: the reader looks for the plain .txt first
// and for the compressed form second.
const gzSuffix = ".txt.gz"

// outputFile is one opened spill file: the reader plus everything that has to
// be released with it (the decompressor, the file and the directory root).
type outputFile struct {
	io.Reader
	closers []io.Closer
}

// Close releases the file after its reader; a broken reader is closed anyway.
func (o *outputFile) Close() error {
	var first error
	for _, c := range o.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OpenOutput opens the spill file named by ref (out:<session_id>:<16 hex>)
// under sessionsDir for reading. It is the one reader of externalised outputs
// (recall, session_search with a ref, export): it validates the ref, refuses
// anything that would lead outside the session's outputs directory - a planted
// symlink included - and transparently reads <stem>.txt or <stem>.txt.gz, with
// the .txt winning when both exist. A .gz is decoded while it is read, so a
// large output is never loaded into memory as a whole; a damaged .gz is
// reported as such instead of yielding garbage.
func OpenOutput(sessionsDir, ref string) (io.ReadCloser, error) {
	sessionID, name, err := parseOutputRef(ref)
	if err != nil {
		return nil, err
	}
	return openOutputFile(sessionsDir, sessionID, name, ref)
}

// openOutputFile opens an already validated ref through an os.Root on the
// sessions directory: both the session directory and the file itself then have
// to stay inside it, whatever the file system holds.
func openOutputFile(sessionsDir, sessionID, name, ref string) (io.ReadCloser, error) {
	if strings.TrimSpace(sessionsDir) == "" {
		return nil, errors.New("no sessions directory configured")
	}
	root, err := os.OpenRoot(sessionsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("output %s not found (session %q has no saved outputs)", ref, sessionID)
		}
		return nil, err
	}
	rel := filepath.Join(sessionID, "outputs", name+".txt")
	f, err := root.Open(rel)
	if err == nil {
		return &outputFile{Reader: f, closers: []io.Closer{f, root}}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		_ = root.Close()
		return nil, fmt.Errorf("open output %s: %w", ref, err)
	}
	gz, err := root.Open(filepath.Join(sessionID, "outputs", name+gzSuffix))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			hint := noOutputsHint(root, sessionID) // read before the root is closed
			_ = root.Close()
			return nil, fmt.Errorf("output %s not found%s", ref, hint)
		}
		_ = root.Close()
		return nil, fmt.Errorf("open output %s: %w", ref, err)
	}
	zr, err := gzip.NewReader(gz)
	if err != nil {
		_ = gz.Close()
		_ = root.Close()
		return nil, fmt.Errorf("output %s: damaged gzip: %w", ref, err)
	}
	return &outputFile{Reader: zr, closers: []io.Closer{zr, gz, root}}, nil
}

// noOutputsHint explains a missing output when the session has no outputs
// directory at all, so a ref of a session that never spilled is not mistaken
// for a file that was lost.
func noOutputsHint(root *os.Root, sessionID string) string {
	if _, err := root.Stat(filepath.Join(sessionID, "outputs")); errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf(" (session %q has no saved outputs)", sessionID)
	}
	return ""
}

// gzLogicalBytes is the uncompressed size gzip records in the trailer of a .gz
// file (its last 4 bytes, little endian), or 0 when it cannot be read. Spill
// accounting uses it so the quota compares logical, not physical, bytes.
func gzLogicalBytes(f io.ReaderAt, size int64) int64 {
	if size < 4 {
		return 0
	}
	var trailer [4]byte
	if _, err := f.ReadAt(trailer[:], size-4); err != nil {
		return 0
	}
	return int64(uint32(trailer[0]) | uint32(trailer[1])<<8 | uint32(trailer[2])<<16 | uint32(trailer[3])<<24)
}
