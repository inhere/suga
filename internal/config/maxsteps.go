package axconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// DefaultRunMaxSteps is the per-turn step limit of run when neither a
// mode-specific setting, max_steps nor an environment override is set.
const DefaultRunMaxSteps = 240

// DefaultChatMaxSteps is the per-turn step limit of interactive chat when no
// mode-specific setting or environment override is set.
const DefaultChatMaxSteps = 1000

// DefaultMaxSteps is kept as the legacy run default for callers that used the
// original single-mode setting.
const DefaultMaxSteps = DefaultRunMaxSteps

// ReadMaxSteps resolves the legacy/run step limit. Chat callers should use
// ReadMaxStepsFor with chat=true so chat can have its larger default.
func ReadMaxSteps(flagMaxSteps int) (int, error) {
	return ReadMaxStepsFor(flagMaxSteps, false)
}

// ReadMaxStepsFor resolves one main-agent turn. Precedence is
// --max-steps > SUGA_<MODE>_MAX_STEPS > SUGA_MAX_STEPS > <mode>.max_steps >
// max_steps > the mode default. flagMaxSteps 0 means "not given". A value
// below 1 from the flag or an environment is an error; a non-positive config
// value keeps its older meaning (no limit).
func ReadMaxStepsFor(flagMaxSteps int, chat bool) (int, error) {
	if flagMaxSteps < 0 {
		return 0, fmt.Errorf("--max-steps must be a whole number >= 1, got %d", flagMaxSteps)
	}
	if flagMaxSteps > 0 {
		return flagMaxSteps, nil
	}
	modeKey, modeEnv, defaultValue := "run.max_steps", "SUGA_RUN_MAX_STEPS", DefaultRunMaxSteps
	if chat {
		modeKey, modeEnv, defaultValue = "chat.max_steps", "SUGA_CHAT_MAX_STEPS", DefaultChatMaxSteps
	}
	for _, envName := range []string{modeEnv, "SUGA_MAX_STEPS"} {
		if raw := strings.TrimSpace(os.Getenv(envName)); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s must be a whole number >= 1, got %q", envName, raw)
			}
			return n, nil
		}
	}
	if gkconfig.Get(modeKey) != nil {
		return gkconfig.Int(modeKey, defaultValue), nil
	}
	if gkconfig.Get("max_steps") != nil {
		return gkconfig.Int("max_steps", defaultValue), nil
	}
	return defaultValue, nil
}
