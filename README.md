# Suga

[![Unit-Tests](https://github.com/inhere/suga/actions/workflows/go.yml/badge.svg)](https://github.com/inhere/suga/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

English | [简体中文](README.zh-CN.md)

> **Status:** suga is currently v0 (0.x). Minor releases may include incompatible changes; see [CHANGELOG.md](CHANGELOG.md).

Suga is a personal coding agent written in Go. One persistent conversation engine serves two interfaces: a terminal CLI (`chat` and one-shot `run`) and an ACP stdio server (`acp`).
[Gofer](https://github.com/inhere/gofer) can launch either one and adds jobs, workers, logs and interactive approval on top.

> The full documentation lives in the reference docs of the built-in skill [suga-guide](skills/suga-guide/SKILL.md) (`references/`, written in Chinese). Suga unpacks it to `~/.suga/skills/suga-guide/` on first run, so you can simply ask chat how to configure suga.

![suga chat demo: reading a project, queueing a message, approving an edit](docs/images/demo.gif)

> Recorded with a fake model so it can be re-recorded; see [docs/demo/README.md](docs/demo/README.md).

## Features

- **Development tools**: read files in line windows, list, grep and find, exact-replacement edits, shell commands (PowerShell on Windows), web fetch and search.
- **Safe by default**: read-only unless writing is enabled; chat asks before writing or running commands; file tools stay in the workspace; web fetches refuse private networks.
- **Persistent sessions**: one SQLite database with resume, automatic compaction, plans, handoff and an event log for tracing and stats.
- **Sub-agents and background tasks**: read-only sub-agents bring back only a report; long commands and watchers run in the background and notify the model.
- **Skills**: standard `SKILL.md` skills, listed or searched on demand; a built-in configuration and usage guide, `suga-guide`.
- **MCP**: stdio MCP servers with allowlists and deferred loading for large tool sets.
- **Persistent memory**: user and project memory across sessions, with swappable backends (files, gofer or an external command).
- **History search and output offloading**: full-text search over earlier sessions; oversized tool results are saved aside, the context keeps the head, the tail and a ref.

## Positioning

Suga is deliberately a local-first, single-binary tool. It combines read-only-by-default operation and approvals with persistent sessions, automatic context segmentation, long-session evaluation and regression gates. It can run as an ACP server or be embedded through `pkg/sugasdk`.

It is a good fit when you want a hackable Go agent for local coding work, durable conversations, auditable tool calls, or an embeddable engine. It is not a replacement for a hosted SaaS, a graphical IDE, a multi-project control plane such as Gofer, or a stable v1 API; provider credentials and model access remain your responsibility.

## Install

- **Recommended**: install with [eget](https://github.com/inherelab/eget): `eget install inhere/suga`
- With Go: `go install github.com/inhere/suga/cmd/suga@latest`
- Download an archive from [GitHub Releases](https://github.com/inhere/suga/releases) (a pushed `v*` tag publishes Linux, macOS and Windows builds) and put `suga` on your `PATH`.
- From source (Go 1.26+): `git clone https://github.com/inhere/suga.git && cd suga && make install`
  - Builds (compressing with `upx` when available) and installs to `$(go env GOPATH)/bin`; on Windows it first moves aside a binary that a running chat still uses.
- Make sure the install directory is on the `PATH` of your shell (and of the Gofer server, if used), then check `suga version`.

## Quick start

Details: [references/config.md](skills/suga-guide/references/config.md) (onboarding and configuration commands, Chinese). The detailed built-in guide remains in Chinese; the examples below use the public provider presets and environment variables.

```bash
suga init                                         # create ~/.suga and keep existing files
# Put the key for the provider you choose in ~/.suga/.env:
#   OPENAI_API_KEY=...
#   DEEPSEEK_API_KEY=...
#   ANTHROPIC_API_KEY=...

# OpenAI-compatible official providers (choose one):
suga config set model deepseek/deepseek-chat
# or: suga config set model openai/<model-id>    # an OpenAI model id your account can use

# Anthropic official API (alternative):
# suga config set model anthropic/claude-sonnet-5-5

suga config get model                             # verify the parsed model and source
suga models                                       # list configured models offline; suga config get <key> shows value and source
suga config set tools.allow_write true            # optionally let chat write files and run commands (it still asks first)
suga config set tools.web_fetch.user_agent "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"  # optionally set a browser UA
suga inspect rules output --builtin               # print a built-in rule template
suga chat                                         # start an interactive chat in the current directory
```

The presets `openai`, `deepseek` and `anthropic` already provide their official base URLs. Keep secrets in `$SUGA_HOME/.env`, not in YAML. To enable writing and command execution in chat, set `tools.allow_write: true`; it still asks for approval by default.

- One turn: `suga run --prompt "Summarize this project"` (read-only), `suga run --allow-write --prompt "Fix the failing test"`, `suga run --resume <session-id> --prompt "Continue"`, `suga run --image shots/a.png --image shots/b.png --prompt "Compare these two"`; `--max-steps` caps the turn's steps (default 240).
- Delivery check (`run` only): `suga run --verify "go test ./..." --prompt "Fix the failing test"` runs that command in the session workspace (the shell `shell_exec` uses) once the turn finished normally; a failure is handed back to the model as an injected turn so it can fix it. `--verify-rounds N` (default 2, 1-5) caps the fix rounds, `--verify-timeout D` (default 10m) bounds one run and a timeout counts as a failure. The command is the caller's, so it skips approval, but a cancelled run kills its process tree. Each check is visible: text mode prints one stderr line `verify #1: go test ./... → exit 1 (12.3s)` and a tail summary when it never passes (stdout stays the answer), stream-json adds a `{"type":"verify",...}` line and `"verify":{"passed":...,"rounds":...}` to the result line (a run without `--verify` is unchanged), and `suga inspect trace` shows a `verify` event. A turn that failed, yielded or was cancelled is not checked. Exit codes of `run`: `0` done (check passed, or no `--verify`), `1` the turn failed, `4` the resumed session does not exist, `5` the `--verify` command still failing after the rounds, `130` interrupted.
- Output contract (`run` only): `suga run --output-schema review.json --prompt "..."` makes the final answer a JSON value that satisfies the caller's JSON Schema, or fails explicitly. The value is `-` (stdin, read once), inline JSON (starts with `{` or `[`) or a file path; the schema is compiled before the session exists, so a bad one (syntax, not an object/boolean at the top, over 32 KiB, an external `$ref`, an unknown `$schema`) is a usage error with exit code `2` and no session is created. The schema is shown to the model in this request's *ephemeral* context: at the very end of the request, never stored in the session (a resume does not inherit it), and the system prompt and tool schemas are untouched. It travels at most twice: the judge runs last in the pipeline (`Run → background tasks → nudge → --verify → contract`) and a failing answer gets exactly one injected correction turn whose message carries the validation errors only. The answer is read in a fixed order - the whole answer, fenced blocks (` ```json ` first, then unlabelled, last block first), balanced `{...}`/`[...]` pieces of prose - and the first value that satisfies the schema wins (`source` is `whole`/`fenced`/`substring`, or `no_json`). Extraction keeps the model's text: numbers are never rounded through float64, so `{"id":9007199254740993}` comes back exactly. `format` is an annotation, not an assertion; assert with `pattern`/`enum` instead. The schema is data only: no external resource is ever loaded, and a `description` can shape the output but cannot widen what the tools may do. A failing contract keeps stdout the answer (text mode also prints one `[output schema] ...` stderr line) and ends with exit code `6`: `0` done, `1` the turn failed, `2` bad usage (an invalid schema included), `4` the resumed session does not exist, `5` the `--verify` command still failing, `6` the answer still not satisfying the schema after one correction, `130` interrupted. `--output-format stream-json` adds `"output_schema":{"checked":true,"passed":...,"rounds":...,"source":...,"errors":[...],"value":...}` to the result line (`{"checked":false,"reason":...}` when the turn ended wrapped-up, truncated, yielded or empty); a run without `--output-schema` keeps its stdout, its stream-json lines and its trace byte for byte as before. `suga inspect trace` shows an `output_schema` event per check (round, action, passed, source, at most 5 errors, the schema's hash and size - never the schema itself), and `suga chat` refuses the flag.
- Stopping-too-early check (`agents.nudge`, default `on`): when a turn finished normally, fixed-rule kinds look at what the turn left behind. `unverified_write`: after the last successful `file_edit`/`file_write`/`file_remove` no check command of the rule's check_command ran (the built-in list covers the common test/vet/build commands); `plan_open`: the session plan still has pending/in_progress steps. `promise`: the answer ends with a continuation phrase ("接下来我去跑测试", "Let me run the tests", "I'll now check …") in its last paragraph; an answer that ends by asking is left alone. Rules are configurable in `nudge-rules.yaml` (built-in → `$SUGA_HOME/nudge-rules.yaml` → `<workspace>/.suga/nudge-rules.yaml`, same-name whole replacement, `enabled: false` switches one off). `run` pushes the model back with an injected `[收工检查]` turn that lists the evidence (promise at most twice per run, the checked kinds once together, both skipped with `--verify`), text mode prints one stderr line `nudge: …` and stream-json a `{"type":"nudge",…}` line; `chat` only shows a gray `⚠ …` line and never starts a turn; ACP only writes the `nudge` trace event. `agents.nudge off` disables it; `suga inspect manifest` lists the effective rules.
- [config/config.yaml](config/config.yaml) lists every field; `suga inspect manifest` reports the full effective capabilities.

## Chat

Details: [references/chat.md](skills/suga-guide/references/chat.md) (Chinese)

- **Local commands**: `:session`, `:compact`, `:quit`/`:exit`, `:diff [N]`, `:code [N]`, `:model [p/m]`, `:add-dir`/`:dirs`/`:rm-dir`, `:image <路径|clear>`（附图到下一条消息）, `:queue [-d <N>|clear]`, `:checkpoints [N]`, `:undo [N] [force]`, `:help [<命令>]`, `:usage`, `:skills [--only-names] [-n N]`（已加载的 skills）, `:context [--files]`（当前上下文的构成 / 已加载的文件）, `:tasks [-a|-k <ID>]`（运行中+最近结束；`-a` 全部，`-k` 终止）; every command answers `-h`/`--help` with its usage (`:help <命令>` is the same text) and refuses an argument it cannot take with that usage; keyboard input only. A `:word` that names no command is answered with the closest command names instead of being sent to the model, while `:)`, `: 文本`, `:中文` and `::1` still send as ordinary messages.
- **Rendering**: answers are rendered as terminal markdown (`chat.markdown`); the palette follows `chat.theme` (`auto|dark|light`, default `auto`: a light terminal background from `$COLORFGBG` picks light; `SUGA_THEME` overrides it); code blocks are highlighted line by line by the language of their fence (`chat.code_highlight`: comments, strings, numbers and keywords in 16 colors) and folded after `chat.code_max_lines` lines, `:code [N]` shows a folded block of the last answer whole; GFM tables are aligned by display width (CJK counts two columns). Tool calls print a line of their own with the arguments as the tool reads them (`file_read a.go:10-29`, `shell_exec go test ./...`, `agent explore find Foo`); a tool without a summary (`update_plan`, MCP tools) keeps its argument JSON, and only the display changes: run, stream-json, ACP and the trace keep the full JSON.
- **Startup hint**: the banner names the commands of every turn only (`:session`, `:compact`, `:diff`, `:quit`/`:exit`, `:help`); `:add-dir`/`:dirs`/`:rm-dir`, `:checkpoints` and `:undo` still work, still complete and are listed by `:help`.
- **Input**: Up/Down history (project-scoped by default; `chat.history_scope: all` recalls the other projects again), Ctrl+R reverse search, Tab completion for `:` commands and directories; newline with `Ctrl+Enter` (Windows Terminal, native via win32-input-mode; `Ctrl+J` elsewhere on Windows) or `Alt+Enter`; `Alt+V` pastes the clipboard's picture onto the same queue as `:image`.
- **Steering**: while the model works the prompt becomes `↳ ` and Enter adds the line to the running turn; `Tab` queues the line for the turn after this one instead (`[排队] …（本轮结束后发送）`, `:queue` lists and `:queue clear` drops the queue); `Esc` stops only the turn, two `Ctrl-C` within 3s quit.
- **`!` local commands**: `!cmd` runs in the local shell without the model; `!!cmd` also attaches the result to your next message.
- **Resume preview**: resuming shows the last model reply first; a session left `running` by a killed process is recovered.
- **Demo**: `make demo-cast` records a 30-second terminal demo of chat (tool lines, Tab queueing, write approval, `:context`, the resume line) to `docs/demo/suga-chat.cast`, replayed with `asciinema play`; [docs/demo/README.md](docs/demo/README.md) covers the timeline, the fake model behind it and how to turn it into a GIF.
- **Terminal smoke suites**: `make smoke-chat` runs the comprehensive Python PTY scenarios on Linux/macOS (`scripts/ptysmoke`, including steering, shell cancellation and paste). `make smoke-pty-go` runs the smaller Go `ptytest` suite on Windows ConPTY and Unix, covering approval prompt redraw, local commands, unknown colon input and bracketed paste. The Go suite is the Windows regression gate; the Python suite remains the Linux/macOS behavior oracle.

## Configuration

Details: [references/config.md](skills/suga-guide/references/config.md) (every key and default), [references/providers.md](skills/suga-guide/references/providers.md) (providers and models) (Chinese)

- Only `$SUGA_HOME/config.yaml` and `$SUGA_HOME/.env` are read (default `~/.suga`), never the working directory's; `SUGA_CONFIG` names a single config file.
- `suga init` is safe to repeat; `suga init --force` backs up and replaces only the home config. It never writes `.env` values, and refuses to bypass an explicit `SUGA_CONFIG`.
- `suga config get|set|unset|list|path` shows effective values and sources, preserves YAML comments, and rejects secret keys in YAML; put credentials in `.env` and reference them as `${VAR}`.
- `language` controls human-facing UI text: `auto` (default) uses Chinese on Chinese systems and English elsewhere; explicit `zh` or `en` is also accepted. Common aliases are normalized (for example `en_US` → `en` and `中文` → `zh`). `SUGA_LANGUAGE` takes precedence over the config value. Changes take effect after restarting suga; `suga inspect manifest` shows the resolved language and its source. The UI currently supports Chinese and English, while some model-facing prompts remain Chinese for a later phase.
- `suga models [--json]` is an offline view of local providers and model references. It does not replace `suga inspect manifest` or chat's session-local `:model` switch.
- `model` is `<provider>/<model>`, precedence `--model` > `SUGA_MODEL` > config; `fallback_models` are tried in order, `compact_model` writes summaries.
- `providers.<id>`: `type` (`openai`, `openai-responses`, `anthropic`), `base_url`, `api_key`, `headers`, plus per-model `context_window`, `max_output_tokens`, `model_id`, `price`, `thinking`.
- For a self-hosted or private-compatible gateway, define a custom provider and keep its credentials in `.env`:

  ```yaml
  model: my-gateway/my-model
  providers:
    my-gateway:
      type: openai
      base_url: "${MY_GATEWAY_BASE_URL}"
      api_key: "${MY_GATEWAY_API_KEY}"
      # headers: {x-session: "{{session_id}}"}
  ```

- Common switches: `tools.allow_write`, `tools.approval`, `tools.disabled`, `tools.loop_guard`, `agents.nudge`, `tools.session_message.enabled` / `tools.session_message.max_hops`, `mcp.servers`, `memory.*`, `hooks.*`, `chat.*`, `skills.*`, `workspace`.
- Time awareness: `run.deadline_env` names the env var that carries this run's deadline (RFC3339; gofer injects `GOFER_JOB_DEADLINE`). When set, each model request ends with a transient hint of the current time, the deadline and the remaining minutes; under 10% of the window (or 5 minutes) it asks for an immediate wrap-up, past the deadline it reports the overrun. Empty = off.
- All data lives in `$SUGA_HOME/db/sessions.db`.

## Tools and permissions

Details: [references/tools.md](skills/suga-guide/references/tools.md) (Chinese)

- **Tools**: `file_read` (text and pictures)/`file_list`/`file_grep`/`file_find`, `web_fetch`/`web_search`, `skill`, `session_search`/`recall`, `session_message`, `agent`, `memory`, `file_write`/`file_edit`/`file_remove`, `shell_exec`, `task`.
- **Fetching**: `web_fetch` takes one `url`, or `urls` (1–5) to fetch several concurrently: each URL gets a `## [i/N] <url>` section in input order, a failing one is reported in its section alone (`IsError` only when all failed), and every rule (private addresses, redirects, size, timeout) applies per URL. It sends `tools.web_fetch.user_agent` (empty = `suga-web-fetch/1.0`).
- **Images**: `file_read` reads a local picture too (PNG, JPEG, GIF first frame, WebP; recognized by its file header, whatever the extension) and hands it to a model that can see; a gofer attachment such as `[attach 1] /path/shot.png` is read the same way. A model that cannot see images gets a one-line note with the format, the pixel size and how to switch (`:model`) instead of an error. The bytes are stored in `$SUGA_HOME/sessions/<id>/images/` and the database keeps only the reference; pictures older than the last history fold, or over the per-request caps (8 images, 20MB), are replaced by a text note that names the image and says `file_read <ref>`. `view_image` stays one version as a *hidden alias* of the same pipeline (callable by name for old sessions and name-based configuration, not listed in the tool table, manifest or `tool_search`); `tools.disabled: [view_image]` therefore only turns off the alias - to turn picture reading off use `tools.disabled: [file_read]` or a hook. The tool table no longer depends on the model's vision, so `:model` switches no longer change it. From the CLI, `suga run --image <path>` (repeatable, order kept) attaches pictures to that turn's user message with the same checks, scaling and `sessions/<id>/images/` storage; relative paths resolve against the run's working directory, and a model without vision or an unreadable picture fails the run before the first turn (exit 1, naming the picture) instead of quietly dropping it. Over ACP, `initialize` declares `promptCapabilities.image` when any configured model can see, and `session/prompt` image blocks (base64 or a local-file URI) follow the same checks and scaling; a model without vision gets a one-time notice and text notes instead (see [docs/usage/gofer-personal-agent.md](docs/usage/gofer-personal-agent.md)).
- **Read-only by default**: write tools need `run --allow-write`, `tools.allow_write: true` in chat, or ACP `default` mode; `--read-only` always wins.
- **Approval**: with `tools.approval: ask` chat asks before writes and shell: `y` once, `a`/`d` allow or deny the same scope (command prefix, directory, path) for this session, optionally saved to a rules file; dangerous recursive deletes always need a typed `yes`.
- **Undo**: `file_write`/`file_edit`/`file_remove` snapshot a file before changing it; in chat `:checkpoints [N]` lists (N = the last N turns) and `:undo [N] [force]` rolls back, from the CLI `suga session undo <session>`; `shell_exec` changes are listed, not rolled back (`tools.snapshots.*`).
- **Approval rules**: `tools.approval_rules` reads `allow`/`ask`/`deny` rules (tool, command and path globs) from `$SUGA_HOME/approval-rules.yaml` and `<workspace>/.suga/approval-rules.yaml`; the strongest match wins, project `allow` needs `project_trust`, and rules never open protected paths (`$SUGA_HOME`, `.suga/`, `.git/`).
- **Directories**: `--add-dir <absolute dir>[:ro|:rw]` / `SUGA_ADD_DIRS` add directories; `SUGA_ADD_DIRS` entries are comma-separated on every platform (paths containing commas should use repeated `--add-dir` flags); in chat, access outside them asks `[r]/[w]/[n]`.
- **Read-only roots**: dependency source caches and idev-std directories are found automatically (`tools.dependency_roots`, `tools.standards_roots`); add more with `tools.read_roots`.
- **Background and watch tasks**: `shell_exec` with `background: true` runs in the background; automatic handoff uses `tools.shell_background_after`. Foreground `timeout` is capped by `tools.shell_timeout_max` (600s default), while background tasks are capped independently by `tools.task_timeout_max` (1h default).
- **Large output**: results over `tools.max_output_bytes` are offloaded to a ref; shell test/build output is first trimmed by output rules (customizable in `output-rules.yaml`).
- **Language rules**: `file_symbols`/`file_refs` recognise a language from `lang-rules.yaml` (built-in → `$SUGA_HOME` → `<workspace>/.suga`): one YAML entry adds a language's extensions, comment/string masking, declaration patterns, block style and test command, so Go/PHP/YAML/TOML stay in code and every other language (JavaScript/TypeScript, Python, Java, C#, Kotlin, Ruby, C/C++ built in) is data; `enabled: false` switches one off and `suga inspect manifest` lists the effective languages and the rule files it skipped.
- **MCP**: `--mcp-config` or `mcp.servers`; all calls are denied until allowed in `tools.auto_allow` / `tools.read_only_allow`.
- **Deferred native tools** (off by default): `tools.deferred_native: auto` keeps the 9 low-frequency built-in tools (`agent`, `file_symbols`, `memory`, `recall`, `session_search`, `session_message`, `skill`, `suga_manifest`, `web_fetch`) out of the per-request tool table; each new segment lists them with one purpose line, `tool_search` (or `select:a,b`) loads them, and `tools.deferred_apply: segment` (default) uses the always-present `tool_call {name, arguments}` proxy inside the segment, folding them into the table at the next segment boundary. Approval, read-only mode, hooks, taint and the trace all see the real tool name. `tools.deferred: never` turns the whole mechanism off; with the default `never` the tool table, segment context and request bytes are unchanged. See [docs/usage/llm-config-v0.md](docs/usage/llm-config-v0.md).
- **Disabled tools**: `tools.disabled: [web_fetch, web_search]` (default empty) removes those tools from the runtime, so the model never sees them (not in the tool list, not loadable by `tool_search`, not listed by `suga inspect manifest`) instead of being refused on every call. Sub-agents lose them too. A name that is neither a native tool nor a `server/tool` MCP tool fails the startup with the list of available tools, so a typo cannot quietly leave a tool on. Typical use: keep the model offline by disabling the two web tools.
- **Preferred tools**: `tools.preferred` declares commands (`rg`, `xerj`) and MCP servers this machine already has; the available ones are listed in the environment section (a `kind: command` entry only in a session that registered `shell_exec`, so a read-only session leaves it out) and `suga inspect manifest` shows `present` plus a reason for every declaration, including the missing ones, with command reasons naming that read-only limit. Preference only ranks tools: it never allows an MCP call or skips the `shell_exec` approval, and a missing tool just stays out of the prompt.

## Sub-agents

Details: [references/tools.md#子agent](skills/suga-guide/references/tools.md#子agent) (Chinese)

- The `agent` tool hands an independent task to a read-only sub-agent and brings back only its final report.
- Two types: `explore` (default, workspace read tools) and `research` (adds `web_fetch` and `web_search`).
- `background: true` runs it as a task; `task wait` returns the report, and several can run in parallel.
- Limits `agents.max_steps` (30), `agents.max_tokens` (400000), `agents.timeout` (10m); `agents.model` picks a cheaper model.
- Each call is a child session: `suga session list --agents` lists them, and their usage counts toward the parent.

## Skills

Details: [references/sessions.md#skills](skills/suga-guide/references/sessions.md#skills) (Chinese)

- Scanned in order: `<workspace>/.agents/skills`, `<workspace>/skills`, `~/.suga/skills`, `~/.agents/skills`; the first of a name wins.
- The built-in `suga-guide` is unpacked on first run and refreshed on upgrade only while you have not changed its files.
- Beyond `skills.list_max` only names are listed; the model loads one by name or searches with `skill(query=…)`; `skills.always_load` keeps a body in every prompt.
- `suga inspect manifest` lists the `SKILL.md` files it skipped (parse or frontmatter error) and same-name skills shadowed by an earlier root, with the winner and the loser; chat says the counts once when it opens a session.

## Resident context

- Every request carries the system prompt plus the segment context: environment, project rules, memory index, skills list, additional instructions and the deferred-tools listing. `suga inspect manifest` reports the bytes and estimated tokens of each part and the total, with per-file sizes for the rules (`--json`: `context.*`).
- Soft limits (warnings, nothing is dropped): rules 32KB, memory index 8KB, skills list 8KB, total 64KB. A part over its limit is marked `OVER` with the action that brings it down (trim rules, `suga memory summaries`, fewer always-listed skills).
- Rules are hard-capped at 64KB per file and 128KB in total; when that cuts content, the manifest shows `已截断（原 X 字节，保留 Y）` and chat says it once at session start (run and ACP only log it).
- Keep resident text scarce: general short rules in `AGENTS.md`, anything with a trigger scenario as a skill, deterministic steps as a script or hook.

## Memory

Details: [references/sessions.md#记忆](skills/suga-guide/references/sessions.md#记忆) (Chinese)

- The model writes cross-session memory with the `memory` tool: `user`, `feedback`, `project` and `reference` entries.
- Every index line carries a one-sentence summary (`- [title](path) — kind: summary`); `add` requires `summary` (the first sentence of the body is used when it is missing) and the prompt's memory index shows it. `suga memory summaries --missing [--fill [--apply]]` lists or fills the missing ones without touching entry bodies.
- User memory lives in `$SUGA_HOME/memory/user`; the project backend is picked automatically (repo-root `MEMORY.md`, `.gofer/tracker`, or `$SUGA_HOME/projects/<key>/memory`).
- The gofer project backend stores the title in `--title` and the one-sentence summary in `--summary` (needs gofer >= v0.179.0; older gofers fall back to the old behaviour with a one-time notice); `suga memory gofer-titles` migrates the legacy summary slot into the title (default: preview, `--apply` writes).
- `suga memory lint [--scope user|project] [--json]` is a read-only report and never calls a model: it lists duplicate entries (identical after lowercasing, collapsing whitespace and dropping punctuation, or token similarity at least `0.70`), possibly conflicting ones (same title after normalization, different body) and possibly stale ones (not updated for 365 days; entries carry no expiry field, so this is a hint). Each candidate gives the entry ids, the rule that flagged it and a suggested action (`merge`/`review`); nothing is executed and the exit code stays `0`. Bodies are read only through one enumeration per scope and pairs are blocked by a 4-part MinHash over token sets, so 10k entries scan in milliseconds instead of an O(n²) comparison. Backends that cannot enumerate (gofer, exec) are compared on title and index summary only (`bodies_read: false`).
- `memory lint --json` is stable: `{"read_only": true, "scopes": [{"scope": "user", "entries": 12, "bodies_read": true, "candidates": [{"kind": "duplicate|conflict|stale", "ids": ["tabs", "tabs-2"], "more": 0, "title": "Tabs", "similarity": 1, "reason": "…", "action": "merge|delete|review"}], "notes": ["…"]}], "totals": {"entries": 12, "candidates": 1}}`; `more` and `notes` are omitted when empty, scopes without a backend are left out.
- The memory index is frozen per segment; what this session writes shows up in the next segment.
- `memory search` on the files backend ranks hits through a rebuildable FTS5 index (Markdown stays the source of truth), and entries may carry optional `applies_when` conditions.
- A session that has read outside content must confirm memory writes (`memory.write_tainted`).

## Sessions

Details: [references/sessions.md](skills/suga-guide/references/sessions.md) (Chinese)

- `--session-id` creates or reuses a session, `--resume` requires it to exist (exit code 4 otherwise), `run --from` starts a new one carrying over the summary and plan.
- Resuming from another directory asks first (switch to the original / continue here / cancel); without a terminal use `--allow-cwd-change`.
- Near the context window or on `:compact` a new segment starts with a handoff summary (fixed sections); old segments stay reachable through `session_search` / `recall`, and `session_search` with `kind: handoff` finds the summaries themselves.
- `session_search` covers user and assistant messages and compaction/handoff summaries; tool outputs are not indexed - read them with `recall`. A ledger from before this change still holds historical tool rows: `suga data shrink-fts` was removed in v0.41.0, so cleaning such a ledger needs suga v0.40; `suga data vacuum` then returns the file space.
- An interrupted turn is replayed up to its last complete tool call, with a short recap.
- History folding starts by context size (`context.fold_at`, off by default; e.g. 0.5 starts folding at half the compaction threshold) and then folds old results of re-runnable read-only tools too; idle chat compacts in the background (`context.idle_after`).
- The system prompt holds only stable content; environment, rules (`AGENTS.md`), memory index and skills list go into each segment's context message.

## Embedding (pkg/sugasdk, experimental)

- `pkg/sugasdk` lets another Go program drive suga in its own process: `Open` an engine, `OpenSession` per conversation, `Send` a turn and receive `Event`s, then `Card`, `Compact`, `Steer`, `Search`, `Sessions` and `Memory`.
- Experimental: the API may change in a minor release until the embedding host (xhen) has settled. The exported surface names no `internal` package; `extra tools`, `host context`, the approver and the memory confirmation are the same extension points `wiring` offers.
- suga's configuration is process global: one engine per process (`ErrEngineOpen` on a second `Open`, an `Open` after `Close` is fine), and the data root is guarded (`Open` sets and loads `SUGA_HOME`, reuses the same root, returns `ErrSugaHomeMismatch` for another one).
- Sessions record origin `xhen` by default, so `suga session list` hides them; list them with `Engine.Sessions(SessionFilter{Origin: "xhen"})`. A session runs one turn at a time (`ErrBusy` instead of queueing), `Steer` inserts into the running turn, and a panic inside a turn comes back as an error.

## Using with Gofer

Details: [references/gofer.md](skills/suga-guide/references/gofer.md) (Chinese), [docs/usage/gofer-personal-agent.md](docs/usage/gofer-personal-agent.md)

- Gofer launches `suga run` (CLI jobs) or `suga acp` (persistent sessions with per-call approval through `permission_policy`).
- Agent configuration, read-only and writable runs, stream-json events and the denial contract are covered in the two documents above.

## Observability

Details: [references/observability.md](skills/suga-guide/references/observability.md) (Chinese)

```text
suga inspect trace <session> [--turn <id>] [--json]            # timeline: model calls, tools, denials, plans
suga inspect trace <session> --analyze | --web                 # loop analysis / local web page
suga inspect trace <session> --transcript [--turn <id>] [-o <file>]  # model-readable transcript (see below)
suga session list [keywords] [--since 7d] [--agents] [--all | --cwd <path>] [--origin <name>]  # session list; default: the current directory only, without origin=xhen sessions (empty result -> stderr hint naming --all and the hidden xhen count); --origin all lists every origin
suga inspect stats [--session <id>] [--json]                   # calls, latency, tokens, cache, cost
suga session compact <session> [--json]                        # start a new segment now (the CLI form of chat :compact)
suga inspect export <session> | suga data gc --older-than 30d  # export / clean up
suga data gc --retention [--apply]                             # remove by origin class: 180d chat / 30d run+ACP; origin=xhen sessions follow their host and are only reported
suga data bodies [--status|--move|--undo|--check|--remove-files]  # move tool results and assistant replies to sessions/<id>/bodies.jsonl (the v0.36 name tool-bodies, --slim, --rename-files and the one-time migrations shrink-fts / dedupe-tool-results were removed in v0.41.0: a pre-v0.36 ledger needs suga v0.40 for them)
suga data vacuum                                               # rewrite the db file, truncate the WAL
suga data usage [--json] [--top N]                             # read-only: where the db file, entries, events, FTS and session dirs hold their bytes
suga data backup --output <file>.suga-backup.zip               # verified online backup; spill excluded by default
suga data restore <file>.suga-backup.zip --dry-run             # validate without changing the home
suga data restore <file>.suga-backup.zip                       # staged restore with a pre-restore backup
suga ps | suga inspect manifest                                # running chats / capability manifest
```

- `run --output-format stream-json` emits cumulative `usage` after each model call, `retry` on retries, and `wrap_up` on the `result` of a turn ended by a limit.
- `trace --web` opens the session store read-only (a missing or uninitialized store is an error; nothing is created or migrated); its auto refresh switch is off by default, and once on pulls only new steps: every 5s while a turn runs, every 15s when idle (to notice the next turn).
- `trace --transcript` renders the session (or one `--turn`) as the plain text a reviewer or another agent reads: one labelled block per stored entry, in seq order - `[user]`, `[assistant]`, `[assistant → tool_call <tool>]` with its arguments JSON, `[tool_result <tool> ok|error]`, `[compaction summary]`, `[context update]`, `[plan]`, `[handoff]`, `[reasoning]` (drop it with `--no-reasoning`) and `[image: <ref> <w>×<h>]`. Only one tool result is shortened: above `--max-observation` bytes (default and minimum 10000; a smaller value is refused, since a short cut makes a reader diagnose the cut as a defect of the run) the block is cut on a UTF-8 boundary and followed by `…[transcript renderer: 截去 N 字节，完整内容见 suga inspect trace <session> --json 或 ref entry:<session>:<seq>]` - that marker is the renderer's, not the tool's output; user text, assistant text and summaries are never cut. `--transcript` is exclusive with `--json` and `--analyze`, `-o <file>` writes a 0600 file instead of stdout, and bodies that moved to the session file come back whole (only the renderer's limit applies).
- Diagnostic logs go to `$SUGA_HOME/logs/suga-<YYYYMMDD>.log` (one file per day and machine, rotated by size); `log.level`, `log.file`, `log.stderr`, `log.max_size`, `log.keep`, `log.compress` configure them, and `SUGA_DEBUG=1` forces debug and also writes them to stderr. Records of a turn carry `session_id` and `turn_id`.
- Subprocesses of a session (`shell_exec`, including its background and watch tasks, chat's `!cmd`/`!!cmd` and the hook command) get `SUGA_SESSION_ID`, `SUGA_SESSION_NAME` (only when the session has a short name) and `SUGA_TURN_ID` (only inside a turn) in their environment, so a nested agent or gofer can tell which conversation it runs for; `GOFER_SESSION_ID` is never set or changed. See [the config reference](skills/suga-guide/references/config.md#suga-导出给子进程的变量).

## Development

```bash
go test -race ./...     # unit tests
make smoke-chat         # PTY end-to-end chat tests incl. chat_memory and chat_idle (Linux/macOS, Python 3)
go run ./scripts/eval --fake --bin tmp/smoke/suga   # task evals, see scripts/eval/README.md
make build-all          # cross-compile to dist/
```

- The eval has multi-turn tasks (`prompts` in `task.yaml`, one session) and reports `first_call_uncached` (FIRST column: uncached input of the first main model call); `--probe-prefix` runs three prefix-cache probes (same workspace, a different workspace, a control with a random nonce in the system prompt); `--agent omp` runs the same tasks on omp for comparison.
- `cmd/suga`: entry point; `internal/cli`: one file per command; `internal/wiring`: builds providers, tools, skills, the system prompt and the runner for CLI and ACP.
- `internal/personal`: turns, segments, handoff and events (`internal/personal/store` is the SQLite store); `internal/chat`: terminal chat; `internal/acpserver`: the ACP server.
- `pkg/agentcore`: the reusable layers below the app, independent of `internal/*` ([README](pkg/agentcore/README.md)): the kernel (model/tool loop, run state, transcript) plus `llm` (providers), `schedule` (in-process After/Every/Idle timers), `tools` (registry, approval, deferred tools), `tools/native` (built-in tools), `tools/mcp` (MCP client), `skills`, `memory` (store interface, governance, router; `memory/files` and `memory/execstore` backends), `redact` (secret and invisible-text cleaning) and `extproc` (exec protocol v1 and credential stripping for external commands).
- Release notes are in [CHANGELOG.md](CHANGELOG.md); design notes and plans are in [docs/](docs/README.md). CI runs tests on Ubuntu and Windows with Go 1.26 and stable. The old control plane (team, cron, orchestrator) is kept at the `v0.1.0` tag.

## License

Suga is distributed under the [MIT License](LICENSE).
