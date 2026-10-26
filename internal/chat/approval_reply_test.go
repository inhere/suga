package chat

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
)

func TestParseApprovalReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want ApprovalReply
	}{
		{"yes", " YES ", ReplyAllow},
		{"always", "a", ReplyAllowAlways},
		{"deny always", "deny", ReplyDenyAlways},
		{"empty stays deny", "", ReplyDeny},
		{"unknown stays deny", "ok", ReplyDeny},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Eq(t, tc.want, ParseApprovalReply(tc.in)) })
	}
}

func TestParsePersistReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want PersistReply
	}{
		{"user", "u", PersistUser},
		{"project", " PROJECT ", PersistProject},
		{"enter", "", PersistSession},
		{"unknown", "yes", PersistSession},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Eq(t, tc.want, ParsePersistReply(tc.in)) })
	}
}

func TestApprovalPrompts(t *testing.T) {
	assert.Eq(t, "允许执行 shell_exec？[y]是 [N]否 [a]本会话总是允许“echo”开头的命令 [d]本会话总是拒绝：",
		ApprovalPrompt("shell_exec", "“echo”开头的命令", true))
	assert.Eq(t, "允许执行 shell_exec？[y]是 [N]否 [d]本会话总是拒绝：",
		ApprovalPrompt("shell_exec", "", false))
	assert.NotContains(t, PersistPrompt("allow"), "写回尚未启用")
	assert.StrContains(t, PersistPrompt("deny"), "[p]")
}

func TestApprovalPromptsEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "Allow shell_exec? [y] Yes [N] No [a] Always allow for this session commands beginning with “echo” [d] Always deny for this session:",
		ApprovalPromptWithTranslator(tr, "shell_exec", "commands beginning with “echo”", true))
	assert.Eq(t, "Write this rule to a rules file? [u] User-level ~/.suga/approval-rules.yaml [p] Project-level .suga/approval-rules.yaml [N] This session only:",
		PersistPromptWithTranslator(tr, "deny"))
}
