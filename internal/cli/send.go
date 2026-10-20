package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gookit/goutil/cflag"
	"github.com/gookit/goutil/cflag/capp"
	personalprocess "github.com/inhere/suga/internal/personal/process"
)

// maxSendStdinBytes caps `suga send` stdin; exit codes: 2 ambiguous, 3 offline, 4 too large.
const maxSendStdinBytes = 1 << 20

// sendHelp documents the three text sources of `suga send`, including the way
// to pass text that would otherwise be read as an option, what --session
// accepts, and what --steer and --wait wait for.
const sendHelp = `The message text comes from arguments, --text, or stdin; the first source that is
given wins, and arguments cannot be combined with --text:
  suga send --session <id|name> 你好 世界      arguments, joined with a single space
  suga send --session <id|name> --text 你好    the --text option
  echo 你好 | suga send --session <id|name>    stdin, when neither of the above is used
Options may be written before or after the arguments. Everything after a "--"
separator counts as text, which is how to pass text that starts with "-":
  suga send --session <id|name> -- -hello

--session takes a session ID, a session ID prefix, or the short name of a
running chat (suga ps, like suga-k7); the full ID wins over a short name, which
wins over a prefix. A selector that matches several processes exits 2.

--steer inserts the text into the turn running right now (chat.steering on,
which is the default): the model sees it before its next request. With no turn
running, with chat.steering off, or when the turn is already ending, the text is
queued for the next turn instead and the command says so. --wait keeps the
connection until the message's fate is known: it returns 0 once the message
reached the model or starts the turn of its own, and exits 3 when the message is
discarded (the turn it was inserted into was cancelled or failed, or chat
exited). Both options leave the text a plain message: it never runs as a command.

Without --wait a queued message returns as soon as it is queued, so an automated
sender never blocks for the length of the chat's current turn. A message
discarded on the way (chat exiting) is reported then; chat prints how many it
dropped.`

// sendExamples is shown under "Examples:" by `suga send --help`.
const sendExamples = `suga send --session 1a2b 你好 世界
suga send --session suga-k7 你好 世界
suga send --session 1a2b --text 你好
echo 你好 | suga send --session 1a2b
suga send --session 1a2b --wait 你好
suga send --session 1a2b --steer --wait 只改 README，不要动配置
suga send --session 1a2b -- -hello`

func newSendCommand(stdin io.Reader, stderr io.Writer) *capp.Cmd {
	var sendSession, sendText, sendSource string
	var sendWait, sendSteer bool
	var sendImages []string
	send := capp.NewCmd("send", "Send text to a local Suga chat process", func(c *capp.Cmd) error {
		// The session check runs before any stdin read, so a missing --session
		// fails at once instead of blocking on input.
		if strings.TrimSpace(sendSession) == "" {
			return fmt.Errorf("--session is required")
		}
		text, err := sendMessageText(sendText, c.RemainArgs(), stdin)
		if err != nil {
			return err
		}
		root, err := localProcessRoot()
		if err != nil {
			return err
		}
		records, err := personalprocess.List(root)
		if err != nil {
			return err
		}
		record, err := personalprocess.Resolve(records, sendSession)
		if errors.Is(err, personalprocess.ErrNotOnline) {
			return processExitError{code: 3, err: err}
		}
		if errors.Is(err, personalprocess.ErrAmbiguous) {
			return processExitError{code: 2, err: err}
		}
		if err != nil {
			return err
		}
		// --steer asks for the running turn (falling back to the queue for the next
		// turn); without it the message always waits for its own turn.
		mode := personalprocess.ModeFollowUp
		if sendSteer {
			mode = personalprocess.ModeSteer
		}
		// The pictures come first: a target that cannot take them fails the whole
		// message before anything is delivered (design send --image D2/D3).
		images, err := sendImageRefs(record, sendImages)
		if err != nil {
			return err
		}
		// --wait keeps the connection until the message reached the model or starts a
		// turn (or was discarded, exit 3). Without it a queued message returns at
		// once, so an automated sender never blocks for the length of the current turn.
		response, err := personalprocess.Send(record, personalprocess.Request{Text: text, From: sendSource, Wait: sendWait, Mode: mode, Images: images})
		if errors.Is(err, personalprocess.ErrNotOnline) {
			return processExitError{code: 3, err: err}
		}
		if errors.Is(err, personalprocess.ErrQueueFull) {
			// The server answered queue_full: the inbox of the running turn, or the queue
			// of messages waiting for the next turn, is full (design D11).
			return processExitError{code: 1, err: fmt.Errorf("queue_full: %w", err)}
		}
		if errors.Is(err, personalprocess.ErrImagesUnsupported) {
			// The receiver does not know Request.Images at all: nothing of the message
			// was taken (design send --image D2).
			return processExitError{code: 5, err: fmt.Errorf("目标会话 %s（进程 %d）的 suga 版本不支持附图，未投递消息；请在新版本里重新打开该会话：%w",
				record.SessionID, record.PID, err)}
		}
		if err != nil {
			return err
		}
		if response.Warning != "" {
			// Accepted with a caveat (the model cannot see pictures, so they became a
			// text note): the sender shows it and still exits 0.
			fmt.Fprintln(stderr, "提示："+response.Warning)
		}
		switch {
		case response.Steered && !sendWait:
			// The message went into the turn running right now (no --wait: the ack is
			// all the sender asked for).
			fmt.Fprintln(stderr, "steered")
		case response.Queued && !sendWait:
			if sendSteer {
				// A --steer that met no turn it could be inserted into is queued for the
				// next turn, like a plain delivery.
				fmt.Fprintln(stderr, "未插入当前回合，已排队")
			} else {
				fmt.Fprintln(stderr, "queued")
			}
		default:
			fmt.Fprintln(stderr, "delivered")
		}
		return nil
	})
	send.SetOutput(stderr)
	send.Usage = "suga send --session <id|name> [--text <text> | <text>...] [--source <label>] [--steer] [--wait]"
	send.Example = sendExamples
	send.LongHelp = sendHelp
	send.StringVar(&sendSession, "session", "", "session ID, ID prefix, or the short name of a running chat (suga ps)")
	send.StringVar(&sendText, "text", "", "message text; cannot be combined with arguments; defaults to stdin{[]}")
	send.StringVar(&sendSource, "source", "cli", "source label the chat shows before the text as [来自 <source>]; text already starting with a [...] tag is shown as is{[]}")
	send.BoolVar(&sendWait, "wait", false, "wait until the message reaches the running turn or starts a turn; exit 3 if it is discarded{[]}")
	send.BoolVar(&sendSteer, "steer", false, "insert the text into the turn running right now; queue it for the next turn when there is none{[]}")
	send.Var((*cflag.Strings)(&sendImages), "image", "image file delivered with the message (repeatable, at most 16; same checks and scaling as chat :image)")
	return send
}

// sendMessageText resolves the message from the three accepted sources: the
// positional arguments, the --text option, or stdin. Arguments and --text are
// mutually exclusive, so a typo cannot silently drop half of the message; when
// neither is given (or the arguments are all blank, like a quoted "") the text
// is read from stdin up to maxSendStdinBytes.
func sendMessageText(textFlag string, args []string, stdin io.Reader) (string, error) {
	// A run of blank arguments counts as absent, so `--text ""` and `suga send
	// --session <id> ""` both fall through to stdin instead of sending nothing.
	positional := strings.Join(args, " ")
	if strings.TrimSpace(positional) == "" {
		positional = ""
	}
	if positional != "" && textFlag != "" {
		return "", fmt.Errorf("arguments and --text are mutually exclusive: pass the text as arguments or with --text, not both")
	}
	if positional != "" {
		return positional, nil
	}
	if textFlag != "" {
		return textFlag, nil
	}
	// Read one byte past the cap so oversize input is rejected, never truncated.
	data, err := io.ReadAll(io.LimitReader(stdin, maxSendStdinBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSendStdinBytes {
		return "", processExitError{code: 4, err: fmt.Errorf("stdin exceeds the %d byte (1 MiB) limit; nothing was sent", maxSendStdinBytes)}
	}
	return string(data), nil
}
