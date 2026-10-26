#!/usr/bin/env python3
"""End-to-end smoke test of file_grep -> file_edit in `suga chat` (Linux/macOS).

Starts fakellm.py in edit mode, writes a temporary SUGA_HOME with
tools.allow_write: true and a workspace with target.txt, runs `suga chat` in
a PTY and checks: file_grep runs without a question and finds the line, the
file_edit question previews old and new text, y applies the edit on disk, and
two Ctrl-C exit.

  make smoke-chat                        # runs this after chat_approval.py
  python3 -I scripts/ptysmoke/chat_edit.py --bin tmp/smoke/suga
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


def scenario(s, ws):
    m = s.mark()
    s.check("banner", s.until(r"Suga chat · fake/m1", m))
    m = s.mark(); s.line("把 old 改成 new")
    s.check("grep: ran without a question", s.until(r"target.txt:2: SMOKE_TARGET old", m)
            and not s.contains(r"允许执行 file_grep", m))
    s.check("edit: question", s.until(r"允许执行 file_edit", m))
    s.check("edit: old text shown", s.contains(r"原文：\s+SMOKE_TARGET old", m))
    s.check("edit: new text shown", s.contains(r"改为：\s+SMOKE_TARGET new", m))
    s.line("y")
    # The result line is cut to the terminal width after the absolute path, so
    # match its start; the disk check below proves the replacement.
    s.check("edit: applied", s.until(r"ok: edited ", m) and s.until(r"完成", m))
    time.sleep(0.3)
    with open(os.path.join(ws, "target.txt"), encoding="utf-8") as f:
        s.check("edit: file changed on disk", f.read() == "line one\nSMOKE_TARGET new\nline three\n")
    m = s.mark(); time.sleep(0.5); s.send(CTRL_C)
    s.check("ctrl-c once: hint", s.until(r"再按一次 Ctrl\+C 退出", m))
    m = s.mark(); s.send(CTRL_C)
    s.check("ctrl-c twice: exit", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    match = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return match.group(1) if match else ""


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-edit")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write("line one\nSMOKE_TARGET old\nline three\n")
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "edit", "--delay", "0.3"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s, ws)
        s.close()
        s.check("session id", bool(session))
        if session:
            trace = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True,
                                   text=True, env=env, cwd=ws, timeout=60)
            events = []
            for line in trace.stdout.splitlines():
                try:
                    events.append(json.loads(line))
                except ValueError:
                    pass
            tool = next((e for e in events if e.get("kind") == "tool" and e.get("name") == "file_edit"), {})
            snapshot = (tool.get("attrs") or {}).get("snapshot")
            s.check("trace: file_edit has complete snapshot attrs",
                    isinstance(snapshot, dict) and snapshot.get("status") == "complete")
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
