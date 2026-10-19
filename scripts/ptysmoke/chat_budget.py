#!/usr/bin/env python3
"""Budget chat smoke for Linux/macOS PTYs.

The scenario uses the real chat binary and fake OpenAI server to check budget
status/reminders, soft wrap-up, session short-circuiting, estimated usage,
estimate=off, a returned steer becoming a fresh turn, and the no-budget
compatibility path. Trace JSON is checked after every budgeted session.

Reverse verification: temporarily remove the budget hook's AfterModel check,
stop forwarding the budget snapshot to Renderer.Handle, or do not reset the
turn budget for returned steering; the corresponding checks below must fail.
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
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402
from ptydrive import CTRL_C, PtySession  # noqa: E402


STATUS = r"tokens · "


def stop_fake(fake):
    fake.terminate()
    try:
        output, _ = fake.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        fake.kill()
        output, _ = fake.communicate(timeout=5)
    return [line for line in output.splitlines() if line.strip().startswith("{")]


def request_count(lines):
    return sum(1 for line in lines if json.loads(line).get("path") == "/v1/chat/completions")


def session_from(text):
    found = re.search(r"续接：suga chat --resume (sess_\S+)", text)
    return found.group(1) if found else ""


def exit_chat(s, mark):
    # Ctrl-C on a non-empty input line only clears it, so press up to three
    # times and stop once chat has left (as chat_steer.py does).
    for _ in range(3):
        time.sleep(0.25)
        s.send(CTRL_C)
        if s.until(r"续接：suga chat --resume sess_", mark, timeout=1):
            break
    s.check("exit: resume command", s.until(r"续接：suga chat --resume sess_", mark))


def start_group(binary, work, port, args, env_extra=None):
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    if env_extra:
        env.update(env_extra)
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "text", "--delay", "0.8"] + args,
                            stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return home, ws, env, fake


def budget_turn(binary, work, port, keep):
    home, ws, env, fake = start_group(binary, work, port, ["--prompt-tokens", "10"])
    s = PtySession([binary, "chat", "--budget-tokens", "12"], env=env, cwd=ws, size=(100, 30))
    try:
        m = s.mark()
        s.check("prompt", s.until(r"❯ ", m))
        s.line("第一轮")
        s.check("turn budget reaches 100%", s.until(r"budget 100%", m))
        s.check("token budget wrap-up is shown", s.until(r"本轮因 token 预算用尽收尾", m))
        s.check("first turn ends", s.until(STATUS, m))
        # A returned steer is deliberately inserted while the first request is
        # waiting. The completed budget turn hands it back as the next turn.
        m = s.mark()
        s.line("主任务")
        s.check("turn input stays available", s.until(r"↳ ", m))
        time.sleep(0.2)
        s.line("插话")
        s.check("steer starts a new budget turn", s.until(r"budget 100%", m))
        s.check("second turn ends", s.until(STATUS, m))
        # The steer the wrapped-up turn handed back runs as a third turn: wait
        # for it before leaving, or chat exits before it reaches the model.
        m = s.mark()
        s.check("returned steer runs as the next turn", s.until(STATUS, m))
        session = session_from(s.text())
        exit_chat(s, s.mark())
        session = session or session_from(s.text())
        lines = stop_fake(fake)
        # fakellm logs json.dumps lines (non-ASCII escaped): decode before matching.
        s.check("steer was sent to fake model", any("插话" in json.dumps(json.loads(line), ensure_ascii=False)
                                                    for line in lines if line.strip().startswith("{")))
        if session:
            events = trace_events(binary, env, session)
            budget = [e for e in events if e.get("kind") == "budget"]
            s.check("trace has budget event", bool(budget))
            attrs = (budget[-1].get("attrs") or {}) if budget else {}
            s.check("trace budget reason", attrs.get("reason") == "max_tokens")
            s.check("trace budget scope", attrs.get("scope") == "turn")
            s.check("trace budget is not estimated", attrs.get("estimated") is False)
    finally:
        s.close(grace=0)
        if fake.poll() is None:
            fake.terminate()
            fake.wait(timeout=5)
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results


def session_short_circuit(binary, work, port, keep):
    home, ws, env, fake = start_group(binary, work, port, ["--prompt-tokens", "10"])
    s = PtySession([binary, "chat", "--budget-tokens", "12", "--budget-scope", "session"],
                   env=env, cwd=ws, size=(100, 30))
    try:
        m = s.mark()
        s.until(r"❯ ", m)
        s.line("耗尽会话")
        s.check("session budget wraps up", s.until(r"session budget 100%", m))
        s.check("session wrap-up reason", s.until(r"本轮因 token 预算用尽收尾", m))
        before = s.mark()
        s.line("不应调用模型")
        s.check("spent session still returns to prompt", s.until(r"❯ ", before))
        s.check("short-circuit has no second wrap-up request", not s.contains(r"▣", before))
        session = session_from(s.text())
        mx = s.mark()
        exit_chat(s, mx)
        session = session or session_from(s.text())
        lines = stop_fake(fake)
        s.check("session short-circuit made no extra model request", request_count(lines) <= 2)
        if session:
            events = trace_events(binary, env, session)
            budget = [e for e in events if e.get("kind") == "budget"]
            attrs = (budget[-1].get("attrs") or {}) if budget else {}
            s.check("short-circuit trace is session scoped", attrs.get("scope") == "session")
    finally:
        s.close(grace=0)
        if fake.poll() is None:
            fake.terminate()
            fake.wait(timeout=5)
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results


def estimation(binary, work, port, keep, estimate_off):
    extra = {"SUGA_BUDGET_ESTIMATE": "off"} if estimate_off else None
    home, ws, env, fake = start_group(binary, work, port, ["--prompt-tokens", "10", "--no-usage"], extra)
    s = PtySession([binary, "chat", "--budget-tokens", "5"], env=env, cwd=ws, size=(100, 30))
    try:
        m = s.mark()
        s.until(r"❯ ", m)
        s.line("无用量")
        if estimate_off:
            s.check("estimate off shows zero budget", s.until(r"budget 0%", m))
            s.check("estimate off does not wrap up", not s.until(r"本轮因 token 预算用尽收尾", m, timeout=2))
        else:
            s.check("estimated budget is marked", s.until(r"budget ~100%", m))
            s.check("estimated budget wraps up", s.until(r"本轮因 token 预算用尽收尾", m))
        s.check("estimate turn ends", s.until(STATUS, m))
        session = session_from(s.text())
        exit_chat(s, s.mark())
        session = session or session_from(s.text())
        if session and not estimate_off:
            events = trace_events(binary, env, session)
            estimated = [e for e in events if (e.get("attrs") or {}).get("estimated") is True]
            s.check("trace marks estimated usage", bool(estimated))
    finally:
        s.close(grace=0)
        lines = stop_fake(fake)
        s.check("fake accepted no-usage mode", bool(lines))
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results


def status_mentions_budget(text):
    """Whether a status line (the line holding STATUS) mentions a budget."""
    return any("budget" in line for line in text.splitlines() if re.search(STATUS, line))


def no_budget(binary, work, port, keep):
    home, ws, env, fake = start_group(binary, work, port, ["--prompt-tokens", "10"])
    s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
    try:
        m = s.mark()
        s.until(r"❯ ", m)
        s.line("旧行为")
        # Only the status line may not mention a budget: the screen also shows
        # the work directory (".../smoke-budget-no-budget/...") whenever the
        # session socket path is over the 107-byte Linux limit, which happens
        # once the PID has five digits - the old whole-screen check then
        # failed only late in `make smoke-chat` (suga-yk7d).
        s.check("unconfigured status remains unchanged", s.until(STATUS, m) and not status_mentions_budget(s.text(m)))
        exit_chat(s, s.mark())
    finally:
        s.close(grace=0)
        stop_fake(fake)
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-budget")
    p.add_argument("--port", type=int, default=18805)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()
    binary = os.path.abspath(args.bin)
    groups = [
        ("turn and steer", budget_turn, args.port),
        ("session short-circuit", session_short_circuit, args.port + 1),
        ("estimate on", lambda b, w, p, k: estimation(b, w, p, k, False), args.port + 2),
        ("estimate off", lambda b, w, p, k: estimation(b, w, p, k, True), args.port + 3),
        ("no budget", no_budget, args.port + 4),
    ]
    results = []
    for name, scenario, port in groups:
        print("\n== " + name + " ==")
        results += [(name + ": " + check, ok) for check, ok in
                    scenario(binary, os.path.abspath(args.work + "-" + name.replace(" ", "-")), port, args.keep)]
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
