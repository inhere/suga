#!/usr/bin/env python3
"""Drive `suga acp` over stdio with a real model: a minimal ACP client.

It starts the suga binary as an ACP server in an isolated SUGA_HOME (the
sessions and traces land there, never in the real ~/.suga), opens one session
in --cwd and sends each --prompt in turn, allowing every permission request.
--wait N sleeps N seconds before the next prompt (background tasks keep
running). After the last prompt it closes the connection, then prints the
session's task events from `suga inspect trace --json`.

The real provider config: --config-from DIR uses DIR/config.yaml through
SUGA_CONFIG and reads DIR/.env into the server's environment (in memory; the
file is never copied).

  python3 -I scripts/acpprobe/acp_probe.py --bin tmp/acp/suga --cwd tmp/acp/ws \\
      --config-from ~/.suga --prompt "..." --wait 15 --prompt "..."

Prints a line per prompt (stop reason, answer text, tool calls) and exits 0
when every prompt ended with end_turn.
"""
import argparse
import base64
import json
import os
import shutil
import subprocess
import sys
import threading
import time


def read_dotenv(path):
    """KEY=VALUE lines of a .env file (no export, no interpolation)."""
    env = {}
    if not os.path.exists(path):
        return env
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            env[key.strip()] = value.strip().strip('"').strip("'")
    return env


class Client:
    """JSON-RPC over the server's stdin/stdout, one JSON object per line."""

    def __init__(self, proc, permission):
        self.proc = proc
        self.permission = permission
        self.permission_requests = 0
        self.next_id = 0
        self.pending = {}
        self.lock = threading.Lock()
        self.updates = []  # session/update params, in order
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def send(self, msg):
        data = json.dumps(msg, ensure_ascii=False) + "\n"
        self.proc.stdin.write(data.encode("utf-8"))
        self.proc.stdin.flush()

    def call(self, method, params, timeout=600):
        with self.lock:
            self.next_id += 1
            rid = self.next_id
            box = {"done": threading.Event()}
            self.pending[rid] = box
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        if not box["done"].wait(timeout):
            raise TimeoutError(f"{method} timed out after {timeout}s")
        if "error" in box:
            raise RuntimeError(f"{method}: {box['error']}")
        return box["result"]

    def read(self):
        for raw in self.proc.stdout:
            try:
                msg = json.loads(raw)
            except ValueError:
                continue
            if "method" in msg and "id" in msg:  # a request from the agent
                self.answer(msg)
            elif "method" in msg:  # a notification
                if msg["method"] == "session/update":
                    self.updates.append(msg["params"])
            else:
                with self.lock:
                    box = self.pending.pop(msg.get("id"), None)
                if box is not None:
                    if "error" in msg:
                        box["error"] = msg["error"]
                    box["result"] = msg.get("result")
                    box["done"].set()

    def answer(self, msg):
        if msg["method"] == "session/request_permission":
            options = msg["params"].get("options", [])
            self.permission_requests += 1
            wanted = {
                "allow_once": ("allow_once", "allow"),
                "allow_always": ("allow_always", "allow_always"),
                "reject": ("reject_once", "reject"),
                "reject_always": ("reject_always", "reject_always"),
            }[self.permission]
            kind, option_id = wanted
            pick = next((o for o in options if o.get("kind") == kind and o.get("optionId") == option_id), None)
            if pick is None:
                pick = next((o for o in options if o.get("kind") == kind), None) or (options[0] if options else {})
            tool = (msg["params"].get("toolCall") or {}).get("title", "")
            print(f"  permission: {tool} -> {pick.get('optionId')}", flush=True)
            self.send({"jsonrpc": "2.0", "id": msg["id"],
                       "result": {"outcome": {"outcome": "selected", "optionId": pick.get("optionId")}}})
        else:
            self.send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "not supported"}})


def summarize(updates):
    """The answer text and the tool calls of a prompt's updates."""
    text, tools = [], []
    for p in updates:
        u = p.get("update", {})
        kind = u.get("sessionUpdate")
        if kind == "agent_message_chunk":
            text.append((u.get("content") or {}).get("text", ""))
        elif kind == "tool_call":
            tools.append(u.get("title") or u.get("toolCallId", ""))
    return "".join(text).strip(), tools


def update_kinds(updates):
    """The kinds (and tool titles) of a run's session/update notifications."""
    kinds = []
    for p in updates:
        u = p.get("update", {})
        kind = u.get("sessionUpdate")
        if kind == "tool_call":
            kinds.append("tool_call:" + str(u.get("title") or u.get("toolCallId", ""))[:60])
        elif kind:
            kinds.append(kind)
    return kinds


def current_model_of(options):
    """The currentValue of the model select in a configOptions list ("" absent)."""
    for o in options or []:
        if o.get("id") == "model":
            # The select variant serializes flat (currentValue at top level),
            # older shapes nest it under 'select' — accept both.
            return o.get("currentValue", "") or (o.get("select") or {}).get("currentValue", "")
    return ""


def lifecycle(client, binary, cwd, env, args):
    """Prove list/load/resume/close/set_config_option against a real model."""
    ok = True
    mark = len(client.updates)
    created = client.call("session/new", {"cwd": cwd, "mcpServers": []}, timeout=60)
    sid = created["sessionId"]
    opts = created.get("configOptions") or []
    print(f"session {sid} configOptions: {[o.get('id') for o in opts]} "
          f"model={current_model_of(opts)}", flush=True)
    ok = ok and "model" in [o.get("id") for o in opts] and "mode" in [o.get("id") for o in opts]
    ok = ok and current_model_of(opts)

    start, mark = time.time(), len(client.updates)
    res = client.call("session/prompt", {"sessionId": sid, "prompt": [
        {"type": "text", "text": "请用 shell_exec 工具运行 echo hello-lifecycle，然后简短回答。"}]}, timeout=300)
    answer, tools = summarize(client.updates[mark:])
    stop = res.get("stopReason")
    ok = ok and stop == "end_turn" and bool(tools)
    print(f"prompt 1 ({time.time() - start:.1f}s) stop={stop} tools={tools} kinds={update_kinds(client.updates[mark:])}", flush=True)

    close_mark = len(client.updates)
    client.call("session/close", {"sessionId": sid}, timeout=60)
    print("session/close ok", flush=True)
    refused = False
    try:
        client.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": "还在吗？"}]}, timeout=60)
    except RuntimeError as exc:
        refused = "-32602" in str(exc)
    ok = ok and refused
    print(f"prompt after close refused: {refused}", flush=True)

    listed = client.call("session/list", {"cwd": cwd}, timeout=60)
    ids = [s.get("sessionId") for s in listed.get("sessions", [])]
    meta = {str(k): v for k, v in (listed.get("_meta") or {}).items() if str(k).startswith("suga/")}
    print(f"session/list cwd={cwd}: {[(s.get('sessionId'), s.get('title')) for s in listed.get('sessions', [])]} "
          f"meta={meta}", flush=True)
    ok = ok and sid in ids

    resume_mark = len(client.updates)
    resumed = client.call("session/resume", {"sessionId": sid, "cwd": cwd, "mcpServers": []}, timeout=60)
    replayed_between = update_kinds(client.updates[resume_mark:])
    ok = ok and replayed_between == [] and bool(resumed.get("modes"))
    print(f"session/resume modes={bool(resumed.get('modes'))} configOptions={[o.get('id') for o in resumed.get('configOptions') or []]} "
          f"replayed={replayed_between}", flush=True)

    load_mark = len(client.updates)
    client.call("session/load", {"sessionId": sid, "cwd": cwd, "mcpServers": []}, timeout=60)
    replay = update_kinds(client.updates[load_mark:])
    print(f"session/load replay ({len(replay)} notifications):", flush=True)
    for k in replay[:12]:
        print(f"  {k}", flush=True)
    ok = ok and replay and "user_message_chunk" in replay and "agent_message_chunk" in replay
    ok = ok and any(k.startswith("tool_call:") for k in replay)

    target = args.switch_to
    switched = client.call("session/set_config_option", {"sessionId": sid, "configId": "model", "value": target}, timeout=60)
    now_model = current_model_of(switched.get("configOptions") or [])
    ok = ok and now_model == target
    print(f"set_config_option model -> {target}: currentValue={now_model}", flush=True)

    start, mark = time.time(), len(client.updates)
    res = client.call("session/prompt", {"sessionId": sid, "prompt": [
        {"type": "text", "text": "用一句话回答：1+1 等于几？"}]}, timeout=300)
    answer, _ = summarize(client.updates[mark:])
    ok = ok and res.get("stopReason") == "end_turn"
    print(f"prompt 2 on {target} ({time.time() - start:.1f}s) stop={res.get('stopReason')} answer: {answer[:300]}", flush=True)

    trace = subprocess.run([binary, "trace", sid, "--json"], capture_output=True, text=True, env=env, timeout=60)
    for line in trace.stdout.splitlines():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("kind") == "session" and e.get("name") == "model_switch":
            a = e.get("attrs") or {}
            print(f"trace model_switch {a.get('from')} -> {a.get('to')}", flush=True)
            ok = ok and a.get("to") == target
    return ok


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", required=True, help="suga binary")
    p.add_argument("--cwd", required=True, help="session workspace (created if missing)")
    p.add_argument("--home", default="tmp/acp/home", help="isolated SUGA_HOME (reset at start)")
    p.add_argument("--config-from", required=True, help="dir with the real config.yaml and .env")
    p.add_argument("--prompt", action="append", default=[], help="a prompt (repeatable, sent in order)")
    p.add_argument("--prompt-with-image", action="append", default=[], metavar="TEXT|FILE",
                   help="a prompt carrying one image block: TEXT|(FILE) — a text part and a base64 image part")
    p.add_argument("--model", help="the main model of the session (SUGA_MODEL=<provider>/<model>; default keeps the config)")
    p.add_argument("--permission", choices=("allow_once", "allow_always", "reject", "reject_always"),
                   default="allow_once", help="permission option to select for each request")
    p.add_argument("--scenario", choices=("prompts", "lifecycle"), default="prompts",
                   help="lifecycle: new -> chat with a tool call -> close -> list -> resume -> load (replay) -> "
                        "set_config_option model switch -> one more prompt (design 2026-10-20, ACP coverage)")
    p.add_argument("--switch-to", help="lifecycle: the provider/model ref set_config_option switches to")
    p.add_argument("--wait", action="append", type=float, default=[],
                   help="seconds to sleep after the prompt of the same position (repeatable)")
    args = p.parse_args()

    binary = os.path.abspath(args.bin)
    cwd = os.path.abspath(args.cwd)
    home = os.path.abspath(args.home)
    src = os.path.abspath(os.path.expanduser(args.config_from))
    shutil.rmtree(home, ignore_errors=True)
    os.makedirs(home, exist_ok=True)
    os.makedirs(cwd, exist_ok=True)
    env = dict(os.environ)
    for key in ("SUGA_MODEL", "SUGA_CONFIG", "SUGA_HOME"):
        env.pop(key, None)
    if args.model:
        env["SUGA_MODEL"] = args.model
    env.update(read_dotenv(os.path.join(src, ".env")))
    env.update(SUGA_HOME=home, SUGA_CONFIG=os.path.join(src, "config.yaml"), SUGA_HOOKS="off")

    proc = subprocess.Popen([binary, "acp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=open(os.path.join(home, "acp.stderr.log"), "wb"), env=env, cwd=cwd)
    client = Client(proc, args.permission)
    ok = True
    try:
        resp = client.call("initialize", {"protocolVersion": 1, "clientCapabilities": {}}, timeout=60)
        caps = (resp.get("agentCapabilities") or {}).get("promptCapabilities") or {}
        sess_caps = resp.get("agentCapabilities", {}).get("sessionCapabilities") or {}
        print(f"initialize promptCapabilities: {caps} sessionCapabilities: {sorted(sess_caps)}", flush=True)
        if args.scenario == "lifecycle":
            ok = lifecycle(client, binary, cwd, env, args)
            sys.exit(0 if ok else 1)
        sid = client.call("session/new", {"cwd": cwd, "mcpServers": []}, timeout=60)["sessionId"]
        print(f"session {sid}", flush=True)
        for i, text in enumerate(args.prompt):
            start, mark = time.time(), len(client.updates)
            res = client.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": text}]})
            answer, tools = summarize(client.updates[mark:])
            stop = res.get("stopReason")
            ok = ok and stop == "end_turn"
            print(f"prompt {i + 1} ({time.time() - start:.1f}s) stop={stop} tools={tools} "
                  f"permission_requests={client.permission_requests}\n  answer: {answer[:600]}", flush=True)
            if i < len(args.wait) and args.wait[i] > 0:
                time.sleep(args.wait[i])
        for i, spec in enumerate(args.prompt_with_image):
            text, _, file = spec.partition("|")
            with open(file, "rb") as f:
                data = base64.b64encode(f.read()).decode("ascii")
            blocks = [{"type": "text", "text": text},
                      {"type": "image", "data": data, "mimeType": "image/png"}]
            start, mark = time.time(), len(client.updates)
            res = client.call("session/prompt", {"sessionId": sid, "prompt": blocks}, timeout=900)
            answer, tools = summarize(client.updates[mark:])
            stop = res.get("stopReason")
            ok = ok and stop == "end_turn"
            print(f"image prompt {i + 1} ({time.time() - start:.1f}s) stop={stop} tools={tools} "
                  f"permission_requests={client.permission_requests}\n  answer: {answer[:900]}", flush=True)
    finally:
        proc.stdin.close()  # the connection closes: running tasks are killed
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            proc.kill()
            ok = False
            print("suga acp did not exit after the connection closed")
    trace = subprocess.run([binary, "trace", sid, "--json"], capture_output=True, text=True, env=env, timeout=60)
    for line in trace.stdout.splitlines():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("kind") == "task" or (e.get("kind") == "tool" and e.get("name") in ("shell_exec", "task")):
            a = e.get("attrs") or {}
            print(f"  trace {e.get('kind')} {e.get('name')} status={e.get('status')} "
                  f"task_id={a.get('task_id', '')} arg={str(a.get('arg', ''))[:80]}")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
