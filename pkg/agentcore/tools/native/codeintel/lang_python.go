package codeintel

// pythonLanguage is the Python declaration: pytest, run directly or as a
// module of the interpreter (`-k` runs the tests matching an expression).
//
// It declares no DependencyAPI and no dependency root: the packages live in the
// interpreter's own site-packages, and the base prompt's dependency-API
// sentence has no Python clause; both would change the cached prompt prefix or
// the read-only root list, which suga-xt0o keeps unchanged.
var pythonLanguage = Language{
	Name:         "Python",
	Markers:      []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "tox.ini", "Pipfile"},
	TestCommands: `\b(pytest|python3? -m pytest)\b`,
	FocusedFlags: []string{"-k"},
}
