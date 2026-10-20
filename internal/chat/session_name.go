package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
)

// sessionLine is what chat prints for :session: the session id and, once the
// local session socket is registered, the short name `suga send --session <name>`
// accepts.
func sessionLine(sessionID, shortName string) string {
	line := "session id: " + sessionID
	if shortName != "" {
		line += " · 会话 " + shortName
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
func resolveChatShortName(stored string, records []personalprocess.Record, dir string, suffix personalprocess.SuffixSource) (name string, save bool, notice string) {
	taken := personalprocess.TakenNames(records, os.Getpid())
	if stored == "" {
		return personalprocess.NewShortName(dir, taken, suffix), true, ""
	}
	if !taken(stored) {
		return stored, false, ""
	}
	name = personalprocess.NewShortName(dir, taken, suffix)
	return name, false, fmt.Sprintf("提示：会话短名称 %s 已被另一个进程占用，本进程临时使用 %s（会话里保存的名称不变，下次启动仍用 %s）。\n", stored, name, stored)
}

// registerChatProcess registers this chat in the local session socket directory
// (`suga send` and gofer delivery) under the session's short name, and returns
// the server plus the effective name for the banner, `suga ps` and the exit
// summary. The name is generated and stored on the first registration and reused
// by every resume. deliver is where the socket hands an incoming message
// (chatState.deliverMessage, which resolves the request's mode). The record
// declares FeatureImages, so a `suga send --image` sender knows this receiver
// takes pictures (design send --image D2).
func registerChatProcess(sessionID string, runner *personal.Runner, deliver personalprocess.DeliverImagesFunc, stderr io.Writer) (*personalprocess.Server, string, error) {
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
	name, save, notice := resolveChatShortName(stored, records, chatNameDir(runner, sessionID, cwd), personalprocess.RandomSuffix)
	// The record declares FeatureImages, so a `suga send --image` sender knows this receiver
	// takes pictures (design send --image D2), and FeatureHops, so a session message sender
	// knows this chat holds a message at the automatic-reply limit instead of starting a turn
	// (design session_message).
	server, err := personalprocess.StartWaitImages(runner.ProcessRoot(), sessionID, name, cwd,
		[]string{personalprocess.FeatureImages, personalprocess.FeatureHops}, deliver)
	if err != nil {
		return nil, "", err
	}
	if save {
		// Losing the stored name only costs a fresh name on the next chat, so a
		// failed write is a warning, not the end of the session.
		if err := runner.SaveSessionShortName(ctx, sessionID, name); err != nil {
			fmt.Fprintf(stderr, "提示：会话短名称 %s 未能保存（%v），下次启动会重新生成。\n", name, err)
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
