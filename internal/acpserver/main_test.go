package acpserver

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestMain isolates tests from the real ~/.suga and from a configured hook command:
// SUGA_HOME points at a throwaway directory unless a test overrides it, and
// SUGA_HOOKS=off disables hooks.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "suga-test-home-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("SUGA_HOME", home)
	_ = os.Setenv("SUGA_HOOKS", "off")
	// The tests assert the Chinese UI; auto would follow the machine locale
	// (English in a bare container). Language tests override it with t.Setenv.
	_ = os.Setenv("SUGA_LANGUAGE", "zh")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// testCtx is the one hang guard of the connection tests: it bounds a broken
// test instead of measuring any latency, so 30s is comfortably wider than the
// old per-test budgets (10s, 15s, enough for a loaded Windows host,
// suag-5o5g) while t.Cleanup unwinds the context after every test without
// repeating the defer in each test.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
