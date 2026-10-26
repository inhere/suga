#!/usr/bin/env python3
"""PTY smoke for persisted chat input history and completion.

Reverse validation: remove the history SetHistory call, the second chat's Up and
Ctrl-R checks fail; remove installCompletion, command and path Tab checks fail.
This is intentionally Linux/macOS-only: Windows console input has no Python PTY.
"""
import argparse, os, shutil, subprocess, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession, resolve_port
from chat_approval import isolate_ws, wait_port, write_home


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-history")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true")
    a = p.parse_args(); a.port = resolve_port(a.port)
    work = os.path.abspath(a.work); home = os.path.join(work, "home"); ws = os.path.join(work, "ws")
    shutil.rmtree(work, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
    os.makedirs(os.path.join(ws, "tm-dir"))
    write_home(home, a.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(os.path.dirname(__file__), "fakellm.py"), "--port", str(a.port), "--mode", "text"], stdout=subprocess.PIPE, text=True)
    sessions = []
    try:
        wait_port(fake)
        for n in range(2):
            # A wide window: the completed absolute path must not wrap (the check reads
            # the raw output, where a wrapped line is broken by the editor redraw).
            s = PtySession([os.path.abspath(a.bin), "chat"], env=env, cwd=ws, size=(240, 50)); sessions.append(s)
            m = s.mark(); s.check("prompt", s.until(r"❯ ", m))
            if n == 0:
                s.line("first history line"); s.until(r"❯ ", s.mark())
                s.line("second history line"); s.until(r"❯ ", s.mark())
                s.send(CTRL_C); s.until(r"再按一次", s.mark()); s.send(CTRL_C); s.close(); continue
            s.send("\x1b[A"); s.check("Up restores prior local input", s.until("second history line", m))
            s.send("\x15"); s.send("\x12"); s.send("second")
            s.check("Ctrl-R finds a match", s.until("reverse-i-search", m) and s.contains("second history line", m))
            s.send("\x07")
            s.send(":se\t")
            s.check("command Tab completion (:session)", s.until(":session", m))
            s.send("\x15:add-dir " + os.path.join(ws, "tm") + "\t")
            s.check("directory Tab completion", s.until(":add-dir " + os.path.join(ws, "tm-dir") + os.sep, m))
            s.send("\x03"); s.send("\x03")
            s = None
    finally:
        for s in sessions:
            if s is not None: s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5)
        if not a.keep: shutil.rmtree(work, ignore_errors=True)
    failed = [name for name, ok in sessions[1].results if not ok] if len(sessions) > 1 else []
    print(f"{len(sessions[1].results)-len(failed)}/{len(sessions[1].results)} passed")
    if failed: print("failed: " + ", ".join(failed)); raise SystemExit(1)

if __name__ == "__main__": main()
