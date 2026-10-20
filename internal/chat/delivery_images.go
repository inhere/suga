package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Receiving images of a `suga send --image` message (design send --image D1/D3).
//
// The sender writes every picture into this session's own images directory and
// the request names it by reference only, so the receiver checks each file
// before the picture enters the message: it must live below the session's images
// directory and its bytes must still hash to the sha256 the sender named. One
// bad reference refuses the whole message - a turn that silently answers without
// a picture is worse than a delivery the sender can repeat.

// maxDeliveredImageBytes bounds what a verified file may hold; a stored image is
// already limited when written, this catches a corrupt one.
const maxDeliveredImageBytes = 20 << 20

// verifyDeliveryImages turns the references of one delivery into image parts
// that carry no bytes (the request view reads them back from the session
// directory, images.go). It fails with ErrImageRejected for a reference that
// escapes the images directory, a missing file, or a sha256 mismatch.
func verifyDeliveryImages(sessionsDir, sessionID string, refs []personalprocess.ImageRef) ([]agentcore.ImagePart, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if sessionsDir == "" || sessionID == "" {
		return nil, fmt.Errorf("%w: 本会话没有图片目录", personalprocess.ErrImageRejected)
	}
	imagesDir := store.SessionImagesDir(sessionsDir, sessionID)
	parts := make([]agentcore.ImagePart, 0, len(refs))
	for i, ref := range refs {
		path, ok := deliveredImagePath(imagesDir, ref.Ref)
		if !ok {
			return nil, fmt.Errorf("%w: 第 %d 张图片的引用 %q 不在本会话的图片目录里", personalprocess.ErrImageRejected, i+1, ref.Ref)
		}
		data, err := readDeliveredImage(path)
		if err != nil {
			return nil, fmt.Errorf("%w: 第 %d 张图片（%s）读取失败：%v", personalprocess.ErrImageRejected, i+1, ref.Ref, err)
		}
		if ref.SHA256 == "" || imageDigest(data) != ref.SHA256 {
			return nil, fmt.Errorf("%w: 第 %d 张图片（%s）的内容与声明的 sha256 不一致", personalprocess.ErrImageRejected, i+1, ref.Ref)
		}
		mediaType := ref.MediaType
		if mediaType == "" {
			mediaType = store.MediaTypeForRef(ref.Ref)
		}
		parts = append(parts, agentcore.ImagePart{Ref: ref.Ref, SHA256: ref.SHA256, MediaType: mediaType,
			Width: ref.Width, Height: ref.Height})
	}
	return parts, nil
}

// deliveredImagePath resolves a reference below the session's images directory;
// an absolute path, a ".." or anything outside images/ is refused.
func deliveredImagePath(imagesDir, ref string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(ref)))
	if ref == "" || filepath.IsAbs(ref) || !strings.HasPrefix(clean, store.ImageDirName+"/") {
		return "", false
	}
	return filepath.Join(imagesDir, filepath.FromSlash(filepath.Base(clean))), true
}

// readDeliveredImage reads one file, bounded by maxDeliveredImageBytes so a
// corrupt file cannot be read whole.
func readDeliveredImage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDeliveredImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDeliveredImageBytes {
		return nil, fmt.Errorf("图片文件超过 %d 字节", maxDeliveredImageBytes)
	}
	return data, nil
}

// imageDigest is the sha256 hex of one picture (the name the store gives it).
func imageDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
