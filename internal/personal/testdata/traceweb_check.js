// Renders traceweb.html's page script against a minimal DOM and checks the
// text it produces (TestTraceWebRenders). The DOM keeps the real append
// semantics: a non-Node argument is printed as String(x), so passing an array
// shows "[object HTMLDivElement]" like a browser does.
//   node traceweb_check.js <traceweb.html> <session.json> <env.json>
const fs = require("fs");
const vm = require("vm");
const [htmlPath, sessionPath, envPath] = process.argv.slice(2);

class Node {
  constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.style = {}; this.className = ""; this.dataset = {}; this.classList = { toggle() {}, add() {} }; }
  append(...xs) { for (const x of xs) this.children.push(x instanceof Node ? x : new Text(String(x))); }
  replaceChildren(...xs) { this.children = []; this.append(...xs); }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener() {}
  set textContent(t) { this.children = [new Text(t)]; }
  get textContent() { return this.children.map(c => c.textContent).join(""); }
}
class Text extends Node { constructor(t) { super("#text"); this.t = t; } get textContent() { return this.t; } }
const blocks = new Set(["div", "p", "h3", "h4", "tr", "pre", "summary"]);
const ids = {};
Object.assign(globalThis, {
  Node,
  document: { createElement: t => new Node(t), createTextNode: t => new Text(t), getElementById: id => (ids[id] ||= new Node("div")), querySelectorAll: () => [] },
  location: { hash: "" }, history: { replaceState() {} }, fetch: () => new Promise(() => {}),
  __dump: n => n instanceof Text ? n.textContent : n.children.map(__dump).join(blocks.has(n.tag) ? "\n" : ""),
  __session: JSON.parse(fs.readFileSync(sessionPath, "utf8")),
  __env: JSON.parse(fs.readFileSync(envPath, "utf8")),
  __html: fs.readFileSync(htmlPath, "utf8"),
});

const page = /<script>([\s\S]*)<\/script>/.exec(fs.readFileSync(htmlPath, "utf8"))[1];
const checks = `
data = __session; env = __env;
data.events = data.events || []; data.entries = data.entries || [];
for (const en of data.entries) entryBySeq.set(en.seq, en);
t0 = Math.min(...data.events.map(at));
let bad = 0;
const check = (name, ok) => { console.log((ok ? "PASS " : "FAIL ") + name); if (!ok) bad++; };
tab = "env";
const main = new Node("main");
renderEnv(main);
let text = __dump(main);
check("env tab: no [object ...]", !text.includes("[object"));
check("env tab: tools listed", env.tools.every(t => text.includes(t.name)));
const ctx = data.events.find(e => e.kind === "context");
const box = new Node("div");
showDetail(box, ctx);
text = __dump(box);
check("context: no [object ...]", !text.includes("[object"));
check("context: tool definitions named", text.includes("工具定义（") && ctx.attrs.tool_names.every(n => text.includes(n)));
delete ctx.attrs.tool_names; // recorded by an older suga
showDetail(box, ctx);
text = __dump(box);
check("old context: the tools the session called", text.includes("没有工具名单") && text.includes("本会话调用过的工具："));
for (const e of data.events) { const b = new Node("div"); showDetail(b, e); if (__dump(b).includes("[object")) check("detail " + e.kind + ": no [object ...]", false); }

// P2: the background task view — the timeline row at the task's start with its
// links, the analysis numbers and the notice label of the context tab.
tab = "timeline";
const tl = new Node("main");
renderTimeline(tl);
text = __dump(tl);
const rows = text.split("\\n"), lineOf = s => rows.findIndex(l => l.includes(s));
const taskRow = lineOf("启动行"), waitRow = lineOf("action=wait id=t1"), starterRow = lineOf("command=go test");
// The name cell is the line above the summary cell of the same row.
check("timeline: background task row is named 后台 t1", taskRow > 0 && rows[taskRow - 1].includes("后台 t1"));
check("timeline: task row sits at the task start, before the later wait", taskRow >= 0 && waitRow > taskRow);
check("timeline: the starting shell_exec row links to the task", starterRow >= 0 && starterRow < taskRow && rows[starterRow].includes("后台 t1"));

// P3: the badge names the task by its kind (a watch, a Stop hook) and the
// summary of a function task shows what it reported.
const watchRow = lineOf("5 次运行"), watchStarter = lineOf("command=go vet"), hookRow = lineOf("Stop hook · exit=0");
check("timeline: a watch row is named 监听 t2", watchRow > 0 && rows[watchRow - 1].includes("监听 t2"));
check("timeline: a watch summary shows runs and changes", watchRow >= 0 && rows[watchRow].includes("5 次运行") && rows[watchRow].includes("2 次变化"));
check("timeline: the watch's shell_exec links to it as a watch", watchStarter >= 0 && rows[watchStarter].includes("监听 t2"));
check("timeline: a Stop hook task row is named by its summary", hookRow > 0 && rows[hookRow - 1].includes("Stop hook t3"));

// P4: sub-agent calls — the badge names the sub-agent by its description, the
// summary shows what it spent and the command that opens its own trace.
const agentRow = lineOf("12 步"), taskAgentRow = lineOf("agent: survey the deps");
check("timeline: a sub-agent row is named by its description", agentRow > 0 && rows[agentRow - 1].includes("子 agent find the callers"));
check("timeline: a sub-agent summary shows steps, tokens and stop", agentRow >= 0 && rows[agentRow].includes("in 85k（缓存 60k）") && rows[agentRow].includes("out 3k") && rows[agentRow].includes("stop=final"));
check("timeline: the row names the command that opens its trace", agentRow >= 0 && rows[agentRow].includes("suga inspect trace sess_child1 --web"));
check("timeline: a background sub-agent's task row is named by its description", taskAgentRow > 0 && rows[taskAgentRow - 1].includes("子 agent survey the deps"));
check("timeline: the agent task row shows the steps its sub-agent ran", taskAgentRow >= 0 && rows[taskAgentRow].includes("7 步"));
const agentEv = data.events.find(e => e.name === "agent" && e.attrs && e.attrs.child_session_id);
const agentBox = new Node("div");
showDetail(agentBox, agentEv);
const agentText = __dump(agentBox);
check("detail: a sub-agent shows the command for its own trace", agentText.includes("子会话") && agentText.includes("suga inspect trace sess_child1 --web"));

tab = "context";
const cx = new Node("main");
renderContext(cx);
text = __dump(cx);
check("context: a task notice is labelled, not shown as an input", text.includes("后台任务通知") && text.includes("[background task notice] t1 ended"));
check("context: a running task's report is a notice too", text.includes("reports: run 5") && (text.match(/后台任务通知/g) || []).length === 2);
check("context: the person input is labelled as an input", text.includes("用户输入") && text.includes("read a.go"));
check("context: a steering message is labelled as such, not as an input", text.includes("插话") && text.includes("only change the README"));

tab = "analysis";
const an = new Node("main");
renderAnalysis(an);
text = __dump(an);
check("analysis: background task numbers", ["background_tasks", "background_overlap_ms", "task_waits", "task_wait_ms", "immediate_waits"].every(k => text.includes(k)));
// Each kv row is a key cell and a value cell on their own lines; rows run into
// each other (a value line starts with the value and ends with the next key),
// so a value is checked at the start of the line after its key.
const arows = text.split(String.fromCharCode(10)), kvVal = k => { const i = arows.findIndex(l => l.endsWith(k)); return i >= 0 ? arows[i + 1] : ""; };
check("analysis: watch and Stop hook numbers", [["watch_tasks", "1"], ["watch_runs", "5"], ["watch_changes", "2"], ["stop_hooks", "1"]].every(([k, v]) => kvVal(k).startsWith(v)));
check("analysis: no [object ...]", !text.includes("[object"));
check("analysis: sub-agent numbers", ["agent_calls", "agent_steps", "agent_tool_calls", "agent_input_tokens", "agent_cost_usd", "agent_ms", "agent_stops"].every(k => text.includes(k)));
check("analysis: sub-agent counts", [["agent_calls", "2"], ["agent_steps", "19"], ["agent_tool_calls", "8"]].every(([k, v]) => kvVal(k).startsWith(v)));
check("analysis: why the sub-agents stopped", kvVal("agent_stops").includes('"final":1'));

// 自动刷新：首屏是全量，之后的响应只带新增（delta），页面把它们合并进去。
const beforeEvents = data.events.length, beforeEntries = data.entries.length;
const newest = data.events[data.events.length - 1];
check("auto refresh: the page has the switch, off by default", __html.includes('id="auto"') && document.getElementById("auto").checked !== true);
applyView({delta:true, session:data.session, analysis:data.analysis,
  events:[{id:9999, kind:"tool", name:"shell_exec", status:"ok", start:newest.start, duration_ms:1, attrs:{arg:"delta-probe-command"}}], entries:[]});
check("auto refresh: a delta appends the new event only", data.events.length === beforeEvents + 1 && data.entries.length === beforeEntries);
check("auto refresh: the markers follow what the page has", markEvent === 9999 && markSeq === data.entries[data.entries.length - 1].seq);
const tl2 = new Node("main");
renderTimeline(tl2);
check("auto refresh: the merged event shows in the timeline", __dump(tl2).includes("delta-probe-command"));
process.exitCode = bad ? 1 : 0;
`;
vm.runInThisContext(page + checks);
