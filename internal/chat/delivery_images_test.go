package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Tests for the receiving side of `suga send --image` (design send --image): the
// references of a delivery are checked against the session's images directory
// before anything is queued, the pictures ride with the right user message (the
// next turn of a follow-up delivery, the running turn of a steer), and a model
// without vision accepts them with a warning for the sender.

// deliveryImageFile writes one stored picture below the sessions directory and
// returns the reference the sender would name.
func deliveryImageFile(t *testing.T, sessionsDir, sessionID string, data []byte) personalprocess.ImageRef {
	t.Helper()
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	ref := store.ImageRef(sha, "image/png")
	dir := store.SessionImagesDir(sessionsDir, sessionID)
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, filepath.Base(ref)), data, 0o600)))
	return personalprocess.ImageRef{Ref: ref, SHA256: sha, MediaType: "image/png", Width: 2, Height: 2}
}

func TestVerifyDeliveryImages(t *testing.T) {
	const sessionID = "session-images"
	data := []byte("picture bytes")

	t.Run("a matching file becomes a reference-only part", func(t *testing.T) {
		sessionsDir := t.TempDir()
		ref := deliveryImageFile(t, sessionsDir, sessionID, data)
		parts, err := verifyDeliveryImages(sessionsDir, sessionID, []personalprocess.ImageRef{ref})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(parts)))
		assert.Eq(t, ref.Ref, parts[0].Ref)
		assert.Eq(t, ref.SHA256, parts[0].SHA256)
		assert.Eq(t, 2, parts[0].Width)
		assert.Eq(t, 0, len(parts[0].Data), "the bytes stay on disk")
	})

	t.Run("a sha256 that does not match the file is rejected", func(t *testing.T) {
		sessionsDir := t.TempDir()
		ref := deliveryImageFile(t, sessionsDir, sessionID, data)
		// The file is replaced after the sender hashed it: the reference lies.
		dir := store.SessionImagesDir(sessionsDir, sessionID)
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, filepath.Base(ref.Ref)), []byte("something else"), 0o600)))
		_, err := verifyDeliveryImages(sessionsDir, sessionID, []personalprocess.ImageRef{ref})
		assert.Require(t, assert.Err(t, err))
		assert.ErrIs(t, err, personalprocess.ErrImageRejected)
	})

	t.Run("a missing file is rejected", func(t *testing.T) {
		sessionsDir := t.TempDir()
		ref := deliveryImageFile(t, sessionsDir, sessionID, data)
		dir := store.SessionImagesDir(sessionsDir, sessionID)
		assert.Require(t, assert.NoErr(t, os.Remove(filepath.Join(dir, filepath.Base(ref.Ref)))))
		_, err := verifyDeliveryImages(sessionsDir, sessionID, []personalprocess.ImageRef{ref})
		assert.ErrIs(t, err, personalprocess.ErrImageRejected)
	})

	t.Run("a reference outside the images directory is rejected", func(t *testing.T) {
		sessionsDir := t.TempDir()
		ref := deliveryImageFile(t, sessionsDir, sessionID, data)
		ref.Ref = "../" + ref.Ref
		_, err := verifyDeliveryImages(sessionsDir, sessionID, []personalprocess.ImageRef{ref})
		assert.ErrIs(t, err, personalprocess.ErrImageRejected)
	})

	t.Run("no reference is no image", func(t *testing.T) {
		parts, err := verifyDeliveryImages(t.TempDir(), sessionID, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Nil(t, parts)
	})
}

// newSendImageHarness is newSendSteerHarness with a sessions directory (where the
// delivered pictures live) and an optional vision gate, so the delivery endpoint
// of the session socket sees what a real chat sees.
func newSendImageHarness(t *testing.T, provider heldProvider, sessionsDir string, vision bool) *steerHarness {
	t.Helper()
	h := newAskHarness(t)
	h.source.SetSteering(true)
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	registry.Register(workStub{})
	runner, err := personal.New(personal.Config{Repository: repo, Provider: provider, Tools: registry, SessionsDir: sessionsDir})
	assert.Require(t, assert.NoErr(t, err))
	model, err := runner.NewSession(context.Background(), "", "send image test")
	assert.Require(t, assert.NoErr(t, err))
	render := newRenderer(rendererOptions{Out: h.out, Err: h.out})
	state := &chatState{
		ctx: context.Background(), runner: runner, source: h.source, render: render,
		stderr: h.out, sessionID: model.ID, stats: &chatStats{},
		idle:     newIdleConsolidation(runner, model.ID, 0, 0, render, nil, nil),
		steering: true, vision: tools.NewVisionGate(vision),
	}
	t.Cleanup(func() { state.idle.Close() })
	return &steerHarness{askHarness: h, state: state, cast: provider, repo: repo, sessID: model.ID}
}

// TestDeliverMessageImagesFollowUp: the pictures of a follow-up delivery are
// queued with the message and reach the turn it starts as parts that name the
// stored file only.
func TestDeliverMessageImagesFollowUp(t *testing.T) {
	sessionsDir := t.TempDir()
	prov := newToolGateProv()
	h := newSendImageHarness(t, prov, sessionsDir, true)
	ref := deliveryImageFile(t, sessionsDir, h.sessID, []byte("picture bytes"))

	delivery, _, err := h.state.deliverMessage(personalprocess.ModeFollowUp, "看看这张", "cli", "", 0,
		[]personalprocess.ImageRef{ref})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "", delivery.Warning)

	var event chatInputEvent
	select {
	case event = <-h.source.deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery was not queued")
	}
	assert.Require(t, assert.Eq(t, 1, len(event.images)))
	assert.Eq(t, ref.Ref, event.images[0].Ref)

	done := make(chan error, 1)
	go func() { done <- h.state.runTurn(event.line, event.injected, event.images) }()
	<-prov.started()
	assert.Eq(t, []int{1}, prov.imageCounts(), "the turn's user message carries the picture")
	prov.letGo()
	select {
	case err := <-done:
		assert.NoErr(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not end")
	}
}

// TestDeliverMessageImagesSteer: a steered delivery puts its pictures into the
// turn running right now: the turn's next model request carries them.
func TestDeliverMessageImagesSteer(t *testing.T) {
	sessionsDir := t.TempDir()
	prov := newToolGateProv()
	h := newSendImageHarness(t, prov, sessionsDir, true)
	ref := deliveryImageFile(t, sessionsDir, h.sessID, []byte("picture bytes"))

	done := h.startTurn()
	<-prov.started()

	delivery, _, err := h.state.deliverMessage(personalprocess.ModeSteer, "看看这张", "cli", "", 0,
		[]personalprocess.ImageRef{ref})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, delivery.Steered)
	prov.letGo()
	select {
	case err := <-done:
		assert.NoErr(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not end")
	}
	assert.Eq(t, []int{0, 1}, prov.imageCounts(), "the second request carries the steered picture")
}

// TestDeliverMessageImagesWithoutVisionWarns: a model that cannot see still takes
// the message and its pictures (the provider layer turns them into notes), and the
// sender is told about it through the delivery's warning.
func TestDeliverMessageImagesWithoutVisionWarns(t *testing.T) {
	sessionsDir := t.TempDir()
	h := newSendImageHarness(t, newToolGateProv(), sessionsDir, false)
	ref := deliveryImageFile(t, sessionsDir, h.sessID, []byte("picture bytes"))

	delivery, _, err := h.state.deliverMessage(personalprocess.ModeFollowUp, "看看这张", "cli", "", 0,
		[]personalprocess.ImageRef{ref})
	assert.Require(t, assert.NoErr(t, err))
	assert.NotEmpty(t, delivery.Warning)
	assert.Contains(t, delivery.Warning, "不支持看图")
	assert.NotContains(t, delivery.Warning, "\x00")

	select {
	case event := <-h.source.deliveries:
		assert.Eq(t, 1, len(event.images))
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery was not queued")
	}
}

// TestDeliverMessageImagesRejected: one bad reference refuses the whole message:
// nothing is queued and the sender learns that its images were refused.
func TestDeliverMessageImagesRejected(t *testing.T) {
	sessionsDir := t.TempDir()
	h := newSendImageHarness(t, newToolGateProv(), sessionsDir, true)
	ref := deliveryImageFile(t, sessionsDir, h.sessID, []byte("picture bytes"))
	ref.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	_, _, err := h.state.deliverMessage(personalprocess.ModeFollowUp, "看看这张", "cli", "", 0,
		[]personalprocess.ImageRef{ref})
	assert.Require(t, assert.Err(t, err))
	assert.ErrIs(t, err, personalprocess.ErrImageRejected)
	assert.Eq(t, 0, len(h.source.deliveries), "nothing was queued")
}
