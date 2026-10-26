#!/usr/bin/env python3
"""Records the suga chat demo (README / blog) as an asciicast v2 file.

The recording is deterministic and needs neither a real model nor an API key: it
starts scripts/ptysmoke/fakellm.py in demo mode, writes a tiny todo project into
a scratch workspace, and types a fixed timeline into a real `suga chat` running
in a pseudo terminal. The pty output becomes docs/demo/suga-chat.cast (asciicast
v2, 100x28), which `asciinema play` replays and `agg` turns into a GIF.

What the timeline shows: the banner, a question about the small project (two tool
lines), a line queued with Tab while the model answers ([排队] …), an edit that
asks for approval (answered y), :context, and the resume line `:quit` prints.

Linux/macOS only (it needs a pty). Record from the repository root:

  make demo-cast                                                     # build + record
  python3 -I scripts/demo/record.py --bin tmp/smoke/suga             # record only
  python3 -I scripts/demo/record.py --dry-run                        # timeline only

Exit status 0 when every step of the timeline happened, the cast is within
--max-seconds (60: the length a README GIF should keep) and no absolute path of
the machine leaked into it.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, os.path.join(REPO, "scripts", "ptysmoke"))
# The pty driver only exists on Linux/macOS; --dry-run stays usable on a Windows
# host (it prints the timeline instead of recording).
PTY_ERROR = None
try:
    from chat_approval import isolate_ws, wait_port  # noqa: E402
    from ptydrive import PtySession  # noqa: E402
except ImportError as exc:  # no pty module on Windows
    PTY_ERROR = exc
    PtySession = object
    isolate_ws = wait_port = None

# The cast's terminal size: the recording must never need more than 100 columns
# (a README GIF is unreadable when lines wrap and scroll sideways).
COLS, ROWS = 100, 28
# The status line every turn ends with (its "· ctx N% · 1.2s · 12:34" part).
TURN_END = r"tokens · ctx \d+% · [\d.]+m?s · \d{2}:\d{2}"
# Seconds to wait for one expected line before the recording counts as broken.
WAIT_TIMEOUT = 60
# Seconds between two typed characters: fast enough to stay short, slow enough
# that the line editor of a viewer can follow the typing.
TYPE_DELAY = 0.05
# The line queued with Tab, and the line that asks for the edit: the fake model
# picks its turn by these words ("运行" / "改成", see fakellm demo_plan).
QUEUED_LINE = "顺便说下怎么运行"
EDIT_LINE = "把 todo.py 里 added: 改成中文提示"
SURVEY_LINE = "先看下这个项目是做什么的？"

# The small project the model reads: two source files and a test, nothing that
# needs to run for the demo (the model only reads them).
SAMPLE_FILES = {
    "README.md": """# todo

A tiny command line todo list. `todo.py` adds, lists and finishes items and
keeps them in `~/.todo.json`; `test_todo.py` holds the pytest tests.
""",
    "todo.py": '''"""A tiny command line todo list."""
import json
import os
import sys

PATH = os.path.join(os.path.expanduser("~"), ".todo.json")


def load():
    if not os.path.exists(PATH):
        return []
    with open(PATH, encoding="utf-8") as f:
        return json.load(f)


def save(items):
    with open(PATH, "w", encoding="utf-8") as f:
        json.dump(items, f, ensure_ascii=False, indent=2)


def add(text):
    items = load()
    items.append({"text": text, "done": False})
    save(items)
    print("added:", text)


def finish(index):
    items = load()
    items[index - 1]["done"] = True
    save(items)


def show():
    for i, item in enumerate(load(), 1):
        print("%d. [%s] %s" % (i, "x" if item["done"] else " ", item["text"]))


if __name__ == "__main__":
    if sys.argv[1:2] == ["add"]:
        add(" ".join(sys.argv[2:]))
    elif sys.argv[1:2] == ["done"]:
        finish(int(sys.argv[2]))
    else:
        show()
''',
    "test_todo.py": '''import todo


def test_add(tmp_path, monkeypatch):
    monkeypatch.setattr(todo, "PATH", str(tmp_path / "todo.json"))
    todo.add("write the report")
    assert todo.load() == [{"text": "write the report", "done": False}]


def test_finish(tmp_path, monkeypatch):
    monkeypatch.setattr(todo, "PATH", str(tmp_path / "todo.json"))
    todo.add("ship it")
    todo.finish(1)
    assert todo.load()[0]["done"] is True
''',
}

# What the fake model answers, per turn (fakellm demo mode); passed as flags so
# the wording lives next to the timeline that shows it.
REPLY_SURVEY = "这是一个命令行待办清单：`todo.py` 用 add / done / 无参数 三个入口读写 `~/.todo.json`，`test_todo.py` 是它的 pytest 测试。"
REPLY_QUEUED = "运行方式：`python todo.py add \"写周报\"` 添加，`python todo.py` 列出全部；跑测试用 `pytest -q`。"
REPLY_EDIT = "改好了：新增待办的提示从 `added:` 换成了 `已添加:`，再执行一次 `python todo.py add ...` 就能看到。"

# The timeline. One step is one of:
#   ("wait", regex, what)  a line that must appear before the next step (the
#                          recording is broken when it does not: no silent desync)
#   ("sleep", seconds)     a pause so a viewer can read what is on screen
#   ("type", text)         types the text character by character (no Enter)
#   ("enter",)             submits the line
#   ("tab",)               queues the line for the turn after the current one
# The waits are the only synchronization: the pauses after them decide the pace.
STEPS = [
    ("wait", r"Suga chat · demo/demo-chat", "banner with the model"),
    ("wait", r"❯ ", "prompt"),
    ("sleep", 1.2),
    ("type", SURVEY_LINE),
    ("enter",),
    ("wait", r"⏺ file_read README\.md", "first tool line"),
    ("wait", r"⏺ file_read todo\.py", "second tool line"),
    # The model is answering now: the line typed here is queued, not sent, so the
    # answer in flight is not disturbed (chat.steering: Tab queues, Enter steers).
    ("type", QUEUED_LINE),
    ("tab",),
    ("wait", r"\[排队\] " + QUEUED_LINE + "（本轮结束后发送）", "queued line"),
    ("wait", r"\[排队\] " + QUEUED_LINE + "（已发送）", "queued line runs next"),
    ("wait", r"运行方式：", "answer to the queued line"),
    ("wait", TURN_END, "end of that turn"),
    ("sleep", 0.8),
    ("type", EDIT_LINE),
    ("enter",),
    ("wait", r"允许执行 file_edit", "approval question"),
    ("sleep", 1.6),
    ("type", "y"),
    ("enter",),
    ("wait", r"⎿ ok: edited", "edit applied"),
    ("wait", r"改好了：", "answer after the edit"),
    ("wait", TURN_END, "end of the edit turn"),
    ("sleep", 0.8),
    ("type", ":context"),
    ("enter",),
    ("wait", r"上下文构成", ":context"),
    ("sleep", 2.0),
    ("type", ":quit"),
    ("enter",),
    ("wait", r"续接：suga chat --resume", "resume command"),
    ("sleep", 0.8),
]


class Recorder(PtySession):
    """A PtySession that keeps every chunk it reads with the time it arrived:
    the asciicast events are exactly what a terminal received, echo included."""

    def __init__(self, *args, **kwargs):
        self.events = []
        self.t0 = None
        super().__init__(*args, **kwargs)
        self.t0 = time.time()

    def _read_once(self, timeout):
        before = len(self.buf)
        ok = super()._read_once(timeout)
        if len(self.buf) > before:
            self.events.append((time.time() - self.t0, self.buf[before:].decode("utf-8", "replace")))
        return ok

    def duration(self):
        return self.events[-1][0] if self.events else 0.0


def write_home(home, port):
    """Writes the scratch SUGA_HOME: a fake OpenAI-compatible provider on the
    local fake server, write tools on (chat still asks before a write), project
    memory on the files backend so no real gofer is called."""
    os.makedirs(home, exist_ok=True)
    # An empty .env marks the home as already set up, which keeps the first-run
    # notice (it names the work directory) out of the recording.
    open(os.path.join(home, ".env"), "a", encoding="utf-8").close()
    with open(os.path.join(home, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(f"""model: demo/demo-chat
providers:
  demo:
    type: openai
    base_url: http://127.0.0.1:{port}/v1
    api_key: demo-key-not-a-secret
    models:
      demo-chat:
        context_window: 65536
tools:
  allow_write: true
# The demo's last turn ends after an edit without a check command: the nudge hint
# would be honest but is not what the recording is about.
agents:
  nudge: false
memory:
  project:
    backend: files
""")


def prepare(work):
    """The scratch SUGA_HOME and workspace, rebuilt from scratch: files a previous
    run left behind would change what this one records."""
    home = os.path.abspath(os.path.join(work, "home"))
    ws = os.path.abspath(os.path.join(work, "todo"))
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    os.makedirs(ws, exist_ok=True)
    for name, content in SAMPLE_FILES.items():
        with open(os.path.join(ws, name), "w", encoding="utf-8") as f:
            f.write(content)
    # Its own git root, so suga does not pick up the enclosing repository.
    isolate_ws(ws)
    return home, ws


def scrubber(home, ws):
    """Replaces this machine's absolute paths, so the cast names no real
    directory (the workspace shows up as a neutral project path). The workspace
    goes first: it sits inside the repository, so it is the longer prefix."""
    pairs = [(ws, "~/demo/todo"), (home, "~/.suga"), (REPO, "~")]

    def scrub(text):
        for old, new in pairs:
            text = text.replace(old, new)
        return text

    return scrub


def play(s, steps, out=sys.stdout):
    """Runs the timeline; returns the notes of the steps that failed (empty when
    every expected line appeared)."""
    failed = []
    for step in steps:
        kind = step[0]
        if kind == "wait":
            pattern, what = step[1], step[2]
            mark = s.mark()
            ok = s.until(pattern, mark, timeout=WAIT_TIMEOUT)
            print(("  ok   " if ok else "  MISS ") + what, file=out, flush=True)
            if not ok:
                failed.append(what)
        elif kind == "sleep":
            time.sleep(step[1])
        elif kind == "type":
            for ch in step[1]:
                s.send(ch)
                time.sleep(TYPE_DELAY)
        elif kind == "enter":
            s.send("\r")
            time.sleep(0.2)
        elif kind == "tab":
            s.send("\t")
        else:
            raise SystemExit("unknown step: %r" % (step,))
    return failed


def write_cast(path, session, scrub):
    """Writes the asciicast v2 file: the header line, then one [time, "o", data]
    event per chunk read from the pty. idle_time_limit caps the pauses a viewer
    has to sit through (the timeline's own sleeps stay visible)."""
    header = {"version": 2, "width": COLS, "height": ROWS, "idle_time_limit": 2,
              "env": {"TERM": "xterm-256color"}}
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(json.dumps(header) + "\n")
        for at, data in session.events:
            f.write(json.dumps([round(at, 3), "o", scrub(data)], ensure_ascii=False) + "\n")


def start_fake(port):
    """Starts fakellm in demo mode: slow enough that the line typed while the
    model answers is queued, and streamed so the answer arrives line by line."""
    fake = subprocess.Popen([sys.executable, "-I", os.path.join(REPO, "scripts", "ptysmoke", "fakellm.py"),
                             "--port", str(port), "--mode", "demo", "--delay", "1.6",
                             "--chunk", "6", "--chunk-delay", "0.05",
                             "--reply", REPLY_SURVEY,
                             "--demo-reply-queued", REPLY_QUEUED,
                             "--demo-reply-edit", REPLY_EDIT],
                            stdout=subprocess.PIPE, text=True)
    wait_port(fake)
    return fake


def dry_run():
    """Prints the timeline without a pty: the Windows host has none, so this is
    the self-check that the plan reads the way it is meant to."""
    print(f"demo timeline: {COLS}x{ROWS}, {len(STEPS)} steps, max 60s")
    for i, step in enumerate(STEPS, 1):
        kind = step[0]
        if kind == "wait":
            print(f"{i:>2}. wait  {step[2]:<28} /{step[1]}/")
        elif kind == "sleep":
            print(f"{i:>2}. pause {step[1]}s")
        elif kind == "type":
            print(f"{i:>2}. type  {step[1]}")
        elif kind == "enter":
            print(f"{i:>2}. enter")
        elif kind == "tab":
            print(f"{i:>2}. tab   (queue the line for the next turn)")


def record(args):
    """Prepares the scratch home, records the timeline and writes the cast; None
    when the timeline broke (the caller then exits non 0)."""
    home, ws = prepare(args.work)
    write_home(home, args.port)
    env = dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm-256color", SUGA_THEME="dark")
    env.pop("SUGA_CONFIG", None)  # the child reads its own SUGA_HOME config
    env.pop("NO_COLOR", None)  # the demo is recorded with colors
    fake = start_fake(args.port)
    s = None
    failed = ["suga chat did not start"]
    try:
        s = Recorder([args.bin, "chat"], env=env, cwd=ws, size=(COLS, ROWS))
        failed = play(s, STEPS)
    finally:
        if s is not None:
            # drain before closing: close() pumps and DROPS what is still coming
            s.drain(0.8)
            s.close(grace=0)
        fake.terminate()
        fake.wait(timeout=5)
    try:
        if failed:
            # The last screen tells what the timeline was waiting for (the cast is
            # not written: it would be a recording with a hole in it).
            if s is not None:
                print("\nlast output:\n" + "\n".join(s.text(0).splitlines()[-40:]), file=sys.stderr)
            print("\nthe timeline broke at: " + ", ".join(failed), file=sys.stderr)
            return None
        duration = s.duration()
        checks = [(f"duration {duration:.1f}s within {args.max_seconds:g}s", duration <= args.max_seconds)]
        cast_text = scrubber(home, ws)("\n".join(data for _, data in s.events))
        # A real directory or the user's home in the output would make the cast
        # unusable in a README (the scrubber above should already have removed
        # them, so this catches a new one that does not go through it).
        leaks = [p for p in (home, ws, REPO, os.path.expanduser("~")) if p and p in cast_text]
        checks.append(("no real directory in the output" + (" (%s)" % ", ".join(leaks) if leaks else ""),
                       not leaks))
        write_cast(args.out, s, scrubber(home, ws))
        for name, ok in checks:
            print(("PASS " if ok else "FAIL ") + name)
        if any(not ok for _, ok in checks):
            return False
        print(f"\nwrote {args.out}: {len(s.events)} events, {duration:.1f}s, {COLS}x{ROWS}")
        return True
    finally:
        if not args.keep:
            shutil.rmtree(os.path.abspath(args.work), ignore_errors=True)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--bin", default="tmp/smoke/suga", help="suga binary to record")
    p.add_argument("--out", default=os.path.join(REPO, "docs", "demo", "suga-chat.cast"),
                   help="cast file to write")
    p.add_argument("--work", default="tmp/demo", help="scratch dir (SUGA_HOME and the sample project)")
    p.add_argument("--port", type=int, default=18788, help="port of the fake model")
    p.add_argument("--max-seconds", type=float, default=60.0, help="longest the recording may take")
    p.add_argument("--keep", action="store_true", help="keep the scratch home and workspace")
    p.add_argument("--dry-run", action="store_true", help="print the timeline instead of recording")
    args = p.parse_args()
    if args.dry_run:
        dry_run()
        return
    if PTY_ERROR:
        raise SystemExit("recording needs a pty (Linux/macOS only); "
                         "--dry-run prints the timeline here instead: %s" % PTY_ERROR)
    args.bin = os.path.abspath(args.bin)
    if not os.path.exists(args.bin):
        raise SystemExit("binary not found: " + args.bin + " (make demo-cast builds it)")
    if not record(args):
        sys.exit(1)


if __name__ == "__main__":
    main()
