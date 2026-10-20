#!/usr/bin/env python3
"""End-to-end smoke test: sub-agents (the agent tool) in chat (Linux/macOS).

fakellm.py (tool mode, --agent) has the model call the agent tool; the
sub-agent's requests (its system prompt names it) grep once and report.

sync: by default the sub-agent's grep is summarized (no `  ·` line) with a count/duration line; the `sync-full` case keeps the old per-call progress line. The report comes back as the tool result, and the trace has the agent tool event naming the sub-agent session, which `suga sessions` hides and `suga sessions --agents` lists.

cancel: the sub-agent is slow (--child-delay); Ctrl-C during it ends chat
cleanly (resume line), the interrupted turn reads 已中断本轮 (never the raw
"context canceled"), and the sub-agent session is not left running.

background: the agent call has background: true; the turn ends at once and
the sub-agent's end wakes the idle chat with a notice holding its report.

  python3 -I scripts/ptysmoke/chat_agent.py --bin tmp/smoke/suga

Exit status 0 when every check passes. --agents-off is the reverse check
(agents.enabled: false). The work dir defaults to tmp/smoke-agent.
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


def start_fake(port, *extra):
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "fakellm.py"), "--port", str(port),
                             "--mode", "tool", "--agent", "--delay", "0.2", *extra], stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return fake


def exit_chat(s):
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.until(r"续接：suga chat --resume sess_", m)
    found = re.search(r"--resume (sess_\S+)", s.text(m))
    return found.group(1) if found else ""


def sessions(binary, env, *args):
    # --all: sessions lists the current directory's sessions by default, and
    # this scenario's chat ran in its own workspace.
    out = subprocess.run([binary, "sessions", "--json", "-n", "0", "--all", *args], capture_output=True, text=True,
                         env=env, timeout=30)
    try:
        return json.loads(out.stdout or "[]")
    except ValueError:
        return []


def sync_scenario(s, binary, env, full=False):
    m = s.mark()
    s.check("sync: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("调研一下")
    s.check("sync: agent call shown", s.until(r"⏺ agent", m))
    if full:
        s.check("sync full: sub-agent progress line", s.until(r"  · file_grep", m))
    if not full:
        # full keeps the old per-call lines and prints no summary line.
        s.check("sync summary: count and duration line", s.until(r"子 agent smoke survey：1 次工具调用，耗时", m))
    s.check("sync: report is the result", s.until(r"⎿ ok: SUBREPORT: SMOKE is in target\.txt", m))
    s.check("sync: turn ends", s.until(r"tokens · ", m))
    if not full:
        # Checked once the turn has ended: right after the agent call the
        # sub-agent has not run its grep yet, so an early check proves nothing.
        s.check("sync summary: no per-call progress line", not s.contains(r"  · file_grep", m))
    session = exit_chat(s)
    s.check("sync: session id", bool(session))
    events = trace_events(binary, env, session) if session else []
    agent = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "agent"), {})
    child = (agent.get("attrs") or {}).get("child_session_id", "")
    s.check("sync trace: agent event names the sub-agent session", child.startswith("sess_"))
    s.check("sync trace: stop=final", (agent.get("attrs") or {}).get("stop") == "final")
    listed = [r["id"] for r in sessions(binary, env)]
    s.check("sync: sessions hides the sub-agent session", session in listed and child not in listed)
    s.check("sync: sessions --agents lists it", child in [r["id"] for r in sessions(binary, env, "--agents")])


def cancel_scenario(s, binary, env, label="cancel", steering=True):
    """Ctrl-C while a slow sub-agent runs: with steering on it ends this turn only
    (chat asks for the next line again, a second press leaves), with steering off it
    ends the whole chat as before."""
    def check(name, ok):
        return s.check(f"{label}: {name}", ok)

    m = s.mark()
    check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("调研一下")
    check("sub-agent at work", s.until(r"  · file_grep", m))
    m = s.mark()
    s.send(CTRL_C)  # while its report is slow to come
    # The interrupted turn is reported as 已中断本轮: the runner's raw
    # "context canceled" must not reach the terminal.
    check("interrupted turn reads 已中断本轮", s.until(r"已中断本轮", m, timeout=15))
    if steering:
        # Only the turn ended: chat is still there and takes the next line.
        check("chat stays after one Ctrl-C", not s.contains(r"续接：", m))
        time.sleep(0.3)
        s.send(CTRL_C)  # the first press counts: within the confirmation window
    check("chat ends with the resume line", s.until(r"续接：suga chat --resume sess_", m, timeout=15))
    check("never shows the raw context canceled", not s.contains(r"context canceled", m))
    s.pump(0.5)
    rows = sessions(binary, env, "--agents")
    child = next((r for r in rows if r.get("parent")), {})
    check("the sub-agent session is not left running", child and child.get("status") != "running")


def background_scenario(s):
    m = s.mark()
    s.check("background: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("后台调研")
    s.check("background: task started", s.until(r"background agent task t1 started: smoke survey", m))
    s.check("background: turn ends", s.until(r"tokens · ", m))
    m = s.mark()
    s.check("background: summary", s.until(r"子 agent smoke survey：1 次工具调用，耗时", m, timeout=20))
    s.check("background: idle chat woken by its report", s.until(r'agent "smoke survey" finished \(stop=final\)', m, timeout=20))
    s.check("background: report in the notice", s.until(r"SUBREPORT: SMOKE is in target\.txt", m))
    s.check("background: notice turn ends", s.until(r"tokens · ", m))
    exit_chat(s)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-agent", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18803)
    p.add_argument("--keep", action="store_true", help="keep the scratch homes after the run")
    p.add_argument("--agents-off", action="store_true", help="reverse check: agents.enabled false")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    results, homes = [], []
    s = fake = None
    agents = "agents:\n  enabled: false\n" if args.agents_off else ""
    scenarios = [
        ("sync", [], "", lambda s, env: sync_scenario(s, binary, env)),
        ("sync-full", [], "chat:\n  agent_progress: full\n", lambda s, env: sync_scenario(s, binary, env, full=True)),
        # The two Ctrl-C groups (plan F1): with steering on (the default) a press
        # ends the turn only, with chat.steering false it ends chat as before.
        ("cancel", ["--child-delay", "30"], "chat:\n  agent_progress: full\n", lambda s, env: cancel_scenario(s, binary, env)),
        ("cancel-off", ["--child-delay", "30"], "chat:\n  agent_progress: full\n  steering: false\n",
         lambda s, env: cancel_scenario(s, binary, env, label="cancel-off", steering=False)),
        ("background", ["--background"], "", lambda s, env: background_scenario(s)),
    ]
    try:
        for name, fake_args, extra, run in scenarios:
            home = os.path.abspath(os.path.join(args.work, "home-" + name))
            ws = os.path.abspath(os.path.join(args.work, "ws-" + name))
            homes.append(home)
            shutil.rmtree(home, ignore_errors=True)
            os.makedirs(ws, exist_ok=True)
            isolate_ws(ws)
            with open(os.path.join(ws, "target.txt"), "w") as f:
                f.write("SMOKE_TARGET here\n")
            port = args.port + len(homes)
            write_home(home, port, extra=agents + extra)
            env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
            env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
            fake = start_fake(port, *fake_args)
            s = PtySession([binary, "chat"], env=env, cwd=ws)
            run(s, env)
            s.close()
            results += s.results
            fake.terminate(); fake.wait(timeout=5)
            fake = None
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
