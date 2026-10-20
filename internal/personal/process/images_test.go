package process

import (
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// Tests for the wire shape of `suga send --image` (design send --image): the
// request of a sender without pictures must stay byte-for-byte what older
// receivers already parse, and a receiver only takes the pictures when its
// record declares FeatureImages.

func TestRequestWithoutImagesIsUnchanged(t *testing.T) {
	data, err := json.Marshal(Request{Type: "deliver", SessionID: "s1", Text: "hi", From: "cli", Mode: ModeFollowUp})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, `{"type":"deliver","session_id":"s1","text":"hi","from":"cli","mode":"follow_up"}`, string(data))
}

func TestRequestImagesWireFormat(t *testing.T) {
	data, err := json.Marshal(Request{Text: "看看这张", Images: []ImageRef{{
		Ref: "images/abc.png", SHA256: "abc", MediaType: "image/png", Width: 4, Height: 2,
	}}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, `{"type":"","text":"看看这张","images":[{"ref":"images/abc.png","sha256":"abc","media_type":"image/png","width":4,"height":2}]}`, string(data))
}

func TestStartWaitImagesDeliversImagesAndDecLaresTheFeature(t *testing.T) {
	root := shortProcessRoot(t)
	delivered := make(chan []ImageRef, 1)
	server, err := StartWaitImages(root, "session-img", "img", t.TempDir(), []string{FeatureImages},
		func(mode, text, from, fromName string, hops int, images []ImageRef) (Delivery, <-chan error, error) {
			delivered <- images
			return Delivery{Queued: true, Warning: "模型不支持看图"}, nil, nil
		})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	assert.True(t, server.Record().HasFeature(FeatureImages))
	// The feature is in the record a sender reads, not only in memory.
	records, err := List(root)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(records)))
	assert.True(t, records[0].HasFeature(FeatureImages))

	refs := []ImageRef{{Ref: "images/abc.png", SHA256: "abc", MediaType: "image/png"}}
	response, err := Send(records[0], Request{Text: "看看这张", From: "cli", Images: refs})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "模型不支持看图", response.Warning)
	assert.Eq(t, refs, <-delivered)
}

func TestServerWithoutImagesRefusesAnImageRequest(t *testing.T) {
	root := shortProcessRoot(t)
	delivered := make(chan string, 1)
	server, err := Start(root, "session-plain", "plain", t.TempDir(), func(mode, text, from string) (Delivery, error) {
		delivered <- text
		return Delivery{}, nil
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	assert.False(t, server.Record().HasFeature(FeatureImages))

	_, err = Send(server.Record(), Request{Text: "看看这张", Images: []ImageRef{{Ref: "images/a.png", SHA256: "a"}}})
	assert.ErrIs(t, err, ErrImagesUnsupported)
	assert.Eq(t, 0, len(delivered), "the text of a refused images request is not delivered")

	// A plain message still goes through, and its record has no features field.
	_, err = Send(server.Record(), Request{Text: "你好"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "你好", <-delivered)
}
