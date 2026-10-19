package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// searchTimeout bounds one file_grep / file_find walk; what was found by
// then is returned and marked incomplete. A var so tests can shorten it.
var searchTimeout = 10 * time.Second

// maxSearchFileBytes: larger files are skipped by file_grep.
const maxSearchFileBytes = 1 << 20

// errSearchFull stops a walk once enough results were collected.
var errSearchFull = errors.New("search result limit reached")

// walkResult says how a walk ended: timedOut marks an incomplete result and
// ignoreNote what was skipped as ignored (empty when nothing was).
type walkResult struct {
	timedOut   bool
	ignoreNote string
}

// walkWorkspace visits the regular files under start (a path inside workspace)
// in lexical order, calling fn with the slash-separated path relative to the
// workspace. Every open goes through os.Root and symlinks are never followed,
// so nothing outside the workspace is read. Directories and files ignored by
// the .gitignore files or ignoredDirs are skipped unless noIgnore is set
// (.git always is); start itself is always searched, even when ignored. fn may
// return errSearchFull to stop early. The walk ends at ctx's deadline.
func walkWorkspace(ctx context.Context, workspace, start string, noIgnore bool, fn func(root *os.Root, rel string, d fs.DirEntry) error) (walkResult, error) {
	var res walkResult
	_, err := withRoot(workspace, start, func(root *os.Root, relStart string) (struct{}, error) {
		ign := newIgnoreMatcher(root, noIgnore)
		fsStart := filepath.ToSlash(relStart)
		info, err := root.Lstat(relStart)
		if errors.Is(err, fs.ErrNotExist) {
			return struct{}{}, fmt.Errorf("%s does not exist", start)
		} else if err != nil {
			return struct{}{}, err
		}
		if info.Mode().IsRegular() { // a single file
			err := fn(root, fsStart, fs.FileInfoToDirEntry(info))
			if errors.Is(err, errSearchFull) {
				return struct{}{}, nil
			}
			return struct{}{}, err
		}
		werr := fs.WalkDir(root.FS(), fsStart, func(rel string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				res.timedOut = true
				return fs.SkipAll
			}
			if err != nil {
				if rel == fsStart {
					return err
				}
				return nil // unreadable entry: skip it
			}
			if d.IsDir() {
				if rel != fsStart && ign.skipDir(rel, d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || ign.skipFile(rel) {
				return nil // symlinks and special files are not followed
			}
			return fn(root, rel, d)
		})
		if errors.Is(werr, errSearchFull) {
			werr = nil
		}
		res.ignoreNote = ign.note()
		return struct{}{}, werr
	})
	return res, err
}

// isBinaryHead reports whether head (the start of a file) looks binary.
func isBinaryHead(head []byte) bool { return bytes.IndexByte(head, 0) >= 0 }

// globMatch matches a slash-separated rel path against pattern. "**" matches
// any number of path segments; a pattern without "/" matches the base name
// at any depth ("*.go").
func globMatch(pattern, rel string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(rel))
		return ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
