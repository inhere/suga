package store

import "testing"

func TestReload(t *testing.T) {
	if err := New().Reload(); err != nil {
		t.Fatal(err)
	}
}
