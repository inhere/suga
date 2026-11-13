//go:build ptye2e

package ptye2e

import (
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The picture fixtures of the image scenarios: a compact opaque PNG of px×px
// (make_png in chat_images.py), written into the scenario's workspace or read by
// the fake clipboard hook.
func makePNG(t *testing.T, path string, px int, rgb [3]uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, px, px))
	for x := 0; x < px; x++ {
		for y := 0; y < px; y++ {
			img.Set(x, y, color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// sessionImages lists the picture files one session stored under
// sessions/<sid>/images (nil when the directory does not exist).
func sessionImages(home, sid string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "sessions", sid, "images"))
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	return files
}

// waitRequestUser waits for a request whose last user message text (or raw
// content, for a message that carries pictures as a list) contains want.
func waitRequestUser(t *testing.T, f *fakeLLM, want string) (fakeRequest, bool) {
	t.Helper()
	return waitRequest(f, 0, func(r fakeRequest) bool {
		return strings.Contains(r.User, want) || strings.Contains(r.UserRaw, want)
	}, 20*time.Second)
}

// TestChatImages ports scripts/ptysmoke/chat_images.py: Alt+V and `:image` are
// refused with a `:model` hint on a model the vision list does not know, and on
// a model that can see both attach paths fill one queue, `:image`/`:image clear`
// answer, the pictures ride the message to the model as image parts, and the
// store keeps them under sessions/<sid>/images.
func TestChatImages(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Reply: "完成", Delay: 100 * time.Millisecond}
	server := httptest.NewServer(fake)
	defer server.Close()

	work := t.TempDir()
	makePNG(t, filepath.Join(work, "shot.png"), 3, [3]uint8{10, 20, 30})
	clip := filepath.Join(t.TempDir(), "clipboard.png")
	makePNG(t, clip, 5, [3]uint8{200, 150, 100}) // different bytes: a second file
	shot := filepath.Join(work, "shot.png")

	t.Run("vision off", func(t *testing.T) {
		home := t.TempDir()
		s, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL,
			Model: "fake/m1", SetEnv: []string{"SUGA_FAKE_CLIPBOARD_IMAGE=" + clip}})

		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		mark = s.Mark()
		s.Send("\x1bv") // Alt+V as one chunk, like the terminal sends it
		ok := check2(t, "alt+v refused with :model hint", s.Expect(`不支持看图`, mark, 15*time.Second))
		check(t, "alt+v refused with :model hint", ok && s.Expect(`:model`, mark, 15*time.Second))
		before := len(fake.Requests())
		mark = s.Mark()
		s.SendLine(":image " + shot)
		ok = check2(t, ":image refused with :model hint", s.Expect(`不支持看图`, mark, 15*time.Second))
		check(t, ":image refused with :model hint", ok && s.Expect(`:model`, mark, 15*time.Second))
		check(t, "refusals started no turn", len(fake.Requests()) == before)

		id := ctrlCTwice(t, s)
		check(t, "session id", id != "")
		check(t, "vision off: no picture reached the store", len(sessionImages(home, id)) == 0)
	})

	t.Run("vision on", func(t *testing.T) {
		home := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL,
			Model: "fake/gpt-4o", SetEnv: []string{"SUGA_FAKE_CLIPBOARD_IMAGE=" + clip}})

		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		before := len(fake.Requests())
		mark = s.Mark()
		s.Send("\x1bv")
		expect(t, s, `已附图 1：`, mark)
		check(t, "alt+v started no turn", len(fake.Requests()) == before)
		mark = s.Mark()
		s.SendLine(":image " + shot)
		expect(t, s, `已附图 2：`, mark)
		// The queue answers :image (the list) and :image clear.
		mark = s.Mark()
		s.SendLine(":image")
		ok := check2(t, "the list shows both", s.Expect(`已附图 1：`, mark, 15*time.Second))
		check(t, "the list shows both", ok && s.Expect(`已附图 2：`, mark, 15*time.Second))
		mark = s.Mark()
		s.SendLine(":image clear")
		expect(t, s, `已清空待发图片`, mark)
		// Attach again (the new Alt+V) and ride it into the turn.
		mark = s.Mark()
		s.Send("\x1bv")
		expect(t, s, `已附图 1：`, mark)
		mark = s.Mark()
		s.SendLine(":image " + shot)
		expect(t, s, `已附图 2：`, mark)
		mark = s.Mark()
		s.SendLine("看图说说这两张")
		expect(t, s, `▣ 完成`, mark)
		expect(t, s, `tokens · `, mark)

		req, ok := waitRequestUser(t, fake, "看图说说")
		check(t, "the model received the message text", ok)
		// The pictures ride in the same user message as image parts (OpenAI chat
		// shape: content is a list holding image_url parts).
		check(t, "the model request carries both pictures as image parts", ok && req.ImageParts >= 2)

		id := exitAfterTurn(t, s, mark)
		check(t, "session id", id != "")
		// The store kept the pictures: the clipboard one and the :image one are
		// different bytes, so exactly two files (same-bytes attaches dedup).
		check(t, "vision on: both pictures in sessions/<sid>/images", len(sessionImages(home, id)) == 2)
		check(t, "trace: the turn is recorded", len(traceEvents(t, binary, work, env, id)) > 0)
	})
}

// TestChatReadImage ports scripts/ptysmoke/chat_read_image.py: file_read of a
// picture. With vision the tool line names the picture, the tool text says it is
// attached as an image part, the trace counts the picture and its bytes and the
// store keeps it; without vision the tool text is the note that names :model and
// nothing is stored.
func TestChatReadImage(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "image", ReadPath: "shot.png", Reply: "看图完成", Delay: 100 * time.Millisecond}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	makePNG(t, filepath.Join(work, "shot.png"), 4, [3]uint8{30, 120, 200})

	run := func(t *testing.T, model, want string) (id string, env []string, from int) {
		t.Helper()
		home := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Model: model})
		from = len(fake.Requests())
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		mark = s.Mark()
		s.SendLine("读一下这张图说说是什么")
		expect(t, s, `▸ file_read shot\.png`, mark)
		expect(t, s, `▣ `+want, mark)
		expect(t, s, `tokens · `, mark)
		id = exitAfterTurn(t, s, mark)
		check(t, "session id", id != "")
		check(t, "the model received the message text", waitUser(t, fake, from, "读一下这张图"))
		return id, env, from
	}

	t.Run("vision on", func(t *testing.T) {
		id, env, from := run(t, "fake/gpt-4o", "看图完成")
		if id == "" {
			return
		}
		check(t, "vision on: the tool text says the picture is attached", toolTexts(fake, from, "attached as an image part"))
		check(t, "vision on: the picture is stored in sessions/<sid>/images", len(sessionImages(homeOf(env), id)) == 1)
		read := fileReadEvents(t, binary, work, env, id)
		check(t, "vision on: trace has a file_read event", len(read) >= 1)
		if len(read) == 0 {
			return
		}
		last := attrs(read[len(read)-1])
		images, _ := last["images"].(float64)
		imageBytes, _ := last["image_bytes"].(float64)
		check(t, "vision on: the event counts the picture", int(images) == 1)
		check(t, "vision on: the event counts its bytes", imageBytes > 0)
	})

	t.Run("vision off", func(t *testing.T) {
		id, env, from := run(t, "fake/m1", "看图完成")
		if id == "" {
			return
		}
		check(t, "vision off: the tool text is the note", toolTexts(fake, from, "cannot see images"))
		check(t, "vision off: the note names the switch", toolTexts(fake, from, ":model"))
		check(t, "vision off: no picture is stored", len(sessionImages(homeOf(env), id)) == 0)
		read := fileReadEvents(t, binary, work, env, id)
		check(t, "vision off: trace has the file_read event", len(read) >= 1)
		if len(read) == 0 {
			return
		}
		check(t, "vision off: the event has no picture", attrs(read[len(read)-1])["images"] == nil)
	})
}

// waitUser reports whether a request from index from on whose last user message
// contains want arrived (the scenario's turn reached the model).
func waitUser(t *testing.T, f *fakeLLM, from int, want string) bool {
	t.Helper()
	_, ok := waitRequest(f, from, func(r fakeRequest) bool { return strings.Contains(r.User, want) }, 20*time.Second)
	return ok
}

// toolTexts reports whether any request from index from on has a last tool
// result containing want.
func toolTexts(f *fakeLLM, from int, want string) bool {
	_, ok := waitRequest(f, from, func(r fakeRequest) bool {
		return strings.Contains(r.ToolTextFull, want)
	}, 5*time.Second)
	return ok
}

// fileReadEvents lists the trace's file_read tool events.
func fileReadEvents(t *testing.T, binary, work string, env []string, id string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, e := range traceEvents(t, binary, work, env, id) {
		if e["kind"] == "tool" && e["name"] == "file_read" {
			events = append(events, e)
		}
	}
	return events
}

// homeOf reads SUGA_HOME out of a child environment.
func homeOf(env []string) string {
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "SUGA_HOME" {
			return value
		}
	}
	return ""
}
