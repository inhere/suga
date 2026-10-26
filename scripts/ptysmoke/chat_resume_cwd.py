#!/usr/bin/env python3
"""End-to-end smoke test of continuing a session in another working directory
(Linux/macOS).

A session records the directory it was created in, but resuming it elsewhere used
to keep working in the new directory silently: the tools, the workspace and the
recorded directory disagreed. This scenario creates a session in directory A and
continues it from directory B with `suga chat --resume`:

- the three-choice question names both directories and is asked before anything
  else happens;
- choice 3 (cancel) leaves without opening chat and without touching sessions.cwd;
- choice 1 moves the process to A: the model then reads a *relative* path, and the
  file content proves the file tools see A (that path does not exist in B);
- choice 2 continues in B and moves sessions.cwd there, which `suga inspect trace` shows
  as a session_cwd_changed line and `suga inspect trace --json` as its event (from A to B);
- in the session's own directory the question does not appear at all.

The non-interactive entries run as subprocesses: `suga run --resume` from A (the
session now lives in B) exits non-zero naming both directories and both ways on,
`--allow-cwd-change` continues there (the relative read proves the workspace) and
moves the session back, and a `suga chat --resume` whose stdin is a pipe is
refused the same way.

Reverse verification (drop the behaviour and the checks fail): making the
comparison in internal/cli/cwdcheck.go always report "same directory" (e.g.
`personal.SameDir(recorded, recorded)`) removes the question and the refusal -
"the cwd question is asked before anything else", every choice and both run
checks, the trace check and the piped-chat check fail while the same-dir checks
keep passing.

  python3 -I scripts/ptysmoke/chat_resume_cwd.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-resume-cwd.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

MARK = "SMOKE_RESUME_CWD_A"
# The relative path the model reads: only A has it, so where the read succeeds is
# where the session's file tools point.
READ_PATH = "cwd_smoke.txt"
PROMPT = "只读：读一下 " + READ_PATH
QUESTION = r"请选择 \[1/2/3\]"


def session_row(binary, env, cwd, sid):
    """The `suga sessions --json` row of one session ({} when it is missing)."""
    # --all: the session may have moved to another directory, and sessions lists
    # the current directory's sessions by default.
    out = subprocess.run([binary, "sessions", "--json", "-n", "0", "--all"], capture_output=True, text=True,
                         env=env, cwd=cwd, timeout=60)
    try:
        rows = json.loads(out.stdout)
    except ValueError:
        return {}
    return next((row for row in rows if row.get("id") == sid), None) or {}


def trace_events(binary, env, cwd, sid):
    out = subprocess.run([binary, "inspect", "trace", sid, "--json"], capture_output=True, text=True,
                         env=env, cwd=cwd, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def cwd_moves(binary, env, cwd, sid):
    return [e for e in trace_events(binary, env, cwd, sid) if e.get("name") == "session_cwd_changed"]


def run_resume(binary, env, cwd, sid, extra=()):
    """One `suga run --resume` from cwd; the prompt makes the model read the
    relative path, so a turn that ran in A carries the file's content in the
    tool result of its stream-json output (the text output shows only the
    model's reply)."""
    return subprocess.run([binary, "run", "--read-only", "--output-format", "stream-json",
                           "--resume", sid, "--prompt", PROMPT, *extra],
                          capture_output=True, encoding="utf-8", errors="replace", env=env, cwd=cwd, timeout=120)


def create_session(s):
    """A first chat in A: it creates the session (recording A) without a turn."""
    m = s.mark()
    s.check("create: banner shows the model", s.until(r"Suga chat · fake/m1", m))
    s.check("create: no cwd question for a new session", not s.contains(QUESTION, m))
    m = s.mark()
    s.line(":exit")
    s.check("create: exit summary names the resume command", s.until(r"续接：suga chat --resume ", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def leave(s, m, name):
    s.check(name + ": exit summary", s.until(r"续接：suga chat --resume ", m))


def same_dir_scenario(binary, env, ws_a, sid, name):
    """Continuing in the session's own directory asks nothing."""
    s = PtySession([binary, "chat", "--resume", sid], env=env, cwd=ws_a)
    try:
        m = s.mark()
        s.check(name + ": ❯ prompt", s.until(r"❯ ", m))
        s.check(name + ": no cwd question", not s.contains(QUESTION, m))
        m = s.mark()
        s.line(":exit")
        leave(s, m, name)
    finally:
        s.close(grace=0)
    return s


def question_scenario(binary, env, ws_a, ws_b, sid, choice, name, follow_up=False):
    """A chat resumed in B: the question must come up before anything else, and
    the answer decides where the session goes on."""
    s = PtySession([binary, "chat", "--resume", sid], env=env, cwd=ws_b)
    m = s.mark()
    asked = s.check(name + ": the cwd question is asked before anything else", s.until(QUESTION, m))
    if asked:
        text = s.text(m)
        s.check(name + ": the question names the session's directory", ws_a in text)
        s.check(name + ": the question names the current directory", ws_b in text)
        s.check(name + ": the question offers going back, staying and cancelling",
                "1) 切到 " + ws_a in text and "2) 在 " + ws_b in text and "3) 取消" in text)
    m = s.mark()
    s.line(choice)
    if choice == "3":
        s.check(name + ": cancelling reports it and leaves", s.until(r"已取消", m))
        s.check(name + ": no chat was opened", not s.contains(r"❯ ", m))
    else:
        s.check(name + ": chat starts", s.until(r"❯ ", m))
        if follow_up:
            m = s.mark()
            # The relative read is what tells which directory the tools got.
            s.line(PROMPT)
            s.check(name + ": the relative file of directory A is found", s.until(MARK, m))
            s.check(name + ": the turn ends", s.until(r"tokens · ", m))
        m = s.mark()
        s.line(":exit")
        leave(s, m, name)
    s.close()
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-resume-cwd", help="scratch dir (SUGA_HOME and the two workspaces)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch dirs after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    raw_a, raw_b = os.path.join(work, "a"), os.path.join(work, "b")
    for path in (home, raw_a, raw_b):
        shutil.rmtree(path, ignore_errors=True)
        os.makedirs(path, exist_ok=True)
    isolate_ws(raw_a)
    isolate_ws(raw_b)
    # The directories as the programs see them (a scratch path may go through a
    # symbolic link, and suga records the resolved one).
    ws_a, ws_b = os.path.realpath(raw_a), os.path.realpath(raw_b)
    # The file the model reads: only A has it.
    with open(os.path.join(ws_a, READ_PATH), "w", encoding="utf-8") as f:
        f.write(MARK + "\n")
    # approval: auto, so the file_read runs without a question of its own.
    write_home(home, args.port, tools_extra="  approval: auto\n")
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "adddir", "--delay", "0.3",
                             "--read-path", READ_PATH, "--reply", "读完了"], stdout=subprocess.PIPE, text=True)
    sessions = []
    session_id = ""
    try:
        wait_port(fake)
        first = PtySession([binary, "chat"], env=env, cwd=ws_a)
        sessions.append(first)
        session_id = create_session(first)
        first.check("create: session id", bool(session_id))
        session_id = session_id or "s_missing"
        first.close()
        first.check("create: the session records directory A",
                    session_row(binary, env, ws_a, session_id).get("cwd") == ws_a)

        # 1) The session's own directory: nothing to ask.
        same = same_dir_scenario(binary, env, ws_a, session_id, "same-dir")
        sessions.append(same)

        # 2) Choice 3: cancel, nothing is opened and nothing changes.
        cancel = question_scenario(binary, env, ws_a, ws_b, session_id, "3", "cancel")
        sessions.append(cancel)
        cancel.check("cancel: sessions.cwd is unchanged", session_row(binary, env, ws_a, session_id).get("cwd") == ws_a)
        cancel.check("cancel: nothing was recorded", cwd_moves(binary, env, ws_a, session_id) == [])

        # 3) Choice 1: the process moves to A, which the relative read proves.
        back = question_scenario(binary, env, ws_a, ws_b, session_id, "1", "back-to-a", follow_up=True)
        sessions.append(back)
        back.check("back-to-a: sessions.cwd is unchanged", session_row(binary, env, ws_a, session_id).get("cwd") == ws_a)

        # 4) Choice 2: continue in B, and record the move.
        stay = question_scenario(binary, env, ws_a, ws_b, session_id, "2", "stay-in-b")
        sessions.append(stay)
        stay.check("stay-in-b: sessions.cwd moved to B", session_row(binary, env, ws_a, session_id).get("cwd") == ws_b)
        moved = cwd_moves(binary, env, ws_b, session_id)
        stay.check("stay-in-b: the trace records one move from A to B",
                   len(moved) == 1 and (moved[0].get("attrs") or {}).get("from") == ws_a
                   and (moved[0].get("attrs") or {}).get("to") == ws_b)
        trace_text = subprocess.run([binary, "inspect", "trace", session_id], capture_output=True, text=True,
                                    env=env, cwd=ws_b, timeout=60).stdout
        stay.check("stay-in-b: `suga inspect trace` shows the move in one line",
                   any("session_cwd_changed" in line and ws_a in line and ws_b in line
                       for line in trace_text.splitlines()))

        # 5) run --resume from A (the session lives in B now) is refused.
        refused = run_resume(binary, env, ws_a, session_id)
        print("  run --resume in A: exit", refused.returncode, "stdout:", refused.stdout.strip()[:120],
              "stderr:", refused.stderr.strip()[:300], flush=True)
        stay.check("run-mismatch: exits non-zero", refused.returncode != 0)
        stay.check("run-mismatch: the error names the session's directory and this one",
                   ws_a in refused.stderr and ws_b in refused.stderr)
        stay.check("run-mismatch: the error offers cd and --allow-cwd-change",
                   "cd " in refused.stderr and "--allow-cwd-change" in refused.stderr)
        stay.check("run-mismatch: no turn ran", MARK not in refused.stdout)

        # 6) --allow-cwd-change continues in A and moves the session back.
        allowed = run_resume(binary, env, ws_a, session_id, ["--allow-cwd-change"])
        stay.check("run-allow: exits 0", allowed.returncode == 0)
        stay.check("run-allow: the turn read the file of directory A", MARK in allowed.stdout)
        stay.check("run-allow: sessions.cwd moved back to A",
                   session_row(binary, env, ws_a, session_id).get("cwd") == ws_a)
        stay.check("run-allow: the second move is in the trace", len(cwd_moves(binary, env, ws_a, session_id)) == 2)

        # 7) chat without a terminal (a pipe) is refused like run.
        piped = subprocess.run([binary, "chat", "--resume", session_id], capture_output=True, text=True,
                               input=":quit\n", env=env, cwd=ws_b, timeout=120)
        stay.check("chat-piped: exits non-zero", piped.returncode != 0)
        stay.check("chat-piped: the error names both directories", ws_a in piped.stderr and ws_b in piped.stderr)
        stay.check("chat-piped: the error offers --allow-cwd-change", "--allow-cwd-change" in piped.stderr)
        stay.check("chat-piped: sessions.cwd is unchanged",
                   session_row(binary, env, ws_b, session_id).get("cwd") == ws_a)
    finally:
        for session in sessions:
            session.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)

    total = sum(len(session.results) for session in sessions)
    failed = [name for session in sessions for name in session.failed()]
    print(f"\n{total - len(failed)}/{total} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
