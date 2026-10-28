package wiring

import (
	"context"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
)

// TestSessionMessageInGoferJobs: whether the assembled registry offers
// session_message depends on GOFER_JOB_ID (set by gofer for a job) and
// tools.session_message.in_gofer_jobs, while tools.session_message.enabled and
// SUGA_SESSION_MESSAGE=off stay the global switch. The registry is built through
// OpenCLI, so the native catalog, the messenger and the disabled-tool hints are
// all the ones a real run uses.
func TestSessionMessageInGoferJobs(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	cases := []struct {
		name     string
		jobID    string
		inJobs   bool
		enabled  bool
		envValue string
		want     bool
		// wantHint is the substring the denial message must carry when the tool is
		// not registered; "" means the tool must be there.
		wantHint string
	}{
		{name: "no job, defaults", enabled: true, want: true},
		{name: "no job, in_gofer_jobs on", inJobs: true, enabled: true, want: true},
		{name: "no job, switched off", enabled: false, want: false, wantHint: "tools.session_message.enabled"},
		{name: "no job, env off", enabled: true, envValue: "off", want: false, wantHint: "tools.session_message.enabled"},
		{name: "job, defaults", jobID: "job-1", enabled: true, want: false, wantHint: "tools.session_message.in_gofer_jobs"},
		{name: "job, in_gofer_jobs on", jobID: "job-1", inJobs: true, enabled: true, want: true},
		{name: "job, in_gofer_jobs on but switched off", jobID: "job-1", inJobs: true, enabled: false,
			want: false, wantHint: "tools.session_message.enabled"},
		{name: "job, in_gofer_jobs on but env off", jobID: "job-1", inJobs: true, enabled: true, envValue: "off",
			want: false, wantHint: "tools.session_message.enabled"},
		// SUGA_SESSION_MESSAGE cannot override in_gofer_jobs: only its off values
		// switch the tool, and they switch it off everywhere.
		{name: "job, env on does not override", jobID: "job-1", enabled: true, envValue: "on",
			want: false, wantHint: "tools.session_message.in_gofer_jobs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOFER_JOB_ID", tc.jobID)
			t.Setenv("SUGA_SESSION_MESSAGE", tc.envValue)
			assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", tc.enabled)))
			assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.in_gofer_jobs", tc.inJobs)))
			// The config key of the new switch is a writable bool.
			descriptor, err := appconfig.MatchConfigKey("tools.session_message.in_gofer_jobs")
			assert.Require(t, assert.NoErr(t, err))
			assert.True(t, descriptor.Writable)

			rt, err := OpenCLI(context.Background(), Options{})
			assert.Require(t, assert.NoErr(t, err))
			t.Cleanup(func() { _ = rt.Close() })

			registered := false
			for _, name := range rt.Registry.List() {
				registered = registered || name == "session_message"
			}
			assert.Eq(t, tc.want, registered)
			if tc.want {
				return
			}
			// A call of the missing tool is answered with the reason, not with a
			// plain "not found".
			_, execErr := rt.tools.ExecuteTool(context.Background(), agentcoreapi.ToolCall{CallID: "c", Name: "session_message"})
			assert.ErrIs(t, execErr, tools.ErrDenied)
			assert.StrContains(t, execErr.Error(), tc.wantHint)
		})
	}
}

// TestSessionMessageHintFollowsTheLanguageAndTheSwitch: the three refusal hints
// are catalog entries, so language: en yields English text, and each switch
// picks its own reason.
func TestSessionMessageHintFollowsTheLanguageAndTheSwitch(t *testing.T) {
	resetConfig(t)
	t.Setenv("SUGA_SESSION_MESSAGE", "")
	assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", true))
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("GOFER_JOB_ID", "job-1")
		assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", false))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.enabled", true) })
		// The zh entry keeps the historical wording.
		assert.Eq(t, "该工具已在配置中关闭（tools.session_message.enabled / SUGA_SESSION_MESSAGE）", sessionMessageHint(zh))
		assert.StrContains(t, sessionMessageHint(en), "turned off in the configuration")
	})
	t.Run("gofer job", func(t *testing.T) {
		t.Setenv("GOFER_JOB_ID", "job-1")
		assert.StrContains(t, sessionMessageHint(zh), "tools.session_message.in_gofer_jobs")
		assert.StrContains(t, sessionMessageHint(en), "inside a gofer job")
	})
	t.Run("no job keeps the SUGA_HOME reason", func(t *testing.T) {
		t.Setenv("GOFER_JOB_ID", "")
		assert.StrContains(t, sessionMessageHint(zh), "SUGA_HOME")
		assert.StrContains(t, sessionMessageHint(en), "SUGA_HOME")
	})
}
