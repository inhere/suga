#!/usr/bin/env python3
"""PTY smoke for chat diff preview and the keyboard-only :diff command.

This deliberately checks the visible diff, not only the edited file: removing
Renderer.showToolResult (or the pending ApprovalDiff call) makes these checks
fail. The Linux/macOS supervisor runs it through make smoke-chat.
"""
import argparse, os, shutil, subprocess, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession
from chat_approval import isolate_ws, wait_port, write_home

def main():
    p=argparse.ArgumentParser(description=__doc__); p.add_argument('--bin',default='tmp/smoke/suga'); p.add_argument('--work',default='tmp/smoke-diff'); p.add_argument('--port',type=int,default=18798); p.add_argument('--keep',action='store_true'); a=p.parse_args()
    work=os.path.abspath(a.work); home=os.path.join(work,'home'); ws=os.path.join(work,'ws'); shutil.rmtree(work,ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
    with open(os.path.join(ws,'target.txt'),'w',encoding='utf-8') as f: f.write('line one\nSMOKE_TARGET old\nline three\n')
    write_home(home,a.port)
    env=dict(os.environ,SUGA_HOME=home,HOME=home,SUGA_HOOKS='off',TERM='xterm',NO_COLOR='1'); here=os.path.dirname(os.path.abspath(__file__))
    fake=subprocess.Popen([sys.executable,'-I',os.path.join(here,'fakellm.py'),'--port',str(a.port),'--mode','edit','--delay','0.2'],stdout=subprocess.PIPE,text=True)
    s=None
    try:
        wait_port(fake); s=PtySession([os.path.abspath(a.bin),'chat'],env=env,cwd=ws); m=s.mark(); s.until(r'Suga chat · fake/m1',m); s.line('改文件'); s.check('approval diff old',s.until(r'-  2 SMOKE_TARGET old',m)); s.check('approval diff new',s.until(r'\+  2 SMOKE_TARGET new',m)); s.check('approval question',s.until(r'\[y\]是',m)); m=s.mark(); s.line('y'); s.check('result diff',s.until(r'diff target.txt',m)); s.check('the edit is applied',s.until(r'tokens · ',m) and not s.contains(r'用户拒绝执行',m)); m=s.mark(); s.line(':diff'); s.check('full diff command',s.until(r'\+  2 SMOKE_TARGET new',m)); s.send(CTRL_C); s.until(r'再按一次 Ctrl\+C 退出',m); s.send(CTRL_C); s.until(r'续接：suga chat --resume (?:sess_|s_)',m); s.close()
    finally:
        if s: s.close(grace=0)
        fake.terminate(); fake.wait(timeout=5)
        if not a.keep: shutil.rmtree(work,ignore_errors=True)
    failed=s.failed() if s else ['setup']; print(f'\n{len(s.results)-len(failed) if s else 0}/{len(s.results) if s else 0} passed')
    if failed: print('failed: '+', '.join(failed)); sys.exit(1)
if __name__=='__main__': main()
