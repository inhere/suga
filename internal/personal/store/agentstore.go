package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// AgentStorage implements api.Storage on top of the core store with
// run_id == turn_id: SaveRun/LoadRun use turns.state_json, and messages are
// session entries tagged with the turn id.
//
// The turn row must exist before SaveRun/AppendMessages (the Runner creates it
// first); a run id without a turn is reported as ErrTurnNotFound.
type AgentStorage struct {
	store *Store
}

var _ api.Storage = (*AgentStorage)(nil)

// AgentStorage returns the agentcore storage adapter for this store.
func (s *Store) AgentStorage() *AgentStorage { return &AgentStorage{store: s} }

// messageEntryTypes are the entry types LoadMessages replays.
var messageEntryTypes = []string{EntryUser, EntryAssistant, EntryTool}

// entryTypeOf maps a message to its entry type. The full Message is stored in
// entry.json, so the type only drives filtering.
func entryTypeOf(m api.Message) string {
	switch {
	case m.ToolResult != nil || m.Role == "tool":
		return EntryTool
	case m.Role == "assistant":
		return EntryAssistant
	default:
		return EntryUser
	}
}

// SaveRun upserts the RunState snapshot into the turn's state_json, without
// Messages: they are already the turn's session entries (AppendMessages), and
// a resumed run rebuilds them from there (agentcore.LoadRunAuthority replaces
// state.Messages with LoadMessages). Storing them twice doubled the database.
// Old rows that still carry Messages load unchanged.
func (a *AgentStorage) SaveRun(ctx context.Context, state *api.RunState) error {
	if state == nil || state.RunID == "" {
		return errors.New("run state with run id is required")
	}
	snapshot := *state // shallow copy: the caller's state keeps its messages
	snapshot.Messages = nil
	raw, err := json.Marshal(&snapshot)
	if err != nil {
		return fmt.Errorf("encode run state: %w", err)
	}
	return a.store.UpsertTurnState(ctx, state.RunID, raw)
}

// LoadRun returns the saved snapshot. A missing turn or a turn without a saved
// snapshot is a storage miss (os.ErrNotExist), which agentcore treats as "start
// a fresh run" (see isStorageMiss in runtime.go).
func (a *AgentStorage) LoadRun(ctx context.Context, runID string) (*api.RunState, error) {
	turn, err := a.store.GetTurn(ctx, runID)
	if errors.Is(err, ErrTurnNotFound) {
		return nil, fmt.Errorf("load run %q: %w", runID, os.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	if len(turn.StateJSON) == 0 {
		return nil, fmt.Errorf("load run %q: %w", runID, os.ErrNotExist)
	}
	var st api.RunState
	if err := json.Unmarshal(turn.StateJSON, &st); err != nil {
		return nil, fmt.Errorf("decode run state %q: %w", runID, err)
	}
	return &st, nil
}

// AppendMessages writes the batch as user/assistant/tool entries of the turn's
// session in one transaction.
func (a *AgentStorage) AppendMessages(ctx context.Context, runID string, msgs ...api.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	turn, err := a.store.GetTurn(ctx, runID)
	if err != nil {
		return err
	}
	in := make([]NewEntry, 0, len(msgs))
	for _, m := range msgs {
		// EncodeMessage stores a tool result that repeats Content once (P2).
		raw, err := EncodeMessage(m)
		if err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
		in = append(in, NewEntry{TurnID: runID, Type: entryTypeOf(m), JSON: raw})
	}
	_, err = a.store.AppendEntries(ctx, turn.SessionID, in...)
	return err
}

// LoadMessages returns the turn's messages in seq order. An unknown turn is a
// storage miss; a known turn with no messages yet returns an empty slice.
func (a *AgentStorage) LoadMessages(ctx context.Context, runID string) ([]api.Message, error) {
	turn, err := a.store.GetTurn(ctx, runID)
	if errors.Is(err, ErrTurnNotFound) {
		return nil, fmt.Errorf("load messages %q: %w", runID, os.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	entries, err := a.store.ListEntries(ctx, turn.SessionID, EntryFilter{TurnID: runID, Types: messageEntryTypes})
	if err != nil {
		return nil, err
	}
	out := make([]api.Message, 0, len(entries))
	for _, e := range entries {
		var m api.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil {
			return nil, fmt.Errorf("decode message entry %d: %w", e.Seq, err)
		}
		out = append(out, m)
	}
	return out, nil
}
