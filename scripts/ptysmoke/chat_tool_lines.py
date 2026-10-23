#!/usr/bin/env python3
"""End-to-end smoke test: chat shows a tool call by tool, not as JSON (Linux/macOS).

fakellm.py (display mode) makes one turn with three calls and chat prints one
line for each:

- file_read target.txt with offset 1 and limit 2 -> the path and its line window
  (`⏺ file_read target.txt:1-2`), a summary of the tool's own;
- shell_exec of a two-line command -> the command itself, first line only, the
  rest marked with `…` (`⏺ shell_exec echo SMOKE_DISPLAY …`);
- update_plan -> no summary of the tool, so the compact JSON of the arguments
  stays (`⏺ update_plan {"plan":[{"status":"in_progress","step":"read"}]}`).

It also checks the trimmed startup hint: the banner no longer names
:add-dir/:dirs/:rm-dir, :checkpoints and :undo, while `:help` still lists them
(the commands still work and still complete). The trace keeps the JSON argument
summary of every call, so the display is checked to be a display only.

Reverse verification (drop the behaviour and a check fails):

- remove the file_read row from toolSummaries (internal/chat/tool_summary.go):
  the "file_read line" check fails, the JSON shows instead.
- make summaryShell return the command unmodified: the "shell_exec line" check
  fails on the missing `…` of the second line.
- register update_plan in toolSummaries with a non-empty summary: the
  "update_plan keeps the JSON" check fails.
- put any of the removed commands back into bannerHelpText
  (internal/chat/layout.go): the "banner omits" check fails.

  python3 -I scripts/ptysmoke/chat_tool_lines.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-toollines.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

REPLY = "SMOKE_TOOL_LINES_REPLY"
# The fixture the read looks at: two lines are shown, the third is outside the
# window of the call.
TARGET = "SMOKE_TARGET first\nSMOKE_TARGET second\nSMOKE_TARGET third\n"
# The lines chat prints for the three calls of the turn.
FILE_READ_LINE = r"⏺ file_read target\.txt:1-2"
SHELL_LINE = r"⏺ shell_exec echo SMOKE_DISPLAY …"
PLAN_LINE = r'⏺ update_plan \{"plan":\[\{"status":"in_progress","step":"read"\}\]\}'


def scenario(s):
    """The banner hint, the turn's three tool lines and :help."""
    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("❯ prompt", s.until(r"❯ ", m))
    # The hint is trimmed, and only :help names the rare commands: the checks
    # below look at the banner's own text, before :help runs.
    s.check("banner names the common commands",
            s.contains(":session", m) and s.contains(":compact", m) and s.contains(":diff", m))
    s.check("banner omits the rare commands",
            not s.contains(":add-dir", m) and not s.contains(":checkpoints", m) and not s.contains(":undo", m))

    m = s.mark()
    s.line("显示工具调用行")
    s.check("file_read line: path and line window", s.until(FILE_READ_LINE, m))
    s.check("file_read result", s.until(r"⎿ ok: 1 SMOKE_TARGET first", m))
    s.check("shell_exec line: the command, first line and an ellipsis", s.until(SHELL_LINE, m))
    s.check("shell_exec result", s.until(r"⎿ ok: SMOKE_DISPLAY", m))
    s.check("update_plan keeps the JSON of its arguments", s.until(PLAN_LINE, m))
    s.check("turn ends", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))

    m = s.mark()
    s.line(":help")
    # One command per line since the usage table (:help lists name + summary;
    # the details are :<command> -h).
    s.check(":help still lists :add-dir", s.until(r":add-dir ", m) and s.until(r":rm-dir ", m))
    s.check(":help still lists :checkpoints", s.until(r":checkpoints", m))
    s.check(":help still lists :undo", s.until(r":undo ", m))

    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def check_trace(s, events):
    """The display changed only the screen: the trace keeps the calls as they are."""
    tools = [e for e in events if e.get("kind") == "tool"]
    names = [e.get("name") for e in tools]
    s.check("trace: the three tool events", names == ["file_read", "shell_exec", "update_plan"])
    if names != ["file_read", "shell_exec", "update_plan"]:
        return
    attrs = [e.get("attrs") or {} for e in tools]
    s.check("trace: the file_read kept its path", attrs[0].get("target") == ["target.txt"])
    # The trace keeps its own key=value summary (personal.summarizeToolArgs),
    # not the chat line: no ":1-2" range, the command under its key.
    s.check("trace: the argument summaries keep the trace's own form",
            attrs[0].get("arg") == "path=target.txt"
            and str(attrs[1].get("arg") or "").startswith("command=echo SMOKE_DISPLAY"))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-toollines", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18852)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write(TARGET)
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "display", "--delay", "0.2",
                             "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session))
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
