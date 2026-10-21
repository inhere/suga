#!/usr/bin/env python3
"""End-to-end smoke test: watch tasks and the background Stop hook in chat
(Linux/macOS).

watch: fakellm.py (tool mode, --background --watch-paths a.txt) has the model
start a watch on `cat a.txt` that fails when the file says "bad". Checks: the
watch starts and the turn ends; changing a.txt so the command fails wakes the
idle chat with a notice (exit 0 -> 3); writing the same content again (a new
mtime only) runs it again but leaves no notice; at exit the trace has the
watch's task event with its runs and changes.

stop: hooks.stop_background with a hook that sleeps 3s on Stop and then asks
to continue (not when stop_hook_active). The person types a second line right
after the first turn. Checks: the hook never gets an Interrupt event; the
first Stop is killed by the second; the second's block reply wakes chat as a
task notice and the turn it starts sends Stop with stop_hook_active.

  python3 -I scripts/ptysmoke/chat_watch.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-watch.
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
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))

# The Stop hook: log every event, sleep then ask to continue on a first Stop.
STOP_HOOK = r'''
import json, os, sys, time
event = json.loads(sys.stdin.readline() or "{}")
with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "hook.log"), "a") as f:
    f.write(json.dumps({"name": event.get("hook_event_name"), "active": event.get("stop_hook_active")}) + "\n")
if event.get("hook_event_name") == "Stop" and not event.get("stop_hook_active"):
    time.sleep(3)
    print(json.dumps({"decision": "block", "reason": "HOOK_CONTINUE please check again"}))
'''


def start_fake(port, *extra):
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "fakellm.py"), "--port", str(port),
                             "--delay", "0.2", *extra], stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return fake


def exit_chat(s):
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m)
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def watch_scenario(s, binary, env, ws):
    with open(os.path.join(ws, "a.txt"), "w") as f:
        f.write("good\n")
    m = s.mark()
    s.check("watch: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("监听一下")
    s.check("watch: started", s.until(r"watch task t1 started", m))
    s.check("watch: turn ends", s.until(r"tokens · ", m))
    s.pump(2.5)  # past a poll, so the baseline stamps are taken
    m = s.mark()
    with open(os.path.join(ws, "a.txt"), "w") as f:
        f.write("bad\n")
    s.check("watch: idle chat woken by the change", s.until(r"\[background task notice\] Background task updates", m))
    s.check("watch: notice says exit 0 -> 3", s.until(r"t1 \(watch\) reports: run 2 \(files: a\.txt\): exit 0 -> 3", m))
    s.check("watch: notice turn ends", s.until(r"tokens · ", m))
    s.pump(1.5)
    m = s.mark()
    with open(os.path.join(ws, "a.txt"), "w") as f:
        f.write("bad\n")  # the same result: a run, no notice
    # until keeps what it reads (pump drops it): no report within 7s.
    s.check("watch: the same result leaves no notice", not s.until(r"reports:", m, timeout=7))
    session = exit_chat(s)
    s.check("watch: session id", bool(session))
    # The watch is killed (and its end recorded) after the resume line.
    events = []
    for _ in range(25):
        events = trace_events(binary, env, session) if session else []
        if any(e.get("kind") == "task" for e in events):
            break
        s.pump(0.2)
    shell = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "shell_exec"), {})
    s.check("watch trace: shell_exec carries task_id", (shell.get("attrs") or {}).get("task_id") == "t1")
    task = next((e for e in events if e.get("kind") == "task"), {})
    a = task.get("attrs") or {}
    s.check("watch trace: task event of kind watch", a.get("kind") == "watch")
    s.check("watch trace: 3 runs, 1 change", a.get("runs") == 3 and a.get("changes") == 1)


def stop_scenario(s, binary, env, home):
    m = s.mark()
    s.check("stop: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("你好")
    s.check("stop: first turn ends", s.until(r"tokens · ", m))
    s.pump(0.5)  # back at the prompt
    m = s.mark(); s.line("第二句")  # while the first Stop still runs (it sleeps 3s)
    s.check("stop: second turn ends", s.until(r"tokens · ", m))
    m = s.mark()
    s.check("stop: block reply wakes chat as a notice",
            s.until(r"Stop hook asks to continue: HOOK_CONTINUE", m, timeout=15))
    s.check("stop: the notice turn ends", s.until(r"tokens · ", m))
    s.pump(1.5)  # its Stop (stop_hook_active) answers at once
    session = exit_chat(s)
    s.check("stop: session id", bool(session))
    log = []
    path = os.path.join(home, "hook.log")
    if os.path.exists(path):
        with open(path) as f:
            log = [json.loads(line) for line in f if line.strip()]
    stops = [e for e in log if e["name"] == "Stop"]
    s.check("stop: no Interrupt sent to the hook", not any(e["name"] == "Interrupt" for e in log))
    s.check("stop: three Stops, the last one stop_hook_active",
            len(stops) == 3 and stops[-1]["active"] is True and not stops[0]["active"])
    events = []
    for _ in range(25):
        events = trace_events(binary, env, session) if session else []
        if sum(1 for e in events if e.get("kind") == "task") >= 3:
            break
        s.pump(0.2)
    hooks = [e for e in events if e.get("kind") == "task" and (e.get("attrs") or {}).get("kind") == "hook"]
    status = {e.get("name"): e.get("status") for e in hooks}
    s.check("stop trace: first Stop task killed, second ok", status.get("t1") == "killed" and status.get("t2") == "ok")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-watch", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18801)
    p.add_argument("--keep", action="store_true", help="keep the scratch homes after the run")
    p.add_argument("--no-stop-background", action="store_true", help="reverse check: leave hooks.stop_background off")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    results = []
    s = fake = None
    homes = []
    try:
        # watch
        home = os.path.abspath(os.path.join(args.work, "home-watch"))
        ws = os.path.abspath(os.path.join(args.work, "ws-watch"))
        homes.append(home)
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(ws, ignore_errors=True)
        os.makedirs(ws, exist_ok=True)
        isolate_ws(ws)
        write_home(home, args.port, tools_extra="  approval: auto\n")
        env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
        env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
        fake = start_fake(args.port, "--mode", "tool", "--background", "--watch-paths", "a.txt",
                          "--command", "cat a.txt; if grep -q bad a.txt; then exit 3; fi")
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        watch_scenario(s, binary, env, ws)
        s.close()
        results += s.results
        fake.terminate(); fake.wait(timeout=5)

        # background Stop hook
        home = os.path.abspath(os.path.join(args.work, "home-stop"))
        ws = os.path.abspath(os.path.join(args.work, "ws-stop"))
        homes.append(home)
        shutil.rmtree(home, ignore_errors=True)
        os.makedirs(ws, exist_ok=True)
        isolate_ws(ws)
        os.makedirs(home, exist_ok=True)
        hook = os.path.join(home, "hook.py")
        with open(hook, "w", encoding="utf-8") as f:
            f.write(STOP_HOOK)
        background = "false" if args.no_stop_background else "true"
        write_home(home, args.port + 1, extra=f"hooks:\n  command: [{json.dumps(sys.executable)}, -I, {json.dumps(hook)}]\n"
                                               f"  stop_background: {background}\n  stop_wait_sec: 10\n")
        env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="on", TERM="xterm", NO_COLOR="1")
        env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
        fake = start_fake(args.port + 1, "--mode", "text")
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        stop_scenario(s, binary, env, home)
        s.close()
        results += s.results
    finally:
        if s is not None:
            s.close(grace=0)
        if fake is not None:
            fake.terminate()
            fake.wait(timeout=5)
        if not args.keep:
            for home in homes:
                shutil.rmtree(home, ignore_errors=True)
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
