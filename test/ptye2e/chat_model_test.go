//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var (
	modelCandidate = regexp.MustCompile(`^[^\s/]+/[^\s/]+$`)
	sessionIDLine  = regexp.MustCompile(`session id: ((?:sess_|sg_|s_)\S+)`)
)

// waitModels waits until the fake provider has logged want requests and returns
// the model field of every request so far.
func waitModels(fake *fakeLLM, want int) []string {
	var models []string
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		models = models[:0]
		for _, req := range fake.Requests() {
			models = append(models, req.Model)
		}
		if len(models) >= want {
			break
		}
	}
	return models
}

func sameStrings(a, b []string) bool {
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}

// TestChatModel ports scripts/ptysmoke/chat_model.py: two fake models plus a
// referenced compact and a fallback model; the offline `models --json`
// switchable set must equal the set `:model` lists, switching to m2 changes the
// next request's model field without writing the config, and a resumed session
// keeps m2 (banner and request).
//
// Python check -> Go assertion:
//
//	banner starts with m1 / model list has m1, m2, compact, unlisted fallback
//	models JSON and chat candidates are equal
//	model Tab completion shares unlisted fallback
//	switch accepted / model command does not write config
//	next request uses m2 / session id is printed
//	resume banner keeps m2 / resumed request uses m2
//	trace command succeeds / trace records model switch
func TestChatModel(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Reply: "MODEL_REPLY", Delay: 100 * time.Millisecond}
	server := httptest.NewServer(fake)
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	opts := chatOpts{
		Home: home, Work: work, URL: server.URL,
		ProviderExtra: "    models:\n      m1:\n        context_window: 128000\n      m2:\n" +
			"        context_window: 64000\n        max_output_tokens: 42\n",
		Extra: "compact_model: fake/compact\nfallback_models:\n  - fake/unlisted\nchat:\n  steering: false\n",
	}
	first, env := startChatOpts(t, binary, opts)

	out, errOut, code := sugaRun(t, binary, work, env, "models", "--json")
	if code != 0 {
		t.Fatalf("models --json failed: %s", errOut)
	}
	var catalog struct {
		Switchable []string `json:"switchable"`
	}
	if err := json.Unmarshal([]byte(out), &catalog); err != nil || len(catalog.Switchable) == 0 {
		t.Fatalf("models --json returned no switchable candidates: %v\n%s", err, out)
	}
	configPath := filepath.Join(home, "config.yaml")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	mark := first.Mark()
	until(t, first, "banner starts with m1", `Suga chat · fake/m1`, mark)
	mark = first.Mark()
	first.SendLine(":model")
	until(t, first, "model list has m1", `\* fake/m1`, mark)
	until(t, first, "model list has m2", `fake/m2`, mark)
	until(t, first, "model list has compact", `fake/compact`, mark)
	until(t, first, "model list has unlisted fallback", `fake/unlisted`, mark)
	// Only the lines under "可选模型：" are candidates; the "当前模型：" header
	// above them also looks like provider/model.
	listing := first.Since(mark)
	if _, after, ok := strings.Cut(listing, "可选模型："); ok {
		listing = after
	}
	var listed []string
	seen := map[string]bool{}
	for _, line := range strings.Split(listing, "\n") {
		candidate := strings.TrimPrefix(strings.TrimSpace(line), "* ")
		candidate = strings.TrimSpace(candidate)
		if modelCandidate.MatchString(candidate) && !seen[candidate] {
			seen[candidate] = true
			listed = append(listed, candidate)
		}
	}
	want := append([]string(nil), catalog.Switchable...)
	sort.Strings(want)
	sort.Strings(listed)
	check(t, "models JSON and chat candidates are equal", sameStrings(listed, want))
	if !sameStrings(listed, want) {
		t.Logf("listed %v, switchable %v", listed, want)
	}

	mark = first.Mark()
	first.Send(":model fake/un\t")
	until(t, first, "model Tab completion shares unlisted fallback", `:model fake/unlisted`, mark)
	first.Send("\x15")
	mark = first.Mark()
	first.SendLine(":model fake/m2")
	until(t, first, "switch accepted", `已切换模型：fake/m2`, mark)
	after, err := os.ReadFile(configPath)
	check(t, "model command does not write config", err == nil && string(after) == string(before))
	mark = first.Mark()
	first.SendLine("first request")
	check(t, "next request uses m2", sameStrings(waitModels(fake, 1), []string{"m2"}))
	expect(t, first, `tokens · `, mark)
	first.SendLine(":quit")
	waitExit(t, first)
	found := sessionIDLine.FindStringSubmatch(first.Text())
	check(t, "session id is printed", found != nil)
	if found == nil {
		return
	}
	sid := found[1]

	opts.Args = []string{"--resume", sid}
	second, _ := startChatOpts(t, binary, opts)
	mark = second.Mark()
	until(t, second, "resume banner keeps m2", `Suga chat · fake/m2`, mark)
	// Wait for the prompt (after the resumed reply preview): text typed ahead of
	// the line editor's raw mode keeps its characters but loses the Enter.
	expect(t, second, `❯ `, mark)
	time.Sleep(300 * time.Millisecond)
	mark = second.Mark()
	second.SendLine("resumed request")
	check(t, "resumed request uses m2", sameStrings(waitModels(fake, 2), []string{"m2", "m2"}))
	expect(t, second, `tokens · `, mark)
	second.SendLine(":quit")
	waitExit(t, second)

	traceOut, _, traceCode := sugaRun(t, binary, work, env, "inspect", "trace", sid, "--json")
	check(t, "trace command succeeds", traceCode == 0)
	var sw map[string]any
	for _, line := range strings.Split(traceOut, "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["kind"] == "session" && e["name"] == "model_switch" {
			sw = e
			break
		}
	}
	check(t, "trace records model switch", attrs(sw)["from"] == "fake/m1" && attrs(sw)["to"] == "fake/m2")
}
