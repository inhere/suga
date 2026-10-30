#!/usr/bin/env python3
"""Steering: inserting a message into a running turn, and ending one turn only.

chat.steering (on by default) keeps the input line available while a turn runs:
Enter inserts a message into the running turn, Esc or a single Ctrl-C end this
turn only. The scenarios (each on its own scratch home and fake model):

  same-turn  a shell_exec runs for a few seconds; the message typed while it runs
             reaches the *same* turn: the request after the tool result carries
             it (fakellm's request log records the last messages of every
             request), the turn ends with one turn event and a steer delivered
             event, and the end-of-turn line counts the message.
  paste      two lines pasted into the running turn: the paste stays one piece on
             the input line (its line break submits nothing), and only the
             person's own Enter sends it — the request after the tool result
             carries both lines as ONE user message (fakellm's request log), and
             the trace records one delivery.
  next-turn  the message arrives while the model answers (text mode, a slow
             reply): the answer stands and the message becomes the next turn at
             once (design D3) - the second request carries it as its prompt, the
             trace records it returned (completed_untaken, next_turn).
  followup   Tab on the line typed while the model answers queues it instead
             (suga-8pug): chat shows [排队] …（本轮结束后发送）, the line leaves the
             input line, the running turn's request never carries it, and once
             that turn ends the queued line runs at once as the next turn (the
             second request's prompt, shown as [排队] …（已发送）).
  abort      Esc ends the turn only: chat goes on, the message waits in the input
             line and runs only when the person submits it (Enter); a single
             Ctrl-C ends a turn the same way, two of them leave chat.
  clear      Ctrl-C on a non-empty input line drops the line and nothing else
             (wll1): the running turn is not ended, nothing is inserted, and the
             message typed after the press goes into the same turn - the text the
             press dropped is not part of it. An empty line keeps the old
             meaning: Ctrl-C then ends this turn only.
  send       `suga send --steer` from outside (the way gofer delivers): while a
             tool runs the first response is `steered` and the message reaches
             the *same* turn's next request; `--steer --wait` returns 0 before
             that turn ended (the model is still waiting to answer); an idle chat
             answers `queued` (the person sees 未插入当前回合，已排队); a turn ended
             with Esc discards the message (`--wait` exits 3, the message is back
             in the input line); a ninth message is refused with queue_full.
  withdraw   Up on the empty input line of a running turn takes the messages
             still waiting to be delivered back: they are shown as withdrawn, sit
             on the input line as one line each, in order, and the request after
             the tool result carries neither of them; a `suga send --steer`
             message is left alone (its sender waits) and the text edited out of
             what came back can be submitted again into the same turn (shown as
             waiting, then delivered, and counted on the end-of-turn line).
  approval   typing at an approval question is the answer, never an inserted
             message: no 插话 line, no request carrying that text, no steer event.
  status     what the turn does right now is on the row right above the input line
             (spinner + 思考中… / 执行 shell_exec… + elapsed seconds, refreshed in
             place while a message is typed), the whole turn through: it appears
             while the model is asked, names the tool that runs, goes back to the
             model's wording once the result is in, keeps its row while the answer
             streams and is gone when the turn ends (the ↳ line hands the half-typed
             text to the ❯ prompt). A colored terminal is what turns it on, so this
             and the next scenario drop NO_COLOR.
  status-off chat.spinner: false shows no status line either, in a colored terminal.
  screen     a long, overwide reply streams in slowly while a message is typed:
             the minimal VT screen model (ptydrive.Screen) says the reply keeps
             its rows, whole, and the input line stays one line of its own - the
             check for "only complete lines leave" (r3 M1), for the terminal
             width the line is laid out with, and for the waiting line being off.
  hook       a PostToolUse hook writes a line straight to the terminal and one
             to stderr (chat prints the latter as a notice) while a message is
             typed: the input line keeps its place (task 4/N4, r3 M1).
  off        chat.steering: false keeps the old behaviour: no ↳ prompt during the
             turn, nothing is read, a single Ctrl-C still ends the chat.

  python3 -I scripts/ptysmoke/chat_steer.py --bin tmp/smoke/suga

Reverse verification (each check must fail on purpose), verified 2026-10-15 in a
Linux container:
- drop the Runner.Steer call in turnInput.insert: same-turn fails (the request
  after the tool result carries no inserted message) and next-turn fails.
- run the turn with the chat's context instead of a derived one, or drop the
  cancel in turnInput.endTurn: abort fails (the turn ends the chat, no 已中断本轮).
- drop PrependLine from putSteersBack: abort fails (the message is gone from the
  input line, Enter runs nothing and the second request never comes).
- let a turn keep the terminal cooked (SetBusy(true) without the raw mode steering
  on enters, or turnActive left set): paste fails, the turn no longer asks the
  terminal to bracket pastes (and a real terminal would send no paste markers at
  all), and the line break of the paste is no longer one piece of text.
- with steering on, drop shell_exec's cmd.Cancel: abort fails, the tool's sleep
  keeps the turn running (in a PTY Ctrl-C no longer reaches the foreground
  process group; the turn's context is what ends the tool).
- drop the generation check in turnInput.handle: nothing here fails, it is a race
  guard (its unit tests are in internal/chat/steering_test.go).
- whole lines off (LiveText.SetWholeLines left a no-op): screen fails, the reply's
  partial line is written next to the input line and the model's rows split.
- no SetSize (the line editor back at its 80-column default): screen fails, the
  rows of a reply wrapped at the pty's 60 columns are counted wrongly.
- no restoreOutputProcessing (OPOST|ONLCR left off in raw mode): hook fails, the
  line the hook wrote straight to the terminal keeps its column and the input line
  is repainted into it.

Reverse verification of the status line (each check must fail on purpose):
- give Renderer.startThinking its early return back (or drop the SetLabel calls):
  status fails - the row above the input line never appears (no frame anywhere on
  screen), while the input line checks still pass.
- drop lineedit's status row from Write (the output repaints the prompt without
  clearing the status row): status fails, the status row is left in the output area.
- drop the SetStatus on the tool call (the label stays 思考中…): status fails, the
  row never names shell_exec while the tool runs.
- drop the SetStatus("") of Stop (the end of a turn): status fails, the row is still
  there when the turn is over and above the ❯ prompt.

Reverse verification of the send scenario (removing the steer path makes it fail):
- drop the ModeSteer branch of chatState.deliverMessage (`suga send --steer`
  always queues): send fails - the first response is `queued` instead of
  `steered`, the `--wait` sender returns only after the turn ended (its own turn
  starts then), and the trace shows a `completed_untaken` return instead of a
  delivery.
- drop the ErrDiscarded settling of a cancelled turn's returned messages: send
  fails, the `--wait` sender is never answered (the check times out).
- drop the ErrSteerQueueFull branch (queue the message instead): send fails, the
  ninth message is answered `queued` and queue_full never appears.

Reverse verification of the withdraw scenario (to run in the Linux container; the
machine this was written on has no pty, so it is UNVERIFIED here):
- Runner.WithdrawSteers returning nil (or dropping lineedit's WithdrawCallback
  call in the keyUp case): withdraw fails - nothing is shown as 已撤回, the input
  line stays empty, and the request after the tool result carries both messages.
- letting the withdrawal take every source (WithdrawSteers not narrowing to
  SteerSourceKeyboard): withdraw fails, the `suga send --steer` message comes
  back onto the input line too instead of being delivered.
- dropping the len(t.line) == 0 guard in the keyUp case: withdraw fails, Up on
  the line holding both texts walks the history instead of taking the messages
  back (and a second Up fills a different line).

Reverse verification of the clear scenario (also to run in the Linux container;
UNVERIFIED here):
- dropping the non-empty branch of the Ctrl-C case in lineedit's readLine (the
  t.ctrlCClearsLine one): clear fails - the press ends the turn (已中断本轮), the
  input line keeps the dropped text, and the row check right after the press says
  the line is not empty.
- handing the editor a plain Ctrl-C (SetCtrlCClearsLine(true) for every read):
  the clear scenario still passes, but chat_tool_ctrlc.py's single Ctrl-C and
  chat_approval.py's question checks are what catch that - a question would let
  the press drop the typed answer instead of cancelling itself.

Reverse verification of the followup scenario (suga-8pug; to run in the Linux
container, UNVERIFIED here):
- dropping the Tab branch of installCompletion's AutoCompleteCallback: followup
  fails - no 排队 line ever shows and the request after the answer is never the
  typed line (the text stays on the input line instead).
- leaving source.setFollowUp out of startTurnInput (or dropping the queue drain in
  chatState.runTurn): followup fails the same way - nothing is queued, or the
  queued line never runs as a turn.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fakellm  # noqa: E402
from ptydrive import CTRL_C, PtySession, Screen, input_row, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_approval import trace_events  # noqa: E402

STEER_PROMPT = r"↳ "
# The prompt as the text it is (an exact row of the screen model).
STEER = "↳ "
PROMPT = "❯ "
STARTED = "steer-tool-started"
# The tool whose result a scenario waits for (the model is asked again after it).
QUICK_TOOL = "touch {marker}; sleep 4; echo TOOL_DONE"
# The tool that keeps running until the turn is ended.
SLOW_TOOL = "touch {marker}; sleep 60; echo TOOL_DONE"
# The tool of the paste scenario: long enough for the paste and its checks to
# happen while it runs (the model is asked again once it ends).
PASTE_TOOL = "touch {marker}; sleep 10; echo TOOL_DONE"
# The tool of the send scenario: long enough for the CLI and its checks to run
# while it works, so a `send --steer` arrives well inside the turn.
SEND_TOOL = "touch {marker}; sleep 6; echo TOOL_DONE"
# The tool of the withdraw scenario: long enough for typing two messages, a
# `send --steer`, the Up key and an edit to happen while it runs.
WITHDRAW_TOOL = "touch {marker}; sleep 25; echo TOOL_DONE"
# The tool of the clear scenario: long enough for a message to be typed, dropped
# with Ctrl-C and typed again while it runs.
CLEAR_TOOL = "touch {marker}; sleep 20; echo TOOL_DONE"
# The text the press drops, and the message typed in its place.
CLEAR_DROPPED = "这行要被清掉"
CLEAR_TEXT = "只改 README"
# The clear scenario's pty width: the screen model lays the rows out with it (the
# input row is read off the screen to tell a cleared line from one with text).
CLEAR_COLS = 100
# The two messages typed into the running turn that the scenario takes back with
# Up, and the word added to them before submitting the edited text again.
WITHDRAW_TEXT = "撤回这一句"
WITHDRAW_TEXT2 = "还有这一句"
WITHDRAW_EDIT = "补充说明"
# The edited text as chat shows it (the editor's line break collapsed, like every
# inserted message on its own line).
WITHDRAW_EDITED = WITHDRAW_TEXT + " " + WITHDRAW_TEXT2 + WITHDRAW_EDIT
# The tool of the status scenario: long enough for the frame, screen and typing
# checks of that scenario to happen while it runs.
STATUS_TOOL = "touch {marker}; sleep 8; echo TOOL_DONE"
# The texts `suga send --steer` carries in the send scenario. chat labels every
# message from elsewhere with its source (deliveryLine, --source defaults to cli).
SEND_TEXT = "只改 README"
SEND_TEXT2 = "再改一处"
SEND_TEXT3 = "别改了"
SEND_TEXT4 = "空闲时的话"
SEND_PREFIX = "[来自 cli] "
# The terminal bracketing chat asks for while a line is editable
# (lineedit.SetBracketedPasteMode): its marker ESC[?2004h, and the two lines the
# paste scenario types into the running turn.
PASTE_ON = r"\x1b\[\?2004h"
PASTE_TEXT = "第一行\n第二行"
# The pty window the screen scenario runs in: each reply line is wider than it, so
# the terminal wraps the line and the chat has to know that width (SetSize).
PTY_COLS = 60
# The status scenario's answer: several lines, streamed in small chunks (--chunk),
# so the status line's row can be looked at while the answer arrives.
STATUS_REPLY = "STEER_OK\n第二行\n第三行"
# The rows of the screen model have their trailing spaces removed: the input line of
# an empty one is the prompt alone.
STEER_EMPTY = STEER.rstrip()
# One frame of the status line (spinner.go): the animation character, what the turn
# waits for and the elapsed seconds. The character of a given frame is not
# predictable (the animation advances per frame), so any of them is accepted.
FRAME = r"[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]"
# A status row of any kind: the whole line, as the screen model shows it (the color
# codes are stripped).
STATUS_FRAME = FRAME + r" (?:思考中|执行 [a-z_]+)… \d+s"
LONG = "STEER_LONG"
LONG_LINES = 3
# Not a multiple of PTY_COLS, and neither is a line with the ▣ prefix: a line that
# ends exactly at the right edge makes the row count ambiguous (a terminal holds the
# wrap back until the next character), which is not what this scenario is about.
LONG_WIDE = 175
# The long reply of the screen scenario, built by fakellm itself (--lines/--wide),
# so the expectation cannot drift from the answer.
LONG_REPLY = fakellm.build_reply(argparse.Namespace(mode="text", reply=LONG, lines=LONG_LINES, wide=LONG_WIDE))
# What the hook scenario's hook writes while the turn runs: a line straight to the
# terminal (a bare newline) and one to stderr, which chat prints as a notice.
HOOK_TTY = "HOOK_TTY_LINE"
HOOK_TTY2 = "HOOK_TTY_NEXT"
HOOK_NOTICE = "HOOK_NOTICE_LINE"
HOOK_SCRIPT = '''import json, sys
event = json.loads(sys.stdin.readline() or "{}")
name = event.get("hook_event_name") or event.get("name") or event.get("Name") or ""
if name == "PostToolUse":
    # A bare newline written to the terminal straight: the chat's raw input mode
    # has to keep the output post-processing on (OPOST|ONLCR), or the line keeps
    # its column and the input line is repainted into it.
    with open("/dev/tty", "w", encoding="utf-8") as tty:
        tty.write(%r + "\\n" + %r + "\\n")
    sys.stderr.write(%r + "\\n")
print(json.dumps({"hookSpecificOutput": {"additionalContext": "SMOKE hook context"}}))
''' % (HOOK_TTY, HOOK_TTY2, HOOK_NOTICE)


def wrapped_rows(text, cols, prefix=""):
    """The screen rows a text of whole lines takes on a cols column screen: the
    prefix marks the start of the model message (its first line only, like LiveText
    prints it) and the terminal wraps every line at the window width."""
    rows = []
    for line in (prefix + text).split("\n"):
        rows += [line[i:i + cols] for i in range(0, len(line), cols)]
    return rows


def paste(s, text):
    """Sends a bracketed paste of text, the way a terminal does: the markers
    around the text and its line break passed through as it is (chat_paste.py)."""
    s.send("\x1b[200~" + text + "\x1b[201~")


def frame(label):
    """The regex of one status-line frame saying label (思考中…, 执行 shell_exec…)."""
    return FRAME + r" " + re.escape(label) + r" \d+s"


def screen_lines(s, cols):
    """The rows a terminal would show for everything the chat wrote so far."""
    screen = Screen(width=cols)
    screen.feed(s.text_raw(0))
    return screen.screen_lines()


def wait_screen(s, m, cols, ok, tries=15):
    """Reads the screen until ok(lines) holds: the repaint of a write lands
    asynchronously, so a busy machine must not catch it half way. Returns the last
    screen read."""
    lines = []
    for _ in range(tries):
        lines = screen_lines(s, cols)
        if ok(lines):
            return lines
        s.until(r"\A\Z never", m, timeout=0.2)  # keep reading output meanwhile
    return lines


# What chat says about a message inserted into the running turn (steering.go).
STEER_WAITING = "（等待送达）"
STEER_TAKEN = "（已送达）"
STEER_WITHDRAWN = "（已撤回）"


def steer_notice(text, suffix=STEER_WAITING):
    """The regex of the line chat shows for an inserted message."""
    return re.escape("[插话] " + text + suffix)


def session_id_of(s):
    """The session id chat printed at startup: the address `suga send --session`
    takes."""
    found = re.search(r"session id: ((?:sess_|sg_|s_)\S+)", s.text())
    return found.group(1) if found else ""


def send(binary, env, ws, *args, timeout=60):
    """Runs `suga send` as its own process, the way a Gofer-style caller does
    (deliver_command): returns (exit code, stderr)."""
    proc = subprocess.run([binary, "send", *args], env=env, cwd=ws,
                          capture_output=True, text=True, timeout=timeout)
    return proc.returncode, proc.stderr.strip()


def send_async(binary, env, ws, *args):
    """Starts `suga send` in the background: a scenario can check when it returns
    relative to the turn its message went into."""
    return subprocess.Popen([binary, "send", *args], env=env, cwd=ws,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)


def send_result(proc, timeout=60):
    """Waits for a send started with send_async: (exit code, stderr)."""
    _, err = proc.communicate(timeout=timeout)
    return proc.returncode, (err or "").strip()


class FakeLog:
    """The request log of fakellm.py: one JSON object per model request.

    Every request records its last few messages as {"role", "text"}: how a
    scenario tells whether an inserted message reached the same turn (it shows up
    right after the tool result) or became a turn of its own (it is the prompt)."""

    def __init__(self, proc):
        self._requests = []
        self._lock = threading.Lock()
        self._thread = threading.Thread(target=self._read, args=(proc,), daemon=True)
        self._thread.start()

    def _read(self, proc):
        for line in proc.stdout:
            try:
                request = json.loads(line)
            except ValueError:
                continue
            if "listening" in request:
                continue
            with self._lock:
                self._requests.append(request)

    def requests(self):
        with self._lock:
            return list(self._requests)

    def wait(self, count, timeout=30):
        """Waits for count requests and returns as many as arrived."""
        end = time.time() + timeout
        while time.time() < end:
            got = self.requests()
            if len(got) >= count:
                return got
            time.sleep(0.05)
        return self.requests()


def user_texts(request):
    """The text of the user messages in a request's tail, in order."""
    return [m.get("text", "") for m in request.get("tail", []) if m.get("role") == "user"]


def last_user(request):
    """The text of the last user message of a request ("" when there is none)."""
    texts = user_texts(request)
    return texts[-1] if texts else ""


class Scenario:
    """What one scenario works with: the session, the fake model's request log,
    the workspace, and the binary and environment a trace check needs."""

    def __init__(self, session, log, ws, binary, env, reply, cols=80):
        self.session, self.log, self.ws, self.binary, self.env = session, log, ws, binary, env
        self.reply = reply
        self.cols = cols

    def check(self, name, ok):
        return self.session.check(name, ok)

    def trace(self):
        """The session's trace events; leave the chat first (call it last)."""
        session_id = self.exit_chat()
        self.check("session id", bool(session_id))
        return trace_events(self.binary, self.env, session_id) if session_id else []

    def exit_chat(self):
        """Two Ctrl-C in a row leave chat; returns the session id. A Ctrl-C on a
        non-empty input line only clears it (wll1) - a scenario can end with text
        on that line (messages an aborted turn put back) - so up to three presses
        are sent, stopping as soon as chat has left."""
        m = self.session.mark()
        for _ in range(3):
            time.sleep(0.3)
            self.session.send(CTRL_C)
            if self.session.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=1):
                break
        self.session.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m)
        found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", self.session.text(m))
        return found.group(1) if found else ""

    def wait_file(self, name, seconds):
        path = os.path.join(self.ws, name)
        end = time.time() + seconds
        while time.time() < end:
            if os.path.exists(path):
                return True
            time.sleep(0.1)
        return False


def same_turn_scenario(c):
    """A message typed while a tool runs reaches the same turn."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    c.check("the prompt becomes ↳ while the turn runs", s.until(STEER_PROMPT, m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)

    m = s.mark()
    s.line("只改 README")
    c.check("the message is shown as waiting", s.until(r"\[插话\] 只改 README（等待送达）", m))
    # The tool ends, the model is asked again - with the message in the request.
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m))
    c.check("shown as delivered", s.until(r"\[插话\] 只改 README（已送达）", m))
    # Only the answer text: steering keeps repainting the input line next to the
    # streamed answer, the layout T3b/M1 reworks.
    c.check("the turn answers", s.until(c.reply, m))
    c.check("the end-of-turn line counts it", s.until(r"插话 1", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        c.check("the first request has no inserted message",
                "只改 README" not in " ".join(user_texts(requests[0])))
        tail = requests[1].get("tail", [])
        roles = [msg.get("role") for msg in tail]
        c.check("the second request carries it after the tool result",
                "只改 README" in last_user(requests[1]) and "tool" in roles and roles[-1] == "user")

    events = c.trace()
    c.check("one turn only", len([e for e in events if e.get("kind") == "turn"]) == 1)
    delivered = [e for e in events if e.get("kind") == "steer" and e.get("name") == "delivered"]
    c.check("the trace records the delivery", len(delivered) == 1)
    c.check("it names the keyboard source",
            all((e.get("attrs") or {}).get("source") == "keyboard" for e in delivered))


def paste_scenario(c):
    """Two lines pasted into the running turn become ONE inserted message."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    c.check("the prompt becomes ↳ while the turn runs", s.until(STEER_PROMPT, m))
    # The turn keeps its line editable and bracketed: the prompt read asked for
    # the paste markers and turned them off when it ended, so the marker after
    # the line above was submitted is the turn's own read asking again.
    c.check("bracketed paste is on for the turn", s.until_raw(PASTE_ON, m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)

    # A two-line paste into the running turn: one piece of text on the input line
    # and no submit at its line break. (The rows are looked for in the raw output:
    # the editor's own CR LF reaches the pty with one more CR while raw input
    # keeps output processing on; chat_paste.two_rows.)
    m = s.mark()
    paste(s, PASTE_TEXT)
    c.check("both lines are on the input line", s.until_raw(r"第一行\r*\n第二行", m))
    c.check("the paste submits nothing", not s.contains(r"\[插话\]", m))
    time.sleep(0.3)
    c.check("no request carried it while it waited", len(c.log.requests()) == 1)

    # The person's own Enter sends both lines as ONE inserted message.
    m = s.mark()
    s.send("\r")
    c.check("the message is shown as waiting", s.until(r"\[插话\] 第一行 第二行（等待送达）", m))
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m))
    c.check("shown as delivered", s.until(r"\[插话\] 第一行 第二行（已送达）", m))
    c.check("the turn answers", s.until(c.reply, m))
    c.check("the end-of-turn line counts one message", s.until(r"插话 1", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        c.check("the first request has no inserted message",
                "第一行" not in " ".join(user_texts(requests[0])))
        # fakellm collapses the whitespace of a message (its line break included),
        # so the paste shows as "第一行 第二行": one user message, both lines.
        tail = requests[1].get("tail", [])
        roles = [msg.get("role") for msg in tail]
        c.check("the second request carries both lines as one message",
                last_user(requests[1]) == "第一行 第二行" and "tool" in roles and roles[-1:] == ["user"])

    events = c.trace()
    delivered = [e for e in events if e.get("kind") == "steer" and e.get("name") == "delivered"]
    c.check("the trace records one delivery", len(delivered) == 1)
    c.check("it names the keyboard source",
            all((e.get("attrs") or {}).get("source") == "keyboard" for e in delivered))


def next_turn_scenario(c):
    """A message that arrives after the model's answer becomes the next turn."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("先看看")
    c.check("the prompt becomes ↳ while the model answers", s.until(STEER_PROMPT, m))
    # The turn opens its inbox just after it starts; a message typed into that
    # first instant is not taken (design m8) and would become the next turn as
    # well, but the point here is the message that arrives during the request.
    time.sleep(1.0)
    s.line("改成 README")
    c.check("the message is shown as waiting", s.until(r"\[插话\] 改成 README（等待送达）", m))
    # The answer stands, then the message runs as a turn of its own: the second
    # request is what that turn asks. (Steering shows the streamed answer next to
    # the input line it keeps repainting - the layout T3b/M1 reworks -, so only
    # the answer text itself is checked here.)
    c.check("the first answer", s.until(c.reply, m))
    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        c.check("the first request has no inserted message",
                "改成 README" not in " ".join(user_texts(requests[0])))
        c.check("the next turn asks the message", last_user(requests[1]) == "改成 README")
    c.check("chat is back at the prompt", s.until(r"❯ ", m))

    events = c.trace()
    c.check("two turns", len([e for e in events if e.get("kind") == "turn"]) == 2)
    returned = [e for e in events if e.get("kind") == "steer" and e.get("name") == "returned"]
    c.check("the trace records the return", len(returned) == 1)
    attrs = (returned[0].get("attrs") or {}) if returned else {}
    c.check("completed and untaken", attrs.get("reason") == "completed_untaken")
    c.check("handed to the next turn", attrs.get("disposition") == "next_turn")


def followup_scenario(c):
    """Tab queues the line typed during a turn for the turn after it (suga-8pug)."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("先看看")
    c.check("the prompt becomes ↳ while the model answers", s.until(STEER_PROMPT, m))
    # The model's own delay keeps the turn running while the line is typed.
    time.sleep(1.0)
    m = s.mark()
    s.send("改成 README")
    s.send("\t")
    c.check("the queued line is shown", s.until(r"\[排队\] 改成 README（本轮结束后发送）", m))
    # Tab took the line: nothing of it went into the running turn.
    c.check("nothing is inserted", not s.contains(r"\[插话\]", m))
    c.check("the first answer", s.until(c.reply, m))
    c.check("the queued line is shown as sent", s.until(r"\[排队\] 改成 README（已发送）", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        c.check("the first request has no queued message",
                "改成 README" not in " ".join(user_texts(requests[0])))
        c.check("the next turn asks the queued line", last_user(requests[1]) == "改成 README")
    c.check("chat is back at the prompt", s.until(r"❯ ", m))

    events = c.trace()
    c.check("two turns", len([e for e in events if e.get("kind") == "turn"]) == 2)
    c.check("a queued line is no inserted message",
            not [e for e in events if e.get("kind") == "steer"])


def abort_scenario(c):
    """Esc (and a single Ctrl-C) end this turn only; the message goes back."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)
    s.line("别改了")
    c.check("the message is shown as waiting", s.until(r"\[插话\] 别改了（等待送达）", m))

    m = s.mark()
    s.send("\x1b")  # Esc: the lone-Esc decision waits 50ms, so send nothing right after
    time.sleep(0.3)
    c.check("esc ends this turn only", s.until(r"已中断本轮，1 条插话已放回输入行", m, timeout=15))
    c.check("chat stays after esc", not s.contains(r"续接：", m))
    c.check("the message is back in the input line", s.until(r"别改了", m))
    time.sleep(0.5)
    c.check("nothing was run on its own", len(c.log.requests()) == 1)

    # The put-back line is not submitted by itself: Enter runs it as the next turn.
    m = s.mark()
    s.send("\r")
    requests = c.log.wait(2)
    c.check("Enter runs the message", len(requests) >= 2 and last_user(requests[1]) == "别改了")
    c.check("the next turn calls the tool again", s.until(r"\$ shell_exec", m))
    time.sleep(0.5)
    s.send(CTRL_C)
    c.check("one ctrl-c ends this turn only", s.until(r"已中断本轮", m, timeout=15))
    c.check("chat stays after one ctrl-c", not s.contains(r"续接：", m))
    c.exit_chat()


def send_scenario(c):
    """`suga send --steer`: the message goes into the turn running right now, the
    sender is answered `steered`, and what became of it decides its exit code."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    sid = session_id_of(s)
    c.check("session id", bool(sid))
    if not sid:
        return
    marker = os.path.join(c.ws, STARTED)

    # --- steered into the turn running right now ---------------------------
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    c.check("the prompt becomes ↳ while the turn runs", s.until(STEER_PROMPT, m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)

    # The notice of the inserted message is written before the response leaves, so
    # the mark goes up before the send runs.
    m = s.mark()
    code, err = send(c.binary, c.env, c.ws, "--session", sid, "--steer", SEND_TEXT)
    c.check("the first response is steered (no --wait)", code == 0 and err == "steered")
    c.check("the message is shown as waiting", s.until(steer_notice(SEND_PREFIX + SEND_TEXT), m))
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m))
    c.check("shown as delivered", s.until(steer_notice(SEND_PREFIX + SEND_TEXT, STEER_TAKEN), m))
    c.check("the turn answers", s.until(c.reply, m))
    c.check("the end-of-turn line counts it", s.until(r"插话 1", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        c.check("the first request has no inserted message",
                SEND_TEXT not in " ".join(user_texts(requests[0])))
        tail = requests[1].get("tail", [])
        roles = [msg.get("role") for msg in tail]
        c.check("the turn's next request carries it after the tool result",
                last_user(requests[1]) == SEND_PREFIX + SEND_TEXT and "tool" in roles and roles[-1] == "user")

    # --- --wait returns before that turn ends ------------------------------
    if os.path.exists(marker):
        os.remove(marker)
    m = s.mark()
    s.line("再跑一个长命令")
    c.check("tool call shown again", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("the second tool started", False)
        return
    c.check("the second tool started", True)

    proc = send_async(c.binary, c.env, c.ws, "--session", sid, "--steer", "--wait", SEND_TEXT2)
    code, err = send_result(proc)
    # The message was taken by this turn's next request, so the sender is answered
    # while the model is still working on it: neither the answer nor the end-of-turn
    # line is there yet. (A queued message would answer only once this turn had
    # ended and the message's own turn had started - which is the reverse check.)
    s.drain(0.3)
    c.check("--wait exits 0", code == 0)
    c.check("--wait returned before the turn ended",
            not s.contains(r"tokens · ", m) and not s.contains(c.reply, m))
    c.check("shown as delivered in the running turn",
            s.contains(steer_notice(SEND_PREFIX + SEND_TEXT2, STEER_TAKEN), m))
    c.check("the turn answers", s.until(c.reply, m))
    requests = c.log.wait(4)
    c.check("four requests", len(requests) >= 4)
    if len(requests) >= 4:
        c.check("the message reached the second turn's next request",
                last_user(requests[3]) == SEND_PREFIX + SEND_TEXT2)

    # --- an aborted turn discards the message (exit 3) ----------------------
    if os.path.exists(marker):
        os.remove(marker)
    m = s.mark()
    s.line("再跑一个长命令")
    c.check("tool call shown (third turn)", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("the third tool started", False)
        return
    c.check("the third tool started", True)

    proc = send_async(c.binary, c.env, c.ws, "--session", sid, "--steer", "--wait", SEND_TEXT3)
    c.check("the message is shown as waiting", s.until(steer_notice(SEND_PREFIX + SEND_TEXT3), m))
    s.send("\x1b")  # Esc: the lone-Esc decision waits 50ms, so send nothing right after
    time.sleep(0.3)
    code, err = send_result(proc)
    c.check("a message of an aborted turn exits 3", code == 3)
    c.check("esc ends this turn only", s.until(r"已中断本轮", m, timeout=15))
    c.check("chat stays after esc", not s.contains(r"续接：", m))
    c.check("the message is back in the input line", s.until(r"\[来自 cli\] " + SEND_TEXT3, m))

    # --- a full inbox is refused with queue_full ---------------------------
    if os.path.exists(marker):
        os.remove(marker)
    m = s.mark()
    s.line("再跑一个长命令")
    c.check("tool call shown (fourth turn)", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("the fourth tool started", False)
        return
    c.check("the fourth tool started", True)

    filled = [send(c.binary, c.env, c.ws, "--session", sid, "--steer", "插话 %d" % i)
              for i in range(8)]
    c.check("eight messages fit the inbox",
            all(code == 0 and err == "steered" for code, err in filled))
    code, err = send(c.binary, c.env, c.ws, "--session", sid, "--steer", "第九条")
    c.check("the ninth is refused with queue_full", code == 1 and "queue_full" in err)
    s.send("\x1b")
    c.check("esc ends this turn only", s.until(r"已中断本轮", m, timeout=15))

    # --- an idle chat queues it --------------------------------------------
    c.check("chat is back at the prompt", s.until(r"❯ ", m, timeout=15))
    code, err = send(c.binary, c.env, c.ws, "--session", sid, "--steer", SEND_TEXT4)
    c.check("an idle chat queues the message instead",
            code == 0 and err == "未插入当前回合，已排队")
    c.check("the queued message runs as the next turn",
            s.until(re.escape(SEND_PREFIX + SEND_TEXT4), m, timeout=15))

    events = c.trace()
    delivered = [e for e in events if e.get("kind") == "steer" and e.get("name") == "delivered"]
    c.check("the trace records both deliveries", len(delivered) == 2)
    c.check("they name the send source",
            all((e.get("attrs") or {}).get("source") == "send:cli" for e in delivered))
    returned = [e for e in events if e.get("kind") == "steer" and e.get("name") == "returned"]
    c.check("the aborted turn's messages are traced back to the input line",
            any((e.get("attrs") or {}).get("disposition") == "input_line" for e in returned))


def clear_scenario(c):
    """Ctrl-C on a non-empty input line drops that line and nothing else (wll1)."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    c.check("the prompt becomes ↳ while the turn runs", s.until(STEER_PROMPT, m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)

    # A half-typed message, then Ctrl-C: the line goes away, the turn runs on.
    m = s.mark()
    s.send(CLEAR_DROPPED)
    c.check("the text is on the input line", s.until(re.escape(CLEAR_DROPPED), m))
    s.send(CTRL_C)
    s.drain(0.8)
    c.check("ctrl-c with text: the turn is not ended", not s.contains(r"已中断本轮", m))
    c.check("ctrl-c with text: nothing was inserted", not s.contains(r"\[插话\]", m))
    time.sleep(0.3)
    c.check("ctrl-c with text: no request carried it", len(c.log.requests()) == 1)
    # The input line is empty again: the row holds the prompt and nothing else (a
    # dropped line left on the screen would still be there).
    c.check("ctrl-c with text: the input line is empty", input_row(s, CLEAR_COLS) == STEER.strip())

    # What is typed next is a line of its own: the dropped text did not come along.
    m = s.mark()
    s.line(CLEAR_TEXT)
    c.check("the new message is shown as waiting", s.until(steer_notice(CLEAR_TEXT), m))
    c.check("the dropped text did not come along",
            not s.contains(steer_notice(CLEAR_DROPPED + CLEAR_TEXT), m))
    # The tool ends and the model is asked again: the message reached this turn, so
    # the press dropped a line and did not end anything.
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m, timeout=40))
    c.check("shown as delivered", s.until(steer_notice(CLEAR_TEXT, STEER_TAKEN), m))
    c.check("the turn answers", s.until(c.reply, m))
    c.check("the end-of-turn line counts one message", s.until(r"插话 1", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        carried = user_texts(requests[1])
        c.check("the next request carries only the message typed after the ctrl-c",
                last_user(requests[1]) == CLEAR_TEXT
                and all(CLEAR_DROPPED not in text for text in carried))

    # An empty line keeps the old meaning during a turn: Ctrl-C ends this turn.
    marker = os.path.join(c.ws, STARTED)
    if os.path.exists(marker):
        os.remove(marker)
    m = s.mark()
    s.line("再跑一个长命令")
    c.check("tool call shown again", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("the second tool started", False)
        return
    c.check("the second tool started", True)
    s.send(CTRL_C)
    c.check("ctrl-c on an empty line still ends this turn", s.until(r"已中断本轮", m, timeout=15))
    c.check("chat stays after it", not s.contains(r"续接：", m))
    c.exit_chat()


def withdraw_scenario(c):
    """Up on the empty input line takes the messages still waiting back."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    sid = session_id_of(s)
    c.check("session id", bool(sid))
    if not sid:
        return

    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    c.check("the prompt becomes ↳ while the turn runs", s.until(STEER_PROMPT, m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)

    # Two messages typed into the running turn: both wait for a model request.
    m = s.mark()
    s.line(WITHDRAW_TEXT)
    c.check("the first message is shown as waiting", s.until(steer_notice(WITHDRAW_TEXT), m))
    s.line(WITHDRAW_TEXT2)
    c.check("the second message is shown as waiting", s.until(steer_notice(WITHDRAW_TEXT2), m))
    # And one sent from elsewhere: it waits in the same inbox.
    code, err = send(c.binary, c.env, c.ws, "--session", sid, "--steer", SEND_TEXT)
    c.check("the sent message is steered into the turn", code == 0 and err == "steered")
    c.check("the sent message is shown as waiting", s.until(steer_notice(SEND_PREFIX + SEND_TEXT), m))
    time.sleep(0.3)
    c.check("no request carried them while they waited", len(c.log.requests()) == 1)

    # Up on the empty input line: the typed ones come back, the sent one stays (its
    # sender is waiting for it).
    m = s.mark()
    s.send("\x1b[A")
    c.check("the first is shown as withdrawn", s.until(steer_notice(WITHDRAW_TEXT, STEER_WITHDRAWN), m))
    c.check("the second is shown as withdrawn", s.until(steer_notice(WITHDRAW_TEXT2, STEER_WITHDRAWN), m))
    c.check("the sent one is not withdrawn",
            not s.contains(steer_notice(SEND_PREFIX + SEND_TEXT, STEER_WITHDRAWN), m))
    # Both texts are on the input line, one line each, in the order they were typed
    # (the editor's own line break; the pty adds a CR of its own, like the paste
    # scenario's rows).
    c.check("both are back on the input line, in order",
            s.until_raw(re.escape(WITHDRAW_TEXT) + r"\r*\n" + re.escape(WITHDRAW_TEXT2), m))
    c.check("nothing was submitted by the Up key",
            not s.contains(steer_notice(WITHDRAW_EDITED, STEER_WAITING), m))

    # Edited at the end of the line and submitted again: one new message of the
    # same turn.
    m = s.mark()
    s.line(WITHDRAW_EDIT)
    c.check("the edited text is shown as waiting", s.until(steer_notice(WITHDRAW_EDITED), m))

    # The tool ends and the model is asked again: it carries the sent message and
    # the edited one, not the withdrawn pair. (The tool slept 25s; the steps above
    # are what is left of it, hence the longer wait.)
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m, timeout=40))
    c.check("the sent one is delivered", s.until(steer_notice(SEND_PREFIX + SEND_TEXT, STEER_TAKEN), m))
    c.check("the edited one is delivered", s.until(steer_notice(WITHDRAW_EDITED, STEER_TAKEN), m))
    c.check("the turn answers", s.until(c.reply, m))
    c.check("the end-of-turn line counts the delivered two", s.until(r"插话 2", m))
    c.check("the withdrawn ones are not counted", not s.contains(r"插话 4", m))

    requests = c.log.wait(2)
    c.check("two requests", len(requests) >= 2)
    if len(requests) >= 2:
        second = requests[1]
        c.check("the first request has no inserted message",
                all(t not in " ".join(user_texts(requests[0]))
                    for t in (WITHDRAW_TEXT, WITHDRAW_TEXT2, SEND_PREFIX + SEND_TEXT)))
        # Neither of the withdrawn texts is a message of the next request (exact
        # match: the edited one starts with the first of them), while the sent one
        # and the edited one are.
        carried = user_texts(second)
        seen = second.get("seen", [])
        c.check("the withdrawn ones are not in the next request",
                WITHDRAW_TEXT not in carried and WITHDRAW_TEXT2 not in carried
                and "user:" + WITHDRAW_TEXT not in seen and "user:" + WITHDRAW_TEXT2 not in seen)
        c.check("the sent one is in the next request", SEND_PREFIX + SEND_TEXT in carried)
        c.check("the edited one is what the next request carries last",
                last_user(second) == WITHDRAW_EDITED)

    events = c.trace()
    withdrawn = [e for e in events if e.get("kind") == "steer" and e.get("name") == "withdrawn"]
    c.check("the trace records both withdrawals", len(withdrawn) == 2)
    c.check("each names the keyboard source and the batch size",
            all((e.get("attrs") or {}).get("source") == "keyboard"
                and (e.get("attrs") or {}).get("count") == 2 for e in withdrawn))
    delivered = [e for e in events if e.get("kind") == "steer" and e.get("name") == "delivered"]
    c.check("the delivered ones are the edited and the sent one",
            sorted((e.get("attrs") or {}).get("source", "") for e in delivered) == ["keyboard", "send:cli"])


def screen_scenario(c):
    """A long, overwide reply streams slowly while a message is typed: the reply
    keeps its rows, whole, and the input line stays one line of its own (r3 M1)."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("长回答")
    c.check("the prompt becomes ↳ while the model answers", s.until(STEER_PROMPT, m))
    # The status line of the turn needs a colored terminal, and these scenarios run
    # with NO_COLOR set: nothing of it is drawn (the status scenario checks it with
    # the colors on).
    c.check("no status line without colors", not s.contains(r"思考中", m))

    # Type while the reply streams: the chunks arrive slowly (--chunk-delay).
    time.sleep(0.8)
    for part in ["插", "话", "abc"]:
        s.send(part)
        time.sleep(0.4)
    typed = "插话abc"
    c.check("the half line is echoed", s.until(re.escape(typed), m))
    c.check("the reply streams in", s.until(re.escape(LONG + "-01 "), m, timeout=60))
    c.check("its last line arrived", s.until(re.escape("%s-%02d " % (LONG, LONG_LINES)), m, timeout=60))
    c.check("the turn ends", s.until(r"tokens · ", m, timeout=60))
    # The half line comes back on the next prompt (during the turn its prompt was
    # ↳): wait for that redraw before looking at the screen.
    c.check("the half line is back at the prompt", s.until(r"❯ " + re.escape(typed), m, timeout=10))

    # The screen, as a terminal would show it: the rows the reply occupies and the
    # one row the input line lives on.
    screen = Screen(width=c.cols)
    screen.feed(s.text_raw(0))
    lines = screen.screen_lines()
    expected = wrapped_rows(LONG_REPLY, c.cols, prefix="▣ ")
    start = next((i for i, _ in enumerate(lines) if lines[i:i + len(expected)] == expected), None)
    if start is None:
        print("--- screen ---")
        print("\n".join("%3d|%s" % (i, row) for i, row in enumerate(lines)))
        print("--- the rows the reply should occupy ---")
        print("\n".join("   |%s" % row for row in expected))
    c.check("the reply's rows are whole and in place", start is not None)
    typed_rows = [i for i, row in enumerate(lines) if typed in row]
    c.check("the half line is on one row only", len(typed_rows) == 1)
    c.check("the input line is the last row", bool(typed_rows) and typed_rows[-1] == len(lines) - 1)
    if typed_rows:
        # Whichever end of the turn it is: the running one (↳) or the prompt the
        # half line was handed to (❯, design N5).
        c.check("the input line carries its prompt",
                lines[typed_rows[-1]] in (STEER + typed, PROMPT + typed))
    if start is not None:
        # Nothing of the streamed text is left between the reply and the input line:
        # no fragment of it, and no row that is only its fill character.
        after = lines[start + len(expected):]
        residue = [row for row in after if "STEER_LONG" in row or (row and set(row) == {"."})]
        if residue:
            print("--- screen rows after the reply ---")
            print("\n".join("   |%s" % row for row in after))
        c.check("nothing of the reply is left after it", not residue)
    c.exit_chat()


def status_scenario(c):
    """The turn's own status line above the input line: what the turn waits for,
    refreshed in place while a message is typed, the whole turn through."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    # The model is asked for its first message (--delay 3): the status line says so,
    # on the row above the input line, while nothing of the turn is on screen yet.
    c.check("the status line says the model is asked", s.until(frame("思考中…"), m))
    c.check("the input line stays the ↳ prompt", s.until(STEER_PROMPT, m))
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)
    c.check("the status line names the running tool", s.until(frame("执行 shell_exec…"), m))

    # The rows of the screen, as a terminal would show them: the status line is the
    # row right above the input line.
    lines = wait_screen(s, m, c.cols, lambda ls: len(ls) >= 2 and re.fullmatch(STATUS_FRAME, ls[-2]) is not None)
    c.check("the status line is the row above the input line",
            len(lines) >= 2 and re.fullmatch(STATUS_FRAME, lines[-2]) is not None)
    c.check("the input line is the last row", bool(lines) and lines[-1] == STEER_EMPTY)

    # Type while the tool runs: the frames keep rewriting that row (its seconds
    # change), and what is typed into the input line stays where it is.
    s.send("插话")
    typed = "插话"
    c.check("the half line is echoed", s.until(re.escape(typed), m))
    m2 = s.mark()
    c.check("the status line keeps refreshing", s.until(frame("执行 shell_exec…"), m2))
    lines = wait_screen(s, m2, c.cols, lambda ls: bool(ls) and ls[-1] == STEER + typed)
    c.check("the input line keeps what was typed, as the last row",
            bool(lines) and lines[-1] == STEER + typed)
    c.check("exactly one status row, right above it",
            [i for i, row in enumerate(lines) if re.search(STATUS_FRAME, row)] == [len(lines) - 2])

    # The result is in: the model is asked again (--delay 3) and the status line goes
    # back to the model's own wording.
    m2 = s.mark()
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m2))
    c.check("the status line goes back to the model", s.until(frame("思考中…"), m2))

    # The answer streams in small chunks (--chunk): the status line keeps the row
    # right above the input line, and none of it is left in the output area above.
    c.check("the answer starts streaming", s.until(re.escape("STEER_OK"), m2))
    lines = wait_screen(s, m2, c.cols, lambda ls: len(ls) >= 3 and "STEER_OK" in " ".join(ls[:-2])
                        and re.fullmatch(STATUS_FRAME, ls[-2]) is not None)
    c.check("while streaming: one status row, right above the input line",
            [i for i, row in enumerate(lines) if re.search(STATUS_FRAME, row)] == [len(lines) - 2])
    c.check("while streaming: the ↳ input line is the last row",
            bool(lines) and lines[-1] == STEER + typed)
    c.check("the answer's last line arrived", s.until(re.escape("第三行"), m2))
    c.check("the turn ends", s.until(r"tokens · ", m2))

    # The end of the turn: the status line is gone, and the half line comes back at
    # the ❯ prompt of the next read (design N5).
    c.check("the half line comes back at the prompt",
            s.until(r"❯ " + re.escape(typed), m2, timeout=10))
    lines = wait_screen(s, m2, c.cols, lambda ls: bool(ls) and ls[-1] == PROMPT + typed)
    c.check("no status row is left on screen",
            not [row for row in lines if re.search(STATUS_FRAME, row)])
    c.check("the input line is the last row, at the ❯ prompt",
            bool(lines) and lines[-1] == PROMPT + typed)
    c.exit_chat()


def status_off_scenario(c):
    """chat.spinner: false turns the status line off, in a colored terminal too."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)
    s.send("插话")
    typed = "插话"
    c.check("the half line is echoed", s.until(re.escape(typed), m))
    lines = wait_screen(s, m, c.cols, lambda ls: bool(ls) and ls[-1] == STEER + typed)
    c.check("no status row above the input line",
            not [row for row in lines if re.search(STATUS_FRAME, row)])
    c.check("the ↳ input line is the last row", bool(lines) and lines[-1] == STEER + typed)
    c.check("no status line anywhere", not s.contains(r"思考中", m))

    m2 = s.mark()
    c.check("tool result shown", s.until(r"⎿ ok: TOOL_DONE", m2))
    c.check("the turn answers", s.until(c.reply, m2))
    c.check("the turn ends", s.until(r"tokens · ", m2))
    c.exit_chat()


def hook_scenario(c):
    """A hook's notice and the line it writes straight to the terminal arrive
    while a message is typed: the input line keeps its place (N4, r3 M1)."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一下")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    # Type while the hook runs (PostToolUse fires right after the tool result).
    for part in ["插", "一句"]:
        s.send(part)
        time.sleep(0.3)
    typed = "插一句"
    c.check("the hook wrote to the terminal itself", s.until(HOOK_TTY, m, timeout=30))
    c.check("the hook's notice reached chat", s.until(HOOK_NOTICE, m, timeout=30))
    c.check("the half line is echoed", s.until(re.escape(typed), m))
    c.check("no status line without colors", not s.contains(r"思考中", m))

    # The hook writes to the terminal itself and chat repaints its input line
    # afterwards, asynchronously: read the screen until the repaint has landed
    # (up to ~3s), so a busy machine does not catch it half way.
    for _ in range(15):
        screen = Screen(width=c.cols)
        screen.feed(s.text_raw(0))
        lines = screen.screen_lines()
        typed_rows = [i for i, row in enumerate(lines) if typed in row]
        if typed_rows and typed_rows[-1] == len(lines) - 1:
            break
        s.until(r"\A\Z never", m, timeout=0.2)  # keep reading output meanwhile
    # A process writing to the terminal itself (the hook) moves the cursor without
    # the line editor knowing, so the input line can be repainted a row lower: what
    # must hold is that it is there, whole, as the last row - and that the hook's own
    # line did not end up glued to chat's notice of it, which is what happens when
    # the raw output has no newline processing left (the erase after it only reaches
    # from the column the write stopped at).
    c.check("the input line is the last row", bool(typed_rows) and typed_rows[-1] == len(lines) - 1)
    if typed_rows:
        c.check("the input line carries its prompt",
                lines[typed_rows[-1]] in (STEER + typed, PROMPT + typed))
    # Two lines written straight to the terminal: with output processing kept on
    # (OPOST|ONLCR) the second starts at column 0; without it, it starts where the
    # first one ended (a staircase).
    c.check("a second line the hook wrote starts at column 0",
            any(row.rstrip() == HOOK_TTY2 for row in lines))
    c.check("the hook's own line stayed a row of its own",
            not any(HOOK_TTY in row and HOOK_NOTICE in row for row in lines))
    if not any(row.rstrip() == HOOK_TTY or HOOK_NOTICE in row for row in lines):
        print("--- screen ---")
        print("\n".join("%3d|%s" % (i, row) for i, row in enumerate(lines)))
    # End the turn only, then leave chat.
    s.send("\x1b")
    c.check("esc ends this turn only", s.until(r"已中断本轮", m, timeout=15))
    c.exit_chat()


def approval_scenario(c):
    """An approval answer is never taken as an inserted message."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一下")
    c.check("the question is asked", s.until(r"允许执行 shell_exec", m))
    s.line("插一句")  # not y/a: the answer, not a steer
    c.check("the answer is used as the answer", s.until(r"用户拒绝执行 shell_exec", m))
    c.check("no inserted message was shown", not s.contains(r"\[插话\]", m))
    c.check("the turn ends", s.until(r"tokens · ", m))
    time.sleep(0.5)
    c.check("no request carried that text",
            all("插一句" not in last_user(r) for r in c.log.requests()))
    events = c.trace()
    c.check("no steer in the trace", not [e for e in events if e.get("kind") == "steer"])


def off_scenario(c):
    """chat.steering: false keeps the old behaviour."""
    s = c.session
    m = s.mark()
    c.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一个长命令")
    c.check("tool call shown", s.until(r"\$ shell_exec", m))
    if not c.wait_file(STARTED, 30):
        c.check("tool started (marker file)", False)
        return
    c.check("tool started (marker file)", True)
    c.check("no ↳ prompt during the turn", not s.contains(STEER_PROMPT, m))
    # Cooked mode: the bytes wait in the tty, nothing reads them, so nothing is
    # inserted into the turn.
    s.send("插一句")
    time.sleep(0.5)
    c.check("typing during the turn inserts nothing", not s.contains(r"\[插话\]", m))
    m = s.mark()
    s.send(CTRL_C)
    c.check("one ctrl-c still ends the chat", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))


def run(name, binary, work, port, fake_args, tool, tools_extra, extra, scenario, keep, reply,
        dump=False, cols=None, hooks=False, color=False):
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    if hooks:
        # The hook scenario's command: it writes a line straight to the terminal and
        # one to stderr (chat prints that as a notice), so both arrive while the
        # input line is up. PostToolUse is off unless the config asks for it.
        os.makedirs(home, exist_ok=True)
        hook = os.path.join(home, "hook.py")
        with open(hook, "w", encoding="utf-8") as f:
            f.write(HOOK_SCRIPT)
        extra += ("hooks:\n  command: [%s, -I, %s]\n  post_tool_use:\n    tools: ['*']\n"
                  % (json.dumps(sys.executable), json.dumps(hook)))
    write_home(home, port, tools_extra=tools_extra, extra=extra)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="on" if hooks else "off", TERM="xterm", NO_COLOR="1")
    if color:
        # The status line of a running turn, like the waiting line before it, needs a
        # colored terminal: NO_COLOR (what the other scenarios set) keeps it off.
        env.pop("NO_COLOR", None)
    env.pop("SUGA_CONFIG", None)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port),
                             "--command", tool.format(marker=os.path.join(ws, STARTED)),
                             *fake_args],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        log = FakeLog(fake)
        # The pty's window size is what the chat reads as the terminal width (and
        # what a terminal would wrap the lines at); only the screen scenarios need a
        # size of their own, the others keep the pty's default.
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(cols, 24) if cols else None)
        print(f"\n== {name} ==")
        scenario(Scenario(s, log, ws, binary, env, reply, cols=cols or 80))
        if dump:
            print("--- output ---")
            print(s.text())
            print("--- end ---")
        s.close()
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return [(f"{name}: {check}", ok) for check, ok in (s.results if s else [])]


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-steer", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0,
                   help="fake model port; 0 picks free ports (one per scenario)")
    p.add_argument("--keep", action="store_true", help="keep the scratch homes after the run")
    p.add_argument("--dump", action="store_true", help="print everything the chat wrote, per scenario")
    p.add_argument("--only", default="", help="run one scenario by name only")
    args = p.parse_args()
    # One fake model per scenario: the base port and the next thirteen.
    args.port = resolve_port(args.port, 13)

    binary = os.path.abspath(args.bin)
    # No approval override in the approval scenario: the default is asking, and
    # the question is the subject there.
    auto = "  approval: auto\n"
    # The answer text the model gives in the steering scenarios: short, one
    # chunk, and not a word any prompt of these scenarios uses.
    reply = "STEER_OK"
    scenarios = [
        ("same-turn", ["--mode", "tool", "--delay", "0.2", "--reply", reply], QUICK_TOOL, auto, "",
         same_turn_scenario, {}),
        # A two-line paste into the running turn: one piece of text on the input
        # line, one inserted message once the person's own Enter follows.
        ("paste", ["--mode", "tool", "--delay", "0.2", "--reply", reply], PASTE_TOOL, auto, "",
         paste_scenario, {}),
        ("next-turn", ["--mode", "text", "--delay", "3", "--reply", reply], QUICK_TOOL, auto, "",
         next_turn_scenario, {}),
        # Tab on the line typed while the model answers: it waits for the turn after
        # this one instead of entering it (suga-8pug).
        ("followup", ["--mode", "text", "--delay", "3", "--reply", reply], QUICK_TOOL, auto, "",
         followup_scenario, {}),
        ("abort", ["--mode", "tool", "--delay", "0.2", "--reply", reply], SLOW_TOOL, auto, "",
         abort_scenario, {}),
        # `suga send --steer` from outside: the model's own delay keeps the turn
        # running after the tool result, which is what the --wait timing check needs.
        ("send", ["--mode", "tool", "--delay", "3", "--reply", reply], SEND_TOOL, auto, "",
         send_scenario, {}),
        # Up on the empty input line of a running turn takes the messages that are
        # still waiting back, while a `suga send --steer` message stays.
        ("withdraw", ["--mode", "tool", "--delay", "0.2", "--reply", reply], WITHDRAW_TOOL, auto, "",
         withdraw_scenario, {}),
        # Ctrl-C on a non-empty input line of a running turn: the line is dropped and
        # the turn goes on, while an empty line still ends this turn only (wll1).
        ("clear", ["--mode", "tool", "--delay", "0.2", "--reply", reply], CLEAR_TOOL, auto, "",
         clear_scenario, {"cols": CLEAR_COLS}),
        # A long, overwide reply in small chunks: the screen model checks where its
        # lines and the input line ended up. The pty is 60 columns wide, so every
        # reply line is wrapped by the terminal.
        ("screen", ["--mode", "text", "--reply", LONG, "--lines", str(LONG_LINES),
                    "--wide", str(LONG_WIDE), "--chunk", "20", "--chunk-delay", "0.12"],
         QUICK_TOOL, auto, "", screen_scenario, {"cols": PTY_COLS}),
        # A hook writing to the terminal and to stderr while the turn runs.
        ("hook", ["--mode", "tool", "--delay", "4", "--reply", reply], "sleep 2; echo SMOKE_OK", auto, "",
         hook_scenario, {"cols": PTY_COLS, "hooks": True}),
        ("approval", ["--mode", "tool", "--delay", "0.2", "--reply", reply], QUICK_TOOL, "", "",
         approval_scenario, {}),
        # The status line above the input line while a turn runs: it needs a colored
        # terminal (the scenarios above run with NO_COLOR), and the answer streams in
        # small chunks so its row can be looked at mid-stream.
        ("status", ["--mode", "tool", "--delay", "3", "--reply", STATUS_REPLY,
                    "--chunk", "6", "--chunk-delay", "0.3"], STATUS_TOOL, auto, "",
         status_scenario, {"color": True}),
        # chat.spinner: false turns the status line off (the colored terminal stays).
        ("status-off", ["--mode", "tool", "--delay", "0.5", "--reply", reply], QUICK_TOOL, auto,
         "chat:\n  spinner: false\n", status_off_scenario, {"color": True}),
        ("off", ["--mode", "tool", "--delay", "0.2", "--reply", reply], SLOW_TOOL, auto,
         "chat:\n  steering: false\n", off_scenario, {}),
    ]
    results = []
    for i, (name, fake_args, tool, tools_extra, extra, scenario, opts) in enumerate(scenarios):
        if args.only and name != args.only:
            continue
        results += run(name, binary, os.path.abspath(f"{args.work}-{name}"), args.port + i,
                       fake_args, tool, tools_extra, extra, scenario, args.keep, reply, args.dump, **opts)
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
