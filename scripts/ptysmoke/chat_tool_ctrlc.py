#!/usr/bin/env python3
"""Ctrl-C / Esc while a foreground tool call is running (Linux/macOS).

What Ctrl-C / Esc do during a turn depends on chat.steering, so this smoke runs both
groups in one go (design D2 / plan F1):

  steering on (the default): the turn keeps the input line open, so Ctrl-C arrives
  as the byte 0x03 and a lone Esc as its own sequence. A single press ends this turn
  only ("已中断本轮", chat stays at the prompt, the tool is cut short); a second
  press within three seconds leaves chat (summary and resume line), the way two
  presses at the prompt do.
  steering off: the console stays cooked, Ctrl-C raises SIGINT, the turn is
  interrupted and chat exits at once, as it did before steering.

The chat runs in a PTY; the fake model (tool mode) calls shell_exec with a command
that writes a marker, sleeps and writes a second marker; once the tool is running
the script sends Ctrl-C (or Esc) and checks the shell tool was cut short (the tool's
second marker never appears, so the whole process tree died with the turn).

  python3 -I scripts/ptysmoke/chat_tool_ctrlc.py --bin tmp/smoke/suga

Reverse verification (make a check fail on purpose), checked 2026-10-15:
- drop the turn's own context (run with the chat ctx instead of a derived one) and
  the "steering on" group fails: a single Ctrl-C no longer only ends the turn.
- make SetBusy(true) keep the tty cooked while steering is on (drop the steering
  branch of SetBusy) and the same group fails: Ctrl-C becomes SIGINT again, so a
  single press still ends chat and "chat stays after one press" fails.
- drop the steer read of a turn (startTurnInput) and the same group fails: nothing
  reads the keyboard, so neither Ctrl-C nor Esc ends the turn.
In a PTY, Ctrl-C sends SIGINT to the whole foreground process group, so dropping
shell_exec's cmd.Cancel does not make a check fail here: the tool's shell dies
either way (with steering on it is the turn's context that kills it, see
chat_steer.py). The marker check therefore guards the process-group behaviour,
not suga's own cancellation.

Dropping syscall.SIGINT from signal.NotifyContext (cmd/suga/main.go) still makes
the "steering off" group fail: Ctrl-C then kills suga without its interrupt path.
"""
import argparse
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

# The tool writes this marker before it starts sleeping: the script waits for the
# file, not for output (a tool's output only reaches chat when the tool ends).
STARTED = "tool-started"
FINISHED = "tool-finished"


def scenario(s, ws, steering):
    """Both groups: steering on reads Escape and Ctrl-C as keys (this turn only),
    steering off takes Ctrl-C as SIGINT (the turn and chat end)."""
    started = os.path.join(ws, STARTED)
    finished = os.path.join(ws, FINISHED)

    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("❯ prompt", s.until(r"❯ ", m))

    m = s.mark()
    s.line("跑一个长命令")
    s.check("tool call shown", s.until(r"shell_exec", m))
    if not wait_file(started, 30):
        s.check("tool started (marker file)", False)
        return
    s.check("tool started (marker file)", True)

    # The tool is running and chat is in its turn: Ctrl-C must end it.
    time.sleep(0.5)
    s.send(CTRL_C)
    if not steering:
        # Cooked console: the turn is interrupted and chat itself ends with its
        # summary right away (the tool process tree dies with it).
        s.check("ctrl-c: turn interrupted", s.until(r"已中断", m, timeout=15))
        s.check("ctrl-c: chat exits with the summary", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
        s.check("ctrl-c: chat process ended", not s.pump(5.0))
        time.sleep(1.0)
        s.check("ctrl-c: the tool's command was cut short", not os.path.exists(finished))
        return

    s.check("tool result not yet shown", not s.contains(r"⎿", m))
    s.check("ctrl-c: turn interrupted", s.until(r"已中断本轮", m, timeout=15))
    # The prompt after the interrupt (the mark is the one before it: a search from a
    # fresh mark could miss a prompt that already arrived while waiting above).
    s.check("ctrl-c: chat stays at the prompt",
            s.until(r"(?s)已中断本轮.*?❯ ", m, timeout=15))
    s.check("ctrl-c: chat stays after one press", not s.contains(r"续接：", m))
    time.sleep(0.5)
    s.check("ctrl-c: the tool's command was cut short", not os.path.exists(finished))

    # Esc ends the next turn the same way (no Ctrl-C event involved at all).
    m = s.mark()
    s.line("再来一个长命令")
    s.check("esc: tool call shown", s.until(r"shell_exec", m))
    time.sleep(1.0)
    s.send("\x1b")
    s.check("esc: turn interrupted", s.until(r"已中断本轮", m, timeout=15))
    s.check("esc: chat stays at the prompt",
            s.until(r"(?s)已中断本轮.*?❯ ", m, timeout=15))
    s.check("esc: the tool's command was cut short", not os.path.exists(finished))

    # Two Ctrl-C within the confirmation window leave chat (the first one ended the
    # turn, the second lands at the prompt).
    m = s.mark()
    s.line("第三次")
    s.check("exit: tool call shown", s.until(r"shell_exec", m))
    time.sleep(0.5)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: chat ends", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=20))
    s.check("exit: chat process ended", not s.pump(5.0))
    time.sleep(1.0)
    s.check("exit: the tool's command was cut short", not os.path.exists(finished))


def wait_file(path, seconds):
    end = time.time() + seconds
    while time.time() < end:
        if os.path.exists(path):
            return True
        time.sleep(0.1)
    return False


def run_group(binary, work, port, steering, keep):
    """Runs the scenario once: steering on (chat.steering default) or off."""
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    # No approval question: the tool must start on its own, the subject here is
    # the interrupt, not the approval.
    extra = "" if steering else "chat:\n  steering: false\n"
    write_home(home, port, tools_extra="  approval: auto\n", extra=extra)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)

    here = os.path.dirname(os.path.abspath(__file__))
    command = f"touch {STARTED}; sleep 60; touch {FINISHED}"
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "tool", "--delay", "0.2",
                             "--command", command],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        scenario(s, ws, steering)
        s.close()
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results if s else []


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-tool-ctrlc", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    results = []
    for name, steering in [("steering on", True), ("steering off", False)]:
        print(f"\n== {name} ==")
        group = run_group(binary, os.path.abspath(f"{args.work}-{'on' if steering else 'off'}"),
                          args.port, steering, args.keep)
        results += [(f"{name}: {check}", ok) for check, ok in group]
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
