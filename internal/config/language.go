package axconfig

import (
	"fmt"
	"os"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/internal/i18n"
)

// Language* are the canonical values of the top-level language setting. The
// alias table and the resolved language live in internal/i18n; the configuration
// layer only stores and reports the normalized setting, so `config get language`
// answers the same thing on every machine (auto included).
const (
	LanguageAuto = string(i18n.SettingAuto)
	LanguageZH   = string(i18n.SettingZH)
	LanguageEN   = string(i18n.SettingEN)
)

// ReadLanguage resolves the configured UI language to its canonical form: auto,
// zh or en. SUGA_LANGUAGE (set for one shell or by a script) wins over language:
// in config.yaml, and both are normalized through internal/i18n - the single
// alias table - so a hand-written en_US or 中文 reads back as en or zh. An absent
// setting is auto: the catalog then follows the machine's user locale.
func ReadLanguage() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("SUGA_LANGUAGE")); raw != "" {
		setting, err := i18n.NormalizeSetting(raw)
		if err != nil {
			return "", fmt.Errorf("SUGA_LANGUAGE: %w", err)
		}
		return string(setting), nil
	}
	raw := gkconfig.Get("language")
	if raw == nil {
		return LanguageAuto, nil
	}
	// A non-string leaf (language: [zh]) cannot be normalized, and silently
	// falling back to auto would hide a typo, so it fails like an unknown alias.
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("language must be a string, got %T", raw)
	}
	setting, err := i18n.NormalizeSetting(text)
	if err != nil {
		return "", fmt.Errorf("language: %w", err)
	}
	return string(setting), nil
}
