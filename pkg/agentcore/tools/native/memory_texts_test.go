package native

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// emptyMemoryStore is a store that answers nothing: the read-only and error
// paths under test never reach it with a successful write.
type emptyMemoryStore struct{}

func (emptyMemoryStore) Index(context.Context, memory.Scope) (memory.Index, error) {
	return memory.Index{}, nil
}

func (emptyMemoryStore) Get(context.Context, memory.Scope, string) (memory.Entry, error) {
	return memory.Entry{}, memory.ErrNotFound
}

func (emptyMemoryStore) Write(context.Context, memory.Scope, memory.Op) (memory.Entry, error) {
	return memory.Entry{}, memory.ErrNeedsConfirmation
}

func (emptyMemoryStore) Search(context.Context, memory.Scope, string, int) ([]memory.Hit, error) {
	return nil, nil
}

// TestMemoryTextsZhAndEn covers the model-facing memory errors of phase 1b:
// the historical Chinese text for a run that injects nothing (language: zh),
// English when the host injects it, and the untouched parts of the message
// (action, scope, cause).
func TestMemoryTextsZhAndEn(t *testing.T) {
	readTool := func(texts MemoryTexts) *MemoryTool {
		return NewMemoryTool(emptyMemoryStore{}, MemoryToolOptions{Writable: false, ProjectKey: "proj", Texts: texts})
	}
	en := MemoryTexts{
		ReadOnly:          "memory: cannot write memory in read-only mode (%s rejected); get/search still work",
		NeedsConfirmation: "memory: the session read external content, so a non-interactive run cannot write memory (the write that needs user confirmation was rejected)",
		Denied:            "memory: the session read external content, so writing memory needs confirmation, which was not given: %v",
		Tombstoned:        "memory: this content is the same as a previously removed entry; get the user's confirmation before writing it again (%v)",
		OverBudget:        "memory: the %s memory index is over its limit; merge with replace or remove old entries first, then write (%v)",
		NotFound:          "memory: the %s memory has no entry %q; use action=search to find the id",
		NoBackend:         "memory: no memory backend is configured for %s",
		Invalid:           "memory: invalid arguments: %v",
		Failed:            "memory: %s %s failed: %w",
	}
	// addAction is the write the read-only mode refuses before the store is used.
	addAction := api.ToolCall{CallID: "c1", Name: "memory",
		Arguments: map[string]any{"action": MemoryActionAdd, "scope": "project", "kind": "project",
			"title": "t", "summary": "s", "body": "b"}}

	t.Run("read-only write zh is the historical text", func(t *testing.T) {
		_, err := readTool(MemoryTexts{}).Execute(t.Context(), addAction)
		assert.Err(t, err)
		assert.Eq(t, "memory: 只读模式下不能写入记忆（add 被拒绝）；get/search 仍可使用", err.Error())
	})
	t.Run("read-only write en", func(t *testing.T) {
		_, err := readTool(en).Execute(t.Context(), addAction)
		assert.Err(t, err)
		assert.Eq(t, "memory: cannot write memory in read-only mode (add rejected); get/search still work", err.Error())
	})
	t.Run("store errors keep action, scope and cause", func(t *testing.T) {
		req := memoryRequest{action: MemoryActionAdd, scope: memory.Scope{Kind: memory.ScopeProject}, op: memory.Op{ID: "mem-1"}}
		zhTool := readTool(MemoryTexts{})
		assert.Eq(t, "memory: 会话读过外部内容，非交互模式不能写记忆（需要用户确认的写入被拒绝）",
			zhTool.mapError(req, memory.ErrNeedsConfirmation).Error())
		assert.Eq(t, "memory: project 中没有条目 \"mem-1\"；用 action=search 查找 id",
			zhTool.mapError(req, fmt.Errorf("%w: gone", memory.ErrNotFound)).Error())
		enTool := readTool(en)
		assert.Eq(t, "memory: the session read external content, so a non-interactive run cannot write memory (the write that needs user confirmation was rejected)",
			enTool.mapError(req, memory.ErrNeedsConfirmation).Error())
		assert.StrContains(t, enTool.mapError(req, fmt.Errorf("%w: gone", memory.ErrNotFound)).Error(), "the project memory has no entry \"mem-1\"; use action=search")
		assert.StrContains(t, enTool.mapError(req, errors.New("disk full")).Error(), "memory: add project failed: disk full")
	})
}
