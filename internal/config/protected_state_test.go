package axconfig

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
)

// protectedStateEnv isolates SUGA_HOME and publishes configPath as the file this
// process loaded, so one test sees only its own comparison (Load itself can run
// once per process: the test sets the state Load would leave).
func protectedStateEnv(t *testing.T, body string) (string, string) {
	t.Helper()
	configPath, snapshotPath := protectTestPaths(t)
	assert.NoErr(t, os.WriteFile(configPath, []byte(body), 0o600))
	loadedConfig = loadedConfigState{path: configPath, data: []byte(body), ok: true}
	ResetProtectedStateForTest()
	t.Cleanup(func() {
		ResetProtectedStateForTest()
		ResetLoadedStateForTest()
	})
	return configPath, snapshotPath
}

func TestCheckProtectedOnce(t *testing.T) {
	t.Run("no change is cached as no notice", func(t *testing.T) {
		// The first comparison creates the snapshot from the current file, so
		// nothing changed for this process.
		protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		state := CheckProtectedOnce()
		assert.Require(t, assert.NotNil(t, state))
		assert.True(t, state.Change.Empty())
		assert.Eq(t, 0, len(state.recorded))
		change, ok := state.NoticeFor("s1")
		assert.False(t, ok)
		assert.True(t, change.Empty())
		assert.NoErr(t, state.Advance())
	})

	t.Run("changed keys are reported once per session", func(t *testing.T) {
		configPath, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		// A confirmed snapshot of another value: the file changed after the user
		// last saw it.
		assert.NoErr(t, writeProtectedSnapshot(snapshotPath, protectTestDigest(t, "tools:\n  read_roots: [b]\n")))
		ResetProtectedStateForTest()
		state := CheckProtectedOnce()
		assert.Eq(t, []string{"tools.read_roots"}, state.Change.Keys)
		assert.Eq(t, configPath, state.Path)

		change, ok := state.NoticeFor("s1")
		assert.True(t, ok)
		assert.Eq(t, []string{"tools.read_roots"}, change.Keys)
		// The same session asks again on its next prompt and gets nothing.
		_, ok = state.NoticeFor("s1")
		assert.False(t, ok)
		// Another session still gets the notice.
		_, ok = state.NoticeFor("s2")
		assert.True(t, ok)
	})

	t.Run("the first comparison of the process wins", func(t *testing.T) {
		protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		first := CheckProtectedOnce()
		assert.True(t, first == CheckProtectedOnce())
	})

	t.Run("a configuration this process never loaded is not cached", func(t *testing.T) {
		ResetProtectedStateForTest()
		ResetLoadedStateForTest()
		t.Cleanup(func() {
			ResetProtectedStateForTest()
			ResetLoadedStateForTest()
		})
		state := CheckProtectedOnce()
		assert.Require(t, assert.NotNil(t, state))
		assert.True(t, state.Change.Empty())
		// Without a comparison nothing advances and no session is told.
		_, ok := state.NoticeFor("s1")
		assert.False(t, ok)
		assert.NoErr(t, state.Advance())
		assert.True(t, state != CheckProtectedOnce())
	})

	t.Run("an unreadable snapshot is reported and then repaired by advance", func(t *testing.T) {
		_, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		assert.NoErr(t, os.WriteFile(snapshotPath, []byte("{not json"), 0o600))
		ResetProtectedStateForTest()
		state := CheckProtectedOnce()
		assert.True(t, state.Change.SnapshotUnreadable)
		assert.Eq(t, []string{"tools.read_roots"}, state.Change.Keys)
		change, ok := state.NoticeFor("s1")
		assert.True(t, ok)
		assert.True(t, change.SnapshotUnreadable)
		// The notice listed every key as changed; once chat showed it, the snapshot is
		// rewritten from the current file and the next start compares normally.
		assert.NoErr(t, state.Advance())
		ResetProtectedStateForTest()
		assert.True(t, CheckProtectedOnce().Change.Empty())
	})

	t.Run("a failed comparison cannot advance", func(t *testing.T) {
		configPath, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		// A file where the snapshot directory belongs makes every snapshot read and
		// write fail.
		assert.NoErr(t, os.MkdirAll(filepath.Dir(snapshotPath), 0o700))
		assert.NoErr(t, os.RemoveAll(filepath.Dir(snapshotPath)))
		assert.NoErr(t, os.WriteFile(filepath.Dir(snapshotPath), []byte("not a dir"), 0o600))
		ResetProtectedStateForTest()
		state := CheckProtectedOnce()
		assert.True(t, state.Change.Empty())
		_, ok := state.NoticeFor("s1")
		assert.False(t, ok)
		assert.NoErr(t, state.Advance())
		_, err := os.Stat(snapshotPath)
		assert.True(t, os.IsNotExist(err), "advance wrote a snapshot without a comparison: %v", err)
		assert.Eq(t, configPath, state.Path)
	})
}

func TestProtectedStateAdvance(t *testing.T) {
	t.Run("advance writes the compared digest", func(t *testing.T) {
		configPath, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		assert.NoErr(t, writeProtectedSnapshot(snapshotPath, protectTestDigest(t, "tools:\n  read_roots: [b]\n")))
		ResetProtectedStateForTest()
		state := CheckProtectedOnce()
		assert.Eq(t, []string{"tools.read_roots"}, state.Change.Keys)

		// Another key changes after the comparison and before the banner was shown:
		// advance must keep the digest it compared, so the next start still reports
		// that key instead of silently absorbing it.
		assert.NoErr(t, os.WriteFile(configPath, []byte("tools:\n  read_roots: [a]\n  read_deny: [x]\n"), 0o600))
		assert.NoErr(t, state.Advance())

		digest := protectTestDigest(t, "tools:\n  read_roots: [a]\n  read_deny: [x]\n")
		change, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"tools.read_deny"}, change.Keys)
	})

	t.Run("nothing advances without a change", func(t *testing.T) {
		_, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
		ResetProtectedStateForTest()
		state := CheckProtectedOnce()
		assert.True(t, state.Change.Empty())
		before, err := os.Stat(snapshotPath)
		assert.NoErr(t, err)
		assert.NoErr(t, state.Advance())
		after, err := os.Stat(snapshotPath)
		assert.NoErr(t, err)
		assert.Eq(t, before.ModTime(), after.ModTime())
	})

	t.Run("a nil state is a no-op", func(t *testing.T) {
		var state *ProtectedState
		assert.NoErr(t, state.Advance())
		_, ok := state.NoticeFor("s1")
		assert.False(t, ok)
	})
}

// TestProtectedStateNoticeConcurrent: ACP serves every request on its own
// goroutine, so the same state is asked by several sessions at once; one session
// still gets the notice exactly once (run with -race).
func TestProtectedStateNoticeConcurrent(t *testing.T) {
	_, snapshotPath := protectedStateEnv(t, "tools:\n  read_roots: [a]\n")
	assert.NoErr(t, writeProtectedSnapshot(snapshotPath, protectTestDigest(t, "tools:\n  read_roots: [b]\n")))
	ResetProtectedStateForTest()
	state := CheckProtectedOnce()

	const sessions, calls = 4, 8
	counts := make([]int, sessions)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for session := 0; session < sessions; session++ {
		for call := 0; call < calls; call++ {
			wg.Add(1)
			go func(session int) {
				defer wg.Done()
				if _, ok := state.NoticeFor(sessionIDOf(session)); ok {
					mu.Lock()
					counts[session]++
					mu.Unlock()
				}
			}(session)
		}
	}
	wg.Wait()
	for session, count := range counts {
		assert.Eq(t, 1, count, "session %d", session)
	}
}

// sessionIDOf names a session in TestProtectedStateNoticeConcurrent.
func sessionIDOf(session int) string {
	return "s" + string(rune('a'+session))
}

func TestProtectedChangeText(t *testing.T) {
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	path := filepath.Join("home", "config.yaml")

	t.Run("changed keys are named and joined per language", func(t *testing.T) {
		change := ProtectedChange{Keys: []string{"tools.read_roots", "network.proxy.url"}}
		assert.Eq(t, "安全相关配置自上次确认以来有变化：tools.read_roots、network.proxy.url；如果不是你改的，请检查 "+path,
			ProtectedChangeText(zh, change, path))
		assert.Eq(t, "Protected settings changed since you last confirmed: tools.read_roots, network.proxy.url. If you did not change them, check "+path,
			ProtectedChangeText(en, change, path))
	})

	t.Run("a whole-file change names no keys", func(t *testing.T) {
		assert.Eq(t, "配置文件有变化（含无法逐键比较的写法），请检查 "+path,
			ProtectedChangeText(zh, ProtectedChange{WholeFile: true}, path))
	})

	t.Run("an unreadable snapshot lists every key as changed", func(t *testing.T) {
		change := ProtectedChange{Keys: []string{"tools.read_deny"}, SnapshotUnreadable: true}
		assert.Eq(t, "安全相关配置快照无法读取，以下键视为已改动：tools.read_deny；请检查 "+path,
			ProtectedChangeText(zh, change, path))
	})

	t.Run("a nil translator falls back to Chinese", func(t *testing.T) {
		assert.Eq(t, ProtectedChangeText(zh, ProtectedChange{WholeFile: true}, path),
			ProtectedChangeText(nil, ProtectedChange{WholeFile: true}, path))
	})
}
