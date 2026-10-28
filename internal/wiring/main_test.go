package wiring

import (
	"os"
	"testing"
)

// TestMain keeps tests from running a hook command configured on the developer's
// machine (SUGA_HOOKS=off). SUGA_HOME also points at a throwaway directory so
// tests never touch ~/.suga, and HOME/USERPROFILE do too so the developer's
// ~/.agents/skills never leak into tool or prompt assertions.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "suga-test-home-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("SUGA_HOME", home)
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	_ = os.Setenv("SUGA_HOOKS", "off")
	// The tests assert the Chinese UI; auto would follow the machine locale
	// (English in a bare container). Language tests override it with t.Setenv.
	_ = os.Setenv("SUGA_LANGUAGE", "zh")
	// The suite often runs inside a gofer job (gofer exec job ... go test); the job
	// marker would drop session_message from the expected tool set. Tests that
	// check the job behavior set it themselves with t.Setenv.
	_ = os.Unsetenv("GOFER_JOB_ID")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
