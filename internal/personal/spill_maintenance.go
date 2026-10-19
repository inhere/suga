package personal

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Spill maintenance of the data governance design (Spill 配额、压缩与透明读取):
// it reports the spill files below a sessions root, decides which idle .txt
// files a compression pass may rewrite as .txt.gz, and does that rewrite
// atomically (verify, rename, then remove the plain file). Only `suga data gc
// --compress-spill` calls it; nothing here runs at session start or in a write
// path, and a ref (out:<session_id>:<stem>) never changes.

// SpillFile is one spill file found below the sessions root.
type SpillFile struct {
	// SessionID owns the file; Stem plus SessionID is its ref.
	SessionID string
	Stem      string
	// Size is the bytes on disk, Logical the uncompressed bytes (a .gz records
	// them in its trailer).
	Size    int64
	Logical int64
	// Compressed marks a .txt.gz.
	Compressed bool
	ModTime    time.Time
}

// Ref is the output ref of the file: refs do not change when a file is
// compressed.
func (f SpillFile) Ref() string { return outputRefPrefix + f.SessionID + ":" + f.Stem }

// SpillUsage is the quota picture of a sessions root: spill files are counted
// logically (what recall has to read) and physically (what is on disk).
type SpillUsage struct {
	Sessions      int
	Files         int
	Compressed    int
	LogicalBytes  int64
	PhysicalBytes int64
	MaxBytes      int64
}

// OverQuota reports whether the logical size is over the configured quota.
func (u SpillUsage) OverQuota() bool { return u.MaxBytes > 0 && u.LogicalBytes > u.MaxBytes }

// ScanSpill counts the spill files below sessionsDir and lists them, oldest
// first. A missing sessions root is not an error: a fresh home has no spill
// files yet.
func ScanSpill(sessionsDir string) (SpillUsage, []SpillFile, error) {
	usage := SpillUsage{}
	var files []SpillFile
	sessions := map[string]bool{}
	if strings.TrimSpace(sessionsDir) == "" {
		return usage, nil, errors.New("no sessions directory configured")
	}
	err := filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sessionsDir, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		// sessions/<session_id>/outputs/<file>: anything else is not a spill file.
		if len(parts) != 3 || parts[1] != "outputs" {
			return nil
		}
		name := parts[2]
		stem, ok := OutputStem(name)
		if !ok {
			return nil // a .tmp of an interrupted write, or a foreign file
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			// A symlink or a device is never a spill file: reading it could
			// leave the session directory, and recall refuses it too.
			return nil
		}
		file := SpillFile{
			SessionID: parts[0], Stem: stem, Size: info.Size(), Logical: info.Size(),
			Compressed: strings.HasSuffix(name, gzSuffix), ModTime: info.ModTime(),
		}
		if file.Compressed {
			if f, err := os.Open(path); err == nil {
				if logical := gzLogicalBytes(f, info.Size()); logical > 0 {
					file.Logical = logical
				}
				_ = f.Close()
			}
		}
		files = append(files, file)
		usage.Files++
		usage.LogicalBytes += file.Logical
		usage.PhysicalBytes += file.Size
		if file.Compressed {
			usage.Compressed++
		}
		if !sessions[file.SessionID] {
			sessions[file.SessionID] = true
		}
		return nil
	})
	usage.Sessions = len(sessions)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return usage, nil, err
	}
	// Oldest first: the report and a follow-up pass read them in the order a
	// maintenance window would handle them.
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].Ref() < files[j].Ref()
		}
		return files[i].ModTime.Before(files[j].ModTime)
	})
	return usage, files, nil
}

// SpillCompressEntry is one file a compression pass planned, with the size of
// its compressed form (measured in both modes, so a dry run reports the same
// numbers an apply would produce).
type SpillCompressEntry struct {
	// File is the .txt that was (or would be) rewritten.
	File SpillFile
	// Size is its size on disk, CompressedSize the size of the .txt.gz.
	Size           int64
	CompressedSize int64
}

type SpillCompressOptions struct {
	// OlderThan compresses only files whose mtime is older than this.
	OlderThan time.Duration
	// Active holds the session ids a live process is using; their files are
	// never rewritten (a task output may still be growing).
	Active map[string]bool
	// Apply rewrites the files; false only measures what a pass would do.
	Apply bool
	// Now is the reference time of OlderThan (zero means time.Now).
	Now time.Time
}

// SpillCompressReport is what one pass did (or, in a dry run, would do).
type SpillCompressReport struct {
	// Usage is the spill picture before the pass, AfterUsage the one after.
	Usage      SpillUsage
	AfterUsage SpillUsage
	// Planned are the files the age and activity rules allow to compress.
	Planned []SpillCompressEntry
	// BytesBefore is the on-disk size of Planned, BytesAfter the size its
	// compressed form has (measured in both modes); SkippedActive and
	// SkippedYoung count the files the rules left alone.
	BytesBefore   int64
	BytesAfter    int64
	SkippedActive int
	SkippedYoung  int
}

// CompressSpillOutputs compresses the idle .txt outputs of sessionsDir. Each
// file is written as <stem>.txt.gz through a temporary file in the same
// directory, decompressed again to verify its content, renamed into place and
// only then is the plain file removed: a failure at any step leaves the
// original output readable under the same ref. In a dry run (Apply false)
// nothing on disk changes; the report measures the compressed size by
// compressing into a counter.
func CompressSpillOutputs(ctx context.Context, sessionsDir string, opts SpillCompressOptions) (SpillCompressReport, error) {
	usage, files, err := ScanSpill(sessionsDir)
	if err != nil {
		return SpillCompressReport{}, err
	}
	report := SpillCompressReport{Usage: usage}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var root *os.Root
	if root, err = os.OpenRoot(sessionsDir); err != nil {
		// A home without spill files (or without a sessions root at all) has
		// nothing to compress and is no failure.
		if errors.Is(err, fs.ErrNotExist) && usage.Files == 0 {
			return report, nil
		}
		return report, fmt.Errorf("open sessions directory: %w", err)
	}
	defer root.Close()
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if file.Compressed {
			continue
		}
		if opts.OlderThan > 0 && now.Sub(file.ModTime) < opts.OlderThan {
			report.SkippedYoung++
			continue
		}
		if opts.Active[file.SessionID] {
			report.SkippedActive++
			continue
		}
		report.Planned = append(report.Planned, SpillCompressEntry{File: file, Size: file.Size})
		report.BytesBefore += file.Size
		rel := filepath.Join(file.SessionID, "outputs", file.Stem+".txt")
		size, err := compressSpillFile(ctx, root, rel, file, !opts.Apply)
		if err != nil {
			return report, err
		}
		report.Planned[len(report.Planned)-1].CompressedSize = size
		report.BytesAfter += size
	}
	if opts.Apply {
		after, _, err := ScanSpill(sessionsDir)
		if err != nil {
			return report, err
		}
		after.MaxBytes = usage.MaxBytes
		report.AfterUsage = after
	} else {
		report.AfterUsage = usage
		report.AfterUsage.PhysicalBytes = usage.PhysicalBytes - report.BytesBefore + report.BytesAfter
	}
	return report, nil
}

// compressSpillFile gzips <stem>.txt and publishes it as <stem>.txt.gz. With
// measure set it only reports how large the compressed form would be and leaves
// the file alone; otherwise it returns the published size.
func compressSpillFile(ctx context.Context, root *os.Root, rel string, file SpillFile, measure bool) (int64, error) {
	if measure {
		src, err := root.Open(rel)
		if err != nil {
			return 0, fmt.Errorf("open %s: %w", file.Ref(), err)
		}
		defer src.Close()
		return gzipInto(io.Discard, src)
	}
	tmpRel := rel + ".gz.tmp"
	src, err := root.Open(rel)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", file.Ref(), err)
	}
	defer src.Close()
	tmp, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create the compressed form of %s: %w", file.Ref(), err)
	}
	// The hash and the size of the original are taken while it is read: they are
	// what the published .gz is verified against before the .txt goes away.
	sum := sha256.New()
	source := &countingReader{r: io.TeeReader(src, sum)}
	size, err := gzipInto(tmp, source)
	if err != nil {
		_ = tmp.Close()
		_ = root.Remove(tmpRel)
		return 0, fmt.Errorf("compress %s: %w", file.Ref(), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = root.Remove(tmpRel)
		return 0, fmt.Errorf("sync the compressed form of %s: %w", file.Ref(), err)
	}
	if err := tmp.Close(); err != nil {
		_ = root.Remove(tmpRel)
		return 0, fmt.Errorf("close the compressed form of %s: %w", file.Ref(), err)
	}
	if err := verifyGzip(root, tmpRel, source.n, sum); err != nil {
		_ = root.Remove(tmpRel)
		return 0, fmt.Errorf("verify %s: %w", file.Ref(), err)
	}
	if err := root.Rename(tmpRel, rel+".gz"); err != nil {
		_ = root.Remove(tmpRel)
		return 0, fmt.Errorf("publish the compressed form of %s: %w", file.Ref(), err)
	}
	// Only now is the plain file removed: until this line the same ref reads
	// from the .txt, and the reader prefers the .txt while both exist.
	if err := root.Remove(rel); err != nil {
		return size, fmt.Errorf("remove the plain form of %s: %w", file.Ref(), err)
	}
	if err := ctx.Err(); err != nil {
		return size, err
	}
	return size, nil
}

// verifyGzip decompresses the written file and compares what it holds with the
// size and the hash of the original, so a truncated or mixed up archive is
// caught before it replaces anything.
func verifyGzip(root *os.Root, rel string, size int64, want hash.Hash) error {
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	got := sha256.New()
	n, err := io.Copy(got, zr)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("holds %d bytes after decompression, want %d", n, size)
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		return errors.New("holds different bytes than the original")
	}
	return nil
}

// gzipInto writes r compressed into w and returns the number of compressed
// bytes. No header timestamp is written, so the same input yields the same
// bytes and the pass is idempotent across machines.
func gzipInto(w io.Writer, r io.Reader) (int64, error) {
	counter := &countingWriter{w: w}
	zw := gzip.NewWriter(counter)
	if _, err := io.Copy(zw, r); err != nil {
		_ = zw.Close()
		return counter.n, err
	}
	if err := zw.Close(); err != nil {
		return counter.n, err
	}
	return counter.n, nil
}

// countingWriter passes writes through and remembers the total.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// countingReader passes reads through and remembers how many bytes it saw.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
