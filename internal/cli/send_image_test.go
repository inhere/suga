package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

// Tests for `suga send --image` (design send --image): a target whose process
// record does not declare the images feature gets nothing at all, a capable one
// receives the picture's reference while the file lands in its own session
// directory, and a model that cannot see pictures answers with a warning that
// still counts as a delivered message.

// sendImageHarness serves one live process that takes pictures: every request
// arrives on images, and its ack carries warning.
type sendImageHarness struct {
	session string
	images  chan []process.ImageRef
	warning string
}

func newSendImageHarness(t *testing.T, sessionID string, features []string, warning string) *sendImageHarness {
	t.Helper()
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	h := &sendImageHarness{session: sessionID, images: make(chan []process.ImageRef, 8), warning: warning}
	server, err := process.StartWaitImages(filepath.Join(home, "run"), sessionID, "img", t.TempDir(), features,
		func(mode, text, from, fromName string, hops int, refs []process.ImageRef) (process.Delivery, <-chan error, error) {
			h.images <- refs
			return process.Delivery{Queued: true, Warning: h.warning}, nil, nil
		})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	return h
}

// run executes `suga send --session <session> <args...>` and returns stderr.
func (h *sendImageHarness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmdArgs := append([]string{"send", "--session", h.session}, args...)
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(cmdArgs)
	return stderr.String(), err
}

// imagesDir is the images directory of the target session as the sender resolves
// it ($SUGA_HOME/sessions/<sid>/images).
func (h *sendImageHarness) imagesDir(t *testing.T) string {
	t.Helper()
	sessionsDir, err := appconfig.ResolveSessionsDir()
	assert.Require(t, assert.NoErr(t, err))
	return store.SessionImagesDir(sessionsDir, h.session)
}

func TestSendImageRequiresTheFeature(t *testing.T) {
	h := newSendImageHarness(t, "session-noimg", nil, "")
	picture := writeRunPNG(t, t.TempDir(), "one.png", 10)

	stderr, err := h.run(t, "--image", picture, "看看这张")
	assert.Require(t, assert.Err(t, err))
	var exit processExitError
	assert.True(t, errors.As(err, &exit))
	assert.Eq(t, 5, exit.code)
	assert.Contains(t, err.Error(), "session-noimg")
	assert.Contains(t, err.Error(), "重新打开该会话")
	assert.Eq(t, "", stderr, "nothing is printed as delivered")
	assert.Eq(t, 0, len(h.images), "the text of an images request is not delivered")
	_, err = os.Stat(h.imagesDir(t))
	assert.True(t, os.IsNotExist(err), "nothing was written into the target session")
}

// chdirTo makes dir the sender's working directory, so a relative --image path
// resolves the way a person typing it in that directory expects.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, os.Chdir(dir)))
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

func TestSendImageWritesTheTargetSessionAndSendsTheReference(t *testing.T) {
	h := newSendImageHarness(t, "session-img", []string{process.FeatureImages}, "")
	dir := t.TempDir()
	writeRunPNG(t, dir, "one.png", 10)
	chdirTo(t, dir)
	data, err := os.ReadFile(filepath.Join(dir, "one.png"))
	assert.Require(t, assert.NoErr(t, err))

	stderr, err := h.run(t, "--image", "one.png", "看看这张")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "queued\n", stderr)

	var refs []process.ImageRef
	select {
	case refs = <-h.images:
	default:
		t.Fatal("the pictures were not delivered")
	}
	assert.Require(t, assert.Eq(t, 1, len(refs)))
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	assert.Eq(t, "images/"+sha+".png", refs[0].Ref)
	assert.Eq(t, sha, refs[0].SHA256)
	assert.Eq(t, "image/png", refs[0].MediaType)
	assert.Eq(t, 4, refs[0].Width)
	assert.Eq(t, 2, refs[0].Height)

	stored, err := os.ReadFile(filepath.Join(h.imagesDir(t), filepath.Base(refs[0].Ref)))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, true, bytes.Equal(data, stored), "the stored file is the picture")
}

func TestSendImageStoresOneFilePerContent(t *testing.T) {
	h := newSendImageHarness(t, "session-dedupe", []string{process.FeatureImages}, "")
	dir := t.TempDir()
	writeRunPNG(t, dir, "same.png", 10)
	chdirTo(t, dir)

	for i := 0; i < 2; i++ {
		_, err := h.run(t, "--image", "same.png", "看看这张")
		assert.Require(t, assert.NoErr(t, err))
	}
	first := <-h.images
	second := <-h.images
	assert.Eq(t, first, second, "the same picture keeps its reference")
	entries, err := os.ReadDir(h.imagesDir(t))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, len(entries), "the same sha256 is written once")
}

func TestSendImageWarningKeepsTheExitCode(t *testing.T) {
	h := newSendImageHarness(t, "session-blind", []string{process.FeatureImages}, "当前模型 p/m 不支持看图，附带的 1 张图片已转为文字说明")
	dir := t.TempDir()
	writeRunPNG(t, dir, "one.png", 10)
	chdirTo(t, dir)

	stderr, err := h.run(t, "--image", "one.png", "看看这张")
	assert.Require(t, assert.NoErr(t, err))
	assert.Contains(t, stderr, "不支持看图")
	assert.Contains(t, stderr, "queued")
}

// TestSendWithoutImageDoesNotTouchTheFeature: a plain message neither needs the
// feature nor writes any file.
func TestSendWithoutImageDoesNotTouchTheFeature(t *testing.T) {
	h := newSendImageHarness(t, "session-plain", nil, "")
	stderr, err := h.run(t, "你好")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "queued\n", stderr)
	assert.Eq(t, 0, len(<-h.images), "no pictures ride with a text message")
}
