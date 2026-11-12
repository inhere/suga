package chat

import (
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
)

// protectedBannerLines is the banner line of the protected settings notice: the
// config file changed since the user last confirmed it, and this chat start is
// where they see it. The text itself comes from the config package, so the
// banner and run's stderr line word the same fact the same way.
func protectedBannerLines(tr i18n.Translator, change appconfig.ProtectedChange, path string) []LayoutLine {
	if change.Empty() {
		return nil
	}
	return []LayoutLine{{Text: appconfig.ProtectedChangeText(tr, change, path)}}
}
