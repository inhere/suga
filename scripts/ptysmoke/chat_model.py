#!/usr/bin/env python3
"""PTY smoke test for chat's :model inspection, switching and resume.

It configures two fake models plus referenced compact/fallback models, checks that the offline
``models --json`` switchable set is exactly the same set shown by ``:model``, switches to m2, checks fakellm's request log for the next request's model
field, then resumes the same session and checks both the banner and the next request still use
m2. This is deliberately falsifiable: temporarily retain wiring's old configured-only candidate
generation; the unlisted fallback assertion must fail. Removing command dispatch, the runtime
switch, the session metadata patch, or the trace event also makes a check fail.

Run on Linux/macOS through ``make smoke-chat``; Windows has no PTY equivalent.
"""
import argparse
import json
import os
import re
import select
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import PtySession, resolve_port  # noqa: E402
from chat_approval import isolate_ws, wait_port, write_home  # noqa: E402


def model_log(proc):
    """Drain fake-model JSON lines and return their model fields."""
    models = []
    fd = proc.stdout.fileno()
    while select.select([fd], [], [], 0)[0]:
        line = proc.stdout.readline()
        if not line:
            break
        try:
            item = json.loads(line)
        except ValueError:
            continue
        if "model" in item:
            models.append(item["model"])
    return models


def wait_models(proc, expected, timeout=20.0):
    """Wait until fakellm has logged expected requests and return all models."""
    end = time.time() + timeout
    if not hasattr(proc, "suga_models"):
        proc.suga_models = []
    while time.time() < end:
        proc.suga_models.extend(model_log(proc))
        if len(proc.suga_models) >= expected:
            return proc.suga_models
        time.sleep(0.05)
    return proc.suga_models


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bin", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks a free one")
    a = p.parse_args(); a.port = resolve_port(a.port)
    home = os.path.abspath(a.work + "/home")
    ws = os.path.abspath(a.work + "/ws")
    shutil.rmtree(a.work, ignore_errors=True)
    os.makedirs(ws)
    isolate_ws(ws)
    provider_extra = """    models:
      m1:
        context_window: 128000
      m2:
        context_window: 64000
        max_output_tokens: 42
"""
    write_home(home, a.port, provider_extra=provider_extra, extra="compact_model: fake/compact\nfallback_models:\n  - fake/unlisted\nchat:\n  steering: false\n")
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1")
    env.pop("SUGA_CONFIG", None)
    catalog_result = subprocess.run([os.path.abspath(a.bin), "models", "--json"], capture_output=True,
                                    text=True, env=env, cwd=ws, timeout=60)
    if catalog_result.returncode != 0:
        raise SystemExit("models --json failed: " + catalog_result.stderr)
    catalog = json.loads(catalog_result.stdout)
    switchable = catalog.get("switchable") or []
    if not switchable:
        raise SystemExit("models --json returned no switchable candidates")
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, "-I", here + "/fakellm.py", "--port", str(a.port), "--mode", "text", "--reply", "MODEL_REPLY"], stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    first = PtySession([os.path.abspath(a.bin), "chat"], env=env, cwd=ws)
    config_path = os.path.join(home, "config.yaml")
    with open(config_path, "rb") as config_file:
        config_before = config_file.read()
    try:
        mark = first.mark()
        first.check("banner starts with m1", first.until(r"Suga chat · fake/m1", mark))
        mark = first.mark()
        first.line(":model")
        first.check("model list has m1", first.until(r"\* fake/m1", mark))
        first.check("model list has m2", first.until(r"fake/m2", mark))
        first.check("model list has compact", first.until(r"fake/compact", mark))
        first.check("model list has unlisted fallback", first.until(r"fake/unlisted", mark))
        model_lines = set()
        # Only the lines under "可选模型：" are candidates; the "当前模型：" header
        # above them also looks like provider/model.
        listing = first.text(mark).split("可选模型：", 1)[-1]
        for line in listing.splitlines():
            candidate = line.strip()
            if candidate.startswith("* "):
                candidate = candidate[2:].strip()
            if re.fullmatch(r"[^\s/]+/[^\s/]+", candidate):
                model_lines.add(candidate)
        first.check("models JSON and chat candidates are equal", model_lines == set(switchable))
        mark = first.mark()
        first.send(":model fake/un\t")
        first.check("model Tab completion shares unlisted fallback", first.until(r":model fake/unlisted", mark))
        first.send("\x15")
        mark = first.mark()
        first.line(":model fake/m2")
        first.check("switch accepted", first.until(r"已切换模型：fake/m2", mark))
        with open(config_path, "rb") as config_file:
            config_unchanged = config_file.read() == config_before
        first.check("model command does not write config", config_unchanged)
        mark = first.mark()
        first.line("first request")
        first.check("next request uses m2", wait_models(fake, 1) == ["m2"])
        first.line(":quit")
        time.sleep(0.3)
        session = re.search(r"session id: ((?:sess_|sg_|s_)\S+)", first.text())
        first.check("session id is printed", session is not None)
        sid = session.group(1) if session else ""
    finally:
        first.close()

    second = PtySession([os.path.abspath(a.bin), "chat", "--resume", sid], env=env, cwd=ws)
    try:
        mark = second.mark()
        second.check("resume banner keeps m2", second.until(r"Suga chat · fake/m2", mark))
        second.line("resumed request")
        second.check("resumed request uses m2", wait_models(fake, 2) == ["m2", "m2"])
        second.line(":quit")
    finally:
        second.close()
    first.results.extend(second.results)
    trace = subprocess.run([os.path.abspath(a.bin), "inspect", "trace", sid, "--json"], capture_output=True, text=True, env=env, timeout=60)
    first.check("trace command succeeds", trace.returncode == 0)
    events = [json.loads(line) for line in trace.stdout.splitlines() if line.strip()]
    switch = next((e for e in events if e.get("kind") == "session" and e.get("name") == "model_switch"), {})
    attrs = switch.get("attrs") or {}
    first.check("trace records model switch", attrs.get("from") == "fake/m1" and attrs.get("to") == "fake/m2")
    fake.terminate()
    fake.wait(timeout=5)
    if first.failed():
        print("failed: " + ", ".join(first.failed()))
        raise SystemExit(1)


if __name__ == "__main__":
    main()
