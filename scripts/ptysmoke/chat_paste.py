#!/usr/bin/env python3
"""End-to-end smoke test of pasting a multi-line text into `suga chat` (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (text mode), writes a temporary SUGA_HOME and
runs `suga chat` in a PTY. A pasted multi-line text must land on the input line as
one piece and only the person's Enter may send it, as ONE message with its
internal newlines kept (suga-b1md).

Checked:
  1. chat asks the terminal to bracket pastes (ESC[?2004h) while the line is
     editable;
  2. a paste (ESC[200~...ESC[201~) with an LF line break shows both lines on the
     input line as two rows and starts no turn (the model is not called);
  3. the Enter after it sends one message, and what the model receives is exactly
     "第一行\\n第二行" (fakellm's per-request log carries the user text);
  4. the same with a CR line break, which is what a terminal inside a paste
     usually hands over;
  5. a paste from a terminal WITHOUT bracketed paste support: the text comes
     without markers and is recognised by the bytes that follow each newline.

  The two rows of the input line are looked for in the raw output as CR LF: the
  line editor writes it and a read between turns leaves the output processing off,
  so the pty receives exactly that. The same paste into a *running turn* has its own
  scenario (chat_steer, "paste"): the turn's read keeps the output processing on and
  the row break arrives as CR CR LF there.

Reverse verification: drop the paste handling — bracketed paste not enabled, the
byte pump's newline decision off, and a newline inside a paste submitting again —
and a paste submits at its first line break: the ESC[?2004h check, the "no turn
starts" checks, the one-message checks and the whole no-marker round fail.

  python3 -I scripts/ptysmoke/chat_paste.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-paste.
"""
import argparse
import json
import os
import select
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession  # noqa: E402

REPLY = "完成"
PASTE_ON = r"\x1b\[\?2004h"


class FakeLog:
    """The JSON lines fakellm writes on stdout, one per model request.

    Each line carries "user": the last user message of the request, which is the
    only place a smoke can see what the model actually received.
    """

    def __init__(self, proc):
        self.proc = proc
        self.entries = []

    def drain(self, timeout=0.3):
        """Reads the lines that arrive within timeout; returns the new entries."""
        end = time.time() + timeout
        got = []
        while True:
            left = end - time.time()
            if left <= 0:
                return got
            ready, _, _ = select.select([self.proc.stdout], [], [], left)
            if not ready:
                return got
            line = self.proc.stdout.readline()
            if not line:
                return got
            try:
                entry = json.loads(line)
            except ValueError:
                continue
            self.entries.append(entry)
            got.append(entry)

    def users(self):
        return [str(e.get("user", "")) for e in self.entries]

    def wait_user(self, want, timeout=10.0):
        """Waits until a request's user message contains want."""
        end = time.time() + timeout
        while time.time() < end:
            if any(want in text for text in self.users()):
                return True
            self.drain(0.3)
        return False


def isolate_ws(ws):
    """Make ws its own git root, so suga does not pick up the enclosing repository
    (its root MEMORY.md would switch project memory to the repository layout and
    the smoke would write into the real repo)."""
    subprocess.run(["git", "init", "-q", ws], check=True)


def write_home(home, port):
    """Writes SUGA_HOME/config.yaml: the fake model, no tools to confirm.

    Project memory is forced to the files backend: the scratch workspace lives
    inside this repository, whose .gofer/tracker would otherwise select (and call)
    the real gofer.
    """
    os.makedirs(home, exist_ok=True)
    with open(os.path.join(home, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(f"""model: fake/m1
providers:
  fake:
    type: openai
    base_url: http://127.0.0.1:{port}/v1
    api_key: k
memory:
  project:
    backend: files
""")


def wait_port(proc):
    line = proc.stdout.readline()  # {"listening": port}
    if "listening" not in line:
        raise SystemExit("fake model did not start: " + line)


def paste(s, text):
    """Sends a bracketed paste of text, the way a terminal does: the markers
    around the text, its line breaks passed through as they are."""
    s.send("\x1b[200~" + text + "\x1b[201~")


def scenario(s, log):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    s.check("bracketed paste is on while the line is editable", s.until_raw(PASTE_ON, m))

    # A paste with an LF line break: both lines on the input line, no turn.
    m = s.mark()
    paste(s, "第一行\n第二行")
    s.check("LF paste: both lines are on the input line", s.until_raw("第一行\r\n第二行", m))
    s.check("LF paste: no turn starts", log.drain() == [])
    m = s.mark()
    s.send("\r")
    s.check("LF paste: Enter answers", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("LF paste: the model got one message with both lines", log.wait_user("第一行\n第二行"))
    s.check("LF paste: exactly one user message carries the paste",
            sum(1 for text in log.users() if "第二行" in text) == 1)
    s.check("LF paste: the message is the pasted text, not just its first line",
            "第一行\n第二行" in log.users())

    # A paste with a CR line break (what a terminal usually sends inside a paste).
    m = s.mark()
    paste(s, "第三行\r第四行")
    s.check("CR paste: both lines are on the input line", s.until_raw("第三行\r\n第四行", m))
    s.check("CR paste: no turn starts", log.drain() == [])
    m = s.mark()
    s.send("\r")
    s.check("CR paste: Enter answers", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("CR paste: the model got one message with both lines", log.wait_user("第三行\n第四行"))

    # A terminal that does not support bracketed paste hands the text over without
    # markers: chat's fallback must recognise the newline by the bytes that follow
    # it within a few milliseconds, otherwise the paste submits at its first line.
    m = s.mark()
    s.send("第五行\n第六行")
    s.check("a terminal without markers: both lines on the input line", s.until_raw("第五行\r\n第六行", m))
    s.check("a terminal without markers: no turn starts", log.drain() == [])
    m = s.mark()
    s.send("\r")
    s.check("a terminal without markers: the model got one message", log.wait_user("第五行\n第六行"))

    # A typed Enter with text after it still submits (the newline is only taken
    # for a paste when a byte follows it right away).
    # Multi-line input: Alt+Enter inserts a newline instead of submitting. Cursor
    # movement inside a multi-line input (Up/Down) is covered by lineedit's unit
    # tests; the PTY path here does not move yet (suga-ixb8 - the real terminal does).
    m = s.mark(); s.send("第一行"); s.send("\x1b\r"); s.send("第二行"); s.drain(0.5)
    s.check("alt+Enter: the input holds two lines", s.contains(r"第一行\r\n第二行", m))
    s.send("\x15"); time.sleep(0.2)  # Ctrl+U: clear the line again

    m = s.mark()
    s.line("普通一行")
    s.check("typed Enter still submits", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("typed Enter: the model got the line", log.wait_user("普通一行"))

    m = s.mark()
    time.sleep(0.3)
    s.send("\x04")  # Ctrl-D leaves chat
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-paste", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18805)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--reply", REPLY,
                             "--delay", "0.1"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        log = FakeLog(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        scenario(s, log)
        s.close()
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
