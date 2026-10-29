package native

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

const regexp2FileTimeout = 200 * time.Millisecond

// grepMatcher hides the two regex engines behind the same line-matching API.
type grepMatcher interface {
	MatchString(string) (bool, error)
}

type re2Matcher struct{ re *regexp.Regexp }

func (m re2Matcher) MatchString(s string) (bool, error) { return m.re.MatchString(s), nil }

type regexp2Matcher struct{ re *regexp2.Regexp }

func (m regexp2Matcher) MatchString(s string) (bool, error) { return m.re.MatchString(s) }

// compileGrepPattern prefers RE2 and only falls back for syntax that RE2
// deliberately does not implement. The fallback is bounded per file to keep
// backtracking patterns from monopolising a search.
func compileGrepPattern(pattern string, ignoreCase, literal bool) (grepMatcher, bool, error) {
	if literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	re2Pattern := pattern
	if ignoreCase {
		re2Pattern = "(?i)" + re2Pattern
	}
	if re, err := regexp.Compile(re2Pattern); err == nil {
		return re2Matcher{re: re}, false, nil
	} else if !regexp2FallbackSyntax(pattern) {
		return nil, false, invalidGrepPattern(pattern, err)
	}

	opts := regexp2.RegexOptions(regexp2.RE2 | regexp2.Multiline)
	if ignoreCase {
		opts |= regexp2.IgnoreCase
	}
	re, err := regexp2.Compile(pattern, opts)
	if err != nil {
		return nil, false, invalidGrepPattern(pattern, err)
	}
	re.MatchTimeout = regexp2FileTimeout
	return regexp2Matcher{re: re}, true, nil
}

func regexp2FallbackSyntax(pattern string) bool {
	for _, token := range []string{"(?=", "(?!", "(?<=", "(?<!"} {
		if strings.Contains(pattern, token) {
			return true
		}
	}
	for i := 0; i+1 < len(pattern); i++ {
		if pattern[i] == '\\' && pattern[i+1] >= '1' && pattern[i+1] <= '9' {
			return true
		}
	}
	return false
}

func invalidGrepPattern(pattern string, err error) error {
	suggestion := "use RE2 syntax (for example, escape special characters and keep parentheses balanced)"
	if strings.ContainsAny(pattern, "([.{*") && !strings.HasPrefix(pattern, "(?") {
		suggestion = "if you meant to search these characters literally, set literal: true"
	}
	return fmt.Errorf("file_grep: invalid pattern: %w; fix: %s", err, suggestion)
}

// grepExcludeHint is what a caller reads when an exclude value is neither a
// regexp nor a file wildcard. It must not repeat the invalidGrepPattern advice:
// literal only applies to pattern, so pointing exclude at it sends the model round
// the same loop (sg_c26ak287eqpm: 24 calls, all the same wildcard).
const grepExcludeHint = "exclude leaves lines out by a regular expression (literal does not apply to it); " +
	"to leave whole files out use glob: [\"!*_test.go\"]"

// grepExcludeGlob reports whether an exclude value RE2 rejected still reads as a
// file wildcard, and returns that wildcard. It is only consulted after the regexp
// compile failed, so an exclude that means lines (".*", "^Test") never changes
// meaning, and regexp-only syntax is not taken for a wildcard either.
func grepExcludeGlob(pattern string) (string, bool) {
	if !strings.ContainsAny(pattern, "*?") || strings.ContainsAny(pattern, "([{|\\^$+ \t\r\n") {
		return "", false
	}
	return strings.TrimSpace(pattern), true
}

type grepGlob struct {
	include []string
	exclude []string
}

func parseGrepGlobs(value any) grepGlob {
	var patterns []string
	switch v := value.(type) {
	case string:
		if v != "" {
			patterns = []string{v}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				patterns = append(patterns, s)
			}
		}
	case []string:
		patterns = append(patterns, v...)
	}
	var out grepGlob
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") && len(pattern) > 1 {
			out.exclude = append(out.exclude, strings.TrimPrefix(pattern, "!"))
		} else {
			out.include = append(out.include, pattern)
		}
	}
	return out
}

func (g grepGlob) match(start readTarget, rel string) bool {
	for _, pattern := range g.exclude {
		if start.matchUnder(pattern, rel) {
			return false
		}
	}
	if len(g.include) == 0 {
		return true
	}
	for _, pattern := range g.include {
		if start.matchUnder(pattern, rel) {
			return true
		}
	}
	return false
}
