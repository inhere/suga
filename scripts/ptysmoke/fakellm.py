#!/usr/bin/env python3
"""Fake OpenAI-compatible chat model that streams SSE, for suga smoke tests.

Modes (--mode):
  tool  every user message first gets a shell_exec call (--command), and the
        request after the tool result gets the text --reply. (default)
  text  every request gets the text --reply.
  edit  every user message gets file_grep (pattern SMOKE_TARGET), then after
        its result file_edit on target.txt ("SMOKE_TARGET old" -> "... new");
        messages containing 删除/新建/附加/shell select file_remove/file_write/
        shell_exec so one fake server can drive undo scenarios, then text --reply.
  memory  every user message gets shell_exec (--command), then memory add
        (kind feedback, title "Smoke rule"), then the text --reply; a user
        message containing "直接" skips the shell_exec.
  refs  every user message gets file_refs for --ref-name on --ref-file (one
        file), then file_refs for the same name on --ref-dir (a directory),
        then the text --reply: the reference-search smoke.
  empty-final  first requests file_write (--write-path/--write-body), then return an
        empty streamed completion after the tool result, including resamples.
  preferred  every user message first gets the MCP tool named by --preferred-tool,
        then returns the text --reply. The request log includes visible tool names.
  session  every user message that names a peer gets one session_message send
        (--session-text) to it, then the text --reply: the smoke of session to
        session messages. A received session message names its sender in the tag
        it carries ("[来自会话 <短名称>（<id>）的消息] "), a person's own line names
        it as "发给 <短名称>：…"; a message that names nobody only answers.
  adddir  every user message first gets file_read (--read-path), then
        file_write (--write-path with --write-body), then the text --reply:
  image   one file_read of --read-path (a picture) and then the text --reply;
        the smoke for --add-dir, where both paths live outside the workspace.
        A user message containing "只读" or "只写" narrows it to that one call,
        so a smoke can drive the read and the write of one turn separately.
  display  one turn with three calls whose lines chat shows differently:
        file_read of target.txt with a line window (a summary of its own),
        shell_exec of a two-line command (the first line and an ellipsis), and
        update_plan (no summary of its own: the JSON stays): the smoke of the
        per-tool tool lines (chat_tool_lines.py).
  loop  one turn with four identical file_read calls of loop_target.txt, then
        the text --reply: the smoke of the loop guard (chat_loop_guard.py).
        Each request logs an extra line {"loop": true, "step", "hint_seen",
        "hint_kind"} saying whether it carried the guard's reminder.
  loop-edit  the same successful shell_exec (--loop-command, identical output)
        four times with a successful file_edit in between: the guard must stay
        quiet because the workspace changed between two runs.
  loop-fail  the same failing shell_exec (--loop-fail-command) three times: the
        guard's R3 reminder for a command that keeps failing.
  nudge one turn that writes --write-path and then answers --reply (a closing
        sentence that promises to continue), without running any command: the
        turn hits both "stopped too early" rules, so chat shows its gray hint
        (chat_nudge.py). Chat never pushes the model back, so the turn ends here.
  deferred
        one tool_search {"select": "memory"}, then one tool_call of the loaded
        memory tool (the deferred-native proxy), then the text --reply: the
        smoke of tools.deferred_native: auto (chat_deferred_native.py).
  demo  the timeline of the recorded demo (scripts/demo/record.py): a line
        containing "改成" edits --demo-file (chat asks the person first), one
        containing "运行" only explains how to run it, anything else reads
        --demo-readme and --demo-file and answers --reply. The two other turns
        answer --demo-reply-queued and --demo-reply-edit.

--delay seconds are slept before each response, so a test can type while a
turn is running. --chunk splits a streamed text reply into pieces of that many
characters with --chunk-delay seconds between them (a reply that arrives slowly,
one line at a time), and --lines/--wide turn the text reply into a long answer of
that many lines, each padded to that many columns (a line wider than the
terminal window). --fail-first N answers the first N requests with HTTP 503;
--overload-first M then answers the next M with a 200 carrying only a temporary
API error (server_is_overloaded: an SSE error event, or a JSON error body for a
non-streamed call); --break-first M then streams half of the next M responses'
text and drops the connection (a broken stream), for the retry smoke. --fail-from N
[--fail-count M] fails from the Nth request on, M times (M <= 0: every later
request), which lets the first steps of a turn run and then ends it with a
failure (the interrupted-turn replay smoke). --background makes
the tool-mode shell_exec call a background task; a user message that is a
background task notice or reminder ("[background task") gets the text reply;
--watch-paths a,b adds watch_paths (a watch task). --agent makes the
tool-mode call the agent tool (description "smoke survey", with background
when --background); a sub-agent's request (its system prompt names a "suga
sub-agent") greps SMOKE once, then reports "SUBREPORT: ..." after
--child-delay seconds more. A request with "stream": false gets one JSON completion
instead of SSE (the compaction summary). --prompt-tokens sets the reported prompt usage (the idle
consolidation compares it with the context threshold). --no-usage leaves usage
out of every completion, for estimation and missing-usage smoke scenarios.
Each request is logged as one JSON line on stdout: "user" is the last user
message, "seen" is one token per message in order, which is what a smoke checks
when it has to see more than the last message ("user:<head>" of the text, "a"
or "a:file_write" for an assistant message and its tool calls, "tool:<head>"
for a tool result), and "tail" the last few messages as {"role", "text"} (a
steer a person inserted into the running turn shows up there, after the tool
result).

  python3 -I fakellm.py --port 18795 --mode tool --delay 1
"""
import argparse
import http.server
import json
import re
import sys
import threading
import time


def message_token(m):
    """One short token for one request message, so a smoke can see what the
    model received and in what order: "user:<head>", "a:file_write",
    "tool:edited ...". The provider sends role=tool messages without a name,
    so the head of their content is what identifies them (openai.go builds
    "role", "content", "tool_call_id" only)."""
    role = str(m.get("role") or "?")
    if role == "assistant":
        names = [c.get("function", {}).get("name", "?") for c in (m.get("tool_calls") or [])]
        return "a:" + ",".join(names) if names else "a"
    head = " ".join(str(m.get("content") or "").split())[:40]
    return "%s:%s" % (role, head)


def is_image_attachment(message):
    """A user message whose content parts include an image_url: the picture a
    tool result carries (suga appends it after the tool message for OpenAI)."""
    content = message.get("content")
    return isinstance(content, list) and any(
        isinstance(part, dict) and part.get("type") == "image_url" for part in content)


def last_index(messages, role):
    """The index of the last message with role (-1 when there is none)."""
    for i in range(len(messages) - 1, -1, -1):
        if messages[i].get("role") == role:
            return i
    return -1


def message_tail(messages, count=4, limit=90):
    """The last few messages as {"role", "text"}, so a smoke can see what a
    request actually carried: the tool result of the step and any message a
    person inserted into the running turn (a steer) come after it."""
    out = []
    for m in messages[-count:]:
        content = m.get("content")
        if isinstance(content, list):
            content = " ".join(str(part.get("text", "")) for part in content if isinstance(part, dict))
        text = " ".join(str(content or "").split())
        calls = m.get("tool_calls") or []
        if calls:
            names = ",".join(str((c.get("function") or {}).get("name", "?")) for c in calls)
            text = (text + " tool_calls:" + names).strip()
        out.append({"role": m.get("role"), "text": text[:limit]})
    return out



def edit_call_sequence(args, user_text):
    """Choose a small deterministic edit-mode script for the undo smoke.

    The ordinary chat_edit flow stays grep -> edit. Named prompts select one
    write tool so the undo scenario can exercise new files, trash and shell-only
    turns without adding a second fake provider protocol.
    """
    text = str(user_text)
    if "收到回滚通知" in text or "回滚通知" in text:
        return []
    if "删除" in text:
        return [("file_remove", {"path": args.remove_path})]
    if "新建" in text:
        # A new file in the workspace (undo deletes it); 附加 writes --write-path.
        return [("file_write", {"path": "new.txt", "content": "SMOKE_NEW"})]
    if "附加" in text:
        return [("file_write", {"path": args.write_path, "content": args.write_body})]
    if "shell" in text:
        return [("shell_exec", {"command": args.shell_command})]
    return [("file_grep", {"pattern": "SMOKE_TARGET"}),
            ("file_edit", {"path": args.edit_path, "old_string": args.edit_old,
                           "new_string": args.edit_new})]


def demo_plan(args, user_text):
    """The demo mode's turn: (calls, reply) for the user message of the moment.

    The recording script (scripts/demo/record.py) types three lines: a question
    about the small project (two reads), a line queued while the model answers
    ("运行": how to run it) and an edit request ("改成"). The fake only decides
    what to call; chat asks the person before the edit runs.
    """
    text = str(user_text)
    if "改成" in text:
        return ([("file_edit", {"path": args.demo_file, "old_string": args.demo_edit_old,
                               "new_string": args.demo_edit_new})], args.demo_reply_edit)
    if "运行" in text:
        return ([], args.demo_reply_queued)
    return ([("file_read", {"path": args.demo_readme}),
             ("file_read", {"path": args.demo_file})], args.reply)


def build_reply(args):
    """The text the model answers with: --reply, or a long answer of --lines lines
    of --wide columns each when the short one should not make the point."""
    if args.mode != "text" or args.lines <= 1:
        return args.reply
    out = []
    for i in range(args.lines):
        line = "%s-%02d " % (args.reply, i + 1)
        out.append(line.ljust(args.wide, ".") if args.wide > len(line) else line)
    return "\n".join(out)


def chunked(chunks, size):
    """Splits every text chunk into pieces of size characters: the reply then
    streams in small steps, so a scenario can type while it arrives."""
    out = []
    for chunk in chunks:
        text = None
        choices = chunk.get("choices") or []
        if len(choices) == 1:
            delta = choices[0].get("delta") or {}
            if set(delta) == {"content"}:
                text = delta["content"]
        if not text or len(text) <= size:
            out.append(chunk)
            continue
        for i in range(0, len(text), size):
            out.append({"choices": [{"delta": {"content": text[i:i + size]}}]})
    return out


# The two ways a session-message smoke names its target: the tag a received message
# carries ("[来自会话 <短名称>（<id>）的消息] ") and the person's own line ("发给
# <短名称>：<text>"), so no fake server has to know the peers in advance.
SESSION_TAG = re.compile(r"\[来自会话 (\S+?)（")
SESSION_TO = re.compile(r"发给 (\S+?)[：:]")

# The loop-guard modes: each drives one turn of repeated calls for the guard smoke.
LOOP_MODES = ("loop", "loop-edit", "loop-fail")


def loop_plan(args):
    """The calls of a loop-guard mode, in order (one per model turn).

    loop: four identical reads of an unchanged file (R1 on the third, R2 on the
    fourth). loop-edit: the same successful command four times with a file_edit
    in between (the workspace changes, so no reminder may appear). loop-fail:
    the same failing command three times (R3 on the third failure).
    """
    if args.mode == "loop-edit":
        plan = []
        for i in range(4):
            plan.append(("shell_exec", {"command": args.loop_command}))
            if i < 3:
                # Alternating old/new keeps every edit a successful one.
                old, new = (args.edit_old, args.edit_new) if i % 2 == 0 else (args.edit_new, args.edit_old)
                plan.append(("file_edit", {"path": args.edit_path, "old_string": old, "new_string": new}))
        return plan
    if args.mode == "loop-fail":
        return [("shell_exec", {"command": args.loop_fail_command})] * 3
    return [("file_read", {"path": "loop_target.txt"})] * 4


def agent_wanted(args, user_text):
    """Whether this request calls the agent tool: --agent-when narrows it to the
    user messages containing that text, so a smoke can run an agent turn and then
    a plain text turn in the same chat (the status line after a cancelled or
    killed agent must read 思考中… again)."""
    return not args.agent_when or args.agent_when in str(user_text)


def session_target(text):
    """The short name a session-mode answer goes to: the sender a received session
    message names in its tag, or the peer the person's own line addresses as
    "发给 <短名称>：…". "" when the text names nobody."""
    found = SESSION_TAG.search(text or "") or SESSION_TO.search(text or "")
    return found.group(1) if found else ""


def make_handler(args):
    served = [0]  # requests so far (the server runs handlers in threads)
    lock = threading.Lock()

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            user_text = next((m.get("content") or "" for m in reversed(body["messages"])
                              if m.get("role") == "user"), "")
            # "user" is the last user message: what a smoke can check the model
            # actually received (e.g. a pasted multi-line text as one message).
            print(json.dumps({"path": self.path, "model": body.get("model"), "stream": body.get("stream"),
                              "last_role": body["messages"][-1].get("role"),
                              "user": user_text,
                              "seen": [message_token(m) for m in body["messages"]],
                              "tools": [((tool.get("function") or {}).get("name") or tool.get("name", ""))
                                        for tool in (body.get("tools") or [])],
                              "tail": message_tail(body["messages"]),
                              # The last tool result's text, longer than "tail" keeps it:
                              # a smoke asserts what file_read answered (a picture note).
                              "tool_text": next((str(m.get("content") or "")[:300] for m in reversed(body["messages"])
                                                 if m.get("role") == "tool"), ""),
                              # The deferred-tool listing of the segment context
                              # (the text after "延迟加载的工具", 600 chars): "seen"
                              # keeps only 40 chars per message.
                              "deferred": next((str(m.get("content") or "")[str(m.get("content") or "").find("延迟加载的工具"):][:600]
                                                for m in body["messages"] if "延迟加载的工具" in str(m.get("content") or "")), ""),
                              # The "Preferred tool:" lines of the environment the
                              # model was sent (it never reaches the terminal).
                              "preferred": [line.strip() for m in body["messages"]
                                            for line in str(m.get("content") or "").splitlines()
                                            if "Preferred tool:" in line]}), flush=True)
            system = " ".join(str(m.get("content")) for m in body["messages"] if m.get("role") == "system")
            child = "suga sub-agent" in system
            if child and args.child_fail:
                # --child-fail: the sub-agent's model call fails for good (a 400 is
                # not retried), so the agent tool answers with an error result and no
                # report: the status-line leak smoke (chat_agent.py).
                self.reject()
                return
            # --child-delay slows the sub-agent's report (after its grep).
            reporting = child and body["messages"][-1].get("role") == "tool"
            time.sleep(args.delay + (args.child_delay if reporting else 0))
            with lock:
                served[0] += 1
                n = served[0]
            if n <= args.fail_first:
                self.refuse()
                return
            # --fail-from N [--fail-count M]: fail from the Nth request on, M
            # times (M <= 0: every later request), so the first steps of a turn
            # can run and the turn then ends with a failure.
            if (args.fail_from and n >= args.fail_from
                    and (args.fail_count <= 0 or n < args.fail_from + args.fail_count)):
                self.refuse()
                return
            # --overload-first M: the next M requests answer with a temporary API
            # error only (an overload), the shape responses/openai report inside
            # the stream (or in a 200 body without a stream).
            after_failures = n - args.fail_first
            if after_failures <= args.overload_first:
                self.overload(body.get("stream"))
                return
            broken = after_failures <= args.overload_first + args.break_first
            last = body["messages"][-1]
            # The step's tool result is already in this request: the model answers
            # instead of calling the tool again. Reading only the last message
            # would call it again when a message inserted into the turn (a steer)
            # follows the result.
            tool_done = last_index(body["messages"], "tool") > last_index(body["messages"], "assistant")
            # A tool result followed by the interruption note ("[上一回合…") belongs
            # to an earlier, interrupted turn (an admitted call suga closed with an
            # "unknown state" result): this turn has not called its tool yet.
            last_tool = last_index(body["messages"], "tool")
            if tool_done and any(m.get("role") == "user" and str(m.get("content", "")).startswith("[上一回合")
                                 for m in body["messages"][last_tool + 1:]):
                tool_done = False
            # Tool results since the last user message: the step of this turn.
            # A user message that only carries a tool result's picture (the
            # OpenAI mapping appends one after the tool message) is part of that
            # result, not a new request: it does not end the count.
            step = 0
            for m in reversed(body["messages"]):
                if m.get("role") == "user" and not is_image_attachment(m):
                    break
                if m.get("role") == "tool":
                    step += 1
            edit_calls = edit_call_sequence(args, user_text)
            # agent mode with --agent-when: a user message without the marker is a
            # plain text turn, so a smoke can check the status line after an agent
            # turn was cancelled or its background task killed.
            agent_turn = args.agent and agent_wanted(args, user_text)
            plain_turn = args.agent and not agent_turn
            adddir_calls = [("file_read", {"path": args.read_path}),
                            ("file_write", {"path": args.write_path, "content": args.write_body})]
            memory_calls = [("shell_exec", {"command": args.command}),
                            ("memory", {"action": "add", "kind": "feedback", "title": "Smoke rule",
                                        "body": "smoke memory body"})]
            # refs mode: the file-level call first, then the directory-level one.
            ref_calls = [("file_refs", {"name": args.ref_name, "path": args.ref_file}),
                         ("file_refs", {"name": args.ref_name, "path": args.ref_dir})]
            # display mode: a summary of its own twice (a file read with a line
            # window, a two-line shell command) and one tool without any
            # (update_plan keeps the JSON of its arguments).
            display_calls = [("file_read", {"path": "target.txt", "offset": 1, "limit": 2}),
                             ("shell_exec", {"command": "echo SMOKE_DISPLAY\n  echo SMOKE_SECOND"}),
                             ("update_plan", {"plan": [{"step": "read", "status": "in_progress"}]})]
            if args.mode == "adddir":
                # 只读/只写 asks for one of the two calls only (a smoke that needs
                # one of them to meet a question of its own first).
                if "只读" in str(user_text):
                    adddir_calls = adddir_calls[:1]
                elif "只写" in str(user_text):
                    adddir_calls = adddir_calls[1:]
            # deferred mode: load a native tool through tool_search and call it
            # through the tool_call proxy of the segment apply mode. (Defined here,
            # before the reply chain: a statement inside the if/elif chain splits it,
            # and the second half's else overwrote the first half's tool calls.)
            deferred_calls = [("tool_search", {"query": "select:memory"}),
                              ("tool_call", {"name": "memory",
                                             "arguments": {"action": "search", "query": "smoke"}})]
            # demo mode: this turn's calls and the text it ends with (demo_plan).
            demo_calls, demo_reply = demo_plan(args, user_text) if args.mode == "demo" else ([], "")
            if args.mode == "memory" and "直接" in str(user_text):
                memory_calls = memory_calls[1:]
            if child:
                # A sub-agent: one grep, then the report.
                if last.get("role") != "tool":
                    call = {"index": 0, "id": "call_%d" % time.time_ns(),
                            "function": {"name": "file_grep", "arguments": json.dumps({"pattern": "SMOKE"})}}
                    chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
                else:
                    chunks = [{"choices": [{"delta": {"content": "SUBREPORT: SMOKE is in target.txt"}}]}]
            elif args.mode == "memory" and step < len(memory_calls):
                name, arguments = memory_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "image" and step < 1:
                # image mode: one file_read of --read-path (a picture), then the text.
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "file_read", "arguments": json.dumps({"path": args.read_path or "shot.png"})}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            # A request without tools (the compaction summary call) is answered
            # with text: a tool call there leaves the summary empty and the
            # segment break fails.
            elif args.mode == "deferred" and step < len(deferred_calls) and body.get("tools"):
                name, arguments = deferred_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "adddir" and step < len(adddir_calls):
                name, arguments = adddir_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "refs" and step < len(ref_calls):
                name, arguments = ref_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "display" and step < len(display_calls):
                name, arguments = display_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode in LOOP_MODES and step < len(loop_plan(args)):
                # loop-guard modes: the plan's call of this step (R1/R2 on the
                # repeated reads, a quiet edit/shell alternation, or R3 on the
                # third failure). See loop_plan and chat_loop_guard.py.
                name, arguments = loop_plan(args)[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "demo" and step < len(demo_calls):
                name, arguments = demo_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "edit" and step < len(edit_calls):
                name, arguments = edit_calls[step]
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": name, "arguments": json.dumps(arguments)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "nudge" and not tool_done:
                # nudge mode: a change that is never checked, then an answer that
                # promises to continue (both rules of personal.CheckNudge hit).
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "file_write",
                                     "arguments": json.dumps({"path": args.write_path, "content": args.write_body})}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "tool" and agent_turn and not tool_done and "[background task" not in str(user_text):
                agent_args = {"description": "smoke survey", "prompt": "Where is SMOKE? Report file:line."}
                if args.background:
                    agent_args["background"] = True
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "agent", "arguments": json.dumps(agent_args)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "empty-final" and not tool_done:
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "file_write", "arguments": json.dumps({
                            "path": args.write_path, "content": args.write_body})}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "preferred" and not tool_done:
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": args.preferred_tool,
                                     "arguments": json.dumps({"query": "SMOKE_PREFERRED"})}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "empty-final":
                chunks = [{"choices": [{"delta": {}}]}]
            elif args.mode == "session" and not tool_done and session_target(user_text):
                # One session_message send to the peer the text names, then the answer
                # of --reply: a reply that starts the peer's turn is what makes the
                # two chats answer each other up to the hop limit.
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "session_message",
                                     "arguments": json.dumps({"action": "send",
                                                              "to": session_target(user_text),
                                                              "text": args.session_text})}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            elif args.mode == "tool" and not plain_turn and not tool_done and "[background task" not in str(user_text):
                shell_args = {"command": args.command}
                if args.background:
                    shell_args["background"] = True
                if args.watch_paths:
                    shell_args["watch_paths"] = args.watch_paths.split(",")
                call = {"index": 0, "id": "call_%d" % time.time_ns(),
                        "function": {"name": "shell_exec", "arguments": json.dumps(shell_args)}}
                chunks = [{"choices": [{"delta": {"tool_calls": [call]}}]}]
            else:
                # demo turns end with their own text (demo_reply); every other
                # mode answers build_reply (--reply, or the long answer).
                chunks = [{"choices": [{"delta": {"content": demo_reply or build_reply(args)}}]}]
            usage = None if args.no_usage else {"prompt_tokens": args.prompt_tokens, "completion_tokens": 2,
                                                "total_tokens": args.prompt_tokens + 2}
            if args.mode in LOOP_MODES:
                # loop modes: whether this request carried the loop guard's hint,
                # and which rule's wording it had (R3 says "已失败", R1/R2 say
                # the result did not change). The step's tool results are already
                # in body["messages"].
                text = "\n".join(str(m.get("content") or "") for m in body["messages"])
                kind = "r3" if "已失败" in text else ("repeat" if "换一种方法" in text else "")
                print(json.dumps({"loop": True, "step": step, "hint_seen": bool(kind), "hint_kind": kind}), flush=True)
            chunks = chunked(chunks, args.chunk) if args.chunk > 0 else chunks
            if not body.get("stream"):
                # Non-streaming calls (e.g. the compaction summary) get one JSON body.
                delta = chunks[0]["choices"][0]["delta"]
                message = {"role": "assistant", "content": delta.get("content")}
                if "tool_calls" in delta:
                    message["tool_calls"] = [dict(c, type="function") for c in delta["tool_calls"]]
                finish = "tool_calls" if "tool_calls" in delta else "stop"
                response = {"id": "fake", "object": "chat.completion", "choices": [{"index": 0, "message": message, "finish_reason": finish}]}
                if usage is not None:
                    response["usage"] = usage
                raw = json.dumps(response).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
                return
            if usage is not None:
                chunks.append({"choices": [], "usage": usage})
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            if broken:
                # Half of the text, then hang up without ending the body.
                text = args.reply[: max(1, len(args.reply) // 2)]
                chunks, self.close_connection = [{"choices": [{"delta": {"content": text}}]}], True
            for chunk in chunks + ([] if broken else ["[DONE]"]):
                if args.chunk_delay > 0 and isinstance(chunk, dict) and chunk.get("choices"):
                    time.sleep(args.chunk_delay)
                data = "data: " + (chunk if isinstance(chunk, str) else json.dumps(chunk)) + "\n\n"
                raw = data.encode()
                self.wfile.write(b"%x\r\n%s\r\n" % (len(raw), raw))
                self.wfile.flush()
            if not broken:
                self.wfile.write(b"0\r\n\r\n")

        def overload(self, stream):
            """Answer 200 with only a temporary API error (an overload): as an SSE
            error event when streamed, as a JSON error body otherwise. Both are
            the shapes the providers report an overload in, and both must be
            retried instead of failing the turn."""
            message = "Our servers are currently overloaded. Please try again later."
            if stream:
                raw = ('data: %s\n\n' % json.dumps({"error": {"code": "server_is_overloaded",
                                                         "message": message}})).encode()
                ctype = "text/event-stream"
            else:
                raw = json.dumps({"error": {"code": "server_is_overloaded", "message": message}}).encode()
                ctype = "application/json"
            self.send_response(200)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def reject(self):
            """Answer 400: a failure that is not retried (a sub-agent whose model
            call is broken for good)."""
            raw = b'{"error":{"message":"child model is broken"}}'
            self.send_response(400)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def refuse(self):
            """Answer 503: the shape the retry policy treats as transient."""
            raw = b'{"error":{"message":"overloaded"}}'
            self.send_response(503)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def log_message(self, *a):
            pass

    return Handler


class QuietServer(http.server.ThreadingHTTPServer):
    """Threads: stream and non-stream calls use separate client connections,
    and a kept-alive idle connection must not block the other one. A client
    that hangs up (Ctrl-C, a preempted idle consolidation) is not an error."""
    daemon_threads = True

    def handle_error(self, request, client_address):
        if isinstance(sys.exc_info()[1], (BrokenPipeError, ConnectionResetError)):
            return
        super().handle_error(request, client_address)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--port", type=int, default=18795)
    p.add_argument("--mode", choices=["tool", "text", "edit", "memory", "refs", "adddir", "empty-final", "preferred", "session", "display", "loop", "loop-edit", "loop-fail", "nudge", "image", "deferred", "demo"], default="tool")
    p.add_argument("--delay", type=float, default=1.0)
    p.add_argument("--command", default="echo SMOKE_OK")
    p.add_argument("--reply", default="完成")
    p.add_argument("--lines", type=int, default=0, help="text mode: answer with this many lines")
    p.add_argument("--wide", type=int, default=0, help="text mode with --lines: pad every line to this many columns")
    p.add_argument("--chunk", type=int, default=0, help="split a streamed text reply into pieces of this many characters")
    p.add_argument("--chunk-delay", type=float, default=0.05, help="seconds between two chunks of a streamed text reply")
    p.add_argument("--ref-name", default="Load", help="refs mode: the name file_refs searches")
    p.add_argument("--ref-file", default="svc/svc.go", help="refs mode: the file of the first file_refs call")
    p.add_argument("--ref-dir", default=".", help="refs mode: the directory of the second file_refs call")
    p.add_argument("--prompt-tokens", type=int, default=10)
    p.add_argument("--no-usage", action="store_true", help="omit usage from completion responses")
    p.add_argument("--background", action="store_true", help="tool mode: shell_exec with background: true")
    p.add_argument("--watch-paths", default="", help="tool mode with --background: comma-separated watch_paths")
    p.add_argument("--read-path", default="", help="adddir mode: file to read (outside the workspace)")
    p.add_argument("--write-path", default="", help="adddir mode: file to write (under a :rw added directory)")
    p.add_argument("--write-body", default="SMOKE_ADD_DIR_WRITE", help="adddir mode: content of the written file")
    p.add_argument("--edit-path", default="target.txt", help="edit mode: file_edit target")
    p.add_argument("--edit-old", default="SMOKE_TARGET old", help="edit mode: old_string")
    p.add_argument("--edit-new", default="SMOKE_TARGET new", help="edit mode: new_string")
    p.add_argument("--remove-path", default="delete.txt", help="edit mode: file_remove target")
    p.add_argument("--preferred-tool", default="preferred_lookup", help="preferred mode: MCP tool to call")
    p.add_argument("--shell-command", default="echo SMOKE_SHELL", help="edit mode: shell_exec command")
    p.add_argument("--loop-command", default="echo SMOKE_LOOP_SHELL", help="loop-edit mode: the successful command repeated")
    p.add_argument("--loop-fail-command", default="false", help="loop-fail mode: the command that keeps failing")
    p.add_argument("--agent", action="store_true", help="tool mode: call the agent tool instead of shell_exec")
    p.add_argument("--agent-when", default="",
                   help="tool mode with --agent: only a user message containing this text calls the agent tool")
    p.add_argument("--child-fail", action="store_true",
                   help="a sub-agent's model call fails for good (a 400, not retried)")
    p.add_argument("--session-text", default="SMOKE_SESSION_TEXT",
                   help="session mode: the text one session_message send carries")
    p.add_argument("--child-delay", type=float, default=0, help="extra seconds before each sub-agent response")
    p.add_argument("--demo-readme", default="README.md", help="demo mode: the project file that is read first")
    p.add_argument("--demo-file", default="todo.py", help="demo mode: the project file that is read second")
    p.add_argument("--demo-edit-old", default='"added:"', help="demo mode: old_string of the edit turn")
    p.add_argument("--demo-edit-new", default='"已添加:"', help="demo mode: new_string of the edit turn")
    p.add_argument("--demo-reply-queued", default="运行方式：python todo.py add \"写周报\"，然后 python todo.py list 查看；跑测试用 pytest -q。",
                   help="demo mode: the answer to the line queued with Tab")
    p.add_argument("--demo-reply-edit", default="改好了：新增待办的提示从 added: 换成了 已添加:，再跑一次 list 就能看到。",
                   help="demo mode: the answer after the edit was approved")
    p.add_argument("--fail-first", type=int, default=0, help="answer the first N requests with HTTP 503")
    p.add_argument("--break-first", type=int, default=0, help="then break the next M streams after half the text")
    p.add_argument("--overload-first", type=int, default=0, help="after --fail-first: answer the next M requests with a temporary API error only")
    p.add_argument("--fail-from", type=int, default=0, help="answer requests N and later with HTTP 503 (0 = off)")
    p.add_argument("--fail-count", type=int, default=0, help="--fail-from: how many requests fail (0 = all of them)")
    args = p.parse_args()
    server = QuietServer(("127.0.0.1", args.port), make_handler(args))
    print(json.dumps({"listening": args.port}), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        sys.exit(0)


if __name__ == "__main__":
    main()
