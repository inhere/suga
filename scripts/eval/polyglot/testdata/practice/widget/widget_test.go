package widget

import "testing"

func TestHalf(t *testing.T) {
	if got := Half(4); got != 2 {
		t.Fatalf("Half(4) = %d, want 2", got)
	}
}
