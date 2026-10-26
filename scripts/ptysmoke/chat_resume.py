#!/usr/bin/env python3
"""End-to-end smoke test: a chat resumed after the process that ran its last
turn was killed (Linux/macOS).

Runs scripts/ptysmoke/fakellm.py (text mode, a 51-line reply) and `suga chat` in
a PTY with colors ON (the label above the reply is dim, the reply goes through
the markdown renderer like an answer):
a first chat answers once, then a second turn is killed while the model is asked
for it, leaving the session marked running with a long reply behind it (what a
kill -9 or a crash leaves). Two chats continue that session, `--resume <id>` and
`--session-id <id>` (the path gofer uses): check the dim "上一次回复：" label, the
last reply in full, the "上一回合未正常结束" note of the killed turn and the
"已截断前 N 行" hint naming `suga inspect trace` — all of it above the first ❯ prompt,
which is what tells history from a fresh answer, and all of it without calling
the model. The second continue then sends a prompt: the session was left running
by a process that is gone, so it is recovered first ("已恢复会话", trace event
"session recovered") and the turn answers; its own next turn is killed to leave
the session running again. `suga run --resume <id>` covers the other entry
point: it takes the session lock itself, recovers the session, prints the notice
on stderr and answers on stdout. Finally `suga inspect trace --json` shows one recovered
event per recovery, naming the cancelled turn.

  python3 -I scripts/ptysmoke/chat_resume.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-resume.
"""
import argparse
import os
import re
import select
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

# The model's reply: more lines than the preview shows, so the head is cut.
TAIL = "SMOKE_RESUME_TAIL"
NUM = 50
REPLY = "\n".join(["SMOKE line %02d" % i for i in range(1, NUM + 1)] + [TAIL])
TOTAL = NUM + 1
OMITTED = TOTAL - 40  # lines chat does not show
FIRST_SHOWN = "SMOKE line %02d" % (OMITTED + 1)
LAST_HIDDEN = "SMOKE line %02d" % OMITTED
LABEL = "上一次回复："
INCOMPLETE = "上一回合未正常结束"
RECOVERED = "已恢复会话"


class ModelLog:
    """Counts the requests the fake model served (one JSON line each).

    wait_port reads the "listening" line of the same pipe first; the fake prints
    nothing else before the first request, so nothing can be read ahead here.
    """

    def __init__(self, proc):
        self.fd = proc.stdout.fileno()
        self.buf = ""
        self.n = 0

    def poll(self):
        while select.select([self.fd], [], [], 0)[0]:
            data = os.read(self.fd, 4096)
            if not data:
                break
            self.buf += data.decode("utf-8", "replace")
        lines = self.buf.split("\n")
        self.buf = lines.pop()
        self.n += sum(1 for line in lines if '"path"' in line)
        return self.n

    def wait(self, want, timeout=20.0):
        end = time.time() + timeout
        while time.time() < end:
            if self.poll() >= want:
                return True
            time.sleep(0.05)
        return False


def start_scenario(s, log):
    """A new chat answers, then a second turn is left running: kill the chat
    while the model is asked for it, so sessions.status stays running."""
    m = s.mark()
    s.check("banner shows model", s.until(r"Suga chat · fake/m1", m))
    s.check("❯ prompt", s.until(r"❯ ", m))
    s.check("a new session shows no last reply", LABEL not in s.text(m))

    m = s.mark()
    s.line("第一句")
    s.check("the turn answers", s.until(TAIL, m) and s.until(r"tokens · ", m))

    m = s.mark()
    s.line("第二句")
    s.check("the second turn reached the model", log.wait(2))


def preview(s, m):
    """The text of this run up to the first ❯ prompt."""
    text = s.text(m)
    cut = text.find("❯ ")
    return text if cut < 0 else text[:cut]


def preview_checks(s, sid, name):
    """What a chat continuing the session shows above its first ❯ prompt."""
    m = s.mark()
    s.check(name + ": ❯ prompt", s.until(r"❯ ", m))
    # Everything chat printed before the prompt, i.e. the preview. find() (not
    # index()) so a missing preview fails a check instead of raising.
    before = preview(s, m)
    marker, reply = before.find("▣"), before.find(FIRST_SHOWN)
    s.check(name + ": the label is above the first prompt", LABEL in before)
    s.check(name + ": the label comes before the reply", 0 <= before.find(LABEL) < reply)
    s.check(name + ": the reply keeps the ▣ marker", 0 <= marker < reply)
    s.check(name + ": the last lines of the reply are shown", TAIL in before and reply >= 0)
    s.check(name + ": the head of the long reply is not shown", LAST_HIDDEN not in before)
    s.check(name + ": the truncation hint names the trace command",
            "已截断前 %d 行，完整记录见 suga inspect trace %s" % (OMITTED, sid) in before)
    s.check(name + ": the killed turn is flagged", INCOMPLETE in before)


def quit_chat(s, name):
    m = s.mark()
    # :exit is the same command as :quit; leaving here proves the alias works.
    s.line(":exit")
    s.check(name + ": exit summary", s.until(r"续接：suga chat --resume ", m))


def recover_turn(s, name, prompt):
    """The first prompt of a continue: the stale session is recovered and the
    turn answers."""
    m = s.mark()
    s.line(prompt)
    s.check(name + ": the stale session is recovered", s.until(RECOVERED, m))
    s.check(name + ": the new turn answers", s.until(TAIL, m) and s.until(r"tokens · ", m))
    text = s.text(m)
    s.check(name + ": the notice is above the answer", 0 <= text.find(RECOVERED) < text.find(TAIL))


def kill_turn(s, log, prompt, want):
    """Starts a turn and kills the chat while the model is asked for it: the
    session is left running, like the first scenario."""
    m = s.mark()
    s.line(prompt)
    s.check("the killed turn reached the model", log.wait(want))


def last_session(binary, env, ws):
    """The id of the only session in the scratch home."""
    out = subprocess.run([binary, "sessions", "--json", "-n", "1"], capture_output=True, text=True,
                         env=env, cwd=ws, timeout=60)
    found = re.search(r'"id":"([^"]+)"', out.stdout)
    return found.group(1) if found else ""


def check_trace(s, events):
    """The whole scenario: three turns answered, two stale sessions recovered."""
    kinds = {}
    for e in events:
        kinds[e.get("kind")] = kinds.get(e.get("kind"), 0) + 1
    s.check("trace: one model call per answered turn (the killed ones wrote none)", kinds.get("model") == 3)
    s.check("trace: one turn event per answered turn (the killed ones wrote none)", kinds.get("turn") == 3)
    recovered = [e for e in events if e.get("kind") == "session" and e.get("name") == "recovered"]
    s.check("trace: one recovered event per recovery", len(recovered) == 2)
    s.check("trace: each recovery names its killed turn and the old status",
            all((e.get("attrs") or {}).get("previous_status") == "running"
                and (e.get("attrs") or {}).get("turn_status") == "cancelled" and e.get("turn_id")
                for e in recovered))
    s.check("trace: the two recoveries belong to different turns",
            len({e.get("turn_id") for e in recovered}) == len(recovered))


def run_resume(binary, env, ws, s, sid):
    """`suga run --resume` on the session the killed chat left running: it takes
    the session lock itself, so it recovers it too; the notice goes to stderr
    and stdout keeps the answer."""
    out = subprocess.run([binary, "run", "--read-only", "--resume", sid, "--prompt", "第五句"],
                         capture_output=True, encoding="utf-8", errors="replace", env=env, cwd=ws, timeout=120)
    s.check("run --resume: exits 0", out.returncode == 0)
    s.check("run --resume: the stale session is recovered", RECOVERED in out.stderr)
    s.check("run --resume: the answer is on stdout", TAIL in out.stdout)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-resume", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port)
    # Colors on (like chat_spinner): the label is dim and the reply is rendered
    # as markdown; a pipe or NO_COLOR keeps both plain.
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm")
    env.pop("NO_COLOR", None)
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--delay", "1",
                             "--reply", REPLY], stdout=subprocess.PIPE, text=True)
    sessions = []
    try:
        wait_port(fake)
        log = ModelLog(fake)
        first = PtySession([binary, "chat"], env=env, cwd=ws)
        sessions.append(first)
        start_scenario(first, log)
        first.close(grace=0)  # killed mid-turn: nothing resets the running status
        sid = last_session(binary, env, ws)
        first.check("session id", bool(sid))
        sid = sid or "s_missing"

        # Continuing only reads the session: the preview, no model call and
        # nothing recovered yet (the session is still running afterwards).
        resumed = PtySession([binary, "chat", "--resume", sid], env=env, cwd=ws)
        sessions.append(resumed)
        preview_checks(resumed, sid, "resume")
        resumed.check("resume: no model call", log.poll() == 2)
        quit_chat(resumed, "resume")
        resumed.close()

        by_id = PtySession([binary, "chat", "--session-id", sid], env=env, cwd=ws)
        sessions.append(by_id)
        preview_checks(by_id, sid, "--session-id")
        # The session is running with no live process behind it: the first prompt
        # recovers it and the turn answers.
        recover_turn(by_id, "--session-id", "第三句")
        by_id.check("--session-id: the model was asked once more", log.poll() == 3)
        # Leave it running again, for the `run --resume` path below.
        kill_turn(by_id, log, "第四句", 4)
        by_id.close(grace=0)

        run_resume(binary, env, ws, by_id, sid)
        check_trace(by_id, trace_events(binary, env, sid))
    finally:
        for session in sessions:
            session.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)

    total = sum(len(session.results) for session in sessions)
    failed = [name for session in sessions for name in session.failed()]
    print(f"\n{total - len(failed)}/{total} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
