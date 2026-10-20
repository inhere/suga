package axconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestWriteConfigValuePreservesASTDetails(t *testing.T) {
	cases := []struct {
		name      string
		lineBreak string
	}{
		{name: "LF", lineBreak: "\n"},
		{name: "CRLF", lineBreak: "\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			source := strings.Join([]string{
				"# keep this heading",
				"model: old # keep model comment",
				"providers:",
				"  gateway: # keep provider comment",
				"    type: openai",
				"    models:",
				"      gpt-4.1:",
				"        model_id: old-model",
				"        context_window: 128000 # keep context comment",
				"last: true",
				"",
			}, tc.lineBreak)
			assert.NoErr(t, os.WriteFile(path, []byte(source), 0o600))

			_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{})
			assert.NoErr(t, err)
			_, err = WriteConfigValue(path, "providers.gateway.models.gpt-4.1.context_window", 256000, ConfigWriteOptions{})
			assert.NoErr(t, err)
			_, err = WriteConfigValue(path, "fallback_models", []string{"gateway/new", "openai/backup"}, ConfigWriteOptions{})
			assert.NoErr(t, err)

			got, err := os.ReadFile(path)
			assert.Require(t, assert.NoErr(t, err))
			text := string(got)
			assert.StrContains(t, text, "# keep this heading")
			assert.StrContains(t, text, "model: new # keep model comment")
			assert.StrContains(t, text, "# keep provider comment")
			assert.StrContains(t, text, "# keep context comment")
			assert.StrContains(t, text, "context_window: 256000")
			assert.StrContains(t, text, "fallback_models:")
			assert.True(t, strings.Index(text, "model: new") < strings.Index(text, "providers:"))
			// No blank line may appear: a CRLF comment kept its \r and came back as
			// "# comment\r\r\n", i.e. a comment line followed by an empty line.
			assert.False(t, strings.Contains(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n"), "a blank line was added:\n"+text)
			assert.False(t, strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\r"), "a stray \\r was left")
			if tc.lineBreak == "\r\n" {
				assert.True(t, strings.Contains(text, "\r\n"))
				assert.False(t, strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n"))
			} else {
				assert.False(t, strings.Contains(text, "\r\n"))
			}
		})
	}
}

func TestUnsetConfigValuePreservesCommentsAndCleansEmptyMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	source := "# model documentation\nmodel: old # inline model\nproviders:\n  gateway: # gateway documentation\n    type: openai\nkeep: true\n"
	assert.NoErr(t, os.WriteFile(path, []byte(source), 0o600))

	_, err := UnsetConfigValue(path, "providers.gateway.type", ConfigWriteOptions{})
	assert.NoErr(t, err)
	got, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	text := string(got)
	assert.NotContains(t, text, "providers:")
	assert.NotContains(t, text, "gateway:")
	assert.StrContains(t, text, "# gateway documentation")
	assert.StrContains(t, text, "# model documentation")
	assert.StrContains(t, text, "keep: true")

	_, err = UnsetConfigValue(path, "model", ConfigWriteOptions{})
	assert.NoErr(t, err)
	got, err = os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	assert.NotContains(t, string(got), "model:")
	assert.StrContains(t, string(got), "# model documentation")
}

func TestConfigWriterValidationFailureLeavesOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# user file\nmodel: old\n")
	assert.NoErr(t, os.WriteFile(path, original, 0o600))

	_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{
		Validate: func([]byte) error { return errors.New("invalid model") },
	})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "invalid model")
	got, readErr := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, readErr))
	assert.Eq(t, string(original), string(got))
	_, statErr := os.Stat(path + ".bak")
	assert.True(t, errors.Is(statErr, os.ErrNotExist))
}

func TestConfigWriterPostWriteValidationRestoresOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("model: old\n")
	assert.NoErr(t, os.WriteFile(path, original, 0o600))
	calls := 0
	_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{
		Validate: func([]byte) error {
			calls++
			if calls == 2 {
				return errors.New("read-back validation failed")
			}
			return nil
		},
	})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "read-back validation failed")
	got, readErr := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, readErr))
	assert.Eq(t, string(original), string(got))
}

func TestConfigWriterRenameFailureLeavesOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("model: old\n")
	assert.NoErr(t, os.WriteFile(path, original, 0o600))
	oldReplace := configReplaceFile
	calls := 0
	configReplaceFile = func(source, target string) error {
		calls++
		if calls == 2 {
			return errors.New("injected rename failure")
		}
		return oldReplace(source, target)
	}
	t.Cleanup(func() { configReplaceFile = oldReplace })

	_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "injected rename failure")
	got, readErr := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, readErr))
	assert.Eq(t, string(original), string(got))
}

func TestConfigWriterSyncFailureRestoresOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("model: old\n")
	assert.NoErr(t, os.WriteFile(path, original, 0o600))
	oldSync := configSyncDirectory
	calls := 0
	configSyncDirectory = func(string) error {
		calls++
		if calls == 2 {
			return errors.New("injected directory sync failure")
		}
		return oldSync(filepath.Dir(path))
	}
	t.Cleanup(func() { configSyncDirectory = oldSync })

	_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "injected directory sync failure")
	got, readErr := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, readErr))
	assert.Eq(t, string(original), string(got))
}

func TestConfigWriterRejectsDuplicateKeysAndAnchors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "duplicate", body: "model: old\nmodel: duplicate\n"},
		{name: "anchor", body: "defaults: &defaults\n  model: old\nmodel: *defaults\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			original := []byte(tc.body)
			assert.NoErr(t, os.WriteFile(path, original, 0o600))
			_, err := WriteConfigValue(path, "model", "new", ConfigWriteOptions{})
			assert.Err(t, err)
			got, readErr := os.ReadFile(path)
			assert.Require(t, assert.NoErr(t, readErr))
			assert.Eq(t, string(original), string(got))
		})
	}
}

func TestConfigWriterKeepsOneRecentBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	assert.NoErr(t, os.WriteFile(path, []byte("model: first\n"), 0o600))
	result, err := WriteConfigValue(path, "model", "second", ConfigWriteOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, path+".bak", result.Backup)
	result, err = WriteConfigValue(path, "model", "third", ConfigWriteOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, path+".bak", result.Backup)
	backup, err := os.ReadFile(path + ".bak")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "model: second\n", string(backup))
	_, err = os.Stat(path + ".bak.bak")
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

// TestWriteConfigValueCreatesMissingParentsBlockStyle: a key whose parents are
// missing is written as an indented block, nested deeper than one level too,
// and a later key under the same parent joins that block.
func TestWriteConfigValueCreatesMissingParentsBlockStyle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	assert.NoErr(t, os.WriteFile(path, []byte("# keep me\nmodel: deepseek/m\n"), 0o600))
	_, err := WriteConfigValue(path, "tools.snapshots.keep_days", 3, ConfigWriteOptions{})
	assert.NoErr(t, err)
	_, err = WriteConfigValue(path, "tools.allow_write", true, ConfigWriteOptions{})
	assert.NoErr(t, err)
	data, err := os.ReadFile(path)
	assert.NoErr(t, err)
	text := string(data)
	assert.StrContains(t, text, "# keep me\nmodel: deepseek/m\n")
	assert.StrContains(t, text, "tools:\n  snapshots:\n    keep_days: 3\n  allow_write: true")
	assert.NotContains(t, text, "{")
}
