package native

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dangerous delete detection (suag-dtom). A best-effort check for the common
// slips that wipe a home directory or a disk: a recursive delete of /, ~, a
// drive root, the workspace or one of its parents, or of a path that starts
// with a variable which, if empty, leaves the path at the root ("$X/",
// "$dir\*"). It reads POSIX sh, PowerShell and cmd syntax (also nested in
// sh -c, cmd /c, pwsh -Command), but it is not a security boundary: anything
// it cannot read (command substitution, xargs, scripts) passes.

// shellSyntax is the syntax a command line is read with.
type shellSyntax int

const (
	syntaxSh shellSyntax = iota
	syntaxPS
	syntaxCmd
)

// maxShellNesting bounds sh -c / cmd /c / pwsh -Command nesting.
const maxShellNesting = 3

// DangerousDelete reports why command looks like a catastrophic delete, or
// "" when it does not. powershell selects PowerShell syntax (else POSIX sh);
// relative paths resolve against workspace.
func DangerousDelete(command string, powershell bool, workspace string) string {
	reason, _ := DangerousDeleteKind(command, powershell, workspace)
	return reason
}

// DangerousDeleteKind is DangerousDelete that also says whether the target is
// one nobody may confirm away (hard): the user's home directory, the file
// system root (a drive root included), a parent directory of the workspace, or
// a path outside the workspace. The outside check applies to recursive deletes
// only and reads the path lexically (a symlink inside the workspace that points
// out is not followed). The caller refuses a hard hit whatever the approval
// mode; other hits (the workspace itself, a variable-led path) may still be
// confirmed by the person.
func DangerousDeleteKind(command string, powershell bool, workspace string) (reason string, hard bool) {
	syntax := syntaxSh
	if powershell {
		syntax = syntaxPS
	}
	hit := dangerIn(command, syntax, workspace, workspace, 0)
	return hit.reason, hit.hard
}

// DangerousDelete is DangerousDelete with this tool's shell and workspace.
func (t *ShellExecTool) DangerousDelete(command string) string {
	return DangerousDelete(command, t.shell.powershell, t.workspace)
}

// DangerousDeleteKind is DangerousDeleteKind with this tool's shell and workspace.
func (t *ShellExecTool) DangerousDeleteKind(command string) (reason string, hard bool) {
	return DangerousDeleteKind(command, t.shell.powershell, t.workspace)
}

// dangerHit is what the delete check found: why, and whether it is a hard
// refusal (see DangerousDeleteKind). The zero value is no finding.
type dangerHit struct {
	reason string
	hard   bool
}

// dangerIn checks each simple command of a command line. cwd follows the
// line's cd commands ("" once a cd target cannot be resolved), so relative
// targets resolve where they will run.
func dangerIn(command string, syntax shellSyntax, workspace, cwd string, depth int) dangerHit {
	if depth > maxShellNesting {
		return dangerHit{}
	}
	for _, words := range splitCommands(command, syntax) {
		if len(words) > 0 && cdCommands[commandName(words[0])] {
			cwd = changeDir(words[1:], syntax, workspace, cwd)
			continue
		}
		if hit := dangerInWords(words, syntax, workspace, cwd, depth); hit.reason != "" {
			return hit
		}
	}
	return dangerHit{}
}

// cdCommands change the directory later commands of the line run in.
var cdCommands = map[string]bool{"cd": true, "chdir": true, "pushd": true, "set-location": true, "sl": true}

// changeDir returns the directory a cd moves to: its literal or ~ / known
// variable target resolved against cwd, "" when that cannot be told.
func changeDir(args []string, syntax shellSyntax, workspace, cwd string) string {
	var target string
	for _, a := range args {
		if a == "-" {
			return "" // the previous directory
		}
		if !strings.HasPrefix(a, "-") && !strings.EqualFold(a, "/d") {
			target = a
			break
		}
	}
	if syntax != syntaxSh {
		target = strings.ReplaceAll(target, `\`, "/")
	}
	if target == "" || target == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	p, ok := expandPath(target, syntax, workspace)
	if !ok || p == "" || cwd == "" && !filepath.IsAbs(p) {
		return ""
	}
	return resolveTarget(p, cwd)
}

// dangerInWords checks one simple command (its words, quotes removed).
func dangerInWords(words []string, syntax shellSyntax, workspace, cwd string, depth int) dangerHit {
	// Leading sudo / env / VAR=value do not change what runs.
	for len(words) > 0 {
		w := strings.ToLower(words[0])
		if w == "sudo" || w == "doas" || w == "env" || w == "command" || w == "builtin" ||
			syntax == syntaxSh && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") {
			words = words[1:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return dangerHit{}
	}
	name, args := commandName(words[0]), words[1:]
	// A nested shell: check its script with that shell's syntax.
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && i+1 < len(args) {
				return dangerIn(args[i+1], syntaxSh, workspace, cwd, depth+1)
			}
		}
		return dangerHit{}
	case "cmd":
		for i, a := range args {
			if la := strings.ToLower(a); la == "/c" || la == "/k" {
				return dangerIn(strings.Join(args[i+1:], " "), syntaxCmd, workspace, cwd, depth+1)
			}
		}
		return dangerHit{}
	case "pwsh", "powershell":
		for i, a := range args {
			if la := strings.ToLower(a); len(la) >= 2 && strings.HasPrefix("-command", la) {
				return dangerIn(strings.Join(args[i+1:], " "), syntaxPS, workspace, cwd, depth+1)
			}
		}
		return dangerHit{}
	}
	var recursive bool
	var targets []string
	switch {
	case syntax == syntaxSh && name == "rm":
		recursive, targets = rmArgs(args)
	case syntax == syntaxPS && psRemoveCommands[name]:
		recursive, targets = removeItemArgs(args)
	case syntax == syntaxCmd && (name == "rd" || name == "rmdir" || name == "del" || name == "erase"):
		recursive, targets = cmdDeleteArgs(args)
	default:
		return dangerHit{}
	}
	for _, target := range targets {
		if hit := dangerousTarget(target, syntax, workspace, cwd, recursive); hit.reason != "" {
			hit.reason = fmt.Sprintf("%s：%s", strings.Join(words, " "), hit.reason)
			return hit
		}
	}
	return dangerHit{}
}

// psRemoveCommands are Remove-Item and its PowerShell aliases.
var psRemoveCommands = map[string]bool{"remove-item": true, "rm": true, "ri": true, "del": true, "erase": true, "rd": true, "rmdir": true}

// commandName is the lower-case base name without .exe.
func commandName(word string) string {
	base := word[strings.LastIndexAny(word, `/\`)+1:]
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

// rmArgs reads POSIX rm options: -r, -R or --recursive (alone or combined,
// like -rf); every other word is a target ("--" ends the options).
func rmArgs(args []string) (recursive bool, targets []string) {
	options := true
	for _, a := range args {
		switch {
		case options && a == "--":
			options = false
		case options && a == "--recursive":
			recursive = true
		case options && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			recursive = recursive || strings.ContainsAny(a, "rR")
		case options && strings.HasPrefix(a, "--"):
		default:
			targets = append(targets, a)
		}
	}
	return recursive, targets
}

// psValueParams are Remove-Item parameters whose value is not a path.
var psValueParams = []string{"-filter", "-include", "-exclude", "-credential", "-stream", "-erroraction", "-ea", "-warningaction", "-wa"}

// removeItemArgs reads Remove-Item parameters: -Recurse (any unambiguous
// prefix such as -r), -Path / -LiteralPath values and positional paths
// (comma lists split).
func removeItemArgs(args []string) (recursive bool, targets []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || len(a) < 2 {
			targets = append(targets, strings.Split(a, ",")...)
			continue
		}
		param, _, _ := strings.Cut(strings.ToLower(a), ":")
		switch {
		case strings.HasPrefix("-recurse", param):
			recursive = true
		case strings.HasPrefix("-path", param) && len(param) >= 2, strings.HasPrefix("-literalpath", param) && len(param) >= 2,
			param == "-lp", param == "-pspath":
			if i+1 < len(args) {
				i++
				targets = append(targets, strings.Split(args[i], ",")...)
			}
		default:
			for _, p := range psValueParams {
				if param == p {
					i++ // skip the value
					break
				}
			}
		}
	}
	return recursive, targets
}

// cmdDeleteArgs reads rd /s and del /s; other /options are skipped.
func cmdDeleteArgs(args []string) (recursive bool, targets []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "/") {
			recursive = recursive || strings.EqualFold(a, "/s")
			continue
		}
		targets = append(targets, a)
	}
	return recursive, targets
}

// homeAliases name the home directory itself (compared lower-case).
var homeAliases = map[string]bool{
	"~": true, "$home": true, "${home}": true, "$env:userprofile": true, "${env:userprofile}": true,
	"$env:home": true, "${env:home}": true, "%userprofile%": true, "%homedrive%%homepath%": true,
}

// dangerousTarget reports why deleting target is catastrophic: root, home,
// a drive root, the workspace or a parent of it, or a path led by a variable
// that may be empty. A non-recursive delete only counts when it globs one of
// those directories (rm ~/*). Relative targets resolve against cwd.
func dangerousTarget(target string, syntax shellSyntax, workspace, cwd string, recursive bool) dangerHit {
	p := target
	if syntax != syntaxSh {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	if p == "" {
		return dangerHit{}
	}
	glob := strings.HasSuffix(p, "*")
	if !recursive && !glob {
		return dangerHit{}
	}
	base := strings.TrimRight(strings.TrimSuffix(p, "*"), "/")
	what := "删除"
	if glob {
		what = "清空"
	}
	switch {
	case strings.HasPrefix(p, "/") && base == "":
		return dangerHit{what + "根目录 " + p, true}
	case homeAliases[strings.ToLower(base)]:
		return dangerHit{what + "用户主目录 " + target, true}
	case len(base) == 2 && base[1] == ':' && isLetter(base[0]):
		return dangerHit{what + "盘符根目录 " + base, true}
	}
	if name, rest, ok := leadingVar(p, syntax); ok {
		if _, known := knownVarDir(name, workspace); !known && strings.HasPrefix(rest, "/") {
			return dangerHit{reason: fmt.Sprintf("路径以变量 %s 开头，变量为空时会落到根目录", name)}
		}
	}
	expanded, ok := expandPath(base, syntax, workspace)
	if !ok || cwd == "" && !filepath.IsAbs(expanded) && !strings.HasPrefix(expanded, "/") {
		return dangerHit{} // a variable or directory whose value cannot be told
	}
	abs := resolveTarget(expanded, cwd)
	home, _ := os.UserHomeDir()
	ws := filepath.Clean(workspace)
	switch {
	case filepath.Dir(abs) == abs:
		return dangerHit{what + "根目录 " + abs, true}
	case home != "" && samePath(abs, filepath.Clean(home)):
		return dangerHit{what + "用户主目录 " + abs, true}
	case workspace != "" && samePath(abs, ws):
		return dangerHit{reason: what + "整个工作区"}
	case workspace != "" && isParentDir(abs, ws):
		return dangerHit{what + "工作区的上级目录 " + abs, true}
	case workspace != "" && recursive && abs != "" && !isParentDir(ws, abs):
		// Whatever is left is neither the workspace nor its parent: a recursive
		// delete of a path that is not inside the workspace.
		return dangerHit{"递归删除工作区外的路径 " + abs, true}
	}
	return dangerHit{}
}

// expandPath replaces a leading ~ or always-set variable (home, current
// directory); ok is false when the path holds a variable it cannot resolve.
func expandPath(p string, syntax shellSyntax, workspace string) (string, bool) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.ToSlash(home) + p[1:], true
	}
	if name, rest, ok := leadingVar(p, syntax); ok {
		dir, _ := knownVarDir(name, workspace)
		if dir == "" {
			return "", false
		}
		p = dir + rest
	}
	return p, !strings.ContainsAny(p, "$%`")
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// leadingVar splits a path that starts with a variable: $NAME, ${NAME},
// $(cmd) (sh), $env:NAME, ${env:NAME} (PowerShell), %NAME% (cmd).
func leadingVar(p string, syntax shellSyntax) (name, rest string, ok bool) {
	switch {
	case strings.HasPrefix(p, "${") || strings.HasPrefix(p, "$("):
		closer := "}"
		if p[1] == '(' {
			closer = ")"
		}
		if i := strings.Index(p, closer); i > 0 {
			return p[:i+1], p[i+1:], true
		}
	case strings.HasPrefix(p, "$"):
		i := 1
		for i < len(p) && (isLetter(p[i]) || p[i] == '_' || p[i] >= '0' && p[i] <= '9' || p[i] == ':' && syntax == syntaxPS) {
			i++
		}
		if i > 1 {
			return p[:i], p[i:], true
		}
	case strings.HasPrefix(p, "%") && syntax != syntaxSh:
		if i := strings.Index(p[1:], "%"); i > 0 {
			return p[:i+2], p[i+2:], true
		}
	}
	return "", "", false
}

// knownVarDir resolves variables that are always set: the home directory and
// the current directory; other always-set Windows directories are known but
// not resolved ("" with known true).
func knownVarDir(name, workspace string) (dir string, known bool) {
	key := strings.ToLower(strings.Trim(name, "${}%"))
	key = strings.TrimPrefix(key, "env:")
	switch key {
	case "home", "userprofile":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", true
		}
		return filepath.ToSlash(home), true
	case "pwd", "cd":
		return filepath.ToSlash(workspace), true
	case "temp", "tmp", "appdata", "localappdata", "programdata":
		return "", true
	}
	return "", false
}

// resolveTarget makes p absolute against workspace; on Windows a path with a
// leading "/" is on the workspace's drive.
func resolveTarget(p, workspace string) string {
	if p == "" {
		p = "."
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
		p = filepath.VolumeName(workspace) + p
	}
	if !filepath.IsAbs(p) {
		if workspace == "" {
			return ""
		}
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p)
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isParentDir reports whether dir is a proper ancestor of path.
func isParentDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// splitCommands splits a command line into simple commands (at ; & | and
// newlines, and at PowerShell / cmd block brackets), each a list of words
// with quotes removed. $(...), ${...} stay inside their word.
func splitCommands(s string, syntax shellSyntax) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	escape := byte('\\')
	switch syntax {
	case syntaxPS:
		escape = '`'
	case syntaxCmd:
		escape = '^'
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == escape && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		case c == '\'' && syntax != syntaxCmd:
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				j = len(s) - i - 1
			}
			word.WriteString(s[i+1 : i+1+j])
			inWord = true
			i += j + 1
		case c == '"':
			inWord = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == escape && syntax != syntaxCmd && i+1 < len(s) {
					i++
				}
				word.WriteByte(s[i])
			}
		case c == '$' && i+1 < len(s) && (s[i+1] == '(' || s[i+1] == '{'):
			// Keep $(...) / ${...} whole, nested brackets included.
			open, close := s[i+1], byte(')')
			if open == '{' {
				close = '}'
			}
			depth, j := 0, i+1
			for ; j < len(s); j++ {
				if s[j] == open {
					depth++
				} else if s[j] == close {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			end := min(j+1, len(s))
			word.WriteString(s[i:end])
			inWord = true
			i = end - 1
		case c == '#' && !inWord && syntax != syntaxCmd:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			endCommand()
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')' ||
			syntax == syntaxPS && (c == '{' || c == '}'):
			endCommand()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCommand()
	return commands
}
