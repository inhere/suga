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
func newConfigCommand(stdin io.Reader, stdout, stderr io.Writer) *capp.Cmd {
	config := capp.NewCmd("config", "Read and maintain suga configuration")
	config.LongHelp = `Configuration commands do not modify environment variables.
	Use "suga config path" to see the active home, config, and env paths.`
	config.SetOutput(stderr)
	config.Add(
		newConfigLeaf(stdin, stdout, stderr, "get", "Show an effective configuration value", runConfigGet, "key"),
		newConfigLeaf(stdin, stdout, stderr, "set", "Set a writable configuration value", runConfigSet, "key", "value"),
		newConfigLeaf(stdin, stdout, stderr, "unset", "Remove a writable configuration value", runConfigUnset, "key"),
		newConfigLeaf(stdin, stdout, stderr, "list", "List effective configuration values", runConfigList),
		newConfigLeaf(stdin, stdout, stderr, "path", "Show configuration and data paths", runConfigPath),
	)
	return config
}

// configLeafHandler receives stdin because `config set/unset` has to tell a
// human at a terminal from a command the model ran through a shell tool.
type configLeafHandler func(in io.Reader, out io.Writer, c *capp.Cmd) error

func newConfigLeaf(stdin io.Reader, stdout, stderr io.Writer, name, description string, handler configLeafHandler, args ...string) *capp.Cmd {
	cmd := capp.NewCmd(name, description, func(c *capp.Cmd) error {
		return handler(stdin, stdout, c)
	})
	cmd.SetOutput(stderr)
	for _, arg := range args {
		cmd.AddArg(arg, "the configuration "+arg, true)
	}
	return cmd
}

func runConfigGet(in io.Reader, out io.Writer, c *capp.Cmd) error {
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

func runConfigSet(in io.Reader, out io.Writer, c *capp.Cmd) error {
	tr := cliTranslator()
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config set <key> <value>")
	}
	key := strings.TrimSpace(c.Arg("key").String())
	descriptor, err := writableConfigKey(key)
	if err != nil {
		return err
	}
	path, err := activeConfigPath()
	if err != nil {
		return err
	}
	// Refuse a protected key before parsing its value: the answer must be the
	// protected-key refusal, whatever the value looks like.
	if err := protectedWriteAllowed(descriptor, path, in); err != nil {
		return err
	}
	raw := c.Arg("value").String()
	value, err := appconfig.ParseConfigValue(key, raw)
	if err != nil {
		return configInputError(err.Error())
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

func runConfigUnset(in io.Reader, out io.Writer, c *capp.Cmd) error {
	tr := cliTranslator()
	if len(c.RemainArgs()) != 0 {
		return configInputError("usage: suga config unset <key>")
	}
	key := strings.TrimSpace(c.Arg("key").String())
	descriptor, err := writableConfigKey(key)
	if err != nil {
		return err
	}
	path, err := activeConfigPath()
	if err != nil {
		return err
	}
	if err := protectedWriteAllowed(descriptor, path, in); err != nil {
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

func runConfigList(in io.Reader, out io.Writer, c *capp.Cmd) error {
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

func runConfigPath(in io.Reader, out io.Writer, c *capp.Cmd) error {
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

// configStdinIsTerminal is isTerminalInput in production; tests replace it to
// drive both answers without a real terminal.
var configStdinIsTerminal = isTerminalInput

// protectedWriteAllowed decides whether `config set/unset` may write key.
// Protected keys widen what the model may execute or read/write, so they are
// only writable by a human at a terminal outside suga: inside a session
// (SUGA_SESSION_ID set) or with a piped stdin (the model running the command
// through a shell tool) the write is refused and nothing is changed.
func protectedWriteAllowed(key appconfig.ConfigKey, path string, stdin io.Reader) error {
	if !key.Protected {
		return nil
	}
	if strings.TrimSpace(os.Getenv("SUGA_SESSION_ID")) == "" && configStdinIsTerminal(stdin) {
		return nil
	}
	message := cliText(cliTranslator(), "cli.config.protected_refused", map[string]any{"key": key.Pattern, "path": path})
	return configInputError(message)
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
	formatted := formatConfigValue(value.Value)
	// hooks.commands is a list of mappings: the default value formatting would
	// print the Go representation, so the config command renders it as the
	// single-line flow YAML that `config set` accepts again.
	if value.Key == "hooks.commands" {
		formatted = formatHookEntries(value.Value)
	}
	if _, err := fmt.Fprintf(out, "%s = %s\nsource = %s\n", value.Key, formatted, formatConfigSource(value)); err != nil {
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

// formatHookEntries renders hooks.commands as one line of flow-style YAML, so
// the printed value is the same shape `config set` accepts. An unset or
// unencodable value falls back to formatConfigValue, which prints <unset> for a
// missing value like every other key.
func formatHookEntries(value any) string {
	if value == nil {
		return formatConfigValue(value)
	}
	encoded, err := goccyyaml.MarshalWithOptions(value, goccyyaml.Flow(true))
	if err != nil {
		return formatConfigValue(value)
	}
	text := strings.TrimSpace(string(encoded))
	if text == "" || text == "null" {
		return formatConfigValue(value)
	}
	return text
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
