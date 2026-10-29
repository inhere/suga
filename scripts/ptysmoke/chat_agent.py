#!/usr/bin/env python3
"""End-to-end smoke test: sub-agents (the agent tool) in chat (Linux/macOS).

fakellm.py (tool mode, --agent) has the model call the agent tool; the
sub-agent's requests (its system prompt names it) grep once and report.

sync: by default the sub-agent's grep is summarized (no `  ·` line) with a count/duration line; the `sync-full` case keeps the old per-call progress line. The report comes back as the tool result, and the trace has the agent tool event naming the sub-agent session, which `suga session list` hides and `suga session list --agents` lists.

cancel: the sub-agent is slow (--child-delay); Ctrl-C during it ends chat
cleanly (resume line), the interrupted turn reads 已中断本轮 (never the raw
"context canceled"), and the sub-agent session is not left running.

background: the agent call has background: true; the turn ends at once and
the sub-agent's end wakes the idle chat with a notice holding its report.

error: the sub-agent's model call fails for good (--child-fail): the status
line must stop counting it when its error result comes back (a failed child has
no summary), so the waiting line reads 思考中… again.

cancel-status: Ctrl-C during a slow sub-agent ends the turn; the next turn's
status line must not count the cancelled sub-agent (no tool result ever came).

kill-status: a running background agent is killed with :tasks -k; the status
line counts a background agent by its task, so the next turn must not show it
either.

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
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402
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
    s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m)
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def sessions(binary, env, *args):
    # --all: sessions lists the current directory's sessions by default, and
    # this scenario's chat ran in its own workspace.
    out = subprocess.run([binary, "session", "list", "--json", "-n", "0", "--all", *args], capture_output=True, text=True,
                         env=env, timeout=30)
    try:
        return json.loads(out.stdout or "[]")
    except ValueError:
        return []


def sync_scenario(s, binary, env, full=False):
    m = s.mark()
    s.check("sync: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("调研一下")
    s.check("sync: agent call shown", s.until(r"◆ agent", m))
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
    s.check("sync trace: agent event names the sub-agent session", child.startswith(("sess_", "sg_", "s_")))
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
    check("chat ends with the resume line", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=15))
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


# NOTE: the waiting line ("子 agent：N 个运行中") is drawn by the spinner, which is
# off under NO_COLOR - and the smokes run with NO_COLOR=1. The "no running
# count" checks below can therefore never see a stale label; they guard the
# turn flow only. The label's lifecycle is covered by the unit test
# TestRendererAgentStatusLifecycle (internal/chat/render_test.go), which fails
# when EndTurnAgents does nothing (reverse-checked 2026-10-23).
def error_scenario(s):
    """A sub-agent that fails for good: its entry ends with the error result, so
    the waiting line goes back to 思考中… (it used to keep 子 agent：1 个运行中)."""
    m = s.mark()
    s.check("error: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("调研一下")
    s.check("error: agent call shown", s.until(r"◆ agent", m))
    s.check("error: failed sub-agent result", s.until(r"⎿ error: sub-agent failed", m, timeout=20))
    # The model is asked again after the result: its waiting line must no longer
    # count the failed sub-agent. Without the cleanup the label stays
    # "子 agent：1 个运行中（smoke survey 0 次调用）" and this never appears.
    # The failed call still gets its one summary line; what must not happen is
    # the running count staying on the status line after the result.
    r = s.mark()
    s.check("error: turn ends", s.until(r"tokens · ", m, timeout=20))
    s.check("error: no running count after the result", not s.contains(r"子 agent：\d+ 个运行中", r))
    exit_chat(s)


def cancel_status_scenario(s):
    """Ctrl-C during a slow sub-agent: the next turn's status line starts clean."""
    m = s.mark()
    s.check("cancel-status: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("调研一下")
    s.check("cancel-status: sub-agent at work", s.until(r"  · file_grep", m))
    m = s.mark(); s.send(CTRL_C)
    s.check("cancel-status: turn interrupted", s.until(r"已中断本轮", m, timeout=15))
    s.check("cancel-status: chat takes the next line", s.until(r"❯ ", m, timeout=15))
    # A plain turn (no 调研 in the message: the fake answers with text): its
    # waiting line must not count the cancelled sub-agent.
    m = s.mark(); s.line("你好")
    s.check("cancel-status: no stale sub-agent count", not s.contains(r"子 agent：", m))
    s.check("cancel-status: turn ends", s.until(r"tokens · ", m, timeout=20))
    exit_chat(s)


def kill_status_scenario(s):
    """A killed background agent leaves the status line by its task, not by a
    notice (Kill suppresses that notice)."""
    m = s.mark()
    s.check("kill-status: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("后台调研")
    s.check("kill-status: task started", s.until(r"background agent task t1 started", m))
    s.check("kill-status: turn ends", s.until(r"tokens · ", m, timeout=20))
    m = s.mark(); s.line(":tasks")
    s.check("kill-status: task listed as running", s.until(r"t1 · running", m))
    m = s.mark(); s.line(":tasks -k t1")
    s.check("kill-status: killed", s.until(r"已终止任务 t1：", m))
    m = s.mark(); s.line(":tasks")
    s.check("kill-status: task listed as killed", s.until(r"t1 · killed", m))
    m = s.mark(); s.line(":tasks -k t1")
    s.check("kill-status: killing it again says it ended", s.until(r"任务 t1 已结束（killed），无需终止", m))
    # The sub-agent is gone from the manager: the next turn's waiting line must
    # not count it (the old code kept the entry until a notice that never comes).
    m = s.mark(); s.line("你好")
    s.check("kill-status: no stale sub-agent count", not s.contains(r"子 agent：", m))
    s.check("kill-status: turn ends", s.until(r"tokens · ", m, timeout=20))
    exit_chat(s)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-agent", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0,
                   help="fake model port; 0 picks free ports (one scenario per group)")
    p.add_argument("--keep", action="store_true", help="keep the scratch homes after the run")
    p.add_argument("--agents-off", action="store_true", help="reverse check: agents.enabled false")
    args = p.parse_args()
    # One fake model per scenario: the base port and the next seven.
    args.port = resolve_port(args.port, 8)

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
        ("error", ["--agent-when", "调研", "--child-fail", "--delay", "0.4"], "", lambda s, env: error_scenario(s)),
        ("cancel-status", ["--child-delay", "30", "--agent-when", "调研", "--delay", "0.5"],
         "chat:\n  agent_progress: full\n", lambda s, env: cancel_status_scenario(s)),
        # --child-delay keeps the sub-agent running, so :tasks has a task to kill.
        ("kill-status", ["--background", "--agent-when", "调研", "--child-delay", "30", "--delay", "0.5"], "",
         lambda s, env: kill_status_scenario(s)),
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
