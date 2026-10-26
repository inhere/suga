#!/usr/bin/env python3
"""Check that model and tool text cannot emit terminal control sequences.

This is intentionally falsifiable: removing either the chat text cleaner or the
streaming cleaner makes the raw PTY assertion fail (not just the ANSI-stripped
text assertion). It runs only on Linux/macOS through ``make smoke-chat``.
"""
import argparse
import os
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

DANGEROUS = ("\x1b]52", "\x1b[2J")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    a = p.parse_args(); a.port = resolve_port(a.port)
    home = os.path.abspath(a.work + "/home")
    ws = os.path.abspath(a.work + "/ws")
    shutil.rmtree(a.work, ignore_errors=True)
    os.makedirs(ws)
    isolate_ws(ws)
    write_home(home, a.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)

    here = os.path.dirname(os.path.abspath(__file__))
    reply = "ANSWER \x1b]52;c;PASTE\x07\x1b[2JVISIBLE"
    command = "printf 'TOOL_OK \\033]52;c;PASTE\\007\\033[2JVISIBLE'"
    fake = subprocess.Popen(
        [sys.executable, "-I", here + "/fakellm.py", "--port", str(a.port),
         "--mode", "tool", "--command", command, "--reply", reply, "--delay", "0.1"],
        stdout=subprocess.PIPE, text=True)
    s = PtySession([os.path.abspath(a.bin), "chat"], env=env, cwd=ws)
    try:
        m = s.mark()
        s.check("prompt", s.until(r"❯ ", m))
        m = s.mark()
        s.line("sanitize")
        s.check("tool result text survives", s.until(r"TOOL_OK .*VISIBLE", m))
        s.check("answer text survives", s.until(r"ANSWER .*VISIBLE", m))
        raw = s.text_raw(m)
        for sequence in DANGEROUS:
            s.check("raw PTY has no " + repr(sequence), sequence not in raw)
    finally:
        s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
    if s.failed():
        print("failed: " + ", ".join(s.failed()))
        raise SystemExit(1)


if __name__ == "__main__":
    main()
