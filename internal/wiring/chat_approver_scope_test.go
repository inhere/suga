package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

func TestChatScopedApprovalPromptsEnglish(t *testing.T) {
	workspace := t.TempDir()
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	policy := approvalPolicy{
		Mode: approvalrules.ModeChat, Workspace: workspace, AlwaysPrefixTokens: 2, PersistAsk: true,
		Defaults: defaultApprovalDefaults(approvalrules.ModeChat, false),
	}
	asker := &scriptedAsker{answers: []string{"a", ""}}
	approver := newChatApprover(chatApproverOptions{
		gate: newCLIMCPGateWithPolicy(policy), asker: asker, workspace: workspace, translator: tr,
	})
	tool := &nativeStubTool{name: "shell_exec"}
	assert.NoErr(t, approver.Decide(context.Background(), agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "go test ./..."}}, tool))
	assert.StrContains(t, asker.questions[0].Prompt, "Always allow for this session commands beginning with “go test”")
	assert.StrContains(t, asker.questions[1].Prompt, "Write this rule to a rules file?")
}

func TestChatScopedApprovalPromptsAndPersistence(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	policy := approvalPolicy{
		Mode: approvalrules.ModeChat, Workspace: workspace, AlwaysPrefixTokens: 2, PersistAsk: true,
		Defaults: defaultApprovalDefaults(approvalrules.ModeChat, false),
	}
	asker := &scriptedAsker{answers: []string{"a", "u"}}
	var events []ApprovalRulePersistEvent
	approver := newChatApprover(chatApproverOptions{
		gate: newCLIMCPGateWithPolicy(policy), asker: asker, workspace: workspace,
		persistReporter: func(_ context.Context, event ApprovalRulePersistEvent) { events = append(events, event) },
	})
	tool := &nativeStubTool{name: "shell_exec"}
	assert.NoErr(t, approver.Decide(context.Background(), agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "go test ./..."}}, tool))
	assert.Eq(t, 2, len(asker.questions))
	assert.StrContains(t, asker.questions[0].Prompt, `[a]本会话总是允许“go test”开头的命令`)
	assert.StrContains(t, asker.questions[1].Prompt, "把这条规则写进规则文件")
	assert.StrContains(t, asker.questions[1].Prompt, "[u]")
	assert.Len(t, approver.policy.Rules, 1)
	assert.Len(t, events, 1)
	assert.Eq(t, approvalrules.Allow, events[0].Action)
	assert.Eq(t, "ok", events[0].Result)
	assert.False(t, tools.IsMCP(tool))

	path := filepath.Join(home, "approval-rules.yaml")
	text, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	parsed, err := parseApprovalRuleFile(text, path, approvalrules.SourceUser, workspace)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, parsed.Rules, 1))
	assert.Eq(t, []string{"go test *"}, parsed.Rules[0].Command)
	assert.Eq(t, approvalrules.Allow, parsed.Rules[0].Action)

	loaded, err := LoadApprovalRules(workspace)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, loaded.EffectiveRules(), 1))
	assert.Eq(t, approvalrules.Allow, loaded.EffectiveRules()[0].Action)
}

func TestChatProjectDenyPersistenceAndWriteFailureKeepSessionRule(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	projectPath := filepath.Join(workspace, ".suga", "approval-rules.yaml")

	t.Run("project deny is written", func(t *testing.T) {
		policy := approvalPolicy{Mode: approvalrules.ModeChat, Workspace: workspace, PersistAsk: true,
			Defaults: defaultApprovalDefaults(approvalrules.ModeChat, false)}
		asker := &scriptedAsker{answers: []string{"d", "p"}}
		approver := newChatApprover(chatApproverOptions{gate: newCLIMCPGateWithPolicy(policy), asker: asker, workspace: workspace})
		err := approver.Decide(context.Background(), agentcore.ToolCall{Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}, &nativeStubTool{name: "file_write"})
		assert.ErrIs(t, err, tools.ErrDenied)
		text, readErr := os.ReadFile(projectPath)
		assert.Require(t, assert.NoErr(t, readErr))
		parsed, parseErr := parseApprovalRuleFile(text, projectPath, approvalrules.SourceProject, workspace)
		assert.Require(t, assert.NoErr(t, parseErr))
		assert.Require(t, assert.Len(t, parsed.Rules, 1))
		assert.Eq(t, approvalrules.Deny, parsed.Rules[0].Action)
	})

	t.Run("bad file does not remove the session rule", func(t *testing.T) {
		badHome := t.TempDir()
		t.Setenv("SUGA_HOME", badHome)
		userPath := filepath.Join(badHome, "approval-rules.yaml")
		assert.NoErr(t, os.WriteFile(userPath, []byte("rules: [\n"), 0o600))
		policy := approvalPolicy{Mode: approvalrules.ModeChat, Workspace: workspace, PersistAsk: true,
			Defaults: defaultApprovalDefaults(approvalrules.ModeChat, false)}
		asker := &scriptedAsker{answers: []string{"a", "u"}}
		var events []ApprovalRulePersistEvent
		approver := newChatApprover(chatApproverOptions{gate: newCLIMCPGateWithPolicy(policy), asker: asker, workspace: workspace,
			persistReporter: func(_ context.Context, event ApprovalRulePersistEvent) { events = append(events, event) }})
		err := approver.Decide(context.Background(), agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "go test"}}, &nativeStubTool{name: "shell_exec"})
		assert.NoErr(t, err)
		assert.Len(t, approver.policy.Rules, 1)
		assert.Len(t, events, 1)
		assert.Eq(t, "error", events[0].Result)
		assert.StrContains(t, events[0].File, "approval-rules.yaml")
	})
}

// TestChatWriteScopeCoversTheFileDirectory: "always" on a file write covers the
// directory of the file (docs/a.txt -> docs/), so a sibling is allowed without a
// question. Before the fix the file itself was taken as the directory
// (docs/a.txt/**), which matched nothing.
func TestChatWriteScopeCoversTheFileDirectory(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("SUGA_HOME", t.TempDir())
	policy := approvalPolicy{Mode: approvalrules.ModeChat, Workspace: workspace,
		Defaults: defaultApprovalDefaults(approvalrules.ModeChat, false)}
	asker := &scriptedAsker{answers: []string{"a"}}
	approver := newChatApprover(chatApproverOptions{gate: newCLIMCPGateWithPolicy(policy), asker: asker, workspace: workspace})
	write := func(p string) error {
		return approver.Decide(context.Background(), agentcore.ToolCall{Name: "file_write", Arguments: map[string]any{"path": p}}, &nativeStubTool{name: "file_write"})
	}
	scope := approver.scopeFor(agentcore.ToolCall{Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}, &nativeStubTool{name: "file_write"})
	assert.Eq(t, "docs", scope.key)
	assert.StrContains(t, scope.description, "写入 docs/ 目录")
	assert.NoErr(t, write("docs/a.txt"))
	assert.NoErr(t, write("docs/b.txt"))
	assert.Eq(t, "file_write:docs", approvalScopeAttr("file_write", scope.key))
}

// TestApprovalScopeKeyUsesTheFileDirectory: the ACP always scope of a write or
// edit is the directory of the file, so docs/a.txt and docs/b.txt share it.
func TestApprovalScopeKeyUsesTheFileDirectory(t *testing.T) {
	workspace := t.TempDir()
	policy := approvalPolicy{Mode: approvalrules.ModeACP, Workspace: workspace}
	key := func(name, p string) string {
		return approvalScopeKey(policy, agentcore.ToolCall{Name: name, Arguments: map[string]any{"path": p}}, &nativeStubTool{name: name})
	}
	t.Run("write in a directory", func(t *testing.T) {
		assert.Eq(t, "file_write:docs/**", key("file_write", "docs/a.txt"))
		assert.Eq(t, key("file_write", "docs/a.txt"), key("file_write", "docs/b.txt"))
	})
	t.Run("edit at the root", func(t *testing.T) {
		assert.Eq(t, "file_edit:**", key("file_edit", "README.md"))
	})
	t.Run("remove keeps the exact path", func(t *testing.T) {
		assert.Eq(t, "file_remove:docs/a.txt", key("file_remove", "docs/a.txt"))
	})
}
