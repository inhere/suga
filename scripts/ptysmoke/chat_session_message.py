#!/usr/bin/env python3
"""session_message: two chats messaging each other, stopped by the hop limit.

Two chat processes share one SUGA_HOME (one fake model serves both, in fakellm's
session mode): the person asks chat A to ask chat B what its working directory
holds. The model of A calls session_message send, which starts a turn in B; B's
model answers with a session_message send of its own, which starts a turn in A,
and so on. The chain has to stop by itself: a message at
tools.session_message.max_hops (default 3) starts no turn in the receiver - it is
only queued, the person sees 已排队 on that terminal, and the exchange ends.

What is checked (a timeout bounds every step, so a loop that never stops fails
instead of hanging):
  b tag        A's message reaches B as a user message tagged with A's short name
               and full session ID.
  a tag        B's answer reaches A the same way.
  hint         the 4th message (the 3rd hop) is held: B prints 已排队 and its
               trace shows no turn for it.
  bounded      the turns of A and B stay at 2 and 1, and the model was asked at
               most MAX_REQUESTS times in total.

The model turns the person's own line ("发给 <短名称>：…") and a received tag
("[来自会话 <短名称>（<id>）的消息] ") into the target of its send, so no fake
server has to know the peers in advance (fakellm.py's session mode).

  python3 -I scripts/ptysmoke/chat_session_message.py --bin tmp/smoke/suga

Reverse verification (to run in the Linux container; UNVERIFIED here - this file
was written on a machine without a pty):
- make shouldHoldSessionMessage always false (or drop its call in
  chatState.deliverMessage): the hint check fails, B's trace shows a turn for the
  3rd hop, and the two chats keep answering until the request bound trips.
- send hops 0 instead of personal.TurnHops(ctx)+1 (wiring's
  sessionMessageRequest): every message looks like a person's own, the hint never
  appears and the loop runs past MAX_REQUESTS.
- drop the session label (deliveryLineFor's sessionDeliveryLine branch): the tag
  checks fail, so the model no longer knows whom to answer.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_steer import FakeLog  # noqa: E402
from chat_observe import trace_events  # noqa: E402

PROMPT = "❯ "
# What chat prints for its session (:session line and banner): the full ID and the
# short name `suga send --session <name>` accepts.
SESSION_LINE = re.compile(r"session id: ((?:sess_|sg_|s_)\S+)(?: · 会话 (\S+))?")
REPLY = "SESSION_OK"
# The question the person asks chat A to pass on.
ASK = "你的工作目录里有什么文件？"
# What the receiver says when it held a message at the hop limit
# (chat.sessionQueuedHint).
HINT = "已排队"
# Upper bound of model requests for the whole exchange: A's person turn, B's
# answer, A's answer - six requests, plus room for a retry.
MAX_REQUESTS = 8
# How long the whole exchange may take (the model delays are small; a loop that
# never stops is what this bound catches).
EXCHANGE_TIMEOUT = 90


def session_info(s, timeout=30):
    """(session id, short name) as chat printed them, waiting for the line."""
    m = s.mark()
    s.until(r"session id: ", m, timeout=timeout)
    # The start-up line has the id only; :session adds the short name once the
    # session socket is registered (chat/session_name.go sessionLine).
    s.until(r"❯ ", m, timeout=timeout)
    m = s.mark()
    s.line(":session")
    s.until(r"· 会话 \S+", m, timeout=timeout)
    found = SESSION_LINE.search(s.text(m))
    if not found:
        return "", ""
    return found.group(1), found.group(2) or ""


def turns_of(binary, env, session_id):
    """The turns a session's trace records (0 without a trace)."""
    if not session_id:
        return 0
    return len([e for e in trace_events(binary, env, session_id) if e.get("kind") == "turn"])


def exit_chat(s):
    """Leaves chat (two Ctrl-C in a row) so its trace is complete."""
    m = s.mark()
    for _ in range(3):
        time.sleep(0.3)
        s.send("\x03")
        if s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=1):
            return
    s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m, timeout=10)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-session-message", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    p.add_argument("--dump", action="store_true", help="print what both chats wrote")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    # The tool asks nobody (the default): a message to a peer needs no approval.
    write_home(home, args.port, tools_extra="", extra="")
    env = dict(os.environ, SUGA_HOME=home, TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    # The two chats stand in for a person's terminals: they are not gofer jobs, so
    # the job marker of the process that started this smoke must not take
    # session_message (GOFER_JOB_ID) away from them.
    env.pop("GOFER_JOB_ID", None)

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "session",
                             "--delay", "0.2", "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    a = b = None
    checks = []
    log = None
    try:
        wait_port(fake)
        log = FakeLog(fake)
        # B first: A's first line names it, and its process record is what the
        # tool resolves the short name against.
        b = PtySession([binary, "chat"], env=env, cwd=ws)
        b_id, b_name = session_info(b)
        checks.append(("B is running", bool(b_id and b_name)))
        a = PtySession([binary, "chat"], env=env, cwd=ws)
        a_id, a_name = session_info(a)
        checks.append(("A is running", bool(a_id and a_name)))
        if not (a_id and a_name and b_name):
            return report(checks, a, b, args.dump)

        # The person asks A to talk to B.
        m = a.mark()
        a.line("发给 %s：%s" % (b_name, ASK))
        checks.append(("A calls session_message",
                       a.until(r"⏺ session_message", m, timeout=EXCHANGE_TIMEOUT)))
        # B receives it as a tagged user message and starts a turn of its own.
        mb = b.mark()
        checks.append(("B reads the message as A's",
                       b.until(re.escape("[来自会话 %s（%s）的消息]" % (a_name, a_id)), mb,
                               timeout=EXCHANGE_TIMEOUT)))
        # ...and answers: A reads B's message the same way.
        ma = a.mark()
        checks.append(("A reads B's answer as B's",
                       a.until(re.escape("[来自会话 %s（%s）的消息]" % (b_name, b_id)), ma,
                               timeout=EXCHANGE_TIMEOUT)))
        # The third hop reaches B, which holds it instead of starting a turn.
        checks.append(("the 3rd hop is queued, not answered",
                       b.until(HINT, mb, timeout=EXCHANGE_TIMEOUT)))
        checks.append(("B names the sender of the held message",
                       b.until(re.escape("来自会话 %s 的消息已排队" % a_name), mb,
                               timeout=EXCHANGE_TIMEOUT)))

        # Nothing may follow: the exchange is over, so the counts stay bounded.
        time.sleep(3)
        requests = len(log.requests())
        checks.append(("the model was asked at most %d times (%d)" % (MAX_REQUESTS, requests),
                       requests <= MAX_REQUESTS))
    finally:
        # Leave both chats so their traces hold every turn.
        for s in (a, b):
            if s is not None:
                exit_chat(s)
    try:
        a_turns = turns_of(binary, env, a_id)
        b_turns = turns_of(binary, env, b_id)
        checks.append(("A ran two turns (%d)" % a_turns, a_turns == 2))
        checks.append(("B ran one turn, the held message started none (%d)" % b_turns, b_turns == 1))
    finally:
        for s in (a, b):
            if s is not None:
                s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    return report(checks, a, b, args.dump)


def report(checks, a, b, dump):
    """Prints the checks (and optionally what both chats wrote) and exits."""
    if dump:
        for name, s in (("A", a), ("B", b)):
            if s is not None:
                print("--- %s ---" % name)
                print(s.text())
        print("--- end ---")
    for name, ok in checks:
        print("%s %s" % ("ok  " if ok else "FAIL", name))
    failed = [name for name, ok in checks if not ok]
    print("\n%d/%d passed" % (len(checks) - len(failed), len(checks)))
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
