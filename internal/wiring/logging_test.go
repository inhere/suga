package wiring

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
)

// defaultLogTestSettings are the effective log.* defaults ReadLogSettings
// returns for an empty config.
func defaultLogTestSettings() appconfig.LogSettings {
	return appconfig.LogSettings{
		Level: appconfig.LogLevelInfo, Stderr: appconfig.LogStderrAuto,
		MaxSize: appconfig.DefaultLogMaxSize, Keep: appconfig.DefaultLogKeep,
	}
}

// TestSetupLoggingIsQuietOutsideTheFile: a regular run writes its records to the
// log file only — stderr stays for the notices the run prints itself.
func TestSetupLoggingIsQuietOutsideTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	restoreDefaultLogger(t)

	var stderr bytes.Buffer
	file, err := setupLogging(&stderr, defaultLogTestSettings(), false, time.Now())
	assert.Require(t, assert.NoErr(t, err))
	closeLogFile(t, file)
	slog.Warn("llm request failed, retrying", "provider", "test")

	data, err := os.ReadFile(file.Path())
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(data), "level=WARN")
	assert.StrContains(t, string(data), "llm request failed, retrying")
	assert.StrContains(t, string(data), "provider=test")
	assert.Eq(t, "", stderr.String())
	// The name carries the day: every process of one day appends to one file.
	assert.Eq(t, filepath.Join(home, "logs", "suga-"+time.Now().Format("20060102")+".log"), file.Path())
}

// TestSetupLoggingDebugAlsoWritesStderr: debug mode is the diagnosing mode, so
// the records also reach stderr and name the log file.
func TestSetupLoggingDebugAlsoWritesStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	restoreDefaultLogger(t)

	var stderr bytes.Buffer
	file, err := setupLogging(&stderr, defaultLogTestSettings(), true, time.Now())
	assert.Require(t, assert.NoErr(t, err))
	closeLogFile(t, file)
	slog.Debug("debug record")

	data, err := os.ReadFile(file.Path())
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(data), "DEBUG MODE ENABLED")
	assert.StrContains(t, string(data), "debug record")
	assert.StrContains(t, string(data), "log_file="+file.Path())
	assert.StrContains(t, stderr.String(), "debug record")
}

// TestSetupLoggingStderrModes: log.stderr decides the copy on stderr; auto keeps
// the historical "only in debug mode" behavior.
func TestSetupLoggingStderrModes(t *testing.T) {
	cases := []struct {
		name      string
		stderr    string
		level     string
		debug     bool
		wantErrLn bool
	}{
		{name: "auto info stays quiet", stderr: appconfig.LogStderrAuto, level: appconfig.LogLevelInfo},
		{name: "auto debug writes", stderr: appconfig.LogStderrAuto, level: appconfig.LogLevelInfo, debug: true, wantErrLn: true},
		{name: "auto debug level writes", stderr: appconfig.LogStderrAuto, level: appconfig.LogLevelDebug, wantErrLn: true},
		{name: "always writes", stderr: appconfig.LogStderrAlways, level: appconfig.LogLevelInfo, wantErrLn: true},
		{name: "never stays quiet in debug", stderr: appconfig.LogStderrNever, level: appconfig.LogLevelInfo, debug: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SUGA_HOME", home)
			restoreDefaultLogger(t)
			settings := defaultLogTestSettings()
			settings.Stderr, settings.Level = tc.stderr, tc.level

			var stderr bytes.Buffer
			file, err := setupLogging(&stderr, settings, tc.debug, time.Now())
			assert.Require(t, assert.NoErr(t, err))
			closeLogFile(t, file)
			slog.Warn("stderr mode record")

			// Every mode still lands in the file.
			data, err := os.ReadFile(file.Path())
			assert.Require(t, assert.NoErr(t, err))
			assert.StrContains(t, string(data), "stderr mode record")
			assert.Eq(t, tc.wantErrLn, strings.Contains(stderr.String(), "stderr mode record"))
		})
	}
}

// TestSetupLoggingLevelFiltersRecords: log.level sets the minimum level.
func TestSetupLoggingLevelFiltersRecords(t *testing.T) {
	t.Run("warn drops info", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		restoreDefaultLogger(t)
		settings := defaultLogTestSettings()
		settings.Level = appconfig.LogLevelWarn

		var stderr bytes.Buffer
		file, err := setupLogging(&stderr, settings, false, time.Now())
		assert.Require(t, assert.NoErr(t, err))
		closeLogFile(t, file)
		slog.Info("info record")
		slog.Warn("warn record")

		data, err := os.ReadFile(file.Path())
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, string(data), "info record")
		assert.StrContains(t, string(data), "warn record")
	})
	t.Run("debug records", func(t *testing.T) {
		t.Setenv("SUGA_HOME", t.TempDir())
		restoreDefaultLogger(t)
		settings := defaultLogTestSettings()
		settings.Level = appconfig.LogLevelDebug

		var stderr bytes.Buffer
		file, err := setupLogging(&stderr, settings, false, time.Now())
		assert.Require(t, assert.NoErr(t, err))
		closeLogFile(t, file)
		slog.Debug("debug record")

		data, err := os.ReadFile(file.Path())
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, string(data), "debug record")
	})
}

// TestSetupLoggingFileOff: log.file: off writes no file at all; the stderr copy
// then follows log.stderr alone, and a run that turns both off says so once.
func TestSetupLoggingFileOff(t *testing.T) {
	t.Run("off keeps stderr copy", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("SUGA_HOME", home)
		restoreDefaultLogger(t)
		settings := defaultLogTestSettings()
		settings.File = appconfig.LogFileOff
		settings.Stderr = appconfig.LogStderrAlways

		var stderr bytes.Buffer
		file, err := setupLogging(&stderr, settings, false, time.Now())
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, file == nil)
		slog.Warn("file off record")

		assert.StrContains(t, stderr.String(), "file off record")
		_, statErr := os.Stat(filepath.Join(home, "logs"))
		assert.Err(t, statErr)
	})
	t.Run("off and never is silent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("SUGA_HOME", home)
		restoreDefaultLogger(t)
		settings := defaultLogTestSettings()
		settings.File = appconfig.LogFileOff
		settings.Stderr = appconfig.LogStderrNever

		var stderr bytes.Buffer
		file, err := setupLogging(&stderr, settings, false, time.Now())
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, file == nil)
		slog.Warn("dropped record")

		assert.StrContains(t, stderr.String(), "logging is off")
		assert.NotContains(t, stderr.String(), "dropped record")
		_, statErr := os.Stat(filepath.Join(home, "logs"))
		assert.Err(t, statErr)
	})
}

// TestSetupLoggingFallsBackToStderr: a log file that cannot be opened must not
// silence the logs, so stderr keeps them even though it is otherwise quiet.
func TestSetupLoggingFallsBackToStderr(t *testing.T) {
	// A file where the log directory should be: <SUGA_HOME>/logs cannot be created.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	assert.Require(t, assert.NoErr(t, os.WriteFile(blocker, []byte("x"), 0o600)))
	t.Setenv("SUGA_HOME", blocker)
	restoreDefaultLogger(t)

	var stderr bytes.Buffer
	file, err := setupLogging(&stderr, defaultLogTestSettings(), false, time.Now())
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, true, file == nil)
	slog.Warn("still visible")

	assert.StrContains(t, stderr.String(), "cannot open the log file")
	assert.StrContains(t, stderr.String(), "still visible")
}

// TestSetupLoggingRejectsBadConfig: a bad log.* value stops the run at startup
// and the error names the key (SetupLogging runs right after the config load).
func TestSetupLoggingRejectsBadConfig(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	gkconfig.AddDriver(yaml.Driver)
	assert.Require(t, assert.NoErr(t, gkconfig.LoadSources(gkconfig.Yaml, []byte("log:\n  level: loud\n"))))

	err := SetupLogging()
	assert.ErrMsgContains(t, err, "log.level must be one of")
}

// TestLogFileRotatesBySizeKeepsWholeLines: reaching log.max_size rotates the
// file (a rotated backup appears) and no record of the rotated file is torn.
func TestLogFileRotatesBySizeKeepsWholeLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	settings := defaultLogTestSettings()
	settings.MaxSize = 2048

	file, err := openLogFile(settings, time.Now())
	assert.Require(t, assert.NoErr(t, err))
	closeLogFile(t, file)
	const records = 60
	for i := 0; i < records; i++ {
		_, err := fmt.Fprintf(file, "record %03d %s\n", i, strings.Repeat("x", 64))
		assert.Require(t, assert.NoErr(t, err))
	}

	files := logFiles(t, home)
	// The rotated files plus the current one; the last record sits in the current
	// file and every rotated file keeps whole lines only.
	assert.Eq(t, true, len(files) > 1)
	seen := map[int]bool{}
	for _, path := range files {
		for _, line := range wholeLines(t, path) {
			var n int
			if _, err := fmt.Sscanf(line, "record %03d ", &n); err == nil {
				assert.Eq(t, false, seen[n])
				seen[n] = true
			}
		}
	}
	assert.Eq(t, records, len(seen))
}

// TestLogFileKeepsCountingOldBackups: log.keep prunes rotated files by count.
// rotatefile skips files touched in the last 30s, so the test ages its backups
// first and then waits for the asynchronous cleanup a write triggers.
func TestLogFileKeepsCountingOldBackups(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	settings := defaultLogTestSettings()
	settings.Keep = 2

	file, err := openLogFile(settings, time.Now())
	assert.Require(t, assert.NoErr(t, err))
	closeLogFile(t, file)
	// Six backups from earlier days, all old enough for the cleaner to count.
	old := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 6; i++ {
		path := strings.TrimSuffix(file.Path(), ".log") + fmt.Sprintf("_%d.log", i)
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte("old\n"), 0o600)))
		assert.Require(t, assert.NoErr(t, os.Chtimes(path, old, old)))
	}

	// Writes trigger rotatefile's asynchronous clean with a 20% chance each.
	for i := 0; i < 200; i++ {
		_, err := fmt.Fprintf(file, "record %03d\n", i)
		assert.Require(t, assert.NoErr(t, err))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		backups := 0
		for _, path := range logFiles(t, home) {
			if path != file.Path() {
				backups++
			}
		}
		if backups <= settings.Keep {
			return
		}
		if time.Now().After(deadline) {
			assert.Eq(t, true, backups <= settings.Keep)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestConcurrentWritersKeepEveryLine: several processes append to the same log
// file (a chat and the runs Gofer starts). Each writer is its own rotatefile
// writer, the way another process would be, and rotation by size happens while
// they race: every record must still be there and whole.
func TestConcurrentWritersKeepEveryLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	settings := defaultLogTestSettings()
	settings.MaxSize = 8192

	const writers, records = 4, 60
	lineFormat := regexp.MustCompile(`^writer=(\d+) record=(\d+) pad=[a-z]+$`)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			file, err := openLogFile(settings, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = file.Close() }()
			for i := 0; i < records; i++ {
				line := fmt.Sprintf("writer=%d record=%d pad=%s\n", w, i, strings.Repeat("y", 80))
				if _, err := file.Write([]byte(line)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	seen := map[string]bool{}
	total := 0
	for _, path := range logFiles(t, home) {
		for _, line := range wholeLines(t, path) {
			match := lineFormat.FindStringSubmatch(line)
			assert.Require(t, assert.NotNil(t, match))
			seen[line] = true
			total++
		}
	}
	// No record is lost and none is duplicated.
	assert.Eq(t, writers*records, total)
	assert.Eq(t, writers*records, len(seen))
	for w := 0; w < writers; w++ {
		for i := 0; i < records; i++ {
			found := false
			for line := range seen {
				if strings.HasPrefix(line, fmt.Sprintf("writer=%d record=%d ", w, i)) {
					found = true
					break
				}
			}
			assert.Eq(t, true, found)
		}
	}
}

// logFiles lists the log files of the temporary home (current and rotated).
func logFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "logs"))
	assert.Require(t, assert.NoErr(t, err))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(home, "logs", entry.Name()))
	}
	return paths
}

// wholeLines returns the lines of a log file, failing on one that does not end
// with its newline (a torn record).
func wholeLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	text := string(data)
	if text != "" {
		assert.Eq(t, true, strings.HasSuffix(text, "\n"))
	}
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// closeLogFile closes the log file a test opened. The process keeps it open for
// its lifetime; on Windows an open file blocks the temporary home's cleanup.
func closeLogFile(t *testing.T, file *logFile) {
	t.Helper()
	if file == nil {
		return
	}
	t.Cleanup(func() { _ = file.Close() })
}

// restoreDefaultLogger puts the previous default logger back so the rest of the
// package keeps the logger it started with.
func restoreDefaultLogger(t *testing.T) {
	t.Helper()
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
}
