package personal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// ageOutput backdates one spill file so the age rules of a compression pass
// apply to it.
func ageOutput(t *testing.T, sessionsDir, sid, stem string, age time.Duration) {
	t.Helper()
	path := filepath.Join(SessionOutputsDir(sessionsDir, sid), stem+".txt")
	stamp := time.Now().Add(-age)
	assert.Require(t, assert.NoErr(t, os.Chtimes(path, stamp, stamp)))
}

func TestScanSpill(t *testing.T) {
	dir := shortTempDir(t)
	writeOutput(t, dir, "sess_1", "aaaaaaaaaaaaaaaa", strings.Repeat("x", 100), false)
	writeOutput(t, dir, "sess_1", "bbbbbbbbbbbbbbbb", strings.Repeat("y", 300), true)
	writeOutput(t, dir, "sess_2", "cccccccccccccccc", strings.Repeat("z", 50), false)
	// No output: neither the temp file of an interrupted write nor a foreign
	// file may be counted as a spill file.
	assert.NoErr(t, os.WriteFile(filepath.Join(SessionOutputsDir(dir, "sess_2"), "dddddddddddddddd.txt.gz.tmp"), []byte("half"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(SessionOutputsDir(dir, "sess_2"), "notes.md"), []byte("note"), 0o600))

	usage, files, err := ScanSpill(dir)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 2, usage.Sessions)
	assert.Eq(t, 3, usage.Files)
	assert.Eq(t, 1, usage.Compressed)
	assert.Eq(t, int64(450), usage.LogicalBytes)  // the .gz reports its uncompressed size
	assert.True(t, usage.PhysicalBytes < usage.LogicalBytes, "physical %d", usage.PhysicalBytes)
	assert.Eq(t, 3, len(files))
	assert.Eq(t, "out:sess_1:aaaaaaaaaaaaaaaa", files[0].Ref())

	t.Run("an empty or missing root is not an error", func(t *testing.T) {
		usage, files, err := ScanSpill(filepath.Join(dir, "no-such-root"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, usage.Files)
		assert.Eq(t, 0, len(files))
	})

	t.Run("over quota is a logical comparison", func(t *testing.T) {
		usage.MaxBytes = usage.LogicalBytes - 1
		assert.True(t, usage.OverQuota())
		usage.MaxBytes = usage.LogicalBytes
		assert.False(t, usage.OverQuota())
		usage.MaxBytes = 0 // no quota
		assert.False(t, usage.OverQuota())
	})
}

func TestCompressSpillOutputs(t *testing.T) {
	body := strings.Repeat("a long output line\n", 400)
	newFixture := func(t *testing.T) string {
		t.Helper()
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_old", "aaaaaaaaaaaaaaaa", body, false)
		writeOutput(t, dir, "sess_new", "bbbbbbbbbbbbbbbb", body, false)
		ageOutput(t, dir, "sess_old", "aaaaaaaaaaaaaaaa", 30*24*time.Hour)
		ageOutput(t, dir, "sess_new", "bbbbbbbbbbbbbbbb", time.Hour)
		return dir
	}

	t.Run("dry run measures and changes nothing", func(t *testing.T) {
		dir := newFixture(t)
		report, err := CompressSpillOutputs(context.Background(), dir, SpillCompressOptions{OlderThan: 7 * 24 * time.Hour})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(report.Planned))
		assert.Eq(t, "out:sess_old:aaaaaaaaaaaaaaaa", report.Planned[0].File.Ref())
		assert.Eq(t, int64(len(body)), report.Planned[0].Size)
		assert.True(t, report.Planned[0].CompressedSize > 0 && report.Planned[0].CompressedSize < int64(len(body)))
		assert.Eq(t, 1, report.SkippedYoung)
		assert.Eq(t, int64(len(body)), report.BytesBefore)
		assert.True(t, report.AfterUsage.PhysicalBytes == report.Usage.PhysicalBytes-report.BytesBefore+report.BytesAfter)
		// Nothing on disk changed: the plain file is still there, no .gz and no
		// temp file was left behind.
		_, err = os.Stat(filepath.Join(SessionOutputsDir(dir, "sess_old"), "aaaaaaaaaaaaaaaa.txt"))
		assert.NoErr(t, err)
		assert.True(t, os.IsNotExist(statErr(t, filepath.Join(SessionOutputsDir(dir, "sess_old"), "aaaaaaaaaaaaaaaa.txt.gz"))))
		assert.Eq(t, 1, len(readDirNames(t, SessionOutputsDir(dir, "sess_old"))))
		usage, _, err := ScanSpill(dir)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(2*len(body)), usage.LogicalBytes)
	})

	t.Run("apply compresses the old file and keeps the ref readable", func(t *testing.T) {
		dir := newFixture(t)
		report, err := CompressSpillOutputs(context.Background(), dir, SpillCompressOptions{OlderThan: 7 * 24 * time.Hour, Apply: true})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(report.Planned))
		assert.Eq(t, report.BytesAfter, report.Planned[0].CompressedSize)
		names := readDirNames(t, SessionOutputsDir(dir, "sess_old"))
		assert.Eq(t, []string{"aaaaaaaaaaaaaaaa.txt.gz"}, names)
		assert.Eq(t, body, readOutput(t, mustOpen(t, dir, "out:sess_old:aaaaaaaaaaaaaaaa")))
		usage, _, err := ScanSpill(dir)
		assert.Require(t, assert.NoErr(t, err))
		// The logical size is unchanged: compression buys disk space, not quota.
		assert.Eq(t, int64(2*len(body)), usage.LogicalBytes)
		assert.Eq(t, report.Usage.PhysicalBytes-report.BytesBefore+report.BytesAfter, usage.PhysicalBytes)
	})

	t.Run("a session a live process uses is left alone", func(t *testing.T) {
		dir := newFixture(t)
		report, err := CompressSpillOutputs(context.Background(), dir, SpillCompressOptions{
			OlderThan: 7 * 24 * time.Hour, Apply: true, Active: map[string]bool{"sess_old": true},
		})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(report.Planned))
		assert.Eq(t, 1, report.SkippedActive)
		assert.Eq(t, []string{"aaaaaaaaaaaaaaaa.txt"}, readDirNames(t, SessionOutputsDir(dir, "sess_old")))
	})

	t.Run("a cancelled pass stops without touching the next file", func(t *testing.T) {
		dir := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := CompressSpillOutputs(ctx, dir, SpillCompressOptions{OlderThan: 7 * 24 * time.Hour, Apply: true})
		assert.Err(t, err)
		assert.Eq(t, []string{"aaaaaaaaaaaaaaaa.txt"}, readDirNames(t, SessionOutputsDir(dir, "sess_old")))
	})

	t.Run("an output that is already compressed is left alone", func(t *testing.T) {
		dir := shortTempDir(t)
		writeOutput(t, dir, "sess_gz", "aaaaaaaaaaaaaaaa", body, true)
		path := filepath.Join(SessionOutputsDir(dir, "sess_gz"), "aaaaaaaaaaaaaaaa.txt.gz")
		stamp := time.Now().Add(-30 * 24 * time.Hour)
		assert.Require(t, assert.NoErr(t, os.Chtimes(path, stamp, stamp)))
		report, err := CompressSpillOutputs(context.Background(), dir, SpillCompressOptions{OlderThan: 7 * 24 * time.Hour, Apply: true})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(report.Planned))
		assert.Eq(t, 0, report.SkippedYoung)
		assert.Eq(t, []string{"aaaaaaaaaaaaaaaa.txt.gz"}, readDirNames(t, SessionOutputsDir(dir, "sess_gz")))
	})
}

func statErr(t *testing.T, path string) error {
	t.Helper()
	_, err := os.Stat(path)
	return err
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	assert.Require(t, assert.NoErr(t, err))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
