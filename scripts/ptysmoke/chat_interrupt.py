#!/usr/bin/env python3
"""End-to-end smoke test: an interrupted turn's record replays into the next
turn (Linux/macOS), issue suga-y0sk.

Starts fakellm.py in edit mode with --fail-from 3 --fail-count 3 and short retry
waits (retry.max_attempts 3): the turn runs file_grep, then file_edit (which
changes target.txt on disk), and then the request after that tool result fails
with 3 HTTP 503s, so the turn ends failed with its side effect already applied.
The next prompt ("继续") must reach a model whose request again carries that
file_grep/file_edit exchange plus the interruption note the failed turn left
behind (fakellm's "seen" digest), and `suga inspect export` must show the note entry.

  python3 -I scripts/ptysmoke/chat_interrupt.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-interrupt.
"""
import argparse
import json
import os
import re
import select
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

TARGET = "line one\nSMOKE_TARGET old\nline three\n"
EDITED = "line one\nSMOKE_TARGET new\nline three\n"
# The head of the interruption note the failed turn writes (replay.go), and the
# prefix of its token in fakellm's "seen" digest; the tool tokens are the head of
# the tool result the tool printed (file_grep's line, file_edit's "edited ...").
NOTE = "上一回合运行失败"
NOTE_TOKEN = "user:[" + NOTE
EDIT_RESULT = "tool:edited "
# The turn-2 prompt: its request is the one the replay has to show up in.
PROMPT = "继续"
RETRY_CONFIG = "retry:\n  max_attempts: 3\n  base_delay: 200ms\n  max_delay: 400ms\n"
# The edit applies without a question: this smoke is about the failure, not the
# approval (chat_edit.py covers that).
WRITE_AUTO = "  approval: auto\n"


class FakeLog:
    """fakellm's per-request JSON lines: "user" is the last user message and
    "seen" one token per message of the request (fakellm.message_token)."""

    def __init__(self, proc):
        self.proc = proc
        self.entries = []
        # Bytes read from the pipe but not yet a whole line. The pipe is read with
        # os.read, not readline: a buffered readline after select leaves a second
        # line that arrived in the same chunk in Python's buffer, where select no
        # longer sees it - the log then trails one request behind.
        self.pending = b""

    def drain(self, timeout=0.3):
        end = time.time() + timeout
        fd = self.proc.stdout.fileno()
        while True:
            left = end - time.time()
            if left <= 0:
                return
            ready, _, _ = select.select([fd], [], [], left)
            if not ready:
                return
            chunk = os.read(fd, 65536)
            if not chunk:
                return
            self.pending += chunk
            *lines, self.pending = self.pending.split(b"\n")
            for line in lines:
                try:
                    self.entries.append(json.loads(line.decode("utf-8", "replace")))
                except ValueError:
                    continue

    def prompt_request(self, prompt):
        """The "seen" digest of the first request whose last user message is
        prompt (the replay has to show up in it), else None."""
        for entry in self.entries:
            if str(entry.get("user") or "") == prompt:
                return [str(t) for t in entry.get("seen") or []]
        return None

    def wait_prompt_request(self, prompt, timeout=20.0):
        end = time.time() + timeout
        while time.time() < end:
            seen = self.prompt_request(prompt)
            if seen is not None:
                return seen
            self.drain(0.3)
        return self.prompt_request(prompt)


def scenario(s, ws, log):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))

    # Turn 1: two tool calls run, then the model request that would answer fails
    # with 503s until the retry policy gives up.
    m = s.mark(); s.line("把 old 改成 new")
    s.check("turn 1: file_grep ran", s.until(r"target.txt:2: SMOKE_TARGET old", m))
    s.check("turn 1: file_edit applied", s.until(r"ok: edited ", m))
    s.check("turn 1: the retry is shown", s.until(r"↻ 模型请求失败（HTTP 503）", m))
    s.check("turn 1: failed after its attempts",
            s.until(r"Error: .*request failed after 3 attempts", m))
    s.check("turn 1: prompt is back", s.until(r"❯ ", m))
    with open(os.path.join(ws, "target.txt"), encoding="utf-8") as f:
        s.check("turn 1: the edit is on disk", f.read() == EDITED)

    # Turn 2: the model sees the interrupted turn again.
    m = s.mark(); s.line(PROMPT)
    seen = log.wait_prompt_request(PROMPT)
    s.check("turn 2: reply arrives", s.until(r"完成", m) and s.until(r"tokens · ", m))
    s.check("turn 2 request was sent", seen is not None)
    seen = seen or []  # an empty digest fails the checks below, it does not skip them
    s.check("turn 2 request carries the interruption note",
            any(t.startswith(NOTE_TOKEN) for t in seen))
    s.check("turn 2 request replays the grep call", "a:file_grep" in seen)
    s.check("turn 2 request replays the edit call", "a:file_edit" in seen)
    s.check("turn 2 request replays the edit result",
            any(t.startswith(EDIT_RESULT) for t in seen))
    order = [next((i for i, t in enumerate(seen) if t.startswith(p)), -1) for p in
             ("a:file_grep", "a:file_edit", EDIT_RESULT, NOTE_TOKEN)]
    s.check("turn 2 request keeps them in order", -1 not in order and order == sorted(order))

    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def check_store(s, binary, env, session):
    """The store keeps both turns and the note the failed one appended."""
    events = trace_events(binary, env, session)
    turns = [e for e in events if e.get("kind") == "turn"]
    s.check("trace: the interrupted turn is recorded failed",
            any(t.get("status") == "failed" for t in turns))
    s.check("trace: the turn after it completed",
            any(t.get("status") == "completed" for t in turns))
    out = subprocess.run([binary, "inspect", "export", session], capture_output=True, text=True,
                         env=env, timeout=60)
    s.check("export: the interruption note is stored",
            "suga.turn_interrupted" in out.stdout and NOTE in out.stdout)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-interrupt", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18806)
    p.add_argument("--keep", action="store_true", help="keep the scratch dir after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    with open(os.path.join(ws, "target.txt"), "w", encoding="utf-8") as f:
        f.write(TARGET)
    write_home(home, args.port, tools_extra=WRITE_AUTO, extra=RETRY_CONFIG)
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "edit", "--delay", "0.2",
                             "--fail-from", "3", "--fail-count", "3"],
                            stdout=subprocess.PIPE, text=True)
    log = FakeLog(fake)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s, ws, log)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_store(s, binary, env, session)
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(args.work, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
