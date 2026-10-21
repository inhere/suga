package codeintel

// phpLanguage is the PHP declaration: PHPUnit and Pest, and the flag that
// limits a run to some tests (`--filter`).
//
// It declares no DependencyAPI and no dependency root: Composer installs the
// packages in the project's own vendor/, which the file tools read where it is
// (file_symbols with no_ignore: true lists their API), so no command and no
// read-only root of its own is needed and the base prompt's dependency-API
// sentence - part of the provider's cached prompt prefix - stays as it is.
//
// The test commands are the ones the trace analysis recognised before PHP
// became a language of its own (lang.go's otherTestCommands held phpunit):
// moving them here keeps the recognition of an existing command unchanged.
// `vendor/bin/phpunit` and `pest` are the two spellings a project without a
// global installation uses.
var phpLanguage = Language{
	Name:         "PHP",
	Markers:      []string{"composer.json", "composer.lock"},
	TestCommands: `\b(vendor/bin/phpunit|phpunit|pest)\b`,
	FocusedFlags: []string{"--filter"},
}
