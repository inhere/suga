package main

import (
	"bytes"
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

// TestWebSets: the temp config override that hides the two web tools is added
// only for the runs whose policy is off; it uses suga's general tools.disabled,
// so the model never sees the tools (denying each call would show them and end
// the turn after a few refusals).
func TestWebSets(t *testing.T) {
	assert.Eq(t, []string{"tools.disabled=[web_fetch, web_search]"}, webSets(webOff))
	assert.Eq(t, webOffSet, webSets(webOff)[0])
	assert.Eq(t, 0, len(webSets(webOn)))
	assert.Eq(t, 0, len(webSets(webUnsupported)))
}

// TestWebLabel: compare.md records the network policy of a variant, also when
// its rows differ or carry none.
func TestWebLabel(t *testing.T) {
	t.Run("single value", func(t *testing.T) {
		assert.Eq(t, webOff, webLabel([]Result{{Web: webOff}, {Web: webOff}}))
	})
	t.Run("mixed values keep the order off, on, unsupported", func(t *testing.T) {
		assert.Eq(t, "off+unsupported", webLabel([]Result{{Web: webUnsupported}, {Web: webOff}}))
	})
	t.Run("no rows", func(t *testing.T) {
		assert.Eq(t, "-", webLabel(nil))
	})
}

// TestPrintWebNote: the summary below the table says whether the run had web
// access, including the agent the eval cannot restrict.
func TestPrintWebNote(t *testing.T) {
	var buf bytes.Buffer
	printWebNote(&buf, []Result{{Web: webOff}, {Web: webOff}, {Web: webUnsupported}})
	assert.StrContains(t, buf.String(), "off=2 on=0 unsupported=1")
	assert.StrContains(t, buf.String(), "tools.disabled")
	assert.StrContains(t, buf.String(), "not restricted")

	buf.Reset()
	printWebNote(&buf, nil)
	assert.Eq(t, 0, buf.Len())
}
