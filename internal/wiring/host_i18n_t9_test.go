package wiring

import (
	"context"
	"runtime"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// TestHostNoticesEnglish covers the host-facing diagnostics of this package in
// English: the resident context notices, the danger guard's chat question and
// the memory write confirmation. Each goes through the real call path (the
// approver the chat gate uses, the confirmer the runtime installs), only the
// catalog is injected; the Chinese text keeps its own tests.
func TestHostNoticesEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("resident context notices", func(t *testing.T) {
		c := ManifestContext{
			Rules: ManifestContextPart{Bytes: 40 * 1024, LimitBytes: ResidentRulesSoftBytes,
				Over: true, Truncated: &ManifestContextTruncated{OriginalBytes: 40960, KeptBytes: 32768}},
			TotalBytes: 70 * 1024,
			OverBudget: []string{"rules", "total"},
		}
		sk := ManifestSkills{Skipped: []ManifestSkillSkipped{{Path: "/a/SKILL.md", Err: "bad"}}, Shadowed: []ManifestSkillShadow{{Name: "dup"}}}
		notices := ContextNotices(tr, c, sk)
		assert.Require(t, assert.Len(t, notices, 3))
		assert.StrContains(t, notices[0], "the project rules were truncated")
		assert.StrContains(t, notices[1], "exceeds its soft thresholds")
		assert.StrContains(t, notices[1], "rules 40960/32768 bytes")
		assert.StrContains(t, notices[1], "total 71680/65536 bytes")
		assert.StrContains(t, notices[2], "1 SKILL.md were skipped and 1 skills were shadowed")
	})

	t.Run("danger guard asks in english", func(t *testing.T) {
		ws := t.TempDir()
		shell := native.NewShellExecTool(ws, 0)
		danger := "rm -rf ."
		if runtime.GOOS == "windows" {
			danger = "Remove-Item -Recurse -Force ."
		}
		asker := &scriptedAsker{answers: []string{"no"}}
		chat := testChatApprover(nil, true, asker, ws, nil)
		chat.translator = tr
		call := agentcore.ToolCall{CallID: "c1", Name: "shell_exec", Arguments: map[string]any{"command": danger}}
		err := guardDangerous(chat).Decide(context.Background(), call, shell)
		assert.ErrMsgContains(t, err, "用户拒绝执行危险删除")
		assert.Require(t, assert.Len(t, asker.questions, 1))
		assert.StrContains(t, asker.questions[0].Details[0], "Command: "+danger)
		assert.StrContains(t, asker.questions[0].Details[1], "⚠ dangerous delete:")
		assert.StrContains(t, asker.questions[0].Prompt, "Type yes to run it")
	})

	t.Run("memory confirmation", func(t *testing.T) {
		op := memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback, ID: "abc", Title: "t", Body: "b"}
		asker := &scriptedAsker{answers: []string{"n"}}
		c := newChatMemoryConfirmer(asker, tr)
		assert.ErrMsgContains(t, c.confirm(context.Background(), memory.ProjectScope("p"), op), "用户拒绝写入记忆")
		assert.Require(t, assert.Len(t, asker.questions, 1))
		q := asker.questions[0]
		assert.Eq(t, "memory", q.Tool)
		assert.Eq(t, "Write this to memory? [y] Yes [N] No [a] Do not ask again this session:", q.Prompt)
		details := q.Details
		assert.Require(t, assert.Len(t, details, 7))
		assert.StrContains(t, details[0], "the session read external content")
		assert.StrContains(t, details[1], "Scope: project:p")
		assert.StrContains(t, details[2], "Action: add")
		assert.StrContains(t, details[3], "Kind: feedback")
		assert.StrContains(t, details[4], "id: abc")
		assert.StrContains(t, details[5], "Title: t")
		assert.StrContains(t, details[6], "Body: b")
	})
}
