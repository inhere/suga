#!/usr/bin/env python3
"""End-to-end smoke test: the PreToolUse hook gates a tool call (Linux/macOS).

Two scenes on scripts/ptysmoke/fakellm.py (tool mode) and a hook command
answering PreToolUse (`hooks.pre_tool_use.tools: [shell_exec]`):

1. deny: the hook refuses the call, so chat never asks for approval, the person
   sees the hook's reason as the tool error and nothing runs.
   trace: a PreToolUse hook event with decision=deny.
2. rewrite: the hook replaces the command, so the approval question (and the run)
   shows the rewritten command, not the one the model sent
   (rewritten arguments are approved again). trace: rewritten_args=true and
   approval=user on the shell_exec event.

  python3 -I scripts/ptysmoke/chat_pretool.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-pretool.
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

DENY_REASON = "SMOKE pre-tool deny"
REWRITTEN = "echo SMOKE_REWRITTEN"
HOOK_SCRIPT = """import json, sys
event = json.loads(sys.stdin.readline() or "{}")
if event.get("hook_event_name") != "PreToolUse":
    sys.exit(0)
args = event.get("tool_input") or {}
command = str(args.get("command", ""))
if "SMOKE_DENY" in command:
    print(json.dumps({"hookSpecificOutput": {"permissionDecision": "deny",
                                             "permissionDecisionReason": %r}}))
elif "SMOKE_OK" in command:
    args["command"] = %r
    print(json.dumps({"hookSpecificOutput": {"updatedInput": args}}))
""" % (DENY_REASON, REWRITTEN)


def write_scene(home, port, hook):
    """Writes the scene's SUGA_HOME: hooks on, only shell_exec sends PreToolUse."""
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(home, exist_ok=True)
    with open(hook, "w", encoding="utf-8") as f:
        f.write(HOOK_SCRIPT)
    write_home(home, port, extra=("hooks:\n"
                                  f"  command: [{json.dumps(sys.executable)}, -I, {json.dumps(hook)}]\n"
                                  "  pre_tool_use:\n"
                                  "    tools: [shell_exec]\n"))


def start_scene(binary, home, ws, port, here, command):
    """Starts fakellm and a chat PTY for one scene; returns (session, fake)."""
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="on", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "tool", "--command", command,
                             "--delay", "0.5"], stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return PtySession([binary, "chat"], env=env, cwd=ws), fake, env


def trace_events(binary, env, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"],
                         capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def finish(s):
    """Ends the chat with two Ctrl-C presses and returns the session id."""
    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def deny_scene(s):
    """The hook's deny refuses the call before any approval question."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一下")
    s.check("deny reason shown", s.until(re.escape(DENY_REASON), m))
    s.check("no approval question", not s.until(r"允许执行 shell_exec", m, timeout=2))
    # The command text shows on the call line itself: only a result line counts.
    s.check("nothing ran", not s.until(r"⎿[^\n]*SMOKE_DENY", m, timeout=0.5))
    s.check("turn ended", s.until(r"tokens · ", m))
    return finish(s)


def rewrite_scene(s):
    """The rewritten command is what the approval question and the run show."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("跑一下")
    s.check("question shows the rewritten command", s.until(r"允许执行 shell_exec", m)
            and REWRITTEN in s.text(m))
    s.line("y")
    s.check("rewritten command ran", s.until(r"⎿ ok: SMOKE_REWRITTEN", m))
    s.check("turn ended", s.until(r"tokens · ", m))
    return finish(s)


def check_deny_trace(s, events):
    hooks = [e for e in events if e.get("kind") == "hook" and e.get("name") == "PreToolUse"]
    s.check("trace: PreToolUse hook event", len(hooks) >= 1)
    s.check("trace: decision=deny", any((e.get("attrs") or {}).get("decision") == "deny" for e in hooks))
    shell = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "shell_exec"), {})
    s.check("trace: the shell_exec call is recorded without a run", "output" not in (shell.get("attrs") or {}))


def check_rewrite_trace(s, events):
    hooks = [e for e in events if e.get("kind") == "hook" and e.get("name") == "PreToolUse"]
    s.check("trace: PreToolUse hook event", len(hooks) >= 1)
    s.check("trace: rewritten_args", any((e.get("attrs") or {}).get("rewritten_args") for e in hooks))
    shell = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "shell_exec"), {})
    s.check("trace: approval=user after the rewrite", (shell.get("attrs") or {}).get("approval") == "user")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-pretool", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0,
                   help="fake model port; 0 picks free ports (the second scene runs on the next one)")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    # Two scenes: the base port and base+1.
    args.port = resolve_port(args.port, 1)

    binary = os.path.abspath(args.bin)
    here = os.path.dirname(os.path.abspath(__file__))
    sessions = []
    fakes = []
    checks = []
    try:
        for index, (name, command, scene, check) in enumerate((
                ("deny", "echo SMOKE_DENY", deny_scene, check_deny_trace),
                ("rewrite", "echo SMOKE_OK", rewrite_scene, check_rewrite_trace))):
            work = os.path.abspath(os.path.join(args.work, name))
            home = os.path.join(work, "home")
            ws = os.path.join(work, "ws")
            shutil.rmtree(work, ignore_errors=True)  # a leftover home would change behavior
            os.makedirs(ws, exist_ok=True)
            isolate_ws(ws)
            hook = os.path.join(home, "hook.py")
            write_scene(home, args.port + index, hook)
            s, fake, env = start_scene(binary, home, ws, args.port + index, here, command)
            sessions.append(s)
            fakes.append(fake)
            session = scene(s)
            s.close()
            s.check("session id", bool(session))
            if session:
                check(s, trace_events(binary, env, session))
            checks.append((name, s))
    finally:
        for s in sessions:
            s.close(grace=0)
        for fake in fakes:
            fake.terminate()
            fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(os.path.abspath(args.work), ignore_errors=True)

    # Every scene's checks count: merge them into one result list.
    total = 0
    failed = []
    for name, s in checks:
        total += len(s.results)
        failed += [f"{name}: {f}" for f in s.failed()]
    print(f"\n{total - len(failed)}/{total} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
