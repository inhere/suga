#!/usr/bin/env python3
"""End-to-end smoke test of win32-input-mode (DECSET 9001) in `suga chat` (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (text mode) and runs `suga chat` in a PTY with
`chat.win32_input_mode: on` in its config. The PTY stands in for Windows Terminal:
it answers the mode request by sending every key press as the record
`CSI Vk;Sc;Uc;Kd;Cs;Rc _` (a down and an up record each), which is what the
terminal sends once DECSET 9001 is on. On Windows the mode is what makes
Ctrl+Enter a newline without a settings.json binding (suga-tcgw).

Checked:
  1. chat asks for the mode while the line is editable (ESC[?9001h);
  2. CJK text arrives through the records (one per UTF-16 code unit) and shows on
     the input line;
  3. Ctrl+Enter (Vk=13, Cs=8) inserts a newline: both lines are on the input line
     as two rows and no turn starts;
  4. Enter (a record as well) sends ONE message, and the model receives exactly
     "第一行\\n第二行" (fakellm's per-request log carries the user text);
  5. Backspace and a repeat count work through records: "abc" + Backspace + a
     held Backspace (Rc=2) leave "a"... seen from the message the model gets;
  6. a `!` local command runs with the mode off (a child process reads plain
     keys) and the mode is back afterwards: Ctrl+Enter inserts a newline again
     instead of submitting, and Enter submits the two lines;
  7. Ctrl-D leaves chat and the terminal is restored (ESC[?9001l at the end).

Reverse verification: drop the Ctrl+Enter mapping in lineedit/win32input.go
(win32KeyOf, the `ctrl || alt || shift` branch on win32VKReturn) and the record
starts a turn: check 3 ("no turn starts") and checks 4/6 fail.

  python3 -I scripts/ptysmoke/chat_win32input.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-win32input.
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
MODE_ON = r"\x1b\[\?9001h"
MODE_OFF = r"\x1b\[\?9001l"


class FakeLog:
    """The JSON lines fakellm writes on stdout, one per model request."""

    def __init__(self, proc):
        self.proc = proc
        self.entries = []

    def drain(self, timeout=0.3):
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
        end = time.time() + timeout
        while time.time() < end:
            if any(want in text for text in self.users()):
                return True
            self.drain(0.3)
        return False


def rec(vk, uc, cs=0, kd=1, sc=0, rc=1):
    """One win32-input-mode record (vm code, scan code, code unit, key down,
    control state, repeat count)."""
    return f"\x1b[{vk};{sc};{uc};{kd};{cs};{rc}_"


def press(vk, uc, cs=0, sc=0, rc=1):
    """A key press as the terminal reports it: the down record and the up record."""
    return rec(vk, uc, cs=cs, sc=sc, rc=rc) + rec(vk, uc, cs=cs, kd=0, sc=sc, rc=rc)


def text(s):
    """Plain typed text: one record per UTF-16 code unit (a character outside the
    BMP arrives as two records, a surrogate pair)."""
    return "".join(press(0, unit) for unit in _units(s))


def _units(s):
    data = s.encode("utf-16-le")
    return [int.from_bytes(data[i:i + 2], "little") for i in range(0, len(data), 2)]


ENTER = press(13, 13, sc=28)
CTRL_ENTER = press(13, 10, cs=8, sc=28)
BACKSPACE = press(8, 8)


def isolate_ws(ws):
    """Make ws its own git root, so suga does not pick up the enclosing repository."""
    subprocess.run(["git", "init", "-q", ws], check=True)


def write_home(home, port):
    """Writes SUGA_HOME/config.yaml: the fake model and win32-input-mode on.

    The mode is `auto` outside Windows Terminal (no WT_SESSION here), so the
    scenario turns it on explicitly; project memory is forced to the files
    backend like the other chat smokes.
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
chat:
  win32_input_mode: on
""")


def wait_port(proc):
    line = proc.stdout.readline()  # {"listening": port}
    if "listening" not in line:
        raise SystemExit("fake model did not start: " + line)


def scenario(s, log):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    s.check("win32-input-mode is on while the line is editable", s.until_raw(MODE_ON, m))

    # CJK text through the records, then Ctrl+Enter: a newline, not a submit.
    m0 = s.mark()
    s.send(text("第一行"))
    s.check("records type CJK text", s.until_raw("第一行", m0))
    m = s.mark()
    s.send(CTRL_ENTER)
    s.send(text("第二行"))
    # How the editor redraws a second line is its own business (it may or may
    # not repaint the first one): both lines are on screen since before the
    # first was typed, and the model check below proves they form one message.
    s.check("Ctrl+Enter puts both lines on the input line",
            s.until_raw("第二行", m) and "第一行" in s.text(m0))
    s.check("Ctrl+Enter starts no turn", log.drain() == [])

    m = s.mark()
    s.send(ENTER)
    s.check("Enter answers", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("the model got one message with both lines", log.wait_user("第一行\n第二行"))
    s.check("exactly one user message carries it",
            sum(1 for t in log.users() if "第二行" in t) == 1)

    # Backspace and a held key (Rc) through records: "abc", one Backspace, then a
    # held Backspace that stands for two presses leaves nothing, so type it again
    # with a single held Backspace counted from the message the model gets.
    m = s.mark()
    s.send(text("abcd") + BACKSPACE + press(8, 8, rc=2) + ENTER)
    s.check("backspace and a repeat count delete through records", s.until(r"▣ " + REPLY, m))
    s.check("the line after Backspace, a held Backspace and Enter is a",
            log.wait_user("a"))

    # A `!` local command: a child process reads plain keys, so the mode goes off
    # for as long as it runs and comes back afterwards.
    m = s.mark()
    s.line("!echo 本地命令")
    s.check("the local command runs", s.until(r"本地命令", m))
    s.check("the prompt comes back", s.until(r"\n❯ ", m))
    s.check("the mode was switched off for the child process", s.contains_raw(MODE_OFF, m))
    m = s.mark()
    s.send(text("上") + CTRL_ENTER + text("下"))
    s.check("the mode is back: Ctrl+Enter is a newline again",
            s.until_raw("上\r\n下", m))
    s.check("no turn started for it", log.drain() == [])
    m = s.mark()
    s.send(ENTER)
    s.check("Enter sends it", s.until(r"▣ " + REPLY, m))
    s.check("the model got the two lines after the local command", log.wait_user("上\n下"))

    # Leaving chat restores the terminal.
    m = s.mark()
    time.sleep(0.3)
    s.send(press(68, 4, cs=8))  # Ctrl-D
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))
    s.check("exit: win32-input-mode is switched off again", s.until_raw(MODE_OFF, m))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-win32input", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18831)
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
