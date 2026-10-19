package native

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// outputRuleFixture is made from the normal text emitted by each tool. The
// progress lines are deliberately unique so the generic repeat/line-cap passes
// cannot make the reverse check pass without the named rule.
type outputRuleFixture struct {
	name, rule, command string
	success, failure    string
	keepSuccess         string
	keepFailure         []string
}

func numberedOutput(format string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, format+"\n", i)
	}
	return b.String()
}

func withExit(output string, code int) string {
	return output + fmt.Sprintf("[exit code: %d]", code)
}

// These samples are based on the tools' documented human-readable output. Sources:
// https://www.typescriptlang.org/docs/handbook/compiler-options.html,
// https://eslint.org/docs/latest/use/formatters/, https://docs.astral.sh/ruff/
// (diagnostics), https://mypy.readthedocs.io/en/stable/command_line.html,
// https://go.dev/doc/compile, https://docs.docker.com/build/buildkit/,
// https://docs.docker.com/compose/, https://www.gnu.org/software/make/manual/,
// https://cli.github.com/manual/, https://docs.phpunit.de/ and https://pestphp.com/.
// The line forms are intentionally real, rather than synthetic machine-readable
// formats.
func TestOutputRulesNewCommands(t *testing.T) {
	cases := []outputRuleFixture{
		{
			name: "tsc --noEmit", rule: "tsc", command: "tsc --noEmit",
			success:     numberedOutput("TSFILE: /work/src/file%d.js", 40),
			failure:     numberedOutput("TSFILE: /work/src/file%d.js", 40) + "src/app.ts:1:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n",
			keepSuccess: "tsc: succeeded", keepFailure: []string{"src/app.ts:1:7 - error TS2322", "Type 'string' is not assignable"},
		},
		{
			name: "eslint", rule: "eslint", command: "npx eslint .",
			success:     numberedOutput("✔ /work/src/file%d.js", 40),
			failure:     numberedOutput("✔ /work/src/file%d.js", 40) + "/work/src/app.js:1:1: error Unexpected var. (no-var)\n✖ 1 problem (1 error, 0 warnings)\n",
			keepSuccess: "eslint: succeeded", keepFailure: []string{"/work/src/app.js:1:1: error Unexpected var", "✖ 1 problem"},
		},
		{
			name: "ruff check", rule: "ruff-check", command: "ruff check .",
			success:     numberedOutput("src/module%d.py", 40) + "All checks passed!\n",
			failure:     numberedOutput("src/module%d.py", 40) + "src/app.py:1:1: E501 Line too long (90 > 88)\nFound 1 error.\n",
			keepSuccess: "ruff check: succeeded", keepFailure: []string{"src/app.py:1:1: E501", "Found 1 error."},
		},
		{
			name: "mypy", rule: "mypy", command: "mypy src",
			success:     numberedOutput("LOG: checking module%d", 40) + "Success: no issues found in 40 source files\n",
			failure:     numberedOutput("LOG: checking module%d", 40) + "src/app.py:4:5: error: Incompatible types in assignment [assignment]\nFound 1 error in 1 file (checked 40 source files)\n",
			keepSuccess: "mypy: succeeded", keepFailure: []string{"src/app.py:4:5: error: Incompatible types", "Found 1 error"},
		},
		{
			name: "go build", rule: "go-build", command: "go build ./...",
			success:     numberedOutput("go: downloading example.com/mod%d v1.0.0", 40),
			failure:     numberedOutput("go: downloading example.com/mod%d v1.0.0", 40) + "# example.com/app\nmain.go:12:3: undefined: Missing\n",
			keepSuccess: "go build: succeeded", keepFailure: []string{"# example.com/app", "main.go:12:3: undefined: Missing"},
		},
		{
			name: "go vet", rule: "go-vet", command: "go vet ./...",
			success:     numberedOutput("go: downloading example.com/mod%d v1.0.0", 40),
			failure:     numberedOutput("go: downloading example.com/mod%d v1.0.0", 40) + "# example.com/app\nmain.go:12:3: printf: fmt.Println call has possible formatting directive %s\n",
			keepSuccess: "go vet: succeeded", keepFailure: []string{"main.go:12:3: printf:", "possible formatting directive"},
		},
		{
			name: "docker build", rule: "docker-build", command: "docker build .",
			success:     numberedOutput(" => [%d/40] RUN build-step", 40),
			failure:     numberedOutput(" => [%d/40] RUN build-step", 40) + " => ERROR [3/40] RUN make\nDockerfile:7\nERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully\n",
			keepSuccess: "docker build: succeeded", keepFailure: []string{"=> ERROR [3/40] RUN make", "Dockerfile:7", "failed to solve"},
		},
		{
			name: "docker compose build", rule: "docker-compose-build", command: "docker compose build",
			success:     numberedOutput(" => [%d/40] exporting layer", 40),
			failure:     numberedOutput(" => [%d/40] exporting layer", 40) + " => ERROR [web 3/40] RUN make\nDockerfile:7\nERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully\n",
			keepSuccess: "docker compose build: succeeded", keepFailure: []string{"=> ERROR [web 3/40] RUN make", "Dockerfile:7", "failed to solve"},
		},
		{
			name: "docker compose up", rule: "docker-compose-up", command: "docker compose up",
			success:     numberedOutput("✔ Container app-%d Started", 40),
			failure:     numberedOutput("✔ Container app-%d Started", 40) + "Error response from daemon: driver failed programming external connectivity on endpoint app-1\n",
			keepSuccess: "docker compose up: succeeded", keepFailure: []string{"Error response from daemon", "failed programming external connectivity"},
		},
		{
			name: "make", rule: "make", command: "make",
			success:     numberedOutput("make[1]: Entering directory '/work/sub%d'", 20) + numberedOutput("make[1]: Leaving directory '/work/sub%d'", 20),
			failure:     numberedOutput("make[1]: Entering directory '/work/sub%d'", 20) + numberedOutput("make[1]: Leaving directory '/work/sub%d'", 20) + "make[1]: *** [Makefile:12: test] Error 1\nmake: *** [Makefile:4: all] Error 2\n",
			keepSuccess: "make: succeeded", keepFailure: []string{"make[1]: *** [Makefile:12: test] Error 1", "make: *** [Makefile:4: all] Error 2"},
		},
		{
			name: "gh read-only", rule: "gh-readonly", command: "gh run view --log-failed 123",
			success:     numberedOutput("Fetching run metadata page %d", 40) + "View this run on GitHub: https://github.com/example/app/actions/runs/123\n",
			failure:     numberedOutput("Fetching run metadata page %d", 40) + "Error: HTTP 500\n",
			keepSuccess: "gh: succeeded", keepFailure: []string{"Error: HTTP 500"},
		},
		{
			name: "phpunit", rule: "phpunit", command: "vendor/bin/phpunit",
			success:     numberedOutput("PASS Test%d", 60) + "OK (40 tests, 40 assertions)\n",
			failure:     numberedOutput("PASS Test%d", 60) + "FAILURES!\nTests/Feature/ExampleTest.php:12\nFailed asserting that 1 is identical to 2.\n",
			keepSuccess: "phpunit: succeeded", keepFailure: []string{"FAILURES!", "Tests/Feature/ExampleTest.php:12", "Failed asserting"},
		},
		{
			name: "pest", rule: "pest", command: "pest",
			success:     numberedOutput("✓ it passes %d", 40) + "Tests: 40 passed\n",
			failure:     numberedOutput("✓ it passes %d", 40) + "⨯ it fails\ntests/Feature/ExampleTest.php:12\nExpected: 3\nReceived: 4\nTests: 1 failed, 40 passed\n",
			keepSuccess: "pest: succeeded", keepFailure: []string{"⨯ it fails", "tests/Feature/ExampleTest.php:12", "Expected: 3", "Received: 4"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" success", func(t *testing.T) {
			tidy, ok := MinimizeToolOutput(shellCall(tc.command), withExit(tc.success, 0))
			assert.Require(t, assert.True(t, ok))
			assert.StrContains(t, tidy, tc.keepSuccess)
		})
		t.Run(tc.name+" failure", func(t *testing.T) {
			tidy, ok := MinimizeToolOutput(shellCall(tc.command), withExit(tc.failure, 1))
			assert.Require(t, assert.True(t, ok))
			for _, line := range tc.keepFailure {
				assert.StrContains(t, tidy, line)
			}
		})
		t.Run(tc.name+" reverse verification", func(t *testing.T) {
			without := make([]OutputRule, 0, len(DefaultOutputRules()))
			for _, rule := range DefaultOutputRules() {
				if rule.Name != tc.rule {
					without = append(without, rule)
				}
			}
			_, ok := NewOutputMinimizer(without).Minimize(shellCall(tc.command), withExit(tc.success, 0))
			assert.False(t, ok)
		})
	}
}
