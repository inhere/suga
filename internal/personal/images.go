package personal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Images in the request view (image input, design 0.3).
//
// The store keeps only a reference (images/<sha256>.<ext>) of every image; the
// bytes are read back here, while the request is assembled: BuildMessages is
// the only place that knows the session directory and the request budget. The
// rules:
//
//   - the bytes of an image are attached only for the messages newer than the
//     last history fold boundary; older images are replaced by a text note (the
//     boundary moves once per batch, so the request prefix stays cacheable), and
//     the model can re-read one with file_read <ref>;
//   - one request carries at most images.max_request_count images and
//     images.max_request_bytes encoded bytes; the oldest ones are replaced by a
//     note;
//   - an image whose file is missing or does not match its sha256 is replaced
//     by a note instead of failing the turn.
const (
	// maxImageFileBytes bounds what is read back for one image (a stored file is
	// already limited when written; this catches a corrupt one).
	maxImageFileBytes = 20 << 20
)

// imageLoader reads stored images below one session directory, caching by
// sha256 so a picture used in several messages is read once.
type imageLoader struct {
	dir   string // <sessions>/<session_id>
	cache map[string][]byte
}

func newImageLoader(sessionsDir, sessionID string) *imageLoader {
	if sessionsDir == "" || sessionID == "" {
		return nil
	}
	return &imageLoader{dir: filepath.Join(sessionsDir, sessionID), cache: map[string][]byte{}}
}

// data returns the bytes of a part and whether they could be read and verified.
func (l *imageLoader) data(part agentcore.ImagePart) ([]byte, bool) {
	if part.Ref == "" {
		return nil, false
	}
	if data, ok := l.cache[part.SHA256]; ok && len(data) > 0 {
		return data, true
	}
	path, ok := l.path(part.Ref)
	if !ok {
		return nil, false
	}
	data, err := readImageFile(path)
	if err != nil {
		return nil, false
	}
	if part.SHA256 != "" && imageDigest(data) != part.SHA256 {
		return nil, false
	}
	if part.SHA256 != "" {
		l.cache[part.SHA256] = data
	}
	return data, true
}

// path resolves a reference below the images directory; a reference that
// escapes it (absolute, "..", another subdirectory) is refused.
func (l *imageLoader) path(ref string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(ref)))
	if filepath.IsAbs(ref) || !strings.HasPrefix(clean, store.ImageDirName+"/") {
		return "", false
	}
	return filepath.Join(l.dir, filepath.FromSlash(clean)), true
}

// readImageFile reads one stored image, bounded by maxImageFileBytes.
func readImageFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImageFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageFileBytes {
		return nil, fmt.Errorf("image file %s exceeds %d bytes", path, maxImageFileBytes)
	}
	return data, nil
}

func imageDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// imageNote is the text that replaces an image left out of the request.
func imageNote(part agentcore.ImagePart, reason string) string {
	kind := part.MediaType
	if kind == "" {
		kind = "image"
	}
	size := ""
	if part.Width > 0 && part.Height > 0 {
		size = fmt.Sprintf(" %d×%d", part.Width, part.Height)
	}
	ref := part.Ref
	if ref == "" {
		ref = "(unknown)"
	}
	return fmt.Sprintf("[%s (%s%s)：%s；需要时 file_read %s]", kind, filepath.Base(ref), size, reason, ref)
}

// prepareImages returns a copy of view whose images carry their bytes, with the
// history and per-request rules above applied. view and its messages are not
// modified; the caller's messages (and the stored entries) keep their
// references only.
func (b *contextBuilder) prepareImages(view []agentcore.Message) []agentcore.Message {
	if len(view) == 0 || !hasImages(view) {
		return view
	}
	loader := newImageLoader(b.r.cfg.SessionsDir, b.sessionID)
	// The limits come from the images.* section; a runner built without them (a
	// test) keeps the built-in defaults.
	turns, count, bytes := b.r.cfg.ImageHistoryTurns, b.r.cfg.ImageRequestCount, b.r.cfg.ImageRequestBytes
	if turns <= 0 {
		turns = appconfig.DefaultImageHistoryTurns
	}
	if count <= 0 {
		count = appconfig.DefaultImageRequestCount
	}
	if bytes <= 0 {
		bytes = appconfig.DefaultImageRequestBytes
	}
	// Same cadence as the fold boundary: the size-triggered parameters apply
	// once folding is on (foldParams), so pictures are trimmed on the batch that
	// already invalidates the cached prefix.
	every, keep := b.foldParams()
	cut := imageHistoryCut(view, every, keep, turns)
	out := make([]agentcore.Message, len(view))
	for i, m := range view {
		trimmed := i < cut
		out[i] = b.resolveMessageImages(m, loader, trimmed)
	}
	return limitRequestImages(out, count, bytes)
}

// hasImages reports whether any message of msgs carries an image.
func hasImages(msgs []agentcore.Message) bool {
	for _, m := range msgs {
		if len(m.Images) > 0 || (m.ToolResult != nil && len(m.ToolResult.Images) > 0) {
			return true
		}
	}
	return false
}

// imageHistoryCut returns the index of the first message whose images stay in
// the request: the messages from the last fold boundary on, or, with folding
// off, the messages of the last `turns` assistant steps (images.history_turns).
// A message belongs to the step that follows it (a user message asks
// for it, a tool message answers it), so the cut is the first message after
// `boundary` assistant steps. It uses the same step boundary as foldHistory,
// so trimming pictures happens on the batch cadence that already invalidates
// the cached prefix.
func imageHistoryCut(msgs []agentcore.Message, every, keep, turns int) int {
	boundary, steps := foldBoundary(msgs, every, keep)
	if boundary == 0 && every <= 0 {
		// No folding: keep the pictures of the newest `turns` steps.
		boundary = max(steps-turns, 0)
	}
	if boundary == 0 {
		return 0
	}
	seen := 0
	for i, m := range msgs {
		if seen >= boundary {
			return i
		}
		if m.Role == "assistant" {
			seen++
		}
	}
	return len(msgs)
}

// resolveMessageImages copies one message with its image bytes filled; a part
// that cannot be read, or that is trimmed, is dropped and described in the
// message text.
func (b *contextBuilder) resolveMessageImages(m agentcore.Message, loader *imageLoader, trimmed bool) agentcore.Message {
	images, notes := b.resolveImages(m.Images, loader, trimmed)
	m.Images = images
	if m.ToolResult != nil && len(m.ToolResult.Images) > 0 {
		results, resultNotes := b.resolveImages(m.ToolResult.Images, loader, trimmed)
		notes = append(notes, resultNotes...)
		res := *m.ToolResult
		res.Images = results
		if len(resultNotes) > 0 {
			res.Content = appendNote(res.Content, resultNotes)
		}
		m.ToolResult = &res
	}
	if len(notes) > 0 {
		m.Content = appendNote(m.Content, notes)
	}
	return m
}

// resolveImages returns the parts that keep their bytes and the notes of the
// ones that do not.
func (b *contextBuilder) resolveImages(images []agentcore.ImagePart, loader *imageLoader, trimmed bool) ([]agentcore.ImagePart, []string) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]agentcore.ImagePart, 0, len(images))
	var notes []string
	for _, part := range images {
		if trimmed {
			notes = append(notes, imageNote(part, "已省略"))
			continue
		}
		if len(part.Data) > 0 {
			out = append(out, part)
			continue
		}
		if loader == nil {
			notes = append(notes, imageNote(part, "图片不可用"))
			continue
		}
		data, ok := loader.data(part)
		if !ok {
			notes = append(notes, imageNote(part, "图片缺失"))
			continue
		}
		filled := part
		filled.Data = data
		if filled.MediaType == "" {
			filled.MediaType = store.MediaTypeForRef(part.Ref)
		}
		out = append(out, filled)
	}
	return out, notes
}

// appendNote adds the omitted-image notes to a message body on their own lines.
func appendNote(content string, notes []string) string {
	var sb strings.Builder
	sb.WriteString(content)
	for _, note := range notes {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(note)
	}
	return sb.String()
}

// limitRequestImages enforces the per-request caps: scanning from the newest
// message backwards it keeps at most maxCount images and maxBytes of encoded
// bytes, and replaces the rest by a note. The caps are about the cost of one
// request, so the newest pictures (the ones the turn is about) win.
func limitRequestImages(msgs []agentcore.Message, maxCount int, maxBytes int) []agentcore.Message {
	if maxCount <= 0 {
		return msgs
	}
	count, size := 0, 0
	out := make([]agentcore.Message, len(msgs))
	copy(out, msgs)
	keep := func(part agentcore.ImagePart) bool {
		encoded := len(part.Data)
		if count+1 > maxCount || size+encoded > maxBytes {
			return false
		}
		count++
		size += encoded
		return true
	}
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		images, notes := capImages(m.Images, keep)
		m.Images = images
		if m.ToolResult != nil && len(m.ToolResult.Images) > 0 {
			results, resultNotes := capImages(m.ToolResult.Images, keep)
			notes = append(notes, resultNotes...)
			res := *m.ToolResult
			res.Images = results
			if len(resultNotes) > 0 {
				res.Content = appendNote(res.Content, resultNotes)
			}
			m.ToolResult = &res
		}
		if len(notes) > 0 {
			m.Content = appendNote(m.Content, notes)
		}
		out[i] = m
	}
	return out
}

// capImages keeps the parts keep accepts and returns a note for the others.
func capImages(images []agentcore.ImagePart, keep func(agentcore.ImagePart) bool) ([]agentcore.ImagePart, []string) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]agentcore.ImagePart, 0, len(images))
	var notes []string
	for _, part := range images {
		if keep(part) {
			out = append(out, part)
			continue
		}
		notes = append(notes, imageNote(part, "超出单请求图片上限，已省略"))
	}
	return out, notes
}
