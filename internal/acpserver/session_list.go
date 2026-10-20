package acpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// listPageSize is one page of session/list (design 2026-10-20): the client
// walks a long catalog with the returned cursor instead of one huge reply.
const listPageSize = 50

// listMeta separates the meta keys session/list uses on _meta (a client MUST
// not assume their values; only our own client extension reads them).
const (
	metaAll       = "suga/all"
	metaOtherDirs = "suga/otherDirs"
	metaHint      = "suga/hint"
)

// closeWaitTimeout bounds how long session/close waits for a running turn to
// notice its cancellation; the turn cancels on its own context. A variable so
// the close-during-turn tests can shrink it instead of sleeping tens of
// seconds (the behaviour under test is the waiting, not the length).
var closeWaitTimeout = 30 * time.Second

// ListSessions answers session/list: the sessions of the requested directory
// (the request's cwd, else this process's working directory — the directory
// the client started suga in; design 2026-10-20 会话列表默认当前目录), newest
// first, listPageSize per page. Sub-agent sessions and archived ones stay
// hidden. _meta.suga/all lists every directory instead; the response's _meta
// always reports how many sessions the other directories hold, and names the
// way out on an empty result (suga/hint). Only metadata is returned — never
// the conversation content of another directory's session.
func (a *Agent) ListSessions(ctx context.Context, req acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	all := false
	if v, ok := req.Meta[metaAll].(bool); ok && v {
		all = true
	}
	// The directory scope: the request's cwd when given (absolute, like
	// session/new's), else the process working directory.
	scope := ""
	if req.Cwd != nil && strings.TrimSpace(*req.Cwd) != "" {
		if !rootAbs(*req.Cwd) {
			return acp.ListSessionsResponse{}, acp.NewInvalidParams(map[string]any{"cwd": *req.Cwd, "reason": "absolute cwd required"})
		}
		scope = *req.Cwd
	} else if !all {
		if wd, err := os.Getwd(); err == nil {
			scope = wd
		}
	}
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	defer repo.Close()
	sessions, err := repo.ListSessions(ctx) // newest first already
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("list sessions: %w", err)
	}
	filtered := make([]store.Session, 0, len(sessions))
	otherDirs := 0
	for _, s := range sessions {
		if store.IsAgentSession(s) || s.Status == store.SessionArchived {
			continue
		}
		if scope == "" {
			filtered = append(filtered, s)
			continue
		}
		// A session with no recorded directory stays out of the default view
		// like on the CLI (design 2026-10-20); --all is the way to see it.
		if s.Cwd == "" || !personal.SameDir(s.Cwd, scope) {
			otherDirs++
			continue
		}
		filtered = append(filtered, s)
	}
	start := 0
	if req.Cursor != nil {
		if start, err = pageStart(filtered, *req.Cursor); err != nil {
			return acp.ListSessionsResponse{}, acp.NewInvalidParams(map[string]any{"cursor": *req.Cursor, "reason": "unreadable cursor"})
		}
	}
	end := start + listPageSize
	var next *string
	if end < len(filtered) {
		next = strPtr(pageCursor(filtered[end-1]))
	}
	stop := end
	if stop > len(filtered) {
		stop = len(filtered)
	}
	infos := make([]acp.SessionInfo, 0, stop-start)
	for _, s := range filtered[start:stop] {
		info := acp.SessionInfo{SessionId: acp.SessionId(s.ID), Cwd: s.Cwd}
		if title := personal.SessionTitle(s.Title); title != "" {
			info.Title = &title
		}
		if !s.UpdatedAt.IsZero() {
			info.UpdatedAt = strPtr(s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"))
		}
		infos = append(infos, info)
	}
	meta := map[string]any{metaOtherDirs: otherDirs}
	// The hint only rides on a genuinely empty default view: with suga/all the
	// client itself asked for everything, so no directory needs naming.
	if len(infos) == 0 && !all && otherDirs > 0 {
		meta[metaHint] = personal.EmptyScopeHint(scope, otherDirs)
	}
	return acp.ListSessionsResponse{Sessions: infos, NextCursor: next, Meta: meta}, nil
}

// rootAbs reports whether path is an absolute directory path (the shape
// session/new's cwd must have).
func rootAbs(path string) bool {
	return path != "" && (path[0] == '/' || (len(path) > 2 && path[1] == ':'))
}

func strPtr(s string) *string { return &s }

// pageCursor encodes the (updated_at, id) of a page's last session, so the
// next request continues right after it.
func pageCursor(s store.Session) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", s.UpdatedAt.UnixNano(), s.ID)))
}

// pageStart finds the index the next page starts at: the first session after
// the cursor's (updated_at, id) in the newest-first order.
func pageStart(filtered []store.Session, cursor string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("cursor has %d parts", len(parts))
	}
	var unixNano int64
	if _, err := fmt.Sscanf(parts[0], "%d", &unixNano); err != nil {
		return 0, err
	}
	id := parts[1]
	for i, s := range filtered {
		nano := s.UpdatedAt.UnixNano()
		if nano < unixNano || (nano == unixNano && s.ID < id) {
			return i, nil
		}
	}
	return len(filtered), nil
}

// refuseWith returns a refusal (its JSON-RPC class must survive: an
// invalid-params RequestError must not be wrapped away) and surfaces a failed
// runtime close alongside it; a clean close never changes the answer.
func refuseWith(err error, closeErr error) error {
	if closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return err
}

// ResumeSession answers session/resume: the same checks as session/load (the
// session exists, its directory is this request's directory, no turn is
// running) and no history is replayed — the client asked for the session
// without its messages. The response carries the initial modes and the
// config options like new/load do.
func (a *Agent) ResumeSession(ctx context.Context, req acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	cwd, servers, err := sessionInputs(req.Cwd, req.AdditionalDirectories, req.McpServers)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	runtime, err := a.open(ctx, cwd, true, nil, nil)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	// Directory and busy-turn checks are load's: a resumed session keeps its
	// recorded workspace (see the long comment in LoadSession).
	model, loadErr := runtime.Runner.LoadSession(ctx, string(req.SessionId))
	if loadErr != nil {
		return acp.ResumeSessionResponse{}, refuseWith(acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId}), runtime.close())
	}
	if recorded := strings.TrimSpace(model.Cwd); recorded != "" && !personal.SameDir(recorded, cwd) {
		return acp.ResumeSessionResponse{}, refuseWith(cwdMismatchError(req.SessionId, recorded, cwd), runtime.close())
	}
	if err := a.checkNotBusy(req.SessionId); err != nil {
		return acp.ResumeSessionResponse{}, refuseWith(err, runtime.close())
	}
	options := sessionConfigOptions(&runtime, currentModelOf(&runtime), "default")
	if err := runtime.close(); err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	a.registerSession(req.SessionId, cwd, servers, "default")
	return acp.ResumeSessionResponse{Modes: sessionModes("default"), ConfigOptions: options}, nil
}

// checkNotBusy refuses the request while a turn of the session is running.
func (a *Agent) checkNotBusy(id acp.SessionId) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[id]; state != nil && state.cancel != nil {
		return acp.NewInvalidParams(map[string]any{"sessionId": id, "reason": "prompt active"})
	}
	return nil
}

// registerSession keeps the process state of an open session. The always
// rules of a session this process already knew survive the registration (a
// resume after load keeps them); a session the client starts fresh gets none.
func (a *Agent) registerSession(id acp.SessionId, cwd string, servers []mcp.ServerSettings, mode acp.SessionModeId) acp.SessionModeId {
	a.mu.Lock()
	defer a.mu.Unlock()
	modeNow := mode
	if modeNow == "" {
		modeNow = "default"
	}
	always := a.sessions[id].getAlways()
	a.sessions[id] = &sessionState{cwd: cwd, servers: servers, mode: modeNow, always: always}
	return modeNow
}

func (s *sessionState) getAlways() *sessionPolicy {
	if s == nil || s.always == nil {
		return newSessionPolicy()
	}
	return s.always
}

// CloseSession answers session/close (SDK contract): a running turn of the
// session is cancelled — as if session/cancel had been called — and this
// process waits for it to wind down before the session state is released.
// The session itself is not deleted (data governance stays with `suga data
// gc`); a prompt on the closed session is refused with invalid params.
func (a *Agent) CloseSession(ctx context.Context, req acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	state := a.sessions[req.SessionId]
	a.mu.Unlock()
	if state == nil {
		return acp.CloseSessionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId})
	}
	deadline := time.Now().Add(closeWaitTimeout)
	for {
		a.mu.Lock()
		running := state.cancel != nil
		a.mu.Unlock()
		if !running {
			break
		}
		if err := a.Cancel(context.Background(), acp.CancelNotification{SessionId: req.SessionId}); err != nil {
			return acp.CloseSessionResponse{}, fmt.Errorf("cancel the running turn: %w", err)
		}
		if time.Now().After(deadline) {
			return acp.CloseSessionResponse{}, fmt.Errorf("session %s turn did not end within %s", req.SessionId, closeWaitTimeout)
		}
		if err := ctx.Err(); err != nil {
			return acp.CloseSessionResponse{}, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.mu.Lock()
	delete(a.sessions, req.SessionId)
	a.mu.Unlock()
	return acp.CloseSessionResponse{}, nil
}
