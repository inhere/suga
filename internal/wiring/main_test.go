package wiring

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// memExecEnv makes the test binary act as an exec protocol v1 memory backend
// (see pkg/agentcore/memory/execstore): it reads one request line and answers
// from the JSON fixture the variable points at, a map of op name to result.
const memExecEnv = "SUGA_TEST_MEMEXEC"

// serveMemExec answers one request from the fixture; an op without a fixture
// entry is reported as not found.
func serveMemExec(fixture string) {
	var req struct {
		Op string `json:"op"`
	}
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &req) != nil {
		os.Exit(2)
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		os.Exit(2)
	}
	var results map[string]json.RawMessage
	if json.Unmarshal(raw, &results) != nil {
		os.Exit(2)
	}
	if res, ok := results[req.Op]; ok {
		_, _ = os.Stdout.WriteString(`{"ok":true,"result":` + string(res) + "}\n")
		return
	}
	_, _ = os.Stdout.WriteString(`{"ok":false,"error":"not found: ` + req.Op + `"}` + "\n")
}

// TestMain keeps tests from running a hook command configured on the developer's
// machine (SUGA_HOOKS=off). SUGA_HOME also points at a throwaway directory so
// tests never touch ~/.suga, and HOME/USERPROFILE do too so the developer's
// ~/.agents/skills never leak into tool or prompt assertions.
func TestMain(m *testing.M) {
	// Spawned by the memory exec backend in a test: act as the backend only.
	if fixture := os.Getenv(memExecEnv); fixture != "" {
		serveMemExec(fixture)
		os.Exit(0)
	}
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
