package native

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestLanguagesRegistry checks what every language must declare: a name, the
// project files that identify it, a compilable test command regex and the
// dependency-API clause of the languages the prompt names. The registry keeps
// the historical order: it decides the order of the dependency roots in the
// environment prompt and of the prompt clauses (suga-xt0o keeps both unchanged).
func TestLanguagesRegistry(t *testing.T) {
	langs := Languages()
	assert.Require(t, assert.NotEmpty(t, langs))

	var names, clauses []string
	for _, lang := range langs {
		t.Run(lang.Name, func(t *testing.T) {
			assert.NotEmpty(t, lang.Name, "every language has a name")
			assert.NotEmpty(t, lang.Markers, "the project files that identify it")
			assert.Require(t, assert.NotEmpty(t, lang.TestCommands), "its test command regex")
			_, err := regexp.Compile(lang.TestCommands)
			assert.NoErr(t, err, lang.TestCommands)
			for _, flag := range lang.FocusedFlags {
				assert.NotEmpty(t, flag, "a focused-run flag")
				_, err = regexp.Compile(flag)
				assert.NoErr(t, err, flag)
			}
			// A clause must read as part of the dependency-API sentence, so it
			// holds the command in backticks.
			if lang.DependencyAPI != "" {
				assert.StrContains(t, lang.DependencyAPI, "`", "the command in backticks")
			}
		})
		names = append(names, lang.Name)
		if lang.DependencyAPI != "" {
			clauses = append(clauses, lang.Name)
		}
	}
	assert.Eq(t, []string{"Go", "Rust", "Java", "JavaScript/TypeScript", "Python", "PHP"}, names)
	// These two are the languages the prompt's dependency-API line names.
	assert.Eq(t, []string{"Go", "Java"}, clauses)
}

// TestRegistryDependencyRoots ties the dependency roots to the registry: the Go
// language declares the module cache, Rust the crate sources, Java the Maven
// repository and the Gradle cache. Removing a language from the registry drops
// its roots (reverse verification of suga-xt0o).
func TestRegistryDependencyRoots(t *testing.T) {
	byName := map[string]Language{}
	for _, lang := range Languages() {
		byName[lang.Name] = lang
	}
	for _, name := range []string{"Go", "Rust", "Java"} {
		lang, ok := byName[name]
		assert.Require(t, assert.True(t, ok, "registered: "+name))
		assert.NotNil(t, lang.dependencyRoots, name)
	}
	// JS, Python and PHP have no cache outside the workspace: their packages live
	// in node_modules, site-packages and vendor/, which the file tools read where
	// they are.
	for _, name := range []string{"JavaScript/TypeScript", "Python", "PHP"} {
		assert.Nil(t, byName[name].dependencyRoots, name)
	}

	t.Run("the java probe adds the maven repository and the gradle cache", func(t *testing.T) {
		home := setHome(t) // no GRADLE_USER_HOME: the home directory counts
		java := byName["Java"]
		labels := func() []string {
			collector := &rootCollector{}
			java.dependencyRoots(langEnv{home: home}, collector)
			var out []string
			for _, root := range collector.out {
				out = append(out, root.Label)
			}
			return out
		}
		// Neither directory exists yet: the probe adds nothing.
		assert.Empty(t, labels())
		repo := filepath.Join(home, ".m2", "repository")
		gradle := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
		assert.NoErr(t, os.MkdirAll(repo, 0o755))
		assert.NoErr(t, os.MkdirAll(gradle, 0o755))
		assert.Eq(t, []string{"Maven local repository", "Gradle module cache"}, labels())
	})
}

// TestLanguageTestCommandPattern locks the commands the trace analysis counts as
// test runs: the Go, Rust, JS and Python commands of before the split plus the
// Java and Gradle ones suga-iizi added (the wrappers ./mvnw, mvnw.cmd, gradlew,
// gradlew.bat, a subproject task ./gradlew :app:test and mvn verify) and the
// PHPUnit/Pest ones PHP moved out of otherTestCommands (phpunit alone is
// recognised as before).
func TestLanguageTestCommandPattern(t *testing.T) {
	pattern := TestCommandPattern()
	matching := []string{
		"go test ./...",
		"go test -count=1 -run TestX ./internal/...",
		"pytest tests",
		"python -m pytest -q",
		"python3 -m pytest",
		"cargo test",
		"cd crate && cargo test --lib",
		"npm test",
		"npm run test",
		"pnpm test",
		"pnpm run test",
		"yarn test",
		"npx jest",
		"pnpm vitest run",
		"mvn test",
		"mvn -B test",
		"mvn -o -Dtest=AppTest test",
		"mvn verify",
		"./mvnw test",
		"./mvnw -q test",
		"mvnw.cmd -B test",
		`.\mvnw.cmd test`,
		"gradle test",
		"gradle --no-daemon test",
		"./gradlew test",
		"gradlew.bat :app:test",
		`.\gradlew.bat :app:test`,
		"./gradlew clean :app:test",
		"./gradlew test --tests 'com.example.*'",
		"make test",
		"phpunit",
		"phpunit --filter testAdd",
		"./vendor/bin/phpunit",
		"vendor/bin/phpunit --filter testAdd tests/",
		"pest",
		"./vendor/bin/pest --filter testAdd",
		"rspec spec",
		"ctest --output-on-failure",
		"dotnet test",
		"cd /work/demo; ./gradlew :service:test --info",
	}
	for _, command := range matching {
		t.Run("matches "+command, func(t *testing.T) {
			assert.True(t, pattern.MatchString(command), command)
		})
	}
	// A build or an unrelated command is no test run, whatever tool it uses.
	notMatching := []string{
		"go build ./...",
		"go vet ./...",
		"gofmt -l .",
		"git log --oneline",
		"cargo build --release",
		"npm install",
		"npm run build",
		"mvn -B package -DskipTests",
		"mvn compile",
		"mvn dependency:sources",
		"mvn -N install",
		"./mvnw -N install",
		"gradle assemble",
		"./gradlew :app:assemble",
		"./gradlew clean build",
		"dotnet build",
		"make build",
		"composer install",
	}
	for _, command := range notMatching {
		t.Run("no match "+command, func(t *testing.T) {
			assert.False(t, pattern.MatchString(command), command)
		})
	}
}

// TestLanguageFocusedTestPattern locks the focused-run flags: -run (Go), -k
// (pytest), --test (cargo) and -t (jest/vitest) of before the split plus -Dtest
// (Maven), --tests (Gradle) and --filter (PHPUnit/Pest), the last one with the
// PHP entry. Such a run checks one test or one class, so it is no proof that
// the suite passes and the analysis must recognise it.
func TestLanguageFocusedTestPattern(t *testing.T) {
	pattern := FocusedTestPattern()
	focused := []string{
		"go test -count=1 -run TestScratch -v ./",
		"go test -run=TestScratch ./...",
		"pytest -k test_add",
		"pytest -k=test_add",
		"cargo test --test integration",
		"cargo test --test=integration",
		"npx jest -t 'adds two'",
		"mvn -Dtest=AppTest test",
		"mvn -B -Dtest=AppTest#testAdd test",
		"./mvnw -Dtest='AppTest*' verify",
		"./gradlew test --tests 'com.example.*'",
		"./gradlew :app:test --tests=com.example.AppTest",
		"vendor/bin/phpunit --filter testAdd",
		"phpunit --filter=testAdd",
		"./vendor/bin/pest --filter 'it adds two'",
	}
	for _, command := range focused {
		t.Run("focused "+command, func(t *testing.T) {
			assert.True(t, pattern.MatchString(command), command)
		})
	}
	// The whole suite: no flag of the list, and no flag inside a longer word or
	// an unrelated switch either.
	whole := []string{
		"go test ./...",
		"go test -count=1 -v ./internal/...",
		"pytest tests",
		"cargo test",
		"mvn test",
		"mvn -B verify",
		"./mvnw test",
		"./gradlew test",
		"./gradlew :app:test",
		"mvn -o -DskipTests verify",
		"npm test -- --coverage",
		"vendor/bin/phpunit",
		"vendor/bin/phpunit tests/",
		"pest",
	}
	for _, command := range whole {
		t.Run("whole suite "+command, func(t *testing.T) {
			assert.False(t, pattern.MatchString(command), command)
		})
	}
}
