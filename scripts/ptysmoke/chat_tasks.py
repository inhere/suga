#!/usr/bin/env python3
"""End-to-end smoke test: background tasks in chat and run (Linux/macOS).

chat: fakellm.py (tool mode, --background) has the model start
`sleep 1.5; echo TASK_DONE` as a background task and answer at once. Checks:
the task-started result, the turn ends while the task runs, then the task's
end wakes the idle chat (tasks.wake_on_exit) with a notice turn showing the
exit and the output; the trace has the shell_exec event with task_id and the
task event.

run: the same fake model starts `sleep 30` in the background and answers.
Checks: run reminds the model once (a second turn), then kills the task at
exit and says so on stderr; the process ends quickly.

  python3 -I scripts/ptysmoke/chat_tasks.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-tasks.
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


def chat_scenario(s):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("后台跑一下")
    s.check("task started", s.until(r"background task t1 started", m))
    # TASK_DONE alone is the task's output; "echo TASK_DONE" is the command shown.
    s.check("turn ends while the task runs", s.until(r"tokens · ", m) and not s.contains(r"(?<!echo )TASK_DONE", m))
    m = s.mark()
    s.check("idle chat woken by the task end", s.until(r"\[background task notice\]", m))
    s.check("notice: exit and output", s.until(r"t1 exited \(exit 0\)", m) and s.until(r"TASK_DONE", m))
    s.check("notice turn ends", s.until(r"tokens · ", m))
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def check_trace(s, events):
    shell = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "shell_exec"), {})
    s.check("trace: shell_exec carries task_id", (shell.get("attrs") or {}).get("task_id") == "t1")
    task = next((e for e in events if e.get("kind") == "task"), {})
    s.check("trace: task event", task.get("name") == "t1" and task.get("status") == "ok"
            and (task.get("attrs") or {}).get("lines", 0) >= 1)
    turns = [e for e in events if e.get("kind") == "turn"]
    s.check("trace: the notice turn is injected", len(turns) >= 2 and (turns[-1].get("attrs") or {}).get("injected") is True)


def run_scenario(s, binary, env, ws):
    start = time.time()
    out = subprocess.run([binary, "run", "--allow-write", "--prompt", "后台跑"], capture_output=True, text=True,
                         env=env, cwd=ws, timeout=60)
    took = time.time() - start
    s.check("run: exit 0", out.returncode == 0)
    s.check("run: task killed at exit, named on stderr", "killed background task at exit: t1 killed" in out.stderr)
    s.check("run: did not wait for the 30s task", took < 20)
    # sessions lists the current directory's sessions by default: ask from ws,
    # the directory the run worked in.
    sessions = subprocess.run([binary, "sessions", "--json", "-n", "1"], capture_output=True, text=True, env=env,
                              cwd=ws, timeout=30)
    sid = re.search(r'"id":"([^"]+)"', sessions.stdout)
    events = trace_events(binary, env, sid.group(1)) if sid else []
    turns = [e for e in events if e.get("kind") == "turn"]
    s.check("run: reminded once (two turns)", len(turns) == 2)
    task = next((e for e in events if e.get("kind") == "task"), {})
    s.check("run: task event killed", task.get("status") == "killed")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-tasks", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18800)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    here = os.path.dirname(os.path.abspath(__file__))

    def start_fake(command):
        fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"), "--port", str(args.port),
                                 "--mode", "tool", "--background", "--delay", "0.2", "--command", command],
                                stdout=subprocess.PIPE, text=True)
        wait_port(fake)
        return fake

    s = None
    fake = None
    try:
        fake = start_fake("sleep 1.5; echo TASK_DONE")
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = chat_scenario(s)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session))
        fake.terminate(); fake.wait(timeout=5)
        fake = start_fake("sleep 30")
        run_scenario(s, binary, env, ws)
    finally:
        if s is not None:
            s.close(grace=0)
        if fake is not None:
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
