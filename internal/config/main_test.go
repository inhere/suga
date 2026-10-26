package axconfig

import (
	"os"
	"testing"
)

// TestMain clears suga's own settings from the environment: a developer's
// SUGA_HOME / SUGA_CONFIG / SUGA_MODEL (or an eval's, inherited through the
// agent's shell) would otherwise decide what Load and ReadBudgetSettings see.
// Tests that need them set them with t.Setenv.
func TestMain(m *testing.M) {
	for _, key := range []string{
		"SUGA_HOME", "SUGA_CONFIG", "SUGA_MODEL", "SUGA_MAX_STEPS", "SUGA_MAX_OUTPUT_TOKENS",
		"SUGA_LANGUAGE",
		"SUGA_BUDGET_TOKENS", "SUGA_BUDGET_COST_USD", "SUGA_BUDGET_SCOPE",
		"SUGA_BUDGET_ESTIMATE", "SUGA_BUDGET_ESTIMATE_FACTOR", "SUGA_BUDGET_NOTICE_RATIO",
	} {
		_ = os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
