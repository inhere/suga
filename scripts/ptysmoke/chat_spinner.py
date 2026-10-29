#!/usr/bin/env python3
"""End-to-end smoke test: chat shows the "思考中…" waiting line while the model
thinks and erases it before the next output, paints the line the person
submitted on a background of its own, and names the terminal tab after the
session (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (tool mode with --delay 2, shell_exec
approved automatically) and `suga chat` in a PTY with colors ON (NO_COLOR is
dropped: neither the waiting line, the input background nor the terminal title
shows on a terminal without colors), sends one prompt and checks: nothing is
shown before the first turn, the line with the elapsed seconds appears while the
model is asked for its first message, the tool call replaces it, it comes back
while the model reads the tool result, and the answer is printed on a line the
hint was erased from, and the submitted line comes back on a 256-color
background filled to the end of the row. The frame checks run with
`chat.steering: false`: while chat keeps the input line on screen for the whole
turn (chat.steering, the default) the waiting line stays off - a frame is a bare
carriage return plus erase on the current row and would fight that line for it
(T3b/r3 M1); the steering side of it is checked in chat_steer.py. On the terminal
title: the terminal's
own title is saved and the session named at startup (no session title yet), the
title is updated with the first message's title, and leaving chat clears the
title and pops the saved one. A second chat, configured with chat.spinner:
false, chat.highlight_input: false and chat.terminal_title: false, shows none of
them (dropping the title code without dropping `terminal_title: false` here
turns these checks red in either direction).

  python3 -I scripts/ptysmoke/chat_spinner.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-spinner.
"""
import argparse
import os
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_D, PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

HINT = r"思考中… \d+s"
REPLY = "SMOKE_SPINNER_REPLY"
OK = r"⎿ ok: SMOKE_SPINNER_OK"
# shell_exec runs without a question; the danger check still asks.
TOOLS_EXTRA = "  approval: auto\n"
# The line is erased with ESC[2K (carriage return + erase line); ptydrive strips
# those codes from text(), so the raw buffer is needed to see the erase.
ERASE = "\x1b[2K"
# The background chat paints the submitted line with (chat.highlight_input);
# the checks look at the raw output, text() drops the escape codes.
INPUT_BG = r"\x1b\[48;5;236m"
# The terminal title (chat.terminal_title): OSC 0 with the program, the session's
# short name ("ws-<two chars>", the workspace directory) and the session title.
TITLE = r"\x1b\]0;suga"
TITLE_SAVE = r"\x1b\[22;0t"
TITLE_RESTORE = r"\x1b\[23;0t"
TITLE_CLEAR = r"\x1b\]0;\x07"


# The frame checks need chat.steering: false: with steering on (the default) the
# waiting line stays off while a turn runs, because chat keeps the input line on
# screen then and a frame would fight it for the row (see the docstring).
def on_chat_config():
    return "chat:\n  steering: false\n"


def off_chat_config():
    """Top-level config of the second run: every switch turns its feature off."""
    return "chat:\n  spinner: false\n  highlight_input: false\n  terminal_title: false\n"


def scenario(s):
    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("❯ prompt", s.until(r"❯ ", m))
    s.check("nothing before the first turn", not s.contains("思考中", m))
    # The terminal title is set before the banner: saved first, then named after
    # the session (no session title before the first message).
    s.check("title: the terminal's own title is saved", s.until_raw(TITLE_SAVE, m))
    s.check("title: names the program and the session's short name",
            s.until_raw(TITLE + r" · ws-[a-z0-9]{2}\x07", m))
    s.check("title: no session title before the first message",
            not s.contains_raw(r"\x1b\]0;suga · ws-[a-z0-9]{2} · ", m))

    m = s.mark()
    s.line("你好")
    s.check("line while waiting for the first message", s.until(HINT, m))
    # The first message is the session title, so the terminal title changes with
    # it (the same title the session stores).
    s.check("title: updated with the session title",
            s.until_raw(TITLE + r" · ws-[a-z0-9]{2} · 你好\x07", m))
    # The repaint: the style, the prompt (which carries its own colors and ends
    # with a reset), the style again, what was typed, then spaces to the row end.
    s.check("input line: painted on a background", s.until_raw(INPUT_BG, m))
    s.check("input line: the text sits on it",
            s.until_raw(INPUT_BG + r"[\s\S]*?❯ (?:\x1b\[[0-9;]*m)*" + INPUT_BG + r"(?:\x1b\[[0-9;]*m)*你好", m))
    s.check("input line: the background reaches the row end",
            s.until_raw(INPUT_BG + r"[\s\S]*?你好 +\x1b\[0m", m))
    s.check("tool call line", s.until(r"\$ shell_exec", m))
    s.check("tool result line", s.until(OK, m))

    m = s.mark()
    s.check("line again while the model reads the result", s.until(HINT, m))
    s.check("answer", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))

    raw = s.buf[m:].decode("utf-8", "replace")
    hint = raw.rfind("思考中")
    # The raw bytes carry color codes around the "▣" prefix: find the reply itself.
    answer = raw.rfind(REPLY)
    s.check("the last hint was erased before the answer",
            hint >= 0 and answer > hint and ERASE in raw[hint:answer])

    # Ctrl-D at the prompt leaves chat: the terminal title is emptied (a
    # terminal that keeps no title stack then shows its own default) and the
    # saved title popped after it.
    m = s.mark()
    s.send(CTRL_D)
    s.check("title: cleared on exit", s.until_raw(TITLE_CLEAR, m))
    s.check("title: the saved title is popped on exit", s.until_raw(TITLE_RESTORE, m))


def off_scenario(s):
    """chat.spinner: false, chat.highlight_input: false and chat.terminal_title:
    false show none of them, in a colored terminal (the reverse of the checks
    above)."""
    # The whole session is searched for the title sequences: a title set at
    # startup would sit before the first mark, too.
    start = 0
    m = s.mark()
    s.check("off: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("你好")
    s.check("off: answer", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("off: no waiting line anywhere", not s.contains("思考中", m))
    s.check("off: the submitted line has no background", not s.contains_raw(r"\x1b\[48;5;236m", m))
    s.check("off: no terminal title anywhere", not s.contains_raw(r"\x1b\]0;", start))
    s.check("off: no title save or restore either",
            not s.contains_raw(r"\x1b\[2[23];0t", start))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-spinner", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    off_home = os.path.abspath(os.path.join(args.work, "home-off"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(off_home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra=TOOLS_EXTRA, extra=on_chat_config())
    write_home(off_home, args.port, tools_extra=TOOLS_EXTRA, extra=off_chat_config())
    # Colors on: the waiting line needs a terminal without NO_COLOR (the other
    # scenarios keep the default NO_COLOR=1).
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm")
    env.pop("NO_COLOR", None)
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "tool", "--delay", "2",
                             "--command", "echo SMOKE_SPINNER_OK", "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    sessions = []
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        sessions.append(s)
        scenario(s)
        s.close()

        s_off = PtySession([binary, "chat"], env=dict(env, SUGA_HOME=off_home), cwd=ws)
        sessions.append(s_off)
        off_scenario(s_off)
    finally:
        for session in sessions:
            session.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
            shutil.rmtree(off_home, ignore_errors=True)

    total = sum(len(session.results) for session in sessions)
    failed = [name for session in sessions for name in session.failed()]
    print(f"\n{total - len(failed)}/{total} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
