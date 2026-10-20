#!/usr/bin/env python3
"""End-to-end smoke test: a chat registers a short session name that `suga send`
accepts (Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (text mode) and `suga chat` in a PTY whose
workspace directory is "Smoke_WS", then checks: the banner shows the generated
short name "smokews-xy" (the directory lower cased and cleaned), `suga ps` and
`suga ps --json` list that name for the session, `suga send --session <short
name>` delivers a message chat runs as a turn, the first prompt text is *not* an
address (the record no longer carries the first message as its name), :session
shows the id and the name, the exit summary names it next to the resume command,
and a `--resume` goes on under the same name.

  python3 -I scripts/ptysmoke/chat_shortname.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-shortname.
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
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

# "Smoke_WS" is cleaned to "smokews", so the banner proves the directory is the
# source of the name; the suffix is two characters of [a-z0-9].
NAME_PATTERN = r"smokews-[a-z0-9]{2}"
BANNER = r"Suga chat · fake/m1[^\n]*会话 " + NAME_PATTERN
REPLY = "收到啦"


def ps_rows(binary, env):
    out = subprocess.run([binary, "ps", "--json"], capture_output=True, text=True, env=env, timeout=30)
    try:
        return json.loads(out.stdout or "[]")
    except ValueError:
        return []


def run_send(binary, env, session, text):
    """Runs `suga send --session <session> <text>`; returns (exit code, stderr)."""
    out = subprocess.run([binary, "send", "--session", session, text], capture_output=True, text=True,
                         env=env, timeout=30)
    return out.returncode, out.stderr.strip()


def record_of(binary, env, session):
    rows = [r for r in ps_rows(binary, env) if r.get("session_id") == session]
    return rows[0] if len(rows) == 1 else None


def scenario(s, binary, env):
    m = s.mark()
    s.check("banner shows the short name", s.until(BANNER, m))
    found = re.search(r"会话 (" + NAME_PATTERN + ")", s.text(m))
    name = found.group(1) if found else ""
    session = re.search(r"session id: ((?:sess_|s_)\S+)", s.text(m))
    sid = session.group(1) if session else ""
    s.check("the same line shows the session id", bool(sid))
    if not (sid and name):
        # Without an address the remaining checks cannot be meaningful (an empty
        # pattern would match anything).
        s.check("registration produced a short name", False)
        return sid, name

    record = record_of(binary, env, sid)
    s.check("ps --json lists the short name", record is not None and record.get("name") == name)
    listed = subprocess.run([binary, "ps"], capture_output=True, text=True, env=env, timeout=30).stdout
    s.check("ps lists the short name", "%s\t%s\t" % (sid, name) in listed)

    # A message addressed by short name reaches the running chat and starts a turn.
    m = s.mark()
    code, err = run_send(binary, env, name, "来自短名称")
    s.check("send by short name succeeds", code == 0 and err == "delivered")
    s.check("chat shows the delivered message", s.until(r"\[来自 cli\] 来自短名称", m))
    s.check("chat answers the delivered message", s.until(REPLY, m) and s.until(r"tokens · ", m))

    # A typed prompt becomes the session title, never an address: the record keeps
    # the short name and the prompt text is not resolvable.
    m = s.mark()
    s.line("第一句话")
    s.check("the typed prompt is answered", s.until(REPLY, m) and s.until(r"tokens · ", m))
    code, _ = run_send(binary, env, "第一句话", "不要送到这里")
    s.check("the first prompt is not a name", code == 3)
    record = record_of(binary, env, sid)
    s.check("the record keeps the short name after the first prompt", record is not None and record.get("name") == name)

    m = s.mark()
    s.line(":session")
    s.check(":session shows id and name",
            s.until(r"session id: " + re.escape(sid) + r" · 会话 " + re.escape(name), m))

    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit summary names the session",
            s.until(r"会话 " + re.escape(name) + r" · 续接：suga chat --resume " + re.escape(sid) + r"\r?\n", m))
    return sid, name


def resume(binary, env, ws, sid, name):
    """A resumed session registers under the name it was first given."""
    s = PtySession([binary, "chat", "--resume", sid], env=env, cwd=ws)
    try:
        if not name:
            s.check("resume has a short name to look for", False)
            return s
        m = s.mark()
        s.check("resume shows the same short name", s.until(r"Suga chat · fake/m1[^\n]*会话 " + re.escape(name), m))
        # Wait for the prompt (after the resumed reply preview): text typed ahead
        # of the line editor's raw mode keeps its characters but loses the Enter.
        s.check("resume: ❯ prompt", s.until(r"❯ ", m))
        m = s.mark()
        s.line(":quit")
        s.check("resume exit summary keeps the name",
                s.until(r"会话 " + re.escape(name) + r" · 续接：suga chat --resume " + re.escape(sid) + r"\r?\n", m))
    finally:
        s.close(grace=0)
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-shortname", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18802)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    # A directory name with an underscore: the short name must be cleaned ("smokews").
    ws = os.path.abspath(os.path.join(args.work, "Smoke_WS"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--delay", "0.3",
                             "--reply", REPLY],
                            stdout=subprocess.PIPE, text=True)
    s, resumed = None, None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        sid, name = scenario(s, binary, env)
        s.close()
        if sid and name:
            resumed = resume(binary, env, ws, sid, name)
            code, _ = run_send(binary, env, name, "太晚了")
            s.check("a closed chat is no longer at that address", code == 3)
    finally:
        if s is not None:
            s.close(grace=0)
        if resumed is not None:
            resumed.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    results = (s.results if s else []) + (resumed.results if resumed else [])
    failed = [n for n, ok in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
