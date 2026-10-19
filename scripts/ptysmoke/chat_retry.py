#!/usr/bin/env python3
"""End-to-end smoke test: chat shows model retries and the trace counts them
(Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (text mode, --fail-first 1 --overload-first 1
--break-first 1: an HTTP 503, then a stream that carries only an overload error,
then a stream that breaks after half the reply, then the whole reply) and
`suga chat` in a PTY with short retry waits, sends one prompt and checks: a
retry notice for the 503, a retry notice for the overload ("服务端过载/限流"),
a restart notice for the broken stream, the whole reply after them, and the
session's model event in `suga inspect trace --json` with retries=3.

  python3 -I scripts/ptysmoke/chat_retry.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-retry.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from chat_observe import trace_events  # noqa: E402

REPLY = "SMOKE_RETRY_REPLY"
RETRY_CONFIG = "retry:\n  base_delay: 200ms\n  max_delay: 400ms\n"


def scenario(s):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("你好")
    s.check("notice: HTTP 503 retry", s.until(r"↻ 模型请求失败（HTTP 503）.*第 1 次", m))
    # The overload arrived as an error event inside the stream: the turn is
    # requested again instead of failing, and nothing was streamed yet, so the
    # notice carries no "将重新输出".
    s.check("notice: stream overload retry", s.until(r"↻ 模型请求失败（服务端过载/限流）.*第 1 次", m))
    s.check("notice: broken stream restarts", s.until(r"↻ 模型请求失败（stream broken）.*将重新输出", m))
    s.check("whole reply after the restart", s.until(r"▣ " + REPLY, m) and s.until(r"tokens · ", m))
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume sess_", m))
    found = re.search(r"--resume (sess_\S+)", s.text(m))
    return found.group(1) if found else ""


def check_trace(s, events):
    model = next((e for e in events if e.get("kind") == "model"), {})
    attrs = model.get("attrs") or {}
    s.check("trace: model event counts 3 retries", attrs.get("retries") == 3)
    s.check("trace: last retry reason", attrs.get("retry_reason") == "stream broken")
    s.check("trace: retry wait recorded", attrs.get("retry_wait_ms", 0) > 0)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-retry", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=18798)
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    home = os.path.abspath(os.path.join(args.work, "home"))
    ws = os.path.abspath(os.path.join(args.work, "ws"))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, extra=RETRY_CONFIG)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "text", "--delay", "0.2",
                             "--reply", REPLY, "--fail-first", "1", "--overload-first", "1", "--break-first", "1"],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s)
        s.close()
        s.check("session id", bool(session))
        if session:
            check_trace(s, trace_events(binary, env, session))
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
