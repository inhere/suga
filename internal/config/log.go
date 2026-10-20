package axconfig

import (
	"fmt"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// Log levels, stderr modes and defaults of the process logger (see
// internal/wiring/logging.go). The vocabulary lives here so `suga config set`
// and the runtime reader cannot drift apart.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"

	// LogStderrAuto keeps the historical behavior: the records also reach stderr
	// while debug mode is on (SUGA_DEBUG or log.level: debug).
	LogStderrAuto   = "auto"
	LogStderrAlways = "always"
	LogStderrNever  = "never"

	// DefaultLogMaxSize is the size one log file may reach before it rotates
	// (log.max_size).
	DefaultLogMaxSize = 10 << 20
	// DefaultLogKeep is how many rotated log files are kept (log.keep); 0 keeps
	// every rotated file.
	DefaultLogKeep = 5
	// LogFileOff is the log.file value that turns the log file off.
	LogFileOff = "off"
)

// LogLevels lists the accepted log.level values.
var LogLevels = []string{LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError}

// LogSettings are the effective log.* settings of the process logger.
type LogSettings struct {
	// Level is the minimum record level (log.level; "debug" also enables the
	// stderr copy under log.stderr: auto).
	Level string
	// File is the log path, empty for the default <SUGA_HOME>/logs/suga.log,
	// LogFileOff for no file at all (log.file).
	File string
	// Stderr decides the stderr copy (log.stderr): auto|always|never.
	Stderr string
	// MaxSize is the size in bytes after which the file rotates (log.max_size).
	MaxSize int64
	// Keep is the number of rotated files to keep (log.keep).
	Keep int
	// Compress gzips rotated files as they age out (log.compress).
	Compress bool
}

// ReadLogSettings reads the log: section of config.yaml. Unset keys take the
// defaults above; an invalid value is an error naming the key, so a typo stops
// the run at startup instead of silently falling back.
func ReadLogSettings() (LogSettings, error) {
	out := LogSettings{
		Level:  normalizeConfigToken(gkconfig.String("log.level", LogLevelInfo)),
		File:   strings.TrimSpace(gkconfig.String("log.file", "")),
		Stderr: normalizeConfigToken(gkconfig.String("log.stderr", LogStderrAuto)),
	}
	if err := readLogNumber("log.max_size", DefaultLogMaxSize, 1, &out.MaxSize); err != nil {
		return LogSettings{}, err
	}
	var keep int64
	if err := readLogNumber("log.keep", DefaultLogKeep, 0, &keep); err != nil {
		return LogSettings{}, err
	}
	out.Keep = int(keep)
	if gkconfig.Get("log.compress") != nil {
		value, ok := gkconfig.Get("log.compress").(bool)
		if !ok {
			return LogSettings{}, fmt.Errorf("log.compress must be true or false, got %q", strings.TrimSpace(gkconfig.String("log.compress")))
		}
		out.Compress = value
	}

	if !containsString(LogLevels, out.Level) {
		return LogSettings{}, fmt.Errorf("log.level must be one of %s, got %q", strings.Join(LogLevels, ", "), out.Level)
	}
	switch out.Stderr {
	case LogStderrAuto, LogStderrAlways, LogStderrNever:
	default:
		return LogSettings{}, fmt.Errorf("log.stderr must be one of %s, %s, %s, got %q",
			LogStderrAuto, LogStderrAlways, LogStderrNever, out.Stderr)
	}
	if out.File != "" && out.File != LogFileOff && strings.ContainsAny(out.File, "\x00") {
		return LogSettings{}, fmt.Errorf("log.file contains a NUL byte")
	}
	return out, nil
}

// readLogNumber reads one numeric log.* key; it reads the raw value as text so
// a mistyped value ("10MB") is reported against its key instead of being
// silently replaced by the default.
func readLogNumber(key string, defaultVal int64, min int64, dst *int64) error {
	*dst = defaultVal
	if gkconfig.Get(key) == nil {
		return nil
	}
	raw := strings.TrimSpace(gkconfig.String(key))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < min {
		return fmt.Errorf("%s must be a whole number >= %d, got %q", key, min, raw)
	}
	*dst = n
	return nil
}

// containsString reports whether values holds want (lower-case tokens).
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
