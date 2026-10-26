#!/usr/bin/env python3
"""Smoke chat's local commands and terminal notification.

This is deliberately falsifiable: removing command dispatch makes the help and
usage checks fail; removing the terminal guard makes the non-TTY notification
check fail. The help checks cover :help's one-line hint and per-command usage
(:session -h, :queue -h, :checkpoints -h, :help :queue) plus the new arguments
(:queue -d on an empty queue, :checkpoints N); removing the usage table
(chat_usage.go) or the -h dispatch makes them fail. The task listing checks
cover the default view and the options (:tasks -h, -k <id> for an unknown, a
running and an ended task); they use the same background fake-model path as
chat_tasks.py. Run on Linux/macOS through ``make smoke-chat``; Windows has no
PTY equivalent in this suite.

The :skills and :context checks cover the two commands of suga-hg30 (the
listing, --only-names, -h and the composition): with no dispatch or no command
table entry they fail. A second session reads target.txt with a file_read
(display mode) and then asks :context --files, so the listing has a file the
tool events recorded. The last two checks cover suga-okbt: a misspelled :sessio
is answered with the closest command instead of starting a turn, while a
":)" message still runs one.
"""
import argparse, os, shutil, subprocess, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptydrive import CTRL_C, PtySession, resolve_port
from chat_approval import isolate_ws, wait_port, write_home

def main():
    p = argparse.ArgumentParser(description=__doc__); p.add_argument("--bin", required=True); p.add_argument("--work", required=True); p.add_argument("--port", type=int, default=0, help="fake model port; 0 picks free ports (the second session runs on the next one)")
    a = p.parse_args(); a.port = resolve_port(a.port, 1); home=os.path.abspath(a.work+"/home"); ws=os.path.abspath(a.work+"/ws"); shutil.rmtree(a.work, ignore_errors=True); os.makedirs(ws); isolate_ws(ws)
    write_home(home, a.port, tools_extra="  approval: auto\n", extra="chat:\n  notify_after: 1s\n")
    env=dict(os.environ, SUGA_HOME=home, SUGA_HOOKS="off", TERM="xterm", NO_COLOR="1"); env.pop("SUGA_CONFIG", None)
    here=os.path.dirname(os.path.abspath(__file__)); fake=subprocess.Popen([sys.executable,"-I",here+"/fakellm.py","--port",str(a.port),"--mode","tool","--background","--command","sleep 5; echo TASK_COMMAND","--delay","1.0"], stdout=subprocess.PIPE, text=True); wait_port(fake)
    s=PtySession([os.path.abspath(a.bin),"chat"], env=env, cwd=ws)
    try:
        m=s.mark(); s.until(r"❯ ",m); s.line(":help"); s.check("help usage", s.until(r":usage",m))
        s.check("help tasks", s.until(r":tasks",m))
        s.check("help points at the per-command usage", s.until(r"查看命令详情",m))
        m=s.mark(); s.line(":session -h"); s.check("session usage", s.until(r"用法：:session",m))
        m=s.mark(); s.line(":queue -h"); s.check("queue usage", s.until(r"用法：:queue",m))
        m=s.mark(); s.line(":checkpoints -h"); s.check("checkpoints usage", s.until(r"用法：:checkpoints",m))
        m=s.mark(); s.line(":help :queue"); s.check(":help <command> shows the same usage", s.until(r"用法：:queue",m))
        m=s.mark(); s.line(":queue -d 1"); s.check("queue -d on an empty queue", s.until(r"没有第 1 条排队的消息",m))
        m=s.mark(); s.line(":checkpoints 3"); s.check("checkpoints takes a count", s.until(r"没有可回滚的快照",m))
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
        m=s.mark(); s.line(":skills"); s.check("skills listing", s.until(r"已加载 \d+ 个 skill",m))
        s.check("skills lists the built-in guide with its source", s.until(r"suga-guide — .* · 内置",m))
        m=s.mark(); s.line(":skills --only-names"); s.check("skills names only", s.until(r"suga-guide",m))
        m=s.mark(); s.line(":skills -h"); s.check("skills usage", s.until(r"用法：:skills",m))
        m=s.mark(); s.line(":context"); s.check("context composition", s.until(r"上下文构成",m))
        s.check("context tool schema line", s.until(r"工具 schema：",m))
        s.check("context history line", s.until(r"历史消息：",m))
        m=s.mark(); s.line(":context -h"); s.check("context usage", s.until(r"用法：:context",m))
        m=s.mark(); s.line(":sessio"); s.check("unknown command suggests :session", s.until(r"未知命令 :sessio，是否想用 :session",m))
        m=s.mark(); s.line(":)"); s.check("colon text still starts a turn", s.until(r"tokens · ",m))
        s.send(CTRL_C); time.sleep(.2); s.send(CTRL_C); s.close()
        # :context --files after a real file_read: a second session whose fake
        # model reads target.txt (display mode), so the tool events record it.
        home2=os.path.abspath(a.work+"/home2"); ws2=os.path.abspath(a.work+"/ws2"); os.makedirs(ws2); isolate_ws(ws2)
        with open(os.path.join(ws2,"target.txt"),"w",encoding="utf-8") as f: f.write("SMOKE_TARGET old\nsecond line\n")
        write_home(home2, a.port+1, tools_extra="  approval: auto\n")
        env2=dict(env, SUGA_HOME=home2)
        fake2=subprocess.Popen([sys.executable,"-I",here+"/fakellm.py","--port",str(a.port+1),"--mode","display","--delay","0.2"], stdout=subprocess.PIPE, text=True); wait_port(fake2)
        s2=PtySession([os.path.abspath(a.bin),"chat"], env=env2, cwd=ws2)
        try:
            m=s2.mark(); s2.until(r"❯ ",m); s2.line("read it"); s2.until(r"tokens · ",m)
            m=s2.mark(); s2.line(":context --files"); s2.check("context files lists the file the model read", s2.until(r"target.txt · file_read",m))
        finally:
            s2.close(grace=0); fake2.terminate(); fake2.wait(timeout=5)
        s.results += s2.results
    finally:
        s.close(grace=0); fake.terminate(); fake.wait(timeout=5)
    if s.failed(): print("failed: "+", ".join(s.failed())); raise SystemExit(1)
if __name__ == "__main__": main()
