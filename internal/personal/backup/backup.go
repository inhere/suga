// Package backup owns the portable, verified suga backup archive format.
package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

const FormatVersion = 1

const (
	maxArchiveFiles = 100000
	maxArchiveBytes = uint64(8 << 30)
	maxExpandRatio  = uint64(100)
)

type Options struct {
	Home         string
	Output       string
	IncludeSpill bool
	SugaVersion  string
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Source string `json:"-"`
}

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	SugaVersion   string    `json:"suga_version"`
	Files         []File    `json:"files"`
	Spill         SpillInfo `json:"spill"`
}

type SpillInfo struct {
	Policy        string `json:"policy"`
	FileCount     int    `json:"file_count"`
	Bytes         int64  `json:"bytes"`
	OmittedBytes  int64  `json:"omitted_bytes"`
	OmittedReason string `json:"omitted_reason,omitempty"`
}

func Create(ctx context.Context, opts Options) (Manifest, error) {
	home, err := absoluteHome(opts.Home)
	if err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(opts.Output) == "" {
		return Manifest{}, errors.New("backup output is required")
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		return Manifest{}, err
	}

	tmp, err := os.CreateTemp(filepath.Join(home, "run"), "backup-snapshot-*.db")
	if err != nil {
		return Manifest{}, fmt.Errorf("create snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)
	_ = os.Remove(tmpPath)

	dbPath := filepath.Join(home, "db", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return Manifest{}, err
	}
	dbSize := store.DatabaseFileBytes(dbPath)
	free, err := store.FreeDiskSpace(dbPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read free disk space: %w", err)
	}
	if free < uint64(dbSize) {
		return Manifest{}, fmt.Errorf("not enough free disk space for backup snapshot: need about %s, %s available", humanBytes(dbSize), humanBytes(int64(free)))
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return Manifest{}, err
	}
	var snapshotErr error
	for attempt := 0; attempt < 3; attempt++ {
		snapshotErr = repo.Snapshot(ctx, tmpPath)
		if snapshotErr == nil {
			break
		}
		lower := strings.ToLower(snapshotErr.Error())
		if !strings.Contains(lower, "busy") && !strings.Contains(lower, "locked") {
			break
		}
		select {
		case <-ctx.Done():
			_ = repo.Close()
			return Manifest{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
		}
	}
	if snapshotErr != nil {
		_ = repo.Close()
		return Manifest{}, fmt.Errorf("online backup snapshot failed after retries: %w", snapshotErr)
	}
	if err := repo.Close(); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{FormatVersion: FormatVersion, CreatedAt: time.Now().UTC(), SugaVersion: opts.SugaVersion}
	if err := addExternal(&manifest, tmpPath, "db/sessions.db"); err != nil {
		return Manifest{}, err
	}
	configPath := filepath.Join(home, "config.yaml")
	if _, err := os.Lstat(configPath); err == nil {
		if err := addHomeFile(&manifest, home, configPath, "config.yaml"); err != nil {
			return Manifest{}, err
		}
	}
	for _, root := range []struct{ path, zipRoot string }{{filepath.Join(home, "memory", "user"), "memory/user"}, {filepath.Join(home, "projects"), "memory/projects"}} {
		if err := collectTree(&manifest, home, root.path, root.zipRoot, "memory"); err != nil {
			return Manifest{}, err
		}
	}
	if err := scanSymlinks(filepath.Join(home, "sessions")); err != nil {
		return Manifest{}, err
	}
	if opts.IncludeSpill {
		if err := collectSpill(&manifest, home, filepath.Join(home, "sessions")); err != nil {
			return Manifest{}, err
		}
		manifest.Spill.Policy = "included"
		for _, f := range manifest.Files {
			if strings.HasPrefix(f.Path, "sessions/") {
				manifest.Spill.FileCount++
				manifest.Spill.Bytes += f.Size
			}
		}
	} else {
		manifest.Spill.Policy = "excluded"
		manifest.Spill.OmittedReason = "spill is excluded by default; pass --include-spill"
		manifest.Spill.OmittedBytes = treeBytes(filepath.Join(home, "sessions"))
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return writeArchive(output, tmpPath, home, manifest)
}

func writeArchive(output, snapshot, home string, manifest Manifest) (Manifest, error) {
	file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Manifest{}, err
	}
	zw := zip.NewWriter(file)
	for _, item := range manifest.Files {
		var src string
		if item.Path == "db/sessions.db" {
			src = snapshot
		} else {
			src = item.Source
			if src == "" {
				src = filepath.Join(home, filepath.FromSlash(item.Path))
			}
		}
		in, err := os.Open(src)
		if err != nil {
			_ = zw.Close()
			_ = file.Close()
			return Manifest{}, err
		}
		out, err := zw.Create(item.Path)
		if err == nil {
			_, err = io.Copy(out, in)
		}
		_ = in.Close()
		if err != nil {
			_ = zw.Close()
			_ = file.Close()
			return Manifest{}, err
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		_, err = writeZipFile(zw, "manifest.json", data)
	}
	if err == nil {
		err = zw.Close()
	} else {
		_ = zw.Close()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func writeZipFile(zw *zip.Writer, name string, data []byte) (int, error) {
	w, err := zw.Create(name)
	if err != nil {
		return 0, err
	}
	return w.Write(data)
}

// Validate checks the archive central directory, manifest and every file hash
// without changing the home directory.
func Validate(path, home string) (Manifest, error) {
	staging, manifest, err := validateAndExtract(path, home)
	if err != nil {
		return Manifest{}, err
	}
	_ = os.RemoveAll(staging)
	return manifest, nil
}
func Restore(ctx context.Context, archivePath, home string, noAuto bool, autoBackup func(string) error) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("restore home is empty")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	archivePath, err = filepath.Abs(archivePath)
	if err != nil {
		return "", err
	}
	staging, manifest, err := validateAndExtract(archivePath, home)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if noAuto { // The CLI obtains explicit confirmation before reaching this path.
	} else if autoBackup != nil {
		backupPath := filepath.Join(home, "backups", "pre-restore-"+time.Now().UTC().Format("20060102-150405")+".suga-backup.zip")
		if err := autoBackup(backupPath); err != nil {
			return "", fmt.Errorf("automatic pre-restore backup: %w", err)
		}
	}
	stagedDB := filepath.Join(staging, "db", "sessions.db")
	ro, err := store.OpenReadOnly(stagedDB)
	if err != nil {
		return "", fmt.Errorf("validate staged database: %w", err)
	}
	if err := store.CheckIntegrity(ctx, ro.DB()); err != nil {
		_ = ro.Close()
		return "", err
	}
	_ = ro.Close()
	if err := replaceData(staging, home, manifest); err != nil {
		return "", err
	}
	return manifest.CreatedAt.Format(time.RFC3339), nil
}

func validateAndExtract(path, home string) (string, Manifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", Manifest{}, fmt.Errorf("open backup: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > maxArchiveFiles {
		return "", Manifest{}, errors.New("backup contains too many files")
	}
	seen := map[string]bool{}
	var total uint64
	var mf *zip.File
	for _, f := range zr.File {
		name, err := safePath(f.Name)
		if err != nil {
			return "", Manifest{}, err
		}
		if seen[name] {
			return "", Manifest{}, fmt.Errorf("duplicate archive path %q", name)
		}
		seen[name] = true
		if name == "manifest.json" {
			mf = f
		}
		if f.UncompressedSize64 > maxExpandRatio*(f.CompressedSize64+1<<20) {
			return "", Manifest{}, fmt.Errorf("archive expansion ratio too large for %q", name)
		}
		total += f.UncompressedSize64
		if total > maxArchiveBytes {
			return "", Manifest{}, errors.New("backup is too large")
		}
	}
	if mf == nil {
		return "", Manifest{}, errors.New("backup has no manifest.json")
	}
	mdata, err := readZip(mf)
	if err != nil {
		return "", Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(mdata, &manifest); err != nil {
		return "", Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	if manifest.FormatVersion != FormatVersion {
		return "", Manifest{}, fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	wanted := map[string]File{}
	for _, f := range manifest.Files {
		name, err := safePath(f.Path)
		if err != nil {
			return "", Manifest{}, err
		}
		if name == "manifest.json" || wanted[name].Path != "" {
			return "", Manifest{}, fmt.Errorf("invalid manifest path %q", f.Path)
		}
		wanted[name] = f
	}
	for name := range seen {
		if name != "manifest.json" {
			if _, ok := wanted[name]; !ok {
				return "", Manifest{}, fmt.Errorf("archive file %q is not listed in manifest", name)
			}
		}
	}
	if _, ok := wanted["db/sessions.db"]; !ok {
		return "", Manifest{}, errors.New("manifest does not contain db/sessions.db")
	}
	for name := range wanted {
		if !seen[name] {
			return "", Manifest{}, fmt.Errorf("manifest lists missing archive file %q", name)
		}
	}
	staging, err := os.MkdirTemp(home, ".restore-*")
	if err != nil {
		return "", Manifest{}, err
	}
	for _, f := range zr.File {
		name := f.Name
		if name == "manifest.json" {
			continue
		}
		item := wanted[name]
		body, err := readZip(f)
		if err != nil {
			os.RemoveAll(staging)
			return "", Manifest{}, err
		}
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), item.SHA256) {
			os.RemoveAll(staging)
			return "", Manifest{}, fmt.Errorf("sha256 mismatch for %q", name)
		}
		dst := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			os.RemoveAll(staging)
			return "", Manifest{}, err
		}
		if err := os.WriteFile(dst, body, 0o600); err != nil {
			os.RemoveAll(staging)
			return "", Manifest{}, err
		}
	}
	return staging, manifest, nil
}

func replaceData(staging, home string, manifest Manifest) error {
	rollback, err := os.MkdirTemp(home, ".restore-rollback-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(rollback)
	paths := []string{"db/sessions.db", "config.yaml", "memory/user", "projects"}
	moved := make([]string, 0, len(paths))
	installed := make([]string, 0, len(paths))
	restore := func() {
		for i := len(installed) - 1; i >= 0; i-- {
			_ = os.RemoveAll(filepath.Join(home, filepath.FromSlash(installed[i])))
		}
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(filepath.Join(rollback, filepath.FromSlash(moved[i])), filepath.Join(home, filepath.FromSlash(moved[i])))
		}
	}
	for _, rel := range paths {
		src := filepath.Join(staging, filepath.FromSlash(rel))
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := os.Lstat(dst); err == nil {
			backup := filepath.Join(rollback, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
				restore()
				return err
			}
			if err := os.Rename(dst, backup); err != nil {
				restore()
				return fmt.Errorf("replace %s (close suga handles and retry): %w", rel, err)
			}
			moved = append(moved, rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			restore()
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			restore()
			return fmt.Errorf("rename restored %s (Windows may still have an open handle): %w", rel, err)
		}
		installed = append(installed, rel)
	}
	// Spill files are intentionally merged rather than replacing the whole sessions root.
	for _, f := range manifest.Files {
		if !strings.HasPrefix(f.Path, "sessions/") {
			continue
		}
		src := filepath.Join(staging, filepath.FromSlash(f.Path))
		dst := filepath.Join(home, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			restore()
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			restore()
			return fmt.Errorf("restore spill %s: %w", f.Path, err)
		}
		installed = append(installed, f.Path)
	}
	return nil
}

func readZip(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, int64(maxArchiveBytes)+1))
}
func safePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path traversal in archive %q", name)
	}
	return clean, nil
}
func absoluteHome(home string) (string, error) {
	if home == "" {
		return appconfig.ResolveSugaHome()
	}
	return filepath.Abs(home)
}
func addExternal(m *Manifest, path, zipPath string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	m.Files = append(m.Files, File{Path: zipPath, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Source: path})
	return nil
}
func addHomeFile(m *Manifest, home, path, zipPath string) error {
	if filepath.Clean(path) != filepath.Join(home, filepath.FromSlash(zipPath)) {
		return errors.New("backup path escapes home")
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}
	return addExternal(m, path, zipPath)
}
func collectTree(m *Manifest, home, root, zipRoot, source string) error {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		zipPath := filepath.ToSlash(filepath.Join(zipRoot, rel))
		return addHomeFile(m, home, path, zipPath)
	})
}
func collectSpill(m *Manifest, home, root string) error {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 3 || parts[len(parts)-2] != "outputs" {
			return nil
		}
		zipPath := filepath.ToSlash(filepath.Join("sessions", rel))
		return addHomeFile(m, home, path, zipPath)
	})
}

func scanSymlinks(root string) error {
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
		return nil
	})
}
func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink %s", path)
	}
	return nil
}
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/1024)
}

func treeBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Mode().IsRegular() && filepath.Base(filepath.Dir(path)) == "outputs" {
			total += info.Size()
		}
		return nil
	})
	return total
}
