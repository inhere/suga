package chat

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestChatKeepsRunningWithoutProcessRegistration covers the local socket path limit: when the
// session process cannot register (here: the SUGA_HOME socket path exceeds the platform limit
// and the per-user fallback location is past it too), chat must keep running and say why it
// is unreachable instead of exiting before the first prompt.
func TestChatKeepsRunningWithoutProcessRegistration(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	// 120 extra characters push the socket path past every platform limit (107 bytes on Linux,
	// 103 on macOS, 100 UTF-16 code units on Windows).
	home := filepath.Join(shortTestHome(t), strings.Repeat("d", 120))
	t.Setenv("SUGA_HOME", home)
	// A too-long home alone no longer fails: Start falls back to the per-user socket
	// directory. Pin the genuinely failing case by pushing the fallback base past the
	// same limit, so even the fallback cannot save the registration.
	base := shortTestHome(t)
	for i := 0; i < 8; i++ {
		base = filepath.Join(base, strings.Repeat("fall-segment-", 3))
	}
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", base)
	} else {
		t.Setenv("TMPDIR", base)
	}
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository:  repo,
		Provider:    mock.NewProvider(mock.Final("unused")),
		ProcessRoot: filepath.Join(home, "run"),
	})
	assert.Require(t, assert.NoErr(t, err))

	var stdout, stderr strings.Builder
	err = Run(context.Background(), runner, Options{}, strings.NewReader(":quit\n"), &stdout, &stderr, nil)
	assert.NoErr(t, err)
	// One Chinese line explains what is lost (`suga send`, gofer delivery) and the way out.
	assert.StrContains(t, stderr.String(), "suga send")
	assert.StrContains(t, stderr.String(), "gofer")
	assert.StrContains(t, stderr.String(), "SUGA_HOME")
	// A failed registration leaves no record and no socket behind.
	entries, dirErr := os.ReadDir(filepath.Join(home, "run"))
	assert.Require(t, assert.NoErr(t, dirErr))
	assert.Len(t, entries, 0)
}
