package native

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// repeatLines renders format (with the index as its only verb) n times.
func repeatLines(n int, format string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, format+"\n", i)
	}
	return b.String()
}

// mavenPOMWarnings is the effective-model warning block Maven prints for a POM
// with a missing plugin version (7 lines, all starting with [WARNING]).
const mavenPOMWarnings = "[WARNING] Some problems were encountered while building the effective model for com.example:demo:jar:1.0-SNAPSHOT\n" +
	"[WARNING] 'build.plugins.plugin.version' for org.apache.maven.plugins:maven-compiler-plugin is missing. @ line 25, column 15\n" +
	"[WARNING] \n" +
	"[WARNING] It is highly recommended to fix these problems because they threaten the stability of your build.\n" +
	"[WARNING] \n" +
	"[WARNING] For this reason, future Maven versions might no longer support building such malformed projects.\n" +
	"[WARNING] \n"

func TestOutputRulesBuiltin(t *testing.T) {
	cases := []struct {
		name, command, output string
		want, notWant         []string
	}{
		{
			name:    "go test without -v: packages only",
			command: "go test ./...",
			output:  repeatLines(60, "?   \tgithub.com/x/cmd%d\t[no test files]") + repeatLines(10, "ok  \tgithub.com/x/pkg%d\t0.1s") + "[exit code: 0]",
			want:    []string{"ok  \tgithub.com/x/pkg9\t0.1s\n[exit code: 0]\ngo test: 10 package(s) ok"},
			notWant: []string{"no test files", "test(s) passed"},
		},
		{
			name:    "cargo test passed",
			command: "cargo test",
			output: repeatLines(5, "   Compiling dep%d v0.1.0") + "    Finished test target(s) in 1.2s\n\nrunning 30 tests\n" +
				repeatLines(30, "test tests::case_%d ... ok") + "\ntest result: ok. 30 passed; 0 failed; 0 ignored\n[exit code: 0]",
			want:    []string{"test result: ok. 30 passed", "cargo test: 30 test(s) passed"},
			notWant: []string{"case_0 ... ok", "Compiling"},
		},
		{
			name:    "cargo test failed",
			command: "cd crate && cargo test --lib",
			output: "running 30 tests\n" + repeatLines(29, "test tests::case_%d ... ok") + "test tests::bad ... FAILED\n\nfailures:\n\n" +
				"---- tests::bad stdout ----\nthread 'tests::bad' panicked at src/lib.rs:10:5:\nassertion `left == right` failed\n  left: 1\n right: 2\n\n" +
				"test result: FAILED. 29 passed; 1 failed\n[exit code: 101]",
			want:    []string{"test tests::bad ... FAILED", "panicked at src/lib.rs:10:5", "  left: 1", "test result: FAILED"},
			notWant: []string{"case_0 ... ok", "test(s) passed"},
		},
		{
			name:    "jest passed",
			command: "npx jest",
			output:  " PASS  src/a.test.js\n  sum\n" + repeatLines(30, "    ✓ adds case %d (3 ms)") + "\nTest Suites: 1 passed, 1 total\nTests:       30 passed, 30 total\n[exit code: 0]",
			want:    []string{"Tests:       30 passed, 30 total", "tests: 30 passed"},
			notWant: []string{"✓", "PASS  src/a.test.js"},
		},
		{
			name:    "npm test with a failure",
			command: "npm test",
			output: " FAIL  src/b.test.js\n" + repeatLines(29, "    ✓ case %d (1 ms)") + "    ✕ adds wrong (2 ms)\n\n  ● sum › adds wrong\n\n" +
				"    expect(received).toBe(expected)\n    Expected: 3\n    Received: 4\n\nTests:       1 failed, 29 passed, 30 total\n[exit code: 1]",
			want:    []string{"FAIL  src/b.test.js", "✕ adds wrong", "● sum › adds wrong", "Expected: 3", "Received: 4"},
			notWant: []string{"✓ case 0", "tests: "},
		},
		{
			name:    "vitest passed",
			command: "pnpm vitest run",
			output:  repeatLines(30, " ✓ src/m%d.test.ts (5 tests) 3ms") + "\n Test Files  30 passed (30)\n      Tests  150 passed (150)\n[exit code: 0]",
			want:    []string{"Tests  150 passed (150)", "tests: 30 passed"},
			notWant: []string{"src/m0.test.ts"},
		},
		{
			name:    "pytest -v passed",
			command: "python -m pytest -v tests",
			output: "============================= test session starts ==============================\ncollected 30 items\n\n" +
				repeatLines(30, "tests/test_a.py::test_%d PASSED                                          [ 50%%]") +
				"\n============================== 30 passed in 0.12s ==============================\n[exit code: 0]",
			want:    []string{"30 passed in 0.12s", "pytest: 30 test(s) passed"},
			notWant: []string{"::test_0 PASSED"},
		},
		{
			name:    "pytest failed",
			command: "pytest tests",
			output: repeatLines(29, "tests/test_a.py::test_%d PASSED                                          [ 50%%]") +
				"tests/test_a.py::test_bad FAILED                                       [100%]\n" +
				"=================================== FAILURES ===================================\n" +
				"___________________________________ test_bad ___________________________________\n\n" +
				"    def test_bad():\n>       assert 1 == 2\nE       assert 1 == 2\n\ntests/test_a.py:5: AssertionError\n" +
				"FAILED tests/test_a.py::test_bad - assert 1 == 2\n========================= 1 failed, 29 passed in 0.12s =========================\n[exit code: 1]",
			want:    []string{"::test_bad FAILED", "E       assert 1 == 2", "tests/test_a.py:5: AssertionError", "1 failed, 29 passed"},
			notWant: []string{"::test_0 PASSED", "test(s) passed"},
		},
		{
			name:    "maven passed with effective-model POM warnings",
			command: "mvn test",
			output: "[INFO] Scanning for projects...\n[INFO] \n" +
				"[INFO] -------------------< com.example:demo >--------------------\n" +
				"[INFO] Building demo 1.0-SNAPSHOT\n[INFO]   from pom.xml\n" +
				"[INFO] --------------------------------[ jar ]---------------------------------\n" +
				mavenPOMWarnings +
				repeatLines(6, "[INFO] Downloading from central: https://repo.maven.apache.org/maven2/org/x/lib%d.pom") +
				"[INFO] --- resources:3.3.1:resources (default-resources) @ demo ---\n" +
				"[INFO] Copying 1 resource from src/main/resources to target/classes\n" +
				"[INFO] --- compiler:3.11.0:compile (default-compile) @ demo ---\n" +
				"[INFO] Nothing to compile - all classes are up to date\n" +
				"[INFO] --- surefire:3.2.5:test (default-test) @ demo ---\n[INFO] \n" +
				"[INFO] -------------------------------------------------------\n[INFO]  T E S T S\n[INFO] -------------------------------------------------------\n" +
				"[INFO] Running com.example.demo.AppTest\n" +
				"OpenJDK 64-Bit Server VM warning: Sharing is only supported for boot loader classes because bootstrap classpath has been appended\n" +
				repeatLines(20, "[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0, Time elapsed: 0.02 s -- in com.example.demo.Test%d") +
				"[INFO] Tests run: 60, Failures: 0, Errors: 0, Skipped: 0, Time elapsed: 0.30 s -- in com.example.demo.AppTest\n" +
				"[INFO] \n[INFO] Results:\n[INFO] \n[INFO] Tests run: 60, Failures: 0, Errors: 0, Skipped: 0\n[INFO] \n" +
				"[INFO] ------------------------------------------------------------------------\n[INFO] BUILD SUCCESS\n" +
				"[INFO] ------------------------------------------------------------------------\n" +
				"[INFO] Total time:  2.041 s\n[INFO] Finished at: 2026-10-14T09:15:22+08:00\n[exit code: 0]",
			want: []string{
				"maven: 7 POM warning line(s) folded",
				"maven: Tests run 60, Failures: 0, Errors: 0, Skipped: 0; BUILD SUCCESS",
				"Copying 1 resource from src/main/resources to target/classes", // real build output stays
			},
			notWant: []string{
				"Scanning for projects", "Downloading from", "Running com.example", "T E S T S", "Sharing is only supported",
				"Time elapsed", "Total time", "plugin:version", "[INFO] BUILD SUCCESS", "build.plugins.plugin.version",
			},
		},
		{
			// Every module of a reactor prints its own total: the summary adds them up.
			name:    "maven multi-module sums the test totals",
			command: "mvn -B verify",
			output: "[INFO] Scanning for projects...\n" +
				repeatLines(10, "[INFO] Downloading from central: https://repo.maven.apache.org/maven2/org/x/dep%d.jar") +
				repeatLines(4, "[INFO] ------------------< com.example:mod%d >------------------") +
				repeatLines(4, "[INFO] Building mod%d 1.0") +
				strings.Repeat("[INFO] Tests run: 4, Failures: 0, Errors: 0, Skipped: 1\n", 2) +
				"[INFO] Tests run: 6, Failures: 0, Errors: 0, Skipped: 0\n" +
				"[INFO] Reactor Summary for demo 1.0:\n[INFO] \n[INFO] demo .............. SUCCESS [  2.0 s]\n[INFO] BUILD SUCCESS\n[exit code: 0]",
			want:    []string{"maven: Tests run 14, Failures: 0, Errors: 0, Skipped: 2; BUILD SUCCESS"},
			notWant: []string{"Downloading from", "Building mod", "[INFO] Tests run:", "Scanning for projects"},
		},
		{
			name:    "maven build without tests still says it succeeded",
			command: "mvn -B package -DskipTests",
			output: "[INFO] Scanning for projects...\n" +
				repeatLines(30, "[INFO] --- maven-jar-plugin:3.3.0:jar (default-jar) @ demo%d ---") +
				"[INFO] BUILD SUCCESS\n" +
				"[INFO] ------------------------------------------------------------------------\n" +
				"[INFO] Total time:  1.704 s\n[exit code: 0]",
			want:    []string{"maven: BUILD SUCCESS"},
			notWant: []string{"Scanning for projects", "Tests run", "maven-jar-plugin"},
		},
		{
			name:    "maven test failure keeps the details and drops the summary",
			command: "./mvnw test",
			output: "[INFO] Scanning for projects...\n[INFO] Building demo 1.0\n" +
				repeatLines(20, "[INFO] Downloading from central: https://repo.maven.apache.org/maven2/org/x/lib%d.pom") +
				"[INFO] -------------------------------------------------------\n[INFO]  T E S T S\n[INFO] -------------------------------------------------------\n" +
				"[INFO] Running com.example.demo.AppTest\n" +
				"[ERROR] Tests run: 3, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.11 s <<< FAILURE! -- in com.example.demo.AppTest\n" +
				"[ERROR] com.example.demo.AppTest.testAdd -- Time elapsed: 0.01 s <<< FAILURE!\n" +
				"org.opentest4j.AssertionFailedError: expected: <3> but was: <4>\n" +
				"\tat com.example.demo.AppTest.testAdd(AppTest.java:11)\n[INFO] \n[INFO] Results:\n[INFO] \n" +
				"[ERROR] Failures: \n[ERROR]   AppTest.testAdd:11 expected: <3> but was: <4>\n" +
				"[ERROR] Tests run: 3, Failures: 1, Errors: 0, Skipped: 0\n" +
				"[INFO] BUILD FAILURE\n[INFO] Total time:  2.5 s\n[exit code: 1]",
			want: []string{
				"<<< FAILURE!",
				"org.opentest4j.AssertionFailedError: expected: <3> but was: <4>",
				"AppTest.testAdd:11 expected: <3> but was: <4>", // the per-test failure list stays
				"BUILD FAILURE",
			},
			notWant: []string{"Scanning for projects", "Downloading from", "T E S T S", "maven: "},
		},
		{
			name:    "maven compilation error keeps the java locations",
			command: "mvn compile",
			output: "[INFO] Scanning for projects...\n[INFO] Building demo 1.0\n" +
				repeatLines(12, "[INFO] --- maven-resources-plugin:3.3.1:resources (default-resources) @ demo%d ---") +
				"[INFO] -------------------------------------------------------------\n" +
				"[ERROR] COMPILATION ERROR : \n" +
				"[ERROR] /D:/work/demo/src/main/java/com/example/demo/App.java:[12,20] cannot find symbol\n" +
				"  symbol:   variable greeter\n  location: class com.example.demo.App\n" +
				"[INFO] 1 error\n[INFO] BUILD FAILURE\n[exit code: 1]",
			want: []string{
				"COMPILATION ERROR",
				"App.java:[12,20] cannot find symbol",
				"symbol:   variable greeter",
				"BUILD FAILURE",
			},
			notWant: []string{"Scanning for projects", "maven-resources-plugin", "maven: "},
		},
		{
			name:    "gradle passed",
			command: "./gradlew test",
			output: "Starting a Gradle Daemon, 1 busy Daemon could not be reused, use --status for details\n\n" +
				"> Configure project :\n" + repeatLines(10, "> Task :sub%d:compileJava UP-TO-DATE") +
				"> Task :compileJava\n> Task :processResources NO-SOURCE\n> Task :classes\n" +
				"> Task :compileTestJava\n> Task :testClasses\n> Task :test\n\n" +
				repeatLines(30, "AppTest > case%d() PASSED") +
				"\nBUILD SUCCESSFUL in 2s\n3 actionable tasks: 3 executed\n[exit code: 0]",
			want:    []string{"BUILD SUCCESSFUL in 2s", "gradle: 30 test(s) passed"},
			notWant: []string{"Starting a Gradle Daemon", "> Configure project", "> Task :compileJava", "UP-TO-DATE", "actionable tasks", "case0() PASSED"},
		},
		{
			name:    "gradle test failure keeps the details",
			command: "gradle --no-daemon test",
			output: repeatLines(10, "> Task :sub%d:compileJava UP-TO-DATE") +
				"> Task :compileJava\n> Task :test FAILED\n\n" +
				repeatLines(28, "AppTest > case%d() PASSED") +
				"AppTest > testAdd() FAILED\n    org.opentest4j.AssertionFailedError at AppTest.java:11\n\n" +
				"28 tests completed, 1 failed\n\n> Task :test FAILED\n\nBUILD FAILED in 2s\n" +
				"3 actionable tasks: 2 executed, 1 failed\n[exit code: 1]",
			want: []string{
				"AppTest > testAdd() FAILED",
				"org.opentest4j.AssertionFailedError at AppTest.java:11",
				"28 tests completed, 1 failed",
				"BUILD FAILED in 2s",
			},
			notWant: []string{"PASSED", "> Task :compileJava", "UP-TO-DATE", "actionable tasks", "gradle: "},
		},
		{
			name:    "rules only act on matching commands",
			command: "cat build.log",
			output:  repeatLines(40, "--- PASS: TestCase%d (0.00s)") + "[exit code: 0]",
			// nothing tidies these lines, so the result is left alone (below)
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tidy, ok := MinimizeToolOutput(shellCall(tc.command), tc.output)
			if len(tc.want) == 0 {
				assert.False(t, ok)
				return
			}
			assert.Require(t, assert.True(t, ok))
			for _, w := range tc.want {
				assert.StrContains(t, tidy, w)
			}
			for _, w := range tc.notWant {
				assert.NotContains(t, tidy, w)
			}
		})
	}
}

// TestOutputRuleFoldAndSum checks the two rule features the maven rule uses,
// with exact output: a fold block collapses a run of warnings into one line and
// the sum of a capture group goes into the summary.
func TestOutputRuleFoldAndSum(t *testing.T) {
	rules, errs := parseOutputRules([]byte(`
rules:
  - name: fake-maven
    match: '\bfake-mvn\b'
    drop: ['^\[INFO\] noise']
    fold:
      - start: '^\[WARNING\] some problems'
        body: '^\[WARNING\]'
        text: 'warnings: {lines} line(s)'
    sum:
      tests: '^\[INFO\] total (\d+) tests$'
    summary: 'fake: {tests} test(s)'
`))
	assert.Empty(t, errs)
	m := NewOutputMinimizer(rules)

	out := repeatLines(40, "[INFO] noise %d") +
		"[WARNING] some problems were encountered\n[WARNING] fix them\n[WARNING] \n" +
		"[INFO] total 12 tests\n[INFO] total 3 tests\n[exit code: 0]"
	tidy, ok := m.Minimize(shellCall("fake-mvn test"), out)
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, "warnings: 3 line(s)\n[INFO] total 12 tests\n[INFO] total 3 tests\n[exit code: 0]\nfake: 15 test(s)", tidy)

	t.Run("a fold block needs its start line", func(t *testing.T) {
		tidy, ok := m.Minimize(shellCall("fake-mvn test"), repeatLines(40, "[INFO] noise %d")+"[WARNING] fix them\n[exit code: 0]")
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "[WARNING] fix them\n[exit code: 0]", tidy)
	})
}

// TestBuiltinRuleFiles: the built-in rules come from the embedded files under
// outrules/, and builtinRuleFileOrder lists every one of them (a new file that is
// not listed there would be silently ignored) exactly once.
func TestBuiltinRuleFiles(t *testing.T) {
	entries, err := builtinRuleFiles.ReadDir("outrules")
	assert.NoErr(t, err)
	var embedded []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			embedded = append(embedded, "outrules/"+entry.Name())
		}
	}
	// ReadDir reports sorted names, so the listed order must be a permutation of
	// it (same files, no duplicate) - and it is the order the rules are applied
	// in, which is the one of the former single output_rules.yaml.
	listed := append([]string(nil), builtinRuleFileOrder...)
	sort.Strings(listed)
	assert.Eq(t, embedded, listed)
	assert.Eq(t, []string{"outrules/go.yaml", "outrules/rust.yaml", "outrules/js.yaml", "outrules/python.yaml", "outrules/java.yaml", "outrules/git.yaml", "outrules/grep.yaml", "outrules/docker.yaml", "outrules/make.yaml", "outrules/gh.yaml", "outrules/php.yaml", "outrules/dart.yaml"}, builtinRuleFileOrder)
}

// TestDefaultOutputRuleFields locks the built-in rules' names, their match
// regexes and their summaries: splitting the rules into outrules/ (suga-xt0o) keeps
// every field as it was.
func TestDefaultOutputRuleFields(t *testing.T) {
	var got []string
	for _, rule := range DefaultOutputRules() {
		got = append(got, rule.Name+" | "+rule.Match+" | "+rule.Summary)
	}
	assert.Eq(t, []string{
		`go-build | \bgo\s+build\b | go build: succeeded`,
		`go-vet | \bgo\s+vet\b | go vet: succeeded`,
		`go-test | \bgo test\b | go test: {packages} package(s) ok[, {passed} test(s) passed]`,
		`cargo-test | \bcargo (test|nextest)\b | cargo test: {passed} test(s) passed`,
		`tsc | \b(tsc)\b | tsc: succeeded`,
		`eslint | \beslint\b | eslint: succeeded`,
		`js-test | \b(jest|vitest|mocha)\b|\b(npm|pnpm|yarn|bun) (run )?test\b | tests: {passed} passed`,
		`ruff-check | \bruff\s+check\b | ruff check: succeeded`,
		`mypy | \bmypy\b | mypy: succeeded`,
		`pytest | \bpytest\b|\bpython3? -m pytest\b | pytest: {passed} test(s) passed`,
		`maven | \bmvnw?\b | maven: [Tests run {tests}, Failures: {failures}, Errors: {errors}, Skipped: {skipped}; ]BUILD SUCCESS`,
		`gradle | \bgradlew?\b | gradle: {passed} test(s) passed`,
		`git-log | ^\s*git\s+log\b | `,
		`git-diff | ^\s*git\s+(diff|show)\b | `,
		`git-status | ^\s*git\s+status\b | `,
		`grep | ^\s*(rg|grep)\b | `,
		`docker-build | \bdocker\s+(build|buildx\s+build)\b | docker build: succeeded`,
		`docker-compose-build | \bdocker(?:-compose|\s+compose)\s+build\b | docker compose build: succeeded`,
		`docker-compose-up | \bdocker(?:-compose|\s+compose)\s+up\b | docker compose up: succeeded`,
		`make | ^\s*make(?:\.exe)?(?:\s|$) | make: succeeded`,
		`gh-readonly | ^\s*gh\s+(run\s+(view|list)|pr\s+(checks|view)|issue\s+view|repo\s+view)\b | gh: succeeded`,
		`phpunit | \bphpunit\b | phpunit: succeeded`,
		`pest | \bpest\b | pest: succeeded`,
		`flutter-test | \b(?:fvm\s+)?flutter(?:\.bat)? test\b|\bdart(?:\.exe)? test\b | flutter test: All tests passed! ({tests} test(s))`,
		`dart-analyze | \b(?:fvm\s+)?flutter(?:\.bat)? analyze\b|\bdart(?:\.exe)? analyze\b | `,
		`flutter-build | \b(?:fvm\s+)?flutter(?:\.bat)? build\b | flutter build: succeeded`,
		`dart-pub-get | \b(?:fvm\s+)?flutter(?:\.bat)? pub get\b|\bdart(?:\.exe)? pub get\b | pub get: {changed} package(s) changed`,
	}, got)
}

func TestLoadOutputRules(t *testing.T) {
	names := func(rules []OutputRule) string {
		var out []string
		for _, r := range rules {
			out = append(out, r.Name)
		}
		return strings.Join(out, ",")
	}
	t.Run("built-in only", func(t *testing.T) {
		rules, errs := LoadOutputRules(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.Empty(t, errs)
		assert.Eq(t, "go-build,go-vet,go-test,cargo-test,tsc,eslint,js-test,ruff-check,mypy,pytest,maven,gradle,git-log,git-diff,git-status,grep,docker-build,docker-compose-build,docker-compose-up,make,gh-readonly,phpunit,pest,flutter-test,dart-analyze,flutter-build,dart-pub-get", names(rules))
	})
	t.Run("user then project: override, add, switch off", func(t *testing.T) {
		dir := t.TempDir()
		user, project := filepath.Join(dir, "user.yaml"), filepath.Join(dir, "project.yaml")
		assert.NoErr(t, os.WriteFile(user, []byte(`
rules:
  - name: make-check
    match: '\bmake check\b'
    drop: ['^CHECK OK ']
    count: {passed: '^CHECK OK '}
    summary: 'make check: {passed} ok'
  - name: maven
    enabled: false
`), 0o644))
		assert.NoErr(t, os.WriteFile(project, []byte(`
rules:
  - name: make-check
    match: '\bmake check\b'
    drop: ['^CHECK OK ']
    count: {passed: '^CHECK OK '}
    summary: 'project check: {passed} ok'
`), 0o644))
		rules, errs := LoadOutputRules(user, project)
		assert.Empty(t, errs)
		assert.Eq(t, "go-build,go-vet,go-test,cargo-test,tsc,eslint,js-test,ruff-check,mypy,pytest,gradle,git-log,git-diff,git-status,grep,docker-build,docker-compose-build,docker-compose-up,make,gh-readonly,phpunit,pest,flutter-test,dart-analyze,flutter-build,dart-pub-get,make-check", names(rules))

		m := NewOutputMinimizer(rules)
		tidy, ok := m.Minimize(shellCall("make check"), repeatLines(40, "CHECK OK item %d")+"[exit code: 0]")
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "[exit code: 0]\nproject check: 40 ok", tidy) // the project rule won
		_, ok = m.Minimize(shellCall("mvn test"), repeatLines(40, "[INFO] Running com.x.Test%d")+"[exit code: 0]")
		assert.False(t, ok) // maven switched off
	})
	t.Run("a broken file is reported and skipped", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "bad.yaml")
		assert.NoErr(t, os.WriteFile(bad, []byte("rules:\n  - name: x\n    match: '(unclosed'\n"), 0o644))
		rules, errs := LoadOutputRules(bad)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.StrContains(t, errs[0].Error(), "bad.yaml: rule x:")
		assert.Eq(t, "go-build,go-vet,go-test,cargo-test,tsc,eslint,js-test,ruff-check,mypy,pytest,maven,gradle,git-log,git-diff,git-status,grep,docker-build,docker-compose-build,docker-compose-up,make,gh-readonly,phpunit,pest,flutter-test,dart-analyze,flutter-build,dart-pub-get", names(rules))

		assert.NoErr(t, os.WriteFile(bad, []byte("rules:\n  - match: 'x'\n"), 0o644))
		_, errs = LoadOutputRules(bad)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.StrContains(t, errs[0].Error(), "name is required")
	})
}

func TestRenderSummary(t *testing.T) {
	names := []string{"packages", "passed"}
	summary := "go test: {packages} package(s) ok[, {passed} test(s) passed]"
	assert.Eq(t, "go test: 2 package(s) ok, 5 test(s) passed", renderSummary(summary, names, map[string]int{"packages": 2, "passed": 5}))
	assert.Eq(t, "go test: 2 package(s) ok", renderSummary(summary, names, map[string]int{"packages": 2}))
	assert.Eq(t, "", renderSummary(summary, names, map[string]int{}))
}
