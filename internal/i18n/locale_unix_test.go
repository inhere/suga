//go:build !windows

package i18n

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestSystemLocalePrecedence(t *testing.T) {
	t.Setenv("LC_ALL", "zh-CN")
	t.Setenv("LC_MESSAGES", "en-US")
	t.Setenv("LANG", "en-US")
	assert.Eq(t, "zh-CN", systemLocale())
	t.Setenv("LC_ALL", "")
	assert.Eq(t, "en-US", systemLocale())
	t.Setenv("LC_MESSAGES", "")
	assert.Eq(t, "en-US", systemLocale())
	t.Setenv("LANG", "")
	assert.Eq(t, "", systemLocale())
}
