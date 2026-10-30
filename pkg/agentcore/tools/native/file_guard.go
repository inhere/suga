package native

import (
	"fmt"
	"github.com/inhere/suga/pkg/agentcore/api"
	"os"
	"sync"
	"time"
)

// diskStamp identifies the state of a file by size and modification time. It is
// a stamp, not a content hash, on purpose: file_read of a large file (or of a
// line window of it) must not cost a full read just to guard a later write, and
// a Stat is enough to notice an editor, a formatter or another process having
// written the file. The one miss is a same-size rewrite inside the filesystem's
// mtime granularity, which the guard accepts.
type diskStamp struct {
	size  int64
	mtime time.Time
}

// FileGuard remembers the stamp of every file a session's file_read, file_write
// and file_edit last saw, and refuses a write over a file that changed since
// (tools.protect_changed_files): the model would be writing from stale content.
// A file never read or written in the session is not guarded - creating a file
// or writing one the model never looked at keeps working. One FileGuard belongs
// to one tool set (BuildTools makes one per call), so a sub-agent has its own
// table; the mutex covers tools running in parallel.
type FileGuard struct {
	mu   sync.Mutex
	seen map[string]diskStamp
}

// NewFileGuard returns an empty guard.
func NewFileGuard() *FileGuard { return &FileGuard{seen: map[string]diskStamp{}} }

// stampOf stats target through the same os.Root sandbox as the file tools.
func stampOf(target readTarget) (diskStamp, bool) {
	st, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (os.FileInfo, error) {
		return root.Stat(rel)
	})
	if err != nil || st.IsDir() {
		return diskStamp{}, false
	}
	return diskStamp{size: st.Size(), mtime: st.ModTime()}, true
}

// remember stores stamp as the last known state of path; ok=false (the file is
// gone or unreadable) forgets it. A nil guard does nothing.
func (g *FileGuard) remember(path string, stamp diskStamp, ok bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !ok {
		delete(g.seen, path)
		return
	}
	g.seen[path] = stamp
}

// rememberNow stamps target as it is now (after this tool wrote it).
func (g *FileGuard) rememberNow(target readTarget) {
	if g == nil {
		return
	}
	stamp, ok := stampOf(target)
	g.remember(target.abs, stamp, ok)
}

// check returns an error when target was seen by this session and its stamp
// differs now. tool names the refusing tool in the message.
func (g *FileGuard) check(tool string, target readTarget) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	last, seen := g.seen[target.abs]
	g.mu.Unlock()
	if !seen {
		return nil
	}
	now, ok := stampOf(target)
	if !ok || now == last {
		// A file removed since is a create for file_write, and file_edit reports
		// the missing file itself.
		return nil
	}
	return fmt.Errorf("%s: %s changed on disk since you last read or wrote it (another process or editor modified it); "+
		"nothing was written - run file_read on it again to see the current content, then redo the change", tool, target.abs)
}

// readGuarded is readEntry that remembers the file's stamp for the write guard.
// The stamp is taken before the read: a file modified while it is being read
// then differs from the remembered stamp and the next write is refused, instead
// of the guard trusting a stamp newer than the text the model saw.
func (t *FileReadTool) readGuarded(item readItem, offset, limit int, outline, single bool) (string, *api.ImagePart, error) {
	if t.guard == nil {
		return t.readEntry(item, offset, limit, outline, single)
	}
	var target readTarget
	var stamp diskStamp
	var stamped bool
	if tg, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), item.path); err == nil {
		target = tg
		stamp, stamped = stampOf(tg)
	}
	text, part, err := t.readEntry(item, offset, limit, outline, single)
	if err == nil && stamped {
		t.guard.remember(target.abs, stamp, true)
	}
	return text, part, err
}
