package chat

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestInputHistoryLoadOrderAndRedaction(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Run("disabled history does not read or write", func(t *testing.T) {
		h, err := newInputHistory(t.TempDir(), "project", false, 3)
		assert.NoErr(t, err)
		assert.False(t, h.enabled)
		assert.Eq(t, "", h.path)
		assert.NoErr(t, h.add("not persisted"))
	})
	run := t.TempDir()
	h, err := newInputHistory(run, "project", true, 3)
	assert.NoErr(t, err)
	assert.NoErr(t, h.add("old"))
	h.entries = append(h.entries, historyEntry{Workspace: t.TempDir(), Text: "other"})
	assert.NoErr(t, h.add("token=secret"))
	// project 范围下另一项目的条目不召回；all 范围的行为在
	// TestInputHistoryProjectScope 里单独验证。
	assert.Eq(t, []string{"old", "token=[REDACTED]"}, h.lines())
	loaded, err := newInputHistory(run, "project", true, 3)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"old", "token=[REDACTED]"}, loaded.lines())
}

func TestInputHistoryConcurrentAppend(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	root := t.TempDir()
	h, err := newInputHistory(root, "project", true, 1000)
	assert.NoErr(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); assert.NoErr(t, h.add(string(rune('a'+i)))) }(i)
	}
	wg.Wait()
	data, err := os.ReadFile(h.path)
	assert.NoErr(t, err)
	assert.True(t, len(data) > 0)
	assert.Eq(t, 20, len(h.entries))
}

func TestInputHistoryTrimAndReload(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Run("keeps a margin before trimming", func(t *testing.T) {
		work := t.TempDir()
		h, err := newInputHistory(work, "project", true, 10)
		assert.NoErr(t, err)
		for i := 0; i < historyTrimLimit(10); i++ {
			assert.NoErr(t, h.add(string(rune('a'+i))))
		}
		data, err := os.ReadFile(h.path)
		assert.NoErr(t, err)
		assert.Eq(t, historyTrimLimit(10), len(strings.Split(strings.TrimSpace(string(data)), "\n")))
		assert.NoErr(t, h.add("last"))
		loaded, err := newInputHistory(work, "project", true, 10)
		assert.NoErr(t, err)
		assert.Eq(t, 10, len(loaded.entries))
		assert.Eq(t, "last", loaded.entries[len(loaded.entries)-1].Text)
	})
	t.Run("does not lose another process append", func(t *testing.T) {
		work := t.TempDir()
		h1, err := newInputHistory(work, "all", true, 3)
		assert.NoErr(t, err)
		assert.NoErr(t, h1.add("one"))
		other := t.TempDir()
		h2, err := newInputHistory(other, "all", true, 3)
		assert.NoErr(t, err)
		assert.NoErr(t, h2.add("two"))
		assert.NoErr(t, h1.add("three"))
		assert.NoErr(t, h2.add("four"))
		loaded, err := newInputHistory(work, "all", true, 3)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"three", "two", "four"}, loaded.lines())
	})
}

// TestInputHistoryProjectScope: chat.history_scope 配置后 Up/Down 的范围。
// "project" 用项目根比对（子目录与 git 根、Windows 大小写都算同一项目，旧
// 记录里存的 cwd 向上套根后再比对），"all" 保留全部并让当前项目优先，空值
// 按 project 处理。
func TestInputHistoryProjectScope(t *testing.T) {
	// 一个"项目"在这里就是一个 git 仓库：假仓库根建一个 .git，子目录向上套到
	// 同一根。每个子测试用自己的 SUGA_HOME，前一个写的 history.jsonl 不串
	// 到下一个的比对里。
	repo := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o700))
	sub := filepath.Join(repo, "sub")
	assert.NoErr(t, os.MkdirAll(sub, 0o700))
	other := t.TempDir()

	t.Run("a subdirectory and the repository root are one project", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		h, err := newInputHistory(sub, "project", true, 100)
		assert.NoErr(t, err)
		assert.Eq(t, repo, h.workspace)
	})

	t.Run("project scope recalls only the current project", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		h, err := newInputHistory(repo, "project", true, 100)
		assert.NoErr(t, err)
		assert.NoErr(t, h.add("mine"))
		ho, err := newInputHistory(other, "project", true, 100)
		assert.NoErr(t, err)
		assert.NoErr(t, ho.add("theirs"))
		// 手工补的条目：旧记录里存的 cwd（本仓库的子目录）、大小写不同的同一
		// 项目（Windows 不分大小写）与其他项目的条目。
		h.entries = append(h.entries,
			historyEntry{Workspace: sub, Text: "old cwd"},
			historyEntry{Workspace: strings.ToUpper(repo), Text: "case"},
			historyEntry{Workspace: other, Text: "other project"},
		)
		assert.Eq(t, []string{"mine", "old cwd", "case"}, h.lines())
	})

	t.Run("all scope keeps the old cross-project order", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		ha, err := newInputHistory(repo, "all", true, 100)
		assert.NoErr(t, err)
		assert.NoErr(t, ha.add("b1"))
		ha.entries = append(ha.entries,
			historyEntry{Workspace: sub, Text: "old cwd"},
			historyEntry{Workspace: other, Text: "other project"},
		)
		assert.Eq(t, []string{"b1", "old cwd", "other project"}, ha.lines())
	})

	t.Run("an empty scope behaves like project", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		hd, err := newInputHistory(repo, "", true, 100)
		assert.NoErr(t, err)
		assert.NoErr(t, hd.add("d1"))
		hd.entries = append(hd.entries, historyEntry{Workspace: other, Text: "other project"})
		assert.Eq(t, []string{"d1"}, hd.lines())
	})
}
