#!/usr/bin/env python3
"""PTY smoke tests for scoped chat approval rules (Linux/macOS).

The scenarios drive the real ``suga chat`` process and fakellm without human
input.  They cover allow and deny rule precedence, command-prefix and file
-directory session scopes, ``a``/``d`` follow-up choices, user/project
writeback across a new process, unsafe-shell prompt restrictions, trace and
manifest diagnostics, terminal-text cleaning, and the ``enabled: false``
legacy fallback.

Reverse verification (drop the behaviour and a check must fail):
- make the matcher stop at the first matching rule: the allow-then-deny
  precedence scenario must execute instead of reporting a denial;
- bypass ``Policy.Decide`` in the chat approver: the allow scenario asks and
  the deny scenario executes or loses its tool error;
- remove rule writeback: the second process in the user/project scenarios asks
  again;
- bypass ``termtext.Clean``: the command/path raw escape checks fail.

The work directories are scratch directories below the requested ``--work``;
they are removed after the run.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402
from ptydrive import CTRL_C, PtySession, resolve_port  # noqa: E402

QUESTION = r"允许执行 shell_exec"
RESUME = r"续接：suga chat --resume (?:sess_|sg_|s_)"


def start(binary, work, port, mode="tool", command="echo RULE_OK", write_path="",
          rules_extra=""):
    """Start fakellm and return the isolated home, workspace, environment, and fake."""
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "ws"))
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    isolate_ws(ws)
    extra = "  approval_rules:\n    persist_ask: true\n" + rules_extra
    write_home(home, port, tools_extra=extra)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    here = os.path.dirname(os.path.abspath(__file__))
    args = [sys.executable, "-I", os.path.join(here, "fakellm.py"), "--port", str(port),
            "--mode", mode, "--command", command, "--delay", "0.1"]
    if write_path:
        args += ["--write-path", write_path]
    fake = subprocess.Popen(args, stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return home, ws, env, fake


def rules_file(home, body):
    with open(os.path.join(home, "approval-rules.yaml"), "w", encoding="utf-8") as f:
        f.write("rules:\n" + body)


def exit_chat(s, turn_mark=None):
    """Exit from the prompt and return the persisted session id from the summary.

    turn_mark is where the last turn's output starts: the callers see the model's
    reply ("完成") before the turn has ended, and a Ctrl-C sent then cancels the
    turn instead of asking to exit (seen under load in the verify workflow). Wait
    for the turn's status line first."""
    if turn_mark is not None:
        s.check("exit: the turn has ended", s.until(r"tokens · ", turn_mark, timeout=60))
        s.drain(0.3)
    mark = s.mark()
    s.send(CTRL_C)
    s.check("exit: first Ctrl-C gives a hint", s.until(r"再按一次 Ctrl\+C 退出", mark))
    s.send(CTRL_C)
    s.check("exit: resume summary", s.until(RESUME, mark))
    found = re.search(r"--resume ((?:sess_|sg_|s_)\S+)", s.text(mark))
    return found.group(1) if found else ""


def trace_events(binary, env, session, cwd):
    """Read trace JSON without making non-JSON diagnostics fatal to the smoke."""
    out = subprocess.run([binary, "inspect", "trace", session, "--json"], capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    events = []
    for line in out.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    return out, events


def manifest(binary, env, cwd):
    out = subprocess.run([binary, "inspect", "manifest", "--json"], capture_output=True,
                         text=True, env=env, cwd=cwd, timeout=60)
    try:
        return out, json.loads(out.stdout)
    except ValueError:
        return out, {}


def check_case(binary, work, port, rule_body, command, expected_question, answer="n",
               expect_denied=False):
    home, ws, env, fake = start(binary, work, port, command=command)
    rules_file(home, rule_body)
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark()
        s.until(r"❯ ", mark)
        s.line("run")
        if expected_question:
            s.check("rule case asks", s.until(QUESTION, mark))
            s.line(answer)
        else:
            s.check("rule case does not ask", not s.until(QUESTION, mark, timeout=0.8))
        if expect_denied:
            s.check("rule case reports tool denial", s.until(r"规则 .*拒绝执行|用户拒绝执行", mark))
        s.check("turn completes", s.until(r"完成", mark))
        exit_chat(s, mark)
        s.close()
        return s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def session_scope(binary, work, port):
    """An ``a`` command-prefix rule is active only in this chat session."""
    home, ws, env, fake = start(binary, work, port, command="echo SCOPED")
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark()
        s.until(r"❯ ", mark)
        s.line("first")
        s.check("scoped command prompt", s.until(r"\[a\]本会话总是允许“echo SCOPED”开头的命令", mark))
        s.line("a")
        s.check("scoped allow persistence prompt", s.until(r"把这条规则写进规则文件", mark))
        s.line("")
        s.check("scoped first call runs", s.until(r"完成", mark))
        mark = s.mark()
        s.line("second")
        s.check("same command prefix is not asked again", not s.until(QUESTION, mark, timeout=0.8))
        s.check("same command prefix completes", s.until(r"完成", mark))
        exit_chat(s, mark)
        s.close()
        return s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def path_scope(binary, work, port):
    """An ``a`` file rule covers the directory, not only the first path."""
    home, ws, env, fake = start(binary, work, port, mode="adddir", write_path="docs/one.txt")
    os.makedirs(os.path.join(ws, "docs"), exist_ok=True)
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark()
        s.until(r"❯ ", mark)
        s.line("只写 first")
        s.check("directory scope prompt", s.until(r"\[a\]本会话总是允许写入 docs/ 目录", mark))
        s.line("a")
        s.check("directory scope persistence prompt", s.until(r"把这条规则写进规则文件", mark))
        s.line("")
        s.check("directory scoped write completes", s.until(r"完成", mark))
        mark = s.mark()
        s.line("只写 second")
        s.check("same directory is not asked again", not s.until(QUESTION, mark, timeout=0.8))
        s.check("same directory write completes", s.until(r"完成", mark))
        s.check("directory scoped file was written", os.path.exists(os.path.join(ws, "docs", "one.txt")))
        exit_chat(s, mark)
        s.close()
        return s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def deny_scope(binary, work, port):
    """A ``d`` command-prefix rule returns a tool error and remains active."""
    home, ws, env, fake = start(binary, work, port, command="echo DENY_SCOPE")
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark()
        s.until(r"❯ ", mark)
        s.line("first")
        s.check("deny scope prompt", s.until(r"\[d\]本会话总是拒绝", mark))
        s.line("d")
        s.check("deny persistence prompt", s.until(r"把这条规则写进规则文件", mark))
        s.line("")
        s.check("deny first call reports tool error", s.until(r"规则 .*拒绝执行", mark))
        s.check("deny first turn continues", s.until(r"完成", mark))
        mark = s.mark()
        s.line("second")
        s.check("same denied scope is not asked again", not s.until(QUESTION, mark, timeout=0.8))
        s.check("denied second turn continues", s.until(r"完成", mark))
        exit_chat(s, mark)
        s.close()
        return s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def user_persistence(binary, work, port):
    """A user ``u`` writeback is loaded by a newly started chat process."""
    home, ws, env, fake = start(binary, work, port, command="echo USER_RULE")
    s = s2 = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark(); s.until(r"❯ ", mark); s.line("persist user")
        s.check("user persistence asks", s.until(r"\[a\]本会话总是允许", mark))
        s.line("a")
        s.check("user persistence follow-up", s.until(r"把这条规则写进规则文件", mark))
        s.line("u")
        s.check("user rule first call completes", s.until(r"完成", mark))
        user_path = os.path.join(home, "approval-rules.yaml")
        s.check("user rule file exists", os.path.exists(user_path))
        if os.path.exists(user_path):
            with open(user_path, encoding="utf-8") as f:
                text = f.read()
            s.check("user rule file contains allow", "action: allow" in text and "echo USER_RULE" in text)
        first_results = s.results[:]
        exit_chat(s, mark)
        s.close()

        s2 = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s2.mark(); s2.until(r"❯ ", mark); s2.line("new process")
        s2.check("new process uses user rule without asking", not s2.until(QUESTION, mark, timeout=0.8))
        s2.check("new process user rule completes", s2.until(r"完成", mark))
        exit_chat(s2, mark)
        s2.close()
        return first_results + s2.results
    finally:
        if s is not None:
            s.close(grace=0)
        if s2 is not None:
            s2.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def project_persistence_and_diagnostics(binary, work, port):
    """A project ``p`` deny survives a process restart and is observable."""
    home, ws, env, fake = start(binary, work, port, command="echo PROJECT_RULE")
    s = s2 = None
    session = ""
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark(); s.until(r"❯ ", mark); s.line("persist project")
        s.check("project deny asks", s.until(r"\[d\]本会话总是拒绝", mark))
        s.line("d")
        s.check("project deny follow-up", s.until(r"把这条规则写进规则文件", mark))
        s.line("p")
        s.check("project deny first turn continues", s.until(r"完成", mark))
        project_path = os.path.join(ws, ".suga", "approval-rules.yaml")
        s.check("project rule file exists", os.path.exists(project_path))
        if os.path.exists(project_path):
            with open(project_path, encoding="utf-8") as f:
                text = f.read()
            s.check("project file contains deny only", "action: deny" in text and "action: allow" not in text)
        session = exit_chat(s, mark)
        s.close()

        out, events = trace_events(binary, env, session, ws) if session else (None, [])
        denial = next((e for e in events if e.get("name") == "shell_exec" and
                       e.get("kind") in ("denial", "tool")), {})
        attrs = denial.get("attrs") or {}
        s.check("trace command succeeds", out is not None and out.returncode == 0)
        s.check("trace exposes approval rule", "session#1" in attrs.get("approval_rule", "") and
                "action=deny" in attrs.get("approval_rule", ""))
        s.check("trace exposes approval scope", attrs.get("approval_scope") == "shell_exec:echo PROJECT_RULE")

        out, data = manifest(binary, env, ws)
        project = (data.get("approval_rules") or {}).get("project") or {}
        s.check("manifest command succeeds", out.returncode == 0)
        s.check("manifest exposes project rule reason", project.get("effective_rules", 0) >= 1 and
                "trust" in project.get("trust_reason", ""))

        s2 = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s2.mark(); s2.until(r"❯ ", mark); s2.line("new process")
        s2.check("new process uses project deny without asking", not s2.until(QUESTION, mark, timeout=0.8))
        s2.check("new process project denial continues", s2.until(r"完成", mark))
        first_results = s.results[:]
        exit_chat(s2, mark)
        s2.close()
        return first_results + s2.results
    finally:
        if s is not None:
            s.close(grace=0)
        if s2 is not None:
            s2.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
        shutil.rmtree(work, ignore_errors=True)


def control_text(binary, work, port):
    """Unsafe shell commands cannot display an allow-always option."""
    home, ws, env, fake = start(binary, work, port, command="echo\x1b[2KCONTROL")
    s = None
    results = []
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark(); s.until(r"❯ ", mark); s.line("control")
        s.check("command control bytes are cleaned", s.until(r"命令：echoCONTROL", mark))
        s.check("unsafe shell has no allow-always option", not s.contains(r"\[a\]本会话总是允许", mark))
        s.check("command escape is not written", not s.contains_raw(re.escape("命令：echo\x1b[2K"), mark))
        s.line("n"); s.until(r"完成", mark); exit_chat(s, mark); s.close(); results = s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5); shutil.rmtree(work, ignore_errors=True)

    path_work = work + "-path"
    control_path = "dir\x1b[2K/file.txt"
    home, ws, env, fake = start(binary, path_work, port + 1, mode="adddir", write_path=control_path)
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark(); s.until(r"❯ ", mark); s.line("只写")
        s.check("path control bytes are cleaned", s.until(r"路径：.*dir/file.txt", mark))
        s.check("path escape is not written", not s.contains_raw(re.escape("dir\x1b[2K"), mark))
        s.line("n"); s.until(r"完成", mark); exit_chat(s, mark); s.close(); results += s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5); shutil.rmtree(path_work, ignore_errors=True)
    return results


def legacy_disabled(binary, work, port):
    """Disabled rules use the old tool-level prompt and session always map."""
    home, ws, env, fake = start(binary, work, port, command="echo LEGACY", rules_extra="    enabled: false\n")
    rules_file(home, "  - tool: shell_exec\n    command: echo LEGACY\n    action: deny\n")
    s = None
    try:
        s = PtySession([binary, "chat"], env=env, cwd=ws, size=(100, 30))
        mark = s.mark(); s.until(r"❯ ", mark); s.line("legacy")
        s.check("disabled rules keep old prompt", s.until(r"允许执行 shell_exec？\[y\]是 \[N\]否 \[a\]本会话总是允许该工具", mark))
        s.check("disabled rules do not use scoped command prompt", not s.contains(r"开头的命令", mark))
        s.line("a")
        s.check("disabled rules old always completes", s.until(r"完成", mark))
        s.check("disabled rules have no persistence follow-up", not s.contains(r"把这条规则写进规则文件", mark))
        mark = s.mark(); s.line("legacy second")
        s.check("disabled rules old always is not asked again", not s.until(QUESTION, mark, timeout=0.8))
        s.check("disabled rules second turn completes", s.until(r"完成", mark))
        exit_chat(s, mark); s.close(); return s.results
    finally:
        if s is not None:
            s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5); shutil.rmtree(work, ignore_errors=True)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", default="tmp/smoke/suga")
    p.add_argument("--work", default="tmp/smoke-rules")
    p.add_argument("--port", type=int, default=0,
                   help="fake model port; 0 picks free ports (one per case, up to the next ten)")
    args = p.parse_args()
    # One fake model per case: the base port and the next ten.
    args.port = resolve_port(args.port, 10)
    binary = os.path.abspath(args.bin)
    results = []
    results += check_case(binary, args.work + "-allow", args.port,
                          "  - tool: shell_exec\n    command: echo ALLOW\n    action: allow\n", "echo ALLOW", False)
    results += check_case(binary, args.work + "-deny", args.port + 1,
                          "  - tool: shell_exec\n    command: echo DENY\n    action: deny\n", "echo DENY", False, expect_denied=True)
    results += check_case(binary, args.work + "-precedence", args.port + 2,
                          "  - tool: shell_exec\n    command: echo PRECEDENCE\n    action: allow\n  - tool: shell_exec\n    command: echo PRECEDENCE\n    action: deny\n", "echo PRECEDENCE", False, expect_denied=True)
    results += session_scope(binary, args.work + "-session", args.port + 3)
    results += path_scope(binary, args.work + "-path", args.port + 4)
    results += deny_scope(binary, args.work + "-deny-scope", args.port + 5)
    results += user_persistence(binary, args.work + "-user", args.port + 6)
    results += project_persistence_and_diagnostics(binary, args.work + "-project", args.port + 7)
    results += control_text(binary, args.work + "-control", args.port + 8)
    results += legacy_disabled(binary, args.work + "-disabled", args.port + 10)
    failed = [name for name, ok in results if not ok]
    print(f"{len(results) - len(failed)}/{len(results)} passed")
    if failed:
        print("failed: " + ", ".join(failed))
        raise SystemExit(1)


if __name__ == "__main__":
    main()
