#!/usr/bin/env python3
"""End-to-end smoke test for chat undo checkpoints (Linux/macOS).

This drives the real binary with fakellm's edit mode.  It covers the prompt-only
command boundary, completion, confirmation, edit/new/delete restoration,
conflict versus force, shell-only warnings, the deferred ``suga.undo`` model
notice, snapshot attributes in trace, terminal-text cleaning, removed added
roots, and a resumed session whose cwd changed.  A second isolated home checks
the no-snapshot path.

Reverse verification (run by the Linux supervisor before accepting the scene):
- move the snapshot BeforeCall after the write: the first disk-restore check
  fails;
- make confirmation accept an empty answer: the first undo cancellation check
  fails;
- bypass termtext cleaning: the raw-PTY control-byte check fails;
- remove the undo notice injection: the fake request log check fails.

  python3 -I scripts/ptysmoke/chat_undo.py --bin tmp/smoke/suga
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

CONTROL = "\x1b"


def turn_end(s, start):
    return s.until(r"tokens · ", start)


def approve(s, start, label):
    s.check(label + ": question", s.until(r"允许执行 (?:file_edit|file_write|file_remove|shell_exec)", start))
    s.line("y")
    return s.until(r"tokens · ", start)


def trace_events(binary, env, session, cwd):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return out, events


def scenario(s, ws, shared):
    m = s.mark()
    s.check("banner", s.until(r"Suga chat · fake/m1", m))
    s.check("prompt", s.until(r"❯ ", m))

    m = s.mark()
    s.send(":u\t")
    s.check("completion offers :undo", s.until(r":undo", m))
    s.send("\x15")

    m = s.mark()
    s.line("编辑")
    s.check("edit tool completed", approve(s, m, "edit") and s.contains(r"ok: edited ", m))
    s.check("edit changed on disk", open(os.path.join(ws, "target.txt"), encoding="utf-8").read()
            == "SMOKE_TARGET new\n")

    m = s.mark()
    s.line(":checkpoints")
    s.check("checkpoint list", s.until(r"快照点", m) and s.until(r"恢复 target\.txt", m))

    m = s.mark()
    s.line(":undo")
    s.check("undo plan", s.until(r"将回滚最近 1 个回合", m))
    s.check("undo confirmation", s.until(r"执行？\[y\] 回滚", m))
    s.send("\r")
    s.check("empty confirmation cancels", s.until(r"已取消", m))
    m = s.mark()
    s.line(":undo")
    s.check("undo plan repeats", s.until(r"将回滚最近 1 个回合", m))
    s.check("undo confirmation repeats", s.until(r"执行？\[y\] 回滚", m))
    s.line("y")
    # :undo is a local command: no model turn, so no turn status line follows.
    s.check("undo restores edit", s.until(r"已回滚 1 个文件", m) and s.until(r"❯ ", m))
    s.check("edit restored on disk", open(os.path.join(ws, "target.txt"), encoding="utf-8").read()
            == "SMOKE_TARGET old\n")

    # This is a real following turn.  The fake request log checks that the
    # injected suga.undo fact reached the model without becoming a new request.
    m = s.mark()
    s.line("收到回滚通知")
    s.check("next turn after undo", s.until(r"完成", m) and turn_end(s, m))

    shell = "printf '\\033[2JSMOKE_SHELL'"
    m = s.mark()
    s.line("shell")
    s.check("shell tool completed", approve(s, m, "shell") and s.contains(r"SMOKE_SHELL", m))
    m = s.mark()
    s.line(":checkpoints")
    s.check("shell is shown as outside undo", s.until(r"shell_exec：.*改动不在快照范围内", m))
    # The line editor itself writes escapes; what must not reach the terminal is
    # the command's own clear-screen sequence (cleanText escapes it).
    s.check("checkpoint terminal text is clean", CONTROL + "[2J" not in s.text_raw(m))

    m = s.mark()
    s.line("新建")
    s.check("new file write", approve(s, m, "new file") and s.until(r"wrote \d+ bytes", m))
    new_path = os.path.join(ws, "new.txt")
    s.check("new file exists", os.path.exists(new_path))
    m = s.mark()
    s.line(":undo")
    # :undo 1 covers only the newest turn (the new file); the shell-only turn
    # before it is outside that range, so its boundary is not repeated here.
    s.check("new-file undo plan deletes the new file", s.until(r"删除 new\.txt", m))
    s.check("new-file undo confirmation", s.until(r"执行？\[y\] 回滚", m))
    s.line("y")
    s.check("new file deleted by undo", s.until(r"删除 1 个新建文件", m) and not os.path.exists(new_path))

    m = s.mark()
    s.line("删除")
    s.check("remove tool completed", approve(s, m, "remove") and s.until(r"removed", m))
    delete_path = os.path.join(ws, "delete.txt")
    s.check("deleted file is absent", not os.path.exists(delete_path))
    m = s.mark()
    s.line(":undo")
    s.check("trash undo plan", s.until(r"将回滚最近 1 个回合", m))
    s.check("trash undo confirmation", s.until(r"执行？\[y\] 回滚", m))
    s.line("y")
    s.check("trash undo restores file", s.until(r"已回滚 1 个文件", m)
            and os.path.exists(delete_path))

    # Conflict is deliberately introduced after a successful edit.  The same
    # checkpoint is then retried with force, proving force is scoped to content.
    m = s.mark()
    s.line("冲突")
    s.check("conflict edit", s.until(r"允许执行 file_edit", m))
    s.line("y")
    s.check("conflict edit completed", s.until(r"ok: edited ", m) and turn_end(s, m))
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write("MANUAL_CONFLICT\n")
    m = s.mark()
    s.line(":undo")
    s.check("conflict listed", s.until(r"跳过 target\.txt.*冲突", m))
    s.check("conflict undo confirmation", s.until(r"执行？\[y\] 回滚", m))
    s.line("y")
    s.check("conflict skipped", s.until(r"跳过 1 个冲突", m))
    s.check("manual conflict remains", open(os.path.join(ws, "target.txt"), encoding="utf-8").read()
            == "MANUAL_CONFLICT\n")
    m = s.mark()
    s.line(":undo force")
    s.check("force plan", s.until(r"涵盖内容冲突", m))
    s.check("force undo confirmation", s.until(r"执行？\[y\] 回滚", m))
    s.line("y")
    s.check("force restores content conflict", s.until(r"已回滚 1 个文件", m))
    s.check("force restored on disk", open(os.path.join(ws, "target.txt"), encoding="utf-8").read()
            == "SMOKE_TARGET old\n")

    m = s.mark()
    s.line("附加")
    s.check("added-dir write", approve(s, m, "added dir") and s.until(r"wrote \d+ bytes", m))
    s.check("added-dir file exists", os.path.exists(os.path.join(shared, "added.txt")))
    m = s.mark()
    s.line(":rm-dir " + shared)
    s.check("added-dir removal", s.until(r"已撤销", m))
    m = s.mark()
    s.line(":checkpoints")
    s.check("removed added root is explained", s.until(r"附加目录已移除，无法恢复", m))

    m = s.mark()
    s.send(CTRL_C)
    s.until(r"再按一次 Ctrl\+C 退出", m)
    s.send(CTRL_C)
    s.until(r"--resume sess_\S+", m, timeout=5)
    found = re.search(r"--resume (sess_\S+)", s.text(m))
    s.check("exit reports session", bool(found))
    return found.group(1) if found else ""


def no_snapshot_scenario(binary, work, port):
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write("SMOKE_TARGET old\n")
    write_home(home, port, tools_extra="  snapshots:\n    enabled: false\n")
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "edit", "--delay", "0.1"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([os.path.abspath(binary), "chat"], env=env, cwd=ws)
        m = s.mark()
        s.check("no-snapshot prompt", s.until(r"❯ ", m))
        m = s.mark()
        s.line("编辑")
        s.check("no-snapshot edit still runs", s.until(r"允许执行 file_edit", m))
        s.line("y")
        s.check("no-snapshot write completes", s.until(r"ok: edited ", m) and turn_end(s, m))
        m = s.mark()
        s.line(":checkpoints")
        s.check("no-snapshot reports no checkpoint", s.until(r"没有可回滚的快照", m))
        m = s.mark()
        s.line(":undo")
        s.check("no-snapshot undo is also empty", s.until(r"没有可回滚的快照", m))
        s.send(CTRL_C)
        s.until(r"再按一次 Ctrl\+C 退出", m)
        s.send(CTRL_C)
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-undo")
    p.add_argument("--port", type=int, default=18843)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    ws2 = os.path.join(work, "ws-cwd")
    shared = os.path.join(work, "shared")
    for path in (home, ws, ws2, shared):
        shutil.rmtree(path, ignore_errors=True)
        os.makedirs(path, exist_ok=True)
    isolate_ws(ws)
    isolate_ws(ws2)
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write("SMOKE_TARGET old\n")
    with open(os.path.join(ws, "delete.txt"), "w", encoding="utf-8") as f:
        f.write("SMOKE_DELETE\n")
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    here = os.path.dirname(os.path.abspath(__file__))
    shell = "printf '\\033[2JSMOKE_SHELL'"
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "edit", "--delay", "0.15",
                             "--write-path", os.path.join(shared, "added.txt"),
                             "--write-body", "SMOKE_ADDED", "--remove-path", "delete.txt",
                             "--shell-command", shell], stdout=subprocess.PIPE, text=True)
    sessions = []
    session_id = ""
    fake_output = ""
    try:
        wait_port(fake)
        first = PtySession([binary, "chat", "--add-dir", shared + ":rw"], env=env, cwd=ws)
        sessions.append(first)
        session_id = scenario(first, ws, shared)
        first.close()
        first.check("session id", bool(session_id))

        if session_id:
            resumed = PtySession([binary, "chat", "--resume", session_id], env=env, cwd=ws2)
            sessions.append(resumed)
            m = resumed.mark()
            resumed.check("cwd-change question", resumed.until(r"当前工作目录是", m))
            resumed.line("2")
            resumed.check("cwd-change chat opens", resumed.until(r"❯ ", m))
            m = resumed.mark()
            resumed.line(":checkpoints")
            resumed.check("cwd-change checkpoint is explained",
                          resumed.until(r"会话目录已变更，无法恢复", m))
            resumed.send(CTRL_C)
            resumed.until(r"再按一次 Ctrl\+C 退出", m)
            resumed.send(CTRL_C)
            resumed.close()

            fake.terminate()
            fake_output = fake.communicate(timeout=5)[0]
            # fakellm logs each request as JSON with ASCII escapes: match both forms.
            notice = "用户回滚了文件改动"
            first.check("next turn received suga.undo", notice in fake_output
                        or json.dumps(notice)[1:-1] in fake_output)
            out, events = trace_events(binary, env, session_id, ws2)
            first.check("trace command succeeds", out.returncode == 0)
            undo = [e for e in events if e.get("kind") == "undo"]
            first.check("trace has EventUndo", bool(undo))
            if undo:
                first.check("EventUndo has restored/skipped attrs",
                            (undo[0].get("attrs") or {}).get("restored") is not None
                            and (undo[0].get("attrs") or {}).get("skipped") is not None)
            tools = [e for e in events if e.get("kind") == "tool"]
            snapshots = [(e.get("name"), (e.get("attrs") or {}).get("snapshot")) for e in tools]
            first.check("tool events have snapshot attrs",
                        any(name in ("file_edit", "file_write", "file_remove") and isinstance(attr, dict)
                            and attr.get("status") for name, attr in snapshots))
    finally:
        for s in sessions:
            s.close(grace=0)
        if fake.poll() is None:
            fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)

    no_snap = no_snapshot_scenario(binary, os.path.join(work, "no-snapshot"), args.port + 1)
    if no_snap is not None:
        sessions.append(no_snap)
    if not args.keep:
        shutil.rmtree(work, ignore_errors=True)
    failed = [name for s in sessions for name in s.failed()]
    total = sum(len(s.results) for s in sessions)
    print(f"\n{total - len(failed)}/{total} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
