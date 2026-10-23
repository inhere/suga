package native

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Image input, first phase (design 0.3): view_image reads a local picture and
// hands it to the model as an image part of the tool result.
//
// The bytes never stay in the message: the store writes them to the session's
// images directory and keeps a reference (internal/personal). The tool itself is
// read-only and never marked Prefetchable: a prefetch would decode, scale and
// encode a picture a cut-away step never used, all of that wasted (the bytes are
// only written by AppendMessages, so a prefetch leaves no file behind).
// file_read reads pictures through this pipeline too and exempts its picture
// calls from the prefetch for the same reason (tools.PrefetchExempt).
const (
	// DefaultImageMaxSide is the long side an image is scaled down to
	// (images.max_side). 1568 is what both Anthropic and OpenAI recommend as an
	// upper bound for a single picture.
	DefaultImageMaxSide = 1568
	// maxImageInputBytes is the largest source file view_image reads.
	maxImageInputBytes = 20 << 20
	// maxImagePixels bounds the decoded size of a picture (40MP): the header is
	// read before any full decode, so a small file cannot expand into gigabytes.
	maxImagePixels = 40_000_000
	// maxImagePartBytes is the largest encoded image sent to a model (3.5MB, the
	// tighter of the provider limits).
	maxImagePartBytes = 3_500_000
	// imageJPEGQuality is the quality of a re-encoded picture without opacity.
	imageJPEGQuality = 85
)

// ViewImageTool returns a picture as an image part of its tool result, so a
// model that can see gets to look at it.
type ViewImageTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	// imagesDir is the session's images directory; a path argument naming one of
	// its files by ref (images/<sha256>.<ext>, as an omitted image note shows it)
	// is read from there. Empty = only ordinary paths.
	// imagesDirFn is the session resolver set by the host (design 0.3): the
	// images directory belongs to the session being served, which the runtime
	// only knows once it opens, so it is resolved at call time. Non-empty wins
	// over the static imagesDir.
	imagesDir   string
	imagesDirFn func() string
	// vision is the live capability of the current model (:model switching
	// updates it); a model that cannot see is told how to switch instead of
	// receiving bytes nobody reads. nil = can see.
	vision *tools.VisionGate
	// maxSide is the default long side a picture is scaled to (images.max_side);
	// 0 = DefaultImageMaxSide.
	maxSide int
}

// NewViewImageTool returns the tool for a workspace and the session's images
// directory ("" when the session has none).
func NewViewImageTool(workspace string, readRoots []string, addDirs *AddDirs, imagesDir string) *ViewImageTool {
	return &ViewImageTool{workspace: workspace, readRoots: readRoots, addDirs: addDirs, imagesDir: imagesDir}
}

// SetVision binds the tool to the runtime's live vision gate, so the check
// follows a model switch instead of the model the tool was built with.
func (t *ViewImageTool) SetVision(gate *tools.VisionGate) *ViewImageTool {
	t.vision = gate
	return t
}

// SetImagesDirResolver binds a function that resolves the served session's
// images directory at call time, so view_image works across the session a chat
// opens later (:resume, :new). Function may return "" while no session is open.
func (t *ViewImageTool) SetImagesDirResolver(fn func() string) *ViewImageTool {
	t.imagesDirFn = fn
	return t
}

func (t *ViewImageTool) Name() string { return "view_image" }

// defaultMaxSide is the long side a picture is scaled to when the call names
// none: images.max_side, else DefaultImageMaxSide.
func (t *ViewImageTool) defaultMaxSide() int {
	if t.maxSide > 0 {
		return t.maxSide
	}
	return DefaultImageMaxSide
}

func (t *ViewImageTool) Description() string {
	return fmt.Sprintf("Look at a local picture and hand it to you as an image (PNG, JPEG, GIF first frame or "+
		"WebP). The path follows the file tools (workspace, read-only roots, added directories); a session "+
		"image ref such as images/<sha256>.png works too. The picture is scaled down to %d px on its long "+
		"side, so very large screenshots stay cheap. Read text files with file_read.", t.defaultMaxSide())
}

// ToolMeta marks the tool as a read-only read. It is not Prefetchable: its
// bytes would be stored even when the step is cut away (design 0.3). It is
// Hidden: file_read reads pictures now, so view_image stays only as an alias one
// version longer - callable by name for old sessions and name-based
// configuration, never listed (design 2026-10-23 Q4).
func (*ViewImageTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryRead, ReadOnly: true, Hidden: true}
}

func (t *ViewImageTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Picture to look at: a path in the workspace (or a read-only root, added directory) or a session image ref images/<sha256>.<ext>",
				},
				"max_side": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Long side to scale the picture down to (default %d)", t.defaultMaxSide()),
				},
			},
			"required": []any{"path"},
		},
	}
}

func (t *ViewImageTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	arg, _ := call.Arguments["path"].(string)
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("view_image: path is required")
	}
	if !t.vision.CanSee() {
		return nil, fmt.Errorf("current model cannot see images: switch to a model with vision (chat: :model)")
	}
	maxSide := t.defaultMaxSide()
	if n, ok := numberArg(call.Arguments["max_side"]); ok && n > 0 {
		maxSide = int(n)
	}
	data, name, err := t.read(arg)
	if err != nil {
		return nil, fmt.Errorf("view_image: %w", err)
	}
	part, text, err := prepareImage(data, name, maxSide)
	if err != nil {
		return nil, fmt.Errorf("view_image: %w", err)
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: text, Images: []api.ImagePart{part}}, nil
}

// errUnsupportedImage names the formats the tool accepts.
var errUnsupportedImage = fmt.Errorf("unsupported image format (PNG, JPEG, GIF and WebP are supported)")

// read returns the bytes of the path argument and the name to report.
func (t *ViewImageTool) read(arg string) ([]byte, string, error) {
	imagesDir := t.imagesDir
	if t.imagesDirFn != nil {
		// The served session decides: its directory wins over the one built with.
		imagesDir = t.imagesDirFn()
	}
	if isSessionImageRef(arg) {
		if imagesDir == "" {
			return nil, "", fmt.Errorf("session image %s: no session images directory in this process", arg)
		} else if data, err := readImageLimit(joinPath(imagesDir, path.Base(filepathToSlash(arg)))); err != nil {
			return nil, "", fmt.Errorf("session image %s: %w", arg, err)
		} else {
			return data, arg, nil
		}
	}
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), arg)
	if err != nil {
		return nil, "", err
	}
	data, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) ([]byte, error) {
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		// Stat first: only an ordinary file is read, never a device or a pipe.
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", arg)
		}
		if info.Size() > maxImageInputBytes {
			return nil, fmt.Errorf("%s is %d bytes, larger than the %d byte limit", arg, info.Size(), maxImageInputBytes)
		}
		return io.ReadAll(io.LimitReader(f, maxImageInputBytes+1))
	})
	if err != nil {
		return nil, "", err
	}
	return data, arg, nil
}

// isSessionImageRef reports whether arg is a session image reference
// (images/<sha256>.<ext>, the form an omitted-image note prints).
func isSessionImageRef(arg string) bool {
	clean := path.Clean(filepathToSlash(arg))
	if strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return false
	}
	return path.Dir(clean) == "images" && path.Base(clean) != "images"
}

// prepareImage identifies a picture, scales it down to maxSide and encodes it
// for the model. It returns the image part (without a store reference: the host
// writes the bytes) and the text that describes it.
func prepareImage(data []byte, name string, maxSide int) (api.ImagePart, string, error) {
	_, format, err := sniffImage(data)
	if err != nil {
		return api.ImagePart{}, "", err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return api.ImagePart{}, "", fmt.Errorf("decode %s header: %w", format, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return api.ImagePart{}, "", fmt.Errorf("decode %s header: no size", format)
	}
	if config.Width*config.Height > maxImagePixels {
		return api.ImagePart{}, "", fmt.Errorf("%d×%d pixels is over the %d pixel limit", config.Width, config.Height, maxImagePixels)
	}
	img, err := decodeImage(data)
	if err != nil {
		return api.ImagePart{}, "", err
	}
	width, height := config.Width, config.Height
	// A JPEG may store the sensor orientation in EXIF instead of rotating the
	// pixels: turn it upright before scaling, so the model sees it as the
	// person did and the size matches the picture.
	orientation := exifOrientation(data)
	if orientation != 1 {
		img = applyOrientation(img, orientation)
		if orientation >= 5 {
			width, height = height, width
		}
	}
	if maxSide > 0 && (width > maxSide || height > maxSide) {
		img, width, height = scaleImage(img, maxSide)
	}
	encoded, mediaType, err := encodeImage(img)
	if err != nil {
		return api.ImagePart{}, "", err
	}
	if len(encoded) > maxImagePartBytes {
		// One more try at a smaller size before giving up: a huge photo at the
		// default long side can still exceed the single-image budget.
		img, width, height = scaleImage(img, maxSide/2)
		if encoded, mediaType, err = encodeImage(img); err != nil {
			return api.ImagePart{}, "", err
		}
		if len(encoded) > maxImagePartBytes {
			return api.ImagePart{}, "", fmt.Errorf("encoded image is %d bytes, over the %d byte limit", len(encoded), maxImagePartBytes)
		}
	}
	part := api.ImagePart{MediaType: mediaType, Width: width, Height: height, Data: encoded}
	text := fmt.Sprintf("%s: %s %d×%d (sent as %s %d×%d, %d bytes)", name, format,
		config.Width, config.Height, mediaType, width, height, len(encoded))
	if orientation != 1 {
		text += fmt.Sprintf(", EXIF orientation %d applied", orientation)
	}
	return part, text, nil
}

// snackImage identifies a picture by its file header, so a text file named
// .png is refused before it is decoded.
func sniffImage(data []byte) (string, string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "PNG", nil
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", "JPEG", nil
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", "GIF", nil
	case len(data) > 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return "image/webp", "WebP", nil
	}
	return "", "", errUnsupportedImage
}

// decodeImage decodes the first frame of a picture (GIF and WebP included; the
// WebP decoder refuses an animation, which the tool reports as unsupported).
func decodeImage(data []byte) (image.Image, error) {
	switch _, format, err := sniffImage(data); {
	case err != nil:
		return nil, err
	case format == "GIF":
		img, err := gif.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode GIF: %w", err)
		}
		return img, nil
	case format == "WebP":
		img, err := webp.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode WebP: %w", err)
		}
		return img, nil
	default:
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode image: %w", err)
		}
		return img, nil
	}
}

// scaleImage scales img so its long side is side (never up), returning the
// picture and its new size.
func scaleImage(img image.Image, side int) (image.Image, int, int) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if side <= 0 || (width <= side && height <= side) {
		return img, width, height
	}
	if width >= height {
		height = max(height*side/width, 1)
		width = side
	} else {
		width = max(width*side/height, 1)
		height = side
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst, width, height
}

// encodeImage encodes a picture for the request: PNG when it has transparency,
// JPEG (a smaller body) otherwise.
func encodeImage(img image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("encode PNG: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: imageJPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("encode JPEG: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}

// hasAlpha reports whether any pixel of img is not fully opaque.
func hasAlpha(img image.Image) bool {
	if op, ok := img.(interface{ Opaque() bool }); ok {
		return !op.Opaque()
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xFFFF {
				return true
			}
		}
	}
	return false
}

// readImageLimit reads a stored session image, bounded like an input file.
func readImageLimit(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", file)
	}
	if info.Size() > maxImageInputBytes {
		return nil, fmt.Errorf("%s is %d bytes, larger than the %d byte limit", file, info.Size(), maxImageInputBytes)
	}
	return io.ReadAll(io.LimitReader(f, maxImageInputBytes+1))
}

// ReadImageFile loads a picture for a chat-side command (:image, Alt+V paste)
// with exactly the rules view_image applies: the same path resolution
// (workspace, read-only roots, added directories) and input size limits.
func ReadImageFile(workspace string, readRoots []string, addDirs *AddDirs, arg string) ([]byte, string, error) {
	return NewViewImageTool(workspace, readRoots, addDirs, "").read(arg)
}

// PrepareImage identifies and scales a loaded picture the view_image way and
// returns the image part for a message plus its text description. Chat's
// :image and the clipboard paste reuse the tool's pipeline through it.
func PrepareImage(data []byte, name string, maxSide int) (api.ImagePart, string, error) {
	return prepareImage(data, name, maxSide)
}

// filepathToSlash is filepath.ToSlash for the ref form of a path.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// joinPath joins a directory and a file name (path, not filepath: the ref form
// keeps slashes).
func joinPath(dir, name string) string {
	return dir + string(os.PathSeparator) + name
}
