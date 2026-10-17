<!-- template_id: plan; template_version: 1.2.0 -->
# 网络与接入实施计划

> 状态：Draft 0.1 / 待用户审批

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `docs/design/2026-10-18-net-access-design.md` Draft 0.1 与 `suga-gtr6`、`suga-8pvi`、`suga-48zd`，拆成代理、模型能力、MCP HTTP 三组独立实施任务和外部验证 Gate；待用户审批。 |

## 目标与完成定义

目标：在用户批准后分别交付三件事：全局代理与三类公共出网统一选择；模型能力声明与 per-model fallback；MCP Streamable HTTP、认证头、超时和冲突诊断。批准前只提交本设计与本计划，不改 runtime、协议或配置 reader。

完成定义：

1. gtr6：`network.proxy.url/sites` 通过 registry 校验；全局未配置时 fetch/LLM/search 保留现状，配置后只按站点名单走显式 proxy；web_fetch 的私网、跳转和大小/超时规则仍通过；真实 `http://127.0.0.1:10801` 代理至少验证模型、fetch、search 及一次带工具调用 provider 对话。
2. 8pvi：模型/ provider 能力字段能控制窗口、max output、thinking mode、prompt cache、stream/stream_required；旧配置和未声明模型保持启发式回退；每个 fallback 使用自己的 max output；`suga models` 与 manifest 离线展示有效能力和来源。
3. 48zd：stdio 不回归；Streamable HTTP 能完成 initialize、tools/list、tools/call、session header、超时/取消和 JSON/SSE response；认证值只从 `${VAR}`/ACP 显式 headers 进入内存；ACP 接受 HTTP；同名工具 first-wins 且 log/runtime diagnostics 可见；preferred/deferred/approval 语义不变。
4. 每个功能点有近端单测、至少一次反向验证和独立本地 conventional commit；不 push、不打 tag、不修改 `.gofer/tracker`。
5. Windows 主机命令与 Linux 容器 `-race`、Linux PTY/ACP（如涉及）和真实 provider/HTTP MCP 外部动作均有证据；无法运行的动作明确标记 `UNVERIFIED_*`，不以 fixture 冒充真实验证。

## 范围、排除项与授权

- 范围：设计中 A/B/C 的配置、解析、运行时接线、展示、测试和使用文档；实施只在当前 worktree 进行。
- 排除：MCP 纳入全局 proxy；PAC/自动发现/TLS MITM；legacy SSE；远程 model capability 探测；cache TTL/compact_at；MCP 自动重命名；approval/deferred/preferred 权限语义变更；provider 专用 gateway 字段扩张。
- 兼容边界：全局 proxy 未配置时不改变三类出网点的既有环境代理差异；旧 `tools.web_fetch.proxy` 仅在全局 URL 为空时 fallback；旧 provider 字段和启发式继续可用；旧 stdio MCP、旧 ACP stdio 和旧 MCP wire 行为不变。若要修改这些语义，停止并回到设计 Gate。
- `host_or_non_offline_action=REQUIRED`：主机代理真实请求、真实 provider 带工具调用、真实 MCP HTTP/ACP、Linux `-race`/PTY 与 Windows job 都是实施完成所需的外部环境动作，计划阶段不执行；Windows 主机不能代替 Linux PTY/race。
- 授权：用户批准设计与本计划后才实施；批准只授权当前 worktree 的本地修改和提交，不授权 push/tag/release/tracker 修改。

## 输入与批准证据

- 设计：`docs/design/2026-10-18-net-access-design.md`，Draft 0.1 / 待用户审批。
- issues：`suga-gtr6` 全局代理/现状核对；`suga-8pvi` 能力字段与 fallback max output；`suga-48zd` HTTP/SSE、headers、冲突与 startup timeout。
- gtr6 现状：`pkg/agentcore/tools/native/fetch.go:63-128,132-167`、`pkg/agentcore/llm/claude.go:103-121`、`openai.go:58-80`、`openai_stream.go:18-25`、`tools/native/search.go:21-37`、`internal/wiring/capabilities.go:220-226`、`internal/config/settings_registry.go:152-154`。
- 8pvi 现状：`internal/config/providers.go:27-49,569-591`、`context_window.go:5-33`、`thinking.go:23-45`、`pkg/agentcore/llm/claude_thinking.go:33-78`、`compat.go:9-77`、`compat_deepseek.go:5-23`、`compat_commandcode.go:3-18`、`model_catalog.go:11-42,146-159`、`internal/cli/models.go:14-19,64-87`、`internal/wiring/manifest.go:45-59,327-333`。
- 48zd 现状：`pkg/agentcore/tools/mcp/client.go:1-39,55-145`、`provider.go:29-47,69-129`、`internal/config/mcp.go:11-59,102-105`、`internal/acpserver/policy.go:467-488`、`internal/wiring/capabilities.go:45-79`、`go.mod:5-18`、`docs/design/2026-10-17-preferred-tools-design.md:29-32,121-136`。
- 实施前必须重新确认 HEAD、branch、dirty 状态，并以代码事实为准；若现状与设计有冲突，不进入 T1。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 三类公共出网的按站点显式代理与兼容 fallback | `fetch.go:98-128`；LLM client constructors；`search.go:30-37`；`x/net/proxy` | `http.Transport` (`pkg/agentcore/tools/native/fetch.go:121-128`), existing fetch target check (`fetch.go:148-167`), current LLM/search client constructors (`pkg/agentcore/llm/openai_stream.go:21-25`; `pkg/agentcore/tools/native/search.go:30-37`) | `internal/config` proxy reader + 一个 selector；各调用点只注入 client | MINIMAL_NEW_MODULE | 当前没有全局 selector；fetch/model/search 语义相反 | selector 不读环境；未配置分支必须独立测试；web_fetch 安全 owner 仍是 fetch |
| CAP-02 | 模型能力解析、请求形状与 catalog/manifest projection | `providers.go`、`context_window.go`、`claude_thinking.go`、`compat.go`、`model_catalog.go` | `ModelSpec` (`internal/config/providers.go:97-117`)、existing per-provider/model fields (`providers.go:27-49,60-95`)、compat fallback (`pkg/agentcore/llm/compat.go:52-77`)、catalog/manifest (`model_catalog.go:30-42`; `internal/wiring/manifest.go:45-59`) | `ResolvedModelCapabilities` 与能力 reader；provider 消费 resolved data | MINIMAL_NEW_MODULE | thinking/stream/cache 仍有启发式；fallback request 共享 | 只能有一个 config owner；catalog/manifest 不再各自推断；旧 heuristic source 可回滚 |
| CAP-03 | MCP Streamable HTTP JSON-RPC client | `mcp/client.go`/`provider.go`；`net/http`；go.mod；ACP SDK | `toolClient` (`pkg/agentcore/tools/mcp/provider.go:29-34`)、JSON-RPC structs/call (`pkg/agentcore/tools/mcp/client.go:183-220`)、tool adapter (`client.go:143-160,346-379`)、call timeout/output cap (`client.go:32-42`) | `httpClient` 与 transport-specific ServerSettings；不引第三方 MCP SDK | MINIMAL_NEW_MODULE | 当前 Client 只有 exec pipe；依赖库无 MCP HTTP client | stdio/HTTP 共用 Provider 生命周期；session/response body/Stop 需独立 owner |
| CAP-04 | MCP 配置 env headers、超时与 ACP HTTP shape | `internal/config/mcp.go`、`settings_registry.go`、ACP `McpServer` types | existing `${VAR}` provider expansion (`internal/config/providers.go:267-275`)、`mcp.call_timeout_sec` (`internal/config/mcp.go:102-105`)、ACP sessionInputs/stdio mapping (`internal/acpserver/policy.go:467-488`) | mcp reader 增 url/transport/headers/startup timeout；ACP adapter 增 HTTP branch | THIN_ADAPTER | headers/url 不存在；ACP 只接受 stdio | config env expansion 不延伸到 ACP payload；secrets 不进 diagnostics |
| CAP-05 | 冲突告警且不改变 preferred/deferred/approval | `provider.go:116-129`；`tools.Registry`；preferred design；manifest | first-wins (`pkg/agentcore/tools/mcp/provider.go:121-129`)、existing slog warning (`provider.go:126`)、`tools.IsMCP` and preferred/Visibility/approval (`internal/wiring/capabilities.go:61-79`; `pkg/agentcore/tools/visibility.go:117-170`; `internal/wiring/approval_policy.go:174-220`) | conflict diagnostics + runtime tools/manifest view | OWNER_EXTENSION | 现有 warn 不可查询，standalone manifest 不启动 MCP | 不自动重命名；diagnostics 不含 URL/args/env/header；权限仍由既有 policy |
| CAP-06 | 外部/跨平台/协议验证 | `fetch_test.go`、LLM tests、MCP tests、`acpprobe`、host jobs、`scripts/ptysmoke` | httptest and existing fetch tests (`pkg/agentcore/tools/native/fetch_test.go:136-186`)、LLM tests (`pkg/agentcore/llm/*_test.go`)、ACP probe (`scripts/acpprobe/acp_probe.py`)、host jobs and PTY smoke (`scripts/ptysmoke/README.md`) | 新 proxy/MCP fixtures、model request assertions、必要 smoke scenario | OWNER_EXTENSION | 当前无三类集成覆盖和真实 HTTP MCP fixture | fake server 不能替代真实 provider；不能在 Windows 声称 pty/race |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-01 | CAP-01 | `internal/config/network_proxy.go` + `pkg/agentcore/netproxy` | settings registry；fetch transport；LLM/search client constructors | 没有全局站点 selector 和 SOCKS5 拨号封装 | reader 校验 + 纯 selector；调用者注入 client | 三条路径没有共同 transport | netproxy 只拥有选择规则，fetch 仍拥有 SSRF check | 删除后 fetch 回 legacy proxy、LLM/search 回默认 transport；无数据迁移 |
| MOD-02 | CAP-02 | `internal/config/model_capabilities.go` | providers/context/thinking/catalog/manifest | 没有逐字段 resolved capability/source projection | 一份 resolver + provider/catalog/manifest adapter | 名称启发式散落在 config/llm/compat | config owner 维护 fallback 顺序，provider 不复制推断 | 删除后旧字段/heuristic 直接恢复；未知 capabilities 被旧版忽略 |
| MOD-03 | CAP-03 | `pkg/agentcore/tools/mcp/http_client.go` | mcp stdio client/provider；标准库 net/http | 没有 HTTP lifecycle/session/SSE response parser | 实现 `toolClient` 的 HTTP client，复用 JSON-RPC/tool adapter | go.mod 没有 MCP HTTP transport | HTTP client 自己负责 response body/session/close，Provider 只编排 | 删除后 URL 配置报 unsupported，stdio 不受影响；无用户数据迁移 |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| MCP 专用第三方 HTTP/SSE SDK | CAP-03 | 删除后标准库 client 仍复用现有 JSON-RPC；当前 go.mod 没有可直接复用的 MCP client，引入 SDK 会增加版本/生命周期风险。 |
| 全局 `NO_PROXY` 反向名单模式 | CAP-01 | 删除后 allowlist + `*` 已表达直连/全代理；反向语义会把未列出的内部 host 送入 proxy，安全默认更差。 |
| provider/gateway 专用 capability structs | CAP-02 | 删除后通用 `thinking/stream/cache` 字段仍覆盖请求形状；专用字段会复制 compat 和配置，扩大维护面。 |
| legacy SSE 与 Streamable HTTP 同批 | CAP-03 | 删除后第一版仍有清晰的 unsupported error；当前只有注释没有可回退 SSE 实现，双生命周期会增加失败面。 |
| MCP 工具自动重命名 | CAP-05 | 删除后 first-wins + owner diagnostics 足够解释冲突；改名会改变模型可见的工具契约并破坏 preferred/allowlist。 |
| 远程 provider capability discovery | CAP-02 | 删除后显式 config + heuristic fallback 仍能工作；联网探测会让 `suga models` 不再 offline，并增加 secrets/network 失败。 |

## 前置检查

- P0：记录 `git status --short`、HEAD、branch、`git diff --stat`；确认只有本批两份文档；每个功能点提交前检查 `git diff --cached --name-only`，不 stage tracker。
- P1：重读设计与三个 issue 的全部描述/评论；确认状态仍为待审批；确认 `network.proxy`、`capabilities`、HTTP MCP reader 不存在或未有未记录实现。
- P2：近端基线（实施批准后）：`go test -count=1 ./internal/config/... ./internal/wiring/... ./internal/acpserver/... ./pkg/agentcore/llm/... ./pkg/agentcore/tools/...`；失败标 `BASELINE_FAILED`，不进入功能实现。
- P3：用 `go doc`/源码确认 Go 1.26 `http.Transport`、`x/net/proxy` 的 SOCKS5 行为，以及 ACP SDK `McpServerHttpInline` 的真实字段；禁止凭猜测写 ACP adapter。
- P4：确认 `settings_registry` 的 dynamic provider/model path、`${VAR}` expansion、candidate validation 和敏感值输出；network proxy 与 MCP headers 不能进入 `config list/get` 明文或 manifest。
- P5：确认现有 Responses stream path 和 fallback pool 是否能在 provider 边界复制 request；若必须改变公共 `Provider`/ACP 协议，停止回 Gate。
- P6：准备 `httptest` proxy、最小 Streamable HTTP MCP server 和 fail/slow variants；测试产物只放 worktree `tmp/`，结束删除。

## 波次与依赖

| 波次 | 任务 | 依赖 | 执行方式 |
|---|---|---|---|
| W0 | T0 前置检查与 Gate | 无 | 只读；基线/SDK事实冲突即停止 |
| W1 | T1 gtr6 配置与 selector | T0 | config → pure selector，独立提交 |
| W1 | T2 gtr6 三类 client 与 web_fetch 安全 | T1 | LLM/native 接线与近端测试，独立提交 |
| W2 | T3 8pvi capabilities 与展示 | T0 | config → llm → catalog/manifest，独立提交 |
| W3 | T4 48zd HTTP client/provider | T0 | HTTP lifecycle + config reader，独立提交 |
| W3 | T5 48zd ACP/diagnostics/preferred 接线 | T4 | 入口与 Registry/manifest diagnostics，独立提交 |
| W4 | T6 跨平台、真实 provider/MCP 与反向 Gate | T1–T5 | Windows/Linux/host external action；修复回所属提交 |

## 任务

### T0 前置检查与实现 Gate

- 文件：无产品改动；证据写入交付报告。
- 动作：执行 P0–P6；确认 `http.Transport` SOCKS5 实现策略、ACP HTTP struct 字段、fallback pool request ownership、registry reader 范围；核对三件事独立 owner。
- 验证命令：`go test -count=1 ./internal/config/... ./internal/wiring/... ./internal/acpserver/... ./pkg/agentcore/llm/... ./pkg/agentcore/tools/...`；`go doc net/http.Transport`；`git status --short`。
- 完成标准：基线 exit code=0；依赖事实与设计一致；没有公共协议/权限边界扩张。冲突则停止，不进入 T1。
- 反向验证：不适用；T0 fail-closed。
- 提交：不提交。

### T1 gtr6 配置与 proxy selector

- 文件：`internal/config/settings_registry.go`、`internal/config/network_proxy.go`（新）及测试；`pkg/agentcore/netproxy`（新）及测试；`config/config.yaml`、`docs/usage/llm-config-v0.md`、配置向导文档（如实现范围需要）。
- 动作：注册 `network.proxy.url`（sensitive/string）与 `network.proxy.sites`（writable string list/reader）；实现 `${VAR}` 解析、URL scheme/host 校验、精确 host/`.suffix`/port/`*` 匹配；HTTP/HTTPS 使用 transport proxy，SOCKS5 使用 x/net/proxy。全局 URL 为空返回“未配置”而非显式 direct，交由各调用方保持旧分支；全局 URL 有值时 selector 不读环境。旧 `tools.web_fetch.proxy` fallback 只在 URL 为空时保留。
- 验证命令：`go test -count=1 ./internal/config/... ./pkg/agentcore/netproxy/...`；隔离 `SUGA_HOME` 下 `suga config set/get/list/unset`；非法 scheme/host/site 与 credential-in-YAML fixture；不把 secret 出现在 registry 输出。
- 测试要点：`t.Run` 覆盖 no config、exact、subdomain、port、star、non-match direct、HTTP/HTTPS/SOCKS5、env URL、empty sites；用 assert 包，不碰真实 `~/.suga`。
- 完成标准：registry 与 runtime reader 只有一个 owner；新增函数有实际调用点；config candidate 失败不写回原文件；selector 不读取 `HTTP_PROXY`。
- 反向验证：临时移除 site suffix 匹配，子域用例失败；临时把空 sites 当 `*`，empty-list 用例失败；临时把 URL secret 写入 config output，脱敏断言失败。
- 预计改动规模：M，4–8 个 Go/测试/文档文件。
- 提交：`feat(net): add global proxy configuration and selector`。

### T2 gtr6 client 接线与 web_fetch 安全

- 文件：`pkg/agentcore/llm/claude.go`、`openai.go`、`openai_stream.go`、`responses.go`（如需）；`pkg/agentcore/tools/native/catalog.go`、`fetch.go`、`search.go` 及测试；`internal/wiring/capabilities.go`、启动校验与测试。
- 动作：有全局 proxy 时给非流式/流式 LLM、search、fetch 注入显式 selector client；无全局 URL 保持三条现状；fetch 使用全局 sites 做每 URL 选择但不跳过 `checkHost`，保留 redirect recheck、allow_private、timeout/size；启动期把坏 global URL 与 legacy proxy 分别报清晰错误。不要把 MCP HTTP client 接入 gtr6。
- 验证命令：`go test -count=1 ./pkg/agentcore/llm/... ./pkg/agentcore/tools/native/... ./internal/wiring/...`；测试 proxy 记录三类请求；`go vet ./pkg/agentcore/llm/... ./pkg/agentcore/tools/native/... ./internal/wiring/...`。
- 测试要点：未配置+环境 proxy 的兼容矩阵；配置后名单命中/不命中；web_fetch public through proxy、DNS private/literal/redirect private refused；stream client 的 `Proxy` 与 response header timeout 同时保留；legacy fetch-only fallback。
- 完成标准：模型/search/fetch 共用选择规则但不共用错误/输出处理；未配置分支现状测试通过；没有日志输出 URL credentials。
- 反向验证：临时删除 fetch proxy 前 `checkHost`，private fixture 必须失败；临时让 non-match 走 proxy，direct fixture 必须失败；临时只改非流式 client，stream proxy test 必须失败。
- 预计改动规模：L，8–14 个 Go/测试文件。
- 提交：`feat(net): apply global proxy to model and web clients`。

### T3 8pvi 模型 capabilities、fallback 与展示

- 文件：`internal/config/providers.go`、新 `model_capabilities.go`/测试、`settings_registry.go`；`pkg/agentcore/llm/claude_thinking.go`、provider constructors/request paths/测试；`internal/config/model_catalog.go`、`internal/cli/models.go`、`internal/wiring/manifest.go`、相关 manifest/runtime/fallback 测试；配置/LLM 文档。
- 动作：解析 provider/model `capabilities`；字段为 context_window、max_output_tokens、thinking（budget/adaptive/none）、prompt_cache、stream、stream_required；旧短字段映射为兼容默认；显式值逐字段覆盖 heuristic；`ResolvedModelCapabilities` 作为唯一 projection。将 Claude name heuristic、context name heuristic、compat model substring 降为 source=heuristic fallback。让 fallback 每次建立自己的 output/stream/thinking request，不沿用 main request 上限。catalog/manifest/text 增有效字段和 source，不显示 endpoint/secret。
- 验证命令：`go test -count=1 ./internal/config/... ./internal/wiring/... ./internal/cli/... ./pkg/agentcore/llm/... ./internal/personal/...`；`go vet ./internal/config/... ./pkg/agentcore/llm/...`。
- 测试要点：t.Run 覆盖 model>provider>legacy>heuristic；Claude budget/adaptive/none；cache on/off；stream false/true/required；Responses required `stream=true`；main/fallback A/B 各自 max output；旧配置 body 不变；models/manifest JSON offline projection。
- 完成标准：capability 未声明时既有模型请求回归；`stream_required` 与 `stream=false` 冲突启动失败；每个新 resolver/provider helper 有实际调用点；fallback test 证明 request 不共享 mutable limit。
- 反向验证：临时让显式 thinking 被 heuristic 覆盖，explicit mode test 失败；临时从 fallback copy 删除 max output override，A/B limit test 失败；临时把 models catalog 继续只读 context，capability display test 失败。
- 预计改动规模：L，10–18 个 Go/测试/文档文件。
- 提交：`feat(llm): resolve model capabilities explicitly`。

### T4 48zd Streamable HTTP client 与配置

- 文件：`pkg/agentcore/tools/mcp/client.go`/新 `http_client.go`/测试；`pkg/agentcore/tools/mcp/provider.go`/测试；`internal/config/mcp.go`、`settings_registry.go`、配置模板/文档。
- 动作：扩展 ServerSettings/ServerConfig 为 command 或 URL union；实现 Streamable HTTP initialize/tools/list/call/stop，`Mcp-Session-Id`、JSON/SSE response、response body close、call context；保留 stdio `toolClient`；增加 `mcp.startup_timeout_sec`，现有 call timeout 继续复用；headers config 值 `${VAR}` 展开且 secrets 仅在内存。单 server startup/list 独立有界。
- 验证命令：`go test -count=1 ./pkg/agentcore/tools/mcp/... ./internal/config/...`；`go vet ./pkg/agentcore/tools/mcp/... ./internal/config/...`。
- 测试要点：httptest server 的 initialize/list/call；JSON response、SSE response、session header、auth header、slow startup/call timeout、cancel/close；stdio 全套回归；url/command 互斥与 sse unsupported 明确报错。
- 完成标准：HTTP client 只通过 Provider 实际调用；没有第三方 MCP SDK；HTTP 与 stdio tool schema/Kind/CallTimeout/RawBytes 一致；测试不写系统临时目录（httptest/t.TempDir 仅测试例外）。
- 反向验证：临时不带 `Mcp-Session-Id`，session fixture 失败；临时把 JSON response 当 SSE 解析，JSON fixture 失败；临时删除 HTTP context timeout，slow call test 失败。
- 预计改动规模：L，6–12 个 Go/测试/文档文件。
- 提交：`feat(mcp): add streamable http transport`。

### T5 48zd ACP、冲突 diagnostics 与 preferred 接线

- 文件：`internal/acpserver/policy.go`/测试；`internal/config/mcp_cli.go`（如 Claude config 也接受 URL）；`internal/wiring/capabilities.go`、`manifest.go`/测试；`pkg/agentcore/tools/mcp/provider.go`/registry diagnostics；preferred/deferred 相关测试与文档。
- 动作：ACP mcpSettings 接受 SDK 的 stdio/HTTP shape，校验 name/URL/headers/唯一性，HTTP headers 不做宿主 `.env` expansion；将配置与 ACP 都映射同一 ServerSettings。Provider 保持 first-wins，保留 slog.Warn 并输出非敏感 conflict diagnostics；runtime `:tools`/manifest 显示实际冲突，standalone manifest 保持不启动 MCP。确认 HTTP MCP 注册成功后 preferred `kind:mcp` present，Visibility pinned、approval/ACP permission 与 stdio 完全相同。
- 验证命令：`go test -count=1 ./internal/acpserver/... ./internal/wiring/... ./pkg/agentcore/tools/mcp/... ./pkg/agentcore/tools/...`；`go vet ./internal/acpserver/... ./internal/wiring/...`；如有 CLI fixture 运行隔离 `suga manifest --json`。
- 测试要点：ACP stdio/http accepted、malformed rejected；HTTP MCP read_only permission；native/stdio/http same-name first-wins and diagnostics owner; preferred/auto/always/never; standalone manifest no server start and no secrets.
- 完成标准：无 HTTP 特殊 auto-allow；冲突可解释但工具名不变；preferred 以 registration success 为准；diagnostics 不含 URL path、args、env、Authorization token。
- 反向验证：临时删 conflict diagnostics，diagnostic assertion 失败；临时将 HTTP MCP 当非 MCP，approval test 失败；临时移除 preferred pin，deferred test 失败；临时让 standalone manifest 启动 server，offline manifest test 失败。
- 预计改动规模：M/L，7–14 个 Go/测试文件。
- 提交：`feat(mcp): expose http servers through acp and diagnostics`。

### T6 跨平台、真实 provider/MCP 与反向 Gate

- 文件：无新功能；必要时只回补所属任务的测试；若新增交互观测则 `scripts/ptysmoke/`/Makefile 归本任务。
- 动作：在 Windows 跑 build/vet/gofmt/test/staticcheck；Linux 容器跑 race；用主机 `http://127.0.0.1:10801` 做 gtr6 实测；至少一轮真实 provider 带工具调用；若无现成 HTTP MCP server，用仓库 `httptest` 做协议验收并明确真实 MCP 未验证；若通过 ACP 验证 HTTP，使用 `scripts/acpprobe/acp_probe.py`。按 house rule 做 T1–T5 反向验证，恢复后再跑正向。仅交互行为新增时才补 PTY；Windows 不运行/不声称通过 PTY。
- 验证命令：`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")`（无输出）、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...`；Linux `go test -race ./...`；需要时 `make smoke-chat`；proxy/real provider/MCP 命令见各任务。
- 完成标准：每条命令同时 exit code=0 且输出无新告警；真实/fixture 边界写明；`UNVERIFIED_REAL_MODEL`、`UNVERIFIED_REAL_MCP_HTTP`、`UNVERIFIED_PTY` 不得隐藏；反向测试能证明检查确实守住行为。
- 反向验证：本任务负责汇总证据，不替代 T1–T5 的近端反向断言。
- 预计改动规模：S/M，仅测试或文档证据。
- 提交：无独立空提交；若补测试，按所属功能点追加 `test(<scope>): cover ...`。

## 提交与合并顺序

获批后按功能点本地提交，不 push：

1. `feat(net): add global proxy configuration and selector`（T1）
2. `feat(net): apply global proxy to model and web clients`（T2）
3. `feat(llm): resolve model capabilities explicitly`（T3）
4. `feat(mcp): add streamable http transport`（T4）
5. `feat(mcp): expose http servers through acp and diagnostics`（T5）
6. T6 只有补测试时追加所属 scope 的 test commit；不制造空提交。

每次提交前执行 `git status --short`、`git diff --stat`、`git diff --cached --name-only`，只 stage 当前功能文件；不改 `.gofer/tracker`。修复回归所属功能点，不把无关清理混入提交。

## 回滚与恢复

- T1 独立 revert：删除 `network.proxy` reader/selector；旧 fetch fallback 与 LLM/search 环境行为恢复，无迁移。
- T2 独立 revert：三类 client 回到现有构造，fetch 回到现有 `tools.web_fetch.proxy` wiring；SSRF 测试和安全规则保留。
- T3 独立 revert：旧 model/provider fields、heuristics、compat、catalog/manifest 字段恢复；新 `capabilities` 是可忽略配置，不需改写用户 YAML。
- T4 独立 revert：url/headers/transport 配置停止可用并返回 unsupported，stdio client/provider/timeout/registry 不变；无用户数据迁移。
- T5 独立 revert：ACP 回到 stdio-only，删除 conflict diagnostics 扩展；first-wins 与原 warning 可保留；preferred/deferred/approval 不回滚到新权限。
- 所有回滚都不删除用户 `.env`，但交付说明提醒用户移除不再使用的 proxy/MCP env references；不把 secret 写入 Git。

## 人工 Gate

1. Gate A：用户批准设计与计划；批准前只保留两份 Draft 文档提交。
2. Gate B：T0 重新核对 HEAD/代码/ACP SDK；若需要公共协议、MCP legacy SSE、全局 MCP proxy 或权限语义扩张，暂停。
3. Gate C：T1–T5 近端单测和反向验证通过后，才做主机代理、真实 provider、真实 ACP/MCP 和 Linux race/PTY 外部动作。
4. Gate D：监督核对 Windows 输出、Linux race/PTY、真实 provider 工具调用、真实/fixture MCP 边界和 secrets 脱敏；证据齐全后才宣布实施完成。不得 push/tag/release。

## 可追溯性

| 设计要求 | 计划任务 | 证据 |
|---|---|---|
| gtr6 全局 URL/sites 与未配置兼容 | T1/T2 | registry、selector 矩阵、default transport 兼容测试 |
| gtr6 web_fetch 私网规则在 proxy 下保留 | T2/T6 | private DNS/literal/redirect tests；真实 proxy 不替代拒绝断言 |
| gtr6 主机代理和真实 provider | T6 | `127.0.0.1:10801` 代理日志、带工具调用对话 |
| 8pvi capabilities 字段与 fallback 独立上限 | T3 | config/provider body、fallback A/B request tests |
| 8pvi heuristic fallback 与展示关系 | T3 | old config body、catalog/models/manifest offline tests |
| 48zd HTTP transport 与现有 stdio | T4 | httptest JSON/SSE/session/call/timeout + stdio regression |
| 48zd env headers 和不泄露 | T4/T5 | `${VAR}` fixture、manifest/log/diagnostic redaction |
| 48zd ACP 放开 HTTP且不放权 | T5 | ACP HTTP fixture、permission/read_only assertions |
| 48zd 冲突告警与 v0.29 preferred | T5 | first-wins diagnostics、preferred/deferred/approval matrix |
| 三件事可分别回滚 | T1–T5 | 原子提交、无共享迁移/协议版本变更、diff review |

## 完成 Gate

宣布完成必须同时满足：

- 文档 validator：
  `python -I D:\work\inhere\runtime\idev-std-releases\0.26.1\plugins\inhere-dev-standards\skills\inhere-doc-workflow\scripts\validate_document.py --kind design docs/design/2026-10-18-net-access-design.md`
  与
  `python -I D:\work\inhere\runtime\idev-std-releases\0.26.1\plugins\inhere-dev-standards\skills\inhere-doc-workflow\scripts\validate_document.py --kind plan docs/plans/2026-10-18-net-access-plan.md`
  均 exit code=0 且 `errors=[]`。
- Windows：`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")` 无输出、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...` 均 exit code=0。
- Linux：`go test -race ./...`；只有新增交互时才 `make smoke-chat`，并有正反向日志。Windows 不声称 PTY/race 通过。
- gtr6：主机代理 `http://127.0.0.1:10801` 的模型、fetch、search 与真实 provider 工具调用证据；无法执行写 `UNVERIFIED_REAL_MODEL`。
- 48zd：真实 HTTP MCP initialize/list/tool call；若没有现成 server，httptest 只能证明协议实现，交付标记 `UNVERIFIED_REAL_MCP_HTTP`；ACP HTTP 需 `acpprobe` 或等价证据。
- T1–T5 每项至少一条“去掉逻辑后失败、恢复后通过”的反向证据；`git status --short` 无夹带，提交列表只有本批对应功能点，不 push。
