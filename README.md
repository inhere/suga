# Suga

[![Unit-Tests](https://github.com/inhere/suga/actions/workflows/go.yml/badge.svg)](https://github.com/inhere/suga/actions/workflows/go.yml)

English | [简体中文](README.zh-CN.md)

Suga is a personal coding agent written in Go. One persistent conversation engine serves two interfaces: a terminal CLI (`chat` and one-shot `run`) and an ACP stdio server (`acp`).
[Gofer](https://github.com/inhere/gofer) can launch either one and adds jobs, workers, logs and interactive approval on top.

> The full documentation lives in the reference docs of the built-in skill [suga-guide](skills/suga-guide/SKILL.md) (`references/`, written in Chinese). Suga unpacks it to `~/.suga/skills/suga-guide/` on first run, so you can simply ask chat how to configure suga.

## Features

- **Development tools**: read files in line windows, list, grep and find, exact-replacement edits, shell commands (PowerShell on Windows), web fetch and search.
- **Safe by default**: read-only unless writing is enabled; chat asks before writing or running commands; file tools stay in the workspace; web fetches refuse private networks.
- **Persistent sessions**: one SQLite database with resume, automatic compaction, plans, handoff and an event log for tracing and stats.
- **Sub-agents and background tasks**: read-only sub-agents bring back only a report; long commands and watchers run in the background and notify the model.
- **Skills**: standard `SKILL.md` skills, listed or searched on demand; a built-in configuration and usage guide, `suga-guide`.
- **MCP**: stdio MCP servers with allowlists and deferred loading for large tool sets.
- **Persistent memory**: user and project memory across sessions, with swappable backends (files, gofer or an external command).
- **History search and output offloading**: full-text search over earlier sessions; oversized tool results are saved aside, the context keeps the head, the tail and a ref.

## Install

- **Recommended**: install with [eget](https://github.com/inherelab/eget): `eget install inhere/suga`
- With Go: `go install github.com/inhere/suga/cmd/suga@latest` (private access needs `GOPRIVATE=github.com/inhere/*`)
- Download an archive from [GitHub Releases](https://github.com/inhere/suga/releases) (a pushed `v*` tag publishes Linux, macOS and Windows builds) and put `suga` on your `PATH`.
- From source (Go 1.26+): `git clone https://github.com/inhere/suga.git && cd suga && make install`
  - Builds (compressing with `upx` when available) and installs to `$(go env GOPATH)/bin`; on Windows it first moves aside a binary that a running chat still uses.
- Make sure the install directory is on the `PATH` of your shell (and of the Gofer server, if used), then check `suga version`.

## Quick start

Details: [references/config.md](skills/suga-guide/references/config.md) (onboarding and configuration commands, Chinese)

```bash
suga init                                         # create ~/.suga, a commented config.yaml and a comment-only .env (existing files are kept)
# put keys in ~/.suga/.env, e.g. DEEPSEEK_API_KEY=... (built-in providers: openai / anthropic / deepseek)
suga config set model deepseek/deepseek-chat      # pick a model (<provider>/<model>); secrets cannot be set this way
suga config set tools.allow_write true            # let chat write files and run commands (it asks first)
suga models                                       # list usable models offline; suga config get <key> shows value and source
suga chat                                         # interactive chat in the current directory
```

- One turn: `suga run --prompt "Summarize this project"` (read-only), `suga run --allow-write --prompt "Fix the failing test"`, `suga run --resume <session-id> --prompt "Continue"`; `--max-steps` caps the turn's steps (default 240).
- [config/config.yaml](config/config.yaml) lists every field; `suga manifest` reports the full effective capabilities.

## Chat

Details: [references/chat.md](skills/suga-guide/references/chat.md) (Chinese)

- **Local commands**: `:session`, `:compact`, `:quit`/`:exit`, `:diff [N]`, `:model [p/m]`, `:add-dir`/`:dirs`/`:rm-dir`, `:help`, `:usage`, `:tasks`; keyboard input only.
- **Input**: Up/Down history, Ctrl+R reverse search, Tab completion for `:` commands and directories; newline with `Ctrl+Enter`/`Ctrl+J` (Windows) or `Alt+Enter`.
- **Steering**: while the model works the prompt becomes `↳ ` and Enter adds the line to the running turn; `Esc` stops only the turn, two `Ctrl-C` within 3s quit.
- **`!` local commands**: `!cmd` runs in the local shell without the model; `!!cmd` also attaches the result to your next message.
- **Resume preview**: resuming shows the last model reply first; a session left `running` by a killed process is recovered.
- **Delivery**: `suga send --session <id|short name> <text>` feeds a running chat; `--steer` targets the current turn, `--wait` waits for delivery.

## Configuration

Details: [references/config.md](skills/suga-guide/references/config.md) (every key and default), [references/providers.md](skills/suga-guide/references/providers.md) (providers and models) (Chinese)

- Only `$SUGA_HOME/config.yaml` and `$SUGA_HOME/.env` are read (default `~/.suga`), never the working directory's; `SUGA_CONFIG` names a single config file.
- `suga init` is safe to repeat; `suga init --force` backs up and replaces only the home config. It never writes `.env` values, and refuses to bypass an explicit `SUGA_CONFIG`.
- `suga config get|set|unset|list|path` shows effective values and sources, preserves YAML comments, and rejects secret keys in YAML; put credentials in `.env` and reference them as `${VAR}`.
- `suga models [--json]` is an offline view of local providers and model references. It does not replace `suga manifest` or chat's session-local `:model` switch.
- `model` is `<provider>/<model>`, precedence `--model` > `SUGA_MODEL` > config; `fallback_models` are tried in order, `compact_model` writes summaries.
- `providers.<id>`: `type` (`openai`, `openai-responses`, `anthropic`), `base_url`, `api_key`, `headers`, plus per-model `context_window`, `max_output_tokens`, `model_id`, `price`, `thinking`.
- Common switches: `tools.allow_write`, `tools.approval`, `mcp.servers`, `memory.*`, `hooks.*`, `chat.*`, `skills.*`, `workspace`.
- All data lives in `$SUGA_HOME/db/sessions.db`.

## Tools and permissions

Details: [references/tools.md](skills/suga-guide/references/tools.md) (Chinese)

- **Tools**: `file_read`/`file_list`/`file_grep`/`file_find`, `web_fetch`/`web_search`, `skill`, `session_search`/`recall`, `agent`, `memory`, `file_write`/`file_edit`/`file_remove`, `shell_exec`, `task`.
- **Read-only by default**: write tools need `run --allow-write`, `tools.allow_write: true` in chat, or ACP `default` mode; `--read-only` always wins.
- **Approval**: with `tools.approval: ask` chat asks before writes and shell: `y` once, `a`/`d` allow or deny the same scope (command prefix, directory, path) for this session, optionally saved to a rules file; dangerous recursive deletes always need a typed `yes`.
- **Undo**: `file_write`/`file_edit`/`file_remove` snapshot a file before changing it; in chat `:checkpoints` lists and `:undo [N] [force]` rolls back, from the CLI `suga undo <session>`; `shell_exec` changes are listed, not rolled back (`tools.snapshots.*`).
- **Approval rules**: `tools.approval_rules` reads `allow`/`ask`/`deny` rules (tool, command and path globs) from `$SUGA_HOME/approval-rules.yaml` and `<workspace>/.suga/approval-rules.yaml`; the strongest match wins, project `allow` needs `project_trust`, and rules never open protected paths (`$SUGA_HOME`, `.suga/`, `.git/`).
- **Directories**: `--add-dir <absolute dir>[:ro|:rw]` / `SUGA_ADD_DIRS` add directories; in chat, access outside them asks `[r]/[w]/[n]`.
- **Read-only roots**: dependency source caches and idev-std directories are found automatically (`tools.dependency_roots`, `tools.standards_roots`); add more with `tools.read_roots`.
- **Background and watch tasks**: `shell_exec` with `background: true` runs in the background; `every` / `watch_paths` turn it into a watcher that notifies the model on changes.
- **Large output**: results over `tools.max_output_bytes` are offloaded to a ref; shell test/build output is first trimmed by output rules (customizable in `output-rules.yaml`).
- **MCP**: `--mcp-config` or `mcp.servers`; all calls are denied until allowed in `tools.auto_allow` / `tools.read_only_allow`.
- **Preferred tools**: `tools.preferred` declares commands (`rg`, `xerj`) and MCP servers this machine already has; the available ones are listed in the environment section (a `kind: command` entry only in a session that registered `shell_exec`, so a read-only session leaves it out) and `suga manifest` shows `present` plus a reason for every declaration, including the missing ones, with command reasons naming that read-only limit. Preference only ranks tools: it never allows an MCP call or skips the `shell_exec` approval, and a missing tool just stays out of the prompt.

## Sub-agents

Details: [references/tools.md#子-agent](skills/suga-guide/references/tools.md#子-agent) (Chinese)

- The `agent` tool hands an independent task to a read-only sub-agent and brings back only its final report.
- Two types: `explore` (default, workspace read tools) and `research` (adds `web_fetch` and `web_search`).
- `background: true` runs it as a task; `task wait` returns the report, and several can run in parallel.
- Limits `agents.max_steps` (30), `agents.max_tokens` (400000), `agents.timeout` (10m); `agents.model` picks a cheaper model.
- Each call is a child session: `suga sessions --agents` lists them, and their usage counts toward the parent.

## Skills

Details: [references/sessions.md#skills](skills/suga-guide/references/sessions.md#skills) (Chinese)

- Scanned in order: `<workspace>/.agents/skills`, `<workspace>/skills`, `~/.suga/skills`, `~/.agents/skills`; the first of a name wins.
- The built-in `suga-guide` is unpacked on first run and refreshed on upgrade only while you have not changed its files.
- Beyond `skills.list_max` only names are listed; the model loads one by name or searches with `skill(query=…)`; `skills.always_load` keeps a body in every prompt.

## Memory

Details: [references/sessions.md#记忆](skills/suga-guide/references/sessions.md#记忆) (Chinese)

- The model writes cross-session memory with the `memory` tool: `user`, `feedback`, `project` and `reference` entries.
- User memory lives in `$SUGA_HOME/memory/user`; the project backend is picked automatically (repo-root `MEMORY.md`, `.gofer/tracker`, or `$SUGA_HOME/projects/<key>/memory`).
- The memory index is frozen per segment; what this session writes shows up in the next segment.
- `memory search` on the files backend ranks hits through a rebuildable FTS5 index (Markdown stays the source of truth), and entries may carry optional `applies_when` conditions.
- A session that has read outside content must confirm memory writes (`memory.write_tainted`).

## Sessions

Details: [references/sessions.md](skills/suga-guide/references/sessions.md) (Chinese)

- `--session-id` creates or reuses a session, `--resume` requires it to exist (exit code 4 otherwise), `run --from` starts a new one carrying over the summary and plan.
- Resuming from another directory asks first (switch to the original / continue here / cancel); without a terminal use `--allow-cwd-change`.
- Near the context window or on `:compact` a new segment starts with a handoff summary (fixed sections); old segments stay reachable through `session_search` / `recall`, and `session_search` with `kind: handoff` finds the summaries themselves.
- An interrupted turn is replayed up to its last complete tool call, with a short recap.
- Optional history folding (`context.fold_every`); idle chat compacts in the background (`context.idle_after`).
- The system prompt holds only stable content; environment, rules (`AGENTS.md`), memory index and skills list go into each segment's context message.

## Using with Gofer

Details: [references/gofer.md](skills/suga-guide/references/gofer.md) (Chinese), [docs/usage/gofer-personal-agent.md](docs/usage/gofer-personal-agent.md)

- Gofer launches `suga run` (CLI jobs) or `suga acp` (persistent sessions with per-call approval through `permission_policy`).
- Agent configuration, read-only and writable runs, stream-json events and the denial contract are covered in the two documents above.

## Observability

Details: [references/observability.md](skills/suga-guide/references/observability.md) (Chinese)

```text
suga trace <session> [--turn <id>] [--json]       # timeline: model calls, tools, denials, plans
suga trace <session> --analyze | --web            # loop analysis / local web page
suga sessions [keywords] [--since 7d] [--agents]  # session list
suga stats [--session <id>] [--json]              # calls, latency, tokens, cache, cost
suga export <session> | suga gc --older-than 30d  # export / clean up
suga gc --shrink-fts [--status|--abort]          # shrink the tool-output FTS index
suga gc --vacuum                                 # rewrite the db file, truncate the WAL
suga backup --output <file>.suga-backup.zip       # verified online backup; spill excluded by default
suga restore <file>.suga-backup.zip --dry-run     # validate without changing the home
suga restore <file>.suga-backup.zip               # staged restore with a pre-restore backup
suga ps | suga manifest                           # running chats / capability manifest
```

- `run --output-format stream-json` emits cumulative `usage` after each model call, `retry` on retries, and `wrap_up` on the `result` of a turn ended by a limit.
- `trace --web` opens the session store read-only (a missing or uninitialized store is an error; nothing is created or migrated); its auto refresh switch is off by default, and once on pulls only new steps: every 5s while a turn runs, every 15s when idle (to notice the next turn).
- Diagnostic logs go only to `$SUGA_HOME/logs/suga.log`; `SUGA_DEBUG=1` also writes them to stderr.

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
