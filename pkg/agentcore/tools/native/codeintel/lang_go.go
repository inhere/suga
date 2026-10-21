package codeintel

// goLanguage is the Go declaration: the module cache and the standard library
// sources as read-only roots, `go doc` for a dependency's API, `go test` (and
// `-run` for one test) to run the tests.
var goLanguage = Language{
	Name:          "Go",
	Markers:       []string{"go.mod", "go.sum"},
	DependencyAPI: "`go doc <package>` (or a grep for the exported names) for Go",
	TestCommands:  `\bgo test\b`,
	FocusedFlags:  []string{"-run"},
}
