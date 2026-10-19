package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

// newBackupHome builds a home with an open (empty) database, as two tests below
// rely on the snapshot step.
func newBackupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "db"), 0o700))
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	return home
}

// TestCreateAlwaysIncludesSessionImages: an images file belongs to every
// backup (design 0.3), counts as its own category, the omitted spill bytes do
// not include it and a restore installs it back under the session directory.
func TestCreateAlwaysIncludesSessionImages(t *testing.T) {
	home := newBackupHome(t)
	images := filepath.Join(home, "sessions", "s1", "images")
	assert.NoErr(t, os.MkdirAll(images, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(images, "ab12.png"), []byte("PNGDATA"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: test\n"), 0o600))

	archive := filepath.Join(home, "with-images.suga-backup.zip")
	manifest, err := Create(context.Background(), Options{Home: home, Output: archive, SugaVersion: "test"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "excluded", manifest.Spill.Policy)
	assert.Eq(t, 1, manifest.Images.FileCount)
	assert.Eq(t, int64(len("PNGDATA")), manifest.Images.Bytes)
	var found *File
	for i := range manifest.Files {
		if manifest.Files[i].Path == "sessions/s1/images/ab12.png" {
			found = &manifest.Files[i]
		}
	}
	assert.Require(t, found != nil, "the image file is in the manifest")
	assert.Eq(t, categorySessionImages, found.Category)

	// A restore installs the file back where the model can read it.
	target := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(target, "db"), 0o700))
	repo, err := store.Open(filepath.Join(target, "db", "sessions.db"))
	assert.NoErr(t, repo.Close())
	assert.Require(t, assert.NoErr(t, err))
	_, err = Restore(context.Background(), archive, target, true, nil)
	assert.Require(t, assert.NoErr(t, err))
	restored, err := os.ReadFile(filepath.Join(target, "sessions", "s1", "images", "ab12.png"))
	assert.NoErr(t, err)
	assert.Eq(t, "PNGDATA", string(restored))
}

// TestCreateOmitsSessionImagesFromSpillCounting: with --include-spill the
// spill counters count only outputs/ files, not the always-included images.
func TestCreateOmitsSessionImagesFromSpillCounting(t *testing.T) {
	home := newBackupHome(t)
	images := filepath.Join(home, "sessions", "s1", "images")
	assert.NoErr(t, os.MkdirAll(images, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(images, "ab12.png"), []byte("PNGDATA"), 0o600))
	out := filepath.Join(home, "sessions", "s1", "outputs")
	assert.NoErr(t, os.MkdirAll(out, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(out, "x.txt"), []byte("output"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: test\n"), 0o600))

	archive := filepath.Join(home, "spill.zip")
	manifest, err := Create(context.Background(), Options{Home: home, Output: archive, IncludeSpill: true})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, manifest.Spill.FileCount, "only the outputs file is spill")
	assert.Eq(t, int64(len("output")), manifest.Spill.Bytes)
	assert.Eq(t, 1, manifest.Images.FileCount)
}
