package native

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestPHPTestFilesAreSkipped pins the PHPUnit/Pest naming: FooTest.php is a
// test file the directory searches leave out unless include_tests is set.
func TestPHPTestFilesAreSkipped(t *testing.T) {
	t.Run("FooTest.php is a test file", func(t *testing.T) {
		assert.True(t, isTestFileName("GreeterTest.php"))
	})
	t.Run("an ordinary class file is not", func(t *testing.T) {
		assert.False(t, isTestFileName("Greeter.php"))
		assert.False(t, isTestFileName("Testing.php"))
	})
}
