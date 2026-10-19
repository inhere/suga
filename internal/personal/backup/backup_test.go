package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
)

func TestCreateArchiveManifestExcludesEnvAndSpillByDefault(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "memory", "user"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("key: ${SECRET}\n"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, ".env"), []byte("SECRET=do-not-copy\n"), 0o600))
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "sessions", "s1", "outputs"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "sessions", "s1", "outputs", "x.txt"), []byte("spill"), 0o600))
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "db"), 0o700))
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	archive := filepath.Join(home, "out.suga-backup.zip")
	manifest, err := Create(context.Background(), Options{Home: home, Output: archive, SugaVersion: "test"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "excluded", manifest.Spill.Policy)
	zr, err := zip.OpenReader(archive)
	assert.Require(t, assert.NoErr(t, err))
	defer zr.Close()
	for _, f := range zr.File {
		assert.Eq(t, false, f.Name == ".env")
		assert.Eq(t, false, strings.Contains(f.Name, "x.txt"))
	}
	var config []byte
	for _, f := range zr.File {
		if f.Name == "config.yaml" {
			r, e := f.Open()
			assert.NoErr(t, e)
			config, e = io.ReadAll(r)
			assert.NoErr(t, e)
			assert.NoErr(t, r.Close())
		}
	}
	assert.Eq(t, "key: ${SECRET}\n", string(config))
}

func TestCreateIncludeSpillAndValidateHash(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "db"), 0o700))
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "sessions", "s", "outputs"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: test\n"), 0o600))
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "sessions", "s", "outputs", "o.txt"), []byte("output"), 0o600))
	archive := filepath.Join(home, "with-spill.zip")
	manifest, err := Create(context.Background(), Options{Home: home, Output: archive, IncludeSpill: true})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "included", manifest.Spill.Policy)
	assert.Eq(t, 1, manifest.Spill.FileCount)
	_, err = Validate(archive, home)
	assert.NoErr(t, err)
	bad := filepath.Join(home, "bad.zip")
	assert.NoErr(t, rewriteArchive(t, archive, bad, func(name string, body []byte) []byte {
		if name == "config.yaml" {
			return append(body, 'x')
		}
		return body
	}))
	_, err = Validate(bad, home)
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "sha256 mismatch")
}

func TestValidateRejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "bad.zip")
	file, err := os.Create(path)
	assert.Require(t, assert.NoErr(t, err))
	zw := zip.NewWriter(file)
	manifest := Manifest{FormatVersion: FormatVersion, Files: []File{{Path: "../evil", SHA256: "00"}}}
	data, err := json.Marshal(manifest)
	assert.NoErr(t, err)
	w, err := zw.Create("manifest.json")
	assert.NoErr(t, err)
	_, err = w.Write(data)
	assert.NoErr(t, err)
	assert.NoErr(t, zw.Close())
	assert.NoErr(t, file.Close())
	_, err = Validate(path, home)
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "path traversal")
}

func TestRestoreRestoresDatabaseAndCreatesAutomaticBackup(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, "db", "sessions.db")
	assert.NoErr(t, os.MkdirAll(filepath.Dir(dbPath), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: before\n"), 0o600))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(context.Background(), store.Session{ID: "keep", Status: store.SessionOpen}))
	assert.NoErr(t, repo.Close())
	archive := filepath.Join(home, "original.zip")
	_, err = Create(context.Background(), Options{Home: home, Output: archive})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: changed\n"), 0o600))
	repo, err = store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(context.Background(), store.Session{ID: "extra", Status: store.SessionOpen}))
	assert.NoErr(t, repo.Close())
	auto := func(path string) error {
		_, e := Create(context.Background(), Options{Home: home, Output: path, IncludeSpill: true})
		return e
	}
	_, err = Restore(context.Background(), archive, home, false, auto)
	assert.NoErr(t, err)
	body, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	assert.NoErr(t, err)
	assert.Eq(t, "model: before\n", string(body))
	repo, err = store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.GetSession(context.Background(), "keep")
	assert.NoErr(t, err)
	_, err = repo.GetSession(context.Background(), "extra")
	assert.Err(t, err)
	assert.NoErr(t, repo.Close())
	entries, err := os.ReadDir(filepath.Join(home, "backups"))
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(entries))
}

func TestRestoreDryRunDoesNotChangeConfig(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, "db", "sessions.db")
	assert.NoErr(t, os.MkdirAll(filepath.Dir(dbPath), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: stable\n"), 0o600))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	archive := filepath.Join(home, "archive.zip")
	_, err = Create(context.Background(), Options{Home: home, Output: archive})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: live\n"), 0o600))
	_, err = Validate(archive, home)
	assert.NoErr(t, err)
	body, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	assert.NoErr(t, err)
	assert.Eq(t, "model: live\n", string(body))
}

func TestCreateAlwaysIncludesToolBodyFiles(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "db"), 0o700))
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "sessions", "s1", "outputs"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "sessions", "s1", "outputs", "x.txt"), []byte("spill"), 0o600))
	bodyPath := filepath.Join(home, "sessions", "s1", store.ToolBodyFileName)
	assert.NoErr(t, os.WriteFile(bodyPath, []byte("tool body record\n"), 0o600))
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())

	archive := filepath.Join(home, "out.zip")
	manifest, err := Create(context.Background(), Options{Home: home, Output: archive})
	assert.Require(t, assert.NoErr(t, err))
	// The tool body file is in the archive although spill is excluded, and it is
	// not counted as omitted spill bytes.
	assert.Eq(t, "excluded", manifest.Spill.Policy)
	assert.Eq(t, 1, manifest.ToolBodies.FileCount)
	assert.Eq(t, int64(len("tool body record\n")), manifest.ToolBodies.Bytes)
	assert.Eq(t, int64(len("spill")), manifest.Spill.OmittedBytes)
	zr, err := zip.OpenReader(archive)
	assert.Require(t, assert.NoErr(t, err))
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if f.Name != "sessions/s1/"+store.ToolBodyFileName {
			continue
		}
		found = true
		r, e := f.Open()
		assert.NoErr(t, e)
		body, e := io.ReadAll(r)
		assert.NoErr(t, e)
		assert.NoErr(t, r.Close())
		assert.Eq(t, "tool body record\n", string(body))
	}
	assert.True(t, found, "the archive must hold the tool body file")
	// --include-spill adds the output file but does not count the body file as spill.
	manifest, err = Create(context.Background(), Options{Home: home, Output: archive, IncludeSpill: true})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "included", manifest.Spill.Policy)
	assert.Eq(t, 1, manifest.Spill.FileCount)
	assert.Eq(t, int64(len("spill")), manifest.Spill.Bytes)
}

// TestWriteArchiveCopiesRecordedBytes: a file that grows between the size scan
// and the archive copy is still stored exactly as recorded, and the manifest hash
// matches the stored bytes (a live session file being appended to during backup).
func TestWriteArchiveCopiesRecordedBytes(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "sessions", "s1", store.ToolBodyFileName)
	assert.NoErr(t, os.MkdirAll(filepath.Dir(source), 0o700))
	first := []byte("{\"seq\":1}\n")
	assert.NoErr(t, os.WriteFile(source, first, 0o600))
	manifest := Manifest{FormatVersion: FormatVersion, CreatedAt: time.Now(), Files: []File{{
		Path: "sessions/s1/" + store.ToolBodyFileName, Category: categoryToolBodies, Size: int64(len(first)), Source: source,
	}}}
	// A writer appends after the scan, before the copy.
	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0o600)
	assert.Require(t, assert.NoErr(t, err))
	_, err = file.Write([]byte("{\"seq\":2}\n"))
	assert.NoErr(t, err)
	assert.NoErr(t, file.Close())

	archive := filepath.Join(home, "out.zip")
	got, err := writeArchive(archive, "", home, manifest)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(len(first)), got.Files[0].Size)
	sum := sha256.Sum256(first)
	assert.Eq(t, hex.EncodeToString(sum[:]), got.Files[0].SHA256)
	zr, err := zip.OpenReader(archive)
	assert.Require(t, assert.NoErr(t, err))
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != got.Files[0].Path {
			continue
		}
		r, e := f.Open()
		assert.NoErr(t, e)
		body, e := io.ReadAll(r)
		assert.NoErr(t, e)
		assert.NoErr(t, r.Close())
		assert.Eq(t, string(first), string(body))
	}
}

// TestRestoreInstallsToolBodyFiles: the restore brings the session file back,
// also when the target home already has one (Windows cannot rename over an
// existing file), and the restored ledger reads its body again.
func TestRestoreInstallsToolBodyFiles(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dbPath := filepath.Join(home, "db", "sessions.db")
	assert.NoErr(t, os.MkdirAll(filepath.Dir(dbPath), 0o700))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Status: store.SessionOpen}))
	body := strings.Repeat("line of tool output\n", 200)
	raw, err := json.Marshal(map[string]any{"Role": "tool", "Content": body, "ToolResult": map[string]any{"CallID": "c1", "Name": "shell", "Content": body}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{Type: store.EntryTool, JSON: raw})
	assert.Require(t, assert.NoErr(t, err))
	path := filepath.Join(home, "sessions", "s1", store.ToolBodyFileName)
	_, err = os.Stat(path)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())

	archive := filepath.Join(home, "backup.zip")
	manifest, err := Create(ctx, Options{Home: home, Output: archive})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, manifest.ToolBodies.FileCount)

	// Lose the body file and leave a stale copy behind for the restore to replace.
	assert.NoErr(t, os.WriteFile(path, []byte("stale\n"), 0o600))
	auto := func(path string) error {
		_, e := Create(ctx, Options{Home: home, Output: path, IncludeSpill: true})
		return e
	}
	_, err = Restore(ctx, archive, home, false, auto)
	assert.NoErr(t, err)
	report, err := ToolBodyCheck(ctx, home)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, report.OK(), "every stub finds its record again: "+strings.Join(report.Problems, "; "))
	assert.Eq(t, int64(1), report.Checked)
	repo, err = store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	entries, err := repo.ListEntries(ctx, "s1", store.EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, len(entries))
	assert.Eq(t, string(raw), string(entries[0].JSON))
}

func rewriteArchive(t *testing.T, source, destination string, mutate func(string, []byte) []byte) error {
	t.Helper()
	in, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	for _, f := range in.File {
		r, err := f.Open()
		if err != nil {
			return err
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return err
		}
		body = mutate(f.Name, body)
		w, err := zw.Create(f.Name)
		if err != nil {
			return err
		}
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}
