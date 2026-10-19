package personal

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// writeOutput writes one spill file of a session: the plain .txt or, with
// compress set, the .txt.gz that a compression pass leaves behind.
func writeOutput(t *testing.T, sessionsDir, sid, stem, content string, compress bool) {
	t.Helper()
	dir := SessionOutputsDir(sessionsDir, sid)
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
	if !compress {
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, stem+".txt"), []byte(content), 0o600)))
		return
	}
	f, err := os.Create(filepath.Join(dir, stem+gzSuffix))
	assert.Require(t, assert.NoErr(t, err))
	_, err = gzipInto(f, strings.NewReader(content))
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, f.Close()))
}

func readOutput(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	body, err := io.ReadAll(r)
	assert.Require(t, assert.NoErr(t, err))
	return string(body)
}

func TestOpenOutput(t *testing.T) {
	t.Run("plain and compressed outputs read the same content", func(t *testing.T) {
		dir := shortTempDir(t)
		body := "line one\nline two\n中文行\n" + strings.Repeat("tail\n", 100)
		writeOutput(t, dir, "sess_1", "aaaaaaaaaaaaaaaa", body, false)
		writeOutput(t, dir, "sess_1", "bbbbbbbbbbbbbbbb", body, true)
		assert.Eq(t, body, readOutput(t, mustOpen(t, dir, "out:sess_1:aaaaaaaaaaaaaaaa")))
		assert.Eq(t, body, readOutput(t, mustOpen(t, dir, "out:sess_1:bbbbbbbbbbbbbbbb")))
	})

	t.Run("the plain file wins when both forms exist", func(t *testing.T) {
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_2", "cccccccccccccccc", "plain wins", false)
		writeOutput(t, dir, "sess_2", "cccccccccccccccc", "compressed loses", true)
		assert.Eq(t, "plain wins", readOutput(t, mustOpen(t, dir, "out:sess_2:cccccccccccccccc")))
	})

	t.Run("a damaged gzip is reported as such", func(t *testing.T) {
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_3", "dddddddddddddddd", "body", true)
		path := filepath.Join(SessionOutputsDir(dir, "sess_3"), "dddddddddddddddd"+gzSuffix)
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte("not gzip at all"), 0o600)))
		_, err := OpenOutput(dir, "out:sess_3:dddddddddddddddd")
		assert.ErrMsgContains(t, err, "damaged gzip")
	})

	t.Run("a missing output names the ref", func(t *testing.T) {
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_4", "eeeeeeeeeeeeeeee", "body", false)
		_, err := OpenOutput(dir, "out:sess_4:ffffffffffffffff")
		assert.ErrMsgContains(t, err, "out:sess_4:ffffffffffffffff")
		_, err = OpenOutput(dir, "out:sess_missing:eeeeeeeeeeeeeeee")
		assert.ErrMsgContains(t, err, "out:sess_missing:eeeeeeeeeeeeeeee not found")
		_, err = OpenOutput(filepath.Join(dir, "no-such-root"), "out:sess_4:eeeeeeeeeeeeeeee")
		assert.ErrMsgContains(t, err, "no saved outputs")
	})

	t.Run("refs that could leave the session directory are refused", func(t *testing.T) {
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_5", "aaaaaaaaaaaaaaaa", "body", false)
		for _, ref := range []string{
			"out:sess_5:../../../../etc/passwd",
			"out:../sess_5:aaaaaaaaaaaaaaaa",
			"out:sess_5/..:aaaaaaaaaaaaaaaa",
			"out::aaaaaaaaaaaaaaaa",
			"out:sess_5:AAAAAAAAAAAAAAAA",
			"out:sess_5:aaaaaaaaaaaaaaa",
		} {
			_, err := OpenOutput(dir, ref)
			assert.Err(t, err, ref)
		}
	})

	t.Run("a symlink out of the session directory is refused", func(t *testing.T) {
		dir := shortTempDir(t)
		outside := filepath.Join(shortTempDir(t), "secret.txt")
		assert.Require(t, assert.NoErr(t, os.WriteFile(outside, []byte("outside"), 0o600)))
		writeOutput(t, dir, "sess_6", "aaaaaaaaaaaaaaaa", "body", false)
		link := filepath.Join(SessionOutputsDir(dir, "sess_6"), "bbbbbbbbbbbbbbbb.txt")
		if err := os.Symlink(outside, link); err != nil {
			// Windows without the symlink privilege cannot build this fixture.
			t.Skipf("symlinks are unavailable here: %v", err)
		}
		_, err := OpenOutput(dir, "out:sess_6:bbbbbbbbbbbbbbbb")
		assert.Err(t, err)
	})

	t.Run("a session directory that is a symlink out of the root is refused", func(t *testing.T) {
		dir := shortTempDir(t)
		outside := shortTempDir(t)
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Join(outside, "outputs"), 0o700)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(outside, "outputs", "aaaaaaaaaaaaaaaa.txt"), []byte("outside"), 0o600)))
		if err := os.Symlink(outside, filepath.Join(dir, "sess_7")); err != nil {
			t.Skipf("symlinks are unavailable here: %v", err)
		}
		_, err := OpenOutput(dir, "out:sess_7:aaaaaaaaaaaaaaaa")
		assert.Err(t, err)
	})
}

func mustOpen(t *testing.T, sessionsDir, ref string) io.ReadCloser {
	t.Helper()
	r, err := OpenOutput(sessionsDir, ref)
	assert.Require(t, assert.NoErr(t, err))
	return r
}

// compressOutput replaces the plain .txt of a stem with its gzip form and
// removes the original, the way the spill maintenance pass publishes one file.
func compressOutput(t *testing.T, sessionsDir, sid, stem string) error {
	t.Helper()
	dir := SessionOutputsDir(sessionsDir, sid)
	body, err := os.ReadFile(filepath.Join(dir, stem+".txt"))
	if err != nil {
		return err
	}
	writeOutput(t, sessionsDir, sid, stem, string(body), true)
	return os.Remove(filepath.Join(dir, stem+".txt"))
}

// TestLedgerReadsCompressedOutput compresses an output the way the maintenance
// pass does and reads it back through recall and session_search(ref): the ref
// does not change and the content is the same as before.
func TestLedgerReadsCompressedOutput(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := withObserveScope(context.Background(), "s_gz", "turn_1")
	f.session(t, "s_gz", "")
	body := "first line\nneedle alpha\nlast line\n"
	ref, err := NewSessionSpiller(f.dir).Spill(ctx, agentcore.ToolCall{CallID: "c1"}, body)
	assert.Require(t, assert.NoErr(t, err))
	stem := strings.TrimPrefix(ref, "out:s_gz:")
	dir := SessionOutputsDir(f.dir, "s_gz")
	assert.Require(t, assert.NoErr(t, compressOutput(t, f.dir, "s_gz", stem)))

	t.Run("recall reads the compressed output", func(t *testing.T) {
		text, err := f.ledger.Fetch(context.Background(), ref, 1, 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, text, "needle alpha")
		assert.StrContains(t, text, "3\tlast line")
	})

	t.Run("session_search with the ref scans the compressed output", func(t *testing.T) {
		res, err := f.ledger.Search(context.Background(), native.LedgerQuery{Ref: ref, Query: "needle alpha"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(res.Hits))
		assert.Eq(t, 2, res.Hits[0].Line)
	})

	t.Run("only the compressed file is left", func(t *testing.T) {
		_, err := os.Stat(filepath.Join(dir, stem+".txt"))
		assert.True(t, os.IsNotExist(err), "the plain file should be gone")
		_, err = os.Stat(filepath.Join(dir, stem+gzSuffix))
		assert.NoErr(t, err)
	})
}
