#!/usr/bin/env python3
"""End-to-end smoke test of the chat idle consolidation (Linux/macOS).

Starts fakellm.py in text mode (each response delayed longer than the idle
time, reporting a prompt size above half the context threshold), writes a
temporary SUGA_HOME with context.idle_after: 2s and a small context_window,
runs `suga chat` in a PTY and checks: a turn longer than idle_after does not
trigger it; a few seconds after the turn "✦ 空闲整理中…" then "✦ 已整理上下文：X
→ Y token" appear; input afterwards works; typing while a consolidation runs
preempts it (no success line, the turn answers); `suga inspect trace` shows the
schedule events and the idle segment.

  make smoke-chat                        # runs this after chat_memory.py
  python3 -I scripts/ptysmoke/chat_idle.py --bin tmp/smoke/suga
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

RUNNING = r"✦ 空闲整理中…"
DONE = r"✦ 已整理上下文：\d+ → \d+ token"


def scenario(s):
    def turn_end(start):
        return s.until(r"tokens · ", start)

    m = s.mark()
    s.check("banner", s.until(r"Suga chat · fake/m1", m))

    # Each response takes 2.5s, longer than idle_after (2s): the turn pauses it.
    m = s.mark(); s.line("你好")
    s.check("turn 1 ends", s.until(r"完成", m) and turn_end(m))
    s.check("turn 1: not during the turn", not s.contains(r"空闲整理", m))

    m = s.mark()
    s.check("idle: starts after the turn", s.until(RUNNING, m, timeout=10))
    s.check("idle: consolidated", s.until(DONE, m, timeout=15))

    m = s.mark(); s.line("再来")
    s.check("input after idle works", s.until(r"完成", m) and turn_end(m))

    # Next idle run: type while it waits for the summary (2.5s) -> preempted.
    m = s.mark()
    s.check("idle again: starts", s.until(RUNNING, m, timeout=10))
    m = s.mark(); s.line("第三轮")
    s.check("preempt: turn answers", s.until(r"完成", m) and turn_end(m))
    s.check("preempt: no success line", not s.contains(r"已整理上下文", m))

    # Wait for the prompt: input typed before it stays in the line unsubmitted.
    time.sleep(0.5)
    m = s.mark(); s.line(":quit")
    s.check("quit", s.until(r"续接：suga chat --resume sess_", m))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-idle")
    p.add_argument("--port", type=int, default=18801)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(args.work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    # Window 16000 -> threshold 12800; the fake reports 8000 prompt tokens (0.62).
    write_home(home, args.port, provider_extra="    models:\n      m1:\n        context_window: 16000\n",
               extra="context:\n  idle_after: 2s\n  idle_min_ratio: 0.5\n")
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"), "--port", str(args.port),
                             "--mode", "text", "--delay", "2.5", "--prompt-tokens", "8000"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        scenario(s)
        sid = re.search(r"session id: (sess_\S+)", s.text())
        s.close()
        trace = ""
        if sid:
            trace = subprocess.run([binary, "inspect", "trace", sid.group(1)], env=env, cwd=ws, capture_output=True,
                                   text=True, timeout=30).stdout
        s.check("trace: schedule fired", re.search(r"schedule idle-consolidate fired", trace) is not None)
        s.check("trace: preempted run cancelled", re.search(r"schedule idle-consolidate cancelled", trace) is not None)
        s.check("trace: idle segment", re.search(r"compaction compact ok .*reason=idle", trace) is not None)
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(args.work, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
