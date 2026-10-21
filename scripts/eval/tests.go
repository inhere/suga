package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skippedDirs are workspace directories a test-file sweep never enters: VCS
// data and dependency caches hold no file the agent wrote for judging.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".venv": true}

// snapshotWorkspace returns the slash-separated relative paths of every file
// under ws. The runner takes it after the workspace is prepared (fixture, repo
// snapshot, generated files, variant overlay) and before the agent runs, so a
// file missing from it is one the agent added.
func snapshotWorkspace(ws string) map[string]bool {
	files := map[string]bool{}
	err := filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if rel, relErr := relSlash(ws, p); relErr == nil {
			files[rel] = true
		}
		return nil
	})
	if err != nil { // an unreadable workspace only means nothing can be exempted
		return map[string]bool{}
	}
	return files
}

// relSlash returns p relative to root with forward slashes.
func relSlash(root, p string) (string, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// injectedFiles lists the files the task's acceptance directory put into ws
// (slash-separated relative paths): they are exempt from the test-file sweep
// because the workspace snapshot predates the injection.
func (t *Task) injectedFiles(ws string) map[string]bool {
	out := map[string]bool{}
	if t.Acceptance == "" {
		return out
	}
	src, err := safeJoin(t.dir, t.Acceptance)
	if err != nil {
		return out
	}
	for rel := range snapshotWorkspace(src) {
		out[rel] = true
	}
	return out
}

// removeForeignTests deletes the test files under ws that the agent added, so
// the checks run the task's own tests plus the injected acceptance tests and a
// broken self-written test cannot fail a correct implementation. It returns the
// removed paths sorted (nil when there is nothing to remove).
func removeForeignTests(ws string, before, exempt map[string]bool) []string {
	var removed []string
	err := filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := relSlash(ws, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if rel != "." && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if before[rel] || exempt[rel] || !isTestFile(d.Name()) {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		removed = append(removed, rel)
		return nil
	})
	if err != nil { // a partial sweep still records what it removed
		return removed
	}
	sort.Strings(removed)
	return removed
}

// isTestFile reports whether a file name is a language's test file, i.e. a file
// whose content the checks would run as a test (Go, Python, Java, Kotlin, PHP,
// Rust, Ruby, JS/TS, C#, Elixir, Swift, C++).
func isTestFile(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "_test.go"),
		strings.HasSuffix(lower, "_test.py"), strings.HasSuffix(lower, "_tests.py"),
		strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py"),
		strings.HasSuffix(lower, "_test.rs"),
		strings.HasSuffix(lower, "_test.rb"), strings.HasSuffix(lower, "_spec.rb"),
		strings.HasSuffix(lower, "_test.php"),
		strings.HasSuffix(lower, "_test.exs"), strings.HasSuffix(lower, "_test.ex"),
		strings.HasSuffix(lower, "_test.cpp"), strings.HasSuffix(lower, "_test.cc"),
		strings.HasSuffix(lower, "_test.c"), strings.HasSuffix(lower, "_test.h"),
		strings.HasSuffix(lower, "_test.dart"), strings.HasSuffix(lower, "_test.kt"),
		strings.HasSuffix(lower, ".spec.js"), strings.HasSuffix(lower, ".test.js"),
		strings.HasSuffix(lower, ".spec.ts"), strings.HasSuffix(lower, ".test.ts"),
		strings.HasSuffix(lower, ".spec.jsx"), strings.HasSuffix(lower, ".test.jsx"),
		strings.HasSuffix(lower, ".spec.tsx"), strings.HasSuffix(lower, ".test.tsx"),
		strings.HasSuffix(lower, ".spec.mjs"), strings.HasSuffix(lower, ".test.mjs"):
		return true
	}
	// Java, Kotlin, C# and Swift name a test class with a capital: FooTest.java
	// (case-sensitive, so "contest.java" is not a test file).
	for _, suffix := range []string{"Test.java", "Tests.java", "Test.kt", "Tests.kt", "Test.cs", "Tests.cs", "Test.swift", "Tests.swift"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
