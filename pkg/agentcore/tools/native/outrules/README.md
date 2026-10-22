# Built-in shell output rules

These files are the rules of the output minimizer (`tools.minimize_output`): a
rule applies when its `match` regex matches the shell command (and, when it sets
one, when `match_output` matches the output); it drops the lines that only say
something passed, keeps what may matter, and shortens what is left. The original
output is always kept under a ref.

## Organisation

One file per language or tool family, all embedded into the binary
(`pkg/agentcore/tools/native/output_rules.go`), which reads them in the fixed
order listed there:

| file | rules |
| --- | --- |
| `go.yaml` | `go-build`, `go-vet`, `go-test` |
| `rust.yaml` | `cargo-test` |
| `js.yaml` | `tsc`, `eslint`, `js-test` (jest, vitest, mocha, the package managers' test scripts) |
| `python.yaml` | `ruff-check`, `mypy`, `pytest` |
| `java.yaml` | `maven`, `gradle` |
| `git.yaml` | `git-log`, `git-diff`, `git-status` |
| `grep.yaml` | `grep` (rg and grep hits) |
| `docker.yaml` | `docker-build`, `docker-compose-build`, `docker-compose-up` |
| `make.yaml` | `make` |
| `gh.yaml` | `gh-readonly` |
| `php.yaml` | `phpunit`, `pest` |

The order matters: every rule whose `match` matches the command is applied in
turn, so it is kept as it was when all rules lived in one `output_rules.yaml`
(the tool rules come last). A new rule goes into the file of its language or
tool; a language that needs none gets no file. Add a file to the order list in
`output_rules.go` (a test fails otherwise).

## Fields

```yaml
rules:
  - name: go-test          # the rule's name (unique; how a user overrides it)
    enabled: true          # false turns this rule (of that name) off
    match: '\bgo test\b'   # regex on the command
    match_output: ''       # optional second condition: the output must hold this
                           # regexp too (a git diff header, say), so a rule stays
                           # out of the way of the same command printing
                           # something else. It is matched against the whole
                           # output, so ^/$ need (?m) - unlike drop, failure,
                           # count, sum and fold below, which see one line
    drop: []               # lines removed (outside a failing block)
    block:                 # per-test grouping: lines matching `body` after
      start: ''            # `start`, or after a `pass` line, belong to that
      body: ''             # test; a `pass` line drops them, a `fail` line keeps
      pass: ''             # them
      fail: ''
    failure: []            # lines that mean something failed (kept; no summary then)
    count: {}              # name -> regex; matching lines are counted for the summary
    sum: {}                # name -> regex with a capture group; the numbers of the
                           # matching lines are added up (e.g. maven's "Tests run")
    fold:                  # runs of repeated noise collapsed into one line: start
      - start: ''          # matches the first line of the run, body each further
        body: ''           # line, text is the line printed instead with
        text: ''           # {lines} = how many lines went, {1}/{name} = a capture
                           # group of the start line (empty text drops the run)
    replace:               # rewrite what the rule kept (after fold/drop/block):
      - pattern: ''        # pattern is a Go regexp compiled in multi-line mode,
        with: ''           # with is the replacement ($1 / ${name} = capture group)
    group_by: ''           # regexp with a capture group (a named one wins): the
                           # captured text is the group key, neighbouring lines
                           # sharing it merge into the whole match as a group
                           # header plus the rest of each line, indented
    head: 0                # keep the first N lines of the rule's result
    tail: 0                # keep the last N lines of it (the lines in between
                           # become "[... N lines omitted ...]")
    min_bytes: 0           # this rule's own gates: 0 (or omitted) leaves the
    min_saving: 0          # global ones (600 bytes, 0.2) in place
    summary: ''            # printed when nothing failed and a count is > 0;
                           # {name} = count, a [part] whose counts are all 0 is
                           # left out
```

## How a rule shapes a result

1. `fold` blocks collapse runs of repeated noise into one line.
2. `drop`, `block`, `failure` and `count`/`sum` decide what is kept and gather
   the numbers of the summary.
3. `replace` rewrites the text that is kept: its patterns are compiled in
   multi-line mode, so `^`/`$` are line anchors (a pattern written per line
   behaves like one) and a pattern may span lines by writing `\n` - `.` still
   does not match a newline, add `(?s)` for that. Every match of a pattern is
   replaced (a literal `$` in `with` is written `$$`), and the items run in
   order, so a later one sees what an earlier one produced.
4. `group_by` merges the neighbouring lines that share a key. A key with a
   single line keeps its own shape: grouping it would only add a line.
5. `head`/`tail` keep the first and last N lines, the rest becomes a marker.
6. `summary` is appended last, so step 5 never cuts it.
7. The general steps follow (minimize.go): runs of three or more identical lines
   fold, and a result of more than 300 lines keeps its head (2/3) and tail (1/3).

The result is only handed to the model when it is shorter than the original
output and saves at least the required share of it, so a rule can reshape text
freely - it never makes the output longer (a rule that only lengthens is simply
not applied). By default an output below 600 bytes is left alone and tidying has
to save 20% to be worth it; `min_bytes` and `min_saving` let a rule ask for
less (0 = no such gate). Of the rules matching one command the most permissive
value wins, and a command that pipes its output (`| grep`, `| Select-String`,
`| tail` ...) gets no rule at all: it already shows what the model asked for.

## Overriding

Users override or add rules by name (a rule with the name of a built-in one
replaces it, a new name is appended; later files win):

- `$SUGA_HOME/output-rules.yaml`
- `<repo>/.suga/output-rules.yaml`

Both hold the same `rules:` shape, `enabled: false` turns a built-in rule off.
