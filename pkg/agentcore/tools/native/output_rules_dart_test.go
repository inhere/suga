package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// dartOutputCase is one recorded shape of a Dart/Flutter command's output: the
// command, the sample file under testdata/outrules/dart (the output as the tool
// printed it) and the lines the rule must keep or drop.
type dartOutputCase struct {
	name, command, file string
	want, notWant       []string
	// keptWithoutRule is a line (or marker) the rule removes: with the rule
	// switched off it has to survive the minimizing, which is the reverse
	// verification of every case below.
	keptWithoutRule string
}

// dartOutputCases are the samples of outrules/dart.yaml: a passing and a failing
// run of each command, in the shape the tool really prints (the compact reporter
// of the Dart test runner, the analyzer's issue lines, a Gradle build and a pub
// get).
func dartOutputCases() []dartOutputCase {
	return []dartOutputCase{
		{
			name:            "flutter test, compact reporter, all passed",
			command:         "flutter test",
			file:            "flutter-test-pass.txt",
			want:            []string{"flutter test: All tests passed! (16 test(s))"},
			notWant:         []string{"Store loads its items", "00:09 +16: All tests passed!"},
			keptWithoutRule: "00:00 +0: loading /home/dev/store/test/store_test.dart",
		},
		{
			name:    "flutter test, compact reporter, one failure",
			command: "flutter test test/store_test.dart",
			file:    "flutter-test-fail.txt",
			want: []string{
				"00:03 +4 -1: Store reload resets the counter of a loaded item [E]",
				"Expected: <3>",
				"Actual: <4>",
				"test/store_test.dart 25:5  main.<fn>",
				"Some tests failed.",
			},
			notWant:         []string{"package:matcher", "package:test_api", "package:flutter_test", "Store loads its items", "flutter test: "},
			keptWithoutRule: "  package:matcher/src/expect/expect.dart 166:31  fail",
		},
		{
			name:            "flutter test, expanded reporter, all passed",
			command:         "fvm flutter test --reporter expanded",
			file:            "flutter-test-expanded-pass.txt",
			want:            []string{"flutter test: All tests passed! (8 test(s))"},
			notWant:         []string{"My store Store loads its items", "00:09 +8: All tests passed!"},
			keptWithoutRule: "00:00 +0: My store Store loads its items from the loader",
		},
		{
			name:            "dart analyze, no issues",
			command:         "dart analyze",
			file:            "dart-analyze-clean.txt",
			want:            []string{"No issues found! (ran in 1.2s)"},
			notWant:         []string{"Analyzing store"},
			keptWithoutRule: "Analyzing store...",
		},
		{
			name:    "dart analyze, three issues grouped by severity",
			command: "flutter analyze",
			file:    "dart-analyze-issues.txt",
			want: []string{
				"error • ", // the group header of the two neighbouring errors
				"The name 'counter' isn't defined • lib/store.dart:9:3 • undefined_identifier",
				"warning • Unused import: 'dart:io' • lib/store.dart:3:8 • unused_import",
				"4 issues found. (ran in 1.2s)", // the analyzer's own summary stays
			},
			notWant:         []string{"Analyzing store", "  error • The name 'counter'"},
			keptWithoutRule: "Analyzing store...",
		},
		{
			name:            "flutter build apk, succeeded",
			command:         "flutter.bat build apk --release",
			file:            "flutter-build-pass.txt",
			want:            []string{"✓ Built build/app/outputs/flutter-apk/app-release.apk (7.5MB)", "Done 24.3s", "flutter build: succeeded"},
			notWant:         []string{"Downloading https://services.gradle.org", "⣽", "  45%"},
			keptWithoutRule: "Downloading https://services.gradle.org/distributions/gradle-8.3-all.zip",
		},
		{
			name:    "flutter build apk, failed",
			command: "flutter build apk --debug",
			file:    "flutter-build-fail.txt",
			want: []string{
				"lib/main.dart:12:10: Error: Undefined name 'counter'.",
				"FAILURE: Build failed with an exception.",
				"* What went wrong:",
				"BUILD FAILED in 24s",
			},
			notWant:         []string{"flutter build: succeeded", "⣽"},
			keptWithoutRule: "Running Gradle task 'assembleRelease'...                            ⣽",
		},
		{
			name:            "dart pub get, three packages changed",
			command:         "flutter pub get",
			file:            "dart-pub-get-pass.txt",
			want:            []string{"pub get: 3 package(s) changed"},
			notWant:         []string{"Resolving dependencies", "+ flutter_lints", "> material_color_utilities", "  path 1.9.0", "Changed 3 dependencies"},
			keptWithoutRule: "+ flutter_lints 3.0.0",
		},
		{
			name:            "dart pub get, nothing changed",
			command:         "dart pub get",
			file:            "dart-pub-get-nochange.txt",
			want:            []string{"Got dependencies!"},
			notWant:         []string{"Resolving dependencies"},
			keptWithoutRule: "Resolving dependencies...",
		},
		{
			name:    "dart pub get, version solving failed",
			command: "dart pub get",
			file:    "dart-pub-get-fail.txt",
			want: []string{
				"Because store depends on http ^2.0.0 which doesn't match any versions, version solving failed.",
			},
			notWant:         []string{"Resolving dependencies", "pub get: "},
			keptWithoutRule: "Resolving dependencies...",
		},
	}
}

// readDartSample returns the recorded output of a case.
func readDartSample(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "outrules", "dart", file))
	assert.Require(t, assert.NoErr(t, err))
	return string(data)
}

// TestOutputRulesDart checks the Dart/Flutter rules over the recorded outputs:
// every sample keeps what matters and drops the noise the rule is written for.
func TestOutputRulesDart(t *testing.T) {
	for _, tc := range dartOutputCases() {
		t.Run(tc.name, func(t *testing.T) {
			tidy, ok := MinimizeToolOutput(shellCall(tc.command), readDartSample(t, tc.file))
			assert.Require(t, assert.True(t, ok), "the rule did not apply to %q", tc.command)
			for _, w := range tc.want {
				assert.StrContains(t, tidy, w)
			}
			for _, w := range tc.notWant {
				assert.NotContains(t, tidy, w)
			}
		})
	}
}

// TestOutputRulesDartDisabled is the reverse verification: with the four rules
// switched off by a user rule file, the same output is either left alone or
// keeps the line the rule would have dropped.
func TestOutputRulesDartDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output-rules.yaml")
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(`
rules:
  - {name: flutter-test, enabled: false}
  - {name: dart-analyze, enabled: false}
  - {name: flutter-build, enabled: false}
  - {name: dart-pub-get, enabled: false}
`), 0o644)))
	rules, errs := LoadOutputRules(path)
	assert.Empty(t, errs)
	m := NewOutputMinimizer(rules)
	for _, tc := range dartOutputCases() {
		t.Run(tc.name, func(t *testing.T) {
			tidy, ok := m.Minimize(shellCall(tc.command), readDartSample(t, tc.file))
			if !ok {
				return // nothing tidied the output: the rule was what did before
			}
			assert.StrContains(t, tidy, tc.keptWithoutRule)
		})
	}
}
