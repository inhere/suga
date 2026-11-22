package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestReadMaxSteps(t *testing.T) {
	t.Run("the default without flag, environment and config", func(t *testing.T) {
		loadYAML(t, "")
		n, err := ReadMaxSteps(0)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, DefaultMaxSteps, n)
	})
	t.Run("mode-specific config and defaults", func(t *testing.T) {
		loadYAML(t, "chat:\n  max_steps: 1001\nrun:\n  max_steps: 17\n")
		chat, err := ReadMaxStepsFor(0, true)
		assert.Require(t, assert.NoError(t, err))
		run, err := ReadMaxStepsFor(0, false)
		assert.Require(t, assert.NoError(t, err))
		assert.Eq(t, 1001, chat)
		assert.Eq(t, 17, run)

		loadYAML(t, "")
		chat, err = ReadMaxStepsFor(0, true)
		assert.Require(t, assert.NoError(t, err))
		assert.Eq(t, DefaultChatMaxSteps, chat)
	})
	t.Run("mode-specific environment wins over shared environment", func(t *testing.T) {
		loadYAML(t, "chat:\n  max_steps: 7\n")
		t.Setenv("SUGA_MAX_STEPS", "8")
		t.Setenv("SUGA_CHAT_MAX_STEPS", "9")
		n, err := ReadMaxStepsFor(0, true)
		assert.Require(t, assert.NoError(t, err))
		assert.Eq(t, 9, n)
	})
	t.Run("the config value", func(t *testing.T) {
		loadYAML(t, "max_steps: 7\n")
		n, err := ReadMaxSteps(0)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 7, n)
	})
	t.Run("the environment overrides the config", func(t *testing.T) {
		loadYAML(t, "max_steps: 7\n")
		t.Setenv("SUGA_MAX_STEPS", "3")
		n, err := ReadMaxSteps(0)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 3, n)
	})
	t.Run("the flag overrides the environment and the config", func(t *testing.T) {
		loadYAML(t, "max_steps: 7\n")
		t.Setenv("SUGA_MAX_STEPS", "3")
		n, err := ReadMaxSteps(2)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, n)
	})
	t.Run("a negative flag is an error", func(t *testing.T) {
		loadYAML(t, "")
		_, err := ReadMaxSteps(-1)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "--max-steps")
	})
	t.Run("an invalid environment value is an error", func(t *testing.T) {
		loadYAML(t, "max_steps: 7\n")
		t.Setenv("SUGA_MAX_STEPS", "0")
		_, err := ReadMaxSteps(0)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "SUGA_MAX_STEPS")
		t.Setenv("SUGA_MAX_STEPS", "abc")
		_, err = ReadMaxSteps(0)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "SUGA_MAX_STEPS")
	})
	t.Run("a non-positive config value keeps its older meaning", func(t *testing.T) {
		loadYAML(t, "max_steps: 0\n")
		n, err := ReadMaxSteps(0)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, n) // no step limit, as before this change
	})
}
