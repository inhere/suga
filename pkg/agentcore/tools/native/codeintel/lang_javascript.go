package codeintel

// jsLanguage is the JavaScript/TypeScript declaration: the test runners of the
// package managers and the two test tools (`-t` runs one matching test).
//
// It declares no DependencyAPI and no dependency root: the packages sit in the
// project's own node_modules, which the file tools read like any other
// directory of the workspace, so no command and no read-only root of its own is
// needed (and the base prompt's dependency-API sentence stays as it is).
//
// The file is lang_javascript.go, not lang_js.go: Go reads a trailing _js as a
// GOOS build constraint (js/wasm), so a file named lang_js.go would only be
// part of that build and the language would vanish from every other one.
var jsLanguage = Language{
	Name: "JavaScript/TypeScript",
	Markers: []string{"package.json", "package-lock.json", "pnpm-lock.yaml",
		"yarn.lock", "bun.lockb", "bun.lock", "tsconfig.json"},
	TestCommands: `\b(npm (run )?test|pnpm (run )?test|yarn test|jest|vitest)\b`,
	FocusedFlags: []string{"-t"},
}
