package wiring

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gookit/rotatefile"

	appconfig "github.com/inhere/suga/internal/config"
)

// logFileName is the base name of the process log under SUGA_HOME; log.file
// overrides the whole path and log.file: off turns the file off.
const logFileName = "suga.log"

// SetupLogging installs the process logger from the log.* configuration (see
// internal/config/log.go). A record goes to the rotating log file and, per
// log.stderr (auto|always|never), additionally to stderr: what a person needs is
// printed by the run itself (the session id, the model, a retry, a wrap-up
// reason are plain notices on stderr) and Gofer reads stdout as protocol, so
// free-form log lines would only clutter the terminal or the job log. An invalid
// log.* value is returned as an error naming the key, so a typo stops the run at
// startup instead of being silently replaced by a default.
func SetupLogging() error {
	settings, err := appconfig.ReadLogSettings()
	if err != nil {
		return err
	}
	_, err = setupLogging(os.Stderr, settings, os.Getenv("SUGA_DEBUG") != "", time.Now())
	return err
}

// setupLogging is SetupLogging with the settings, the debug flag and the clock
// injected, so tests need neither the environment nor the process stderr. It
// returns the open log file (nil when the file is off or could not be opened):
// the process keeps it open for its lifetime, a test closes it before its
// temporary home goes away.
func setupLogging(stderr io.Writer, settings appconfig.LogSettings, debugMode bool, now time.Time) (*logFile, error) {
	level := logLevel(settings.Level)
	if debugMode {
		// SUGA_DEBUG stays the diagnosing switch: it also turns the level down.
		level = slog.LevelDebug
	}

	var (
		logFileHandle *logFile
		openErr       error
	)
	if settings.File != appconfig.LogFileOff {
		logFileHandle, openErr = openLogFile(settings, now)
	}
	writers := make([]io.Writer, 0, 2)
	stderrCopy := settings.Stderr == appconfig.LogStderrAlways ||
		(settings.Stderr == appconfig.LogStderrAuto && level == slog.LevelDebug)
	if logFileHandle != nil {
		// The handler writes each record in one call, so the open file needs no
		// flush at exit; it stays open for the process lifetime.
		writers = append(writers, logFileHandle)
	}
	switch {
	case openErr != nil:
		// A log file that could not be opened must not turn into silence, so
		// stderr keeps the records even when log.stderr says never.
		writers = append(writers, stderr)
	case stderrCopy:
		writers = append(writers, stderr)
	}
	if len(writers) == 0 {
		// log.file: off with log.stderr: never: the logs are discarded, and the
		// one notice says where they went.
		_, _ = fmt.Fprintln(stderr, "suga: logging is off (log.file: off, log.stderr: never)")
		writers = append(writers, io.Discard)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})))

	if openErr != nil {
		slog.Warn("cannot open the log file; logging to stderr only", "path", settings.File, "err", openErr)
	}
	if debugMode || level == slog.LevelDebug {
		slog.Debug("DEBUG MODE ENABLED - Verbose logging is active", "log_file", logFileHandle.Path())
	}
	return logFileHandle, nil
}

// logLevel maps log.level onto a slog level. ReadLogSettings already rejected
// everything else.
func logLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case appconfig.LogLevelDebug:
		return slog.LevelDebug
	case appconfig.LogLevelWarn:
		return slog.LevelWarn
	case appconfig.LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logFile is the rotating log file. Every process of a machine appends to the
// same file, so the writer adds what rotatefile does not give on its own: a
// rebuild after a write failure (see Write).
type logFile struct {
	// mu guards the rotatefile writer this file rebuilds, not the file contents:
	// records of one process are serialized, other processes are not.
	mu sync.Mutex
	// cfg is the rotatefile configuration with the current path; the writer
	// holds a pointer to it, so the field must not be copied.
	cfg rotatefile.Config
	w   *rotatefile.Writer
}

// openLogFile opens (and rotates) the log file of the day now falls in. The
// directory is created with the permissions the rest of the data directory uses.
func openLogFile(settings appconfig.LogSettings, now time.Time) (*logFile, error) {
	base := strings.TrimSpace(settings.File)
	if base == "" {
		home, err := appconfig.ResolveSugaHome()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, "logs", logFileName)
	}
	path := dayLogPath(base, now)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file := &logFile{cfg: rotatefileConfig(path, settings)}
	writer, err := rotatefile.NewWriter(&file.cfg)
	if err != nil {
		return nil, err
	}
	file.w = writer
	return file, nil
}

// rotatefileConfig maps log.* onto a rotatefile writer. Two choices keep the
// many processes that append to this one file (a chat and several Gofer runs)
// from destroying each other's lines:
//
//   - The day is part of the file name (dayLogPath), so a process started the
//     next day writes a new file instead of continuing yesterday's, and every
//     process of one day shares exactly one file.
//   - Rotation is by size only (RotateTime 0, so rotatefile never runs its
//     time rotation) in ModeCreate: a size rotation renames the current file to
//     "<base>_<num>.log" and reopens "<base>.log". ModeRename renames into the
//     same "<name>.<suffix>.log" pattern while reopening the configured path,
//     which under concurrent writers makes two processes rename each other's
//     fresh file; ModeCreate at least names the rotated file after the path the
//     rotating process actually holds, and the day in the name keeps a day's
//     rotation lane to itself. Neither mode is a lock: a rename by one process
//     still moves the file the others have open (their lines keep landing in the
//     rotated file, which the retry in Write survives), and Windows only lets a
//     file be renamed while every handle on it allows delete, which Go's
//     os.OpenFile does not: with three live `suga run` processes some rotations
//     are skipped and the file keeps growing until one succeeds, and every
//     record still lands. The day in the name is what ends such a file at the
//     next day's start.
func rotatefileConfig(path string, settings appconfig.LogSettings) rotatefile.Config {
	return rotatefile.Config{
		Filepath:   path,
		FilePerm:   0o600,
		RotateMode: rotatefile.ModeCreate,
		MaxSize:    uint64(settings.MaxSize),
		RotateTime: 0,
		// BackupNum is log.keep; BackupTime stays 0 so only the count prunes, and
		// rotatefile skips files modified in the last 30s while doing so.
		BackupNum: uint(settings.Keep),
		Compress:  settings.Compress,
		// EveryHour by default; rotation by size still needs a clock.
		TimeClock: rotatefile.DefaultTimeClockFn,
	}
}

// dayLogPath inserts the day into the configured path: "suga.log" becomes
// "suga-20261020.log". The date is what lets the processes of one day append to
// one shared file without any of them having to rename it.
func dayLogPath(base string, now time.Time) string {
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "-" + now.Format("20060102") + ext
}

// Write appends one record. rotatefile closes its handle before it renames the
// file during a size rotation, so a rename that fails (another process rotated
// the very same file a moment earlier, or Windows briefly holds the directory)
// leaves its writer pointing at a closed file and every later record would be
// lost: the writer is rebuilt here and the record is retried once.
func (f *logFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.w.Write(p)
	if err == nil {
		return n, nil
	}
	// Writes are complete before rotation runs, so a record reported as fully
	// written is on disk; rebuilding the writer is enough and a second write
	// would duplicate the line.
	written := n >= len(p)
	writer, openErr := rotatefile.NewWriter(&f.cfg)
	if openErr == nil {
		// Close the replaced writer: it may still hold the file open (rotatefile
		// reopens it after a failed rename), and every leaked handle keeps the
		// file from being renamed or removed on Windows.
		_ = f.w.Close()
		f.w = writer
	}
	if written {
		return n, nil
	}
	if openErr != nil {
		return n, err
	}
	return f.w.Write(p)
}

// Path is the file currently written; "" when the file could not be opened.
func (f *logFile) Path() string {
	if f == nil {
		return ""
	}
	return f.cfg.Filepath
}

// Close closes the rotating writer; the process keeps it open for its lifetime
// and only a test needs this.
func (f *logFile) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.w.Close()
}
