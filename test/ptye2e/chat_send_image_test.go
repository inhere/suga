//go:build ptye2e

package ptye2e

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChatSendImage ports scripts/ptysmoke/chat_send_image.py: while an idle chat
// waits at its prompt, `suga send --image shot.png 看看这张` delivers a picture into
// the session. The sender exits 0 and says the message was delivered, the model's
// request carries the text with an image part, and sessions/<sid>/images holds
// exactly one file named after the sha256 of its content (the delivery went
// through the storage layer).
func TestChatSendImage(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Reply: "完成", Delay: 100 * time.Millisecond}
	server := httptest.NewServer(fake)
	defer server.Close()

	home, work := t.TempDir(), t.TempDir()
	makePNG(t, filepath.Join(work, "shot.png"), 6, [3]uint8{200, 60, 30})
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Model: "fake/gpt-4o"})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	var id string
	if m := chatSessionID.FindStringSubmatch(s.Text()); m != nil {
		id = m[1]
	}
	check(t, "session id", id != "")
	if id == "" {
		return
	}

	out, errOut, code := sugaRun(t, binary, work, env, "send", "--session", id, "--image", "shot.png", "看看这张")
	check(t, "the sender exits 0", code == 0)
	sender := out + errOut
	check(t, "the sender says it was delivered", strings.Contains(sender, "delivered") || strings.Contains(sender, "queued"))
	check(t, "no warning for a model that can see", !strings.Contains(sender, "不支持看图"))

	req, ok := waitRequestUser(t, fake, "看看这张")
	check(t, "the model request carries the text with an image part", ok && req.ImageParts >= 1)
	expect(t, s, `▣ 完成`, mark)
	expect(t, s, `tokens · `, mark)

	files := sessionImages(home, id)
	check(t, "the session's images directory holds one picture", len(files) == 1)
	if len(files) == 1 {
		// The store names every file after the sha256 of the bytes it holds (the
		// sender scales the picture first, so this is checked against the stored
		// file, not the source) and may re-encode it (PNG -> JPEG), so only the
		// name before the extension is the content's sha256.
		body, err := os.ReadFile(filepath.Join(home, "sessions", id, "images", files[0]))
		sum := sha256.Sum256(body)
		check(t, "the file is named after its content", err == nil && strings.TrimSuffix(files[0], filepath.Ext(files[0])) == hex.EncodeToString(sum[:]))
	}

	exitAfterTurn(t, s, mark)
	check(t, "trace: the turn is recorded", findEvent(traceEvents(t, binary, work, env, id), "turn", "") != nil)
}
