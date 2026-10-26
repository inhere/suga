#!/usr/bin/env python3
"""End-to-end smoke test: deferred native tools load inside a segment, run
through the tool_call proxy, and join the tool table at a segment boundary
(Linux/macOS).

Starts scripts/ptysmoke/fakellm.py (deferred mode: tool_search select:memory ->
tool_call memory) and `suga chat` in a PTY with `tools.deferred_native: auto`,
asks one question, runs `:compact` (a segment boundary) and a second question,
exits, then checks:

  * the requests the fake model saw: the first one carries no `memory` tool but
    carries `tool_search` and `tool_call`, and its segment context lists the
    deferred native tools (`- memory —`); after the compaction the tool table
    contains `memory` again;
  * `suga inspect trace --json`: the context event reports deferred_native > 0,
    the tool_search event loaded memory with cache_invalidated=false and
    newly_loaded_kinds {"native":1}, and the tool event is named `memory` (the
    proxy call was expanded before approval and trace).

  python3 -I scripts/ptysmoke/chat_deferred_native.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to
tmp/smoke-deferred.
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
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402

DEFERRED_TOOLS = "  deferred_native: auto\n"


def fake_requests(path):
    """The request log lines the fake model wrote (one JSON object each)."""
    out = []
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    out.append(json.loads(line))
                except ValueError:
                    pass
    except FileNotFoundError:
        pass
    return out


def scenario(s):
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark()
    s.line("找一下记忆里的 smoke 规则")
    s.check("first answer", s.until(r"SMOKE_ANSWER_ONE", m) and s.until(r"❯ ", m))
    m = s.mark()
    s.line(":compact")
    s.check("compacted", s.until(r"❯ ", m))
    m = s.mark()
    s.line("再说一次")
    s.check("second answer", s.until(r"SMOKE_ANSWER_ONE", m))
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


def check_requests(s, requests):
    # The log starts with fakellm's {"listening": port} line: the first request
    # is the first entry with a path.
    reqs = [r for r in requests if r.get("path")]
    first = reqs[0] if reqs else {}
    tools = first.get("tools") or []
    s.check("request 1: memory is not in the tool table", "memory" not in tools)
    s.check("request 1: tool_search is registered", "tool_search" in tools)
    s.check("request 1: tool_call is registered", "tool_call" in tools)
    seen = str(first.get("deferred") or "")
    s.check("request 1: segment context lists the deferred tools", "延迟加载的工具" in seen)
    s.check("request 1: memory is listed with its purpose", "- memory —" in seen)

    later = [r for r in requests if "memory" in (r.get("tools") or [])]
    s.check("after the segment boundary the table contains memory", bool(later))


def check_trace(s, events):
    ctx = next((e for e in events if e.get("kind") == "context"), {})
    attrs = ctx.get("attrs") or {}
    s.check("trace: context reports deferred_native > 0", attrs.get("deferred_native", 0) > 0)
    s.check("trace: context reports a deferred listing", attrs.get("deferred_tools", 0) > 0)

    search = next((e for e in events if e.get("kind") == "tool_search"
                   and "memory" in ((e.get("attrs") or {}).get("newly_loaded") or [])), {})
    sattrs = search.get("attrs") or {}
    s.check("trace: tool_search loaded memory", bool(search))
    s.check("trace: loading inside a segment invalidates no cache", sattrs.get("cache_invalidated") is False)
    s.check("trace: tool_search reports the native kind",
            (sattrs.get("newly_loaded_kinds") or {}).get("native") == 1)

    names = [e.get("name") for e in events if e.get("kind") == "tool"]
    s.check("trace: the proxied call runs as memory", "memory" in names)
    s.check("trace: the proxy never shows up as the executed tool", "tool_call" not in names)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-deferred", help="scratch dir (SUGA_HOME and workspace)")
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    p.add_argument("--keep", action="store_true", help="keep the scratch home after the run")
    args = p.parse_args()
    args.port = resolve_port(args.port)

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    # Start every run from an empty work dir and SUGA_HOME: a leftover config or
    # session changes what this scenario sees.
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    os.makedirs(home, exist_ok=True)
    isolate_ws(ws)
    write_home(home, args.port, tools_extra=DEFERRED_TOOLS)
    env = dict(os.environ, SUGA_HOME=home, TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    env.pop("SUGA_HOOKS", None)

    log_path = os.path.join(work, "fake.jsonl")
    log_file = open(log_path, "w", encoding="utf-8")
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "deferred", "--delay", "0.4",
                             "--reply", "SMOKE_ANSWER_ONE"],
                            stdout=log_file, text=True)
    s = None
    try:
        # fakellm writes {"listening": port} to the log file (stdout goes there,
        # not to a pipe), so wait for that line in the file.
        deadline = time.time() + 15
        while time.time() < deadline:
            with open(log_path, encoding="utf-8") as f:
                if '"listening"' in f.read():
                    break
            time.sleep(0.1)
        else:
            raise SystemExit("fake model did not start")
        s = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(s)
        s.close()
        s.check("session id", bool(session))
        check_requests(s, fake_requests(log_path))
        if session:
            check_trace(s, trace_events(binary, env, session))
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        log_file.close()
        if not args.keep:
            shutil.rmtree(home, ignore_errors=True)
    failed = s.failed() if s else ["setup"]
    print(f"\n{len(s.results) - len(failed) if s else 0}/{len(s.results) if s else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
