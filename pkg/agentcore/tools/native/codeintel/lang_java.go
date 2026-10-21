package codeintel

// javaLanguage is the Java declaration: the local Maven repository and the
// Gradle module cache as read-only roots, `javap` for a dependency's API, and
// the Maven and Gradle commands that run the tests.
//
// The test commands cover the two wrappers as well (suga-iizi): `./mvnw` and
// `mvnw.cmd` behave like `mvn`, `gradlew`/`gradlew.bat` like `gradle`, and a
// Gradle task of a subproject (`./gradlew :app:test`) runs the tests of that
// module. `mvn verify` runs the tests too (the integration ones as well), so it
// counts like `mvn test`. A run limited to some tests is `-Dtest=` for Maven
// and `--tests` for Gradle; both are declared as the flag without its "=",
// which the shared pattern's trailing "(\s|=)" accepts.
var javaLanguage = Language{
	Name:    "Java",
	Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
	DependencyAPI: "for Java, `javap -public -cp <jar> <fully.qualified.Class>` with the jar under " +
		"~/.m2/repository/<group path>/<artifact>/<version>/ (`mvn -o dependency:sources` fetches the sources)",
	TestCommands: `\bmvnw?(\.cmd)?( \S+)* (test|verify)\b` +
		`|\bgradlew?(\.bat)?( \S+)* (:[A-Za-z0-9_.-]+:)?test\b`,
	FocusedFlags: []string{"-Dtest", "--tests"},
}
