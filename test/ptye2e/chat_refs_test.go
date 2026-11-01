//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// refsGoSrc covers all four classes of one name: def (the func and a struct field),
// call with a package qualifier (db.Load), sel (h.Load, not called) and ref (Load
// as a value). The comment and the string literal mention the name too and must
// not be reported; their canary words prove it.
const refsGoSrc = `package svc

import "example/db"

// Load is documented here; a comment is not a reference.
func Load(id string) string { return db.Load(id) }

type Handler struct{ Load func(string) string }

func Run(h Handler) string {
	fn := h.Load
	text := "Load SMOKE_STRING_CANARY"
	return fn(text)
}

var alias = Load
`

const refsTSSrc = `import { Store } from "./store";

// Load SMOKE_COMMENT_CANARY
export function Load(name: string): string {
  return name;
}

export const label = "Load SMOKE_STRING_CANARY";

export function useLoad(s: Store): string {
  return Load(s.id);
}
`

// TestChatRefs ports scripts/ptysmoke/chat_refs.py: file_refs answers "who uses X"
// in chat. The fake model (refs mode) calls file_refs on one Go file, then on the
// whole workspace.
//
// Python check -> Go assertion:
//
//	call 1: file-level arguments / result 1: first match is the classified call   screen lines
//	call 2: directory-level arguments                                             "▸ file_refs Load" alone on its line
//	result 2: the Go file comes first                                             matched AFTER call 2's line (the
//	                                                                              Python pattern also matched result 1)
//	turn ends / exit / session id
//	trace: two file_refs events, both ok, targets [svc/svc.go] and ["."], arg summaries carry the paths
//	stored (suga inspect export): two calls / two results; calls keep name and path;
//	  result 1: four classes (def, call(pkg:db), sel(h), ref), no-type-resolution note, no approx note,
//	  comment and string literal not matches; result 2: reaches web/app.ts, approx note, no canaries
func TestChatRefs(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "refs", Delay: 200 * time.Millisecond}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	// A go.mod of its own makes the fixture a separate module.
	for name, body := range map[string]string{
		"go.mod": "module example.com/refsmoke\n\ngo 1.22\n", filepath.Join("svc", "svc.go"): refsGoSrc,
		filepath.Join("web", "app.ts"): refsTSSrc,
	} {
		path := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL})

	m := s.Mark()
	until(t, s, "❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("谁用了 Load")
	// The call lines show the arguments in the form of the tool: file_refs prints
	// the identifier and the path it searches.
	until(t, s, "call 1: file-level arguments", `▸ file_refs Load svc/svc\.go`, m)
	until(t, s, "result 1: the first match is the classified call", `⎿ ok: svc/svc\.go:6 call\(pkg:db\) `, m)
	until(t, s, "call 2: directory-level arguments", `▸ file_refs Load\r*\n`, m)
	until(t, s, "turn ends", `tokens · `, m)
	text := s.Since(m)
	if loc := regexp.MustCompile(`▸ file_refs Load\r*\n`).FindStringIndex(text); loc != nil {
		check(t, "result 2: the Go file comes first", regexp.MustCompile(`⎿ ok: svc/svc\.go:6 `).MatchString(text[loc[1]:]))
	} else {
		check(t, "result 2: the Go file comes first", false)
	}
	session := exitAfterTurn(t, s, m)
	if !check2(t, "session id", session != "") {
		return
	}

	// The two calls reach the trace as tool events, with their target.
	var refs []map[string]any
	for _, e := range traceEvents(t, binary, work, env, session) {
		if e["kind"] == "tool" && e["name"] == "file_refs" {
			refs = append(refs, e)
		}
	}
	if check2(t, "trace: two file_refs events", len(refs) == 2) {
		one, many := attrs(refs[0]), attrs(refs[1])
		check(t, "trace: both calls ended ok", refs[0]["status"] == "ok" && refs[1]["status"] == "ok")
		check(t, "trace: the file-level call names its file", jsonText(one["target"]) == `["svc/svc.go"]`)
		check(t, "trace: the directory-level call names the workspace root", jsonText(many["target"]) == `["."]`)
		check(t, "trace: the argument summaries carry the paths",
			strings.Contains(str(one["arg"]), "path=svc/svc.go") && strings.Contains(str(many["arg"]), "path=."))
	}

	// What the session keeps: the arguments of both calls and what each result
	// listed (the tool event attrs carry only the target, not the result text).
	exported, _, _ := sugaRun(t, binary, work, env, "inspect", "export", session)
	var calls, results []string
	for _, line := range strings.Split(exported, "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil || row["kind"] != "entry" {
			continue
		}
		body := jsonText(row["json"])
		switch row["type"] {
		case "assistant":
			if strings.Contains(body, `"Name":"file_refs"`) {
				calls = append(calls, body)
			}
		case "tool":
			results = append(results, body)
		}
	}
	check(t, "stored: two file_refs calls", len(calls) == 2)
	if !check2(t, "stored: two file_refs results", len(results) == 2) || len(calls) != 2 {
		return
	}
	check(t, "stored: the calls kept the name and the target",
		strings.Contains(calls[0], `"name":"Load"`) && strings.Contains(calls[1], `"name":"Load"`) &&
			strings.Contains(calls[0], `"path":"svc/svc.go"`) && strings.Contains(calls[1], `"path":"."`))
	one, many := results[0], results[1]
	check(t, "result 1: the four Go classes",
		strings.Contains(one, " def ") && strings.Contains(one, "call(pkg:db)") && strings.Contains(one, "sel(h)") && strings.Contains(one, " ref "))
	check(t, "result 1: the fixed no-type-resolution note", strings.Contains(one, "[no type resolution:"))
	check(t, "result 1: an exact Go result carries no approx note", !strings.Contains(one, "[approx:"))
	check(t, "result 1: the comment and the string literal are not matches",
		!strings.Contains(one, "documented") && !strings.Contains(one, "SMOKE_STRING_CANARY"))
	check(t, "result 2: the workspace search reaches the TypeScript file", strings.Contains(many, "web/app.ts:"))
	check(t, "result 2: the approximate match is marked", strings.Contains(many, "[approx:"))
	check(t, "result 2: the masked comment and string are not matches",
		!strings.Contains(many, "SMOKE_COMMENT_CANARY") && !strings.Contains(many, "SMOKE_STRING_CANARY"))
}

// jsonText is v as compact JSON text (a string stays as is): export writes an
// entry's json as an object, trace attrs hold decoded lists.
func jsonText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // keep < > & readable in the text checked
	_ = enc.Encode(v)
	return strings.TrimSpace(out.String())
}
