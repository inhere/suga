package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
)

// sessionMessageRoot isolates SUGA_HOME for the messenger and returns the local
// process directory inside it.
func sessionMessageRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	root := filepath.Join(home, "run")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(root, 0o700)))
	return root
}

// writeSessionRecord writes a process record the way a running chat leaves it.
func writeSessionRecord(t *testing.T, root, sessionID, name string, features []string) personalprocess.Record {
	t.Helper()
	pid := os.Getpid()
	record := personalprocess.Record{
		PID: pid, SessionID: sessionID, Name: name, CWD: t.TempDir(), Kind: "chat",
		Socket: filepath.Join(root, fmt.Sprintf("%d.sock", pid)), Host: "test", State: "idle",
		Features: features,
	}
	data, err := json.Marshal(record)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.json", pid)), data, 0o600)))
	return record
}

// TestSessionMessengerStatusQueriesTheReceiver: `session_message action=status`
// reaches the live receiver over the session socket and answers its state table; an
// ID the receiver never saw is reported as unknown instead of guessed.
func TestSessionMessengerStatusQueriesTheReceiver(t *testing.T) {
	root := sessionMessageRoot(t)
	server, err := personalprocess.StartMessages(root, "checkout-at-3", "suga-k7", t.TempDir(), nil, nil,
		func(id string) (string, bool) {
			if id == "known" {
				return personalprocess.StateProcessed, true
			}
			return "", false
		})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })

	messenger := &sessionMessenger{root: root}
	state, err := messenger.Status(context.Background(), "suga-k7", "known")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, personalprocess.StateProcessed, state)
	state, err = messenger.Status(context.Background(), "suga-k7", "lost")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "", state)
}

// TestSessionMessengerPeersSkipThisProcess: the list never offers the session of
// this very process (a chat listing itself), and a peer declares whether its
// version understands the hop limit.
func TestSessionMessengerPeersSkipThisProcess(t *testing.T) {
	root := sessionMessageRoot(t)
	messenger := &sessionMessenger{root: root}
	// The record of a peer needs another live process, which a unit test does not
	// have: the own record is what must disappear here.
	writeSessionRecord(t, root, "own-session", "suga-own", []string{personalprocess.FeatureHops})
	peers, err := messenger.Peers(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 0, len(peers))
}

// TestSessionMessengerSendRefusesSelfAndUnknown: a message to itself is refused
// before anything is delivered, and an unknown selector names the way to look the
// sessions up.
func TestSessionMessengerSendRefusesSelfAndUnknown(t *testing.T) {
	root := sessionMessageRoot(t)
	messenger := &sessionMessenger{root: root}
	ctx := context.Background()
	t.Run("self", func(t *testing.T) {
		writeSessionRecord(t, root, "own-session", "suga-own", []string{personalprocess.FeatureHops})
		_, err := messenger.Send(ctx, "suga-own", "hi", personalprocess.ModeFollowUp)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "不能把消息发给自己")
	})
	t.Run("unknown", func(t *testing.T) {
		_, err := messenger.Send(ctx, "suga-nowhere", "hi", personalprocess.ModeFollowUp)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "不存在或已退出")
		assert.StrContains(t, err.Error(), "action=list")
	})
}

// TestSessionMessageRequestCarriesSenderAndHops: the request names the sender's
// full session ID and short name, and it is one hop deeper than the turn it is
// sent in (a person's turn sends 1, a hops-2 turn 3).
func TestSessionMessageRequestCarriesSenderAndHops(t *testing.T) {
	sender := personalprocess.Record{SessionID: "own-session", Name: "suga-own"}
	cases := []struct {
		name string
		hops int
		self string
		want int
	}{
		{"person's turn", 0, "own-session", 1},
		{"session message turn", 2, "own-session", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := personal.WithTurnHops(context.Background(), tc.hops)
			req := sessionMessageRequest(ctx, sender, tc.self, "hi", personalprocess.ModeFollowUp)
			assert.Eq(t, tc.want, req.Hops)
			assert.Eq(t, personalprocess.SessionFromPrefix+"own-session", req.From)
			assert.Eq(t, "suga-own", req.FromName)
			assert.Eq(t, personalprocess.ModeFollowUp, req.Mode)
		})
	}
	// Without a record of its own process the session of ctx names the sender.
	req := sessionMessageRequest(context.Background(), personalprocess.Record{}, "ctx-session", "hi", personalprocess.ModeSteer)
	assert.Eq(t, personalprocess.SessionFromPrefix+"ctx-session", req.From)
	assert.Eq(t, "", req.FromName)
	assert.Eq(t, 1, req.Hops)
}

// TestNewSessionMessengerFollowsTheSwitch: the tool is on by default and turned
// off by tools.session_message.enabled=false or SUGA_SESSION_MESSAGE=off.
func TestNewSessionMessengerFollowsTheSwitch(t *testing.T) {
	sessionMessageRoot(t)
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NotNil(t, newSessionMessenger())
	t.Run("config off", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", false)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.enabled", true) })
		assert.Nil(t, newSessionMessenger())
	})
	t.Run("env off", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "off")
		assert.Nil(t, newSessionMessenger())
	})
}
