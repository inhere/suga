package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestWebPolicy: scoring runs keep the web tools off for every agent the eval
// can restrict; only --allow-web or the task's allow_web turns them back on.
func TestWebPolicy(t *testing.T) {
	cases := []struct {
		name string
		o    options
		task Task
		want string
	}{
		{name: "suga default is off", o: options{agent: "suga"}, want: webOff},
		{name: "claude default is off", o: options{agent: "claude"}, want: webOff},
		{name: "--allow-web turns it on", o: options{agent: "suga", allowWeb: true}, want: webOn},
		{name: "task allow_web turns it on", o: options{agent: "suga"}, task: Task{AllowWeb: true}, want: webOn},
		{name: "omp has no switch", o: options{agent: "omp"}, want: webUnsupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Eq(t, c.want, webPolicy(&c.o, &c.task))
		})
	}
}

// TestWriteWebDenyRules: the deny file lands in the temp SUGA_HOME (which suga
// reads as its user approval-rules.yaml) and names both web tools; with the web
// allowed nothing is written.
func TestWriteWebDenyRules(t *testing.T) {
	t.Run("deny writes both rules", func(t *testing.T) {
		home := t.TempDir()
		assert.NoErr(t, writeWebDenyRules(home, true))
		raw, err := os.ReadFile(filepath.Join(home, "approval-rules.yaml"))
		assert.NoErr(t, err)
		assert.StrContains(t, string(raw), "tool: web_fetch")
		assert.StrContains(t, string(raw), "tool: web_search")
		assert.StrContains(t, string(raw), "action: deny")
	})
	t.Run("allow writes nothing", func(t *testing.T) {
		home := t.TempDir()
		assert.NoErr(t, writeWebDenyRules(home, false))
		_, err := os.Stat(filepath.Join(home, "approval-rules.yaml"))
		assert.True(t, os.IsNotExist(err))
	})
}

// TestPrintWebNote: the summary below the table says whether the run had web
// access, including the agent the eval cannot restrict.
func TestPrintWebNote(t *testing.T) {
	var buf bytes.Buffer
	printWebNote(&buf, []Result{{Web: webOff}, {Web: webOff}, {Web: webUnsupported}})
	assert.StrContains(t, buf.String(), "off=2 on=0 unsupported=1")
	assert.StrContains(t, buf.String(), "not restricted")

	buf.Reset()
	printWebNote(&buf, nil)
	assert.Eq(t, 0, buf.Len())
}
