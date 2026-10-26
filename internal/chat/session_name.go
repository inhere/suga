package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
)

// sessionLine is what chat prints for :session: the session id and, once the
// local session socket is registered, the short name `suga send --session <name>`
// accepts.
func sessionLine(sessionID, shortName string, t i18n.Translator) string {
	line := "session id: " + sessionID
	if shortName != "" {
		line += " · " + trOf(t).T("status.banner.session", map[string]any{"name": shortName})
	}
	return line
}

// resolveChatShortName picks the name this chat process registers under:
//
//   - the session's stored short name, when it has one and no other live chat
//     process uses it (a resume goes on under the same address);
//   - a freshly generated name otherwise.
//
// save asks the caller to store the generated name on the session (only a first
// registration does that). notice explains a stored name that is taken right now
// by another process: this chat then registers under a temporary name and leaves
// the stored one alone, so the next start finds it free again.
func resolveChatShortName(stored string, records []personalprocess.Record, dir string, suffix personalprocess.SuffixSource, t i18n.Translator) (name string, save bool, notice string) {
	taken := personalprocess.TakenNames(records, os.Getpid())
	if stored == "" {
		return personalprocess.NewShortName(dir, taken, suffix), true, ""
	}
	if !taken(stored) {
		return stored, false, ""
	}
	name = personalprocess.NewShortName(dir, taken, suffix)
	return name, false, trOf(t).T("status.session.name_taken", map[string]any{"stored": stored, "name": name}) + "\n"
}

// registerChatProcess registers this chat in the local session socket directory
// (`suga send` and gofer delivery) under the session's short name, and returns
// the server plus the effective name for the banner, `suga ps` and the exit
// summary. The name is generated and stored on the first registration and reused
// by every resume. deliver is where the socket hands an incoming message
// (chatState.deliverRequested, which resolves the request's mode, its message ID and
// the receipt it asked for), and status answers a status query for a message ID of
// this process. The record declares FeatureImages, so a `suga send --image` sender
// knows this receiver takes pictures (design send --image D2).
func registerChatProcess(sessionID string, runner *personal.Runner, deliver personalprocess.DeliverMessageFunc, status personalprocess.StatusFunc, stderr io.Writer, translator i18n.Translator) (*personalprocess.Server, string, error) {
	ctx := context.Background()
	stored, err := runner.SessionShortName(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	records, err := personalprocess.List(runner.ProcessRoot())
	if err != nil {
		return nil, "", err
	}
	cwd, _ := os.Getwd()
	name, save, notice := resolveChatShortName(stored, records, chatNameDir(runner, sessionID, cwd), personalprocess.RandomSuffix, translator)
	// The record declares FeatureImages, so a `suga send --image` sender knows this receiver
	// takes pictures (design send --image D2), and FeatureHops, so a session message sender
	// knows this chat holds a message at the automatic-reply limit instead of starting a turn
	// (design session_message).
	server, err := personalprocess.StartMessages(runner.ProcessRoot(), sessionID, name, cwd,
		[]string{personalprocess.FeatureImages, personalprocess.FeatureHops}, deliver, status)
	if err != nil {
		return nil, "", err
	}
	if save {
		// Losing the stored name only costs a fresh name on the next chat, so a
		// failed write is a warning, not the end of the session.
		if err := runner.SaveSessionShortName(ctx, sessionID, name); err != nil {
			fmt.Fprintf(stderr, "%s\n", trOf(translator).T("status.session.name_save_failed", map[string]any{"name": name, "error": err.Error()}))
		}
	}
	if notice != "" {
		fmt.Fprint(stderr, notice)
	}
	return server, name, nil
}

// chatNameDir is the directory a chat's short name comes from: the workspace
// recorded on the session (config `workspace`, else the directory chat was
// started in, which is what fallback holds). Reading the session must never fail
// the registration, so an error keeps the fallback.
func chatNameDir(runner *personal.Runner, sessionID, fallback string) string {
	model, err := runner.LoadSession(context.Background(), sessionID)
	if err == nil && strings.TrimSpace(model.Cwd) != "" {
		return model.Cwd
	}
	return fallback
}
