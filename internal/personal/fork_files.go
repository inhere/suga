package personal

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/inhere/suga/internal/personal/store"
)

// Attachments of a forked session (plan T6): the image files and spill files
// the copied entries name. They are copied below the new session's directory
// before the transaction; the spill refs in the entry bodies are rewritten to
// the new session, so the fork no longer reads (or loses) the source's files.
// Compaction and handoff records are not rewritten: their Refs stay pointing at
// the source session (R6), a read-only cross-session reference used for search.

// forkImageRef matches the stored image refs of ImagePart.Ref
// (images/<sha256>.<ext>, relative to the session directory).
var forkImageRef = regexp.MustCompile(`images/[0-9a-f]{64}\.(?:png|jpg|gif|webp)`)

// copyForkFiles copies the attachments of entries from the source session
// directory to the new one and rewrites the spill refs in the entries' JSON in
// place. A file the source no longer has is skipped with a warning (the copy
// would be as broken as the original); any other failure fails the fork.
func copyForkFiles(sessionsRoot, srcID, newID string, entries []store.NewEntry) error {
	if sessionsRoot == "" {
		return nil
	}
	spillRef := regexp.MustCompile(regexp.QuoteMeta(outputRefPrefix+srcID+":") + `([0-9a-f]{16})`)
	copiedSpill := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if e.Type == store.EntryCompaction || e.Type == store.EntryHandoff {
			continue
		}
		for _, name := range forkImageRef.FindAll(e.JSON, -1) {
			rel := string(name)
			found, err := copyForkFile(sessionsRoot, srcID, newID, filepath.FromSlash(rel))
			if err != nil {
				return fmt.Errorf("copy image %s: %w", rel, err)
			}
			if !found {
				slog.Warn("fork: image of the source session is missing, not copied", "session_id", srcID, "file", rel)
			}
		}
		for _, m := range spillRef.FindAllSubmatch(e.JSON, -1) {
			stem := string(m[1])
			if _, done := copiedSpill[stem]; done {
				continue
			}
			ok, err := copySpillFile(sessionsRoot, srcID, newID, stem)
			if err != nil {
				return fmt.Errorf("copy output %s: %w", stem, err)
			}
			if !ok {
				slog.Warn("fork: output of the source session is missing, ref left pointing at it", "session_id", srcID, "stem", stem)
			}
			copiedSpill[stem] = ok
		}
		e.JSON = spillRef.ReplaceAllFunc(e.JSON, func(ref []byte) []byte {
			stem := string(ref[len(outputRefPrefix+srcID+":"):])
			if !copiedSpill[stem] {
				return ref
			}
			return []byte(outputRefPrefix + newID + ":" + stem)
		})
	}
	return nil
}

// copySpillFile copies <stem>.txt or its compressed form (the .txt wins when
// both exist, as OpenOutput reads them) and reports whether one was found.
func copySpillFile(sessionsRoot, srcID, newID, stem string) (bool, error) {
	for _, suffix := range []string{".txt", gzSuffix} {
		found, err := copyForkFile(sessionsRoot, srcID, newID, filepath.Join("outputs", stem+suffix))
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// copyForkFile copies one regular file below the session directory (rel is
// built by the callers from a validated session id and a hex name). It reports
// false when the source file does not exist. A symlink or other non-regular file
// is refused: it could lead out of the sessions root.
func copyForkFile(sessionsRoot, srcID, newID, rel string) (bool, error) {
	src := filepath.Join(sessionsRoot, srcID, rel)
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", rel)
	}
	dst := filepath.Join(sessionsRoot, newID, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	// O_EXCL: the same image in two entries is copied once; a file that exists
	// is the one a previous entry wrote (names are content hashes / turn+call
	// hashes of this session).
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return false, err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return false, err
	}
	return true, out.Close()
}
