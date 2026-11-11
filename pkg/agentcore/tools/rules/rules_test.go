package rules

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestPolicyDecide(t *testing.T) {
	tests := []struct {
		name       string
		rules      []Rule
		def        Action
		target     Target
		wantAction Action
		wantIndex  int
		wantRule   bool
	}{
		{
			name: "default is used without a match", def: Ask,
			target: Target{Tool: "shell_exec"}, wantAction: Ask,
		},
		{
			name: "strongest action wins over order",
			rules: []Rule{
				{Source: SourceUser, Index: 1, Tool: "shell_exec", Action: Allow},
				{Source: SourceProject, Index: 2, Tool: "shell_exec", Action: Deny},
			},
			target: Target{Tool: "shell_exec"}, wantAction: Deny, wantIndex: 2, wantRule: true,
		},
		{
			name: "ask beats allow",
			rules: []Rule{
				{Source: SourceUser, Index: 1, Tool: "shell_exec", Action: Allow},
				{Source: SourceSession, Index: 2, Tool: "shell_exec", Action: Ask},
			},
			target: Target{Tool: "shell_exec"}, wantAction: Ask, wantIndex: 2, wantRule: true,
		},
		{
			name: "first equal-strength match is representative",
			rules: []Rule{
				{Source: SourceUser, Index: 1, Tool: "shell_*", Action: Ask},
				{Source: SourceProject, Index: 7, Tool: "*", Action: Ask},
			},
			target: Target{Tool: "shell_exec"}, wantAction: Ask, wantIndex: 1, wantRule: true,
		},
		{
			name: "mode filters a rule",
			rules: []Rule{
				{Source: SourceUser, Index: 1, Tool: "shell_exec", Modes: []Mode{ModeRun}, Action: Deny},
			},
			def: Ask, target: Target{Tool: "shell_exec", Mode: ModeChat}, wantAction: Ask,
		},
		{
			name:   "unset default fails closed",
			target: Target{Tool: "shell_exec"}, wantAction: Deny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := Policy{Rules: tt.rules, Default: tt.def}
			got, rule, _ := policy.Decide(tt.target)
			assert.Eq(t, tt.wantAction, got)
			assert.Eq(t, tt.wantRule, rule != nil)
			if rule != nil {
				assert.Eq(t, tt.wantIndex, rule.Index)
			}
		})
	}
}

func TestRuleMatchesTool(t *testing.T) {
	tests := []struct {
		name   string
		rule   Rule
		target Target
		want   bool
	}{
		{
			name: "native glob does not cross slash",
			rule: Rule{Tool: "file_*"}, target: Target{Tool: "file_write"}, want: true,
		},
		{
			name: "native glob cannot match MCP",
			rule: Rule{Tool: "file_*"}, target: Target{Tool: "file_write", MCP: true, MCPServer: "server"}, want: false,
		},
		{
			name: "qualified MCP glob matches server and tool",
			rule: Rule{Tool: "github/tool_*"}, target: Target{Tool: "tool_read", MCP: true, MCPServer: "github"}, want: true,
		},
		{
			name: "MCP glob cannot match native",
			rule: Rule{Tool: "github/tool_*"}, target: Target{Tool: "tool_read"}, want: false,
		},
		{
			name: "MCP server is part of identity",
			rule: Rule{Tool: "other/tool_read"}, target: Target{Tool: "tool_read", MCP: true, MCPServer: "github"}, want: false,
		},
		{
			name: "omitted tool is native wildcard",
			rule: Rule{}, target: Target{Tool: "anything"}, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Eq(t, tt.want, tt.rule.MatchesTool(tt.target))
		})
	}
}

func TestRuleValidationAndSummary(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		wantErr bool
		want    string
	}{
		{
			name: "valid tool rule", rule: Rule{Source: SourceUser, Index: 3, Tool: "shell_exec", Action: Allow},
		},
		{
			name: "command and path are mutually exclusive",
			rule: Rule{Tool: "shell_exec", Command: []string{"go test *"}, Path: []string{"docs/**"}, Action: Allow}, wantErr: true,
		},
		{
			name: "path requires exact file tool and root",
			rule: Rule{Tool: "file_*", Path: []string{"docs/**"}, Root: ".", Action: Allow}, wantErr: true,
		},
		{
			name: "command requires shell exec",
			rule: Rule{Tool: "file_write", Command: []string{"go test *"}, Action: Allow}, wantErr: true,
		},
		{
			name: "invalid mode",
			rule: Rule{Tool: "shell_exec", Modes: []Mode{"other"}, Action: Allow}, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			assert.Eq(t, tt.wantErr, err != nil)
		})
	}

	summary := (Rule{Source: SourceUser, Index: 3, Tool: "shell_exec", Command: []string{"go test *"}, Action: Allow}).Summary()
	assert.Eq(t, `user#3 shell_exec command="go test *" action=allow`, summary)
}
