package native

import (
	"fmt"
	"runtime"
	"strings"
)

// pathCharsStrict reports whether this platform refuses characters, trailing
// dots and trailing spaces in a file name. Windows does; the POSIX platforms
// allow them, so a path is only checked there.
func pathCharsStrict() bool { return runtime.GOOS == "windows" }

// windowsInvalidNameChars are the characters Windows refuses inside a name.
const windowsInvalidNameChars = `<>:"|?*`

// checkPathChars returns an actionable error for a path this platform cannot
// open, instead of leaving it to the OS.
//
// The reason for the pre-check (session sg_31nv13n28jb8): a model appended its
// own commentary to a path ("login_test.go; why? Hmm tool sees weird.") and,
// because the OS only answered "The filename, directory name, or volume label
// syntax is incorrect", it retried 16 times with a new suffix each time. Naming
// the character and pointing at file_list / file_find stops that loop.
func checkPathChars(path string) error {
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("path 含 NUL 字符，无法作为文件名：%q；请用 file_list / file_find 返回的路径", path)
	}
	for _, r := range path {
		if r < 0x20 {
			return fmt.Errorf("path 含控制字符 %q，无法作为文件名：%q；请用 file_list / file_find 返回的路径", r, path)
		}
	}
	if !pathCharsStrict() {
		return nil
	}
	body := path
	// A volume prefix ("C:") carries the one legal colon.
	if len(body) >= 2 && body[1] == ':' && isASCIILetter(body[0]) {
		body = body[2:]
	}
	if i := strings.IndexAny(body, windowsInvalidNameChars); i >= 0 {
		ch := string(body[i])
		hint := ""
		if ch == "*" || ch == "?" {
			hint = "；`*` / `?` 是通配符：找文件用 file_find，file_read 只接受确切路径"
		}
		return fmt.Errorf("path 含 Windows 不允许的字符 %q（第 %d 个字符起）%s：%q；请原样使用 file_list / file_find 返回的路径",
			ch, i+1, hint, path)
	}
	for _, segment := range strings.Split(strings.ReplaceAll(path, "\\", "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return fmt.Errorf("path 的 %q 以 %q 结尾（Windows 会静默去掉它，看着像不存在或另一个文件）：%q；请用 file_list 显示的准确名字",
				segment, segment[len(segment)-1:], path)
		}
	}
	return nil
}

func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
