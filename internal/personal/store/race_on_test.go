//go:build race

package store

// raceEnabled reports a -race build: the race detector slows SQLite-heavy
// timing tests several times over, so wall-clock budgets do not hold there.
const raceEnabled = true
