package native

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// The built-in output rules live one file per language under rules/ (embedded
// below into the binary); rules/README.md documents the fields and how users
// override a rule.
//
//go:embed rules/*.yaml
var builtinRuleFiles embed.FS

// builtinRuleFileOrder is the order the rule files are read in: it is the order
// the rules had when they lived in one output_rules.yaml (go, rust, js, python,
// java, whose file holds maven and gradle), with git/grep and the later tool
// families (docker, make, gh and PHP test runners) appended. The order matters -
// every rule whose match matches the command is applied in turn, so it decides
// the result on a command several rules match -
// and the loader's test keeps this list and the embedded files in step.
var builtinRuleFileOrder = []string{
	"rules/go.yaml",
	"rules/rust.yaml",
	"rules/js.yaml",
	"rules/python.yaml",
	"rules/java.yaml",
	"rules/git.yaml",
	"rules/grep.yaml",
	"rules/docker.yaml",
	"rules/make.yaml",
	"rules/gh.yaml",
	"rules/php.yaml",
}

// OutputRule tidies the output of the shell commands it matches; see
// rules/README.md for the fields. Rules are data so new tools and languages are
// added without code (omp's shell minimizer works the same way).
type OutputRule struct {
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"` // nil = on
	Match   string `yaml:"match"`
	// MatchOutput, when set, requires the output to hold this pattern as well: a
	// rule written for one output shape (the header of a git diff, say) then
	// stays out of the way of the same command printing something else.
	MatchOutput string   `yaml:"match_output"`
	Drop        []string `yaml:"drop"`
	Block       struct {
		Start string `yaml:"start"`
		Body  string `yaml:"body"`
		Pass  string `yaml:"pass"`
		Fail  string `yaml:"fail"`
	} `yaml:"block"`
	Failure []string          `yaml:"failure"`
	Count   map[string]string `yaml:"count"`
	Sum     map[string]string `yaml:"sum"`
	Fold    []FoldBlock       `yaml:"fold"`
	// Replace rewrites what the rule keeps (after drop/fold/block, before
	// group_by); see ReplaceBlock.
	Replace []ReplaceBlock `yaml:"replace"`
	// GroupBy is a regexp with a capture group holding a group key: neighbouring
	// lines sharing the key merge into one header line and the rest of every
	// line, indented (a grep hit's file path is printed once).
	GroupBy string `yaml:"group_by"`
	// Head and Tail keep the first/last N lines of the rule's result, the lines
	// in between go; they act before the global line cap (minimize.go).
	Head int `yaml:"head"`
	Tail int `yaml:"tail"`
	// MinBytes and MinSaving lower the global gates for this rule (of all rules
	// matching a command the most permissive value wins); nil = global default.
	MinBytes  *int     `yaml:"min_bytes"`
	MinSaving *float64 `yaml:"min_saving"`
	Summary   string   `yaml:"summary"`

	// Source is the layer the rule came from (\"builtin\", or the rule file's
	// path); the loaders fill it and the manifest and `suga inspect rules`
	// report it.
	Source string `yaml:"-"`

	compiled *compiledRule
}

// ReplaceBlock is one `replace` item: pattern is a Go regexp compiled in
// multi-line mode (so ^/$ anchor on a line while a pattern may still cross
// lines with \n) and with replaces every match of it, with $1 / ${name}
// standing for a capture group.
type ReplaceBlock struct {
	Pattern string `yaml:"pattern"`
	With    string `yaml:"with"`
}

// FoldBlock collapses a run of lines repeating the same noise into one line:
// start matches its first line, body every following line of the run, and text
// is what replaces the run ({lines} = the number of lines folded, {1}/{name} =
// a capture group of the start line; empty text drops the whole run). Maven's
// effective-model warnings are the case it was added for.
type FoldBlock struct {
	Start string `yaml:"start"`
	Body  string `yaml:"body"`
	Text  string `yaml:"text"`
}

type compiledRule struct {
	match, matchOutput      *regexp.Regexp
	drop, failure           []*regexp.Regexp
	start, body, pass, fail *regexp.Regexp
	countNames              []string
	counts                  map[string]*regexp.Regexp
	sums                    map[string]*regexp.Regexp
	folds                   []compiledFold
	replaces                []compiledReplace
	group                   *regexp.Regexp
	groupKey                int // submatch index holding the group_by key
	head, tail              int
}

// compiledFold is a compiled FoldBlock.
type compiledFold struct {
	start, body *regexp.Regexp
	names       []string // the start regexp's group names ({name} in the text)
	text        string
}

// compiledReplace is a compiled ReplaceBlock.
type compiledReplace struct {
	re   *regexp.Regexp
	with string
}

type outputRuleFile struct {
	Rules []OutputRule `yaml:"rules"`
}

// BuiltinSource marks a rule that ships with the binary (the embedded rule
// files); a user or project rule file carries its path instead.
const BuiltinSource = "builtin"

// DefaultOutputRules returns the built-in rules (go build/vet/test, cargo test,
// TypeScript/ESLint, Ruff/Mypy, Jest/Vitest, pytest, maven, gradle and the
// command-line tool families), read from the embedded rule files in
// builtinRuleFileOrder. A rule file that does not parse is a build error: the
// rules ship with the binary.
func DefaultOutputRules() []OutputRule {
	var rules []OutputRule
	for _, name := range builtinRuleFileOrder {
		data, err := builtinRuleFiles.ReadFile(name)
		if err != nil {
			panic("native: built-in output rules: " + err.Error())
		}
		file, err := parseOutputRules(data)
		if err != nil {
			panic("native: built-in output rules " + name + ": " + err.Error())
		}
		for i := range file {
			file[i].Source = BuiltinSource
		}
		rules = append(rules, file...)
	}
	return rules
}

// LoadOutputRules returns the built-in rules merged with the rule files in
// order (later files win): a rule with the name of an earlier one replaces
// it, a new name is appended. Missing files are skipped; a file that does not
// parse or holds a broken rule is reported and skipped as a whole.
func LoadOutputRules(paths ...string) ([]OutputRule, []error) {
	rules := DefaultOutputRules()
	var errs []error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		extra, err := parseOutputRules(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		// A file's rules keep the file's path as their layer, so the manifest
		// and `suga inspect rules` show where a rule came from.
		for i := range extra {
			extra[i].Source = path
		}
		rules = mergeOutputRules(rules, extra)
	}
	var out []OutputRule
	for _, r := range rules {
		if r.Enabled != nil && !*r.Enabled {
			continue
		}
		out = append(out, r)
	}
	return out, errs
}

// BuiltinRuleFile is one embedded rule file as the binary carries it.
type BuiltinRuleFile struct {
	// Name is the file's embedded path, e.g. rules/go.yaml.
	Name string
	// YAML is the file's raw content, so `suga inspect rules --builtin` prints
	// exactly what ships and a template can be written from it.
	YAML []byte
}

// BuiltinOutputRuleFiles returns the embedded output rule files in the order
// they are read in, so a rule writer sees the shipped rules verbatim.
func BuiltinOutputRuleFiles() []BuiltinRuleFile {
	out := make([]BuiltinRuleFile, 0, len(builtinRuleFileOrder))
	for _, name := range builtinRuleFileOrder {
		data, err := builtinRuleFiles.ReadFile(name)
		if err != nil {
			panic("native: built-in output rules: " + err.Error())
		}
		out = append(out, BuiltinRuleFile{Name: name, YAML: data})
	}
	return out
}

// mergeOutputRules merges a file's rules into the loaded ones: a same-name rule
// is replaced, a new name is appended.
func mergeOutputRules(base, extra []OutputRule) []OutputRule {
	for _, r := range extra {
		replaced := false
		for i := range base {
			if base[i].Name == r.Name {
				base[i], replaced = r, true
				break
			}
		}
		if !replaced {
			base = append(base, r)
		}
	}
	return base
}

// parseOutputRules decodes and compiles a rule file; every regex must
// compile and every enabled rule needs a name and a match.
func parseOutputRules(data []byte) ([]OutputRule, error) {
	var file outputRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for i := range file.Rules {
		r := &file.Rules[i]
		if r.Name == "" {
			return nil, fmt.Errorf("rules[%d]: name is required", i)
		}
		if r.Enabled != nil && !*r.Enabled {
			continue // only switches a rule of that name off
		}
		c, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.Name, err)
		}
		r.compiled = c
	}
	return file.Rules, nil
}

func compileRule(r *OutputRule) (*compiledRule, error) {
	if r.Match == "" {
		return nil, errors.New("match is required")
	}
	c := &compiledRule{counts: map[string]*regexp.Regexp{}, sums: map[string]*regexp.Regexp{}}
	var err error
	one := func(expr string) *regexp.Regexp {
		if expr == "" || err != nil {
			return nil
		}
		var re *regexp.Regexp
		if re, err = regexp.Compile(expr); err != nil {
			err = fmt.Errorf("%q: %w", expr, err)
		}
		return re
	}
	many := func(exprs []string) []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, e := range exprs {
			if re := one(e); re != nil {
				out = append(out, re)
			}
		}
		return out
	}
	c.match = one(r.Match)
	c.matchOutput = one(r.MatchOutput)
	c.drop, c.failure = many(r.Drop), many(r.Failure)
	c.start, c.body, c.pass, c.fail = one(r.Block.Start), one(r.Block.Body), one(r.Block.Pass), one(r.Block.Fail)
	for name, expr := range r.Count {
		c.counts[name] = one(expr)
		c.countNames = append(c.countNames, name)
	}
	// A summed value is rendered like a count: one name, one number.
	for name, expr := range r.Sum {
		c.sums[name] = one(expr)
		if _, counted := c.counts[name]; !counted {
			c.countNames = append(c.countNames, name)
		}
	}
	for _, f := range r.Fold {
		fold := compiledFold{start: one(f.Start), body: one(f.Body), text: f.Text}
		if fold.start != nil {
			fold.names = fold.start.SubexpNames()
			c.folds = append(c.folds, fold)
		}
	}
	for _, item := range r.Replace {
		if item.Pattern == "" {
			return nil, errors.New("replace: pattern is required")
		}
		// Multi-line mode, so ^/$ anchor on a line: a pattern reads like a
		// per-line one and may still span lines by writing \n.
		re, reErr := regexp.Compile("(?m)" + item.Pattern)
		if reErr != nil {
			return nil, fmt.Errorf("replace %q: %w", item.Pattern, reErr)
		}
		c.replaces = append(c.replaces, compiledReplace{re: re, with: item.With})
	}
	if r.GroupBy != "" {
		re, reErr := regexp.Compile(r.GroupBy)
		if reErr != nil {
			return nil, fmt.Errorf("group_by %q: %w", r.GroupBy, reErr)
		}
		key := groupKeyIndex(re)
		if key < 1 {
			return nil, fmt.Errorf("group_by %q: needs a capture group for the group key", r.GroupBy)
		}
		c.group, c.groupKey = re, key
	}
	if r.Head < 0 || r.Tail < 0 {
		return nil, errors.New("head and tail must not be negative")
	}
	c.head, c.tail = r.Head, r.Tail
	if r.MinBytes != nil && *r.MinBytes < 0 {
		return nil, errors.New("min_bytes must not be negative")
	}
	if r.MinSaving != nil && (*r.MinSaving < 0 || *r.MinSaving > 1) {
		return nil, errors.New("min_saving must be between 0 and 1")
	}
	return c, err
}

// groupKeyIndex returns the submatch index whose text is the group_by key: the
// first named group when the regexp has one, else the first capture group (0
// when the regexp captures nothing at all).
func groupKeyIndex(re *regexp.Regexp) int {
	for i, name := range re.SubexpNames() {
		if i > 0 && name != "" {
			return i
		}
	}
	if re.NumSubexp() > 0 {
		return 1
	}
	return 0
}

// sumValue returns the number in the first capture group of re when the line
// matches and holds one; ok is false otherwise.
func sumValue(re *regexp.Regexp, line string) (int, bool) {
	m := re.FindStringSubmatch(line)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// apply runs the rule over lines: per-test blocks of passed tests and the
// drop lines go, the rest is shaped by the rule's replace, group_by and
// head/tail, and the summary line is appended last - so it is never cut away.
func (c *compiledRule) apply(lines []string, summary string) []string {
	out, counts, failed := c.filter(lines)
	out = c.applyReplace(out)
	out = c.groupBy(out)
	out = c.headTail(out)
	if failed || summary == "" {
		return out
	}
	if text := renderSummary(summary, c.countNames, counts); text != "" {
		out = append(out, text)
	}
	return out
}

// filter keeps what matters: per-test blocks of passed tests and the drop
// lines go, the counts and sums of the retained lines are gathered for the
// summary, and failed reports whether anything failed (no summary then).
func (c *compiledRule) filter(lines []string) (out []string, counts map[string]int, failed bool) {
	lines = c.foldNoise(lines)
	var pending []string
	counts = map[string]int{}
	inPassed := false
	flush := func() {
		out = append(out, pending...)
		pending = pending[:0]
	}
	matchAny := func(res []*regexp.Regexp, l string) bool {
		for _, re := range res {
			if re.MatchString(l) {
				return true
			}
		}
		return false
	}
	for _, l := range lines {
		for name, re := range c.counts {
			if re != nil && re.MatchString(l) {
				counts[name]++
			}
		}
		for name, re := range c.sums {
			if re == nil {
				continue
			}
			if n, ok := sumValue(re, l); ok {
				counts[name] += n
			}
		}
		isFail := matchAny(c.failure, l)
		if isFail {
			failed = true
		}
		inBody := c.body != nil && c.body.MatchString(l)
		switch {
		case c.pass != nil && c.pass.MatchString(l):
			pending, inPassed = pending[:0], true // the passed test's own output goes too
			if matchAny(c.drop, l) {
				continue
			}
			out = append(out, l)
			continue
		case inBody && inPassed:
			continue
		case inBody && !isFail && c.start != nil:
			pending = append(pending, l) // decided by the test's result line
			continue
		}
		inPassed = false
		if c.fail != nil && c.fail.MatchString(l) {
			flush()
			out = append(out, l)
			continue
		}
		if !isFail && matchAny(c.drop, l) {
			if c.start != nil && c.start.MatchString(l) {
				flush() // a new test starts: an undecided block stays
			}
			continue
		}
		flush()
		out = append(out, l)
	}
	flush()
	return out, counts, failed
}

// foldNoise replaces every fold block - its start line and the following lines
// matching its body - with the block's text line. Rules without fold blocks
// return the lines unchanged.
func (c *compiledRule) foldNoise(lines []string) []string {
	if len(c.folds) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		found, n, m := c.foldAt(lines, i)
		if n == 0 {
			out = append(out, lines[i])
			i++
			continue
		}
		if found.text != "" {
			out = append(out, renderFoldText(found, n, m))
		}
		i += n
	}
	return out
}

// foldAt returns the first fold block whose start matches lines[i], how many
// lines it swallows (0 when no block starts there) and the start line's
// capture groups. A following line joins the run while it matches the block's
// body.
func (c *compiledRule) foldAt(lines []string, i int) (compiledFold, int, []string) {
	for _, fold := range c.folds {
		if !fold.start.MatchString(lines[i]) {
			continue
		}
		n := 1
		for fold.body != nil && i+n < len(lines) && fold.body.MatchString(lines[i+n]) {
			n++
		}
		return fold, n, fold.start.FindStringSubmatch(lines[i])
	}
	return compiledFold{}, 0, nil
}

var optionalPart = regexp.MustCompile(`\[([^\[\]]*)\]`)

// renderSummary fills the {name} counts into summary; a [part] whose counts
// are all 0 is left out (the brackets of a kept part go). "" when every count
// is 0.
func renderSummary(summary string, names []string, counts map[string]int) string {
	positive := false
	for _, name := range names {
		positive = positive || counts[name] > 0
	}
	if !positive {
		return ""
	}
	fill := func(text string) (string, bool) {
		any := false
		for _, name := range names {
			if strings.Contains(text, "{"+name+"}") {
				any = any || counts[name] > 0
				text = strings.ReplaceAll(text, "{"+name+"}", fmt.Sprint(counts[name]))
			}
		}
		return text, any
	}
	summary = optionalPart.ReplaceAllStringFunc(summary, func(part string) string {
		if text, any := fill(part[1 : len(part)-1]); any {
			return text
		}
		return ""
	})
	text, _ := fill(summary)
	return text
}
