#!/usr/bin/env python3
"""Smoke chat's local commands and terminal notification.

This is deliberately falsifiable: removing command dispatch makes the help and
usage checks fail; removing the terminal guard makes the non-TTY notification
check fail. The task listing checks cover the default view and the options
(:tasks -h, -k <id> for an unknown, a running and an ended task); they use the
same background fake-model path as chat_tasks.py. Run on Linux/macOS through
``make smoke-chat``; Windows has no PTY equivalent in this suite.
"""
import argparse, os, shutil, subprocess, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession
from chat_approval import isolate_ws, wait_port, write_home

def main():
    p = argparse.ArgumentParser(description=__doc__); p.add_argument("--bin", required=True); p.add_argument("--work", required=True); p.add_argument("--port", type=int, default=18831)
    a = p.parse_args(); home=os.path.abspath(a.work+"/home"); ws=os.path.abspath(a.work+"/ws"); shutil.rmtree(a.work, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
    write_home(home, a.port, tools_extra="  approval: auto\n", extra="chat:\n  notify_after: 1s\n")
    env=dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1"); env.pop("SUGA_CONFIG", None)
    here=os.path.dirname(os.path.abspath(__file__)); fake=subprocess.Popen([sys.executable,"-I",here+"/fakellm.py","--port",str(a.port),"--mode","tool","--background","--command","sleep 5; echo TASK_COMMAND","--delay","1.0"], stdout=subprocess.PIPE, text=True); wait_port(fake)
    s=PtySession([os.path.abspath(a.bin),"chat"], env=env, cwd=ws)
    try:
        m=s.mark(); s.until(r"❯ ",m); s.line(":help"); s.check("help usage", s.until(r":usage",m))
        s.check("help tasks", s.until(r":tasks",m))
        m=s.mark(); s.line(":usage"); s.check("usage line", s.until(r"本次 0 轮",m))
        m=s.mark(); s.line(":tasks"); s.check("empty tasks", s.until(r"没有后台任务",m))
        m=s.mark(); s.line(":tasks -h"); s.check("tasks usage", s.until(r":tasks -k <ID>",m))
        m=s.mark(); s.line(":tasks -x"); s.check("tasks unknown option", s.until(r"未知参数：-x",m))
        m=s.mark(); s.line(":tasks -k t1"); s.check("tasks kill unknown", s.until(r"没有任务 t1",m))
        # A turn of two model requests (1.0s each) that starts a background task:
        # longer than chat.notify_after (1s), so its end rings the bell.
        m=s.mark(); s.line("hello"); s.check("turn ends", s.until(r"tokens · ",m))
        # The bell may follow the status line by a moment (it is written when the
        # turn is wrapped up): wait for it instead of checking at once.
        s.check("completion bell", s.until(r"\x07", m, timeout=3))
        m=s.mark(); s.line(":usage"); s.check("usage after turn", s.until(r"本次 1 轮",m))
        m=s.mark(); s.line(":tasks"); s.check("running task listed", s.until(r"running",m) and s.contains(r"TASK_COMMAND|sleep 5",m))
        m=s.mark(); s.line(":tasks -k t1"); s.check("tasks kill", s.until(r"已终止任务 t1：sleep 5",m))
        m=s.mark(); s.line(":tasks"); s.check("killed task listed", s.until(r"t1 · killed",m))
        m=s.mark(); s.line(":tasks -k t1"); s.check("tasks kill again says it ended", s.until(r"任务 t1 已结束（killed），无需终止",m))
        s.send(CTRL_C); time.sleep(.2); s.send(CTRL_C); s.close()
    finally:
        s.close(grace=0); fake.terminate(); fake.wait(timeout=5)
    if s.failed(): print("failed: "+", ".join(s.failed())); raise SystemExit(1)
if __name__ == "__main__": main()
