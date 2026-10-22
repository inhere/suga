package axconfig

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/ini/v2/dotenv"
)

// loadedHome is the root the configuration was loaded from in this process
// ("" = Load has not run). The suga settings are process global, so a host that
// embeds the agent (pkg/sugasdk) reads it to keep one configuration at a time.
var loadedHome string

// LoadedHome returns the root Load loaded the configuration from, and whether
// Load ran in this process at all.
func LoadedHome() (string, bool) {
	return loadedHome, loadedHome != ""
}

// ResetLoadedStateForTest forgets the loaded root, so a test can load another
// SUGA_HOME. Tests call it from their cleanup; production code never does.
func ResetLoadedStateForTest() {
	loadedHome = ""
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
	if configPath := os.Getenv("SUGA_CONFIG"); configPath != "" {
		if err := gkconfig.LoadFiles(configPath); err != nil {
			return fmt.Errorf("load SUGA_CONFIG: %w", err)
		}
		return validateLoadedSettings()
	}
	// Like .env, config.yaml is read from SUGA_HOME only: a config.yaml in the
	// user's project is that project's file, not suga's.
	if err := gkconfig.LoadExists(filepath.Join(privateDir, "config.yaml")); err != nil {
		slog.Error("loading config file", "err", err)
		return err
	}
	return validateLoadedSettings()
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
