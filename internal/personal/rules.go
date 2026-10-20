package personal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxRuleFileBytes  = 64 * 1024
	maxRuleTotalBytes = 128 * 1024
)

// DefaultRuleFile is the project rule file read at every directory level when
// the caller has none configured (rules.file). The user-level file is fixed:
// $SUGA_HOME/AGENTS.md.
const DefaultRuleFile = "AGENTS.md"

// RuleFileStat is one rule file that LoadRules read, and how much of it
// reached the segment context.
type RuleFileStat struct {
	Path      string
	Original  int // bytes on disk
	Kept      int // bytes emitted into the rules text
	Truncated bool
	Included  bool // read through an @include directive
	// Text is the kept content of the file, used to estimate its tokens.
	Text string
}

// RuleStats reports the size and truncation of the loaded rules. It is
// diagnostic only: the rules text and hash do not depend on it.
type RuleStats struct {
	Files         []RuleFileStat
	OriginalBytes int // sum of Original
	KeptBytes     int // bytes of rule content emitted (headings and markers excluded)
	Truncated     bool
}

// LoadRules reads user and project instructions in authority order. Project
// rules come from one file per directory level (the Git root down to cwd),
// named by ruleFile; "" means DefaultRuleFile. Includes are deliberately one
// level deep so a rule file cannot recursively expand a tree.
func LoadRules(cwd, sugaHome string, noProject bool, ruleFile string) (string, string, error) {
	content, hash, _, err := LoadRulesDetailed(cwd, sugaHome, noProject, ruleFile)
	return content, hash, err
}

// LoadRulesDetailed is LoadRules plus the per-file size and truncation report
// the manifest shows. The text and hash are exactly LoadRules' (this is its
// only implementation).
func LoadRulesDetailed(cwd, sugaHome string, noProject bool, ruleFile string) (string, string, RuleStats, error) {
	var stats RuleStats
	paths, workspaceRoot, err := rulePaths(cwd, sugaHome, noProject, ruleFile)
	if err != nil {
		return "", "", stats, err
	}
	var sections, hashSections []string
	remaining := maxRuleTotalBytes
	seenPaths := make(map[string]struct{})
	for _, path := range paths {
		content, err := expandRuleFile(path, &remaining, seenPaths, &stats, false)
		if err != nil {
			return "", "", stats, err
		}
		if content != "" {
			sections = append(sections, fmt.Sprintf("# %s\n%s", path, content))
			hashSections = append(hashSections, fmt.Sprintf("# %s\n%s", ruleHashLabel(path, sugaHome, workspaceRoot), content))
		}
		if remaining == 0 {
			break
		}
	}
	// A file that did not fit in the 128KB total may never have been read, so
	// the report cannot know its size; OriginalBytes only covers read files.
	for _, f := range stats.Files {
		stats.OriginalBytes += f.Original
	}
	rules := strings.Join(sections, "\n\n")
	// The hash must not depend on where the checkout lives (another directory,
	// container or Windows path), so it covers path-neutral labels plus content.
	hash := sha256.Sum256([]byte(strings.Join(hashSections, "\n\n")))
	return rules, hex.EncodeToString(hash[:]), stats, nil
}

// RuleFiles lists the rule files LoadRules reads, in order: the user-level
// file, then the project file of each level (the user-level one may not
// exist; LoadRules skips it then).
func RuleFiles(cwd, sugaHome string, noProject bool, ruleFile string) ([]string, error) {
	paths, _, err := rulePaths(cwd, sugaHome, noProject, ruleFile)
	return paths, err
}

// rulePaths returns the rule file candidates and the workspace root.
func rulePaths(cwd, sugaHome string, noProject bool, ruleFile string) ([]string, string, error) {
	name := ruleFile
	if name == "" {
		name = DefaultRuleFile
	}
	// Only a file name is accepted, so the value cannot leave the directory of
	// a level (config validates rules.file for the same reason).
	if !validRuleFileName(name) {
		return nil, "", fmt.Errorf("project rule file name %q must be a bare file name without a path separator", name)
	}
	var paths []string
	workspaceRoot := ""
	if sugaHome != "" {
		paths = append(paths, filepath.Join(sugaHome, DefaultRuleFile))
	}
	if !noProject {
		project, root, err := projectRulePaths(cwd, name)
		if err != nil {
			return nil, "", err
		}
		workspaceRoot = root
		paths = append(paths, project...)
	}
	return paths, workspaceRoot, nil
}

// ruleHashLabel returns a machine-independent identity for a rule file: files
// under sugaHome are "suga-home/<rel>", files under the workspace root are the
// slash-separated relative path, anything else keeps only its base name.
func ruleHashLabel(path, sugaHome, workspaceRoot string) string {
	if rel, ok := relInside(sugaHome, path); ok {
		return "suga-home/" + rel
	}
	if rel, ok := relInside(workspaceRoot, path); ok {
		return rel
	}
	return "external/" + filepath.Base(path)
}

// relInside returns path relative to root (slash separated) when path is inside root.
func relInside(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// validRuleFileName reports whether name is a bare file name: non-empty and
// without a path separator, so it stays inside the directory it is joined to.
func validRuleFileName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// projectRulePaths returns the ruleFile of every level from the workspace root
// down to cwd, and the workspace root itself: the Git root, or cwd when there
// is no Git root.
func projectRulePaths(cwd, ruleFile string) ([]string, string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return nil, "", err
	}
	var levels []string
	for current := dir; ; current = filepath.Dir(current) {
		levels = append(levels, current)
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil || filepath.Dir(current) == current {
			break
		} else if !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	// Without a Git root, only the requested cwd is in scope.
	if _, err := os.Stat(filepath.Join(levels[len(levels)-1], ".git")); os.IsNotExist(err) {
		levels = levels[:1]
	}
	var paths []string
	for i := len(levels) - 1; i >= 0; i-- {
		path := filepath.Join(levels[i], ruleFile)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			paths = append(paths, path)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	return paths, levels[len(levels)-1], nil
}

func expandRuleFile(path string, remaining *int, seenPaths map[string]struct{}, stats *RuleStats, included bool) (string, error) {
	pathKey := rulePathKey(path)
	if _, exists := seenPaths[pathKey]; exists {
		return "", nil
	}
	content, truncated, err := readRuleFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read rule %s: %w", path, err)
	}
	seenPaths[pathKey] = struct{}{}
	// Record what this file contributes to the resident context. The deferred
	// update fills Kept once the lines (and includes) have been emitted.
	stat := RuleFileStat{Path: path, Original: len(content), Truncated: truncated, Included: included}
	if truncated {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() > int64(len(content)) {
			stat.Original = int(info.Size())
		}
	}
	stats.Files = append(stats.Files, stat)
	statIndex := len(stats.Files) - 1
	keptStart := stats.KeptBytes
	defer func() {
		// The parent's Kept excludes what its @include files contributed: those
		// have their own entry, so per-file bytes never double count.
		kept := stats.KeptBytes - keptStart
		for _, f := range stats.Files[statIndex+1:] {
			if f.Included {
				kept -= f.Kept
			}
		}
		if kept < 0 {
			kept = 0
		}
		stats.Files[statIndex].Kept = kept
		stats.Files[statIndex].Text = content
		if stats.Files[statIndex].Truncated {
			stats.Truncated = true
		}
	}()
	var out strings.Builder
	// fence 记录当前所处围栏代码块的标记字符（` 或 ~），0 表示不在代码块内。
	// 代码块内的 @Test/@Override 等不是 include 指令，必须原样保留。
	var fence byte
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if marker := fenceMarker(trimmed); marker != 0 {
			if fence == 0 {
				fence = marker
			} else if fence == marker {
				fence = 0
			}
		} else if fence == 0 {
			if includePath := ruleIncludePath(trimmed); includePath != "" {
				include := filepath.Join(filepath.Dir(path), includePath)
				if sameRulePath(path, include) {
					out.WriteString("[循环引用已跳过: " + trimmed + "]\n")
					continue
				}
				includeKey := rulePathKey(include)
				if _, exists := seenPaths[includeKey]; exists {
					continue
				}
				// 目标不存在、是目录/非普通文件或读取失败时，这行很可能只是普通
				// 文本（如 "@某人 负责"），降级为保留原行，避免阻断会话创建。
				if info, statErr := os.Stat(include); statErr == nil && info.Mode().IsRegular() {
					if part, cut, readErr := readRuleFile(include); readErr == nil {
						seenPaths[includeKey] = struct{}{}
						text, dropped := appendLimited(part, remaining)
						stats.KeptBytes += len(text)
						includeStat := RuleFileStat{Path: include, Original: len(part), Kept: len(text), Truncated: cut, Included: true, Text: text}
						if cut {
							if info, statErr := os.Stat(include); statErr == nil && info.Size() > int64(len(part)) {
								includeStat.Original = int(info.Size())
							}
						}
						if cut || dropped {
							stats.Truncated = true
						}
						stats.Files = append(stats.Files, includeStat)
						out.WriteString(text)
						if cut {
							out.WriteString("\n[规则已截断: 单文件超过 64KB]")
						}
						if dropped {
							out.WriteString("\n[规则已截断: 总量超过 128KB]")
							break
						}
						continue
					}
				}
			}
		}
		text, dropped := appendLimited(line, remaining)
		stats.KeptBytes += len(text)
		if dropped {
			stats.Truncated = true
		}
		out.WriteString(text)
		if dropped {
			out.WriteString("\n[规则已截断: 总量超过 128KB]")
			break
		}
	}
	if truncated {
		out.WriteString("\n[规则已截断: 单文件超过 64KB]")
	}
	return out.String(), nil
}

// fenceMarker returns '`' or '~' when the line opens/closes a fenced code block.
func fenceMarker(trimmed string) byte {
	if strings.HasPrefix(trimmed, "```") {
		return '`'
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return '~'
	}
	return 0
}

// ruleIncludePath accepts a line-start @path directive and ignores trailing
// whitespace-delimited explanation text.
func ruleIncludePath(line string) string {
	if !strings.HasPrefix(line, "@") {
		return ""
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || len(fields[0]) < 2 {
		return ""
	}
	return fields[0][1:]
}

func readRuleFile(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRuleFileBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxRuleFileBytes {
		return string(data[:utf8Boundary(data, maxRuleFileBytes)]), true, nil
	}
	return string(data), false, nil
}

// appendLimited cuts content to the remaining budget on a UTF-8 boundary and
// reports whether any content was actually discarded.
func appendLimited(content string, remaining *int) (string, bool) {
	dropped := false
	if len(content) > *remaining {
		content = content[:utf8Boundary([]byte(content), *remaining)]
		dropped = true
	}
	*remaining -= len(content)
	return content, dropped
}

// utf8Boundary moves cut back so that data[:cut] does not split a multi-byte rune.
func utf8Boundary(data []byte, cut int) int {
	for cut > 0 && cut < len(data) && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return cut
}

func sameRulePath(a, b string) bool {
	return rulePathKey(a) == rulePathKey(b)
}

func rulePathKey(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.Clean(path)
	if os.PathSeparator == '\\' {
		path = strings.ToLower(path)
	}
	return path
}
