package personal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Assistant bodies (design 2026-10-20, 0.3): an assistant entry above the
// threshold moves into the session file, and the requests later turns send are
// byte-identical to the ones the same session sends with every body inline.

// bodyRepo opens a store whose sessions root the test can also reach (to break or
// drop the body files) and returns that root.
func bodyRepo(t *testing.T, inlineBytes int64) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := store.Open(filepath.Join(dir, "db", "sessions.db"), store.WithToolBodyInlineBytes(inlineBytes))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	return repo, filepath.Join(dir, "sessions")
}

// bodyToolCall asks for the big tool with large arguments: the arguments are what
// moves the assistant entry into the session file.
func bodyToolCall(id string) (*agentcore.GenerateResult, error) {
	return bodyToolCallSized(id, 3000)
}

// bodyToolCallSized is bodyToolCall with an argument of n bytes.
func bodyToolCallSized(id string, n int) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{
		ToolCalls: []agentcore.ToolCall{{CallID: id, Name: "big",
			Arguments: map[string]any{"path": "a.go", "content": strings.Repeat("y", n)}}},
		FinishReason: agentcore.FinishToolCalls,
	}, nil
}

// requestDigest is the byte form of what a model call was sent and the hash of its
// prefix (every message but the last): the prefix a provider caches.
func requestDigest(t *testing.T, msgs []agentcore.Message) (body, prefix string) {
	t.Helper()
	raw, err := json.Marshal(msgs)
	assert.Require(t, assert.NoErr(t, err))
	body = fmt.Sprintf("%x", sha256.Sum256(raw))
	if len(msgs) > 1 {
		praw, err := json.Marshal(msgs[:len(msgs)-1])
		assert.Require(t, assert.NoErr(t, err))
		prefix = fmt.Sprintf("%x", sha256.Sum256(praw))
	}
	return body, prefix
}

// assertSameRequests checks that two providers were sent the same requests, byte
// for byte and with the same prefix hash.
func assertSameRequests(t *testing.T, want, got *scriptProv) {
	t.Helper()
	assertSameRequestsFrom(t, want, got, 0, 0)
}

// assertSameRequestsFrom compares the requests both providers were sent from the
// given index on (the calls of the turns a test ran after a change).
func assertSameRequestsFrom(t *testing.T, want, got *scriptProv, fromReq, fromSum int) {
	t.Helper()
	assert.Require(t, assert.Eq(t, len(want.reqs), len(got.reqs)))
	for i := fromReq; i < len(want.reqs); i++ {
		bodyWant, prefixWant := requestDigest(t, want.reqs[i].Messages)
		bodyGot, prefixGot := requestDigest(t, got.reqs[i].Messages)
		assert.Eq(t, bodyWant, bodyGot, fmt.Sprintf("messages of request %d differ", i))
		assert.Eq(t, prefixWant, prefixGot, fmt.Sprintf("prefix of request %d differs", i))
	}
	assert.Eq(t, len(want.sumReqs), len(got.sumReqs), "the compaction calls differ")
	for i := fromSum; i < len(want.sumReqs); i++ {
		bodyWant, _ := requestDigest(t, want.sumReqs[i].Messages)
		bodyGot, _ := requestDigest(t, got.sumReqs[i].Messages)
		assert.Eq(t, bodyWant, bodyGot, fmt.Sprintf("summary input %d differs", i))
	}
}

// bodyScenario answers every turn with two tool calls and a long final text, so a
// few turns cross the compaction threshold and every assistant entry is large.
func bodyScenario(t *testing.T, repo *store.Store) (*scriptProv, *Runner) {
	t.Helper()
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n%3 != 0 {
			return bodyToolCall(fmt.Sprintf("c%d", n))
		}
		return finalText(fmt.Sprintf("answer-%d %s", n, strings.Repeat("a", 600)))
	}}
	return prov, compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
}

// runBodyScenario runs n turns of bodyScenario on a fresh store.
func runBodyScenario(t *testing.T, inlineBytes int64, n int) (*scriptProv, *Runner, *store.Store, string, string) {
	t.Helper()
	repo, sessionsDir := bodyRepo(t, inlineBytes)
	prov, runner := bodyScenario(t, repo)
	// A fixed session id keeps the two runs comparable byte for byte (the id itself
	// is part of the summary input).
	sid := "s1"
	for i := 0; i < n; i++ {
		_, err := runner.Run(context.Background(), RunRequest{SessionID: sid, CreateIfMissing: true,
			Prompt: fmt.Sprintf("question-%d %s", i, strings.Repeat("q", 600))})
		assert.Require(t, assert.NoErr(t, err))
	}
	return prov, runner, repo, sessionsDir, sid
}

// TestAssistantBodiesKeepEveryRequestIdentical: the same session sends the same
// requests with every body inline and with the assistant bodies moved out (the
// compaction start record, the mid-turn cuts and the summaries included).
func TestAssistantBodiesKeepEveryRequestIdentical(t *testing.T) {
	inlineProv, _, _, _, _ := runBodyScenario(t, 0, 7)
	movedProv, _, movedRepo, _, movedSID := runBodyScenario(t, store.DefaultToolBodyInlineBytes, 7)

	assertSameRequests(t, inlineProv, movedProv)

	// The moved session really holds stubs, and compaction cut in front of them.
	entries, err := movedRepo.ListEntries(context.Background(), movedSID, store.EntryFilter{NoHydrate: true})
	assert.Require(t, assert.NoErr(t, err))
	stubs := 0
	for _, e := range entries {
		if e.Stub() {
			stubs++
		}
	}
	assert.True(t, stubs > 0, "no entry moved out of the ledger")
	assert.Require(t, assert.True(t, len(compactionEntries(t, movedRepo, movedSID)) >= 1))
}

// TestAssistantBodiesKeepTheInterruptedReplayIdentical: a failed turn that
// compacted mid-way contributes its replayed prefix and the interruption note
// (whose tool summaries carry the call arguments) to the next request, and that
// request is the same with the bodies inline and moved out.
func TestAssistantBodiesKeepTheInterruptedReplayIdentical(t *testing.T) {
	run := func(t *testing.T, inlineBytes int64) (*scriptProv, *store.Store, string) {
		t.Helper()
		repo, _ := bodyRepo(t, inlineBytes)
		mode := "run"
		var prov *scriptProv
		prov = &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			switch {
			case mode == "run" && len(prov.sumReqs) > 0:
				// Compaction of this turn is recorded, then the model fails: the next
				// turn must not adopt it.
				return nil, errors.New("provider exploded")
			case mode == "run":
				return bodyToolCallSized(fmt.Sprintf("c%d", n), 1500)
			}
			return finalText("second answer " + strings.Repeat("b", 600))
		}}
		runner := compactRunner(t, repo, prov, bigToolRegistry(300, nil), nil)
		bad, err := runner.Run(context.Background(), RunRequest{SessionID: "s1", CreateIfMissing: true,
			Prompt: "long job " + strings.Repeat("q", 600)})
		assert.Eq(t, true, err != nil, "the first turn fails")
		assert.Eq(t, RunFailed, bad.State)
		assert.Eq(t, true, len(prov.sumReqs) >= 1, "the failed turn compacted mid-way")
		mode = "after"
		_, err = runner.Run(context.Background(), RunRequest{SessionID: "s1", Prompt: "continue"})
		assert.Require(t, assert.NoErr(t, err))
		return prov, repo, bad.SessionID
	}

	inlineProv, _, sid := run(t, 0)
	movedProv, movedRepo, _ := run(t, store.DefaultToolBodyInlineBytes)
	assertSameRequests(t, inlineProv, movedProv)

	// The note of the interrupted turn reached the model: as a replayed message of
	// the next request, or - when that turn compacts again at its start - inside the
	// summary input. Either way the replayed entries and the note are read back in
	// full (the comparison above already proved the two runs identical).
	reached := false
	for _, req := range movedProv.reqs {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[上一回合") {
				reached = true
			}
		}
	}
	for _, req := range movedProv.sumReqs {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[上一回合") {
				reached = true
			}
		}
	}
	assert.True(t, reached, "the interruption note did not reach the model")
	entries, err := movedRepo.ListEntries(context.Background(), sid, store.EntryFilter{
		Types: []string{store.EntryAssistant}, NoHydrate: true,
	})
	assert.Require(t, assert.NoErr(t, err))
	movedOut := 0
	for _, e := range entries {
		if e.Stub() {
			movedOut++
		}
	}
	assert.True(t, movedOut > 0, "no assistant entry moved out")
}

// corruptRecordsBefore breaks the file records of the entries with a seq below
// before, so reading any of them fails: a caller that never reads them is
// unaffected. It returns how many records it broke.
func corruptRecordsBefore(t *testing.T, sessionsDir, sessionID string, before int64) int {
	t.Helper()
	path := filepath.Join(sessionsDir, sessionID, store.ToolBodyFileName)
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	broken, at := 0, 0
	for {
		i := strings.Index(string(data[at:]), `{"seq":`)
		if i < 0 {
			break
		}
		start := at + i
		head := string(data[start+len(`{"seq":`):])
		digits := 0
		for digits < len(head) && head[digits] >= '0' && head[digits] <= '9' {
			digits++
		}
		seq, err := strconv.ParseInt(head[:digits], 10, 64)
		assert.Require(t, assert.NoErr(t, err))
		jsonAt := strings.Index(head, `"json":`)
		assert.Require(t, assert.True(t, jsonAt > 0, "record header of seq %d", seq))
		if seq < before {
			// One flipped byte inside the record's JSON: its sha256 no longer matches.
			data[start+len(`{"seq":`)+jsonAt+len(`"json":`)+2] ^= 0x01
			broken++
		}
		at = start + len(`{"seq":`)
	}
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, data, 0o600)))
	return broken
}

// TestAssistantBodiesCompactPointIsNotRead: the entries the compaction cut away
// are never read back, so breaking their records changes nothing for the next turn
// (a listing that hydrated everything would fail with ErrToolBodyMissing).
func TestAssistantBodiesCompactPointIsNotRead(t *testing.T) {
	inlineProv, inlineRunner, _, _, inlineSID := runBodyScenario(t, 0, 5)
	movedProv, movedRunner, movedRepo, sessionsDir, movedSID := runBodyScenario(t, store.DefaultToolBodyInlineBytes, 5)

	recs := compactionEntries(t, movedRepo, movedSID)
	assert.Require(t, assert.True(t, len(recs) >= 1, "the session did not compact"))
	broken := corruptRecordsBefore(t, sessionsDir, movedSID, recs[len(recs)-1].FirstKeptSeq)
	assert.True(t, broken > 0, "no moved record lies before the compaction point")

	// The next turn of each session sends the same requests: the broken records are
	// the cut-away ones, which is also what fails if the history hydrates them all.
	reqsInline, sumsInline := len(inlineProv.reqs), len(inlineProv.sumReqs)
	next := func(t *testing.T, runner *Runner, sid string) {
		t.Helper()
		_, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: "next question " + strings.Repeat("n", 600)})
		assert.Require(t, assert.NoErr(t, err))
	}
	next(t, inlineRunner, inlineSID)
	next(t, movedRunner, movedSID)
	assertSameRequestsFrom(t, inlineProv, movedProv, reqsInline, sumsInline)
}

// TestLoadHistoryRefusesStubItems: an item whose body cannot be read back must
// fail the load, never reach the model as a stub (design 2026-10-20, revision 2:
// the view asserts that no stub is left after hydration).
func TestLoadHistoryRefusesStubItems(t *testing.T) {
	ctx := context.Background()
	repo, sessionsDir := bodyRepo(t, store.DefaultToolBodyInlineBytes)
	runner, err := New(Config{Repository: repo, Provider: &scriptProv{name: "main"}, Model: "m", ProviderName: "p"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Status: store.SessionOpen})))
	assert.Require(t, assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: "t1", SessionID: "s1"})))
	assert.Require(t, assert.NoErr(t, repo.UpdateTurn(ctx, store.Turn{ID: "t1", Status: store.TurnCompleted})))

	reply := agentcore.Message{Role: "assistant", Content: "写好了", Reasoning: strings.Repeat("想一想", 500)}
	raw, err := json.Marshal(reply)
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{TurnID: "t1", Type: store.EntryAssistant, JSON: raw})
	assert.Require(t, assert.NoErr(t, err))

	// The body can be read back: the item holds the full message.
	view, err := runner.loadHistory(ctx, "s1")
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(view.items)))
	assert.Eq(t, reply.Reasoning, view.items[0].msg.Reasoning)

	// With the record gone the load refuses the item instead of sending the stub.
	assert.Require(t, assert.NoErr(t, os.Remove(filepath.Join(sessionsDir, "s1", store.ToolBodyFileName))))
	_, err = runner.loadHistory(ctx, "s1")
	assert.True(t, errors.Is(err, store.ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
}

// TestLastReplyReadsNoBody: showing the last reply reads the Content of the last
// assistant entries, and an assistant stub keeps it whole, so no body file is
// opened (a read would fail once the file is gone).
func TestLastReplyReadsNoBody(t *testing.T) {
	ctx := context.Background()
	repo, sessionsDir := bodyRepo(t, store.DefaultToolBodyInlineBytes)
	runner, err := New(Config{Repository: repo, Provider: &scriptProv{name: "main"}, Model: "m", ProviderName: "p"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Status: store.SessionOpen})))

	reply := agentcore.Message{Role: "assistant", Content: "写好了", Reasoning: strings.Repeat("想一想", 500)}
	raw, err := json.Marshal(reply)
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{Type: store.EntryAssistant, JSON: raw})
	assert.Require(t, assert.NoErr(t, err))
	// The reply moved out of the ledger: only its Content stays in the row.
	entries, err := repo.ListEntries(ctx, "s1", store.EntryFilter{NoHydrate: true})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(entries)))
	assert.True(t, entries[0].Stub(), "the reply moved out of the ledger")
	assert.Require(t, assert.NoErr(t, os.Remove(filepath.Join(sessionsDir, "s1", store.ToolBodyFileName))))

	text, _, err := runner.LastReply(ctx, "s1")
	assert.NoErr(t, err)
	assert.Eq(t, "写好了", text)
}

// TestTurnItemsReadsNoBody: a mid-turn compaction lists this turn's entries for
// their metadata only, so it still runs after the session file of the turn's own
// stubs is gone (a hydrating listing would fail with ErrToolBodyMissing and the
// turn would compact without a summary).
func TestTurnItemsReadsNoBody(t *testing.T) {
	dir := t.TempDir()
	repo, err := store.Open(filepath.Join(dir, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()

	prov := &scriptProv{name: "main"}
	prov.normal = func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n == 2 {
			// The first step's assistant entry is a stub now: drop the file so any read
			// of it fails.
			paths, gerr := filepath.Glob(filepath.Join(dir, "sessions", "*", store.ToolBodyFileName))
			assert.NoErr(t, gerr)
			assert.Require(t, assert.True(t, len(paths) > 0))
			for _, p := range paths {
				assert.NoErr(t, os.Remove(p))
			}
		}
		if n <= 4 {
			return bodyToolCall(fmt.Sprintf("c%d", n))
		}
		return finalText("done " + strings.Repeat("d", 600))
	}
	runner := compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "long job " + strings.Repeat("q", 600)})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, RunCompleted, res.State)
	// The turn compacted mid-way after the file was gone: the summary was made, the
	// items it summarized were never hydrated.
	assert.True(t, len(prov.sumReqs) >= 1, "the mid-turn compaction did not run")
}
