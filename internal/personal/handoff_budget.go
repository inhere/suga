package personal

import (
	"fmt"
	"regexp"
	"strings"
)

// Handoff package section names (compactTemplate). They are matched by name
// without the parenthetical note, so the template may carry the budget text
// after it.
const (
	handoffSectionTask     = "当前任务"
	handoffSectionCoords   = "坐标"
	handoffSectionVerified = "验证到哪"
	handoffSectionProgress = "进展与阻塞"
	handoffSectionNext     = "下一步"
)

// handoffSectionRefs is the section name of refsHeading (the refs the package
// offers for recall).
var handoffSectionRefs = strings.TrimSpace(strings.TrimPrefix(refsHeading, "## "))

// handoffProtectedSection holds what must survive a segment break word for
// word: the hard constraints, the rules in force and the person's own words.
// The long-session evaluation checks exactly these lines (research 2026-10-22
// §3: both constraints stayed verbatim in all three packages), so this section
// has no budget, is never cut by handoffCapSection and is never rewritten while
// folding.
const handoffProtectedSection = "采纳与否决"

// handoffSectionBudgets are the output budgets of the model-written sections
// (bytes). They exist because every segment break folds the previous package
// into the new one: without a bound the coordinate table of a long session grew
// the package 4.7 -> 7.8 -> 16.0 KB (tokens_after 8.1k -> 9.3k -> 12.4k,
// research 2026-10-22 §5 #2). Each budget is sized after what a new segment
// needs to look a fact up again:
//
//   - the recent requests are appended verbatim (renderPackage), so the task
//     section is a short pointer to them;
//   - the coordinate table is the section that grows every fold: 2 KB holds
//     ~40 "path: symbol 第 N 行" lines, and anything older can be re-read;
//   - verification is a list of commands with their outcome, short by nature;
//   - progress carries a few open items with their state;
//   - next steps stay a handful of pointers;
//   - refs are one line each and only the ones the new segment may recall.
//
// The protected section has no budget: what it costs is worth its certainty.
var handoffSectionBudgets = map[string]int{
	handoffSectionTask:     1024,
	handoffSectionCoords:   2048,
	handoffSectionVerified: 1536,
	handoffSectionProgress: 2048,
	handoffSectionNext:     1024,
	handoffSectionRefs:     1024,
}

// handoffSection is one "## " section of a package: the heading line as written
// ("" for the text before the first heading) and the lines under it.
type handoffSection struct {
	heading string
	body    string
}

// handoffSectionName returns the name of a section heading line without its
// parenthetical note ("## 坐标（…）" -> "坐标"); ok is false for a non-heading
// line (including an indented sub-heading).
func handoffSectionName(line string) (name string, ok bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "## ") {
		return "", false
	}
	name, _, _ = strings.Cut(strings.TrimSpace(t[len("## "):]), "（")
	return strings.TrimSpace(name), true
}

// splitHandoffSections splits a package into its "## " sections, the text
// before the first heading as a section with an empty heading. joinHandoffSections
// restores the text (the only normalization is that a leading blank line of a
// section is kept as its first body line).
func splitHandoffSections(text string) []handoffSection {
	var out []handoffSection
	cur := handoffSection{}
	started := false // whether cur.body already holds a line
	for _, line := range strings.Split(text, "\n") {
		if _, ok := handoffSectionName(line); ok {
			out = append(out, cur)
			cur, started = handoffSection{heading: line}, false
			continue
		}
		if started {
			cur.body += "\n" + line
		} else {
			cur.body, started = line, true
		}
	}
	return append(out, cur)
}

// joinHandoffSections renders splitHandoffSections' result back to text.
func joinHandoffSections(secs []handoffSection) string {
	var sb strings.Builder
	for i, s := range secs {
		if i > 0 {
			sb.WriteString("\n")
		}
		if s.heading != "" {
			sb.WriteString(strings.TrimRight(s.heading, "\n") + "\n")
		}
		sb.WriteString(s.body)
	}
	return sb.String()
}

// handoffCapSection cuts an over-budget section: the head lines that fit are
// kept verbatim and one marker line names what was dropped and how to get it
// back. Sections without a budget (the protected one, and the requests, steers
// and plan renderPackage appends) are returned unchanged.
func handoffCapSection(s handoffSection, budget int) handoffSection {
	if budget <= 0 || len(s.body) <= budget {
		return s
	}
	name, _ := handoffSectionName(s.heading)
	s.body = handoffCapBody(s.body, budget, name)
	return s
}

// handoffCapBody keeps the newest lines of body within budget (leaving room
// for the marker) and puts the marker first. The newest lines are the freshest
// state of a section (progress, verification, coordinates), and the older ones
// are what the refs and a re-read reconstruct; a single line longer than the
// budget keeps its head and tail (truncateMiddle), so a pasted log cannot fill
// the section.
func handoffCapBody(body string, budget int, name string) string {
	const markerReserve = 200
	limit := max(budget-markerReserve, 0)
	lines := strings.Split(body, "\n")
	var kept []string
	n := 0
	for i := len(lines) - 1; i >= 0; i-- {
		add := len(lines[i]) + 1
		if n+add > limit {
			break
		}
		kept = append(kept, lines[i])
		n += add
	}
	if len(kept) == 0 {
		kept = []string{truncateMiddle(lines[len(lines)-1], limit)}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	out := strings.Join(kept, "\n")
	return fmt.Sprintf("- …（%s节超出 %d 字节预算：前面的 %d 字节已省略，只留最新的行；更早的细节请按 refs recall 或重新读取文件确认）\n%s",
		name, budget, len(body)-len(out), out)
}

// normalizeHandoffBody turns a generated package body into what the package
// stores: the coordinate section keeps one line per file+symbol (the latest)
// and every capped section is cut to its budget. It is the output side of the
// budgets: a summarizer that ignores them still cannot grow the package, and
// the protected section is never touched.
func normalizeHandoffBody(body string) string {
	secs := splitHandoffSections(body)
	for i, s := range secs {
		name, ok := handoffSectionName(s.heading)
		if !ok {
			continue
		}
		if name == handoffSectionCoords {
			s.body = strings.Join(handoffFoldCoords(strings.Split(s.body, "\n"), ""), "\n")
		}
		if budget, ok := handoffSectionBudgets[name]; ok {
			s = handoffCapSection(s, budget)
		}
		secs[i] = s
	}
	return joinHandoffSections(secs)
}

// foldPreviousPackage prepares the closing segment's package for folding into
// the new one (summarize's "# 已有摘要"). Two things keep the fold bounded
// (research 2026-10-22 §5 #2: "折叠时按 refs/坐标去重旧坐标"):
//
//   - the newest material wins: a coordinate whose file and symbol the fresh
//     transcript already mentions (fresh) is dropped, because the summarizer
//     reads that line number from the transcript itself, and within the section
//     a later line about the same file+symbol wins over an earlier one;
//   - the capped sections enter the fold at their budget, so a package written
//     before these budgets (or with a fully grown coordinate table) shrinks
//     instead of being carried forward whole.
//
// The protected section and the sections renderPackage appends (recent
// requests, steers, plan) stay verbatim.
func foldPreviousPackage(previous, fresh string) string {
	secs := splitHandoffSections(previous)
	for i, s := range secs {
		name, ok := handoffSectionName(s.heading)
		if !ok {
			continue
		}
		if name == handoffSectionCoords {
			s.body = strings.Join(handoffFoldCoords(strings.Split(s.body, "\n"), fresh), "\n")
		}
		if budget, ok := handoffSectionBudgets[name]; ok {
			s = handoffCapSection(s, budget)
		}
		secs[i] = s
	}
	return joinHandoffSections(secs)
}

// coordPath matches a path-like token of a coordinate line: it needs a path
// separator or a source file extension, so a bare word, a version or a line
// number is no coordinate.
var coordPath = regexp.MustCompile(`[A-Za-z0-9_.:\-]*[/\\][A-Za-z0-9_.\-/\\]+|` +
	`[A-Za-z0-9_.:\-]+\.(?:go|py|ts|tsx|js|jsx|java|rs|rb|c|h|cc|cpp|hpp|cs|kt|php|md|yaml|yml|json|toml|sql|sh|ps1|txt)`)

// coordSymbol matches an identifier a coordinate line names.
var coordSymbol = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{1,}`)

// Coordinate keys (see coordKeys): a path, a path+symbol pair, a bare symbol,
// or the whole line for a line that names neither.
const (
	coordKeyPath   = "p:"
	coordKeyPair   = "ps:"
	coordKeySymbol = "s:"
	coordKeyText   = "t:"
)

// coordKeys returns the keys a coordinate line speaks about: "同一文件同一符号"
// is one pair key, so a later line about it wins over the earlier one. A line
// without any path or symbol (a branch, a version, "无") is keyed by its
// normalized text, so identical free lines collapse too.
func coordKeys(line string) []string {
	t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•0123456789. 、"))
	if t == "" {
		return nil
	}
	paths := coordPath.FindAllString(t, -1)
	keys := make([]string, 0, len(paths)*2+2)
	for _, p := range paths {
		keys = append(keys, coordKeyPath+p)
	}
	for _, sym := range coordSymbol.FindAllString(t, -1) {
		inPath := false
		for _, p := range paths {
			inPath = inPath || strings.Contains(p, sym)
		}
		if inPath {
			continue
		}
		if len(paths) == 0 {
			keys = append(keys, coordKeySymbol+sym)
			continue
		}
		for _, p := range paths {
			keys = append(keys, coordKeyPair+p+"#"+sym)
		}
	}
	if len(keys) == 0 {
		keys = append(keys, coordKeyText+strings.Join(strings.Fields(t), " "))
	}
	return keys
}

// coordKeyCovered reports whether the fresh transcript already carries what the
// key names: the summarizer then re-derives that coordinate from the newer
// material instead of copying the old line forward.
func coordKeyCovered(key, fresh string) bool {
	if fresh == "" {
		return false
	}
	switch {
	case strings.HasPrefix(key, coordKeyPath):
		return strings.Contains(fresh, key[len(coordKeyPath):])
	case strings.HasPrefix(key, coordKeySymbol):
		return strings.Contains(fresh, key[len(coordKeySymbol):])
	case strings.HasPrefix(key, coordKeyPair):
		pair := key[len(coordKeyPair):]
		if i := strings.LastIndex(pair, "#"); i >= 0 {
			return strings.Contains(fresh, pair[:i]) && strings.Contains(fresh, pair[i+1:])
		}
	}
	return false // free text is never re-derived from the transcript
}

// handoffFoldCoords drops the coordinate lines whose every key is written by a
// later line of the same section or covered by the fresh transcript ("" = no
// transcript). Dropped lines are dropped whole: a kept line stays verbatim, so
// paths, symbols and line numbers are never rewritten.
func handoffFoldCoords(lines []string, fresh string) []string {
	keys := make([][]string, len(lines))
	for i, line := range lines {
		keys[i] = coordKeys(line)
	}
	seen := make(map[string]bool, len(lines)*2)
	keep := make([]bool, len(lines))
	for i := len(lines) - 1; i >= 0; i-- { // latest first: the newest line wins
		switch {
		case len(keys[i]) == 0:
			keep[i] = false // blank or bullet-only line
		default:
			drop := true
			for _, k := range keys[i] {
				if !seen[k] && !coordKeyCovered(k, fresh) {
					drop = false
					break
				}
			}
			keep[i] = !drop
			for _, k := range keys[i] {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if keep[i] {
			out = append(out, line)
		}
	}
	return out
}

// handoffCappedTotal is the sum of the section budgets (bytes): what a summary
// body needs at most for the sections that have one.
func handoffCappedTotal() int {
	n := 0
	for _, b := range handoffSectionBudgets {
		n += b
	}
	return n
}

// handoffOutputTokens is the output cap of one summary call: 0, the protocol's
// own default. A cap sized after the section budgets (about 3.3k tokens) left
// reasoning models with an empty summary - their reasoning spends the same
// output allowance, and a re-ask hit the same wall - so every segment break of
// the 2026-10-23 hard evaluation run failed with "empty summary". The package
// size is bounded where it matters instead: the budgets the template states
// and normalizeHandoffBody, which cuts the capped sections of whatever comes
// back and never the protected one.
func handoffOutputTokens() int {
	return 0
}

// completeProtectedNote is what a re-ask after a truncated summary asks for: it
// names the sections to compress and the one that must stay verbatim.
const completeProtectedNote = "\n\n# 注意\n上一次的输出被输出上限截断。请把“" + handoffSectionCoords + " / " + handoffSectionVerified +
	" / " + handoffSectionProgress + " / " + handoffSectionNext + "”压到各自预算内；“" + handoffProtectedSection +
	"”里的硬约束、规则与用户原话必须逐字保留，不得省略或改写。"
