package acpserver

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// TestMemoryConfirmEnglish: the permission request the ACP client shows uses
// the same catalog as the chat confirmation, so an English session describes the
// write in English while the protocol fields (option ids, kinds) stay as they
// are.
func TestMemoryConfirmEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	hook := &permissionHook{translator: tr}

	t.Run("title and details are english", func(t *testing.T) {
		assert.Eq(t, "memory: the session read external content, confirm this write", hook.tr().T("host.memory.title"))
		op := memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback, Title: "suga-guide", Body: "line one"}
		text := memoryConfirmText(hook.tr(), memory.ProjectScope("p"), op)
		assert.StrContains(t, text, "Scope: project:p")
		assert.StrContains(t, text, "Action: add")
		assert.StrContains(t, text, "Kind: feedback")
		assert.StrContains(t, text, "Title: suga-guide")
		assert.StrContains(t, text, "Body: line one")
		assert.StrNotContains(t, text, "范围")
	})

	t.Run("without a catalog the chinese text stays", func(t *testing.T) {
		bare := &permissionHook{}
		text := memoryConfirmText(bare.tr(), memory.ProjectScope("p"), memory.Op{Action: memory.ActionAdd})
		assert.StrContains(t, text, "范围：project:p")
	})
}
