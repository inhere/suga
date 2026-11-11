package native

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// readDenyMark is what file_list and file_find print after a file that
// tools.read_deny forbids reading: the name stays visible, the content does not.
const readDenyMark = "  [禁读]"

// ReadDenyList is the compiled tools.read_deny list (design 2026-11-10 C): the
// files the model may never read, whatever the approval mode. It is a plain
// value carried in Env, so every tool set (a sub-agent's included) holds its
// own copy and no package-level state exists. The zero value denies nothing.
type ReadDenyList struct {
	rules []readDenyRule
}

type readDenyRule struct {
	pattern string // slash-separated, lower-cased where paths fold case
	negate  bool   // "!" exception
}

// NewReadDenyList compiles patterns, which callers have already expanded
// ("~" and $SUGA_HOME become absolute paths). A pattern is a glob over the
// slash-separated absolute path: "**" matches any number of directories, a
// leading "**/" matches a file name under any directory, a pattern without "/"
// matches the file name at any depth. A leading "!" makes an exception; as in
// .gitignore the last matching pattern wins. A malformed glob is an error so a
// typo cannot silently leave a secret readable.
func NewReadDenyList(patterns []string) (ReadDenyList, error) {
	var list ReadDenyList
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		negate := strings.HasPrefix(p, "!")
		p = strings.TrimSpace(strings.TrimPrefix(p, "!"))
		if p == "" {
			return ReadDenyList{}, fmt.Errorf("tools.read_deny: empty pattern %q", raw)
		}
		p = filepath.ToSlash(p)
		for _, seg := range strings.Split(p, "/") {
			if _, err := path.Match(seg, "x"); err != nil {
				return ReadDenyList{}, fmt.Errorf("tools.read_deny: bad glob %q: %w", raw, err)
			}
		}
		if foldPathCase {
			p = strings.ToLower(p)
		}
		list.rules = append(list.rules, readDenyRule{pattern: p, negate: negate})
	}
	return list, nil
}

// Len is the number of patterns (exceptions included).
func (l ReadDenyList) Len() int { return len(l.rules) }

// Denied reports whether reading the file at absPath is forbidden. The path is
// cleaned and judged as written, and again after its symbolic links are
// resolved: a link named innocently that points at a secret, or a secret
// reached through a linked directory, is denied as well. A path that does not
// exist is judged by its name alone.
func (l ReadDenyList) Denied(absPath string) bool {
	if len(l.rules) == 0 {
		return false
	}
	p := filepath.Clean(absPath)
	if l.matches(p) {
		return true
	}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
		return l.matches(real)
	}
	return false
}

// matches runs the ordered rule list over one path.
func (l ReadDenyList) matches(p string) bool {
	slash := filepath.ToSlash(p)
	if foldPathCase {
		slash = strings.ToLower(slash)
	}
	denied := false
	for _, r := range l.rules {
		if globMatch(r.pattern, slash) {
			denied = !r.negate
		}
	}
	return denied
}

// deniedFile is Denied for a file only: tools.read_deny forbids reading
// content, so a directory argument still resolves (its entries are judged one
// by one while listing or searching).
func (l ReadDenyList) deniedFile(absPath string) bool {
	if !l.Denied(absPath) {
		return false
	}
	if info, err := os.Stat(absPath); err == nil && info.IsDir() {
		return false
	}
	return true
}

// errReadDenied is the refusal every tool gives for a forbidden file.
func errReadDenied(arg string) error {
	return fmt.Errorf("%s 被 tools.read_deny 禁止（敏感路径，不可读取；read denied by tools.read_deny）", arg)
}

// deniedRel reports whether the file at rel (slash-separated, relative to the
// target's base) is forbidden: walkers use it per entry.
func (r readTarget) deniedRel(deny ReadDenyList, rel string) bool {
	if len(deny.rules) == 0 {
		return false
	}
	return deny.Denied(filepath.Join(r.base, filepath.FromSlash(rel)))
}

// denySkipNote is the line a content search ends with when files were skipped.
func denySkipNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("\n[已跳过 %d 个被 tools.read_deny 禁止的文件]", n)
}
