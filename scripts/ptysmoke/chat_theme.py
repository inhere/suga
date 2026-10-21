#!/usr/bin/env python3
"""PTY smoke for chat's theme option (chat.theme / SUGA_THEME, suga-exbd).

Run A drives a normal chat (theme auto, no COLORFGBG -> dark) and checks the
dark SGR of a heading, an inline code span and a highlighted go keyword. Run B
sets SUGA_THEME=light and checks the light SGR of the same elements. The checks
fail when the theme no longer reaches the markdown renderer (both runs print the
other run's palette) or when light falls back to the dark palette.

The raw SGR is read with until_raw() because text() strips escape codes. The
Linux/macOS supervisor runs this through make smoke-chat; a Windows console
cannot be driven this way.
"""
import argparse, os, shutil, subprocess, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession
from chat_approval import isolate_ws, wait_port, write_home


def reply_text():
    """One answer carrying all the elements the theme paints: a heading, an
    inline code span and a one line go block."""
    return ("# 主题测试\n\n运行 `make smoke`。\n\n```go\nfunc demo() {\n}\n```\n")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bin', default='tmp/smoke/suga')
    p.add_argument('--work', default='tmp/smoke-theme')
    p.add_argument('--port', type=int, default=18894)
    p.add_argument('--keep', action='store_true')
    a = p.parse_args()
    work = os.path.abspath(a.work); home = os.path.join(work, 'home')
    # The fake model serves both chats.
    fake = subprocess.Popen([sys.executable, '-I', os.path.join(os.path.dirname(os.path.abspath(__file__)),
                             'fakellm.py'), '--port', str(a.port), '--mode', 'text',
                             '--delay', '0.2', '--reply', reply_text()],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        # Run A: theme auto without COLORFGBG stays dark.
        ws = os.path.join(work, 'ws-dark')
        shutil.rmtree(ws, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
        write_home(home, a.port)
        env = {k: v for k, v in os.environ.items() if k not in ('NO_COLOR', 'COLORFGBG', 'SUGA_THEME')}
        env.update(SUGA_HOME=home, HOME=home, SUGA_HOOKS='off', TERM='xterm')
        s = PtySession([os.path.abspath(a.bin), 'chat'], env=env, cwd=ws)
        m = s.mark()
        s.until(r'Suga chat · fake/m1', m)
        s.line('渲染一下')
        s.check('dark heading keeps bold cyan', s.until_raw(r'\x1b\[1;36m主题测试', m))
        s.check('dark inline code keeps bright yellow', s.until_raw(r'\x1b\[93mmake smoke', m))
        s.check('dark keyword keeps cyan', s.until_raw(r'\x1b\[36mfunc', m))
        s.check('light palette is absent dark', not s.contains_raw(r'\x1b\[1;34m', m))
        s.send(CTRL_C); s.until(r'再按一次 Ctrl\+C 退出', m); s.send(CTRL_C)
        s.until(r'续接：suga chat --resume (?:sess_|sg_|s_)', m)
        s.close()
        # Run B: SUGA_THEME=light paints the same elements with the light palette.
        ws = os.path.join(work, 'ws-light')
        shutil.rmtree(ws, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
        env.update(SUGA_THEME='light')
        s = PtySession([os.path.abspath(a.bin), 'chat'], env=env, cwd=ws)
        m = s.mark()
        s.until(r'Suga chat · fake/m1', m)
        s.line('再渲染一下')
        s.check('light heading is bold blue', s.until_raw(r'\x1b\[1;34m主题测试', m))
        s.check('light inline code is magenta', s.until_raw(r'\x1b\[35mmake smoke', m))
        s.check('light keyword is blue', s.until_raw(r'\x1b\[34mfunc', m))
        s.check('light palette is absent dark', not s.contains_raw(r'\x1b\[1;36m', m))
        s.send(CTRL_C); s.until(r'再按一次 Ctrl\+C 退出', m); s.send(CTRL_C)
        s.until(r'续接：suga chat --resume (?:sess_|sg_|s_)', m)
        s.close()
    finally:
        if s: s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5)
        if not a.keep: shutil.rmtree(work, ignore_errors=True)
    failed = s.failed() if s else ['setup']
    print(f'\n{len(s.results)-len(failed) if s else 0}/{len(s.results) if s else 0} passed')
    if failed: print('failed: ' + ', '.join(failed)); sys.exit(1)


if __name__ == '__main__':
    main()
