#!/usr/bin/env python3
"""End-to-end smoke test of `suga chat` in a pseudo terminal (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (tool mode), writes a temporary SUGA_HOME
with tools.allow_write: true, runs `suga chat` in a PTY and checks:
banner with model, ❯ prompt, approval question with the full command,
n denies and the turn goes on, y runs the tool, Ctrl-C at the question only
cancels the turn (with text typed on the line too), one Ctrl-C at the prompt only
hints (with text on the line it clears the line instead),
two in a row print the resume command and usage summary,
and piped (non-TTY) input denies write tools.

The pre-typed "y" of an approval question runs in two groups of its own (the way
chat_tool_ctrlc.py does it), because chat.steering decides what a line typed
during a turn is: with it on (the default) the line is a message inserted into
the running turn, with it off the console stays cooked and the question's own
read must not take the line as its answer.

Reverse verification (drop the behaviour and the check fails):
- restore OPOST|ONLCR for a prompt read too (restoreOutputProcessing in makeRaw):
  the two "CR LF" checks fail - the editor's CR LF reaches the pty as CR CR LF.
- let a cooked turn read the keyboard (a steer read with chat.steering: false):
  "pre-typed y: no insert at all" fails; without the flush and the epoch bump
  of Ask (ask.go) the pre-typed y answers the question and "n: denied" fails.
- drop the non-empty branch of the Ctrl-C case in lineedit's readLine (the
  t.ctrlCClearsLine one): "ctrl-c non-empty: no exit hint" fails - the press
  starts the exit confirmation (再按一次 Ctrl+C 退出) while "abc" is on the line.
- give the answer read a clearing Ctrl-C as well (SetCtrlCClearsLine(true) for
  every read, ask.go included): "ctrl-c with text: cancelled" fails - the press
  drops the typed answer and the question waits on instead of cancelling.

  make smoke-chat                        # builds tmp/smoke/suga and runs this
  python3 -I scripts/ptysmoke/chat_approval.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke.
"""
import argparse
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, CTRL_U, PtySession, input_row  # noqa: E402

OK = r"⎿ ok: SMOKE_OK"
QUESTION = r"允许执行 shell_exec"
# The pty width every scenario here runs with (PtySession size): the screen model
# reads the input row off the screen and lays its rows out with it.
COLS = 100
# The status line every turn ends with: the context share and the local time.
TURN_STATE = r"tokens · ctx \d+% · [\d.]+m?s · \d{2}:\d{2}"


def isolate_ws(ws):
    """Make ws its own git root, so suga does not pick up the enclosing
    repository (e.g. its root MEMORY.md would switch project memory to the
    repository layout and the smoke would write into the real repo)."""
    subprocess.run(["git", "init", "-q", ws], check=True)


def write_home(home, port, tools_extra="", memory_extra="", provider_extra="", extra=""):
    """Writes SUGA_HOME/config.yaml. Project memory is forced to the files
    backend: the scratch workspace lives inside this repository, whose
    .gofer/tracker would otherwise select (and call) the real gofer.
    provider_extra goes under providers.fake, extra at the top level."""
    os.makedirs(home, exist_ok=True)
    # An empty .env makes this a home that was already set up: without it the
    # first run creates one and prints a notice naming the work directory,
    # which other scenes would then see (chat_onboarding covers first runs).
    open(os.path.join(home, ".env"), "a", encoding="utf-8").close()
    with open(os.path.join(home, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(f"""model: fake/m1
providers:
  fake:
    type: openai
    base_url: http://127.0.0.1:{port}/v1
    api_key: k
{provider_extra}tools:
  allow_write: true
{tools_extra}memory:
  project:
    backend: files
{memory_extra}{extra}""")


def wait_port(proc):
    line = proc.stdout.readline()  # {"listening": port}
    if "listening" not in line:
        raise SystemExit("fake model did not start: " + line)


def turn_end(s, start):
    """Waits for the end-of-turn status line, which every turn prints."""
    return s.until(r"tokens · ", start)


# The pre-typed "y" of an approval question: what such a line is depends on
# chat.steering, so it runs as a scenario of its own, once per setting (the way
# chat_tool_ctrlc.py runs both groups).
def pretyped_scenario(s, steering):
    """The pre-typed y of an approval question, per chat.steering setting.

    steering on (the default): the turn keeps reading the input line, so the y is
    a message inserted into the running turn - shown as such before the model's
    answer, which is what makes the run deterministic - and the question that
    follows is answered by the next line. The insert reached the turn, so the
    request after the denied tool result carries it and the turn answers with the
    model's reply (fakellm's tool mode).
    steering off: the console stays cooked and nothing reads during the turn, so
    the y waits in the tty and the question's own read must not take it for an
    answer (unread input is flushed, ask.go). It is no inserted message either.
    """
    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("❯ prompt", s.until(r"❯ ", m))

    m = s.mark()
    s.line("跑一下")
    if steering:
        # The turn's own read (the ↳ prompt) is up right after the turn starts.
        # Type the y a moment later - its steer inbox is open by then (design m8)
        # - and well before the model's answer (the fake's delay), so it can
        # never land at the question that follows.
        s.check("the prompt becomes ↳ while the turn runs", s.until(r"↳ ", m))
        time.sleep(0.5)
    else:
        time.sleep(0.3)
    m = s.mark()
    s.line("y")
    if steering:
        # The insert is on screen before the model's answer arrived: the y went
        # to the turn and not to the question (a lost byte would fail here
        # instead of changing what the checks below see).
        s.check("pre-typed y: inserted into the turn", s.until(r"\[插话\] y（等待送达）", m))
    s.check("pre-typed y: question still asked", s.until(QUESTION, m))
    s.check("pre-typed y: not executed", not s.contains(OK, m))
    s.line("n")
    s.check("n: denied", s.until(r"用户拒绝执行 shell_exec", m))
    if steering:
        # The request after the denied tool result carried the insert (steering
        # hands it to the turn's next request): the model answers with its text.
        s.check("the insert reached the turn", s.until(r"\[插话\] y（已送达）", m))
        s.check("the turn answers", s.until(r"完成", m))
    s.check("n: denied tools listed", s.until(r"本轮被拒绝的工具：shell_exec", m))
    s.check("n: turn ends", turn_end(s, m))
    if not steering:
        # Nothing reads the keyboard during a cooked turn, so the y became no
        # inserted message at all - checked once the whole turn is over, which is
        # what makes it cover the turn rather than the instant of the check.
        s.check("pre-typed y: no insert at all", not s.contains(r"\[插话\]", m))
    s.check("turn end shows the context share and the local time", s.until(TURN_STATE, m))


def scenario(s):
    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("banner shows write tools on", s.until(r"工具：可读写文件、执行 shell", m))
    s.check("❯ prompt", s.until(r"❯ ", m))

    # A wide window (100 columns): the line editor wraps at the terminal's width,
    # not at a fixed 80, so a 90-column line stays on one row. The check is
    # falsifiable: with the fixed 80 (no SetSize) the row wraps and the text is
    # never 90 consecutive "x" (verified by removing the SetSize call).
    m = s.mark(); s.send("x" * 90)
    s.check("wide window: 90 columns stay on one row", s.until(r"x{90}", m))
    s.send(CTRL_U); time.sleep(0.2)

    # A wide rune never straddles two rows: at the last column the editor writes a
    # filler space first. "a" + 29 wide runes reach column 59, so the filler shows
    # only when the editor lays the line out 60 columns wide - which is what this
    # control run (100 columns) must not do.
    m = s.mark(); s.send("a" + "好" * 29); s.drain(0.8)
    s.check("wide window: no wide-rune filler at column 99", not s.contains(r"好 \r\n", m))
    s.send(CTRL_U); time.sleep(0.2)

    # A window resized while chat runs reaches the editor: at 60 columns the same
    # line hits the last column and the filler appears (SIGWINCH; Windows polls).
    s.resize(60, 30); time.sleep(0.5)
    m = s.mark(); s.send("a" + "好" * 29)
    s.check("resize: the editor lays out at the narrower width", s.until(r"好 \r\n", m, timeout=2))
    s.send(CTRL_U); time.sleep(0.2)
    s.resize(100, 30); time.sleep(0.5)

    # Pre-typed y + Enter while the turn runs must not approve: that section runs
    # in its own groups (pretyped_scenario), because chat.steering decides what
    # such a line is.

    m = s.mark(); s.line("跑一下")
    s.check("y: full command shown", s.until(r"命令：echo SMOKE_OK", m) and s.until(QUESTION, m))
    s.line("y")
    s.check("y: executed", s.until(OK, m) and s.until(r"完成", m))
    s.check("y: answer marked ▣", s.contains(r"▣ 完成", m))
    s.check("y: turn ends", turn_end(s, m))
    # NO_COLOR in this scenario (see main): the input line gets no background.
    s.check("no highlight without colors", not s.contains_raw(r"\x1b\[48;", m))

    m = s.mark(); s.line("第三次")
    s.check("ctrl-c: question", s.until(QUESTION, m))
    s.send(CTRL_C)
    s.check("ctrl-c: cancelled", s.until(r"已取消", m))
    s.check("ctrl-c: not executed", not s.contains(OK, m))
    time.sleep(0.5)

    # Text on the line does not change that: the answer read is the one read whose
    # Ctrl-C stays "cancel" (the prompt and a turn's read drop such a line instead,
    # wll1). The question is asked again for this.
    m = s.mark(); s.line("第四次")
    s.check("ctrl-c with text: question", s.until(QUESTION, m))
    s.send("abc"); s.drain(0.4); s.send(CTRL_C)
    s.check("ctrl-c with text: cancelled", s.until(r"已取消", m))
    s.check("ctrl-c with text: not executed", not s.contains(OK, m))
    time.sleep(0.5)

    m = s.mark(); s.line("继续")
    s.check("a: chat alive, question", s.until(QUESTION, m))
    s.line("a")
    s.check("a: persistence question", s.until(r"把这条规则写进规则文件", m))
    s.line("")
    s.check("a: executed", s.until(OK, m) and turn_end(s, m))
    m = s.mark(); s.line("又一次")
    s.check("a: executed again", s.until(OK, m) and turn_end(s, m))
    s.check("a: no question again", not s.contains(QUESTION, m))

    # Ctrl-C on a non-empty input line only clears the line (wll1, the way Claude
    # Code does it): no exit confirmation, chat stays, and the row is left at the
    # prompt alone. An empty line keeps the old meaning, checked right after.
    m = s.mark(); time.sleep(0.3); s.send("abc"); time.sleep(0.2); s.send(CTRL_C)
    s.drain(0.6)
    s.check("ctrl-c non-empty: no exit hint", not s.contains(r"再按一次 Ctrl\+C 退出", m))
    s.check("ctrl-c non-empty: chat alive", not s.contains(r"续接：", m))
    s.check("ctrl-c non-empty: the input line is empty", input_row(s, COLS) == "❯")
    # The cleared text is really gone: what is typed next is a line of its own.
    s.send("def"); s.drain(0.6)
    s.check("ctrl-c non-empty: the next text is a line of its own", input_row(s, COLS) == "❯ def")
    s.send(CTRL_U); s.drain(0.3)

    # An empty line: one Ctrl-C only asks for another (the old meaning).
    m = s.mark(); s.send(CTRL_C)
    s.check("ctrl-c once: hint", s.until(r"再按一次 Ctrl\+C 退出", m))
    s.check("ctrl-c once: chat alive", not s.contains(r"续接：", m))
    # A second Ctrl-C after the window is again only a hint.
    m = s.mark(); time.sleep(3.5); s.send(CTRL_C)
    s.check("ctrl-c late: hint again", s.until(r"再按一次 Ctrl\+C 退出", m))
    s.check("ctrl-c late: chat alive", not s.contains(r"续接：", m))

    m = s.mark(); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: usage summary", s.until(r"本次 \d+ 轮 · in \d+ / out \d+ tokens", m))
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|s_)", m))


def non_tty(binary, env, cwd):
    out = subprocess.run([binary, "chat"], input="跑一下\n:quit\n", capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    text = out.stdout + out.stderr
    return "标准输入不是终端" in text and "tools.approval: auto" in text


def run_main(binary, args):
    """The main scenario once, with the default chat.steering."""
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "tool", "--delay", "1"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        scenario(s)
        s.close()
        s.check("non-tty: write tool denied with hint", non_tty(binary, env, ws))
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    return s.results if s else []


def run_group(binary, work, port, steering, keep):
    """Runs the pre-typed y scenario once: steering on (chat.steering default) or
    off. The model answers slowly (--delay 2), so the y is typed into the turn well
    before the question it must not answer can appear."""
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, port, extra="" if steering else "chat:\n  steering: false\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "tool", "--delay", "2"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        pretyped_scenario(s, steering)
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
    p.add_argument("--work", default="tmp/smoke", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18795)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    results = run_main(binary, args)
    # The pre-typed y of an approval question, once per chat.steering setting.
    for i, (name, steering) in enumerate([("steering on", True), ("steering off", False)]):
        print(f"\n== pre-typed y: {name} ==")
        group = run_group(binary, os.path.abspath(f"{args.work}-pretyped-{'on' if steering else 'off'}"),
                          args.port + 1 + i, steering, args.keep)
        results += [(f"pre-typed y ({name}): {check}", ok) for check, ok in group]
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
