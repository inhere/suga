#!/usr/bin/env python3
"""End-to-end smoke test of picture attachments in `suga chat`: Alt+V and :image
(image input design 0.3, Linux/macOS only — a PTY is required).

Starts scripts/ptysmoke/fakellm.py (text mode), writes a temporary SUGA_HOME and
runs `suga chat` twice on the same workspace:

  1. vision off (model fake/m1, unknown to the built-in vision list): Alt+V and
     :image <path> are REFUSED with a :model hint, no pending note appears and
     no turn starts;
  2. vision on (model fake/gpt-4o): Alt+V reads the clipboard's picture through
     the fake reader (SUGA_FAKE_CLIPBOARD_IMAGE names a test file — the test
     hook internal/clipboardimage carries) and :image <path> reads a workspace
     file; both land on one queue ("[已附图 N：宽×高 格式]" notes above the
     input line), a plain message rides them to the model, and the store ends
     up with both files under sessions/<sid>/images/ (AppendMessages writes
     them; the model request holds them by reference).

After chat exits the session's trace is read with `suga trace <sid> --json` so
the turn exists and its user message text is what was typed with pictures.

Reverse verification (how this scenario is meant to fail without the feature):
  * without the Alt+V binding the ESC v bytes type into the input line and the
    "[已附图" note never appears (the vision-on note check fails);
  * without the :image wiring the command is an ordinary message — the refusal
    texts (vision off) and the note (vision on) disappear and the model receives
    a reply for the command text itself instead of quiet;
  * without the store's image persistence the session's images/ directory stays
    empty — the file checks fail;
  * without the vision gate refusal, chat_images vision-off part sees an
    "[已附图" note and an images file even though the model cannot see — the
    refusal checks fail.

  python3 -I scripts/ptysmoke/chat_images.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-images.
"""
import argparse
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import time
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port  # noqa: E402

REPLY = "完成"


def make_png(path, px, rgb):
    """Writes a compact opaque PNG of px×px; keep the two must not share sha256."""
    row = b"\x00" + bytes(rgb) * px
    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    head = struct.pack(">IIBBBBB", px, px, 8, 2, 0, 0, 0)
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", head) + chunk(b"IDAT", zlib.compress(row * px)) + chunk(b"IEND", b""))


def write_home(home, port, model):
    """SUGA_HOME/config.yaml: the fake model on HTTP, project memory on files
    (the scratch workspace lives inside this repository, see chat_paste.py)."""
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


class FakeLog:
    """The JSON lines fakellm writes on stdout (see chat_paste.py)."""

    def __init__(self, proc):
        self.proc = proc
        self.entries = []

    def drain(self):
        got, _ = self.entries[:], None
        self.entries.clear()
        return got

    def wait_user(self, want, timeout=10.0):
        """Waits until a model request's last user message contains want."""
        import select
        end = time.time() + timeout
        while time.time() < end:
            ready, _, _ = select.select([self.proc.stdout], [], [], 0.3)
            if ready:
                line = self.proc.stdout.readline()
                if line:
                    try:
                        self.entries.append(json.loads(line))
                    except ValueError:
                        pass
            if any(want in str(e.get("user", "")) for e in self.entries):
                return True
        return any(want in str(e.get("user", "")) for e in self.entries)


def alt_v(s):
    """Sends Alt+V (ESC v) as one chunk, like the terminal would."""
    s.send("\x1bv")


def vision_off_scenario(s, ws, log):
    """A model the built-in vision list does not know: refusals, no pictures."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    alt_v(s)
    s.check("alt+v refused with :model hint", s.until(r"不支持看图", m) and s.until(":model", m))
    s.drain(0.3)
    m = s.mark()
    s.line(":image " + os.path.join(ws, "shot.png"))
    s.check(":image refused with :model hint", s.until(r"不支持看图", m) and s.until(":model", m))
    s.drain(0.3)
    s.check("refusals started no turn", log.drain() == [])


def vision_on_scenario(s, ws, log):
    """Both attach paths, one queue, both files land in the session directory."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    alt_v(s)
    s.check("alt+v shows the attachment note", s.until(r"已附图 1：", m))
    s.drain(0.3)
    s.check("alt+v started no turn", log.drain() == [])
    m = s.mark()
    s.line(":image " + os.path.join(ws, "shot.png"))
    s.check(":image appends as the second picture", s.until(r"已附图 2：", m))
    s.drain(0.3)
    # The queue answers :image and :image clear, then a normal message.
    m = s.mark()
    s.line(":image")
    s.check("the list shows both", s.until(r"已附图 1：", m) and s.until(r"已附图 2：", m))
    s.drain(0.3)
    m = s.mark()
    s.line(":image clear")
    s.check("clear says so", s.until(r"已清空待发图片", m))
    s.drain(0.3)
    # The last Alt+V (for the no-picture check below) then attachment again to
    # carry into the turn.
    m = s.mark()
    alt_v(s)
    s.check("re-attach after clear", s.until(r"已附图 1：", m))
    s.drain(0.3)
    m = s.mark()
    s.line(":image " + os.path.join(ws, "shot.png"))
    s.check(":image appends as the second picture again", s.until(r"已附图 2：", m))
    s.drain(0.3)
    m = s.mark()
    s.line("看图说说这两张")
    s.check("the turn answers", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    s.check("the model received the message text", log.wait_user("看图说说"))
    # The pictures ride in the same user message as image parts (OpenAI chat
    # shape: content is a list holding image_url parts), not only in the store.
    sent = [e for e in log.entries if "看图说说" in str(e.get("user", ""))]
    s.check("the model request carries both pictures as image parts",
            bool(sent) and str(sent[-1].get("user")).count("image_url") >= 2)


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
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True, text=True, env=env, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return events


def assert_images(home, sid, minimum):
    """The image files the turn's AppendMessages wrote (or nothing: files empty)."""
    d = os.path.join(home, "sessions", sid, "images")
    if not os.path.isdir(d):
        return []
    return os.listdir(d)


def run_chat(binary, env, ws, scenario, log):
    s = PtySession([binary, "chat"], env=env, cwd=ws)
    scenario(s, ws, log)
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-images", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18812)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    make_png(os.path.join(ws, "shot.png"), 3, (10, 20, 30))
    clip = os.path.join(work, "clipboard.png")
    make_png(clip, 5, (200, 150, 100))  # different bytes, a second sha/file

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--reply", REPLY,
                             "--delay", "0.1"],
                            stdout=subprocess.PIPE, text=True)
    results_total = [0, 0]  # passed, total
    try:
        wait_port(fake)
        log = FakeLog(fake)
        base_env = dict(os.environ, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1",
                        SUGA_FAKE_CLIPBOARD_IMAGE=clip)
        base_env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

        # 1. round: refusals.
        write_home(home, args.port, "fake/m1")
        s = None
        sid = ""
        try:
            s = run_chat(binary, dict(base_env, SUGA_HOME=home), ws, vision_off_scenario, log)
            sid = exit_and_session(s)
        finally:
            if s is not None:
                s.close(grace=0)
        s.check("vision off: no picture reached the store", not assert_images(home, sid, 0))
        results_total[0] += len(s.results) - len(s.failed())
        results_total[1] += len(s.results)

        # 2. round: attachments ride.
        write_home(home, args.port, "fake/gpt-4o")
        s = None
        sid = ""
        try:
            s = run_chat(binary, dict(base_env, SUGA_HOME=home), ws, vision_on_scenario, log)
            sid = exit_and_session(s)
        finally:
            if s is not None:
                s.close(grace=0)
        # The store kept the pictures: the clipboard one and the :image one are
        # different bytes, so exactly two files (same-bytes attaches dedup).
        files = assert_images(home, sid, 2)
        s.check("vision on: both pictures in sessions/<sid>/images", len(files) == 2)
        s.check("trace: the turn is recorded", any(e.get("kind") for e in trace_events(binary, dict(base_env, SUGA_HOME=home), sid)))
        results_total[0] += len(s.results) - len(s.failed())
        results_total[1] += len(s.results)
    finally:
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    if results_total[0] < results_total[1]:
        print(f"\nfatal: {results_total[0]}/{results_total[1]} passed")
        sys.exit(1)


if __name__ == "__main__":
    main()
