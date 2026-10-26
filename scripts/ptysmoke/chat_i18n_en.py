#!/usr/bin/env python3
"""End-to-end smoke test of the English chat UI in a pseudo terminal (Linux/macOS).

The other scripts in this directory pin `language: zh` in their SUGA_HOME and
assert the Chinese words; this one pins `language: en` and asserts the English
ones, so a regression that leaves a UI string untranslated (or that ignores the
configured language) fails here. It starts scripts/ptysmoke/fakellm.py (tool
mode) and runs `suga chat` in a PTY, covering:

  - the banner (title with the model, the English help line, the tool line);
  - one tool call that needs approval: the English question and its options,
    the "Command:" detail line, a denial, then an approval that runs the tool;
  - the end-of-turn status line;
  - `:help` (the command table summary and the per-command hint);
  - an unknown command;
  - `:context` (its header);
  - the exit summary and the resume command.

A second home starts as `language: zh` and runs `suga config set language
en_US`: the CLI reports the normalized value (`language = en`) and the next
chat of that home speaks English, which is what a person changing the setting
sees. Its checks merge into the same result list (`s.results += ...`).

The fake model's own answer text (`--reply`) is English here, so the negative
checks ("no Chinese exit summary, no Chinese command answer") only see UI text.
Model-visible strings (the tool result of a denied call, the approved note) are
not UI: they stay Chinese in both languages (see wireChatApprover) and are not
asserted.

Reverse verification (drop the behaviour and a check fails):
- remove `language: en` from the home (or write `language: zh`): every English
  check of the first scenario fails - the banner, the question, :help, the exit
  summary;
- make `config set language en_US` keep the raw alias: "config set writes the
  canonical value" fails (the file holds `en_US` instead of `en`);
- drop the language from the chat translator (i18n.Fallback in chat): the
  English banner and exit summary checks fail while the Chinese ones of the
  other scripts still pass.

  make smoke-chat
  python3 -I scripts/ptysmoke/chat_i18n_en.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-i18n.
"""
import argparse
import os
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

# The fake model's answer text: English, so it is no UI text a check could take
# for a translation (the model's own words are never localized).
REPLY = "SMOKE_EN_DONE"
# The question of a shell_exec call that needs a person: the first words and the
# options are common to the three prompt variants (with and without a scope).
QUESTION = r"Allow shell_exec\? \[y\] Yes \[N\] No "
# The end-of-turn status line: the context share and the local time, the same
# words in both languages (the numbers are the localized part).
TURN_STATE = r"tokens · ctx \d+% · [\d.]+m?s · \d{2}:\d{2}"


def start_fake(here, port):
    """The fake model of one chat: tool mode (a shell_exec call that has to be
    approved), answering after a moment so the question is readable."""
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "tool", "--delay", "0.5",
                             "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return fake


def home_env(home):
    """The environment of a chat in this home. SUGA_LANGUAGE is dropped so the
    scenario proves the configured `language:` drives the UI (an exported
    SUGA_LANGUAGE would win over the file, as it does for a real user)."""
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    env.pop("SUGA_LANGUAGE", None)
    return env


def scenario(s):
    """One English chat, from the banner to the exit summary."""
    m = s.mark()
    s.check("banner title and model in English", s.until(r"Suga chat · fake/m1", m))
    s.check("banner help line in English", s.until(r"Type a task to start", m))
    s.check("banner tool line in English", s.until(r"Tools: read/write files and run shell commands", m))
    s.check("❯ prompt", s.until(r"❯ ", m))

    # A tool call that needs a person: the question, its options and the detail
    # line are UI text.
    m = s.mark(); s.line("run it")
    s.check("approval question in English", s.until(QUESTION, m))
    s.check("approval detail line in English", s.until(r"Command: echo SMOKE_OK", m))
    s.check("approval options are not the Chinese ones", not s.contains(r"\[y\]是", m))
    m = s.mark(); s.line("n")
    s.check("denial reported in English", s.until(r"Tools denied this turn: shell_exec", m))
    s.check("denial did not run the tool", not s.contains(r"⎿ ok: SMOKE_OK", m))
    s.check("turn ends with the status line", s.until(TURN_STATE, m))

    # The approving answer runs the tool and the turn answers with the fake's.
    m = s.mark(); s.line("run it again")
    s.check("approval question asked again", s.until(QUESTION, m))
    m = s.mark(); s.line("y")
    s.check("approval runs the tool", s.until(r"⎿ ok: SMOKE_OK", m))
    s.check("turn answers", s.until(r"▣ " + REPLY, m))
    s.check("approved turn ends with the status line", s.until(TURN_STATE, m))

    m = s.mark(); s.line(":help")
    s.check("help lists the commands", s.until(r"Show this session", m))
    s.check("help summarises :usage", s.until(r"Show this session's usage and cost", m))
    s.check("help points at the per-command usage", s.until(r"See a command's details", m))
    s.check("help tail is not the Chinese one", not s.contains(r"查看命令详情", m))

    m = s.mark(); s.line(":context")
    s.check("context header in English", s.until(r"Context composition", m))
    s.check("context tool schema line in English", s.until(r"Tool schemas:", m))

    m = s.mark(); s.line(":nope")
    s.check("unknown command answered in English", s.until(r"Unknown command :nope", m))
    s.check("unknown command is not the Chinese answer", not s.contains(r"未知命令", m))

    m = s.mark(); s.line(":quit")
    s.check("exit summary in English", s.until(r"turns this chat: \d+ · in \d+ / out \d+ tokens", m))
    s.check("exit summary shows the running time", s.until(r"ran [\d.]+m?s", m))
    s.check("resume command in English", s.until(r"resume: suga chat --resume (?:sess_|sg_|s_)", m))
    s.check("exit summary is not the Chinese one", not s.until(r"本次 \d+ 轮", m, timeout=1))


def config_set_scenario(s, binary, home, ws, port):
    """`config set language en_US` on a Chinese home: the CLI writes the
    canonical value and the chat of that home then speaks English."""
    env = home_env(home)
    out = subprocess.run([binary, "config", "set", "language", "en_US"],
                         capture_output=True, text=True, env=env, cwd=ws, timeout=60)
    text = out.stdout + out.stderr
    s.check("config set exits 0", out.returncode == 0)
    # The home was Chinese, so the CLI speaks Chinese up to this write.
    s.check("config set reports the write in the configured language", "已写入" in text)
    s.check("config set reads the canonical value back", "language = en" in text and "source = config:" in text)
    with open(os.path.join(home, "config.yaml"), encoding="utf-8") as f:
        written = f.read()
    s.check("config set wrote the canonical value, not the alias",
            "language: en\n" in written and "en_US" not in written)

    # The alias took effect: the next chat of that home is English.
    here = os.path.dirname(os.path.abspath(__file__))
    fake = start_fake(here, port)
    s2 = None
    try:
        s2 = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        m = s2.mark()
        s2.check("config set: the next chat speaks English", s2.until(r"Type a task to start", m))
        m = s2.mark(); s2.line(":help")
        s2.check("config set: :help is English", s2.until(r"See a command's details", m))
        m = s2.mark(); s2.line(":quit")
        s2.check("config set: exit summary is English",
                 s2.until(r"turns this chat: \d+ · in \d+ / out \d+ tokens", m))
        s2.close()
    finally:
        if s2 is not None:
            s2.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        # The sub-session's checks count: they are printed but must fail the run.
        s.results += s2.results if s2 is not None else []


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-i18n", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0,
                   help="fake model port; 0 picks free ports (the second home runs on the next one)")
    p.add_argument("--keep", action="store_true", help="keep the scratch homes after the run")
    args = p.parse_args()
    # Two homes, each with its own fake model: the base port and the next one.
    args.port = resolve_port(args.port, 1)

    binary = os.path.abspath(args.bin)
    here = os.path.dirname(os.path.abspath(__file__))
    work = os.path.abspath(args.work)
    # A leftover home or workspace of an earlier run would change the behaviour.
    shutil.rmtree(work, ignore_errors=True)

    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra="  approval: ask\n", language="en")

    s = fake = None
    try:
        fake = start_fake(here, args.port)
        s = PtySession([binary, "chat"], env=home_env(home), cwd=ws, size=(100, 30))
        scenario(s)
        s.close()

        # A second home, Chinese first: the config commands write `en` and the
        # chat that follows speaks English.
        print("\n== config set language ==")
        home2 = os.path.join(work, "home2")
        ws2 = os.path.join(work, "ws2")
        os.makedirs(ws2, exist_ok=True)
        isolate_ws(ws2)
        write_home(home2, args.port + 1, tools_extra="  approval: ask\n", language="zh")
        config_set_scenario(s, binary, home2, ws2, args.port + 1)
    finally:
        if s is not None:
            s.close(grace=0)
        if fake is not None:
            fake.terminate()
            fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    results = s.results if s else []
    failed = [name for name, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
