#!/usr/bin/env python3
"""Loop-guard chat smoke for Linux/macOS PTYs.

Runs `suga chat` against fakellm's loop modes and checks what the loop guard
(internal/personal/loop_guard.go, tools.loop_guard, on by default) does:

  loop (on)   four identical file_read calls of an unchanged file: R1 on the
              third, R2 on the fourth. Checks the gray line chat shows, the
              trace events (rule R1 count 3, R2 count 4) and that the request
              after the third call carried the reminder (the fake logs
              hint_seen / hint_kind per request).
  loop (off)  the same turn with tools.loop_guard: false: gray line, hint text,
              hints in the fake's requests and trace events must all be absent.
  loop-edit   the same successful shell command four times with a successful
              file_edit in between (identical output each run). The workspace
              changes, so no reminder may appear. Reverse verification: drop the
              successful-write reset in loop_guard.go and the 4th run of the
              command triggers R2, which fails every check of this scenario.
  loop-fail   the same failing command three times: R3 on the third failure
              (gray line "↻ shell_exec 已失败 ×3，已提醒模型", trace rule R3
              count 3, the reminder in the following request). Reverse
              verification: without R3 (or if R1 reported it with its "结果没有
              变化" wording) the rule and wording checks fail.

  python3 -I scripts/ptysmoke/chat_loop_guard.py --bin tmp/smoke/suga
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
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402
from ptydrive import CTRL_C, PtySession  # noqa: E402

STATUS = r"tokens · "
HINT = "换一种方法"
GRAY_R1 = r"↻ 重复调用 file_read ×3，已提醒模型"
GRAY_R3 = r"↻ shell_exec 已失败 ×3，已提醒模型"
EXIT_LINE = r"续接：suga chat --resume (?:sess_|sg_|s_)"


def stop_fake(fake):
    fake.terminate()
    try:
        output, _ = fake.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        fake.kill()
        output, _ = fake.communicate(timeout=5)
    return [line for line in output.splitlines() if line.strip().startswith("{")]


def loop_requests(lines):
    """The loop-mode json lines the fake logged, in order."""
    out = []
    for line in lines:
        obj = json.loads(line)
        if obj.get("loop"):
            out.append(obj)
    return out


def session_from(text, start=0):
    found = re.search(r"续接：suga chat --resume ((?:sess_|sg_|s_)\S+)", text[start:])
    return found.group(1) if found else ""


def start(binary, work, port, mode, home_extra=""):
    """Fresh SUGA_HOME and workspace for one scenario, plus the running fake.

    tools.approval: auto is what lets the shell_exec and file_edit calls run
    without an approval question (the guard is what this smoke measures).
    """
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    # The file loop mode keeps reading: file_read returns its content.
    with open(os.path.join(ws, "loop_target.txt"), "w", encoding="utf-8") as f:
        f.write("SMOKE_LOOP_TARGET\n")
    # The file loop-edit edits: old -> new -> old, so every edit succeeds.
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write("line one\nSMOKE_TARGET old\nline three\n")
    isolate_ws(ws)
    write_home(home, port, tools_extra="  approval: auto\n" + home_extra)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", mode, "--delay", "0.2"],
                            stdout=subprocess.PIPE, text=True, errors="replace")
    wait_port(fake)
    return home, ws, env, fake


def exit_chat(s):
    """Leaves chat (Ctrl-C until the exit hint) and returns the resume line."""
    m = s.mark()
    for _ in range(3):
        time.sleep(0.25)
        s.send(CTRL_C)
        if s.until(EXIT_LINE, m, timeout=1):
            break
    s.check("exit: resume command", s.until(EXIT_LINE, m))
    return session_from(s.text())


def run_turn(binary, work, port, mode, home_extra, verify):
    """One chat session with one turn; the fake's loop requests and the trace
    events are handed to verify for the scenario's own checks."""
    home, ws, env, fake = start(binary, work, port, mode, home_extra)
    s = PtySession([binary, "chat"], env=env, cwd=ws, size=(110, 30))
    try:
        m = s.mark()
        s.check("prompt", s.until(r"❯ ", m))
        s.line("把规则跑一遍")
        s.check("turn ends", s.until(STATUS, m))
        time.sleep(0.5)  # the last tool results and the guard's line render
        session = exit_chat(s)
        reqs = loop_requests(stop_fake(fake))
        events = trace_events(binary, env, session) if session else []
        verify(s, reqs, events)
    finally:
        s.close(grace=0)
        if fake.poll() is None:
            fake.terminate()
            try:
                fake.communicate(timeout=3)
            except subprocess.TimeoutExpired:
                fake.kill()
    return s.results


def check_trace_quiet(s, events):
    """The guard recorded nothing: no loop_guard trace event."""
    s.check("no trace loop_guard events", not any(e.get("kind") == "loop_guard" for e in events))


def verify_read_on(s, reqs, events):
    """loop mode: R1 on the third identical read, R2 on the fourth."""
    screen = s.text()
    s.check("chat gray line R1", re.search(GRAY_R1, screen) is not None)
    s.check("exactly two gray lines", screen.count("↻ 重复调用 file_read") == 2)
    # Each loop-mode request makes one more file_read call; step 3 is the answer
    # step, so step < 3 marks the repeated calls.
    s.check("three repeated calls ran", sum(1 for r in reqs if r["step"] < 3) >= 3)
    s.check("four loop-mode requests logged", len(reqs) >= 4)
    s.check("earlier requests carry no hint", all(r["hint_kind"] == "" for r in reqs[:2]))
    s.check("third repeated result carries the hint into the next request",
            any(r["hint_kind"] == "repeat" for r in reqs[2:]))
    guards = [e for e in events if e.get("kind") == "loop_guard"]
    if not s.check("trace has loop_guard events", len(guards) >= 2):
        return
    s.check("trace R1 count", (guards[0].get("attrs") or {}).get("rule") == "R1"
            and (guards[0].get("attrs") or {}).get("count") == 3)
    s.check("trace R2 count", (guards[-1].get("attrs") or {}).get("rule") == "R2"
            and (guards[-1].get("attrs") or {}).get("count") == 4)


def verify_read_off(s, reqs, events):
    """tools.loop_guard: false silences all of it: every positive check of the
    on-scenario would fail here if the guard ran anyway."""
    time.sleep(0.2)
    screen = s.text()
    s.check("no gray line while off", re.search(r"↻ 重复调用 file_read", screen) is None)
    s.check("no hint text on screen while off", HINT not in screen)
    s.check("four loop-mode requests logged without the guard", len(reqs) >= 4)
    s.check("no hint reached the model", not any(r["hint_kind"] for r in reqs))
    check_trace_quiet(s, events)


def verify_edit_shell(s, reqs, events):
    """loop-edit: the same successful command with a successful edit between two
    runs is a changed workspace, not a loop."""
    screen = s.text()
    s.check("edits and commands both ran", len(reqs) >= 7)
    s.check("no gray line for edit/command alternation",
            re.search(r"↻ 重复调用 shell_exec", screen) is None)
    s.check("no hint text for edit/command alternation", HINT not in screen)
    s.check("no hint reached the model", not any(r["hint_kind"] for r in reqs))
    check_trace_quiet(s, events)


def verify_fail_shell(s, reqs, events):
    """loop-fail: the third failure of one command is R3."""
    screen = s.text()
    s.check("chat gray line R3", re.search(GRAY_R3, screen) is not None)
    s.check("three loop-mode requests logged", len(reqs) >= 3)
    s.check("the failing command's third result carries the hint into the next request",
            any(r["hint_kind"] == "r3" for r in reqs))
    guards = [e for e in events if e.get("kind") == "loop_guard"]
    if not s.check("trace has an R3 event", len(guards) >= 1):
        return
    s.check("trace R3 count", all((e.get("attrs") or {}).get("rule") == "R3" for e in guards)
            and (guards[0].get("attrs") or {}).get("count") == 3)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="suga", help="the suga binary under test")
    p.add_argument("--work", default="tmp/smoke-loop-guard")
    args = p.parse_args()
    # chat runs in its own workspace: a relative --bin would not resolve there.
    args.bin = os.path.abspath(args.bin)

    results = []
    scenarios = [
        ("on: ", "loop", "", 18866, verify_read_on),
        # The reverse: tools.loop_guard: false must silence all of it.
        ("off: ", "loop", "  loop_guard: false\n", 18867, verify_read_off),
        ("edit-shell: ", "loop-edit", "", 18868, verify_edit_shell),
        ("fail-shell: ", "loop-fail", "", 18869, verify_fail_shell),
    ]
    for prefix, mode, home_extra, port, verify in scenarios:
        name = prefix.strip().rstrip(":")
        results += [(prefix + check, ok) for check, ok in
                    run_turn(args.bin, os.path.join(args.work, name), port, mode, home_extra, verify)]
    failed = [name for name, ok in results if not ok]
    print(f"{len(results) - len(failed)}/{len(results)} passed")
    for name in failed:
        print("FAIL " + name)
    if failed:
        return 1
    print("SMOKE_LOOP_GUARD_OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
