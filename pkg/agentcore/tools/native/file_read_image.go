package native

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// file_read reads pictures too (design 2026-10-23-file-read-images): the same
// pipeline view_image uses - sniffImage, prepareImage and its constants - is
// reached through the FileReadTool.image reader, so there is one implementation
// only. A picture is recognized by its file header before the NUL byte sniff of
// the text path, because every picture holds NUL bytes and would otherwise be
// answered as a binary file.

const (
	// imageHeadBytes is how much of a file is peeked to identify a picture by its
	// header: the longest magic number is 12 bytes (WebP), so 512 leaves room for
	// the dimensions a note names.
	imageHeadBytes = 512
	// imageExtHintMaxBytes is the largest file that gets the "the header is not a
	// supported image format" hint: a bigger file is not one anyone truncated.
	imageExtHintMaxBytes = maxImageInputBytes
)

// imageExts are the extensions that name a picture format. They never decide
// that a file is a picture (the header does); they only exempt a call from
// prefetching (SkipPrefetch) and add the truncated-picture hint.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// SetImagePipeline binds file_read to the picture pipeline it shares with
// view_image: the same instance, so the runtime's live vision gate (:model) and
// the session images directory resolver (:resume, :new) apply to both.
func (t *FileReadTool) SetImagePipeline(img *ViewImageTool) *FileReadTool {
	t.image = img
	return t
}

// imagePipeline returns the picture reader: the bound view_image instance, or a
// default one built from this tool's own settings (a host that binds nothing
// keeps the "can see" default of VisionGate).
func (t *FileReadTool) imagePipeline() *ViewImageTool {
	if t.image != nil {
		return t.image
	}
	return NewViewImageTool(t.workspace, t.readRoots, t.addDirs, "")
}

// imageProbe is what the header of one path argument says before it is read: a
// supported picture (image), a file whose extension names one anyway (looksLike,
// the truncated-download hint) and its size in bytes (0 when unknown, e.g. a
// session image ref).
type imageProbe struct {
	image     bool
	looksLike bool
	format    string // PNG, JPEG, GIF, WebP; empty when not a picture
	size      int64
}

// probeImageItem looks at the first bytes of one entry. A path that cannot be
// opened, is not an ordinary file or is a directory is reported as "not a
// picture" without an error: the ordinary read answers it exactly as before.
func (t *FileReadTool) probeImageItem(arg string) imageProbe {
	if isSessionImageRef(arg) {
		// A stored session image is always a picture: the ref rule decides, the
		// reader's sniff then accepts or refuses its bytes.
		return imageProbe{image: true, looksLike: true}
	}
	probe := imageProbe{looksLike: imageExts[strings.ToLower(filepath.Ext(arg))]}
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), arg)
	if err != nil {
		return probe
	}
	// The head is read through a separate handle: the ordinary read opens the file
	// again and must see it from its start.
	_, _ = withRoot(target.base, target.abs, func(root *os.Root, rel string) (struct{}, error) {
		f, err := root.Open(rel)
		if err != nil {
			return struct{}{}, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return struct{}{}, err
		}
		probe.size = info.Size()
		head := make([]byte, imageHeadBytes)
		n, _ := io.ReadFull(f, head)
		if _, format, err := sniffImage(head[:n]); err == nil {
			probe.image, probe.format = true, format
		}
		return struct{}{}, nil
	})
	return probe
}

// readEntry answers one path entry: a picture goes through the picture branch,
// a file whose name looks like a picture but whose header is not one is read as
// always with a first-line hint, and every other file is read exactly as before
// (byte for byte: the probe only looks, the ordinary read still answers).
func (t *FileReadTool) readEntry(item readItem, offset, limit int, outline, single bool) (string, *api.ImagePart, error) {
	probe := t.probeImageItem(item.path)
	if probe.image {
		// One call attaches at most one picture: a line span, outline=true or a
		// paths entry is answered with a note instead (design section 4).
		return t.readPicture(item, probe, offset, limit, outline, single && len(item.ranges) == 0)
	}
	text, err := t.readItem(item, offset, limit, outline)
	if err == nil && probe.looksLike && probe.size > 0 && probe.size <= imageExtHintMaxBytes {
		// A truncated download or a mislabelled text file: the name promises a
		// picture the header does not back (design section 1, step 3).
		text = fmt.Sprintf("[%s: the file header is not a supported image format (PNG, JPEG, GIF, WebP)]\n%s",
			item.path, text)
	}
	return text, nil, err
}

// readPicture answers one picture entry: its image part and text when the call
// can carry one and the current model can see, else a plain text note (never an
// error - a picture a text model cannot use is a fact to report, not a failure).
func (t *FileReadTool) readPicture(item readItem, probe imageProbe, offset, limit int, outline, attach bool) (string, *api.ImagePart, error) {
	reader := t.imagePipeline()
	if !attach {
		data, _, err := reader.read(item.path)
		if err != nil {
			if isSessionImageRef(item.path) {
				// Same as a single-path ref: a missing session directory is a note.
				return fmt.Sprintf("[image: %s - not attached: %s]", item.arg, err), nil, nil
			}
			return "", nil, err
		}
		format, size := imageSummary(data)
		return fmt.Sprintf("[image: %s, %s%s - not attached: read a picture with a single path call]",
			item.arg, format, size), nil, nil
	}
	if probe.size > maxImageInputBytes {
		// Only the header was read: the note names the format without decoding a
		// file nobody is going to send.
		return fmt.Sprintf("%s: %s, %s - not attached: over the %d MB input limit",
			item.arg, probe.format, byteCount(probe.size), maxImageInputBytes>>20), nil, nil
	}
	data, name, err := reader.read(item.path)
	if err != nil {
		if isSessionImageRef(item.path) {
			// A ref without a session images directory (or a file stored and removed)
			// is a note: the model cannot fix it, and it is not a read failure.
			return fmt.Sprintf("%s: not attached: %s", item.arg, err), nil, nil
		}
		return "", nil, err
	}
	if !reader.vision.CanSee() {
		// A model without vision still learns what the file is (header only) and how
		// to read it for real (design section 3).
		format, size := imageSummary(data)
		return fmt.Sprintf("%s: %s%s. Not attached: the current model cannot see images.\n"+
			"Switch to a model with vision (chat: :model) and read the picture again.", name, format, size), nil, nil
	}
	part, text, err := prepareImage(data, name, reader.defaultMaxSide())
	if err != nil {
		// A corrupt, oversized or unsupported picture is a note like file_read's
		// other "this is what the file is" answers, not a failed call.
		format, size := imageSummary(data)
		return fmt.Sprintf("%s: %s%s - not attached: %s", name, format, size, err), nil, nil
	}
	text += "; the picture is attached as an image part"
	if notes := pictureArgNotes(offset, limit, outline); len(notes) > 0 {
		text += " (" + strings.Join(notes, "; ") + ")"
	}
	return text, &part, nil
}

// pictureArgNotes names the text-only arguments a picture call carried and could
// not honour: they are ignored, not refused (design section 4).
func pictureArgNotes(offset, limit int, outline bool) []string {
	var notes []string
	if offset != 1 || limit != defaultReadLines {
		notes = append(notes, "offset/limit do not apply to a picture")
	}
	if outline {
		notes = append(notes, "outline does not apply to a picture")
	}
	return notes
}

// imageSummary describes a picture from its header alone - format, pixel size
// and byte size - so a note about a picture that is not attached still says what
// it is.
func imageSummary(data []byte) (format, size string) {
	_, format, err := sniffImage(data)
	if err != nil {
		return "unknown format", ""
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return format, ""
	}
	return format, fmt.Sprintf(" %d×%d, %s", config.Width, config.Height, byteCount(int64(len(data))))
}

// byteCount spells a byte count the way tool answers do (2.1 MB, 812 kB).
func byteCount(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
