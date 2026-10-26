"""Drive an interactive suga process in a pseudo terminal (Linux/macOS).

Lessons built in (see docs/plans/2026-10-09-chat-write-approval-retrospective.md):
  - assertions only look at output produced after a mark, never the whole buffer;
  - wait for an expected pattern before typing the next input (the chat clears
    pending input before an approval question, so early input is discarded);
  - Enter is "\\r" in raw mode, Ctrl-C is "\\x03";
  - check result lines ("⎿ ok: ...") rather than text a prompt also echoes.
"""
import fcntl
import os
import pty
import re
import select
import socket
import struct
import termios
import time
import unicodedata

import fcntl
import struct
import termios

ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")
ENTER = "\r"
CTRL_C = "\x03"
CTRL_D = "\x04"
CTRL_U = "\x15"

# The escape sequences the screen model understands: a CSI with its parameters and
# final letter, and an OSC (a title) ended by BEL or ST.
CSI = re.compile(r"\x1b\[([0-9;?]*)([A-Za-z])")
OSC = re.compile(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")


def char_width(ch):
    """The columns one character takes: 2 for the East Asian wide and full width
    ones (the terminal's own rule), 1 for everything else."""
    return 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1


def _bindable(port):
    """Whether nothing else on this machine holds the loopback port right now.
    A port that cannot be bound is in use, so the smoke would fail on it."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        try:
            sock.bind(("127.0.0.1", port))
        except OSError:
            return False
    return True


def free_port(span=0):
    """A local port that is free right now: the scenarios used to pin ports
    (18795 and neighbours) and failed whenever another service held one, so a
    scenario picks its port instead.

    span keeps the ports the scenario derives from its base free too: the
    returned base and the next span ones are all bindable, so a script that
    starts one fake model per group at base, base+1, ... cannot collide.
    """
    for _ in range(50):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", 0))
            base = sock.getsockname()[1]
        if all(_bindable(base + offset) for offset in range(1, span + 1)):
            return base
    raise RuntimeError(f"no free port range of {span + 1} ports found")


def resolve_port(value, span=0):
    """The base port of a scenario: an explicit --port N keeps N (and the
    scenario's N+1..N+span), the default 0 picks a free range (free_port)."""
    return value or free_port(span)


class Screen:
    """A minimal VT screen: enough of a terminal to tell where chat's lines ended
    up - whether a long reply kept its text, whether the input line is still one
    line of its own - instead of only grepping the byte stream.

    It handles CR, LF, backspace, ESC[K (erase to the end of the line), ESC[2K
    (erase the line), ESC[nA/B/C/D (cursor up/down/right/left), ESC[nG (column)
    and wraps at the window width; every other escape (colors, titles, cursor
    visibility) is skipped. Rows are kept as they were written (no scrolling), and
    a wide character marks its two columns as one.

    feed() takes raw pty output (text_raw), screen_lines() the rows without their
    trailing spaces.
    """

    def __init__(self, width=80, height=200):
        self.width, self.height = width, height
        self.rows = [[]]
        self.x = self.y = 0

    def feed(self, data):
        i = 0
        while i < len(data):
            ch = data[i]
            if ch == "\r":
                self.x, i = 0, i + 1
                continue
            if ch == "\n":
                self.y, i = self.y + 1, i + 1
                continue
            if ch == "\b":
                self.x, i = max(0, self.x - 1), i + 1
                continue
            if ch == "\x07":
                i += 1
                continue
            if ch == "\x1b":
                i = self._escape(data, i)
                continue
            self._put(ch)
            i += 1

    def _escape(self, data, i):
        """Handles the escape sequence at i and returns the next index."""
        match = CSI.match(data, i)
        if match:
            self._csi(match.group(1), match.group(2))
            return match.end()
        match = OSC.match(data, i)
        if match:
            return match.end()
        if data.startswith("\x1b[", i):
            # A sequence cut in half by a read boundary: drop the rest rather than
            # printing its bytes as text.
            return len(data)
        # A two character escape (charset, cursor save/restore, ...): skip both.
        return min(len(data), i + 2)

    def _csi(self, params, final):
        n = int(params) if params.isdigit() else 1
        if final == "A":
            self.y = max(0, self.y - n)
        elif final == "B":
            self.y += n
        elif final == "C":
            self.x += n
        elif final == "D":
            self.x = max(0, self.x - n)
        elif final == "G":
            self.x = max(0, n - 1)
        elif final == "K":
            self._erase_line(params)
        elif final == "J":
            self._erase_display(n)
        # Everything else (SGR colors, cursor visibility, ...) leaves the text alone.

    def _erase_line(self, params):
        row = self._cells(self.y)
        if params == "2":
            row[:] = []
            return
        if params == "1":
            for i in range(min(self.x + 1, len(row))):
                row[i] = " "
            return
        if self.x < len(row):
            del row[self.x:]

    def _erase_display(self, n):
        if n not in (2, 3):
            return
        self.rows = [[]]
        self.x = self.y = 0

    def _cells(self, y):
        while len(self.rows) <= y:
            self.rows.append([])
        return self.rows[y]

    def _put(self, ch):
        width = char_width(ch)
        if width == 2 and self.width >= 2 and self.x == self.width - 1:
            self._put(" ")  # a wide character never straddles the end of a row
        row = self._cells(self.y)
        while len(row) < self.x:
            row.append(" ")
        if self.x < len(row):
            row[self.x] = ch
        else:
            row.append(ch)
        if width == 2:
            # The second column of a wide character stays empty (its own cell).
            if len(row) <= self.x + 1:
                row.append("")
            else:
                row[self.x + 1] = ""
        self.x += width
        while self.x >= self.width:
            self.x, self.y = self.x - self.width, self.y + 1

    def screen_lines(self):
        """The rows of the screen, trailing spaces removed; rows the output never
        reached are empty strings (and dropped at the end)."""
        lines = ["".join(row).rstrip() for row in self.rows]
        while lines and not lines[-1]:
            lines.pop()
        return lines

    def row(self, index):
        """One row by its index ("" when the output never reached it)."""
        lines = self.screen_lines()
        return lines[index] if -len(lines) <= index < len(lines) else ""


def input_row(session, cols, prompts=("❯ ", "↳ ")):
    """The row the input line occupies, as the terminal would show it: the last row
    of the screen model that starts with one of the prompts, trailing spaces
    removed (so a cleared line reads as the prompt alone, and a line holding text
    reads as prompt + text).

    cols must be the pty's real width: the screen model lays the rows out with it
    (PtySession.resize, or the size the session was started with)."""
    screen = Screen(width=cols)
    screen.feed(session.text_raw(0))
    rows = screen.screen_lines()
    return next((row for row in reversed(rows)
                 if any(row.startswith(p.strip()) for p in prompts)), "")


def _self_test():
    """Checks the screen model itself:
    python3 -c "import ptydrive; ptydrive._self_test()" ."""
    s = Screen(width=10)
    s.feed("hello\r\nworld")
    assert s.screen_lines() == ["hello", "world"], s.screen_lines()

    s = Screen(width=10)  # a line longer than the window wraps
    s.feed("abcdefghijklm")
    assert s.screen_lines() == ["abcdefghij", "klm"], s.screen_lines()

    s = Screen(width=10)  # ESC[2K erases the row it is on
    s.feed("abc\r\n\x1b[2Kdef")
    assert s.screen_lines() == ["abc", "def"], s.screen_lines()

    s = Screen(width=10)  # up one row, column 0, erase to the end
    s.feed("first\r\nsecond\x1b[1A\r\x1b[Kthird")
    assert s.screen_lines() == ["third", "second"], s.screen_lines()

    s = Screen(width=10)  # colors, a title and a column move leave the text alone
    s.feed("\x1b[31mred\x1b[0m \x1b]0;suga\x07\x1b[9Gok")
    assert s.screen_lines() == ["red     ok"], s.screen_lines()

    s = Screen(width=8)  # a wide character takes two columns
    s.feed("宽" * 5)
    assert s.screen_lines() == ["宽宽宽宽", "宽"], s.screen_lines()

    s = Screen(width=10)  # a sequence cut in half does not become text
    s.feed("ok\x1b[")
    assert s.screen_lines() == ["ok"], s.screen_lines()

    print("ptydrive screen model: ok")


class PtySession:
    def __init__(self, argv, env=None, cwd=None, size=None):
        """size is the pty's initial (cols, rows); None leaves it unset, so the
        child sees a 0x0 window (suga then falls back to its own 80 columns)."""
        self.buf = b""
        self.results = []
        self.pid, self.fd = pty.fork()
        if self.pid == 0:  # child
            if cwd:
                os.chdir(cwd)
            os.execve(argv[0], argv, env or dict(os.environ))
        if size:
            # The window size belongs to the child (it reads it through GetSize and
            # gets a SIGWINCH for it), so the parent sets it after the fork.
            self.resize(size[0], size[1])

    def resize(self, cols, rows=24):
        """Resize the pty's window, the way a person resizes the terminal: the
        kernel hands the child SIGWINCH (and chat notices on its next poll where
        there is no signal). This is the width the program lays lines out with and
        the width a terminal would wrap them at, so the screen model follows it."""
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    # --- output ---------------------------------------------------------
    def pump(self, seconds):
        """Read and DROP output for seconds (keeps the child from blocking on
        a full pty). To wait and still check what came, use until."""
        end = time.time() + seconds
        while time.time() < end:
            ready, _, _ = select.select([self.fd], [], [], 0.1)
            if not ready:
                continue
            try:
                data = os.read(self.fd, 4096)
            except OSError:
                return False
            if not data:
                return False
        return True

    def _read_once(self, timeout):
        ready, _, _ = select.select([self.fd], [], [], timeout)
        if ready:
            try:
                data = os.read(self.fd, 4096)
            except OSError:
                return False
            if not data:
                return False
            self.buf += data
        return True

    def drain(self, seconds=0.3):
        """Read pending output INTO the buffer (pump reads and drops it), so a
        contains() check sees what the child printed meanwhile."""
        end = time.time() + seconds
        while time.time() < end:
            if not self._read_once(0.15):
                return

    def mark(self):
        """Position to search from; use it right before sending input."""
        return len(self.buf)

    def text(self, start=0):
        return ANSI.sub("", self.buf[start:].decode("utf-8", "replace"))

    def until(self, pattern, start, timeout=20.0):
        """Wait until pattern (regex) appears in the output after start."""
        end = time.time() + timeout
        while time.time() < end:
            if re.search(pattern, self.text(start)):
                return True
            if not self._read_once(0.2):
                return bool(re.search(pattern, self.text(start)))
        return False

    def contains(self, pattern, start):
        return bool(re.search(pattern, self.text(start)))

    def text_raw(self, start=0):
        """The raw output after start, escape codes kept: text() strips them, so
        it hides color-only output such as the background of an input line."""
        return self.buf[start:].decode("utf-8", "replace")

    def until_raw(self, pattern, start, timeout=20.0):
        """Wait until pattern (regex) appears in the raw output after start."""
        end = time.time() + timeout
        while time.time() < end:
            if re.search(pattern, self.text_raw(start)):
                return True
            if not self._read_once(0.2):
                return bool(re.search(pattern, self.text_raw(start)))
        return False

    def contains_raw(self, pattern, start):
        return bool(re.search(pattern, self.text_raw(start)))

    # --- input ----------------------------------------------------------
    def send(self, s):
        os.write(self.fd, s.encode())

    def line(self, s):
        self.send(s + ENTER)

    # --- checks ---------------------------------------------------------
    def check(self, name, ok):
        self.results.append((name, bool(ok)))
        print(("PASS " if ok else "FAIL ") + name, flush=True)
        return ok

    def close(self, grace=2.0):
        self.pump(grace)
        try:
            os.kill(self.pid, 9)
        except OSError:
            pass
        try:
            os.waitpid(self.pid, 0)
        except OSError:
            pass

    def failed(self):
        return [name for name, ok in self.results if not ok]
