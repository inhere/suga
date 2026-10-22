package axconfig

import (
	"fmt"

	goccyyaml "github.com/goccy/go-yaml"
	gkconfig "github.com/gookit/config/v2"
)

// ConfigValidationError marks a candidate that is syntactically valid YAML but
// violates an existing typed configuration reader. CLI callers map it to the
// input/configuration exit code instead of treating it as an I/O failure.
type ConfigValidationError struct {
	Err error
}

func (e ConfigValidationError) Error() string { return e.Err.Error() }
func (e ConfigValidationError) Unwrap() error { return e.Err }
func (e ConfigValidationError) ExitCode() int { return 2 }

// ValidateConfigCandidate loads a candidate into the same reader registry used
// at startup. The global config is deliberately reset first; callers should
// call Reload after the edit, whether validation or the write succeeds.
// allowMissingModel is used by unset model: an empty model is a valid file
// state for an edit command even though a later runtime start will reject it.
func ValidateConfigCandidate(content []byte, key string, allowMissingModel ...bool) error {
	var data map[string]any
	if err := goccyyaml.Unmarshal(content, &data); err != nil {
		return ConfigValidationError{Err: fmt.Errorf("parse candidate config: %w", err)}
	}
	gkconfig.Reset()
	if err := gkconfig.LoadData(data); err != nil {
		return ConfigValidationError{Err: fmt.Errorf("load candidate config: %w", err)}
	}
	if len(allowMissingModel) > 0 && allowMissingModel[0] && isModelSelectionKey(key) && gkconfig.Get("model") == nil {
		if err := dispatchConfigReader("providers"); err != nil {
			return ConfigValidationError{Err: err}
		}
		return nil
	}
	if err := ValidateConfigKey(key); err != nil {
		return ConfigValidationError{Err: err}
	}
	return nil
}

// Reload resets the process configuration before loading the active sources.
// This is required after a candidate validation, which temporarily replaces
// the global data with the candidate document.
func Reload() error {
	gkconfig.Reset()
	return Load()
}

func isModelSelectionKey(key string) bool {
	switch key {
	case "model", "compact_model", "fallback_models", "models.compact", "models.idle", "models.handoff":
		return true
	}
	return false
}
