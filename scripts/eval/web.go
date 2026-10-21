package main

import (
	"fmt"
	"io"
)

// The network-tool policy of a run (Result.Web).
const (
	webOff         = "off"         // web_fetch/web_search disabled
	webOn          = "on"          // allowed (--allow-web or the task's allow_web)
	webUnsupported = "unsupported" // the agent has no switch the eval can set
)

// webOffSet keeps a scoring run off the network through suga's general
// tools.disabled setting: a listed tool is not registered at all, so the model
// never sees web_fetch/web_search (they are not in the tool list, tool_search
// cannot load them and the manifest does not list them). Denying each call
// would still show them and a few refusals end the turn (personal.DenialLimit).
const webOffSet = "tools.disabled=[web_fetch, web_search]"

// webPolicy decides whether this run keeps the agent off the network. The
// polyglot Go baseline of 2026-10-21 called web_fetch 79 times and could look up
// Exercism solutions, so scoring runs remove the two web tools by default.
func webPolicy(o *options, t *Task) string {
	switch {
	case o.agent == "omp": // omp has no tool switch the eval can set; the README says so
		return webUnsupported
	case o.allowWeb || t.AllowWeb:
		return webOn
	}
	return webOff
}

// webSets returns the config overrides that implement the policy, for the temp
// SUGA_HOME config.yaml (suga); nil when the web is allowed.
func webSets(policy string) []string {
	if policy != webOff {
		return nil
	}
	return []string{webOffSet}
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
		fmt.Fprint(w, " (off: tools.disabled hides web_fetch/web_search from suga)")
	}
	if count[webUnsupported] > 0 {
		fmt.Fprint(w, " (unsupported: the agent has no switch the eval can set, so it was not restricted)")
	}
	fmt.Fprintln(w)
}
