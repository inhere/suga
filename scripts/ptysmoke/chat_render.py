#!/usr/bin/env python3
"""PTY smoke for chat's markdown rendering extras: code highlighting, GFM table
alignment and the fold of a long code block with the :code command.

The fake model answers with a Chinese table and a 45 line go code block. The
checks fail when the table buffering is removed (the table stays as the model
wrote it), when the fold limit is removed (no "… 还有 N 行" line) or when
highlighting is off (no 16 color SGR sequence). The Linux/macOS supervisor runs
it through make smoke-chat; a Windows console cannot be driven this way.
"""
import argparse, os, shutil, subprocess, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession
from chat_approval import isolate_ws, wait_port, write_home

CODE_LINES = 45


def reply_text():
    """The answer: a GFM table, then a code block longer than chat.code_max_lines."""
    body = "\n".join('    fmt.Println("step %d")' % (i + 1) for i in range(CODE_LINES))
    return ("回答如下：\n\n"
            "| 名称 | 状态 |\n"
            "| --- | --- |\n"
            "| 构建 | 通过 |\n"
            "| 测试 | 通过 |\n\n"
            "```go\n"
            "func demo() {\n" + body + "\n}\n"
            "```\n\n"
            "完成。\n")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bin', default='tmp/smoke/suga')
    p.add_argument('--work', default='tmp/smoke-render')
    p.add_argument('--port', type=int, default=18812)
    p.add_argument('--keep', action='store_true')
    a = p.parse_args()
    work = os.path.abspath(a.work); home = os.path.join(work, 'home'); ws = os.path.join(work, 'ws')
    shutil.rmtree(work, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
    write_home(home, a.port)
    # A colored terminal: TERM is set and NO_COLOR is not, so the chat renders
    # markdown, colors the code and aligns the table.
    env = {k: v for k, v in os.environ.items() if k != 'NO_COLOR'}
    env.update(SUGA_HOME=home, HOME=home, SUGA_HOOKS='off', TERM='xterm')
    here = os.path.dirname(os.path.abspath(__file__))
    fake = subprocess.Popen([sys.executable, '-I', os.path.join(here, 'fakellm.py'), '--port', str(a.port),
                             '--mode', 'text', '--delay', '0.2', '--reply', reply_text()],
                            stdout=subprocess.PIPE, text=True)
    s = None
    try:
        wait_port(fake)
        s = PtySession([os.path.abspath(a.bin), 'chat'], env=env, cwd=ws)
        m = s.mark()
        s.until(r'Suga chat · fake/m1', m)
        s.line('渲染一下')
        # The table is aligned by display width (CJK counts two columns) and its
        # header is bold.
        s.check('table header aligned', s.until(r'│ 名称 │ 状态 │', m))
        s.check('table row aligned', s.until(r'│ 构建 │ 通过 │', m))
        s.check('table separator', s.until(r'─{10,}', m))
        # A go block is colored while it streams: keywords are cyan and strings
        # green (16 color ANSI); text() strips escape codes, so this reads the raw
        # output.
        s.check('highlighted keyword', s.until_raw('\x1b\\[36mfunc', m))
        s.check('highlighted string', s.until_raw('\x1b\\[32m"step 1"', m))
        # Past chat.code_max_lines the block folds and names :code.
        s.check('fold notice', s.until(r'… 还有 %d 行（:code 查看全部）' % (CODE_LINES + 2 - 40), m))
        s.check('the folded line is not printed', not s.contains(r'step %d' % CODE_LINES, m))
        s.check('turn ended', s.until(r'tokens · ', m))
        # :code shows the block it folded whole, again highlighted.
        m = s.mark()
        s.line(':code')
        s.check(':code label', s.until(r'第 1 个代码块（go，共 %d 行）' % (CODE_LINES + 2), m))
        s.check(':code shows the hidden line', s.until(r'step %d' % CODE_LINES, m))
        # A block that does not exist only hints.
        m = s.mark()
        s.line(':code 9')
        s.check(':code N hint', s.until(r'上一条回答只有 1 个代码块', m))
        s.send(CTRL_C); s.until(r'再按一次 Ctrl\+C 退出', m); s.send(CTRL_C)
        s.until(r'续接：suga chat --resume sess_', m)
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
