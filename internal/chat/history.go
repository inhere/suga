package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

const defaultHistorySize = 1000

type historyEntry struct {
	TS        time.Time `json:"ts"`
	Workspace string    `json:"workspace"`
	Text      string    `json:"text"`
}

// inputHistory keeps a local view for the editor and appends each submitted line.
// The file is only compacted after a margin, so reaching maxEntries does not
// rewrite the whole history for every new line.
type inputHistory struct {
	mu         sync.Mutex
	path       string
	workspace  string
	entries    []historyEntry
	enabled    bool
	maxEntries int
	// projectScoped is chat.history_scope: project makes Up/Down only recall the
	// current project's lines, all keeps the old cross-project recall.
	projectScoped bool
	// resolved maps stored workspace strings to their project root, so old
	// entries are compared with the same normalization as the current one
	// without walking the filesystem again per line.
	resolved map[string]string
}

func newInputHistory(workspace, scope string, enabled bool, maxEntries int) (*inputHistory, error) {
	if maxEntries < 1 {
		maxEntries = defaultHistorySize
	}
	// Records and comparisons both use the normalized project root, so starting
	// from a subdirectory of a git repository counts as the same project.
	root := projectRoot(workspace)
	h := &inputHistory{
		workspace:     root,
		projectScoped: scope != appconfig.ChatHistoryScopeAll,
		resolved:      map[string]string{root: root},
		enabled:       enabled,
		maxEntries:    maxEntries,
	}
	if !enabled {
		return h, nil
	}
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return nil, err
	}
	h.path = filepath.Join(home, "history.jsonl")
	f, err := os.Open(h.path)
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := scanHistory(bufio.NewScanner(f))
	if err != nil {
		return nil, err
	}
	h.entries = trimHistory(entries, maxEntries)
	return h, nil
}

func scanHistory(s *bufio.Scanner) ([]historyEntry, error) {
	var entries []historyEntry
	for s.Scan() {
		var e historyEntry
		if json.Unmarshal(s.Bytes(), &e) == nil && e.Text != "" {
			entries = append(entries, e)
		}
	}
	return entries, s.Err()
}

func trimHistory(entries []historyEntry, max int) []historyEntry {
	if len(entries) <= max {
		return entries
	}
	return entries[len(entries)-max:]
}

func (h *inputHistory) add(text string) error {
	if h == nil || !h.enabled || strings.TrimSpace(text) == "" {
		return nil
	}
	e := historyEntry{TS: time.Now().UTC(), Workspace: h.workspace, Text: redact.Secrets(text)}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "%s\n", data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	h.entries = append(h.entries, e)
	// Leave room for other chat processes and avoid a rewrite on every add.
	if count, err := historyFileLen(h.path); err == nil && count > historyTrimLimit(h.maxEntries) {
		if entries, err := h.trimLatest(); err == nil {
			h.entries = entries
		}
	}
	return nil
}

func historyTrimLimit(max int) int {
	margin := max / 10
	if margin < 1 {
		margin = 1
	}
	return max + margin
}

func historyFileLen(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	entries, err := scanHistory(bufio.NewScanner(f))
	return len(entries), err
}

// trimLatest reads again after the append, then replaces the file from that
// latest view. A failed cleanup or rename is deliberately ignored by add: the
// submitted line is already safely appended and can be compacted next time.
func (h *inputHistory) trimLatest() ([]historyEntry, error) {
	f, err := os.Open(h.path)
	if err != nil {
		return nil, err
	}
	entries, readErr := scanHistory(bufio.NewScanner(f))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	entries = trimHistory(entries, h.maxEntries)
	data := make([]byte, 0, len(entries)*64)
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".history-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, h.path); err != nil {
		// Windows cannot rename over an existing file. Removing the old name is
		// the only portable fallback; a failure still leaves the append intact.
		if removeErr := os.Remove(h.path); removeErr != nil {
			return nil, err
		}
		if err := os.Rename(tmpName, h.path); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// projectRoot normalizes a start directory into the stable identity of the
// project behind it: the nearest enclosing git repository's root (a .git entry,
// a directory for a normal clone, a file for a worktree or submodule), or the
// directory itself outside a git repository. Paths stay unresolved by symlinks
// and absolute, so what is stored in the file compares equal later.
func projectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	abs = filepath.Clean(abs)
	for at := abs; ; {
		if hasGitDir(at) {
			return at
		}
		parent := filepath.Dir(at)
		if parent == at {
			// No git repository above dir: the directory itself is the project,
			// never the drive root (which would collapse unrelated projects).
			return abs
		}
		at = parent
	}
}

// hasGitDir reports whether dir holds .git — a directory (a normal clone) or a
// file (a worktree or submodule checkout).
func hasGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// sameProject compares two normalized project roots. Windows paths are
// case-insensitive, so "C:\Work" and "c:\work" must match; on case-sensitive
// systems treating them alike costs nothing.
func sameProject(a, b string) bool {
	return strings.EqualFold(a, b)
}

// entryRoot resolves a stored entry's workspace to its project root. Old files
// hold the start directory as it was (a subdirectory of a git repository), so
// entries go through the same root lookup as the current workspace.
func (h *inputHistory) entryRoot(workspace string) string {
	if root, ok := h.resolved[workspace]; ok {
		return root
	}
	root := projectRoot(workspace)
	h.resolved[workspace] = root
	return root
}

func (h *inputHistory) lines() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.entries))
	// The editor expects oldest-to-newest entries. Under project scope, only the
	// current project's lines are recalled — a project can keep few entries in
	// the shared, globally capped history file (chat.history_size applies to the
	// whole file). Under all scope, the current project's group comes first so
	// Up reaches it before entries from other projects.
	if h.projectScoped {
		for _, entry := range h.entries {
			if sameProject(h.entryRoot(entry.Workspace), h.workspace) {
				out = append(out, entry.Text)
			}
		}
		return out
	}
	for pass := 0; pass < 2; pass++ {
		for _, entry := range h.entries {
			same := sameProject(h.entryRoot(entry.Workspace), h.workspace)
			if (pass == 0) != same {
				continue
			}
			out = append(out, entry.Text)
		}
	}
	return out
}
