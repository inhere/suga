package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goccyyaml "github.com/goccy/go-yaml"
	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
)

// newConfigCommand builds the configuration command group. The group owns only
// parsing and rendering; source resolution, readers and YAML writes stay in
// internal/config so other callers use the same safety rules.
func newConfigCommand(stdout, stderr io.Writer) *capp.Cmd {
	config := capp.NewCmd("config", "Read and maintain suga configuration")
	config.LongHelp = `Configuration commands do not modify environment variables.
	Use "suga config path" to see the active home, config, and env paths.`
	config.SetOutput(stderr)
	config.Add(
		newConfigLeaf(stdout, stderr, "get", "Show an effective configuration value", runConfigGet, "key"),
		newConfigLeaf(stdout, stderr, "set", "Set a writable configuration value", runConfigSet, "key", "value"),
		newConfigLeaf(stdout, stderr, "unset", "Remove a writable configuration value", runConfigUnset, "key"),
		newConfigLeaf(stdout, stderr, "list", "List effective configuration values", runConfigList),
		newConfigLeaf(stdout, stderr, "path", "Show configuration and data paths", runConfigPath),
	)
	return config
}

type configLeafHandler func(io.Writer, *capp.Cmd) error

func newConfigLeaf(stdout, stderr io.Writer, name, description string, handler configLeafHandler, args ...string) *capp.Cmd {
	cmd := capp.NewCmd(name, description, func(c *capp.Cmd) error {
		return handler(stdout, c)
	})
	cmd.SetOutput(stderr)
	for _, arg := range args {
		cmd.AddArg(arg, "the configuration "+arg, true)
	}
	return cmd
}

func runConfigGet(out io.Writer, c *capp.Cmd) error {
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config get <key>")
	}
	key := strings.TrimSpace(c.Arg("key").String())
	value, err := appconfig.ReadConfigValue(key)
	if err != nil {
		return configReadError(key, err)
	}
	return writeConfigValue(out, value)
}

func runConfigSet(out io.Writer, c *capp.Cmd) error {
	tr := cliTranslator()
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config set <key> <value>")
	}
	key := strings.TrimSpace(c.Arg("key").String())
	if _, err := writableConfigKey(key); err != nil {
		return err
	}
	raw := c.Arg("value").String()
	value, err := appconfig.ParseConfigValue(key, raw)
	if err != nil {
		return configInputError(err.Error())
	}
	path, err := activeConfigPath()
	if err != nil {
		return err
	}
	result, err := editConfig(path, key, value, false)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%s\n", cliText(tr, "cli.config.written", map[string]any{"path": result.Path})); err != nil {
		return err
	}
	resolved, err := appconfig.ReadConfigValue(key)
	if err != nil {
		return fmt.Errorf("%s: %w", cliText(tr, "cli.config.read_updated", map[string]any{"key": key}), err)
	}
	return writeConfigValue(out, resolved)
}

func runConfigUnset(out io.Writer, c *capp.Cmd) error {
	tr := cliTranslator()
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config unset <key>")
	}
	key := strings.TrimSpace(c.Arg("key").String())
	if _, err := writableConfigKey(key); err != nil {
		return err
	}
	path, err := activeConfigPath()
	if err != nil {
		return err
	}
	if _, err := editConfig(path, key, nil, true); err != nil {
		if strings.Contains(err.Error(), "is not configured") {
			return configInputError(err.Error())
		}
		return err
	}
	_, err = fmt.Fprintln(out, cliText(tr, "cli.config.deleted", map[string]any{"key": key}))
	return err
}

func runConfigList(out io.Writer, c *capp.Cmd) error {
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config list")
	}
	keys := configListKeys()
	for _, key := range keys {
		value, err := appconfig.ReadConfigValue(key)
		if err != nil {
			return fmt.Errorf("read %s: %w", key, err)
		}
		if err := writeConfigValue(out, value); err != nil {
			return err
		}
	}
	return nil
}

func runConfigPath(out io.Writer, c *capp.Cmd) error {
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config path")
	}
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return fmt.Errorf("resolve suga home: %w", err)
	}
	configPath, err := activeConfigPath()
	if err != nil {
		return err
	}
	envPath := filepath.Join(home, ".env")
	source := "home"
	if strings.TrimSpace(os.Getenv("SUGA_CONFIG")) != "" {
		source = "explicit"
	}
	_, err = fmt.Fprintf(out, "home = %s\nconfig = %s\nenv = %s\nsource = %s\n", home, configPath, envPath, source)
	return err
}

func writableConfigKey(key string) (appconfig.ConfigKey, error) {
	descriptor, err := appconfig.MatchConfigKey(key)
	if err != nil {
		return appconfig.ConfigKey{}, configInputError(err.Error())
	}
	if descriptor.Sensitive || appconfig.IsSecretKey(key) {
		return appconfig.ConfigKey{}, configInputError(fmt.Sprintf("configuration key %q is secret; use .env and a ${NAME} reference", key))
	}
	if !descriptor.Writable {
		return appconfig.ConfigKey{}, configInputError(fmt.Sprintf("configuration key %q is read-only", key))
	}
	if !appconfig.IsSimpleConfigType(descriptor.Type) && descriptor.Reader == "" {
		return appconfig.ConfigKey{}, configInputError(fmt.Sprintf("configuration key %q has no typed reader", key))
	}
	return descriptor, nil
}

func activeConfigPath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("SUGA_CONFIG")); explicit != "" {
		return filepath.Abs(explicit)
	}
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return "", fmt.Errorf("resolve suga home: %w", err)
	}
	return filepath.Join(home, "config.yaml"), nil
}

func editConfig(path, key string, value any, unset bool) (appconfig.ConfigWriteResult, error) {
	validate := func(content []byte) error {
		return appconfig.ValidateConfigCandidate(content, key, unset)
	}
	var (
		result appconfig.ConfigWriteResult
		err    error
	)
	if unset {
		result, err = appconfig.UnsetConfigValue(path, key, appconfig.ConfigWriteOptions{Validate: validate})
	} else {
		result, err = appconfig.WriteConfigValue(path, key, value, appconfig.ConfigWriteOptions{Validate: validate})
	}
	// Candidate validation temporarily replaces gookit/config's process data, so
	// always reload the active file before returning to the command loop.
	reloadErr := appconfig.Reload()
	if err != nil {
		if reloadErr != nil {
			return appconfig.ConfigWriteResult{}, fmt.Errorf("%w; reload config: %v", err, reloadErr)
		}
		return appconfig.ConfigWriteResult{}, err
	}
	if reloadErr != nil {
		return appconfig.ConfigWriteResult{}, fmt.Errorf("reload config after write: %w", reloadErr)
	}
	return result, nil
}

func configListKeys() []string {
	keys := make(map[string]struct{})
	for _, descriptor := range appconfig.KnownConfigKeys() {
		keys[descriptor.Pattern] = struct{}{}
	}
	collectConfigKeys(gkconfig.Data(), nil, keys)
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// collectConfigKeys adds only registered leaves. It also visits mapping nodes,
// allowing dynamic provider/model fields while ignoring arbitrary YAML keys.
func collectConfigKeys(value any, prefix []string, keys map[string]struct{}) {
	if len(prefix) > 0 {
		key := strings.Join(prefix, ".")
		if _, err := appconfig.MatchConfigKey(key); err == nil {
			keys[key] = struct{}{}
		}
	}
	mapping, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, child := range mapping {
		collectConfigKeys(child, append(prefix, key), keys)
	}
}

func writeConfigValue(out io.Writer, value appconfig.ResolvedConfigValue) error {
	if _, err := fmt.Fprintf(out, "%s = %s\nsource = %s\n", value.Key, formatConfigValue(value.Value), formatConfigSource(value)); err != nil {
		return err
	}
	return nil
}

func formatConfigSource(value appconfig.ResolvedConfigValue) string {
	source := string(value.Source)
	switch value.Source {
	case appconfig.SourceConfig:
		source = "config: " + value.SourceName
	case appconfig.SourceEnv:
		source = "env:" + value.SourceName
	}
	return source
}

func formatConfigValue(value any) string {
	if value == nil {
		return "<unset>"
	}
	if items, ok := value.([]string); ok {
		return strings.Join(items, ", ")
	}
	if mapping, ok := value.(map[string]string); ok {
		encoded, err := json.Marshal(mapping)
		if err == nil {
			return string(encoded)
		}
	}
	if preferred, ok := value.([]appconfig.PreferredTool); ok {
		encoded, err := goccyyaml.Marshal(preferred)
		if err == nil {
			return strings.TrimSpace(string(encoded))
		}
	}
	return fmt.Sprint(value)
}

func configInputError(message string) error {
	return processExitError{code: 2, err: fmt.Errorf("%s", message)}
}

func configReadError(key string, err error) error {
	if strings.HasPrefix(err.Error(), "unknown configuration key") {
		return configInputError(err.Error())
	}
	return fmt.Errorf("read %s: %w", key, err)
}
