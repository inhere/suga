#!/usr/bin/env python3
"""End-to-end smoke test for preferred command/MCP discovery (Linux/macOS).

The scenario drives the real ``suga chat`` binary with fakellm.py and a tiny
stdio MCP server.  It checks that the environment lists the found command and
connected preferred MCP server but not the missing command, that a preferred
MCP tool is directly visible with ``tools.deferred: always`` (no tool_search),
and that the ordinary approval question still appears.  It then checks the
turn_context trace contains only the names that were listed.

A second group runs the same configuration read-only (``suga run --read-only``):
without shell_exec the environment must keep the connected MCP server and drop
the command, which the model could not run there.

Reverse verification (drop one behavior and the corresponding check fails):
- remove the present-only prompt filter: the missing-command check fails;
- remove the shell_exec gate of command declarations: the read-only run lists
  the found command and that check fails;
- remove Visibility's pinned MCP set: the fake model request omits the MCP
  schema and the MCP server receives no tools/call;
- remove turn_context's preferred_tools attribute: the trace check fails.

The standalone manifest check also confirms that it reports command presence
and configured MCP status without starting the MCP process or leaking its
configured secret.

  python3 -I scripts/ptysmoke/preferred_tools.py --bin tmp/smoke/suga
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

FOUND_NAME = "preferred-smoke-command"
MISSING_NAME = "suga-missing-preferred-command"
MCP_NAME = "preferred-mcp"
MCP_TOOL = "preferred_lookup"
MCP_SECRET = "SMOKE_MCP_SECRET"
MCP_OK = "PREFERRED_MCP_OK"
# READONLY_PROMPT identifies the read-only run's request in the fake model's log.
READONLY_PROMPT = "只读优先项"

MCP_SERVER = r'''import json
import sys

log_path = sys.argv[1]

def log(method):
    with open(log_path, "a", encoding="utf-8") as stream:
        stream.write(method + "\n")

for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method", "")
    log(method)
    if method == "initialize":
        result = {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}},
                  "serverInfo": {"name": "preferred-smoke", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": "preferred_lookup",
                              "description": "preferred smoke lookup",
                              "inputSchema": {"type": "object",
                                              "properties": {"query": {"type": "string"}}}}]}
    elif method == "tools/call":
        result = {"content": [{"type": "text", "text": "PREFERRED_MCP_OK"}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request.get("id"), "result": result}), flush=True)
'''


def yaml_string(value):
    """Return a JSON string literal, which is also a valid YAML scalar."""
    return json.dumps(value)


def trace_events(binary, env, cwd, session):
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return out, events


def manifest_checks(binary, env, cwd):
    """Run the no-runtime manifest before the MCP server is allowed to start."""
    out = subprocess.run([binary, "inspect", "manifest", "--json"], capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    checks = [("manifest command succeeds", out.returncode == 0)]
    try:
        manifest = json.loads(out.stdout)
    except ValueError:
        manifest = {}
        checks.append(("manifest is JSON", False))
    preferred = {item.get("name"): item for item in manifest.get("preferred_tools", [])}
    checks.extend([
        ("manifest found command", preferred.get(FOUND_NAME, {}).get("present") is True
         and preferred.get(FOUND_NAME, {}).get("reason")
         == "found on PATH; read-only sessions do not list it"),
        ("manifest keeps missing command reason", preferred.get(MISSING_NAME, {}).get("present") is False
         and preferred.get(MISSING_NAME, {}).get("reason") == "not found on PATH"),
        ("manifest reports configured MCP without starting it",
         preferred.get(MCP_NAME, {}).get("present") is False
         and preferred.get(MCP_NAME, {}).get("reason") == "configured; connection confirmed at session start"),
        ("manifest does not leak MCP secret", MCP_SECRET not in out.stdout),
    ])
    return checks


def scenario(s):
    m = s.mark()
    s.check("banner", s.until(r"Suga chat · fake/m1", m))
    s.check("prompt", s.until(r"❯ ", m))

    m = s.mark()
    s.line("使用优先工具")
    # What the model was told is checked from the fake model's request log in
    # main(): the environment section is sent to the model, not shown here.
    s.check("missing command is not shown", not s.contains(MISSING_NAME, m))
    # Preferred is not permission: chat and run deny MCP tools that are not in
    # tools.auto_allow (the existing rule), so the call is refused, not run.
    s.check("preferred MCP is not auto-allowed", s.until(r"需要许可：preferred_lookup", m))
    s.check("turn ends", s.until(r"tokens · ", m))

    m = s.mark()
    time.sleep(0.3)
    s.send(CTRL_C)
    time.sleep(0.3)
    s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--bin", default="tmp/smoke/suga")
    parser.add_argument("--work", default="tmp/smoke-preferred-tools")
    parser.add_argument("--port", type=int, default=18817)
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()

    binary = os.path.abspath(args.bin)
    work = os.path.abspath(args.work)
    home = os.path.join(work, "home")
    ws = os.path.join(work, "ws")
    command_dir = os.path.join(work, "bin")
    command_path = os.path.join(command_dir, FOUND_NAME)
    mcp_script = os.path.join(home, "mcp_server.py")
    mcp_log = os.path.join(home, "mcp.log")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(command_dir, exist_ok=True)
    os.makedirs(home, exist_ok=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    with open(command_path, "w", encoding="utf-8") as stream:
        stream.write("#!/bin/sh\nexit 0\n")
    os.chmod(command_path, 0o755)
    with open(mcp_script, "w", encoding="utf-8") as stream:
        stream.write(MCP_SERVER)

    preferred = f"""  preferred:
    - name: {FOUND_NAME}
      kind: command
      when: 搜索文本
    - name: {MISSING_NAME}
      kind: command
      when: 不应出现
    - name: {MCP_NAME}
      kind: mcp
      when: 代码图谱
"""
    mcp = f"""mcp:
  servers:
    - id: {MCP_NAME}
      command: {yaml_string(sys.executable)}
      args:
        - -I
        - {yaml_string(mcp_script)}
        - {yaml_string(mcp_log)}
      env:
        SMOKE_SECRET: {MCP_SECRET}
"""
    # deferred: always would put every MCP tool behind tool_search; the
    # preferred server's tools must stay direct anyway. (A tiny context window
    # was used before to force deferral, but it made the turn compact first.)
    write_home(home, args.port, tools_extra="  deferred: always\n" + preferred,
               extra=mcp)
    env = dict(os.environ, SUGA_HOME=home, HOME=home, SUGA_HOOKS="off", TERM="xterm",
               NO_COLOR="1")
    env["PATH"] = command_dir + os.pathsep + env.get("PATH", "")
    env.pop("SUGA_CONFIG", None)

    checks = manifest_checks(binary, env, ws)
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([
        sys.executable, "-I", os.path.join(here, "fakellm.py"),
        "--port", str(args.port), "--mode", "preferred", "--delay", "0.3",
        "--preferred-tool", MCP_TOOL, "--reply", "优先工具完成"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    session = None
    pty = None
    try:
        wait_port(fake)
        pty = PtySession([binary, "chat"], env=env, cwd=ws)
        session = scenario(pty)
        pty.close()
        pty.check("session id", bool(session))

        if session:
            trace, events = trace_events(binary, env, ws, session)
            pty.check("trace command succeeds", trace.returncode == 0)
            turns = [e for e in events if e.get("kind") == "turn_context"
                     or e.get("name") == "turn_context"]
            attrs = (turns[0].get("attrs") or {}) if turns else {}
            pty.check("trace: preferred names match prompt",
                      attrs.get("preferred_tools") == [FOUND_NAME, MCP_NAME])
            pty.check("trace: preferred observation is names only",
                      "搜索文本" not in trace.stdout and "SMOKE_SECRET" not in trace.stdout
                      and MISSING_NAME not in attrs.get("preferred_tools", [])
                      and "preferred_count" not in attrs and "preferred_hash" not in attrs)

        for name, ok in checks:
            pty.check(name, ok)

        if os.path.exists(mcp_log):
            with open(mcp_log, encoding="utf-8") as stream:
                methods = [line.strip() for line in stream if line.strip()]
        else:
            methods = []
        pty.check("MCP server was not started by manifest", methods.count("initialize") == 1)
        pty.check("denied MCP call never reached the server", "tools/call" not in methods)

        # Read-only group: the same configuration without shell_exec. The fake
        # model's requests are inspected below, after it was stopped.
        readonly = subprocess.run([binary, "run", "--read-only", "--prompt", READONLY_PROMPT],
                                  capture_output=True, encoding="utf-8", errors="replace",
                                  env=env, cwd=ws, timeout=120)
        pty.check("read-only run exits 0", readonly.returncode == 0)

        fake.terminate()
        fake_output, _ = fake.communicate(timeout=5)
        requests = []
        for line in fake_output.splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("path") == "/v1/chat/completions":
                requests.append(row)
        first_tools = requests[0].get("tools", []) if requests else []
        listed = " ".join(requests[0].get("preferred", [])) if requests else ""
        pty.check("found command is listed",
                  "Preferred tool: preferred-smoke-command (command) — 搜索文本" in listed)
        pty.check("preferred MCP is listed", "Preferred tool: preferred-mcp (mcp) — 代码图谱" in listed)
        pty.check("missing command is not listed", MISSING_NAME not in listed)
        pty.check("fixed help and memory hints are listed", "--help" in listed and "memory" in listed)
        pty.check("fake model saw preferred MCP schema directly", MCP_TOOL in first_tools)
        pty.check("preferred MCP did not require tool_search", "tool_search" not in first_tools)

        # The read-only run's own request: no shell_exec there, so the
        # environment keeps the connected MCP server and drops the command.
        readonly_requests = [row for row in requests if row.get("user") == READONLY_PROMPT]
        readonly_listed = " ".join(readonly_requests[-1].get("preferred", [])) if readonly_requests else ""
        pty.check("read-only run still lists the preferred MCP server",
                  "Preferred tool: preferred-mcp (mcp) — 代码图谱" in readonly_listed)
        pty.check("read-only run leaves the preferred command out",
                  FOUND_NAME not in readonly_listed and MISSING_NAME not in readonly_listed)
    finally:
        if pty is not None:
            pty.close(grace=0)
        fake.terminate()
        try:
            fake.wait(timeout=5)
        except subprocess.TimeoutExpired:
            fake.kill()
            fake.wait(timeout=5)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)

    failed = pty.failed() if pty else ["setup"]
    print(f"\n{len(pty.results) - len(failed) if pty else 0}/{len(pty.results) if pty else 0} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
