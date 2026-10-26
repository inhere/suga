#!/usr/bin/env python3
"""End-to-end smoke test of `suga send --image`: a picture delivered into a
running chat session (send --image design, Linux/macOS only — a PTY is required).

Starts scripts/ptysmoke/fakellm.py (text mode), writes a temporary SUGA_HOME and
runs `suga chat` on a scratch workspace. While chat is idle at its prompt the
scenario runs `suga send --session <session id> --image shot.png 看看这张` as its
own process, the way a Gofer-style caller does:

  * the sender exits 0 and says the message was delivered;
  * the model's request carries the message text with an `image_url` part (the
    sender wrote the file into the session's images directory, the receiver
    checked it and the message holds the reference);
  * sessions/<sid>/images/ holds exactly one file, named after the sha256 of the
    picture, so the delivery went through the storage layer;

A second `suga send --image` with the same picture and a chat whose model cannot
see (fake/m1) are not covered here: the vision warning and the content-addressed
write are unit-tested in internal/cli/send_image_test.go and
internal/chat/delivery_images_test.go, and a PTY cannot show the sender's stderr
without a second terminal.

Reverse verification (how this scenario is meant to fail without the feature),
to run once in a Linux container:
  * without the receiver's FeatureImages declaration the sender exits 5 and prints
    "不支持附图" — the exit-code check fails and no request carries image_url;
  * without verifyDeliveryImages the receiver still takes a forged reference, so
    the picture never reaches the model (no image_url) or the session's images
    file check fails;
  * without chatInputSource.DeliverImages the message arrives without its
    pictures: the request has no image_url part, and the images/ directory stays
    as the sender left it (the same file, so the file count check still passes -
    the image_url check is the one that catches it).

  python3 -I scripts/ptysmoke/chat_send_image.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-send-image.
"""
import argparse
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import time
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port  # noqa: E402
from chat_observe import trace_events  # noqa: E402

REPLY = "完成"
TEXT = "看看这张"


def write_home(home, port, model):
    """SUGA_HOME/config.yaml: the fake model on HTTP, project memory on files
    (the scratch workspace lives inside this repository, see chat_images.py),
    and the Chinese UI the checks assert."""
    os.makedirs(home, exist_ok=True)
    with open(os.path.join(home, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(f"""language: zh
model: {model}
providers:
  fake:
    type: openai
    base_url: http://127.0.0.1:{port}/v1
    api_key: k
memory:
  project:
    backend: files
""")
    # An empty .env marks the home as already set up (chat_approval.write_home).
    open(os.path.join(home, ".env"), "w", encoding="utf-8").close()


def make_png(path, px, rgb):
    """Writes a compact opaque PNG of px×px (the picture the sender delivers)."""
    row = b"\x00" + bytes(rgb) * px

    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    head = struct.pack(">IIBBBBB", px, px, 8, 2, 0, 0, 0)
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", head) + chunk(b"IDAT", zlib.compress(row * px)) + chunk(b"IEND", b""))


class FakeLog:
    """The JSON lines fakellm writes on stdout: one per model request.

    "user" is the raw JSON of the last user message, so a request that carried a
    picture shows its `image_url` part there."""

    def __init__(self, proc):
        self.proc = proc
        self.entries = []

    def drain(self, seconds=0.3):
        import select

        end = time.time() + seconds
        while time.time() < end:
            ready, _, _ = select.select([self.proc.stdout], [], [], 0.1)
            if ready:
                line = self.proc.stdout.readline()
                if line:
                    try:
                        self.entries.append(json.loads(line))
                    except ValueError:
                        pass
        got = self.entries[:]
        self.entries.clear()
        return got

    def wait_image(self, text, timeout=20.0):
        """Waits until a request's last user message holds text and an image part."""
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
            if any(text in str(e.get("user", "")) and "image_url" in str(e.get("user", "")) for e in self.entries):
                return True
        return False


def session_id_of(s):
    """The session id chat printed at startup: what `suga send --session` takes."""
    import re

    found = re.search(r"session id: ((?:sess_|sg_|s_)\S+)", s.text())
    return found.group(1) if found else ""


def send_image(binary, env, ws, session, picture, text):
    """Runs `suga send --session <session> --image <picture> <text>` as its own
    process: (exit code, stderr)."""
    proc = subprocess.run([binary, "send", "--session", session, "--image", picture, text],
                          env=env, cwd=ws, capture_output=True, text=True, timeout=120)
    return proc.returncode, proc.stderr.strip()


def images_of(home, sid):
    """The files the delivery left in the session's images directory."""
    d = os.path.join(home, "sessions", sid, "images")
    if not os.path.isdir(d):
        return []
    return sorted(os.listdir(d))


def exit_chat(s):
    """Leaves chat with the double Ctrl-C and returns the session id."""
    import re

    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def scenario(s, home, binary, env, ws, log):
    """One idle chat receives one picture through `suga send --image`."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    sid = session_id_of(s)
    s.check("session id", bool(sid))
    if not sid:
        return sid

    code, stderr = send_image(binary, env, ws, sid, "shot.png", TEXT)
    s.check("the sender exits 0", code == 0)
    s.check("the sender says it was delivered", "delivered" in stderr or "queued" in stderr)
    s.check("no warning for a model that can see", "不支持看图" not in stderr)

    s.check("the model request carries the text with an image part", log.wait_image(TEXT))
    s.check("the turn answers", s.until(r"▣ " + REPLY, m, timeout=60))

    files = images_of(home, sid)
    s.check("the session's images directory holds one picture", len(files) == 1)
    # The store names every file after the sha256 of the bytes it holds (the
    # sender scales the picture first, so this is checked against the stored
    # file itself rather than against the source).
    stored = files[0] if files else ""
    path = os.path.join(home, "sessions", sid, "images", stored) if stored else ""
    digest = hashlib.sha256(open(path, "rb").read()).hexdigest() if stored else ""
    # The sender may re-encode while scaling (PNG -> JPEG), so only the name
    # before the extension is the content's sha256.
    s.check("the file is named after its content", os.path.splitext(stored)[0] == digest and digest != "")
    return sid


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-send-image", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    make_png(os.path.join(ws, "shot.png"), 6, (200, 60, 30))

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--reply", REPLY,
                             "--delay", "0.1"],
                            stdout=subprocess.PIPE, text=True)
    passed, total = 0, 0
    try:
        wait_port(fake)
        log = FakeLog(fake)
        env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
        env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
        # A model the built-in vision list knows, so the delivery is not degraded.
        write_home(home, args.port, "fake/gpt-4o")
        s = None
        sid = ""
        try:
            s = PtySession([binary, "chat"], env=env, cwd=ws)
            sid = scenario(s, home, binary, env, ws, log)
            exit_chat(s)
        finally:
            if s is not None:
                s.close(grace=0)
        s.check("trace: the turn is recorded",
                any(e.get("kind") == "turn" for e in trace_events(binary, env, sid)))
        passed += len(s.results) - len(s.failed())
        total += len(s.results)
    finally:
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    if passed < total:
        print(f"\nfatal: {passed}/{total} passed")
        sys.exit(1)


if __name__ == "__main__":
    main()
