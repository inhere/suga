package axconfig

import (
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// run.deadline_env: the name of the environment variable that carries the
// job deadline of this run as RFC3339 (gofer injects GOFER_JOB_DEADLINE).
// Empty or unset = disabled: no deadline hint is ever attached. The deadline
// itself is read from the process environment at every model request, so it
// works for run, chat and ACP alike.
const RunDeadlineEnvKey = "run.deadline_env"

// DefaultRunDeadlineEnv is the disabled default of run.deadline_env.
const DefaultRunDeadlineEnv = ""

// ReadRunDeadlineEnv resolves run.deadline_env: an env var name, settings
// like no deadline hint. Nothing here reads the referenced variable itself -
// the personal runner asks the process environment per request.
func ReadRunDeadlineEnv() string {
	if gkconfig.Get(RunDeadlineEnvKey) == nil {
		return DefaultRunDeadlineEnv
	}
	return strings.TrimSpace(gkconfig.String(RunDeadlineEnvKey))
}
