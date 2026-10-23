#!/usr/bin/env python3
"""End-to-end smoke test of file_read reading a picture in `suga chat`
(file_read images design 2026-10-23, Linux/macOS only — a PTY is required).

Starts scripts/ptysmoke/fakellm.py (image mode: one file_read of shot.png, then
a text answer) and runs `suga chat` twice on the same workspace:

  1. vision on (model fake/gpt-4o): the tool line names file_read and the
     picture; the request the model receives carries the picture as an image
     part ("attached as an image part" in the tool text); the trace has a
     file_read tool event with images=1 and image_bytes>0 (Q8); the session's
     images/ directory holds the stored picture.
  2. vision off (model fake/m1, unknown to the built-in vision list): the same
     call answers with the note ("cannot see images", ":model"), the trace
     event has no images attribute and nothing is stored.

Reverse verification (how this scenario is meant to fail without the feature):
  * without the picture branch in file_read the call answers "[binary file:
    shot.png, ...]" — the "attached as an image part" and trace images checks
    fail and no file lands in images/;
  * with the "cannot see" answer turned back into an error the vision-off
    round's note check fails (the tool line shows an error instead);
  * without the trace attributes (addOutputAttrs images/image_bytes) the trace
    checks fail;
  * with view_image put back in the tool table the tool-line check would name
    view_image instead of file_read (fakellm asks for file_read, so the call
    would simply fail).

  python3 -I scripts/ptysmoke/chat_read_image.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-read-image.
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
from chat_approval import isolate_ws, wait_port  # noqa: E402
from chat_images import FakeLog, make_png  # noqa: E402

REPLY = "看图完成"


def write_home(home, port, model):
    """SUGA_HOME/config.yaml: the fake model on HTTP, files-backed memory."""
    os.makedirs(home, exist_ok=True)
    with open(os.path.join(home, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(f"""model: {model}
providers:
  fake:
    type: openai
    base_url: http://127.0.0.1:{port}/v1
    api_key: k
memory:
  project:
    backend: files
""")


def scenario(s, reply, log):
    """One turn: ask for the picture, wait for the tool line and the answer."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("读一下这张图说说是什么")
    s.check("tool line names file_read and the picture", s.until(r"⏺ file_read shot\.png", m))
    s.check("the turn answers", s.until(r"▣ " + reply, m) and s.until(r"tokens · ", m))
    s.check("the model received the message text", log.wait_user("读一下这张图"))


def pump(log, seconds=2.0):
    """Reads the fakellm lines that arrived after wait_user returned (the
    request carrying the tool result comes after the one with the user text).
    The pipe is read non-blocking: an earlier readline may already hold those
    lines in the text buffer, where select on the descriptor cannot see them."""
    import os as _os
    time.sleep(seconds)
    fd = log.proc.stdout.fileno()
    _os.set_blocking(fd, False)
    try:
        while True:
            try:
                line = log.proc.stdout.readline()
            except (BlockingIOError, TypeError):
                break
            if not line:
                break
            try:
                log.entries.append(json.loads(line))
            except ValueError:
                pass
    finally:
        _os.set_blocking(fd, True)


def exit_and_session(s):
    """Leaves chat with the double Ctrl-C and returns the session id."""
    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def trace_events(binary, env, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"],
                         capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def stored_images(home, sid):
    """The image files the turn's AppendMessages wrote for one session."""
    d = os.path.join(home, "sessions", sid, "images")
    if not os.path.isdir(d):
        return []
    return os.listdir(d)


def tool_events(events):
    return [e for e in events if e.get("kind") == "tool" and e.get("name") == "file_read"]


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-read-image", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18821)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    # Start from nothing: a file of an earlier run would change this one's checks.
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    os.makedirs(home, exist_ok=True)
    isolate_ws(ws)
    make_png(os.path.join(ws, "shot.png"), 4, (30, 120, 200))

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "image", "--read-path", "shot.png",
                             "--reply", REPLY, "--delay", "0.1"],
                            stdout=subprocess.PIPE, text=True)
    passed, total = 0, 0
    try:
        wait_port(fake)
        log = FakeLog(fake)
        base_env = dict(os.environ, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
        base_env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

        # 1. round: a model that can see gets the picture.
        write_home(home, args.port, "fake/gpt-4o")
        s = None
        sid = ""
        try:
            s = PtySession([binary, "chat"], env=dict(base_env, SUGA_HOME=home), cwd=ws)
            scenario(s, REPLY, log)
            sid = exit_and_session(s)
        finally:
            if s is not None:
                s.close(grace=0)
        pump(log)
        tool_text = " ".join(str(e.get("tool_text", "")) for e in log.entries)
        s.check("vision on: the tool text says the picture is attached",
                "attached as an image part" in tool_text)
        files = stored_images(home, sid)
        s.check("vision on: the picture is stored in sessions/<sid>/images", len(files) == 1)
        events = tool_events(trace_events(binary, dict(base_env, SUGA_HOME=home), sid)) if sid else []
        s.check("vision on: trace has a file_read event", len(events) >= 1)
        attrs = (events[-1].get("attrs") or {}) if events else {}
        s.check("vision on: the event counts the picture", attrs.get("images") == 1)
        s.check("vision on: the event counts its bytes", int(attrs.get("image_bytes") or 0) > 0)
        passed += len(s.results) - len(s.failed())
        total += len(s.results)

        # 2. round: a model without vision gets the note, nothing is stored.
        write_home(home, args.port, "fake/m1")
        s = None
        sid = ""
        try:
            s = PtySession([binary, "chat"], env=dict(base_env, SUGA_HOME=home), cwd=ws)
            scenario(s, REPLY, log)
            sid = exit_and_session(s)
        finally:
            if s is not None:
                s.close(grace=0)
        pump(log)
        tool_text = " ".join(str(e.get("tool_text", "")) for e in log.entries[-2:])
        s.check("vision off: the tool text is the note", "cannot see images" in tool_text)
        s.check("vision off: the note names the switch", ":model" in tool_text)
        s.check("vision off: no picture is stored", not stored_images(home, sid))
        events = tool_events(trace_events(binary, dict(base_env, SUGA_HOME=home), sid)) if sid else []
        s.check("vision off: trace has the file_read event", len(events) >= 1)
        attrs = (events[-1].get("attrs") or {}) if events else {}
        s.check("vision off: the event has no picture", not attrs.get("images"))
        passed += len(s.results) - len(s.failed())
        total += len(s.results)
    finally:
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    print(f"\n{passed}/{total} checks passed", flush=True)
    if passed < total:
        sys.exit(1)


if __name__ == "__main__":
    main()
