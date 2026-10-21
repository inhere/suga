package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// Variant is one --variant directory: a config.yaml overlaid on the task's
// temp SUGA_HOME config, extra environment variables for the child suga
// processes and files copied into each task workspace before the agent runs.
// name defaults to the directory's base name.
type Variant struct {
	Name string
	Dir  string

	// Config is the parsed variant config.yaml (nil when the dir has none).
	// It is deep-merged over the freshly written config (variant wins).
	Config map[string]any
	// Env are the extra KEY=VALUE variables of the env file (nil when none).
	Env map[string]string
	// WorkDir is the variant's workspace/ directory ("" when absent).
	WorkDir string
}

// rejectEnvKeys are the variables the eval's isolation depends on; a variant
// env file may not override them, or the child would leave the temp SUGA_HOME.
var rejectEnvKeys = map[string]bool{"SUGA_HOME": true, "SUGA_CONFIG": true, "SUGA_MODEL": true, "HOME": true, "USERPROFILE": true}

// variantFlags collects repeated --variant dir flags.
type variantFlags []string

func (s *variantFlags) String() string     { return strings.Join(*s, ",") }
func (s *variantFlags) Set(v string) error { *s = append(*s, v); return nil }

// loadVariants loads every --variant directory in the given order.
func loadVariants(dirs []string) ([]*Variant, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	all := make([]*Variant, 0, len(dirs))
	for _, dir := range dirs {
		v, err := loadVariant(dir)
		if err != nil {
			return nil, err
		}
		all = append(all, v)
	}
	return all, nil
}

func loadVariant(dir string) (*Variant, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("--variant %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("--variant %s: not a directory", dir)
	}
	v := &Variant{Name: filepath.Base(abs), Dir: abs}
	if raw, err := os.ReadFile(filepath.Join(abs, "config.yaml")); err == nil {
		cfg := map[string]any{}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("--variant %s config.yaml: %w", dir, err)
		}
		if len(cfg) > 0 {
			v.Config = cfg
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("--variant %s config.yaml: %w", dir, err)
	}
	if raw, err := os.ReadFile(filepath.Join(abs, "env")); err == nil {
		v.Env, err = parseEnvFile(dir, raw)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("--variant %s env: %w", dir, err)
	}
	if fi, err := os.Stat(filepath.Join(abs, "workspace")); err == nil && fi.IsDir() {
		v.WorkDir = filepath.Join(abs, "workspace")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("--variant %s workspace: %w", dir, err)
	}
	return v, nil
}

// parseEnvFile parses KEY=VALUE lines (blank lines and #-comments skipped).
func parseEnvFile(dir string, raw []byte) (map[string]string, error) {
	env := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--variant %s env line %d: want KEY=VALUE", dir, i+1)
		}
		if rejectEnvKeys[strings.ToUpper(strings.TrimSpace(k))] {
			return nil, fmt.Errorf("--variant %s env line %d: may not override %s", dir, i+1, strings.TrimSpace(k))
		}
		env[string(strings.TrimSpace(k))] = val
	}
	if len(env) == 0 {
		return nil, nil
	}
	return env, nil
}

// applyVariantConfig overlays v.Config onto the config.yaml already written
// into home (base from --config-from or the fake provider, plus --suga-set):
// maps merge recursively, every other value is replaced (variant wins).
func applyVariantConfig(home string, v *Variant) error {
	if v == nil || v.Config == nil {
		return nil
	}
	path := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("variant config overlay: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	mergeConfig(cfg, v.Config)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// mergeConfig overlays top onto base in place: map values merge recursively,
// non-map top values replace the base ones.
func mergeConfig(base, top map[string]any) {
	for k, tv := range top {
		if tm, ok := tv.(map[string]any); ok {
			bm, _ := base[k].(map[string]any)
			if bm == nil {
				bm = map[string]any{}
				base[k] = bm
			}
			mergeConfig(bm, tm)
			continue
		}
		base[k] = tv
	}
}

// withVariantEnv appends the variant's extra variables to env (they win over
// the inherited ones); nil v changes nothing.
func withVariantEnv(env []string, v *Variant) []string {
	if v == nil || len(v.Env) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(v.Env))
	out = append(out, env...)
	for k, val := range v.Env {
		out = append(out, k+"="+val)
	}
	return out
}

// variantSet turns the loaded variants into a loop set: nil when none were
// given (a run then behaves exactly as without --variant), otherwise one
// non-nil entry per variant.
func variantSet(variants []*Variant) []*Variant {
	if len(variants) == 0 {
		return []*Variant{nil}
	}
	return variants
}

// variantName is the result.jsonl variant field of v ("" = none).
func variantName(v *Variant) string {
	if v == nil {
		return ""
	}
	return v.Name
}

// applyVariantWorkspace copies the variant's workspace/ files into ws,
// overwriting same-named files. nil v changes nothing.
func applyVariantWorkspace(ws string, v *Variant) error {
	if v == nil || v.WorkDir == "" {
		return nil
	}
	return copyDir(v.WorkDir, ws)
}
