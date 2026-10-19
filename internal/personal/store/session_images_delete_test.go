package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestDeleteSessionFilesRemovesImages: a session's images directory
// (sessions/<id>/images/, design 0.3) goes with the session — the catalogs
// remove the whole session directory, so an image file must never be left as
// an orphan.
func TestDeleteSessionFilesRemovesImages(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	images := filepath.Join(s.sessionsDir, "s1", ImageDirName)
	assert.Require(t, assert.NoErr(t, os.MkdirAll(images, 0o700)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(images, "ab12.png"), []byte("x"), 0o600)))
	assert.NoErr(t, s.DeleteSessionFiles("s1"))
	_, err := os.Stat(filepath.Join(s.sessionsDir, "s1"))
	assert.True(t, os.IsNotExist(err), "the session directory is gone (images included)")
}
