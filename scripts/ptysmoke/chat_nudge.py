#!/usr/bin/env python3
"""PTY smoke for the "stopped too early" hint in chat (personal.CheckNudge).

The fake model (fakellm --mode nudge) writes a.txt and then answers with a
sentence that promises to continue, without running any command: the turn hits
two rules (unverified-write: a change without a following shell command,
promise-zh: the answer ends with "接下来我去跑测试"). Chat must only show one
gray hint line under the answer, must NOT start a turn on its own (the fake
sees exactly two requests: the write and the final answer), and the trace must
carry two "nudge" events with action=hinted.

The second part runs against a workspace .suga/nudge-rules.yaml that turns the
promise-* rules off: the same answer then only hits unverified-write, so the
hint and the trace no longer carry the promised continuation.

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
from chat_approval import trace_events  # noqa: E402
from chat_steer import FakeLog  # noqa: E402
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402


def session_from(text):
    found = re.search(r"续接：suga chat --resume ((?:sess_|sg_|s_)\S+)", text)
    return found.group(1) if found else ""


def chat_turn(session_env, ws, binary, expect_promise, written):
    """Runs one chat session in ws/env and returns (session, text-on-exit);
    expect_promise decides whether the hint must quote the promise check."""
    s = PtySession([binary, "chat"], env=session_env, cwd=ws)
    mark = s.mark()
    s.check("prompt", s.until(r"❯ ", mark))
    s.line("写个文件")
    s.check("turn completes", s.until(r"tokens · ", mark))
    s.check("hint line is shown", s.contains(r"⚠ ", mark))
    s.check("hint names the unchecked change", s.contains(r"最后修改 " + re.escape(written) + r" 之后没有运行 shell_exec", mark))
    if expect_promise:
        s.check("hint quotes the promised continuation", s.contains(r"结尾说接下来我去跑测试", mark))
    else:
        s.check("promise check is switched off", not s.contains(r"结尾说接下来我去跑测试", mark))
    exit_mark = s.mark()
    s.send(CTRL_C)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", exit_mark))
    session = session_from(s.text())
    s.close()
    s.check("session id", bool(session))
    return session, s.results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", default="tmp/smoke/suga")
    parser.add_argument("--work", default="tmp/smoke-nudge")
    parser.add_argument("--port", type=int, default=0,
                        help="fake model port; 0 picks free ports (the second home runs on the next four)")
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()
    # A second home answers on base+4 (see the nudge scenario).
    args.port = resolve_port(args.port, 4)

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    # A previous run leaves the second part's .suga/nudge-rules.yaml in ws, which
    # would switch the first part's promise rules off: start from an empty ws.
    shutil.rmtree(ws, ignore_errors=True)
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
    fake2 = subprocess.Popen(
        [sys.executable, "-I", os.path.join(here, "fakellm.py"),
         "--port", str(args.port + 4), "--mode", "nudge", "--delay", "0.2",
         "--write-path", "b.txt", "--write-body", "alpha",
         "--reply", "文件已写入。接下来我去跑测试。"],
        stdout=subprocess.PIPE, text=True)
    session = ""
    session2 = ""
    s = None
    log = None
    try:
        wait_port(fake)
        wait_port(fake2)
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
            s.check("trace records both rules", rules == ["promise-zh", "unverified-write"])
            s.check("trace carries the kinds",
                    all((e.get("attrs") or {}).get("kind") in ("promise", "unverified_write") for e in nudges))
            s.check("trace says hinted",
                    all((e.get("attrs") or {}).get("action") == "hinted" for e in nudges))

        # Second part: a workspace nudge-rules.yaml that switches the promise
        # rules off. The same answer then only hits unverified-write (its own
        # fake server on a second port and its own SUGA_HOME, so the first
        # session's fake and config stay as they were).
        os.makedirs(os.path.join(ws, ".suga"), exist_ok=True)
        with open(os.path.join(ws, ".suga", "nudge-rules.yaml"), "w", encoding="utf-8") as f:
            f.write("rules:\n  - name: promise-zh\n    enabled: false\n"
                    "  - name: promise-en\n    enabled: false\n")
        home2 = os.path.abspath(os.path.join(args.work, "home2"))
        write_home(home2, args.port + 4, tools_extra="  approval: auto\n")
        env2 = dict(os.environ, SUGA_HOME=home2, TERM="xterm", NO_COLOR="1")
        env2.pop("SUGA_CONFIG", None)
        session2, results2 = chat_turn(env2, ws, binary, expect_promise=False, written="b.txt")
        # The second part's checks count with the first part's.
        s.results += [("rules file: " + name, ok) for name, ok in results2]
        if session2:
            events = trace_events(binary, env2, session2)
            nudges = [e for e in events if e.get("kind") == "nudge"]
            rules = sorted({(e.get("attrs") or {}).get("rule") for e in nudges})
            s.check("workspace rules file applied", rules == ["unverified-write"])
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake2.terminate()
        fake.wait(timeout=5)
        fake2.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
            shutil.rmtree(home2, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
