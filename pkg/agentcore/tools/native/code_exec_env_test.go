package native

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// echoVarsCommand prints the named environment variables of the command with
// "|" between them, in the syntax of the shell the tool resolved. A variable
// that is not set prints as an empty field.
func echoVarsCommand(t *testing.T, tool *ShellExecTool, names ...string) string {
	t.Helper()
	if tool.shell.powershell {
		exprs := make([]string, 0, len(names))
		for _, name := range names {
			exprs = append(exprs, "$env:"+name)
		}
		return "Write-Output (@(" + strings.Join(exprs, ", ") + ") -join '|')"
	}
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "\"$"+name+"\"")
	}
	// One %s per name: a one-variable probe must not print empty fields of the
	// variables it does not ask for.
	format := strings.TrimSuffix(strings.Repeat("%s|", len(names)), "|")
	return "printf '" + format + "\\n' " + strings.Join(quoted, " ")
}

// firstLine is the command output without the exit-code line the tool appends.
func firstLine(content string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	return line
}

// sessionEnv is a stand-in for wiring's provider: the session comes from ctx, so
// a test changes sessions between calls like a session switch does.
func sessionEnv(tool *ShellExecTool) {
	tool.SetExtraEnv(func(ctx context.Context) []string {
		return []string{"SUGA_SESSION_ID=" + api.SessionIDFrom(ctx), "SUGA_SESSION_NAME=ws-k7z", "SUGA_TURN_ID=turn-9"}
	})
}

// TestShellExecSessionEnv: a command run for a session sees its session id,
// short name and turn; an inherited value of those names is replaced, and
// GOFER_SESSION_ID (gofer's own name) passes through untouched.
func TestShellExecSessionEnv(t *testing.T) {
	t.Setenv("SUGA_SESSION_ID", "parent-session")
	t.Setenv("SUGA_SESSION_NAME", "parent-name")
	t.Setenv("GOFER_SESSION_ID", "gofer-77")
	tool := NewShellExecTool(t.TempDir(), 0)
	sessionEnv(tool)
	command := echoVarsCommand(t, tool, "SUGA_SESSION_ID", "SUGA_SESSION_NAME", "SUGA_TURN_ID", "GOFER_SESSION_ID")

	ctx := api.WithSessionID(context.Background(), "sess-1")
	res, err := tool.Execute(ctx, api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: map[string]any{"command": command}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "sess-1|ws-k7z|turn-9|gofer-77", firstLine(res.Content))
}

// TestShellExecSessionEnvPerCall: the provider is read at every command, so a
// session switch (or a sub-agent's own session) is what the child sees and no
// earlier session lingers.
func TestShellExecSessionEnvPerCall(t *testing.T) {
	tool := NewShellExecTool(t.TempDir(), 0)
	tool.SetExtraEnv(func(ctx context.Context) []string {
		return []string{"SUGA_SESSION_ID=" + api.SessionIDFrom(ctx)}
	})
	command := echoVarsCommand(t, tool, "SUGA_SESSION_ID")
	for _, session := range []string{"sess-a", "sess-b"} {
		res, err := tool.Execute(api.WithSessionID(context.Background(), session),
			api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: map[string]any{"command": command}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, session, firstLine(res.Content))
	}
}

// TestShellExecSessionEnvWithoutShortName: a session without a short name does
// not set the variable, and an inherited value is not passed on.
func TestShellExecSessionEnvWithoutShortName(t *testing.T) {
	t.Setenv("SUGA_SESSION_NAME", "parent-name")
	tool := NewShellExecTool(t.TempDir(), 0)
	tool.SetExtraEnv(func(ctx context.Context) []string {
		return []string{"SUGA_SESSION_ID=" + api.SessionIDFrom(ctx)}
	})
	command := echoVarsCommand(t, tool, "SUGA_SESSION_ID", "SUGA_SESSION_NAME")
	res, err := tool.Execute(api.WithSessionID(context.Background(), "sess-plain"),
		api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: map[string]any{"command": command}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "sess-plain|", firstLine(res.Content))
}

// TestLocalCommandSessionEnv: chat's local commands run with the same session
// variables as shell_exec.
func TestLocalCommandSessionEnv(t *testing.T) {
	t.Setenv("SUGA_SESSION_ID", "parent-session")
	shell := resolveShell("")
	runner := NewLocalCommand(LocalCommandOptions{Workspace: t.TempDir(), Shell: shell.path,
		ExtraEnv: func(ctx context.Context) []string {
			return []string{"SUGA_SESSION_ID=" + api.SessionIDFrom(ctx)}
		}})
	probe := &ShellExecTool{shell: shell}
	command := echoVarsCommand(t, probe, "SUGA_SESSION_ID")

	res, err := runner.Run(api.WithSessionID(context.Background(), "sess-local"), command, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 0, res.ExitCode)
	assert.StrContains(t, res.ModelText, "sess-local")
}
