#!/usr/bin/env python3
"""End-to-end smoke test: chat stops a dangerous delete (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (tool mode, command `rm -rf ~`) and
`suga chat` in a PTY with tools.approval: auto and HOME pointing at a scratch
directory (the only thing the command could ever delete). Checks: the call
is still confirmed, with the danger shown; "y" is not enough (refused, the
scratch home survives); "yes" runs it (the scratch home is gone); the trace
records both calls with danger and the approval source.

  python3 -I scripts/ptysmoke/chat_danger.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-danger.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

DANGER = r"⚠ 危险删除：rm -rf ~：删除用户主目录"
PROMPT = r"输入 yes 执行"


def scenario(s, fake_home):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("删一下")
    s.check("auto mode still asks, danger shown", s.until(DANGER, m) and s.until(PROMPT, m))
    s.line("y")
    s.check("y is not enough: refused", s.until(r"用户拒绝执行危险删除", m) and s.until(r"tokens · ", m))
    s.check("y: scratch home kept", os.path.isdir(fake_home))
    m = s.mark(); s.line("再删一次")
    s.check("asked again", s.until(PROMPT, m))
    s.line("yes")
    s.check("yes: executed", s.until(r"⎿ ok", m) and s.until(r"tokens · ", m))
    s.check("yes: scratch home removed", not os.path.exists(fake_home))
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def check_trace(s, events):
    calls = [e for e in events if e.get("name") == "shell_exec"]
    s.check("trace: two shell_exec events", len(calls) == 2)
    if len(calls) == 2:
        denied, ran = (c.get("attrs") or {} for c in calls)
        s.check("trace: refused call is a denial", calls[0].get("kind") == "denial")
        s.check("trace: refused call has danger and user_denied",
                "用户主目录" in denied.get("danger", "") and denied.get("approval") == "user_denied")
        s.check("trace: run call has danger and approval=user",
                "用户主目录" in ran.get("danger", "") and ran.get("approval") == "user")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-danger", help="scratch dir (SUGA_HOME, HOME and workspace)")
    p.add_argument("--port", type=int, default=18799)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    fake_home = os.path.abspath(os.path.join(args.work, "fakehome"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(os.path.join(fake_home, "keep"), exist_ok=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra="  approval: auto\n")
    # HOME is the scratch dir: `rm -rf ~` can only ever delete it.
    env = dict(os.environ, SUGA_HOME=home, HOME=fake_home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "tool", "--delay", "0.2",
                             "--command", "rm -rf ~"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s, fake_home)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session))
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
            shutil.rmtree(fake_home, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
