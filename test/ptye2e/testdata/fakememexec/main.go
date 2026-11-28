// Command fakememexec is the exec protocol v1 memory backend of the ptye2e
// trusted-memory scenario (test/ptye2e/chat_trusted_memory_test.go).
//
// It reads ONE request line from stdin and answers with ONE result line on
// stdout, the envelope pkg/agentcore/memory/execstore documents:
//
//	{"v":1,"capability":"memory","op":"get","args":{...}}
//	{"ok":true,"result":{...}}   or   {"ok":false,"error":"..."}
//
// The result comes from the JSON fixture named by FAKEMEMEXEC_FIXTURE, a map of
// op name to result - the same fixture shape internal/wiring/main_test.go serves
// for the unit-level chain test. An op without a fixture entry is reported as
// not found. The program uses the standard library only, so Linux and the
// Windows ConPTY run the very same file (no shell script).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// main serves one call of the exec protocol and exits.
func main() {
	var req struct {
		Op string `json:"op"`
	}
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &req) != nil {
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Getenv("FAKEMEMEXEC_FIXTURE"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakememexec: fixture: %v\n", err)
		os.Exit(2)
	}
	var results map[string]json.RawMessage
	if json.Unmarshal(raw, &results) != nil {
		fmt.Fprint(os.Stderr, "fakememexec: fixture is not an object of op to result\n")
		os.Exit(2)
	}
	if res, ok := results[req.Op]; ok {
		fmt.Printf("{\"ok\":true,\"result\":%s}\n", res)
		return
	}
	fmt.Printf("{\"ok\":false,\"error\":\"not found: %s\"}\n", req.Op)
}
