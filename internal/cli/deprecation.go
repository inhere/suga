package cli

import (
	"fmt"
	"io"
)

// Deprecated command line forms. Each one still works but names its replacement
// and the release that drops it: v0.41.0, two minor versions after the notice
// started (user 2026-10-20: the data migration is done, the compatibility names
// can go). The notice is written to stderr next to a command that already writes
// its report to stdout, so nothing that parses stdout (including the stream-json
// contract) sees it.

// deprecationRemoval is the release the forms below are dropped in. Keep it in
// sync with config.DeprecatedConfigRemoval (the configuration key alias).
const deprecationRemoval = "v0.41.0"

// deprecation is one deprecated form: the spelling as typed, what replaces it
// and whether it is a one-time migration of a ledger written by v0.35 or older.
type deprecation struct {
	// form is the deprecated command form as the user types it.
	form string
	// replacement is the command that does the job from now on; empty when the
	// job itself goes away (the one-time migrations).
	replacement string
	// migration marks the one-time jobs that convert a pre-v0.36 ledger.
	migration bool
}

var (
	// The v0.36 name of `data bodies`.
	deprecatedToolBodiesName = deprecation{form: "suga data tool-bodies", replacement: "suga data bodies"}
	// One-time jobs: they convert a ledger written by v0.35 or older.
	deprecatedDedupeJob      = deprecation{form: "suga data dedupe-tool-results", migration: true}
	deprecatedShrinkFTSJob   = deprecation{form: "suga data shrink-fts", migration: true}
	deprecatedSlimJob        = deprecation{form: "suga data bodies --slim", migration: true}
	deprecatedRenameFilesJob = deprecation{form: "suga data bodies --rename-files", migration: true}
)

// tell writes the one-line notice of a deprecated form to stderr.
func (d deprecation) tell(stderr io.Writer) {
	notice := "提示：" + d.form + " 已弃用，将在 " + deprecationRemoval + " 移除"
	switch {
	case d.migration:
		notice += "；它用于把 v0.35 及更早的库迁移到当前格式，已迁移的库不需要"
	case d.replacement != "":
		notice += "，改用 " + d.replacement
	}
	fmt.Fprintln(stderr, notice)
}
