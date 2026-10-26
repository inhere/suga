package i18n

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/gookit/goutil/x/assert"
)

func TestFallbackIsChinese(t *testing.T) {
	tr := Fallback()
	assert.Eq(t, "你好 Alice", tr.T("common.language.greeting", map[string]any{"name": "Alice"}))
}
func TestNormalizeSetting(t *testing.T) {
	cases := map[string]Setting{
		"zh": SettingZH, "ZH-cn": SettingZH, " zh_CN ": SettingZH,
		"zh-Hans": SettingZH, "cn": SettingZH, "chs": SettingZH,
		"chinese": SettingZH, "中文": SettingZH, "简体中文": SettingZH, "汉语": SettingZH,
		"en": SettingEN, "EN-us": SettingEN, "en_GB": SettingEN,
		"eng": SettingEN, "english": SettingEN, "英文": SettingEN, "英语": SettingEN,
		"": SettingAuto, " auto ": SettingAuto, "DEFAULT": SettingAuto, "system": SettingAuto,
	}
	for input, want := range cases {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			got, err := NormalizeSetting(input)
			assert.NoErr(t, err)
			assert.Eq(t, want, got)
		})
	}
	t.Run("invalid", func(t *testing.T) {
		_, err := NormalizeSetting("fr-FR")
		assert.ErrMsgContains(t, err, "auto, zh or en")
	})
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name, env, configured, locale string
		wantLang                      Language
		wantSet                       Setting
		wantSrc                       string
	}{
		{"environment wins", "en_US", "zh", "zh-CN", LanguageEN, SettingEN, "env"},
		{"config wins over auto", "", "zh-Hant", "en-US", LanguageZH, SettingZH, "config"},
		{"auto uses system locale", "", "auto", "zh-CN", LanguageZH, SettingAuto, "config"},
		{"default uses system locale", "", "", "en-US", LanguageEN, SettingAuto, "default"},
		{"unknown locale is English", "", "auto", "fr-FR", LanguageEN, SettingAuto, "config"},
		{"C locale is English", "", "auto", "C", LanguageEN, SettingAuto, "config"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			lang, setting, source, err := Resolve(tc.env, tc.configured, func() string { return tc.locale })
			assert.NoErr(t, err)
			assert.Eq(t, tc.wantLang, lang)
			assert.Eq(t, tc.wantSet, setting)
			assert.Eq(t, tc.wantSrc, source)
		})
	}
	t.Run("invalid environment is reported", func(t *testing.T) {
		_, _, source, err := Resolve("fr", "zh", func() string { return "zh-CN" })
		assert.Eq(t, "env", source)
		assert.ErrMsgContains(t, err, "invalid language setting")
	})
	t.Run("explicit language does not inspect locale", func(t *testing.T) {
		called := false
		lang, _, _, err := Resolve("en", "", func() string { called = true; return "zh-CN" })
		assert.NoErr(t, err)
		assert.Eq(t, LanguageEN, lang)
		assert.False(t, called)
	})
}

var placeholderPattern = regexp.MustCompile(`\{[^{}]+\}`)

type catalogFile struct {
	file  string
	keys  map[string]string
	value map[string]string
}

func readCatalogFiles(fsys fs.FS, dir string) ([]catalogFile, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	files := make([]catalogFile, 0, len(entries))
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ini") {
			continue
		}
		path := dir + "/" + entry.Name()
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, err
		}
		currentSection := ""
		file := catalogFile{file: entry.Name(), keys: make(map[string]string), value: make(map[string]string)}
		for lineNo, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				currentSection = strings.TrimSpace(line[1 : len(line)-1])
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 || currentSection == "" {
				return nil, fmt.Errorf("%s:%d: invalid catalog entry", path, lineNo+1)
			}
			key := currentSection + "." + strings.TrimSpace(parts[0])
			if previous, exists := seen[key]; exists {
				return nil, fmt.Errorf("duplicate catalog key %q in %s and %s", key, previous, path)
			}
			seen[key] = path
			value := strings.TrimSpace(parts[1])
			file.keys[key] = value
			file.value[key] = value
		}
		files = append(files, file)
	}
	return files, nil
}

func TestCatalogParity(t *testing.T) {
	en, err := readCatalogFiles(catalogFS, "catalog/en")
	assert.Require(t, assert.NoErr(t, err))
	zh, err := readCatalogFiles(catalogFS, "catalog/zh")
	assert.Require(t, assert.NoErr(t, err))
	files := func(values []catalogFile) map[string]map[string]string {
		result := make(map[string]map[string]string)
		for _, file := range values {
			result[file.file] = file.keys
		}
		return result
	}
	enFiles := files(en)
	zhFiles := files(zh)
	assert.Eq(t, len(enFiles), len(zhFiles))
	for file, enKeys := range enFiles {
		zhKeys, ok := zhFiles[file]
		assert.True(t, ok)
		var enNames, zhNames []string
		for key := range enKeys {
			enNames = append(enNames, key)
		}
		for key := range zhKeys {
			zhNames = append(zhNames, key)
		}
		sort.Strings(enNames)
		sort.Strings(zhNames)
		assert.Eq(t, enNames, zhNames)
		for key, enValue := range enKeys {
			zhValue := zhKeys[key]
			assert.NotContains(t, enValue, "${")
			assert.NotContains(t, zhValue, "${")
			assert.Eq(t, placeholderPattern.FindAllString(enValue, -1), placeholderPattern.FindAllString(zhValue, -1))
		}
	}
}

func TestCatalogDuplicateKeys(t *testing.T) {
	files := fstest.MapFS{
		"catalog/en/a.ini": &fstest.MapFile{Data: []byte("[common]\nkey = a\n")},
		"catalog/en/b.ini": &fstest.MapFile{Data: []byte("[common]\nkey = b\n")},
	}
	_, err := readCatalogFiles(files, "catalog/en")
	assert.ErrMsgContains(t, err, "duplicate catalog key")
}

func TestCatalogTranslationAndFallback(t *testing.T) {
	var mu sync.Mutex
	warnings := make([]string, 0)
	catalog, err := NewWithLogger(LanguageZH, func(message string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, message)
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "你好 suga", catalog.T("common.language.greeting", map[string]any{"name": "suga"}))
	assert.Eq(t, "第一行\n第二行", catalog.T("common.multiline"))

	catalog.manager.Lang("zh").Delete("common.language.greeting")
	assert.Eq(t, "Hello suga", catalog.T("common.language.greeting", map[string]any{"name": "suga"}))
	assert.Eq(t, "Hello suga", catalog.T("common.language.greeting", map[string]any{"name": "suga"}))
	mu.Lock()
	assert.Eq(t, 1, len(warnings))
	mu.Unlock()

	enCatalog, err := NewWithLogger(LanguageEN, func(message string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, message)
	})
	assert.Require(t, assert.NoErr(t, err))
	enCatalog.manager.Lang("en").Delete("common.language.greeting")
	assert.Eq(t, "common.language.greeting", enCatalog.T("common.language.greeting"))
}

func TestCatalogRejectsUnsupportedLanguage(t *testing.T) {
	catalog, err := New(Language("fr"))
	assert.Nil(t, catalog)
	assert.ErrMsgContains(t, err, "unsupported language")
}

func TestCatalogConcurrentReads(t *testing.T) {
	catalog, err := New(LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = catalog.T("common.language.greeting", map[string]any{"name": "suga"})
			}
		}()
	}
	wg.Wait()
	assert.Eq(t, "Hello suga", catalog.T("common.language.greeting", map[string]any{"name": "suga"}))
}

func TestCatalogManagerIsIndependent(t *testing.T) {
	first, err := New(LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	second, err := New(LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	first.manager.Lang("en").Delete("common.language")
	assert.Eq(t, "Language", second.T("common.language"))
}

func TestCatalogFilesHaveMatchingNames(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		files, err := readCatalogFiles(catalogFS, "catalog/"+lang)
		assert.NoErr(t, err)
		var names []string
		for _, file := range files {
			names = append(names, file.file)
		}
		sort.Strings(names)
		assert.Eq(t, []string{"approval.ini", "common.ini"}, names)
	}
}

func TestCatalogFSIsEmbedded(t *testing.T) {
	_, err := fs.ReadFile(catalogFS, "catalog/en/common.ini")
	assert.NoErr(t, err)
}
