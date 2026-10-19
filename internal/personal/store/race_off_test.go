//go:build !race

package store

// raceEnabled reports a -race build (see race_on_test.go).
const raceEnabled = false
