package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The network-tool policy of a run (Result.Web).
const (
	webOff         = "off"         // web_fetch/web_search denied
	webOn          = "on"          // allowed (--allow-web or the task's allow_web)
	webUnsupported = "unsupported" // the agent has no switch the eval can set
)

// webPolicy decides whether this run keeps the agent off the network. The
// polyglot Go baseline of 2026-10-21 called web_fetch 79 times and could look up
// Exercism solutions, so scoring runs deny the two web tools by default.
func webPolicy(o *options, t *Task) string {
	switch {
	case o.agent == "omp": // omp has no tool switch the eval can set; the README says so
		return webUnsupported
	case o.allowWeb || t.AllowWeb:
		return webOn
	}
	return webOff
}

// denyRuleText is the user approval-rules file that turns the two network tools
// off: suga's existing rule mechanism, so no eval branch is needed in suga
// itself. A call is refused with the rule in the hint the model sees.
const denyRuleText = `# Written by scripts/eval: scoring tasks run without the web tools.
# Turn them back on with --allow-web or the task's allow_web: true.
rules:
  - tool: web_fetch
    action: deny
  - tool: web_search
    action: deny
`

// writeWebDenyRules writes the deny rules into the temp SUGA_HOME
// (approval-rules.yaml) when deny is set; deny=false writes nothing.
func writeWebDenyRules(home string, deny bool) error {
	if !deny {
		return nil
	}
	return os.WriteFile(filepath.Join(home, "approval-rules.yaml"), []byte(denyRuleText), 0o600)
}

// printWebNote sums up the network policy of a run below the results table, so
// a report made from the terminal output says whether the run had web access.
func printWebNote(w io.Writer, rs []Result) {
	count := map[string]int{}
	for _, r := range rs {
		count[r.Web]++
	}
	if len(count) == 0 {
		return
	}
	fmt.Fprintf(w, "\nweb: off=%d on=%d unsupported=%d", count[webOff], count[webOn], count[webUnsupported])
	if count[webOff] > 0 {
		fmt.Fprint(w, " (off: web_fetch/web_search denied in the temp SUGA_HOME approval-rules.yaml)")
	}
	if count[webUnsupported] > 0 {
		fmt.Fprint(w, " (unsupported: the agent has no switch the eval can set, so it was not restricted)")
	}
	fmt.Fprintln(w)
}
