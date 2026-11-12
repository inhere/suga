package axconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/ini/v2/dotenv"
)

// loadedHome is the root the configuration was loaded from in this process
// ("" = Load has not run). The suga settings are process global, so a host that
// embeds the agent (pkg/sugasdk) reads it to keep one configuration at a time.
var loadedHome string

// loadedConfigState is the one read of the config file Load loaded from.
type loadedConfigState struct {
	path string
	data []byte
	ok   bool
}

// loadedConfig is the exact bytes Load read, so the startup comparison of the
// protected settings and the values the process actually runs on come from one
// read of one file. ok is false until Load succeeded in this process.
var loadedConfig loadedConfigState

// LoadedHome returns the root Load loaded the configuration from, and whether
// Load ran in this process at all.
func LoadedHome() (string, bool) {
	return loadedHome, loadedHome != ""
}

// LoadedConfigBytes returns the absolute path of the config file Load read, the
// bytes it loaded and whether Load succeeded in this process. A missing default
// config file leaves data empty.
func LoadedConfigBytes() (path string, data []byte, ok bool) {
	if !loadedConfig.ok {
		return "", nil, false
	}
	return loadedConfig.path, loadedConfig.data, true
}

// ResetLoadedStateForTest forgets the loaded root and config bytes, so a test
// can load another SUGA_HOME. Tests call it from their cleanup; production code
// never does.
func ResetLoadedStateForTest() {
	loadedHome = ""
	loadedConfig = loadedConfigState{}
}

// Load initializes environment and YAML-backed configuration sources for the
// current process. Call it once near process startup before reading settings.
func Load() error {
	privateDir, err := ResolveSugaHome()
	if err != nil {
		return fmt.Errorf("resolve suga home for config: %w", err)
	}
	loadedHome = privateDir

	// Only SUGA_HOME/.env is read. The working directory's .env belongs to the
	// user's project (suga runs inside it) and must not rewrite suga's database
	// or config root. SUGA_HOME/SUGA_CONFIG are honoured from the process
	// environment only: the root was resolved above, so a value injected by the
	// .env file is reverted to keep the config root and data root consistent.
	guarded := snapshotEnv("SUGA_HOME", "SUGA_CONFIG")
	dotenv.LoadExistFiles(filepath.Join(privateDir, ".env"))
	guarded.restore()

	gkconfig.AddDriver(yaml.Driver)
	gkconfig.WithOptions(gkconfig.ParseEnv)
	// Resolve the config file before anything could change the working directory:
	// a relative SUGA_CONFIG is relative to the directory suga was started in.
	configPath := filepath.Join(privateDir, "config.yaml")
	explicit := strings.TrimSpace(os.Getenv("SUGA_CONFIG")) != ""
	if explicit {
		resolved, err := filepath.Abs(strings.TrimSpace(os.Getenv("SUGA_CONFIG")))
		if err != nil {
			return fmt.Errorf("resolve SUGA_CONFIG: %w", err)
		}
		configPath = resolved
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		// Like .env, config.yaml is read from SUGA_HOME only: a config.yaml in the
		// user's project is that project's file, not suga's. A missing default file
		// means an empty configuration; an explicitly chosen file must exist.
		if !explicit && errors.Is(err, os.ErrNotExist) {
			data = []byte{}
		} else if explicit {
			return fmt.Errorf("load SUGA_CONFIG: %w", err)
		} else {
			slog.Error("loading config file", "err", err)
			return err
		}
	}
	// The bytes are read once and loaded from memory (not through the files) so
	// the startup comparison of the protected settings sees the same content.
	if len(data) > 0 {
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(configPath)), ".")
		if format == "" {
			format = "yaml"
		}
		if err := gkconfig.LoadSources(format, data); err != nil {
			if explicit {
				return fmt.Errorf("load SUGA_CONFIG: %w", err)
			}
			slog.Error("loading config file", "err", err)
			return err
		}
	}
	if err := validateLoadedSettings(); err != nil {
		return err
	}
	loadedConfig = loadedConfigState{path: configPath, data: data, ok: true}
	return nil
}

// validateLoadedSettings runs startup validation for settings whose malformed
// values would otherwise be silently ignored until the approval layer starts.
func validateLoadedSettings() error {
	if _, err := ReadApprovalRulesSettings(); err != nil {
		return fmt.Errorf("validate approval rules settings: %w", err)
	}
	if _, err := ReadSnapshotSettings(); err != nil {
		return fmt.Errorf("validate snapshot settings: %w", err)
	}
	return nil
}

type envSnapshot map[string]*string

// snapshotEnv records the current process value (or absence) of each key.
func snapshotEnv(keys ...string) envSnapshot {
	snap := make(envSnapshot, len(keys))
	for _, key := range keys {
		if v, ok := os.LookupEnv(key); ok {
			snap[key] = &v
		} else {
			snap[key] = nil
		}
	}
	return snap
}

// restore puts every recorded key back to its snapshot state.
func (s envSnapshot) restore() {
	for key, v := range s {
		if v == nil {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, *v)
		}
	}
}
