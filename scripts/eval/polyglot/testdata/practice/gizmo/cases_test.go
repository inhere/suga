package gizmo

import "testing"

func TestDoubleZero(t *testing.T) {
	if got := Double(0); got != 0 {
		t.Fatalf("Double(0) = %d, want 0", got)
	}
}
