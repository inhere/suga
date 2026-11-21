package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// newLargePasteCallback keeps ordinary pastes in the editor, but stores a paste
// that crosses either configured threshold in the workspace's ignored tmp dir.
// The returned reference is short enough to edit and tells the model where to
// read the original text when it needs it.
func newLargePasteCallback(workspace string, maxLines, maxChars int) func(string) (string, bool) {
	return func(text string) (string, bool) {
		lines := strings.Count(text, "\n") + 1
		chars := utf8.RuneCountInString(text)
		if lines <= maxLines && chars <= maxChars {
			return "", false
		}

		dir := filepath.Join(workspace, "tmp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Sprintf("[大段粘贴写入临时文件失败：%v]", err), true
		}
		file, err := os.CreateTemp(dir, "suga-paste-*.txt")
		if err != nil {
			return fmt.Sprintf("[大段粘贴写入临时文件失败：%v]", err), true
		}
		name := file.Name()
		cleanup := func(err error) (string, bool) {
			_ = file.Close()
			_ = os.Remove(name)
			return fmt.Sprintf("[大段粘贴写入临时文件失败：%v]", err), true
		}
		if err := file.Chmod(0o600); err != nil {
			return cleanup(err)
		}
		if _, err := file.WriteString(text); err != nil {
			return cleanup(err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(name)
			return fmt.Sprintf("[大段粘贴写入临时文件失败：%v]", err), true
		}
		rel, err := filepath.Rel(workspace, name)
		if err != nil {
			return fmt.Sprintf("[大段粘贴已保存，但无法生成相对路径：%v]", err), true
		}
		rel = filepath.ToSlash(rel)
		return fmt.Sprintf("[大段粘贴已保存到 %s；需要时请用 file_read 读取]", rel), true
	}
}
