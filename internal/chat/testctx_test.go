package chat

import (
	"context"
	"testing"
	"time"
)

// testCtx is a hang guard for a test's blocking call: 30s, with the cancel
// wired into t.Cleanup. It bounds a broken test instead of measuring any
// latency, so it never tightens the timing a test asserts on.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
