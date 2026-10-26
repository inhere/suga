#!/usr/bin/env python3
"""End-to-end smoke test: chat's hook runs and a person's approval reach the
trace (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (tool mode) and `suga chat` in a PTY with
a hook command (a small script answering UserPromptSubmit with additional
context), asks for one shell_exec, waits at the approval question, answers y,
exits, then checks `suga inspect trace --json` of that session: hook events for
SessionStart, UserPromptSubmit (with the injected chars) and Stop; the
shell_exec event with approval=user and the answer time; the turn's context
event counting the hook context.

  python3 -I scripts/ptysmoke/chat_observe.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-observe.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

OK = r"⎿ ok: SMOKE_OK"
QUESTION = r"允许执行 shell_exec"
HOOK_CONTEXT = "SMOKE hook context: answer briefly."
WAIT_BEFORE_YES = 1.2  # seconds at the question, so approval_wait_ms is visible

HOOK_SCRIPT = """import json, sys
event = json.loads(sys.stdin.readline() or "{}")
if event.get("hook_event_name", event.get("name")) == "UserPromptSubmit" or event.get("Name") == "UserPromptSubmit":
    print(json.dumps({"hookSpecificOutput": {"additionalContext": %r}}))
""" % HOOK_CONTEXT


def scenario(s):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("跑一下")
    s.check("question", s.until(QUESTION, m))
    time.sleep(WAIT_BEFORE_YES)
    s.line("y")
    s.check("executed", s.until(OK, m) and s.until(r"tokens · ", m))
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def trace_events(binary, env, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def check_trace(s, events):
    hooks = {e.get("name"): e for e in events if e.get("kind") == "hook"}
    s.check("trace: SessionStart hook", "SessionStart" in hooks)
    submit = hooks.get("UserPromptSubmit", {})
    s.check("trace: UserPromptSubmit hook with injected chars",
            (submit.get("attrs") or {}).get("additional_chars") == len(HOOK_CONTEXT))
    s.check("trace: Stop hook", "Stop" in hooks)
    shell = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "shell_exec"), {})
    attrs = shell.get("attrs") or {}
    s.check("trace: approval=user", attrs.get("approval") == "user")
    s.check("trace: approval wait recorded", attrs.get("approval_wait_ms", 0) >= WAIT_BEFORE_YES * 1000 * 0.8)
    ctx = next((e for e in events if e.get("kind") == "context"), {})
    s.check("trace: context counts the hook context", (ctx.get("attrs") or {}).get("hook_context", 0) >= len(HOOK_CONTEXT))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-observe", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    hook = os.path.join(home, "hook.py")
    os.makedirs(home, exist_ok=True)
    with open(hook, "w", encoding="utf-8") as f:
        f.write(HOOK_SCRIPT)
    write_home(home, args.port, extra=f"hooks:\n  command: [{json.dumps(sys.executable)}, -I, {json.dumps(hook)}]\n")
    # Hooks on (the other scenarios turn them off).
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="on", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "tool", "--delay", "0.5"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s)
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
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
