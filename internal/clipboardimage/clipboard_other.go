//go:build !windows && !darwin && !linux

package clipboardimage

import (
	"errors"

	"github.com/inhere/suga/internal/i18n"
)

// platformReader on an OS without a clipboard reader: chat says so instead of
// failing. tr renders the reason.
func platformReader(tr i18n.Translator) ImageReader {
	return func() ([]byte, string, error) {
		return nil, "", errors.New(trOrFallback(tr).T("host.clipboard.unsupported_os"))
	}
}
