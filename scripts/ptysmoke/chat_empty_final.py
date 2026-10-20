#!/usr/bin/env python3
"""PTY smoke for an empty final answer after a successful tool call.

The fake model writes a file, then returns an empty streamed response on every
resample. Chat must finish normally without printing an empty ``▣ `` answer
line; the turn and final model trace events must carry ``empty_final``.

Reverse verification: temporarily remove the runtime's empty-final fallback;
the model call then fails and the completion/trace checks below fail.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402
from ptydrive import CTRL_C, PtySession  # noqa: E402


def session_from(text):
    found = re.search(r"续接：suga chat --resume ((?:sess_|s_)\S+)", text)
    return found.group(1) if found else ""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", default="tmp/smoke/suga")
    parser.add_argument("--work", default="tmp/smoke-empty-final")
    parser.add_argument("--port", type=int, default=18806)
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen(
        [sys.executable, "-I", os.path.join(here, "fakellm.py"),
         "--port", str(args.port), "--mode", "empty-final", "--delay", "0.2",
         "--write-path", "a.txt", "--write-body", "alpha"],
        stdout=subprocess.PIPE, text=True)
    session = ""
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        mark = s.mark()
        s.check("prompt", s.until(r"❯ ", mark))
        s.line("只写文件")
        s.check("empty final completes", s.until(r"tokens · ", mark))
        s.check("empty final has no answer marker", not s.contains(r"▣ \n", mark))
        exit_mark = s.mark()
        s.send(CTRL_C)
        s.send(CTRL_C)
        s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", exit_mark))
        session = session_from(s.text())
        s.close()
        s.check("session id", bool(session))
        if session:
            events = trace_events(binary, env, session)
            models = [e for e in events if e.get("kind") == "model"]
            turns = [e for e in events if e.get("kind") == "turn"]
            s.check("trace model marks empty final", any((e.get("attrs") or {}).get("empty_final") is True for e in models))
            s.check("trace turn marks empty final", any((e.get("attrs") or {}).get("empty_final") is True for e in turns))
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
