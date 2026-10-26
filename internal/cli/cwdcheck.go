package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// resumeCwdAction is what the working-directory check of a session this
// invocation continues decided the caller should do.
type resumeCwdAction int

const (
	// cwdAgreed: the session and this invocation work in the same directory, or
	// there was nothing to compare (a new session, an old one without a recorded
	// directory, or a handoff).
	cwdAgreed resumeCwdAction = iota
	// cwdUseCurrent: continue in this directory; sessions.cwd is moved here by
	// the caller (chat's second choice, or --allow-cwd-change).
	cwdUseCurrent
	// cwdUseSession: this process was moved into the session's directory
	// (chat's first choice), so everything below runs there.
	cwdUseSession
	// cwdCancelled: the person chose to leave (chat's third choice): nothing is
	// opened and nothing is written.
	cwdCancelled
)

// guardResumeCwd compares the working directory recorded on the session this
// invocation continues (sessions.cwd) with current, the directory it would work
// in. It runs before the runtime is assembled, so no tool call can see the
// wrong directory and no turn is started for a continue that is refused.
//
// On a mismatch a terminal chat asks what to do (interactive), everything else
// fails: run and a chat without a terminal (a pipe, a job runner) exit with an
// error naming both directories and the two ways on, `cd <directory>` or
// --allow-cwd-change. That flag, like chat's second choice, continues in the
// current directory and reports cwdUseCurrent. An unknown session and a session
// without a recorded directory are left to the normal flow (the runner fills an
// empty cwd in on the next turn).
func guardResumeCwd(ctx context.Context, opts personalOptions, current string,
	interactive bool, stdin io.Reader, stderr io.Writer) (resumeCwdAction, error) {
	return guardResumeCwdWithTranslator(ctx, opts, current, interactive, stdin, stderr, cliTranslator())
}

func guardResumeCwdWithTranslator(ctx context.Context, opts personalOptions, current string,
	interactive bool, stdin io.Reader, stderr io.Writer, tr i18n.Translator) (resumeCwdAction, error) {
	if opts.FromID != "" {
		// A handoff starts a new session; its directory is the one this process
		// works in, and the source session is only read.
		return cwdAgreed, nil
	}
	sessionID := opts.ResumeID
	if sessionID == "" {
		sessionID = opts.SessionID
	}
	if sessionID == "" {
		return cwdAgreed, nil
	}
	recorded, err := recordedSessionCwd(ctx, sessionID)
	if err != nil {
		return cwdAgreed, err
	}
	if strings.TrimSpace(recorded) == "" || personal.SameDir(recorded, current) {
		return cwdAgreed, nil
	}
	if opts.AllowCwdChange {
		return cwdUseCurrent, nil
	}
	if !interactive {
		return cwdAgreed, cwdMismatchErrorWithTranslator(sessionID, recorded, current, tr)
	}
	return askResumeCwdWithTranslator(sessionID, recorded, current, stdin, stderr, tr)
}

// recordedSessionCwd reads the working directory stored on a session. A missing
// session is not an error here: opening it reports it (exit code 4 with
// "session not found") and a --session-id that does not exist yet is simply a
// new session.
func recordedSessionCwd(ctx context.Context, sessionID string) (string, error) {
	repo, err := openCoreStore()
	if err != nil {
		return "", err
	}
	defer repo.Close()
	model, err := repo.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrSessionNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return model.Cwd, nil
}

// cwdMismatchError is the failure of a continue in another directory without a
// terminal to ask at: it names both directories and both ways to go on.
func cwdMismatchError(sessionID, recorded, current string) error {
	return cwdMismatchErrorWithTranslator(sessionID, recorded, current, cliTranslator())
}

func cwdMismatchErrorWithTranslator(sessionID, recorded, current string, tr i18n.Translator) error {
	dir := recorded
	if !isDir(recorded) {
		dir += cliText(tr, "cli.cwd.gone")
	}
	return fmt.Errorf("%s", cliText(tr, "cli.cwd.mismatch", map[string]any{"session": sessionID, "recorded": dir, "current": current}))
}

// askResumeCwd asks at the terminal what to do with a session created in
// another directory: continue in the session's directory (this process moves
// there before the runtime is assembled, so the workspace, the shell and the
// session's short name all follow), continue here (the recorded directory is
// moved), or cancel without touching anything. The three answers are the only
// accepted input; EOF (Ctrl-D) cancels, like the third one.
func askResumeCwdWithTranslator(sessionID, recorded, current string, stdin io.Reader, stderr io.Writer, tr i18n.Translator) (resumeCwdAction, error) {
	// The directory is checked once: the question itself says when going back is
	// no longer possible.
	back := isDir(recorded)
	fmt.Fprintln(stderr, cliText(tr, "cli.cwd.header", map[string]any{"session": sessionID, "recorded": recorded, "current": current}))
	if back {
		fmt.Fprintln(stderr, "  "+cliText(tr, "cli.cwd.choice.back", map[string]any{"recorded": recorded}))
	} else {
		fmt.Fprintln(stderr, "  "+cliText(tr, "cli.cwd.choice.back_gone", map[string]any{"recorded": recorded}))
	}
	fmt.Fprintln(stderr, "  "+cliText(tr, "cli.cwd.choice.current", map[string]any{"current": current}))
	fmt.Fprintln(stderr, "  "+cliText(tr, "cli.cwd.choice.cancel"))
	for {
		fmt.Fprint(stderr, cliText(tr, "cli.cwd.prompt")+" ")
		answer, err := readChoice(stdin)
		switch strings.TrimSpace(answer) {
		case "1":
			if !back {
				fmt.Fprintln(stderr, cliText(tr, "cli.cwd.unavailable", map[string]any{"recorded": recorded}))
				continue
			}
			if cerr := os.Chdir(recorded); cerr != nil {
				return cwdAgreed, fmt.Errorf("%s", cliText(tr, "cli.cwd.chdir_failed", map[string]any{"recorded": recorded, "error": cerr}))
			}
			return cwdUseSession, nil
		case "2":
			return cwdUseCurrent, nil
		case "3":
			return cwdCancelled, nil
		}
		if err != nil {
			// End of input (Ctrl-D): leave, like the third answer.
			return cwdCancelled, nil
		}
		fmt.Fprintln(stderr, cliText(tr, "cli.cwd.invalid"))
	}
}

// readChoice reads one line of the terminal by the byte, so nothing typed after
// it is swallowed by a buffer chat would then never see.
func readChoice(stdin io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			switch buf[0] {
			case '\r', '\n':
				return b.String(), nil
			case 0x7f, '\b': // a typo may be corrected before Enter
				s := b.String()
				if s != "" {
					b.Reset()
					b.WriteString(s[:len(s)-1])
				}
			default:
				b.WriteByte(buf[0])
			}
		}
		if err != nil {
			return b.String(), err
		}
	}
}

// isDir reports whether path is an existing directory (the session's recorded
// directory may be gone: another machine, a rename, a deleted checkout).
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// effectiveWorkspace is the directory this invocation works in, resolved the
// way wiring.OpenCLI resolves the tool workspace: the configured `workspace`,
// else the directory the process was started in.
func effectiveWorkspace() (string, error) {
	abs, err := filepath.Abs(appconfig.ResolveWorkspacePath())
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	return abs, nil
}

// isTerminalInput reports whether stdin is a terminal; chat's own input source
// makes the same distinction (a pipe is not asked a question).
func isTerminalInput(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
