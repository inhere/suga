#!/usr/bin/env python3
"""End-to-end smoke test: file_refs answers "who uses X" in chat (Linux/macOS).

fakellm.py (refs mode) has the model call file_refs twice in one turn: first on
a single Go file, then on the whole workspace. The workspace holds a small Go
package and a TypeScript file that both mention Load, plus a comment and a
string literal that must not be matches (`SMOKE_*_CANARY` marks the lines).

Checks: the two calls on screen with their arguments, the first match line of
each result, the trace of the session (two file_refs events, each with the
target and the argument summary of its call) and the stored session (`suga
export`): the calls keep the name and the path they were made with, the Go
result carries the def/call/sel/ref classes, the `call(pkg:db)` package
qualifier and the fixed "no type resolution" note, the workspace result carries
the TypeScript file and the approx note, and no canary of either file appears.

Reverse verification (drop the behaviour and a check fails):

- remove the `file_refs` row from native/catalog.go: the two "call" checks fail
  (the model asks for a tool the runtime does not have).
- return an empty result from FileRefsTool.Execute: the "first match" checks and
  the class/approx checks of the stored result fail.
- drop the byte prefilter/language dispatch and answer Go files with
  ScanLineRefs: "result 1: exact Go only, no approx note" fails.
- keep the string literals of maskInline unmasked: "result 1: the comment and
  the string literal are not matches" and "result 2: the masked comment and
  string are not matches" fail.

  python3 -I scripts/ptysmoke/chat_refs.py --bin tmp/smoke/suga

Exit status 0 when every check passes. The work dir defaults to tmp/smoke-refs.
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
from chat_observe import trace_events  # noqa: E402

NAME = "Load"
# The Go fixture covers all four classes of one name: def (the func and a struct
# field), call with a package qualifier (db.Load), sel (h.Load, not called) and
# ref (Load as a value). The comment and the string literal mention the name too
# and must not be reported; their canary words prove it.
GO_SRC = """package svc

import "example/db"

// Load is documented here; a comment is not a reference.
func Load(id string) string { return db.Load(id) }

type Handler struct{ Load func(string) string }

func Run(h Handler) string {
	fn := h.Load
	text := "Load SMOKE_STRING_CANARY"
	return fn(text)
}

var alias = Load
"""

TS_SRC = """import { Store } from "./store";

// Load SMOKE_COMMENT_CANARY
export function Load(name: string): string {
  return name;
}

export const label = "Load SMOKE_STRING_CANARY";

export function useLoad(s: Store): string {
  return Load(s.id);
}
"""


def scenario(s):
    """One chat turn: the prompt, the two file_refs calls and their results."""
    m = s.mark()
    s.check("❯ prompt", s.until(r"❯ ", m))
    m = s.mark(); s.line("谁用了 Load")
    # The call lines show the arguments the model sent in the form of the tool:
    # file_refs prints the identifier and the path it searches.
    s.check("call 1: file-level arguments",
            s.until(r'⏺ file_refs Load svc/svc\.go', m))
    s.check("result 1: the first match is the classified call",
            s.until(r"⎿ ok: svc/svc\.go:6 call\(pkg:db\) ", m))
    s.check("call 2: directory-level arguments",
            s.until(r'⏺ file_refs Load\r*\n', m))
    s.check("result 2: the Go file comes first", s.until(r"⎿ ok: svc/svc\.go:6 ", m))
    s.check("turn ends", s.until(r"tokens · ", m))
    m = s.mark(); time.sleep(0.3); s.send(CTRL_C); time.sleep(0.3); s.send(CTRL_C)
    s.check("exit: resume command", s.until(r"续接：suga chat --resume (?:sess_|sg_|s_)", m))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(m))
    return found.group(1) if found else ""


def check_trace(s, events):
    """The two calls reach the trace as tool events, with their target."""
    refs = [e for e in events if e.get("kind") == "tool" and e.get("name") == "file_refs"]
    s.check("trace: two file_refs events", len(refs) == 2)
    if len(refs) != 2:
        return
    one, many = refs
    one_attrs, many_attrs = one.get("attrs") or {}, many.get("attrs") or {}
    s.check("trace: both calls ended ok", one.get("status") == "ok" and many.get("status") == "ok")
    s.check("trace: the file-level call names its file", one_attrs.get("target") == ["svc/svc.go"])
    s.check("trace: the directory-level call names the workspace root", many_attrs.get("target") == ["."])
    s.check("trace: the argument summaries carry the paths",
            "path=svc/svc.go" in str(one_attrs.get("arg") or "") and "path=." in str(many_attrs.get("arg") or ""))


def export_rows(binary, env, session):
    """The session's entries and events as the `suga inspect export` jsonl rows."""
    out = subprocess.run([binary, "inspect", "export", session], capture_output=True, text=True, env=env, timeout=60)
    rows = []
    for line in out.stdout.splitlines():
        try:
            rows.append(json.loads(line))
        except ValueError:
            pass
    return rows


def stored(binary, env, session):
    """The stored file_refs calls and results, in turn order: the assistant
    entries that asked for the tool (their raw JSON keeps the arguments) and the
    tool entries that answered them (their raw JSON keeps the result text)."""
    calls, results = [], []
    for row in export_rows(binary, env, session):
        if row.get("kind") != "entry":
            continue
        body = row.get("json") or ""
        if not isinstance(body, str):
            # `suga inspect export` writes an entry's json as an object, not a string.
            body = json.dumps(body, ensure_ascii=False, separators=(",", ":"))
        if row.get("type") == "assistant" and '"Name":"file_refs"' in body:
            calls.append(body)
        elif row.get("type") == "tool":
            results.append(body)
    return calls, results


def check_stored(s, binary, env, session):
    """What the session keeps: the arguments of both calls and what each result
    listed (the tool event attrs carry only the target, not the result text)."""
    calls, results = stored(binary, env, session)
    s.check("stored: two file_refs calls", len(calls) == 2)
    s.check("stored: two file_refs results", len(results) == 2)
    if len(calls) != 2 or len(results) != 2:
        return
    s.check("stored: the calls kept the name and the target",
            all('"name":"Load"' in c for c in calls)
            and '"path":"svc/svc.go"' in calls[0] and '"path":"."' in calls[1])
    one, many = results
    s.check("result 1: the four Go classes",
            " def " in one and "call(pkg:db)" in one and "sel(h)" in one and " ref " in one)
    s.check("result 1: the fixed no-type-resolution note", "[no type resolution:" in one)
    s.check("result 1: an exact Go result carries no approx note", "[approx:" not in one)
    s.check("result 1: the comment and the string literal are not matches",
            "documented" not in one and "SMOKE_STRING_CANARY" not in one)
    s.check("result 2: the workspace search reaches the TypeScript file", "web/app.ts:" in many)
    s.check("result 2: the approximate match is marked", "[approx:" in many)
    s.check("result 2: the masked comment and string are not matches",
            "SMOKE_COMMENT_CANARY" not in many and "SMOKE_STRING_CANARY" not in many)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to test")
    p.add_argument("--work", default="tmp/smoke-refs", help="scratch dir (SUGA_HOME and workspace)")
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
    # A go.mod of its own makes the fixture a separate module: the scratch
    # workspace stays under this repository's tmp/, and a leftover svc.go
    # (it imports a package that does not exist) must not break `go build ./...`.
    with open(os.path.join(ws, "go.mod"), "w", encoding="utf-8") as f:
        f.write("module example.com/refsmoke\n\ngo 1.22\n")
    os.makedirs(os.path.join(ws, "svc"), exist_ok=True)
    os.makedirs(os.path.join(ws, "web"), exist_ok=True)
    with open(os.path.join(ws, "svc", "svc.go"), "w", encoding="utf-8") as f:
        f.write(GO_SRC)
    with open(os.path.join(ws, "web", "app.ts"), "w", encoding="utf-8") as f:
        f.write(TS_SRC)
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config

    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(here, "fakellm.py"),
                             "--port", str(args.port), "--mode", "refs", "--delay", "0.2"],
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
            check_stored(s, binary, env, session)
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
