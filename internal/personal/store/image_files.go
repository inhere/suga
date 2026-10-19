package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// Image files of a session (image input, design 0.3).
//
// An image never enters the database: api.ImagePart carries the bytes in memory
// only (Data, json:"-") and the message stores a reference instead. The files
// live below <sessions>/<session_id>/images/ and are named after the sha256 of
// the bytes they hold, so storing the same picture twice writes one file.
//
// The write happens here, before EncodeMessage, because every message the
// runtime appends goes through AppendMessages (a user picture as well as a
// view_image result); a save hook in the tools would miss the user side.

// ImageDirName is the directory of a session's images; api.ImagePart.Ref is a
// slash-separated path below it, relative to the session directory
// (images/<sha256>.<ext>).
const ImageDirName = "images"

// imageExtensions maps the media types the image tools accept to the extension
// of the stored file.
var imageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ImageExtension returns the stored-file extension of a media type (".bin" for
// an unknown one, which the image tools never produce).
func ImageExtension(mediaType string) string {
	if ext, ok := imageExtensions[strings.ToLower(mediaType)]; ok {
		return ext
	}
	return ".bin"
}

// MediaTypeForRef returns the media type of a stored image by its file
// extension ("" when the extension is not one of the stored formats).
func MediaTypeForRef(ref string) string {
	ext := strings.ToLower(filepath.Ext(ref))
	for mediaType, known := range imageExtensions {
		if known == ext {
			return mediaType
		}
	}
	return ""
}

// ImageRef returns the reference of an image part: images/<sha256>.<ext>.
func ImageRef(sha, mediaType string) string {
	return ImageDirName + "/" + sha + ImageExtension(mediaType)
}

// SessionImagesDir is the images directory of one session.
func SessionImagesDir(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, sessionID, ImageDirName)
}

// WithSessionsDir makes AppendMessages write image files below
// <dir>/<session_id>/images. Without it images are stored with their reference
// only (the bytes are memory-only anyway, so they are not recoverable).
func (a *AgentStorage) WithSessionsDir(dir string) *AgentStorage {
	a.sessionsDir = dir
	return a
}

// storeMessageImages writes the in-memory images of one message and returns the
// message with references in place of the bytes. Both the message's images and
// its tool result's images are stored (a tool message mirrors them).
func (a *AgentStorage) storeMessageImages(sessionID string, m api.Message) (api.Message, error) {
	images, err := a.storeImages(sessionID, m.Images)
	if err != nil {
		return m, err
	}
	m.Images = images
	if m.ToolResult != nil {
		results, err := a.storeImages(sessionID, m.ToolResult.Images)
		if err != nil {
			return m, err
		}
		if len(results) > 0 {
			result := *m.ToolResult
			result.Images = results
			m.ToolResult = &result
		}
	}
	return m, nil
}

// storeImages writes every part carrying bytes of one image list.
func (a *AgentStorage) storeImages(sessionID string, images []api.ImagePart) ([]api.ImagePart, error) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]api.ImagePart, len(images))
	for i, part := range images {
		stored, err := a.storeImage(sessionID, part)
		if err != nil {
			return nil, err
		}
		out[i] = stored
	}
	return out, nil
}

// storeImage writes one image part: the temporary file is synced and renamed
// into place, and a file with the same sha256 is not written again.
func (a *AgentStorage) storeImage(sessionID string, part api.ImagePart) (api.ImagePart, error) {
	if len(part.Data) == 0 {
		if part.SHA256 == "" && part.Ref != "" {
			// A part that came back from storage: the odd encoding of an empty
			// body is not worth an error, keep what the store holds.
			return part, nil
		}
		return part, nil
	}
	sum := sha256.Sum256(part.Data)
	sha := hex.EncodeToString(sum[:])
	mediaType := part.MediaType
	if mediaType == "" {
		mediaType = MediaTypeForRef(part.Ref)
	}
	part.SHA256 = sha
	part.MediaType = mediaType
	part.Ref = ImageRef(sha, mediaType)
	if a.sessionsDir != "" {
		if err := writeImageFile(SessionImagesDir(a.sessionsDir, sessionID), filepath.Base(part.Ref), part.Data); err != nil {
			return part, err
		}
	}
	// The bytes are the caller's memory only: the stored message keeps the
	// reference (EncodeMessage would drop Data anyway).
	part.Data = nil
	return part, nil
}

// writeImageFile writes data to dir/name through a temporary file in the same
// directory, synced and renamed, so a crash never leaves a truncated image
// behind. An existing file of the same name is left untouched (same sha256).
func writeImageFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat image file: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-image-*")
	if err != nil {
		return fmt.Errorf("create image temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write image file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync image file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close image file: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("rename image file: %w", err)
	}
	return nil
}
