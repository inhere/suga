#!/usr/bin/env python3
"""End-to-end smoke test of chat's local commands (!cmd / !!cmd) in a pseudo terminal.

Starts scripts/ptysmoke/fakellm.py (text mode) and `suga chat` in a PTY
(tools.allow_write: true, tools.approval: auto, SUGA_HOOKS off) and checks:

- `!echo hi` runs the command in the local shell, shows its output and asks the
  model nothing (fakellm logs one line per request: none arrives);
- a non-zero exit code is reported: `!exit 3` prints 本地命令退出码 3;
- `!!echo ctx` runs the same and its result travels to the model with the next
  message: the notice 本地命令结果会随下一条消息发给模型 is printed, and the
  request the following message makes carries "[本地命令] echo ctx ..." before
  the person's own text;
- `!sleep 30` + Ctrl-C ends the command only: the notice 已终止本地命令 appears,
  the prompt comes back and the chat still answers a message;
- `suga send` delivering "!echo x" is an ordinary message: chat shows it as
  `[来自 cli] !echo x`, the model answers it as a message and no command ran;
- a `--read-only` chat runs `!echo hi` too (the mode limits the model's tools
  only);
- the session trace carries one `local_command` event per command that ran
  (command, exit code, output bytes, whether the result was sent to the model).

Reverse validation (the named checks must fail):
  - drop the local command case of the command switch in internal/chat/chat.go
    (`case isLocal && state.local != nil`): every `!`/`!!` line reaches the model
    as a message instead -> "no request for !echo hi", "the command's output is
    shown", "the non-zero exit code is reported", "the result reaches the model"
    and the trace checks fail;
  - the keyboard-only guard (the `case injected:` of that switch) cannot be
    reverse-checked through `suga send`: a delivery reaches the loop already
    prefixed with "[来自 cli] ", so it never starts with "!" and the delivered
    checks pass with or without the guard (verified 2026-10-16). The guard
    protects injected text without that prefix (a Stop hook reply); its unit
    test in internal/chat/local_cmd_test.go covers it;
  - drop the read a local command holds (watchLocalCommand in
    internal/chat/local_cmd.go, so the terminal stays cooked): the Ctrl-C of the
    `!sleep 30` step reaches chat as SIGINT and ends it -> "Ctrl-C ends the
    command, not the chat" and the checks after it fail.

  python3 -I scripts/ptysmoke/chat_bang.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-bang.
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
from chat_interrupt import FakeLog  # noqa: E402

OUT_MARK = "hi"
CTX_MARK = "SMOKE_BANG_CTX"
SEND_MARK = "SMOKE_BANG_DELIVERED"
SEND_TEXT = "!echo " + SEND_MARK
REPLY = "完成"
NOTICE = "本地命令结果会随下一条消息发给模型"
STOPPED = "已终止本地命令"


def chat_env(home):
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    return env


def output_line(s, pattern, start):
    """Waits until pattern matches a whole line of the chat output: the command's
    own output, not the echo of the line that was typed (the line editor echoes
    that above the prompt). The line ending is allowed to carry the extra CR a
    read with output processing back on adds (the editor writes CR LF itself)."""
    return s.until(r"(?m)^" + pattern + r"\r*$", start)


def send_text(binary, env, session, text):
    out = subprocess.run([binary, "send", "--session", session, text], capture_output=True, text=True,
                         env=env, timeout=30)
    return out.returncode, out.stderr.strip()


def trace_events(binary, env, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def scenario(s, log, binary, env):
    """Every local command of the first chat; returns the session id."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))

    # 1. "!cmd": the command runs here, the model hears nothing of it.
    # Send only once the next prompt is up: a line typed ahead of the line editor
    # is lost (suga-uxmu).
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("!echo " + OUT_MARK)
    s.check("the command's output is shown", output_line(s, OUT_MARK, m))
    s.check("the prompt comes back", s.until(r"\n❯ ", m))
    log.drain(0.5)
    s.check("no request for !echo hi", log.entries == [])

    # 2. A non-zero exit code is reported.
    m = s.mark()
    s.line("!exit 3")
    s.check("the non-zero exit code is reported", s.until(r"本地命令退出码 3", m))

    # 3. "!!cmd": the command runs, and its result goes to the model with the
    # next message.
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("!!echo " + CTX_MARK)
    s.check("a !! command says the result is for the model", s.until(NOTICE, m))
    s.check("the !! command's output is shown", output_line(s, CTX_MARK, m))
    log.drain(0.5)
    s.check("the !! command asks the model nothing yet", log.entries == [])
    s.until(r"\n❯ ", m)
    log.entries = []
    m = s.mark()
    s.line("你好")
    seen = log.wait_prompt_request("你好")
    s.check("the next message carries the !! result",
            bool(seen) and any(("本地命令" in token and CTX_MARK in token) for token in seen))
    if not seen:
        print("  request seen tokens:", seen, flush=True)
    s.check("the model answers the message", s.until(REPLY, m) and s.until(r"tokens · ", m))

    # 4. Ctrl-C during a command ends the command, not the chat.
    s.until(r"\n❯ ", m)
    m = s.mark()
    s.line("!sleep 30")
    time.sleep(0.6)  # let the shell start, so the press ends a running command
    s.send(CTRL_C)
    s.check("Ctrl-C ends the command", s.until(STOPPED, m))
    s.check("Ctrl-C keeps chat alive", s.until(r"\n❯ ", m))
    m = s.mark()
    s.line("你好")
    s.check("chat still answers after the kill", s.until(REPLY, m) and s.until(r"tokens · ", m))

    # 5. A delivered "!cmd" is an ordinary message: chat shows it and the model
    # answers it (nothing runs).
    sid = re.search(r"session id: ((?:sess_|s_)\S+)", s.text(0))
    session = sid.group(1) if sid else ""
    s.check("session id", bool(session))
    if session:
        s.until(r"\n❯ ", m)
        m = s.mark()
        log.entries = []
        code, err = send_text(binary, env, session, SEND_TEXT)
        s.check("the delivered text reaches the chat", code == 0 and err == "delivered")
        s.check("the delivered text is shown as a message",
                s.until(r"\[来自 cli\] !echo " + SEND_MARK, m))
        s.check("the delivered text is answered, not run",
                s.until(REPLY, m) and s.until(r"tokens · ", m))
        # The fake model's log line can trail the reply on screen: poll for it
        # instead of draining once (a single 0.5s drain saw only the previous
        # request's line on Linux).
        received = False
        end = time.time() + 10
        while not received and time.time() < end:
            log.drain(0.3)
            received = any(str(entry.get("user") or "").endswith(SEND_TEXT) for entry in log.entries)
        s.check("the model received the delivered text", received)
        if not received:
            print("  requests seen:", [str(e.get("user") or "")[-80:] for e in log.entries], flush=True)
    return session


def check_trace(s, events):
    commands = [(e.get("attrs") or {}).get("command") for e in events if e.get("kind") == "local_command"]
    s.check("trace: one local_command event per command that ran",
            commands == ["echo " + OUT_MARK, "exit 3", "echo " + CTX_MARK, "sleep 30"])
    if commands != ["echo " + OUT_MARK, "exit 3", "echo " + CTX_MARK, "sleep 30"]:
        print("  local_command events:", commands, flush=True)
    events = [e for e in events if e.get("kind") == "local_command"]
    if not events:
        return

    def attrs(index):
        return events[index].get("attrs") or {}

    s.check("trace: a plain command records its exit code and bytes",
            attrs(0).get("exit_code") == 0 and attrs(0).get("bytes", 0) > 0 and "sent" not in attrs(0))
    s.check("trace: the failed command records exit 3", attrs(1).get("exit_code") == 3)
    s.check("trace: the !! command is marked as sent to the model", attrs(2).get("sent") is True)
    s.check("trace: the killed command is marked cancelled",
            attrs(3).get("cancelled") is True and attrs(3).get("exit_code") != 0)


def read_only_group(binary, work, port, fake_ro):
    """A --read-only chat runs a local command too: the mode limits the model's
    tools, not the person's own shell."""
    home, ws = os.path.join(work, "home-ro"), os.path.join(work, "ws-ro")
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, port + 1, tools_extra="  approval: auto\n")
    log = FakeLog(fake_ro)
    s = PtySession([binary, "chat", "--read-only"], env=chat_env(home), cwd=ws)
    m = s.mark()
    s.check("read-only chat: ❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("!echo " + OUT_MARK)
    s.check("read-only chat: the local command runs", output_line(s, OUT_MARK, m))
    log.drain(0.5)
    s.check("read-only chat: no model request", log.entries == [])
    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("read-only chat: exit", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-bang")
    p.add_argument("--port", type=int, default=18799)
    p.add_argument("--keep", action="store_true")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home, ws = os.path.join(work, "home"), os.path.join(work, "ws")
    home_ro = os.path.join(work, "home-ro")
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(home_ro, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    # approval: auto, so nothing but the local commands is under test (the
    # chat_approval.py scenarios cover the per-call questions).
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = chat_env(home)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--delay", "0.3", "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    fake_ro = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                                "--port", str(args.port + 1), "--mode", "text", "--delay", "0.3", "--reply", REPLY],
                               stdout=subprocess.PIPE, text=True)
    s = s_ro = None
    results = []
    try:
        wait_port(fake)
        wait_port(fake_ro)
        log = FakeLog(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s, log, binary, env)
        m = s.mark()
        time.sleep(0.3)
        s.send(CTRL_C)
        time.sleep(0.3)
        s.send(CTRL_C)
        s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))
        s.close()
        if session:
            check_trace(s, trace_events(binary, env, session))

        s_ro = read_only_group(binary, work, args.port, fake_ro)
        results = s.results + s_ro.results
    finally:
        for session_pty in (s, s_ro):
            if session_pty is not None:
                session_pty.close(grace=0)
        for proc in (fake, fake_ro):
            proc.terminate()
            proc.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
