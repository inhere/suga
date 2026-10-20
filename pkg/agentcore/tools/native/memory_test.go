package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// fakeMemoryStore records calls and answers from fixed values.
type fakeMemoryStore struct {
	writes   []memory.Op
	scopes   []memory.Scope
	writeErr error
	readErr  error
	entry    memory.Entry
	hits     []memory.Hit
	ctxCall  string // MemoryCallID seen by Write
}

func (f *fakeMemoryStore) Index(context.Context, memory.Scope) (memory.Index, error) {
	return memory.Index{}, nil
}

func (f *fakeMemoryStore) Get(_ context.Context, s memory.Scope, id string) (memory.Entry, error) {
	f.scopes = append(f.scopes, s)
	if f.readErr != nil {
		return memory.Entry{}, f.readErr
	}
	e := f.entry
	e.ID = id
	return e, nil
}

func (f *fakeMemoryStore) Write(ctx context.Context, s memory.Scope, op memory.Op) (memory.Entry, error) {
	f.writes, f.scopes = append(f.writes, op), append(f.scopes, s)
	f.ctxCall = MemoryCallID(ctx)
	if f.writeErr != nil {
		return memory.Entry{}, f.writeErr
	}
	id := op.ID
	if id == "" {
		id = memory.Slug(op.Title)
	}
	return memory.Entry{ID: id, Title: op.Title, Body: op.Body, Kind: op.Kind, AppliesWhen: append([]string(nil), op.AppliesWhen...)}, nil
}

func (f *fakeMemoryStore) Search(_ context.Context, s memory.Scope, _ string, _ int) ([]memory.Hit, error) {
	f.scopes = append(f.scopes, s)
	return f.hits, f.readErr
}

func runMemory(t *testing.T, tool *MemoryTool, args map[string]any) (*api.ToolResult, error) {
	t.Helper()
	return tool.Execute(context.Background(), api.ToolCall{CallID: "c1", Name: "memory", Arguments: args})
}

func TestMemoryToolValidation(t *testing.T) {
	store := &fakeMemoryStore{}
	tool := NewMemoryTool(store, MemoryToolOptions{Writable: true, ProjectKey: "proj"})
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no action", map[string]any{}, "action is required"},
		{"unknown action", map[string]any{"action": "drop"}, "unknown action"},
		{"bad scope", map[string]any{"action": "get", "id": "a", "scope": "people"}, "scope"},
		{"add with id", map[string]any{"action": "add", "id": "x", "kind": "user", "title": "t", "body": "b"}, "omit id"},
		{"add without kind", map[string]any{"action": "add", "title": "t", "body": "b"}, "needs kind"},
		{"add bad kind", map[string]any{"action": "add", "kind": "note", "title": "t", "body": "b"}, "unknown kind"},
		{"add without body", map[string]any{"action": "add", "kind": "user", "title": "t"}, "title and a body"},
		{"replace without id", map[string]any{"action": "replace", "title": "t", "body": "b"}, "needs the id"},
		{"remove without id", map[string]any{"action": "remove"}, "needs an id"},
		{"get bad id", map[string]any{"action": "get", "id": "../x"}, "invalid id"},
		{"add bad applies_when type", map[string]any{"action": "add", "kind": "user", "title": "t", "body": "b", "applies_when": "Windows"}, "applies_when"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runMemory(t, tool, c.args)
			assert.Err(t, err)
			assert.StrContains(t, err.Error(), c.want)
		})
	}
	assert.Empty(t, store.writes)

	t.Run("project scope needs a key", func(t *testing.T) {
		_, err := runMemory(t, NewMemoryTool(store, MemoryToolOptions{Writable: true}), map[string]any{"action": "get", "id": "a"})
		assert.ErrMsgContains(t, err, "scope=user")
	})
}

func TestMemoryToolWrites(t *testing.T) {
	t.Run("add defaults to project scope with provenance", func(t *testing.T) {
		store := &fakeMemoryStore{}
		tool := NewMemoryTool(store, MemoryToolOptions{Writable: true, ProjectKey: "proj",
			ProvenanceFrom: func(context.Context) memory.Provenance {
				return memory.Provenance{SessionID: "s1", TurnID: "t1"}
			}})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "feedback", "title": "Use tabs", "body": "Always tabs", "applies_when": []any{"Go 1.26", "Windows"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, memory.ProjectScope("proj"), store.scopes[0])
		op := store.writes[0]
		assert.Eq(t, memory.ActionAdd, op.Action)
		assert.Eq(t, memory.KindFeedback, op.Kind)
		assert.Eq(t, []string{"Go 1.26", "Windows"}, op.AppliesWhen)
		assert.Eq(t, memory.Provenance{SessionID: "s1", TurnID: "t1", Source: "tool"}, op.Prov)
		assert.Eq(t, "c1", store.ctxCall)
		assert.Nil(t, res.Metadata)
		change, ok := ParseMemoryResult(res.Content)
		assert.True(t, ok)
		assert.Eq(t, MemoryChange{Action: "add", Scope: "project", ID: "use-tabs", Title: "Use tabs"}, change)
	})
	t.Run("replace and remove on user scope", func(t *testing.T) {
		store := &fakeMemoryStore{}
		tool := NewMemoryTool(store, MemoryToolOptions{Writable: true})
		res, err := runMemory(t, tool, map[string]any{"action": "replace", "scope": "user", "id": "name", "title": "Name", "body": "Bob"})
		assert.NoErr(t, err)
		change, _ := ParseMemoryResult(res.Content)
		assert.Eq(t, MemoryChange{Action: "replace", Scope: "user", ID: "name", Title: "Name"}, change)
		res, err = runMemory(t, tool, map[string]any{"action": "remove", "scope": "user", "id": "name"})
		assert.NoErr(t, err)
		change, _ = ParseMemoryResult(res.Content)
		assert.Eq(t, "remove", change.Action)
		assert.Eq(t, memory.Op{Action: memory.ActionRemove, ID: "name", Prov: memory.Provenance{Source: "tool"}}, store.writes[1])
	})
	t.Run("read-only refuses writes before the store", func(t *testing.T) {
		store := &fakeMemoryStore{hits: []memory.Hit{{ID: "a", Title: "A", Kind: memory.KindUser}}}
		tool := NewMemoryTool(store, MemoryToolOptions{Writable: false, ProjectKey: "proj"})
		for _, args := range []map[string]any{
			{"action": "add", "kind": "user", "title": "t", "body": "b"},
			{"action": "replace", "id": "a", "title": "t", "body": "b"},
			{"action": "remove", "id": "a"},
		} {
			_, err := runMemory(t, tool, args)
			assert.ErrMsgContains(t, err, "只读模式")
		}
		assert.Empty(t, store.writes)
		assert.Empty(t, store.scopes)
		_, err := runMemory(t, tool, map[string]any{"action": "get", "id": "a"})
		assert.NoErr(t, err)
		_, err = runMemory(t, tool, map[string]any{"action": "search", "query": "a"})
		assert.NoErr(t, err)
	})
	t.Run("provenance failure after the write is a warning", func(t *testing.T) {
		gov := memory.NewGoverned(&fakeMemoryStore{}, memory.Policy{Provenance: failingProvenance{}})
		tool := NewMemoryTool(gov, MemoryToolOptions{Writable: true, ProjectKey: "proj"})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "user", "title": "T", "body": "B"})
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, "warning:")
		_, ok := ParseMemoryResult(res.Content)
		assert.True(t, ok)
	})
}

type failingProvenance struct{}

func (failingProvenance) Record(context.Context, string, memory.Scope, memory.Entry, string) error {
	return errors.New("db down")
}
func (failingProvenance) Tombstone(context.Context, string, memory.Scope, string, string) error {
	return errors.New("db down")
}
func (failingProvenance) IsTombstoned(context.Context, string, memory.Scope, string) (bool, error) {
	return false, nil
}

func TestMemoryToolErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&memory.BudgetError{Scope: memory.ProjectScope("p"), Lines: 201, MaxLines: 200}, "先用 replace 合并或用 remove 删除"},
		{fmt.Errorf("%w: gone", memory.ErrTombstoned), "征得用户确认"},
		{fmt.Errorf("%w: cannot ask", memory.ErrNeedsConfirmation), "非交互模式不能写记忆"},
		{fmt.Errorf("%w: %w", memory.ErrDenied, errors.New("用户拒绝写入记忆")), "用户拒绝写入记忆"},
		{fmt.Errorf("%w: x", memory.ErrNotFound), "action=search"},
		{fmt.Errorf("%w: x", memory.ErrNoBackend), "没有为 project 配置记忆后端"},
		{fmt.Errorf("%w: bad", memory.ErrInvalid), "参数无效"},
		{errors.New("disk full"), "disk full"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			tool := NewMemoryTool(&fakeMemoryStore{writeErr: c.err}, MemoryToolOptions{Writable: true, ProjectKey: "p"})
			_, err := runMemory(t, tool, map[string]any{"action": "replace", "id": "a", "title": "T", "body": "B"})
			assert.ErrMsgContains(t, err, c.want)
			assert.False(t, errors.Is(err, tools.ErrAborted))
		})
	}
	t.Run("cancelled confirmation aborts the turn", func(t *testing.T) {
		tool := NewMemoryTool(&fakeMemoryStore{writeErr: fmt.Errorf("%w: %w", memory.ErrDenied, context.Canceled)},
			MemoryToolOptions{Writable: true, ProjectKey: "p"})
		_, err := runMemory(t, tool, map[string]any{"action": "remove", "id": "a"})
		assert.True(t, errors.Is(err, tools.ErrAborted))
		assert.True(t, errors.Is(err, context.Canceled))
	})
	t.Run("read errors", func(t *testing.T) {
		tool := NewMemoryTool(&fakeMemoryStore{readErr: fmt.Errorf("%w: x", memory.ErrNotFound)}, MemoryToolOptions{ProjectKey: "p"})
		_, err := runMemory(t, tool, map[string]any{"action": "get", "id": "zz"})
		assert.ErrMsgContains(t, err, `没有条目 "zz"`)
	})
}

func TestMemoryToolExternalReads(t *testing.T) {
	store := &fakeMemoryStore{entry: memory.Entry{Title: "T", Body: "body", Kind: memory.KindProject, Updated: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}}
	backend := map[string]string{memory.ScopeUser: "files", memory.ScopeProject: "gofer"}
	tool := NewMemoryTool(store, MemoryToolOptions{ProjectKey: "p", Backend: func(s memory.Scope) string { return backend[s.Kind] }})
	t.Run("gofer read is external", func(t *testing.T) {
		res, err := runMemory(t, tool, map[string]any{"action": "get", "id": "a"})
		assert.NoErr(t, err)
		assert.Eq(t, true, res.Metadata["external"])
		assert.StrContains(t, res.Content, "# T\nid: a · kind: project · scope: project · updated: 2026-01-02\n\nbody")
		res, err = runMemory(t, tool, map[string]any{"action": "search", "query": "x"})
		assert.NoErr(t, err)
		assert.Eq(t, true, res.Metadata["external"])
	})
	t.Run("files read is not external", func(t *testing.T) {
		res, err := runMemory(t, tool, map[string]any{"action": "get", "id": "a", "scope": "user"})
		assert.NoErr(t, err)
		assert.Nil(t, res.Metadata)
	})
	t.Run("unknown backend counts as external", func(t *testing.T) {
		res, err := runMemory(t, NewMemoryTool(store, MemoryToolOptions{ProjectKey: "p"}), map[string]any{"action": "search", "query": "x"})
		assert.NoErr(t, err)
		assert.Eq(t, true, res.Metadata["external"])
		assert.StrContains(t, res.Content, `No project memory matched "x".`)
	})
	assert.Eq(t, tools.Meta{Category: tools.CategoryEdit, AgentState: true}, tools.MetaOf(tool))
}

// TestMemoryToolFilesRoundTrip runs the tool over Governed + files (the
// built-in backend) end to end.
func TestMemoryToolFilesRoundTrip(t *testing.T) {
	scope := memory.ProjectScope("p")
	backend, err := files.New(map[memory.Scope]files.Layout{scope: files.DirLayout{Dir: t.TempDir()}})
	assert.Require(t, assert.NoErr(t, err))
	tool := NewMemoryTool(memory.NewGoverned(backend, memory.Policy{}), MemoryToolOptions{Writable: true, ProjectKey: "p",
		Backend: func(memory.Scope) string { return backend.BackendName() }})
	res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "project", "title": "Release day", "body": "Fridays only"})
	assert.Require(t, assert.NoErr(t, err))
	change, _ := ParseMemoryResult(res.Content)
	res, err = runMemory(t, tool, map[string]any{"action": "search", "query": "fridays"})
	assert.NoErr(t, err)
	assert.StrContains(t, res.Content, change.ID)
	assert.Nil(t, res.Metadata)
	res, err = runMemory(t, tool, map[string]any{"action": "get", "id": change.ID})
	assert.NoErr(t, err)
	assert.True(t, strings.HasSuffix(res.Content, "Fridays only"))
}

func TestMemoryToolSummary(t *testing.T) {
	t.Run("schema declares summary", func(t *testing.T) {
		tool := NewMemoryTool(&fakeMemoryStore{}, MemoryToolOptions{ProjectKey: "p"})
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		assert.Eq(t, "string", props["summary"].(map[string]any)["type"])
		assert.StrContains(t, tool.Description(), "It is required for add")
	})
	t.Run("add keeps the given summary", func(t *testing.T) {
		store := &fakeMemoryStore{}
		tool := NewMemoryTool(store, MemoryToolOptions{Writable: true, ProjectKey: "p"})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "feedback", "title": "Use tabs",
			"body": "Always tabs.", "summary": "项目缩进约定"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "项目缩进约定", store.writes[0].Summary)
		assert.False(t, strings.Contains(res.Content, "正文首句"))
	})
	t.Run("add without a summary derives it from the body and says so", func(t *testing.T) {
		store := &fakeMemoryStore{}
		tool := NewMemoryTool(store, MemoryToolOptions{Writable: true, ProjectKey: "p"})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "project", "title": "Release day",
			"body": "Fridays only. No exceptions."})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Fridays only.", store.writes[0].Summary)
		assert.StrContains(t, res.Content, "已用正文首句作摘要：Fridays only.")
		change, ok := ParseMemoryResult(res.Content)
		assert.True(t, ok)
		assert.Eq(t, "Release day", change.Title)
	})
	t.Run("files backend stores and replays the summary", func(t *testing.T) {
		scope := memory.ProjectScope("p")
		backend, err := files.New(map[memory.Scope]files.Layout{scope: files.DirLayout{Dir: t.TempDir()}})
		assert.Require(t, assert.NoErr(t, err))
		tool := NewMemoryTool(memory.NewGoverned(backend, memory.Policy{}), MemoryToolOptions{Writable: true, ProjectKey: "p"})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "project", "title": "Release day",
			"body": "Fridays only", "summary": "发布窗口是周五"})
		assert.Require(t, assert.NoErr(t, err))
		change, _ := ParseMemoryResult(res.Content)
		ix, err := backend.Index(context.Background(), scope)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, ix.Lines, 1))
		assert.Eq(t, "发布窗口是周五", ix.Lines[0].Summary)
		// replace without a summary keeps the stored one.
		_, err = runMemory(t, tool, map[string]any{"action": "replace", "id": change.ID, "title": "Release day", "body": "Thursdays now."})
		assert.Require(t, assert.NoErr(t, err))
		e, err := backend.Get(context.Background(), scope, change.ID)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "发布窗口是周五", e.Summary)
		assert.Eq(t, "Thursdays now.", e.Body)
	})
}

func TestMemoryToolAppliesWhen(t *testing.T) {
	t.Run("schema and get rendering", func(t *testing.T) {
		store := &fakeMemoryStore{entry: memory.Entry{Title: "T", Body: "body", Kind: memory.KindProject,
			AppliesWhen: []string{"Go 1.26", "Windows"}}}
		tool := NewMemoryTool(store, MemoryToolOptions{ProjectKey: "p"})
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		applies := props["applies_when"].(map[string]any)
		assert.Eq(t, "array", applies["type"])
		res, err := runMemory(t, tool, map[string]any{"action": "get", "id": "a"})
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, "applies_when: Go 1.26 · Windows")
	})

	t.Run("index-style write result stays unchanged", func(t *testing.T) {
		tool := NewMemoryTool(&fakeMemoryStore{}, MemoryToolOptions{Writable: true, ProjectKey: "p"})
		res, err := runMemory(t, tool, map[string]any{"action": "add", "kind": "user", "title": "T", "body": "B", "applies_when": []string{"CI"}})
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, "kind: user")
		assert.False(t, strings.Contains(res.Content, "applies_when:"))
	})
}

func TestParseMemoryResult(t *testing.T) {
	for _, bad := range []string{"", "ok", "Saved memory ../x (project): t", "Saved memory a (people): t", "Saved memory a project: t"} {
		_, ok := ParseMemoryResult(bad)
		assert.False(t, ok, bad)
	}
}
