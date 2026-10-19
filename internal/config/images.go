package axconfig

import (
	"fmt"

	gkconfig "github.com/gookit/config/v2"
)

// Image input limits (design 0.3, "历史裁剪与成本" and "安全上限"): the byte
// budget of one request, how many pictures it may carry, how much history keeps
// its pictures, and the long side view_image scales to.
const (
	// DefaultImageHistoryTurns is how many recent assistant steps keep their
	// pictures whole when history folding is off (images.history_turns).
	DefaultImageHistoryTurns = 3
	// DefaultImageRequestCount is the number of pictures one request may carry
	// (images.max_request_count).
	DefaultImageRequestCount = 8
	// DefaultImageRequestBytes is the encoded-size budget of the pictures of one
	// request (images.max_request_bytes), below the ~32MB a single Claude request
	// allows.
	DefaultImageRequestBytes = 20 << 20
	// DefaultImageMaxSide is the long side view_image scales a picture down to
	// (images.max_side).
	DefaultImageMaxSide = 1568
)

// ImageSettings are the effective images.* limits.
type ImageSettings struct {
	HistoryTurns    int
	MaxRequestCount int
	MaxRequestBytes int
	MaxSide         int
}

// ReadImageSettings reads the images.* section. Every key falls back to its
// built-in default; a non-positive value is a startup error instead of being
// guessed (a cap of 0 would silently drop every picture).
func ReadImageSettings() (ImageSettings, error) {
	settings := ImageSettings{
		HistoryTurns:    gkconfig.Int("images.history_turns", DefaultImageHistoryTurns),
		MaxRequestCount: gkconfig.Int("images.max_request_count", DefaultImageRequestCount),
		MaxRequestBytes: gkconfig.Int("images.max_request_bytes", DefaultImageRequestBytes),
		MaxSide:         gkconfig.Int("images.max_side", DefaultImageMaxSide),
	}
	for _, field := range []struct {
		key   string
		value int
	}{
		{"images.history_turns", settings.HistoryTurns},
		{"images.max_request_count", settings.MaxRequestCount},
		{"images.max_request_bytes", settings.MaxRequestBytes},
		{"images.max_side", settings.MaxSide},
	} {
		if field.value <= 0 {
			return ImageSettings{}, fmt.Errorf("%s must be positive, got %d", field.key, field.value)
		}
	}
	return settings, nil
}
