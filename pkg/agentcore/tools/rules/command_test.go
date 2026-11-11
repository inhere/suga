package rules

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestRuleMatchesCommand(t *testing.T) {
	tests := []struct {
		name   string
		rule   Rule
		target Target
		want   bool
	}{
		{
			name:   "safe sh command matches exact tokens",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test ./..."}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test ./..."}, want: true,
		},
		{
			name:   "final wildcard accepts any number of tokens",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellBash, Command: "go test -race ./pkg/agentcore"}, want: true,
		},
		{
			name:   "final double wildcard accepts no trailing tokens",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test **"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test"}, want: true,
		},
		{
			name:   "wildcard embedded in token is not a glob",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test*"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go testing"}, want: false,
		},
		{
			name:   "quotes group a token",
			rule:   Rule{Tool: "shell_exec", Command: []string{`printf "hello world"`}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: `printf "hello world"`}, want: true,
		},
		{
			name:   "nested quotes are rejected",
			rule:   Rule{Tool: "shell_exec", Command: []string{`printf "hello"`}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: `printf "he'llo"`}, want: false,
		},
		{
			name:   "unpaired quote is rejected",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: `go test "./...`}, want: false,
		},
		{
			name:   "control operator is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test ./... && echo done"}, want: false,
		},
		{
			name:   "expansion is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test $PKG"}, want: false,
		},
		{
			name:   "tab is not normalized",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go\ttest ./..."}, want: false,
		},
		{
			name:   "newline is not normalized",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test\n./..."}, want: false,
		},
		{
			name:   "wrapper prefix is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "sudo go test ./..."}, want: false,
		},
		{
			name:   "unknown shell cannot allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: "zsh", Command: "go test ./..."}, want: false,
		},
		{
			name:   "similar shell name cannot allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"echo *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: "notpowershell", Command: `echo C:\tmp`}, want: false,
		},
		{
			name:   "unknown shell deny still matches complete command",
			rule:   Rule{Tool: "shell_exec", Command: []string{"rm -rf /"}, Action: Deny},
			target: Target{Tool: "shell_exec", Shell: "unknown", Command: "rm -rf /"}, want: true,
		},
		{
			name:   "command rule cannot impersonate another tool",
			rule:   Rule{Tool: "*", Command: []string{"go test *"}, Action: Allow},
			target: Target{Tool: "other_tool", Shell: ShellSh, Command: "go test ./..."}, want: false,
		},
		{
			name:   "powershell allows windows paths",
			rule:   Rule{Tool: "shell_exec", Command: []string{`go test *`}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: "PowerShell 7 (pwsh)", Command: `go test C:\src\...`}, want: true,
		},
		{
			name:   "powershell expansion is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{"Get-Item *"}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellPowerShell, Command: `Get-Item $env:TEMP`}, want: false,
		},
		{
			name:   "cmd percent expansion is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{`echo *`}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellCmd, Command: `echo %TEMP%`}, want: false,
		},
		{
			name:   "cmd single quote is not an allow",
			rule:   Rule{Tool: "shell_exec", Command: []string{`echo *`}, Action: Allow},
			target: Target{Tool: "shell_exec", Shell: ShellCmd, Command: `echo 'ok'`}, want: false,
		},
		{
			name:   "deny can match an unsafe complete command",
			rule:   Rule{Tool: "shell_exec", Command: []string{"rm -rf /; echo nope"}, Action: Deny},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "rm -rf /; echo nope"}, want: true,
		},
		{
			name:   "ask can match an unsafe complete command",
			rule:   Rule{Tool: "shell_exec", Command: []string{"rm -rf / && echo nope"}, Action: Ask},
			target: Target{Tool: "shell_exec", Shell: ShellSh, Command: "rm -rf / && echo nope"}, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Eq(t, tt.want, tt.rule.MatchesCommand(tt.target))
		})
	}
}

func TestPolicyDecideCommandRule(t *testing.T) {
	policy := Policy{
		Rules: []Rule{
			{Index: 0, Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow},
			{Index: 1, Tool: "shell_exec", Command: []string{"go test ./... && echo done"}, Action: Deny},
		},
		Default: Ask,
	}

	t.Run("safe allow wins", func(t *testing.T) {
		got, rule, _ := policy.Decide(Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test ./pkg"})
		assert.Eq(t, Allow, got)
		assert.NotNil(t, rule)
	})
	t.Run("unsafe command does not use allow", func(t *testing.T) {
		got, rule, _ := policy.Decide(Target{Tool: "shell_exec", Shell: ShellSh, Command: "go test ./... && echo done"})
		assert.Eq(t, Deny, got)
		assert.NotNil(t, rule)
		assert.Eq(t, 1, rule.Index)
	})
}
