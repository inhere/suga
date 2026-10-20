#!/usr/bin/env python3
"""End-to-end smoke test of --add-dir in `suga chat` (Linux/macOS).

Starts fakellm.py (adddir mode) and `suga chat --add-dir <outside> --add-dir
<shared>:rw` in a PTY: the model reads a file that lives outside the workspace
through file_read, writes a file into the :rw directory through file_write
(tools.approval: auto, so no question), exits with two Ctrl-C, then checks
`suga inspect trace --json` of that session: the turn_context event lists both
directories as path:ro and path:rw.

  python3 -I scripts/ptysmoke/chat_adddir.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-adddir.
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

READ_MARK = "SMOKE_ADD_DIR_READ"
WRITE_MARK = "SMOKE_ADD_DIR_WRITE"


def scenario(s):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("读一下附加目录，然后写个文件")
    # file_read of a path outside the workspace: it only works through --add-dir.
    s.check("file_read reaches the added directory", s.until(READ_MARK, m))
    s.check("file_write reaches the :rw directory", s.until(r"wrote \d+ bytes to ", m))
    s.check("turn end", s.until(r"tokens · ", m))
    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))
    found = re.search(r"--resume ((?:sess_|s_)\S+)", s.text(m))
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


def check_trace(s, events, outside, shared):
    turns = [e for e in events if e.get("kind") == "turn_context" or e.get("name") == "turn_context"]
    s.check("trace: turn_context recorded", bool(turns))
    if not turns:
        return
    dirs = (turns[0].get("attrs") or {}).get("add_dirs")
    ok = s.check("trace: add_dirs lists both directories with their modes",
                 dirs == [outside + ":ro", shared + ":rw"])
    if not ok:
        print("  add_dirs:", repr(dirs), flush=True)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-adddir")
    p.add_argument("--port", type=int, default=18797)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    outside = os.path.join(work, "outside")  # read-only added directory
    shared = os.path.join(work, "shared")    # read-write added directory
    read_path = os.path.join(outside, "note.txt")
    write_path = os.path.join(shared, "out.txt")
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    shutil.rmtree(outside, ignore_errors=True)
    shutil.rmtree(shared, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    os.makedirs(outside, exist_ok=True)
    os.makedirs(shared, exist_ok=True)
    isolate_ws(ws)
    with open(read_path, "w", encoding="utf-8") as f:
        f.write(READ_MARK + "\n")
    # approval: auto, so the file_write runs without a question (chat_approval.py
    # covers the question itself).
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "adddir", "--delay", "0.3",
                             "--read-path", read_path, "--write-path", write_path,
                             "--write-body", WRITE_MARK, "--reply", "读完了"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat", "--add-dir", outside, "--add-dir", shared + ":rw"], env=env, cwd=ws)
        session = scenario(s)
        s.close()
        s.check("file_write: file on disk", os.path.exists(write_path)
                and open(write_path, encoding="utf-8").read() == WRITE_MARK)
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session), outside, shared)
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
