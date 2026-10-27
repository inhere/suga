package cli

import (
	"os"

	gkconfig "github.com/gookit/config/v2"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
)

// cliTranslator resolves the startup language for commands that do not assemble
// a wiring.Runtime. Catalog failures deliberately fall back so offline commands
// can still report their diagnostics instead of being blocked by UI resources.
func cliTranslator() i18n.Translator {
	configured := ""
	if value := gkconfig.Get("language"); value != nil {
		if text, ok := value.(string); ok {
			configured = text
		}
	}
	if _, err := appconfig.ReadLanguage(); err != nil {
		return i18n.Fallback()
	}
	lang, _, _, err := i18n.Resolve(os.Getenv("SUGA_LANGUAGE"), configured, nil)
	if err != nil {
		return i18n.Fallback()
	}
	catalog, err := i18n.New(lang)
	if err != nil {
		return i18n.Fallback()
	}
	return catalog
}

// Translator resolves the startup language for the process itself: commands that
// do not assemble a wiring.Runtime and cmd/suga's closing error line use it.
func Translator() i18n.Translator { return cliTranslator() }

func cliText(t i18n.Translator, key string, args ...map[string]any) string {
	if t == nil {
		t = i18n.Fallback()
	}
	return t.T(key, args...)
}
