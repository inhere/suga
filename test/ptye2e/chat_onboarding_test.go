//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestChatOnboarding ports scripts/ptysmoke/chat_onboarding.py: the first-run
// onboarding of the CLI and the offline config/model commands.
//
// The Python checks and their Go assertions (every CLI run is a direct exec of
// the built binary, so no pty is involved):
//
//	fresh: version / --help are pure queries             sugaRun(version|--help) exits 0, no SUGA_HOME created
//	init: writes both files, says so                     stdout 已写入 / stderr 已初始化 suga 配置, config.yaml and .env exist
//	init: private bits                                  home 0700 and both files 0600 (POSIX; Windows uses ACLs)
//	init: the safe model and a comment-only .env        config holds model: deepseek/deepseek-chat, no value line in .env
//	init again: files kept                              stdout 未覆盖, both files byte-identical
//	init over user files: kept                          stdout 未覆盖, both files unchanged
//	init --force: backs up only config                  stdout 已备份+已写入, one config.yaml.bak.* equal to the old file,
//	                                                    config replaced, .env untouched
//	explicit SUGA_CONFIG: refused                       exit 2, stderr SUGA_CONFIG is set, no home config, file untouched,
//	                                                    the explicit home still gets the comment-only .env
//	explicit SUGA_CONFIG: config path                   config = <external> / source = explicit
//	config path: home/explicit leaves                   home = <home> / config = <home>/config.yaml / source = home
//	config set: a secret is refused and never echoed    exit 2, stderr secret, the value appears in neither stream, file unchanged
//	config set/get/list/unset: chat.markdown           已写入 / chat.markdown = false / source = config: and back to unset
//	models: offline projection                          Providers (offline), fake/unlisted, No network request was made.
//	models --json                                       offline=true, switchable={fake/compact,fake/m1,fake/unlisted}, source=config
func TestChatOnboarding(t *testing.T) {
	binary := buildSuga(t)
	t.Run("onboarding", func(t *testing.T) { onboardingScenario(t, binary) })
	t.Run("config and models", func(t *testing.T) { configModelsScenario(t, binary) })
}

// onboardingEnv is the environment of the onboarding runs: an isolated SUGA_HOME
// without an inherited SUGA_CONFIG (the explicit case sets it), the checked words
// in Chinese and hooks off.
func onboardingEnv(t *testing.T, home, config string) []string {
	t.Helper()
	env := testEnv(home)
	if config != "" {
		env = append(env, "SUGA_CONFIG="+config)
	}
	return env
}

// onboardingScenario runs fresh, repeated, forced and explicit-source init.
func onboardingScenario(t *testing.T, binary string) {
	work := t.TempDir()
	home, ws := filepath.Join(work, "home"), filepath.Join(work, "workspace")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	env := onboardingEnv(t, home, "")

	// version and --help are pure queries: they must not create the home.
	for _, arg := range []string{"version", "--help"} {
		_, _, code := sugaRun(t, binary, ws, env, arg)
		check(t, "fresh: "+arg+" exits 0", code == 0)
		_, err := os.Stat(home)
		check(t, "fresh: "+arg+" creates no SUGA_HOME", os.IsNotExist(err))
	}

	stdout, stderr, _ := sugaRun(t, binary, ws, env, "init")
	check(t, "init: stdout", strings.Contains(stdout, "已写入"))
	check(t, "init: stderr", strings.Contains(stderr, "已初始化 suga 配置"))
	config, dotenv := filepath.Join(home, "config.yaml"), filepath.Join(home, ".env")
	configBody, configErr := os.ReadFile(config)
	envBody, envErr := os.ReadFile(dotenv)
	if !check2(t, "init: config.yaml and .env written", configErr == nil && envErr == nil) {
		return
	}
	checkFileMode(t, "init: home is 0700", home, 0o700)
	checkFileMode(t, "init: config.yaml is 0600", config, 0o600)
	checkFileMode(t, "init: .env is 0600", dotenv, 0o600)
	check(t, "init: config carries the safe model", strings.Contains(string(configBody), "model: deepseek/deepseek-chat"))
	check(t, "init: .env has no value line", commentOnly(t, string(envBody)))

	// A repeated init keeps both files byte for byte.
	stdout, _, _ = sugaRun(t, binary, ws, env, "init")
	check(t, "init again: not overwritten", strings.Contains(stdout, "未覆盖"))
	check(t, "init again: files unchanged", unchanged(t, config, configBody) && unchanged(t, dotenv, envBody))

	// User-owned files: kept without --force, backed up and replaced with it.
	oldConfig := []byte("# user-owned config\nmodel: custom/old\n")
	oldEnv := []byte("PRIVATE_API_KEY=keep-me\n")
	if err := os.WriteFile(config, oldConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dotenv, oldEnv, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ = sugaRun(t, binary, ws, env, "init")
	check(t, "init over user files: not overwritten", strings.Contains(stdout, "未覆盖"))
	check(t, "init over user files: kept", unchanged(t, config, oldConfig) && unchanged(t, dotenv, oldEnv))

	stdout, _, _ = sugaRun(t, binary, ws, env, "init", "--force")
	check(t, "init --force: backup announced", strings.Contains(stdout, "已备份") && strings.Contains(stdout, "已写入"))
	backups, _ := filepath.Glob(config + ".bak.*")
	check(t, "init --force: one timestamped config backup", len(backups) == 1)
	if len(backups) == 1 {
		check(t, "init --force: the backup is the old config", unchanged(t, backups[0], oldConfig))
	}
	check(t, "init --force: config replaced, .env untouched",
		!unchanged(t, config, oldConfig) && unchanged(t, dotenv, oldEnv))

	// An explicit SUGA_CONFIG is never overwritten; that home still gets .env.
	external := filepath.Join(work, "external.yaml")
	const externalBody = "model: explicit/provider\n"
	if err := os.WriteFile(external, []byte(externalBody), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitHome := filepath.Join(work, "explicit-home")
	explicitEnv := onboardingEnv(t, explicitHome, external)
	_, stderr, code := sugaRun(t, binary, ws, explicitEnv, "init", "--force")
	check(t, "explicit SUGA_CONFIG: refused with exit 2", code == 2)
	check(t, "explicit SUGA_CONFIG: the refusal names it", strings.Contains(stderr, "SUGA_CONFIG is set"))
	if _, err := os.Stat(filepath.Join(explicitHome, "config.yaml")); err == nil {
		t.Errorf("FAIL explicit SUGA_CONFIG: no home config expected")
	}
	check(t, "explicit SUGA_CONFIG: the external file is untouched", unchanged(t, external, []byte(externalBody)))
	explicitDotenv, err := os.ReadFile(filepath.Join(explicitHome, ".env"))
	check(t, "explicit SUGA_CONFIG: .env created comment-only", err == nil && commentOnly(t, string(explicitDotenv)))
	stdout, _, _ = sugaRun(t, binary, ws, explicitEnv, "config", "path")
	check(t, "explicit SUGA_CONFIG: config path", containsPath(stdout, "config = ", external) &&
		strings.Contains(stdout, "source = explicit"))
}

// configModelsScenario exercises real config leaves and the offline model
// projection against a closed localhost endpoint: no request may be made.
func configModelsScenario(t *testing.T, binary string) {
	work := t.TempDir()
	home, ws := filepath.Join(work, "config-home"), filepath.Join(work, "config-workspace")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	env := onboardingEnv(t, home, "")
	if stdout, _, _ := sugaRun(t, binary, ws, env, "init"); !strings.Contains(stdout, "已写入") {
		t.Fatalf("init did not write the home: %q", stdout)
	}

	fixture := "# keep this comment\nmodel: fake/m1\ncompact_model: fake/compact\nfallback_models:\n  - fake/unlisted\n" +
		"providers:\n  fake:\n    type: openai\n    base_url: http://127.0.0.1:1/v1\n    models:\n      compact:\n" +
		"        context_window: 64000\n      m1:\n        model_id: upstream-m1\n        context_window: 128000\n"
	config := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(config, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ := sugaRun(t, binary, ws, env, "config", "path")
	check(t, "config path: home and config", containsPath(stdout, "home = ", home) && containsPath(stdout, "config = ", config))
	check(t, "config path: source = home", strings.Contains(stdout, "source = home"))

	// A secret value is refused, never echoed and never written.
	stdout, stderr, code := sugaRun(t, binary, ws, env, "config", "set", "providers.fake.api_key", "do-not-write")
	check(t, "config set: a secret is refused", code == 2 && strings.Contains(stderr, "secret"))
	check(t, "config set: the secret is not echoed", !strings.Contains(stdout, "do-not-write") && !strings.Contains(stderr, "do-not-write"))
	check(t, "config set: the file is untouched", unchanged(t, config, []byte(fixture)))

	// A non-protected display key round-trips; the file's comments survive.
	stdout, _, _ = sugaRun(t, binary, ws, env, "config", "set", "chat.markdown", "false")
	check(t, "config set: chat.markdown written", strings.Contains(stdout, "已写入") &&
		strings.Contains(stdout, "chat.markdown = false") && strings.Contains(stdout, "source = config:"))
	body, _ := os.ReadFile(config)
	check(t, "config set: the fixture comment is kept", strings.Contains(string(body), "# keep this comment"))

	stdout, _, _ = sugaRun(t, binary, ws, env, "config", "get", "chat.markdown")
	check(t, "config get: chat.markdown = false", strings.Contains(stdout, "chat.markdown = false") &&
		strings.Contains(stdout, "source = config:"))
	stdout, _, _ = sugaRun(t, binary, ws, env, "config", "list")
	check(t, "config list: model and chat.markdown", strings.Contains(stdout, "model = fake/m1") &&
		strings.Contains(stdout, "chat.markdown = false"))
	stdout, _, _ = sugaRun(t, binary, ws, env, "config", "unset", "chat.markdown")
	check(t, "config unset: chat.markdown removed", strings.Contains(stdout, "已删除 chat.markdown"))

	// models is a projection of the config: it builds no provider and asks nobody.
	stdout, _, _ = sugaRun(t, binary, ws, env, "models")
	check(t, "models: offline listing", strings.Contains(stdout, "Providers (offline)") &&
		strings.Contains(stdout, "fake/unlisted") && strings.Contains(stdout, "No network request was made."))
	stdout, _, _ = sugaRun(t, binary, ws, env, "models", "--json")
	var catalog struct {
		Offline    bool     `json:"offline"`
		Switchable []string `json:"switchable"`
		Source     string   `json:"source"`
	}
	if err := json.Unmarshal([]byte(stdout), &catalog); err != nil {
		t.Fatalf("models --json is not JSON: %v: %q", err, stdout)
	}
	check(t, "models --json: offline=true", catalog.Offline)
	switchable := map[string]bool{}
	for _, id := range catalog.Switchable {
		switchable[id] = true
	}
	check(t, "models --json: switchable set",
		len(catalog.Switchable) == 3 && switchable["fake/compact"] && switchable["fake/m1"] && switchable["fake/unlisted"])
	check(t, "models --json: source=config", catalog.Source == "config")
}

// checkFileMode asserts a file's POSIX permission bits; Windows expresses the
// same through ACLs, so the check is skipped there (the Python did the same).
func checkFileMode(t *testing.T, name, path string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	check(t, name, info.Mode().Perm() == mode)
}

// commentOnly reports whether every non-empty line of the env template is a
// comment (the generated file must never carry a value).
func commentOnly(t *testing.T, body string) bool {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			t.Logf("generated .env value line: %q", line)
			return false
		}
	}
	return true
}

// unchanged reports whether path still holds want.
func unchanged(t *testing.T, path string, want []byte) bool {
	t.Helper()
	got, err := os.ReadFile(path)
	return err == nil && string(got) == string(want)
}

// containsPath reports whether out holds prefix followed by p, in the form the
// test built it or with symlinks and Windows short names resolved (pathForms).
func containsPath(out, prefix, p string) bool {
	for _, form := range pathForms(p) {
		if strings.Contains(out, prefix+form) {
			return true
		}
	}
	return false
}
