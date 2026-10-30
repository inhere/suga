package i18n

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	gookiti18n "github.com/gookit/i18n"
)

// Language is a supported user-interface language.
type Language string

const (
	LanguageZH Language = "zh"
	LanguageEN Language = "en"
)

// Setting is the configured language selection mode.
type Setting string

const (
	SettingAuto Setting = "auto"
	SettingZH   Setting = "zh"
	SettingEN   Setting = "en"
)

// aliases is the single source of truth for accepted language settings.
var aliases = map[string]Setting{
	"": SettingAuto, "auto": SettingAuto, "default": SettingAuto, "system": SettingAuto,
	"zh": SettingZH, "zh_cn": SettingZH, "zh_hans": SettingZH, "zh_sg": SettingZH,
	"zh_tw": SettingZH, "zh_hk": SettingZH, "zh_hant": SettingZH,
	"cn": SettingZH, "chs": SettingZH, "chinese": SettingZH, "中文": SettingZH, "简体中文": SettingZH, "汉语": SettingZH,
	"en": SettingEN, "en_us": SettingEN, "en_gb": SettingEN, "en_au": SettingEN,
	"eng": SettingEN, "english": SettingEN, "英文": SettingEN, "英语": SettingEN,
}

func normalizeAlias(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
}

// NormalizeSetting canonicalizes a language setting or returns an error listing
// the three values that can be persisted in configuration.
func NormalizeSetting(s string) (Setting, error) {
	key := normalizeAlias(s)
	if setting, ok := aliases[key]; ok {
		return setting, nil
	}
	if strings.HasPrefix(key, "zh_") {
		return SettingZH, nil
	}
	if strings.HasPrefix(key, "en_") {
		return SettingEN, nil
	}
	return "", fmt.Errorf("invalid language setting %q: must be auto, zh or en (or a common alias)", strings.TrimSpace(s))
}

// Resolve applies environment, configuration and system-locale precedence.
func Resolve(env, configured string, sys func() string) (lang Language, setting Setting, source string, err error) {
	value := strings.TrimSpace(env)
	source = "env"
	if value == "" {
		value = strings.TrimSpace(configured)
		source = "config"
		if value == "" {
			value = ""
			source = "default"
		}
	}
	setting, err = NormalizeSetting(value)
	if err != nil {
		return "", "", source, err
	}
	if setting == SettingAuto {
		if sys == nil {
			sys = systemLocale
		}
		locale := sys()
		localeSetting, localeErr := NormalizeSetting(locale)
		if localeErr != nil || localeSetting == SettingAuto {
			lang = LanguageEN
		} else {
			lang = Language(localeSetting)
		}
	} else {
		lang = Language(setting)
	}
	return lang, setting, source, nil
}

//go:embed catalog
var catalogFS embed.FS

// WarningFunc receives a missing-catalog-key diagnostic.
type WarningFunc func(message string)

// Translator is the read-only UI translation surface passed to host renderers.
type Translator interface {
	T(key string, args ...map[string]any) string
}

// Catalog is an initialized, immutable language catalog.
type Catalog struct {
	lang    Language
	manager *gookiti18n.I18n
	warn    WarningFunc

	warnMu sync.Mutex
	warned map[string]struct{}
}

var supportedLanguages = map[string]string{
	string(LanguageEN): "English",
	string(LanguageZH): "简体中文",
}

// New creates an independent catalog backed by embedded resources.
func New(lang Language) (*Catalog, error) {
	return NewWithLogger(lang, nil)
}

// initMu serializes catalog loading (see NewWithLogger).
var initMu sync.Mutex

// NewWithLogger creates a catalog and sends missing-key diagnostics to warn.
// A nil callback uses the project's slog logger rather than writing to stderr.
func NewWithLogger(lang Language, warn WarningFunc) (catalog *Catalog, err error) {
	if lang != LanguageEN && lang != LanguageZH {
		return nil, fmt.Errorf("unsupported language %q: must be zh or en", lang)
	}
	if warn == nil {
		warn = func(message string) { slog.Warn("i18n", "message", message) }
	}
	catalog = &Catalog{lang: lang, warn: warn, warned: make(map[string]struct{})}
	defer func() {
		if recovered := recover(); recovered != nil {
			catalog = nil
			err = fmt.Errorf("initialize language catalog: %v", recovered)
		}
	}()
	// The ini parser behind the catalog loader writes a package-level token map
	// on every load: concurrent sessions of one process (the SDK) must load one
	// at a time (suga-g866).
	initMu.Lock()
	defer initMu.Unlock()
	manager := gookiti18n.NewWithFS(catalogFS, "catalog", string(lang), supportedLanguages)
	manager.LoadMode = gookiti18n.DirMode
	manager.TransMode = gookiti18n.ReplaceMode
	manager.FallbackLang = string(LanguageEN)
	manager.Init()
	catalog.manager = manager
	return catalog, nil
}

// T translates key with named {placeholders}; literal backslash-n is a newline.
func (c *Catalog) T(key string, args ...map[string]any) string {
	if c == nil || c.manager == nil || !c.manager.HasLang(string(c.lang)) {
		return key
	}
	if !c.manager.HasKey(string(c.lang), key) {
		c.warnMissing(key)
	}
	var value string
	if len(args) > 0 {
		value = c.manager.Tr(string(c.lang), key, args[0])
	} else {
		value = c.manager.Tr(string(c.lang), key)
	}
	return strings.ReplaceAll(value, `\n`, "\n")
}

// Fallback returns the legacy Chinese catalog for components that have not yet
// been wired with the startup-resolved translator. Real process assembly must
// always inject the resolved catalog instead.
func Fallback() Translator {
	fallbackOnce.Do(func() {
		fallbackCatalog, fallbackErr = NewWithLogger(LanguageZH, nil)
	})
	if fallbackErr != nil {
		// Embedded resources are part of the binary; this is only a compatibility
		// path for old tests and callers, so preserve the historical key behavior.
		return &Catalog{lang: LanguageZH}
	}
	return fallbackCatalog
}

var (
	fallbackOnce    sync.Once
	fallbackCatalog *Catalog
	fallbackErr     error
)

// SystemLocale returns the raw locale used by auto resolution.
func SystemLocale() string { return systemLocale() }

// warnMissing logs a missing key once per catalog.
func (c *Catalog) warnMissing(key string) {
	c.warnMu.Lock()
	defer c.warnMu.Unlock()
	if _, ok := c.warned[key]; ok {
		return
	}
	c.warned[key] = struct{}{}
	c.warn(fmt.Sprintf("i18n: missing translation key %q for language %q; falling back to English", key, c.lang))
}
