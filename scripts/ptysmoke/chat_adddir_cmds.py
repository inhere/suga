#!/usr/bin/env python3
"""End-to-end smoke test of the mid-session directory authorization in chat (Linux/macOS).

Two chats run here, each with its own isolated SUGA_HOME:

1. the `adddir` fake model and `suga chat` started WITHOUT --add-dir
   (tools.allow_write: true, tools.approval: auto):
   - a prompt whose step only reads a file outside the workspace makes chat ask
     `授权访问 <目录>？[r] 只读 [w] 读写 [n] 拒绝`; answering `r` authorizes the
     directory and the read runs in that same turn (the file's mark appears);
   - `:dirs` lists the workspace and that directory as 只读 · 越界授权;
   - `:add-dir <shared>:rw` answers 已授权 …（读写）, and the next prompt's
     file_write into that directory succeeds;
   - `:dirs` lists both, the :rw one as 读写 · 会话中追加;
   - `:rm-dir <shared>` answers 已撤销 …, the next write is refused again
     (the tool result says the path is outside the added read-write
     directories) and no file appears;
   - `:add-dir docs` (relative) only prints the error and the usage;
   - the session trace carries one `add_dir` event per change (path, mode, from:
     ask for the question, keyboard for the commands) and the turn_context of
     each turn names the directories in effect then.

2. the `text` fake model and a second `suga chat`: `suga send --session <id>
   ":add-dir <dir>"` delivers that text as an ordinary message (chat shows it
   as `[来自 cli] …` and the model answers it), and `:dirs` afterwards lists no
   such directory: delivered text can never change what the session may reach.

Reverse validation (the named checks must fail):
  - without the outside question (set `chat.ask_outside_dirs: false` in the
    smoke config, or drop the `askOutsideDir` call in
    internal/wiring/chat_approver.go): the question never appears and the read
    stays refused -> "outside read: question asked" and "outside read: r
    authorizes it and the read runs" fail (the same two subtests of
    internal/wiring/chat_approver_outside_test.go fail);
  - without the delivery protection (both the `[来自 …] ` label `deliveryLine`
    adds in internal/chat/chat_input.go AND the keyboard-only guard, the
    `case injected:` of the command switch in internal/chat/chat.go, which is
    what stops an injected line): the delivered `:add-dir` is executed instead of
    being answered -> "the delivered text is shown as a message", "the model
    answers the delivered text" and ":dirs has no delivered directory" fail.
    Removing the guard alone leaves the labelled delivery a message, so its own
    check is internal/chat/chat_dirs_test.go TestChatHookReplyIsNeverCommand
    (an injected line with no label, a Stop hook's block reason: with the guard
    gone it runs as a command and that test fails);
  - without the commands (drop the `isDirCommand` case in internal/chat/chat.go):
    every :add-dir/:dirs/:rm-dir check fails.

  python3 -I scripts/ptysmoke/chat_adddir_cmds.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-adddir-cmds.
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

READ_MARK = "SMOKE_CMD_READ"
WRITE_MARK = "SMOKE_CMD_WRITE"
REPLY = "读完了"
REPLY_TEXT = "收到"


def start_fake(here, port, mode, **options):
    """Starts fakellm.py in mode and waits for its listening line."""
    args = [sys.executable, "-I", os.path.join(here, "fakellm.py"),
            "--port", str(port), "--mode", mode, "--delay", "0.3"]
    for key, value in options.items():
        args += ["--" + key.replace("_", "-"), str(value)]
    proc = subprocess.Popen(args, stdout=subprocess.PIPE, text=True)
    wait_port(proc)
    return proc


def chat_env(home, extra=None):
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    env.update(extra or {})
    return env


def ask_scenario(s, outside_real, shared, write_path):
    """The question, the commands and what they change (first chat)."""
    m = s.mark()
    s.check("banner omits the rare directory commands", not s.contains(":add-dir/:dirs/:rm-dir", m))
    s.check("❯ prompt", s.until(r"❯ ", m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":dirs")
    s.check(":dirs starts with the workspace only",
            s.until(r"工作区：.+（读写）", m) and s.until(r"附加目录：无", m))

    # A read of a file outside the workspace: chat asks before the tool runs.
    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("只读：看看外面的说明")
    # The question is longer than the line editor's 80 columns and wraps, so
    # it is matched with the wrap's line breaks taken out.
    s.check("outside read: question asked",
            s.until(r"\[n\] 拒绝：", m)
            and ("授权访问 " + outside_real + "？[r] 只读 [w] 读写 [n] 拒绝：") in unwrap(s.text(m)))
    s.check("outside read: the tool is held back until the answer", not s.contains(READ_MARK, m))
    s.line("r")
    s.check("outside read: r authorizes it and the read runs", s.until(READ_MARK, m))
    s.check("outside read: turn ends", s.until(r"tokens · ", m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":dirs")
    s.check(":dirs names the asked directory as 越界授权",
            s.until(r"  " + re.escape(outside_real) + r"（只读 · 越界授权）", m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":add-dir " + shared + ":rw")
    s.check(":add-dir rw confirms", s.until(r"已授权：" + re.escape(shared) + r"（读写）", m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("只写：写个文件")
    s.check("the write into the added directory runs", s.until(r"wrote \d+ bytes to ", m))
    s.check("write: turn ends", s.until(r"tokens · ", m))
    s.check("write: file on disk", os.path.exists(write_path)
            and open(write_path, encoding="utf-8").read() == WRITE_MARK)

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":dirs")
    s.check(":dirs shows both directories with mode and origin",
            s.until(r"  " + re.escape(outside_real) + r"（只读 · 越界授权）", m)
            and s.until(r"  " + re.escape(shared) + r"（读写 · 会话中追加）", m))
    s.check(":dirs labels the workspace too", s.contains(r"工作区：.+（读写）", m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":rm-dir " + shared)
    s.check(":rm-dir revokes it", s.until(r"已撤销：" + re.escape(shared), m))

    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line(":add-dir docs")
    s.check("a relative :add-dir is refused with the usage",
            s.until(r":add-dir: .*is not an absolute path", m) and s.until(r"用法：:add-dir", m))

    # The revoked directory is out of reach again: the write is refused and no
    # file appears (the one of the earlier turn was removed here).
    if os.path.exists(write_path):
        os.remove(write_path)
    # Send only once the next prompt is up: a line typed ahead of the line
    # editor is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("只写：再写一次")
    # Out of reach again, so chat asks again (chat.ask_outside_dirs is on);
    # answering n leaves the write refused.
    s.check("the write after :rm-dir asks again",
            s.until(r"\[n\] 拒绝：", m)
            and ("授权访问 " + shared + "？") in unwrap(s.text(m)))
    s.line("n")
    s.check("the write after :rm-dir is refused", s.until(r"用户拒绝授权访问 ", m))
    s.check("the write after :rm-dir wrote nothing", not os.path.exists(write_path))
    s.check("turn ends after the refusal", s.until(r"tokens · ", m))

    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def unwrap(text):
    """The output with the terminal's line wraps removed: CR LF, or CR CR LF while a
    read keeps output processing on - a running turn's read (chat.steering), whose
    line editor's own CR LF the terminal turns into CR CR LF."""
    return re.sub(r"\r*\n", "", text)


def check_trace(s, events, outside_real, shared):
    """The trace of the first chat: one add_dir event per change, the reach of
    every turn in turn_context."""
    changes = []
    for event in events:
        if event.get("kind") != "add_dir":
            continue
        attrs = event.get("attrs") or {}
        changes.append((event.get("name"), attrs.get("path"), attrs.get("mode"), attrs.get("from")))
    s.check("trace: one add_dir event per authorization change", changes == [
        ("add", outside_real, "ro", "ask"),
        ("add", shared, "rw", "keyboard"),
        ("remove", shared, "rw", "keyboard"),
    ])
    if changes != [("add", outside_real, "ro", "ask"), ("add", shared, "rw", "keyboard"), ("remove", shared, "rw", "keyboard")]:
        print("  add_dir events:", changes, flush=True)

    reach = [(event.get("attrs") or {}).get("add_dirs")
             for event in events if event.get("kind") == "turn_context"]
    s.check("trace: a turn sees the added directory", [outside_real + ":ro", shared + ":rw"] in reach)
    s.check("trace: the last turn sees the revoked one gone",
            reach and reach[-1] == [outside_real + ":ro"])
    if reach[-1:] != [[outside_real + ":ro"]]:
        print("  turn_context add_dirs:", reach, flush=True)


def trace_events(binary, env, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def send(binary, env, session, text):
    out = subprocess.run([binary, "send", "--session", session, text], capture_output=True, text=True,
                         env=env, timeout=30)
    return out.returncode, out.stderr.strip()


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-adddir-cmds")
    p.add_argument("--port", type=int, default=18797)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    home2 = os.path.join(work, "home2")
    ws = os.path.join(work, "ws")
    outside = os.path.join(work, "outside")   # never authorized by a flag
    shared = os.path.join(work, "shared")     # authorized by :add-dir later
    delivery = os.path.join(work, "delivery") # only named by a delivered message
    read_path = os.path.join(outside, "note.txt")
    write_path = os.path.join(shared, "out.txt")
    for path in (home, home2, ws, outside, shared, delivery):
        shutil.rmtree(path, ignore_errors=True)
        os.makedirs(path, exist_ok=True)
    isolate_ws(ws)
    with open(read_path, "w", encoding="utf-8") as f:
        f.write(READ_MARK + "\n")
    # approval: auto, so only the outside-directory question and the commands are
    # under test (chat_approval.py covers the per-call approvals).
    write_home(home, args.port, tools_extra="  approval: auto\n")
    write_home(home2, args.port + 1, tools_extra="  approval: auto\n")
    env = chat_env(home)
    env2 = chat_env(home2)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = start_fake(here, args.port, "adddir", read_path=read_path, write_path=write_path,
                      write_body=WRITE_MARK, reply=REPLY)
    fake2 = start_fake(here, args.port + 1, "text", reply=REPLY_TEXT)
    s = s2 = None
    try:
        # 1. the question, the commands and what they change
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        # The question names the path the tool really resolves (a symlinked work
        # dir, e.g. /tmp on macOS, is authorized in its real form).
        session = ask_scenario(s, os.path.realpath(outside), shared, write_path)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session), os.path.realpath(outside), shared)

        # 2. delivered text is never a command
        s2 = PtySession([binary, "chat"], env=env2, cwd=ws)
        m = s2.mark()
        s2.check("second chat: ❯ prompt", s2.until(r"❯ ", m))
        s2.line("你好")
        s2.check("second chat: the model answers", s2.until(REPLY_TEXT, m) and s2.until(r"tokens · ", m))
        sid = re.search(r"session id: ((?:sess_|sg_|s_)\S+)", s2.text(0))
        s2.check("second chat: session id", bool(sid))
        if sid:
            s2.until(r"\n❯ ", m)
            m = s2.mark()
            code, err = send(binary, env2, sid.group(1), ":add-dir " + delivery)
            s2.check("the delivered :add-dir reaches the chat", code == 0 and err == "delivered")
            s2.check("the delivered text is shown as a message",
                     s2.until(r"\[来自 cli\] :add-dir " + re.escape(delivery), m))
            s2.check("the model answers the delivered text", s2.until(REPLY_TEXT, m) and s2.until(r"tokens · ", m))
            s2.until(r"\n❯ ", m)
            m2 = s2.mark()
            s2.line(":dirs")
            s2.check(":dirs has no delivered directory",
                     s2.until(r"附加目录：无", m2) and not s2.contains(re.escape(delivery), m2))
            s2.check("no authorization came from the delivery", not s2.contains(r"已授权", m))
        m = s2.mark()
        time.sleep(0.3)
        s2.send(CTRL_C)
        time.sleep(0.3)
        s2.send(CTRL_C)
        s2.check("second chat: exit", s2.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    finally:
        for session_pty in (s, s2):
            if session_pty is not None:
                session_pty.close(grace=0)
        for proc in (fake, fake2):
            proc.terminate()
            proc.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    results = (s.results if s else []) + (s2.results if s2 else [])
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
