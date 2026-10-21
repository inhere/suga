package native

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	idevStdReleasesRootEnv    = "IDEV_STD_RELEASES_ROOT"
	inhereDevStandardsRootEnv = "INHERE_DEV_STANDARDS_ROOT"
	standardsWorkspacePin     = "standards.json"
)

// StandardsRoots detects the idev-std release and workspace loading roots. The
// roots are read-only: file tools may read them, while writes and shell commands
// continue to use the workspace.
func StandardsRoots(workspace string) []ReadRoot {
	var roots []ReadRoot
	for _, envName := range []string{idevStdReleasesRootEnv, inhereDevStandardsRootEnv} {
		if dir := os.Getenv(envName); dir != "" {
			if root, ok := existingReadRoot("idev-std releases", dir); ok {
				roots = append(roots, root)
			}
		}
	}
	if root := standardsWorkspaceRoot(workspace); root != "" {
		if artifact, ok := existingReadRoot("idev-std loading artifacts", filepath.Join(root, "tmp", "idev-std")); ok {
			roots = append(roots, artifact)
		}
	}
	return DeduplicateReadRoots(roots)
}

// ExistingReadRoot reports the read root for dir (absolute, existing directory
// only), matching the safety boundary used by configured and dependency roots.
func ExistingReadRoot(label, dir string) (ReadRoot, bool) {
	return existingReadRoot(label, dir)
}

// existingReadRoot accepts only absolute, existing directories, matching the
// safety boundary used by configured and dependency roots.
func existingReadRoot(label, dir string) (ReadRoot, bool) {
	if !filepath.IsAbs(dir) {
		return ReadRoot{}, false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ReadRoot{}, false
	}
	return ReadRoot{Label: label, Path: filepath.Clean(dir)}, true
}

// standardsWorkspaceRoot walks upward from the tool workspace. A pinned
// standards.json wins over the fallback artifact marker at any level; the
// fallback is used only when no pin exists anywhere above the workspace.
func standardsWorkspaceRoot(workspace string) string {
	if workspace == "" {
		return ""
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	root = filepath.Clean(root)
	fallback := ""
	for {
		if hasStandardsPin(root) {
			return root
		}
		if fallback == "" && hasLoadingArtifacts(root) {
			fallback = root
		}
		parent := filepath.Dir(root)
		if sameReadRootPath(parent, root) {
			return fallback
		}
		root = parent
	}
}

// hasStandardsPin reports whether root is bound to an idev-std workspace.
func hasStandardsPin(root string) bool {
	info, err := os.Stat(filepath.Join(root, standardsWorkspacePin))
	return err == nil && !info.IsDir()
}

// hasLoadingArtifacts reports the fallback marker used by older workspaces.
func hasLoadingArtifacts(root string) bool {
	info, err := os.Stat(filepath.Join(root, "tmp", "idev-std"))
	return err == nil && info.IsDir()
}

// DeduplicateReadRoots keeps the first label and path for each root. Windows
// path comparison follows the same case-insensitive rule as native path access.
func DeduplicateReadRoots(roots []ReadRoot) []ReadRoot {
	out := make([]ReadRoot, 0, len(roots))
	for _, root := range roots {
		root.Path = filepath.Clean(root.Path)
		duplicate := false
		for _, existing := range out {
			if sameReadRootPath(existing.Path, root.Path) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, root)
		}
	}
	return out
}

// sameReadRootPath compares root paths using the platform's file access rule.
func sameReadRootPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
