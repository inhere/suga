package cli

import (
	"os"
	"testing"
)

// TestMain keeps tests from running a hook command configured on the developer's
// machine (SUGA_HOOKS=off). SUGA_HOME also points at a throwaway directory so
// tests never touch ~/.suga.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "suga-test-home-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("SUGA_HOME", home)
	// A developer's (or an eval's) model and config file must not decide
	// what the CLI tests resolve.
	_ = os.Unsetenv("SUGA_CONFIG")
	_ = os.Unsetenv("SUGA_MODEL")
	_ = os.Unsetenv("SUGA_MAX_STEPS")
	_ = os.Unsetenv("SUGA_MAX_OUTPUT_TOKENS")
	// language is read from SUGA_LANGUAGE as well as config.yaml; a value set in
	// the developer's shell must not decide what the CLI tests resolve.
	_ = os.Unsetenv("SUGA_LANGUAGE")
	for _, key := range []string{
		"SUGA_BUDGET_TOKENS", "SUGA_BUDGET_COST_USD", "SUGA_BUDGET_SCOPE",
		"SUGA_BUDGET_ESTIMATE", "SUGA_BUDGET_ESTIMATE_FACTOR", "SUGA_BUDGET_NOTICE_RATIO",
	} {
		_ = os.Unsetenv(key)
	}
	_ = os.Setenv("SUGA_HOOKS", "off")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
