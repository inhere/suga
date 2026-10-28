package axconfig

import (
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestSessionMessageEnabled: the tool is on by default and off through
// tools.session_message.enabled or the SUGA_SESSION_MESSAGE=off process switch.
func TestSessionMessageEnabled(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.True(t, SessionMessageEnabled())
	t.Run("config off", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", false)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.enabled", true) })
		assert.False(t, SessionMessageEnabled())
	})
	t.Run("env off", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "off")
		assert.False(t, SessionMessageEnabled())
	})
	t.Run("env on", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "on")
		assert.True(t, SessionMessageEnabled())
	})
}

// TestSessionMessageAvailable: a gofer job (GOFER_JOB_ID set) gets the tool only
// with tools.session_message.in_gofer_jobs=true, and the global switches
// (tools.session_message.enabled, SUGA_SESSION_MESSAGE=off) always win.
func TestSessionMessageAvailable(t *testing.T) {
	cases := []struct {
		name     string
		jobID    string
		inJobs   bool
		enabled  bool
		envValue string
		want     bool
	}{
		{name: "no job, defaults", enabled: true, want: true},
		{name: "no job, in_gofer_jobs on", inJobs: true, enabled: true, want: true},
		{name: "no job, switched off", enabled: false, want: false},
		{name: "no job, env off", enabled: true, envValue: "off", want: false},
		{name: "job, defaults", jobID: "job-1", enabled: true, want: false},
		{name: "job, in_gofer_jobs on", jobID: "job-1", inJobs: true, enabled: true, want: true},
		{name: "job, in_gofer_jobs on but switched off", jobID: "job-1", inJobs: true, enabled: false, want: false},
		{name: "job, in_gofer_jobs on but env off", jobID: "job-1", inJobs: true, enabled: true, envValue: "off", want: false},
		// SUGA_SESSION_MESSAGE only turns the tool off: a value like "on" does
		// not override the gofer job default.
		{name: "job, env on does not override", jobID: "job-1", enabled: true, envValue: "on", want: false},
		{name: "job, blank env on, in_gofer_jobs on", jobID: "job-1", inJobs: true, enabled: true, envValue: "on", want: true},
		{name: "blank job id is no job", jobID: "  ", enabled: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gkconfig.Reset()
			t.Cleanup(gkconfig.Reset)
			t.Setenv(envGoferJobID, tc.jobID)
			t.Setenv(envSessionMessageOff, tc.envValue)
			assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", tc.enabled)))
			assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.in_gofer_jobs", tc.inJobs)))
			assert.Eq(t, strings.TrimSpace(tc.jobID) != "", InGoferJob())
			assert.Eq(t, tc.inJobs, SessionMessageInGoferJobs())
			assert.Eq(t, tc.want, SessionMessageAvailable())
		})
	}
}

// TestSessionMessageMaxHops: the automatic-reply limit defaults to 3, follows the
// config, and never drops below 1 (the loop guard cannot be switched off by
// accident).
func TestSessionMessageMaxHops(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.Eq(t, DefaultSessionMessageMaxHops, SessionMessageMaxHops())
	t.Run("configured", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.max_hops", 5)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.max_hops", 3) })
		assert.Eq(t, 5, SessionMessageMaxHops())
	})
	t.Run("below one keeps the default", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.max_hops", 0)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.max_hops", 3) })
		assert.Eq(t, DefaultSessionMessageMaxHops, SessionMessageMaxHops())
	})
}
