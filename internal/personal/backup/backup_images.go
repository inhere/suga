package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
)

// Session images in the backup (image input design 0.3): like the tool body
// files they are ALWAYS included, spill or no spill — the reference in the
// ledger only names the file (images/<sha256>.<ext>), so losing the files means
// losing the picture.
const categorySessionImages = "session_images"

// SessionImagesInfo counts the session image files a backup carries.
type SessionImagesInfo struct {
	FileCount int   `json:"file_count"`
	Bytes     int64 `json:"bytes"`
}

// collectImages adds every sessions/<id>/images/<file> below a sessions root
// to the manifest, each with the category that keeps it out of the spill
// accounting. Installation into the session directory is the same merge the
// existing sessions/ pass runs in replaceData.
func collectImages(m *Manifest, home, root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink %s", root)
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %s", path)
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Only sessions/<id>/images/<file> is a session image; a deeper or
		// differently placed file stays with the spill pass.
		parts := strings.Split(rel, "/")
		if len(parts) != 4 || parts[0] != "sessions" || parts[2] != store.ImageDirName {
			return nil
		}
		before := len(m.Files)
		if err := addHomeFile(m, home, path, rel); err != nil {
			return err
		}
		m.Files[before].Category = categorySessionImages
		m.Images.FileCount++
		m.Images.Bytes += m.Files[before].Size
		return nil
	})
}
