package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
