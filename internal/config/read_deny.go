package axconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// Configuration keys of the sensitive-path read ban (design 2026-11-10 C). Both
// are read-only for `suga config set`, so the model cannot switch the ban off.
const (
	keyReadDeny         = "tools.read_deny"
	keyReadDenyDefaults = "tools.read_deny_defaults"
)

// builtinReadDeny is the default tools.read_deny list: credential files that a
// coding session never needs the model to read. "~" is the user's home and
// "$SUGA_HOME" the suga data directory; both are expanded by ReadDenyPatterns.
// The "!" entries are exceptions and, as in .gitignore, the last match wins.
var builtinReadDeny = []string{
	"**/.env", "**/.env.*",
	"!**/.env.example", "!**/.env.sample", "!**/.env.template",
	"~/.ssh/**", "~/.aws/credentials", "~/.gnupg/**", "~/.netrc",
	"~/.docker/config.json", "~/.git-credentials", "~/.config/gh/hosts.yml",
	"~/.kube/config", "~/.npmrc", "~/.pypirc",
	"$SUGA_HOME/.env",
}

// ReadDenyPatterns returns the effective deny patterns, in match order: the
// built-in list (unless tools.read_deny_defaults is false), the configured
// tools.read_deny entries, then extra (an embedding host's additions, which can
// only add to the list). "~" and "$SUGA_HOME" are expanded to absolute paths
// and every pattern is validated, so a malformed one fails startup instead of
// leaving a file readable.
func ReadDenyPatterns(extra []string) ([]string, error) {
	var raw []string
	if gkconfig.Bool(keyReadDenyDefaults, true) {
		raw = append(raw, builtinReadDeny...)
	}
	raw = append(raw, gkconfig.Strings(keyReadDeny)...)
	// A host can only add: an "!" exception from it could lift a built-in entry.
	for _, entry := range extra {
		if strings.HasPrefix(strings.TrimSpace(entry), "!") {
			return nil, fmt.Errorf("%s: host additions cannot be exceptions: %q", keyReadDeny, entry)
		}
	}
	raw = append(raw, extra...)

	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		pattern, err := expandReadDenyPattern(entry)
		if err != nil {
			return nil, err
		}
		if pattern != "" {
			out = append(out, pattern)
		}
	}
	return out, nil
}

// LoadReadDeny compiles ReadDenyPatterns for the native tools. Compilation
// validates the globs.
func LoadReadDeny(extra []string) (native.ReadDenyList, error) {
	patterns, err := ReadDenyPatterns(extra)
	if err != nil {
		return native.ReadDenyList{}, err
	}
	return native.NewReadDenyList(patterns)
}

// expandReadDenyPattern expands one entry (keeping a leading "!") into a slash
// separated pattern. A pattern with a "/" must be absolute, start with "**/"
// or with "~" / "$SUGA_HOME": a relative path would never match the absolute
// paths the tools judge, so it is rejected rather than ignored in silence.
func expandReadDenyPattern(entry string) (string, error) {
	p := strings.TrimSpace(entry)
	if p == "" {
		return "", nil
	}
	prefix := ""
	if strings.HasPrefix(p, "!") {
		prefix, p = "!", strings.TrimSpace(p[1:])
	}
	if p == "" {
		return "", fmt.Errorf("%s: empty pattern %q", keyReadDeny, entry)
	}
	p = filepath.ToSlash(p)
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("%s: cannot expand ~ in %q: no home directory", keyReadDeny, entry)
		}
		p = globLiteral(home) + p[1:]
	case p == "$SUGA_HOME" || strings.HasPrefix(p, "$SUGA_HOME/"):
		home, err := ResolveSugaHome()
		if err != nil {
			return "", fmt.Errorf("%s: cannot expand $SUGA_HOME in %q: %w", keyReadDeny, entry, err)
		}
		p = globLiteral(home) + strings.TrimPrefix(p, "$SUGA_HOME")
	case strings.Contains(p, "/") && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "**/") && !isDriveAbs(p):
		return "", fmt.Errorf("%s: pattern %q must be absolute, start with ~, $SUGA_HOME or **/, or be a bare file name", keyReadDeny, entry)
	}
	return prefix + p, nil
}

// globLiteral turns a directory path into slash form with the glob meta
// characters escaped, so a home directory like "C:/Users/a[1]" matches itself.
func globLiteral(dir string) string {
	dir = strings.TrimRight(filepath.ToSlash(dir), "/")
	var b strings.Builder
	for _, r := range dir {
		if r == '*' || r == '?' || r == '[' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isDriveAbs reports a Windows drive path such as "C:/dir".
func isDriveAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}
