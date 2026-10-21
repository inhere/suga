#!/usr/bin/env python3
"""End-to-end smoke test of memory writes in `suga chat` (Linux/macOS).

Starts fakellm.py in memory mode, writes a temporary SUGA_HOME with
tools.approval: auto, memory.taint_tools: [shell_exec] (so a shell call taints
the session) and the files project backend, runs `suga chat` in a PTY and
checks: a clean session writes memory without a question and shows
"已记住：…"; after shell_exec tainted the session a write asks (scope, kind,
title, body shown), y writes, n refuses, a writes and stops asking for the
session; the entry is on disk under SUGA_HOME.

  make smoke-chat                        # runs this after chat_edit.py
  python3 -I scripts/ptysmoke/chat_memory.py --bin tmp/smoke/suga
"""
import argparse
import glob
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

QUESTION = r"写入记忆？\[y\]是 \[N\]否 \[a\]本会话不再问"
SAVED = r"已记住：Smoke rule（project）"


def scenario(s):
    def turn_end(start):
        return s.until(r"tokens · ", start)

    m = s.mark()
    s.check("banner", s.until(r"Suga chat · fake/m1", m))

    m = s.mark(); s.line("直接记一下")
    s.check("clean: written", s.until(SAVED, m) and turn_end(m))
    s.check("clean: no question", not s.contains(QUESTION, m))

    m = s.mark(); s.line("跑一下再记")
    s.check("tainted: shell ran", s.until(r"⎿ ok: SMOKE_OK", m))
    s.check("tainted: question", s.until(QUESTION, m))
    s.check("tainted: details", s.contains(r"会话读过外部内容", m) and s.contains(r"范围：project:", m)
            and s.contains(r"类型：feedback", m) and s.contains(r"标题：Smoke rule", m)
            and s.contains(r"正文：smoke memory body", m))
    s.line("y")
    s.check("y: written", s.until(SAVED, m) and turn_end(m))

    m = s.mark(); s.line("跑一下再记")
    s.check("n: question", s.until(QUESTION, m))
    s.line("n")
    s.check("n: refused", s.until(r"用户拒绝写入记忆", m) and turn_end(m))
    s.check("n: nothing remembered", not s.contains(SAVED, m))

    m = s.mark(); s.line("跑一下再记")
    s.check("a: question", s.until(QUESTION, m))
    s.line("a")
    s.check("a: written", s.until(SAVED, m) and turn_end(m))
    m = s.mark(); s.line("跑一下再记")
    s.check("a: written again", s.until(SAVED, m) and turn_end(m))
    s.check("a: no question again", not s.contains(QUESTION, m))

    m = s.mark(); time.sleep(0.5); s.send(CTRL_C)
    s.check("ctrl-c once: hint", s.until(r"再按一次 Ctrl\+C 退出", m))
    m = s.mark(); s.send(CTRL_C)
    s.check("ctrl-c twice: exit", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-memory")
    p.add_argument("--port", type=int, default=18799)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(args.work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra="  approval: auto\n", memory_extra="  taint_tools: [shell_exec]\n")
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "memory", "--delay", "0.3"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        scenario(s)
        s.close()
        # Four writes reached the files backend in SUGA_HOME (clean, y, a and
        # the one after a); the refused one did not.
        index = glob.glob(os.path.join(home, "projects", "*", "memory", "MEMORY.md"))
        text = open(index[0], encoding="utf-8").read() if index else ""
        s.check("disk: entries in SUGA_HOME", text.count("Smoke rule") == 4)
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
