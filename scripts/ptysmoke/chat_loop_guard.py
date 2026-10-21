#!/usr/bin/env python3
"""Loop-guard chat smoke for Linux/macOS PTYs.

The scenario runs `suga chat` with fakellm in loop mode: the model calls
file_read twice, so nothing fires, then a third time, where the loop guard must
append its reminder to the repeated result (R1), and a fourth longer turn
covers R2 with the non-consecutive repeats.

Checks: the gray loop_guard line chat shows, the trace events (rule R1 count 3
and R2 count 4), and that the request the fake model received after the third
repeated call carried the reminder (fake logs hint_seen). Reverse verification:
a second session starts with tools.loop_guard: false in home/config.yaml; the
gray line, hint text and trace events must all be absent there, so each check
below would fail if the guard logic were removed or the config wiring broke.

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
GRAY_LINE = r"↻ 重复调用 file_read ×3，已提醒模型"
HINT = "换一种方法"


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


def start(binary, work, port, home_extra=""):
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    # The file the model keeps reading: file_read returns its content.
    with open(os.path.join(ws, "loop_target.txt"), "w", encoding="utf-8") as f:
        f.write("SMOKE_LOOP_TARGET\n")
    isolate_ws(ws)
    write_home(home, port, tools_extra=home_extra)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(port), "--mode", "loop", "--delay", "0.2"],
                            stdout=subprocess.PIPE, text=True, errors="replace")
    wait_port(fake)
    return home, ws, env, fake


def loop_turn(binary, work, port, mark_repeats):
    """One chat session whose single turn repeats file_read four times, with
    the guard checked on the screen, the fake model's requests and the trace
    when mark_repeats, or all three asserted quiet when it is off.
    """
    home, ws, env, fake = start(binary, work, port, home_extra=("" if mark_repeats else "  loop_guard: false\n"))
    s = PtySession([binary, "chat"], env=env, cwd=ws, size=(110, 30))
    try:
        m = s.mark()
        s.check("prompt", s.until(r"❯ ", m))
        s.line("把规则读一遍")
        s.check("turn ends", s.until(STATUS, m))
        if mark_repeats:
            s.check("chat gray line", s.until(GRAY_LINE, m))
            s.check("exactly two gray lines", s.text(m).count("↻ 重复调用 file_read") == 2)
        else:
            time.sleep(0.3)
            assert GRAY_LINE not in s.text(m), "gray line shown while the guard is off"
        session = session_from(s.text())
        m = s.mark()
        for _ in range(3):
            time.sleep(0.25)
            s.send(CTRL_C)
            if s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=1):
                break
        s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
        session = session or session_from(s.text())
        lines = stop_fake(fake)
        reqs = loop_requests(lines)
        # Each loop-mode request makes one more file_read call; step 3 is the
        # answer step, so step < 3 marks the repeated calls.
        if mark_repeats:
            s.check("three repeated calls ran", sum(1 for r in reqs if r["step"] < 3) >= 3)
            s.check("four loop-mode requests logged", len(reqs) >= 4)
            s.check("earlier requests carry no hint", not any(r["hint_seen"] for r in reqs[:2]))
            s.check("third repeated result carries the hint into the next request",
                    any(r["hint_seen"] for r in reqs[2:]))
            if session:
                events = trace_events(binary, env, session)
                guards = [e for e in events if e.get("kind") == "loop_guard"]
                s.check("trace has loop_guard events", len(guards) >= 2)
                s.check("trace R1 count", (guards[0].get("attrs") or {}).get("rule") == "R1"
                        and (guards[0].get("attrs") or {}).get("count") == 3)
                s.check("trace R2 count", (guards[-1].get("attrs") or {}).get("rule") == "R2"
                        and (guards[-1].get("attrs") or {}).get("count") == 4)
        else:
            s.check("four loop-mode requests logged without the guard", len(reqs) >= 4)
            s.check("no hint reached the model", not any(r["hint_seen"] for r in reqs))
            if session:
                events = trace_events(binary, env, session)
                s.check("no trace loop_guard events while off",
                        not any(e.get("kind") == "loop_guard" for e in events))
    finally:
        s.close(grace=0)
        if fake.poll() is None:
            fake.terminate()
            try:
                fake.communicate(timeout=3)
            except subprocess.TimeoutExpired:
                fake.kill()
    return s.results


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", default="suga", help="the suga binary under test")
    p.add_argument("--work", default="tmp/smoke-loop-guard")
    args = p.parse_args()

    results = []
    # The positive run repeats past the R1 rule (four reads: R1 at the third,
    # R2 at the fourth, then silence).
    results += [("on: " + name, ok) for name, ok in
                loop_turn(args.bin, os.path.join(args.work, "on"), 18796, True)]
    # The reverse: tools.loop_guard: false in config.yaml must silence all of
    # it, so every positive check below would fail without the wiring.
    results += [("off: " + name, ok) for name, ok in
                loop_turn(args.bin, os.path.join(args.work, "off"), 18797, False)]
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
