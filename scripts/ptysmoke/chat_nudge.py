#!/usr/bin/env python3
"""PTY smoke for the "stopped too early" hint in chat (personal.CheckNudge).

The fake model (fakellm --mode nudge) writes a.txt and then answers with a
sentence that promises to continue, without running any command: the turn hits
both rules (N1: a change without a following shell_exec, N2: the answer ends
with "接下来我去跑测试"). Chat must only show one gray hint line under the
answer, must NOT start a turn on its own (the fake sees exactly two requests:
the write and the final answer), and the trace must carry two "nudge" events
with action=hinted (rule N1 and N2).

Reverse verification: temporarily remove the s.nudgeHint(result) call in
chat_turn.go; the hint line is missing and the checks below fail.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402
from chat_steer import FakeLog  # noqa: E402
from ptydrive import CTRL_C, PtySession  # noqa: E402


def session_from(text):
    found = re.search(r"续接：suga chat --resume ((?:sess_|sg_|s_)\S+)", text)
    return found.group(1) if found else ""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", default="tmp/smoke/suga")
    parser.add_argument("--work", default="tmp/smoke-nudge")
    parser.add_argument("--port", type=int, default=18870)
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
         "--port", str(args.port), "--mode", "nudge", "--delay", "0.2",
         "--write-path", "a.txt", "--write-body", "alpha",
         "--reply", "文件已写入。接下来我去跑测试。"],
        stdout=subprocess.PIPE, text=True)
    session = ""
    s = None
    log = None
    try:
        wait_port(fake)
        log = FakeLog(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        mark = s.mark()
        s.check("prompt", s.until(r"❯ ", mark))
        s.line("写个文件")
        s.check("turn completes", s.until(r"tokens · ", mark))
        s.check("hint line is shown", s.contains(r"⚠ ", mark))
        s.check("hint names the unchecked change", s.contains(r"最后修改 a.txt 之后没有运行 shell_exec", mark))
        s.check("hint quotes the promised continuation", s.contains(r"结尾说接下来我去跑测试", mark))
        # No push in chat: the hint is all that happens, so the fake got exactly
        # the two requests of the one turn (write, final answer) and no third one
        # arrives afterwards.
        got = log.wait(2)
        s.check("one turn only: the write and the answer", len(got) == 2)
        time.sleep(1.5)
        s.check("no turn started on its own", len(log.requests()) == 2)
        exit_mark = s.mark()
        s.send(CTRL_C)
        s.send(CTRL_C)
        s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", exit_mark))
        session = session_from(s.text())
        s.close()
        s.check("session id", bool(session))
        if session:
            events = trace_events(binary, env, session)
            nudges = [e for e in events if e.get("kind") == "nudge"]
            rules = sorted({(e.get("attrs") or {}).get("rule") for e in nudges})
            s.check("trace records both rules", rules == ["N1", "N2"])
            s.check("trace says hinted",
                    all((e.get("attrs") or {}).get("action") == "hinted" for e in nudges))
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
