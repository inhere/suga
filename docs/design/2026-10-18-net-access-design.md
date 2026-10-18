<!-- template_id: design; template_version: 1.1.1 -->
# 网络与接入设计

> 状态：Draft 0.2 / 待用户审批

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `suga-gtr6`、`suga-8pvi`、`suga-48zd` 核对 v0.31.0 代码，设计全局代理、模型能力声明与 MCP Streamable HTTP 接入；三件事保持独立，待用户审批。 |
| 0.2 | 2026-10-18 | 监督（Claude） | 审核简化 8pvi：不新增嵌套 capabilities，只在现有 providers.*（及 models.*）层补 thinking_style 与 stream_required（见“0.2 审核修订”）；Q1–Q5 按建议。 |

### 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：把出网代理从隐式环境行为收口为可解释的全局配置；让 provider 按模型声明能力而不是模型名猜协议细节；让已存在的 MCP provider 能以受控的 Streamable HTTP 接入，并保持现有工具治理。
- allowed_scope：`internal/config` 的 settings registry 与 provider/MCP reader、`pkg/agentcore/llm` 的 HTTP client 与模型能力消费、`pkg/agentcore/tools/native` 的网络工具、`pkg/agentcore/tools/mcp` 的 HTTP transport、`internal/wiring`/`internal/acpserver` 的装配与 manifest/ACP 接线、`internal/cli` 的 models/manifest 展示、对应测试与配置/使用文档。
- non_goals：不把 MCP 纳入全局代理第一版；不引入向量或远程模型目录探测；不为每个 provider/gateway 做专用配置；不自动安装 MCP server；不改变 approval、deferred、preferred 工具的权限语义；不支持旧 SSE，除非后续真实兼容需求证明必要。
- expansion_policy：DEFER_OR_REQUEST。若需要改动 `memory`、ACP 协议版本、工具审批边界、web_fetch 的私网安全边界，停止实施并回到本设计 Gate。
- review budget：小。三件事分别拥有配置、运行时和回滚边界；优先复用现有 `http.Transport`、provider config、MCP JSON-RPC 与 `tools.Registry`，不复制协议实现。
- 停止条件：用户批准本设计和对应计划前不实施。

## 0.2 审核修订（与下文冲突时以本节为准）

- **8pvi 简化**：不新增 `capabilities` 嵌套层。`providers.<id>` 与 `providers.<id>.models.<name>` 下已有 `context_window`、`max_output_tokens`、`thinking`、`prompt_cache`，继续沿用；只补两个缺失字段（模型级覆盖 provider 级）：
  - `thinking_style: budget | adaptive | none`——替代按模型名猜 thinking 请求形状；未声明时保留现有启发式。
  - `stream_required: true`——只接受流式请求的网关；所有请求（含摘要等内部请求）走流式路径。
- Q1–Q5 按建议结论执行。

## 背景与目标

三个 issue 都是 v0.31.0 的接入边界问题，但不共享一个必须同时上线的抽象：

1. `suga-gtr6`：`web_fetch` 显式忽略环境代理，而 provider 和 `web_search` 的默认 `http.Transport` 隐式读取 `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`。需要一个全局代理地址和站点名单，且保留未配置时的兼容行为。
2. `suga-8pvi`：窗口和 Anthropic thinking 方式仍有模型名启发式；fallback 请求还共享一个 `GenerateRequest`，不能让每个 fallback 使用自己的输出上限。需要把协议能力声明放在 `providers.<id>.models.<model>`，未声明时平滑回退。
3. `suga-48zd`：MCP 实现实际只有 stdio，ACP 也明确拒绝非 stdio；注册工具重名时虽已有日志，但工具目录/运行时诊断没有统一的冲突结果。需要 Streamable HTTP、认证头、独立发现超时和可见告警。

目标（每件事独立验收）：

- gtr6：模型请求、`web_fetch`、`web_search` 共享同一代理选择器；全局未配置时不改变现有行为；代理认证不进入 YAML、manifest、trace 或日志。
- 8pvi：能力字段覆盖 `context_window`、`max_output_tokens`、thinking 方式、prompt cache、流式支持/是否强制流式；模型 catalog、`suga models` 和 manifest 显示生效值及来源；fallback 按自己的能力解析上限。
- 48zd：配置文件可声明 Streamable HTTP server 与 `${VAR}` 认证头；stdio 继续工作；HTTP MCP 走同一 Provider/Registry/approval/deferred/preferred 链路；同名工具保留确定性优先者并告警。

## 范围与非目标

### 范围

- `network.proxy.url` 与 `network.proxy.sites` 的配置、registry、环境变量展开、站点匹配、HTTP/HTTPS/SOCKS5 client 选择。
- provider（OpenAI chat/completions、Responses、Anthropic）以及 `web_fetch`、`web_search` 的显式 Transport 接线和未配置回退。
- `providers.<id>.models.<model>.capabilities` 的解析、provider 级默认、启发式兼容回退、模型 catalog/manifest/text 展示。
- MCP server 的 transport/url/headers/启动与 call 超时、Streamable HTTP JSON-RPC、同名冲突诊断、CLI 与 ACP 入口。
- 单元测试、测试 HTTP proxy、测试 MCP HTTP server、真实 provider 与主机代理验证的实施计划。

### 非目标

- 不改变未配置全局代理时的模型/搜索环境代理语义；不强制所有出网点改成直连或改用系统代理。
- 不把代理配置用于 MCP；MCP HTTP 是否走代理留给后续独立需求，避免两个 issue 的站点策略互相覆盖。
- 不支持代理 PAC、自动发现、按请求 header 选代理、TLS MITM 证书配置或代理健康检查。
- 不以模型目录联网探测能力；不自动覆盖用户已经明确写下的 provider/model 字段；不引入 `compact_at`、缓存 TTL、OpenRouter 粘性头等额外能力字段。
- 不在第一版实现旧 MCP SSE transport；不改变 stdio wire protocol；不让 ACP 非 stdio 入口绕过权限或 secrets 规则。
- 不把同名工具自动改名：模型仍看到真实注册名，冲突只保留先注册者并给出诊断。

## 已确认事实与规范

### gtr6：代理现状

| 现状 | 代码位置 |
|---|---|
| `ParseFetchProxy` 接受空值或 `http`/`https`/`socks5` URL，启动期由 wiring 校验坏值 | `pkg/agentcore/tools/native/fetch.go:71-95`；`internal/wiring/runtime.go:259-262` |
| 无代理的 `web_fetch` 将 `Transport.Proxy` 置空，并在真实 dial 的 `Control` 中检查连接 IP；有代理时改为 DNS `checkHost` 后通过 `http.ProxyURL` | `pkg/agentcore/tools/native/fetch.go:98-128,148-167` |
| `web_fetch` 刻意忽略环境代理，以便私网检查看到真实目标；代理模式只在请求前和每次 redirect 本地解析目标 | `pkg/agentcore/tools/native/fetch.go:63-70,132-143` |
| provider 的非流式 client 没有自定义 Transport，因此使用 `http.DefaultTransport` 的 `ProxyFromEnvironment`；流式 client clone default transport，只改 response header timeout | `pkg/agentcore/llm/claude.go:103-121`；`pkg/agentcore/llm/openai.go:58-80`；`pkg/agentcore/llm/openai_stream.go:18-25` |
| Responses provider 复用 OpenAI client；其请求体有 `stream` 字段，流式路径使用相同 provider client | `pkg/agentcore/llm/responses.go:21-43,83-126`；`pkg/agentcore/llm/openai_stream.go:32-58` |
| `web_search` 使用未设置 Transport 的 `http.Client`，因此也隐式跟随环境代理 | `pkg/agentcore/tools/native/search.go:21-37,80-94` |
| 代理配置目前只有 `tools.web_fetch.proxy`；registry 将它标为 sensitive，但没有全局 network key | `internal/wiring/capabilities.go:220-226`；`internal/config/settings_registry.go:152-154` |

**兼容结论：**全局代理未配置时，`web_fetch` 仍忽略环境代理并直连，模型请求和 `web_search` 仍由 Go 默认 transport 读取环境代理。只有全局 URL 配置后，三类客户端才切换到显式选择器，并停止读取环境代理，避免同一进程出现两套生效规则。旧 `tools.web_fetch.proxy` 在全局 URL 为空时继续作为仅 `web_fetch` 的兼容 fallback；全局 URL 一旦存在则优先全局配置。

### 8pvi：模型能力现状与启发式清单

| 现状 | 代码位置 |
|---|---|
| context window 先取模型级/provider 级配置，否则按 `gpt-4.1`、`gpt-4o`、`gpt-5`、`deepseek`、`claude` 子串匹配，未知默认 128k | `internal/config/providers.go:569-580`；`internal/config/context_window.go:5-33` |
| Anthropic 以模型名片段判断 budget thinking：`claude-3`、`-4-5`、`-4-1`、`-4-0`、若干 `opus/sonnet` 版本 | `pkg/agentcore/llm/claude_thinking.go:33-46` |
| Claude thinking 的 adaptive/budget 请求形状由上述判断决定；budget 值和 `max_tokens` 的相互限制也在同文件 | `pkg/agentcore/llm/claude_thinking.go:48-78` |
| OpenAI-compatible 的 gateway compat 会按 base URL host 和 model 子串匹配；DeepSeek 子串启用每回合 reasoning/cache usage 规则，command-code host 设置无 tool choice 与 64k output cap | `pkg/agentcore/llm/compat.go:9-77`；`pkg/agentcore/llm/compat_deepseek.go:5-23`；`pkg/agentcore/llm/compat_commandcode.go:3-18` |
| max output 的显式模型/provider 配置已经存在；未配置时由 protocol default/compat default 决定，Claude 默认 32000，Responses 使用 compat 上限 | `internal/config/providers.go:582-591`；`internal/config/maxoutput.go:10-34`；`pkg/agentcore/llm/claude.go:19-33`；`pkg/agentcore/llm/responses.go:138-146` |
| Claude prompt cache 只有 provider 级 `prompt_cache`，默认关闭；OpenAI usage 已读取标准/DeepSeek cache hit，但没有统一的模型能力声明 | `internal/config/providers.go:80-82,186-213`；`pkg/agentcore/llm/claude.go:72-90`；`pkg/agentcore/llm/openai.go:247-278` |
| 模型列表是离线 projection，当前输出 context/thinking；manifest 当前输出主/compact/fallback 的 context/thinking，不泄露 endpoint 或 secret | `internal/config/model_catalog.go:11-42,146-159`；`internal/cli/models.go:14-19,64-87`；`internal/wiring/manifest.go:29-59,327-333` |

启发式不是全部删除：它们组成未声明能力的默认层，并在 catalog 中标为 `heuristic`；显式字段只覆盖对应字段，不会让旧配置突然失效。

### 48zd：MCP 与入口现状

| 现状 | 代码位置 |
|---|---|
| 包注释提到 stdio/SSE，但 `ServerConfig` 只有 command/args/env，`Client` 只有 exec.Cmd、stdin/stdout scanner 和 JSON-RPC line reader，没有 `net/http` client | `pkg/agentcore/tools/mcp/client.go:1-39,55-74,87-145` |
| MCP provider 启动、initialize、tools/list 共用一个 30 秒 startup timeout；单次 tools/call 使用 `mcp.call_timeout_sec` 默认 120 秒 | `pkg/agentcore/tools/mcp/provider.go:29-47,69-106`；`internal/config/mcp.go:102-105` |
| 注册工具按配置顺序 first-wins；重复名字目前只 `slog.Warn` 并丢弃后者，未形成可查询冲突列表 | `pkg/agentcore/tools/mcp/provider.go:116-129` |
| YAML MCP reader 只要求 command，且只读取 env map；headers/url/transport 尚不存在 | `internal/config/mcp.go:11-59` |
| ACP 的 `mcpSettings` 只接受 stdio，HTTP 入口直接返回 `only stdio transport is supported` | `internal/acpserver/policy.go:467-488` |
| runtime 在同一个 registry 注册 native 与 MCP；preferred MCP 以 provider 注册成功为准，deferred 只影响可见性，不是权限 | `internal/wiring/capabilities.go:45-79`；`docs/design/2026-10-17-preferred-tools-design.md:29-32,121-136` |
| go.mod 没有 MCP client/HTTP/SSE SDK；当前唯一相关外部 SDK 是 ACP SDK，MCP JSON-RPC 是项目自有实现 | `go.mod:5-18`；`pkg/agentcore/tools/mcp/client.go:1-3` |

**依赖结论：**现有依赖不提供可直接复用的 MCP HTTP client。建议第一版继续复用项目的 JSON-RPC 请求/响应模型，在 `pkg/agentcore/tools/mcp` 用标准库 `net/http` 实现 Streamable HTTP；先不新增第三方传输依赖。实现前用 `go doc`/依赖源码复核 ACP 的 HTTP inline 类型字段，但不把 ACP SDK 当成 MCP transport client。

## 总体方案

三件事分别落在三个边界：

```text
network.proxy.url + sites
    -> explicit proxy selector
       -> llm clients / web_fetch / web_search
       -> no global url: preserve each current default

providers.<id>.models.<model>.capabilities
    -> resolved ModelCapabilities
       -> provider request shape + per-model limits
       -> models catalog + manifest (effective values/source)

mcp.servers[] {command | url, transport, headers}
    -> stdio Client or StreamableHTTP Client
       -> common Provider -> Registry(first wins + diagnostics)
       -> visibility / approval / preferred semantics unchanged
```

全局代理与 MCP HTTP 不耦合：前者解决 suga 自己的三类公共出网，后者解决 MCP server 的接入协议。三件事都不把密钥或认证值写入观测数据。

## 架构

| 组件 | 改动与 owner |
|---|---|
| `internal/config/settings_registry.go` 与 network reader | 注册 `network.proxy.url/sites`，做 scheme、host、site 形状和 env expansion 校验；不创建 transport。 |
| `pkg/agentcore/tools/native` 与 `pkg/agentcore/llm` | 共享只读的 proxy selector；fetch 保留目标检查，provider/search 使用显式 client；未配置分支不改变现状。 |
| `internal/config/providers.go` 与 model catalog | 读取 provider/model capabilities，产生每个 `ModelSpec` 的 resolved facts；旧字段和启发式只在缺省时生效。 |
| `pkg/agentcore/llm` | 将 resolved thinking/cache/stream/max output 接到请求 body 与 dispatch；不把 gateway 名称写进通用能力结构。 |
| `pkg/agentcore/tools/mcp` | 新增 HTTP client 与 transport-specific `ServerConfig`，复用 `toolClient`、JSON-RPC tool adapter、call timeout 和 output cap。 |
| `internal/wiring`、`internal/acpserver` | 读取/装配 HTTP server、传入冲突 diagnostics；ACP 接受 HTTP，approval/deferred/preferred 仍由既有层负责。 |
| `internal/cli`、manifest/model types | 展示非敏感的有效能力和运行时冲突；standalone manifest 不启动 MCP、不暴露 URL/header。 |

### 共享边界

- proxy selector 只接收已解析的 URL 与站点规则，不读取环境；兼容行为由调用方在“全局 URL 为空”时显式选择。
- `ResolvedModelCapabilities` 是配置层的纯数据；provider 只消费它，不再次按名字猜能力。未声明时的 heuristic source 由配置层标记。
- stdio/HTTP client 只通过 `toolClient` 暴露 start/list/call/stop；Provider、Registry、Visibility、approval 不知道传输细节。
- diagnostics 是非敏感的 `{server, tool, kept_by, reason}`，不携带 endpoint、args、env 或 header value。

## 关键流程

### 全局代理请求

1. startup 读取 `network.proxy`；无 URL 时分别保留 fetch、LLM、search 的现有 client 构造。
2. 有 URL 时校验 scheme/host，构造显式 selector；对每个请求按 URL host/port 命中 sites，命中走 proxy，未命中直连。
3. web_fetch 在 proxy request 前和 redirect 后执行本地 `checkHost`，然后才交给 selector；allow_private 仍是唯一例外。
4. provider、search、fetch 共用 selector 规则，但各自保留请求 timeout、响应解析和输出治理。

### 模型能力解析与请求

1. `ReadProviders` 读取 provider/model capabilities 并校验；`ResolveModel` 生成每个模型的独立 effective facts。
2. runtime 为 main、compact、fallback 各建立自己的 request limit；fallback 切换时复制 request 并套用目标模型的 max output/stream/thinking。
3. provider 根据 capability 选择 thinking body、cache breakpoint 和 Generate/Stream；`stream_required` 不允许静默退回非流式。
4. catalog、`suga models` 与 manifest 使用同一 resolved projection，离线展示 source，不重复实现推断。

### MCP HTTP 生命周期

1. config/ACP 将 stdio 或 HTTP server 规范化为 `ServerSettings`；HTTP header 在本地 config 路径展开环境引用。
2. provider 为每个 server 建立独立 startup context，HTTP client 执行 initialize，再执行 tools/list；失败只关闭该 server 并返回有界诊断。
3. tools/call 使用独立 call timeout；Streamable HTTP 的 session id 随请求保存，响应 JSON/SSE 都转为同一 JSON-RPC result。
4. wiring 按现有顺序注册工具；first-wins 保持稳定，重复项写 log/diagnostics。注册成功的 HTTP MCP 与 stdio MCP 完全相同地进入 preferred/deferred/approval 链路。


## A. gtr6 全局代理

### 配置与语义

配置项必须进入 v0.28 引入并由 `suga config set/get/list` 使用的 `internal/config/settings_registry.go`（不是在 wiring 中另造隐式键）：`network.proxy.url` 标记 sensitive，`network.proxy.sites` 走同一 registry reader/candidate validation；模型 capabilities 与 MCP startup timeout 也按相同 registry 机制注册。

推荐使用两个最小配置叶子：
```yaml
network:
  proxy:
    # Prefer an environment reference; credentials stay in $SUGA_HOME/.env.
    url: "${SUGA_PROXY_URL}"
    # Only these hosts use the proxy; use "*" to opt every public request in.
    sites: ["api.openai.com", "api.anthropic.com", "serpapi.com"]
```

| 配置项 | 语义 | 为什么必要 |
|---|---|---|
| `network.proxy.url` | `http://`、`https://` 或 `socks5://` 地址；允许 `${VAR}`，认证 userinfo/secret 只放 `.env` | 地址是三类客户端共享的唯一代理目标；环境引用避免把认证写入 YAML/命令行 |
| `network.proxy.sites` | 代理 allowlist：精确 host、子域后缀（写 `.example.com`）、可选 `host:port` 与 `*`；未匹配直连 | 站点名单必须表达“哪些请求主动走代理”，而不是让用户猜环境 `NO_PROXY` 的反向语义；按站点 opt-in 默认更安全、也更接近未配置时的直连行为 |

名单推荐“名单走代理，其他直连”，而不是 `no_proxy` 风格。它减少误把内部/本地服务送进代理的风险，且新增配置只影响用户列出的模型/search/fetch 站点；确实需要全局代理时显式写 `*`。空名单等同于没有匹配项，不是“代理全部”。匹配使用请求 URL 的 host/port，不使用 DNS 解析结果；不支持任意路径通配。`socks5` 使用 `x/net/proxy` 拨号器，HTTP/HTTPS 使用 `http.Transport.Proxy`；URL 中的认证只从环境展开后的 URL 进入 transport，不写日志。

全局 URL 配置后，显式 selector 对每个请求决定 proxy 或 direct，且 transport 不再读取 `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`。全局 URL 未配置时保留上面的兼容结论。配置解析失败、host/scheme 缺失或名单项非法在启动期失败，不静默直连。

### web_fetch 的安全规则

- `tools.web_fetch.allow_private` 继续是唯一的显式私网例外；默认仍拒绝 loopback、private、link-local、multicast、unspecified、CGNAT 等地址。
- 直连时保留现有 dial-time IP 检查；走代理时在发送前对目标 host 做本地 DNS 解析，所有结果都必须通过 `publicIP`，IP literal 直接检查，解析失败或空结果 fail-closed；每次 HTTP(S) redirect 重新检查。
- 代理 CONNECT/转发后，suga 看不到代理最终解析的真实 IP，因此这不是对不可信代理的完备 SSRF 边界：本地检查防住本机 DNS 得到的私网目标，但不能证明代理端不会把同一名字解析到私网。文档和错误信息必须明确代理是受信配置；不能因为“请求已到 proxy”就跳过 `checkHost`。若未来要求对不可信 proxy 也 fail-closed，需要独立设计代理侧解析/允许列表，不能在本任务偷换语义。
- 继续保留 scheme、redirect 次数、20 秒/5 MB/输出上限；代理不改变这些限制。`allow_private=true` 的既有含义不变。

### 观测与兼容

`network.proxy.url` 标为 sensitive；`.env` 的代理凭据只作为 transport 输入。manifest 只可显示 `configured`、匹配规则数量和“environment credentials hidden”，不显示 URL、用户名、密码或完整站点规则；trace 不记录代理选择，避免把访问目标和网络拓扑扩散。`tools.web_fetch.proxy` 只有在全局 URL 未设置时作为 legacy fallback，并继续只影响 web_fetch；文档标为 deprecated，不新增第二个 override 语义。

### 回滚与非目标

删除全局 proxy reader/selector 后，删除 `network.proxy` 配置并恢复旧 fetch fallback 即可；模型/search 会回到默认 transport 环境代理。不会迁移用户数据。PAC、代理自动探测、MCP 代理和证书注入不在本期。

### 验证

- 单测：URL/scheme/认证展开校验；精确 host、子域、端口、`*`、不匹配直连；未配置分支分别证明 fetch 忽略环境代理而 model/search 保持 `ProxyFromEnvironment`；配置后证明三者只走显式 selector；web_fetch 私网 literal、DNS 私网、解析失败、redirect 私网仍拒绝。
- 主机/真实：用 `http://127.0.0.1:10801` 作为可用主机代理跑一次 provider 请求、`web_fetch` 和 `web_search`（测试站点/真实 endpoint 需按站点名单配置），记录代理端收到的请求；再跑一个不在名单的直连用例。至少一次真实 provider 带工具调用的对话必须使用该代理验证。
- 安全限制：不能用“代理返回成功”替代私网拒绝断言；代理后的本地 DNS 检查必须仍有测试覆盖。

## B. 8pvi 按模型声明能力

### 能力形状

在已有 `providers.<id>.models.<model>` 下增加一个可选 `capabilities` map；provider 级 `providers.<id>.capabilities` 作为默认，模型级字段覆盖。只增加真正影响请求形状或预算的字段：

```yaml
providers:
  gateway:
    capabilities:
      context_window: 128000
      max_output_tokens: 32000
      thinking: adaptive       # budget | adaptive | none
      prompt_cache: false
      stream: true              # provider can consume streamed responses
      stream_required: false    # must send/use stream=true
    models:
      remote-responses:
        capabilities:
          context_window: 1000000
          max_output_tokens: 64000
          thinking: adaptive
          prompt_cache: true
          stream: true
          stream_required: true
```

- `context_window` 与 `max_output_tokens` 使用现有正整数约束；`thinking` 是协议方式，不是用户选择的档位，档位仍由现有 `thinking`/`--thinking` 解析，`none` 强制不发送 thinking，`budget` 使用 `budget_tokens`，`adaptive` 使用 adaptive/effort 形状。
- `prompt_cache` 是 provider/model 是否可以安全接收 cache breakpoint 或 cache control；未声明不发送。第一版不声明 TTL，避免把 provider-specific 生命周期做成通用字段。
- `stream` 表示支持流式；`stream_required` 表示请求必须走流式路径，覆盖 v0.31 因 Responses 代理只接受 `stream=true` 的修复场景。`stream_required=true` 隐含 `stream=true`，配置 `stream=false` 与它同时出现报启动错误。
- 为了少定制，不把 gateway 的 tool_choice、reasoning summary、encrypted reasoning 各自再做字段；这些仍由现有 compat 作为未声明时的 provider/gateway fallback，后续若证明需要再单独设计。

### 解析优先级与回退

每个 `ModelSpec` 解析出独立的 `ResolvedModelCapabilities`：模型 capabilities > provider capabilities > 现有明确配置字段 > 现有启发式/协议默认。能力字段按字段独立回退，不因只声明 window 而清空 thinking 或 stream。现有 `providers.<id>.context_window`、`max_output_tokens`、`thinking`、`prompt_cache` 继续可用，作为兼容短写法映射到 resolved capabilities；旧配置无需迁移。

若能力未声明：context window 保留 `ContextWindowForModel` 的启发式；Claude thinking 保留 `claudeBudgetMode`；compat 仍按 host/model 选择 DeepSeek、command-code 等行为；max output 保留模型/provider 配置与 Claude/Responses protocol default；stream 保留当前 provider 调用路径。显式 `none`/`budget`/`adaptive` 优先于名称判断。每次 fallback 尝试都以它自己的 `ModelSpec` 重新解析 capabilities，并为该尝试复制/覆盖 `GenerateRequest.MaxTokens`，不能沿用主模型已解析的上限。

### 展示、缓存与安全

`BuildModelCatalog`/`suga models --json` 增加有效 capabilities（以及 `declared`/`heuristic`/`protocol-default` 的来源）；文本只显示窗口、max output、thinking mode、cache、stream requirement 等短字段。`manifest` 的 main/compact/fallback 同样显示这些非敏感事实，保留已有 `context_window`/`thinking` 字段以兼容消费者。`suga models` 继续离线，不建立 provider、不请求网络；manifest 继续不暴露 base URL、API key、headers 或 proxy。

能力结果参与已有模型切换/segment window 计算，但不进入 stable prompt 的 secret；配置变更在新 runtime 生效。`prompt_cache` 只是允许 provider 请求 cache breakpoint，不保证服务端真的命中，usage 仍以 provider 返回值为准。

### 回滚与非目标

保留旧字段和 fallback 读取意味着可独立回滚：删除 capabilities 解析和展示后，旧 provider/model 配置仍按原路径运行。已声明的新字段在旧二进制中会被忽略（模型仍可用但回到旧推断）。不新增远程能力探测、不做自动修正用户的 thinking、不修改工具 schema。

### 验证

- 单测：模型级覆盖 provider 级；每字段独立 fallback；budget/adaptive/none 的 Claude request body；`stream_required` 强制 Responses `stream=true` 且 `stream=false` 配置报错；cache 开关；fallback A/B 各自 max output；旧配置与未声明模型保持原 body。用现有 `internal/config`、`pkg/agentcore/llm` 测试风格和 `t.Run` 覆盖。
- 离线展示：`suga models --json` 与 `suga manifest --json` 断言主、compact、fallback 的有效能力和来源，且没有 key/base_url/header。
- 真实 provider：至少选一个真实 OpenAI-compatible/Responses 或 Anthropic provider，运行带工具调用的流式对话；再用一个只接受 `stream=true` 的 Responses 代理确认 `stream_required`；无法提供真实模型时交付报告标记 `UNVERIFIED_REAL_MODEL`。

## C. 48zd MCP Streamable HTTP

### 配置与 transport

stdio 与 HTTP 使用同一 server list，按字段判断：

```yaml
mcp:
  startup_timeout_sec: 30
  call_timeout_sec: 120
  servers:
    - id: local
      command: local-mcp
      args: [--stdio]
    - id: remote
      url: "https://mcp.example.test/mcp"
      transport: streamable_http
      headers:
        Authorization: "Bearer ${MCP_TOKEN}"
```

| 配置项 | 语义 | 为什么必要 |
|---|---|---|
| `mcp.servers[].url` | Streamable HTTP endpoint；与 command 互斥 | 远端 server 没有本地子进程，必须有地址 |
| `mcp.servers[].transport` | `stdio` 或 `streamable_http`；url 默认 HTTP，command 默认 stdio | 显式 transport 防止从字段形状猜错，且为错误配置给出稳定信息 |
| `mcp.servers[].headers` | 请求头 map，值展开 `${VAR}`；认证值只在 `$SUGA_HOME/.env`/进程环境 | Bearer/API key 等不能写入仓库或 CLI 参数；map 能覆盖不同 HTTP MCP 的认证约定 |
| `mcp.startup_timeout_sec` | initialize + tools/list 的总发现超时，默认 30 秒 | 现有 30 秒是代码常量；独立配置能防止慢的 HTTP discovery 卡住所有 runtime，同时保留有界行为 |
| `mcp.call_timeout_sec` | 一次 tools/call 的 context 上限，现有默认 120 秒继续适用 | HTTP 请求也必须有明确上限，不能因远端连接让 turn 无限等待 |

headers 只对 config YAML 做 env expansion，空名/非法 URL/同时提供 command 与 url 启动失败；日志、manifest、trace、tool description 不回显值。HTTP client 复用 `http.Client` 的 context deadline、响应大小上限和现有结果治理；Streamable HTTP 响应接受 `application/json` 或 `text/event-stream`，保存 server 返回的 `Mcp-Session-Id`，后续 JSON-RPC 请求带回；在支持关闭的 server 上用 DELETE 结束 session。实现前核对 MCP 规范版本和 ACP SDK 的实际字段。

旧 SSE：当前版本没有可工作的 SSE client，只有包注释提到 SSE，因此没有需要保持的旧行为。第一版只支持 Streamable HTTP；配置 `transport: sse` 返回“legacy SSE not supported”，不静默当 stdio。若真实 provider/用户仍需要旧 SSE，再以独立设计增加 endpoint event + message POST 的兼容 adapter，避免把两套生命周期混进第一版。

### Provider、冲突与入口

HTTP client 实现与 stdio client 共同满足 `toolClient`（Start/ListTools/AsTool/Stop），Provider 不知道传输类型。`internal/wiring/capabilities.go` 继续按配置顺序启动 provider 并注册 native/MCP tools；同名仍 first-wins，以注册顺序作为确定性规则。保留现有 `slog.Warn`，并扩充诊断结构记录 `tool`、丢弃 server、保留 owner：运行时 `:tools`/manifest 可查询冲突摘要，独立 `suga manifest` 不启动 MCP，明确显示“未发现运行时冲突”。不提供自动重命名，避免模型在不同 session 看到不稳定名字。

ACP `session/new`/`session/load` 接受 SDK 的 stdio 与 HTTP MCP server；`mcpSettings` 不再只检查 Stdio，而是将 URL、transport、headers 映射为同一个 `ServerSettings`。ACP 客户端显式传入的 headers 原样进入该 session 的 HTTP client，但 suga 不从 ACP payload 中解析本机 `.env`；`.env` 展开只属于 suga 配置文件路径，避免远程 ACP 客户端借协议读取宿主环境。HTTP MCP 工具仍被 `tools.IsMCP` 标记，ACP permission hook 仍按 MCP 走 permission request，read_only 不因 HTTP 而放宽。

v0.29 preferred 工具语义不变：`kind: mcp` 的 present/注册成功条件仍是 initialize + tools/list 成功；HTTP server 成功注册后与 stdio server 一样可被 preferred pin 到 direct，`tools.deferred` 只是加载时机，不是 allowlist。HTTP 连接失败只让该 provider 失败/诊断，不自动放行或删除其他 server 的工具。

### 安全、兼容与回滚

- 默认只允许配置中明确的 HTTPS/HTTP endpoint；不允许 URL 中携带未脱敏的 inline secret，认证通过 env reference/header 进入内存。
- 每个 server 独立 startup context；一个 HTTP server 的慢 discovery 不延长其他 server 的超时。call context 取消/超时必须释放 response body，Stop 必须关闭 HTTP session 状态。
- MCP HTTP 不自动继承 gtr6 的站点代理；如未来需要，明确把 MCP 纳入全局 selector，而不是隐式读取环境变量。
- 旧配置只有 command/args/env，解析和行为不变；旧二进制看到 url/headers 会忽略未知字段而无法连接远端，回滚只删除远端配置即可。删除 HTTP client/reader 后 stdio、registry、preferred、approval 全部保留。

### 验证

- 单测：配置 shape/互斥/`${VAR}` 展开/secret 不出诊断；测试 HTTP server 完成 initialize、tools/list、tools/call、session header、JSON 与 SSE response、超时/取消、关闭；stdio 回归；两个 server 和 native tool 同名时 first-wins、警告/诊断包含 server/tool/owner 且不含 header/token；preferred 与 deferred/approval 矩阵不变。
- ACP：fixture 传 stdio 与 HTTP 两种 server，HTTP 非 stdio 不再被拒；读写权限和 RequestPermission 事件仍按 MCP；旧无 URL 的 malformed input 仍返回 invalid params。
- 真实验证：若环境没有现成 HTTP MCP server，计划使用仓库内 `httptest.Server` 模拟一个最小 Streamable HTTP server，并另建可选的真实兼容 server job；至少一次真实 MCP HTTP initialize/list/tool call 才可宣布完成。真实 ACP HTTP 验证使用 `scripts/acpprobe/acp_probe.py` 或等价最小客户端；没有可用 server 时标记 `UNVERIFIED_REAL_MCP_HTTP`。

## 决策

| ID | 决策 | 理由 |
|---|---|---|
| D1 | 代理站点采用“名单走代理、其余直连” | opt-in、最少惊讶，减少误代理内部地址；`*` 仍能表达全局代理 |
| D2 | 全局代理未配置保留三条路径各自现状；配置后使用显式 selector，不读环境代理 | 兼容旧安装，同时消除同一配置下的隐式优先级冲突 |
| D3 | web_fetch 走代理仍先本地解析并拒绝私网，接受受信代理的 DNS 限制 | 保留现有 SSRF 检查；不能假装 client 能观察代理端的最终 IP |
| D4 | capabilities 逐字段覆盖，旧字段/启发式作 fallback | 旧配置无迁移成本；用户只需声明真正不同的能力 |
| D5 | stream 与 stream_required 分开 | “可以流”与“非流就拒绝”是两个不同协议约束，Responses 代理 bug 需要后者 |
| D6 | MCP 第一版只做 Streamable HTTP，不做 legacy SSE | 当前 SSE 只有注释没有实现；避免两套 endpoint/session 生命周期，按真实需求再扩展 |
| D7 | MCP HTTP 复用自有 JSON-RPC/Provider/Registry，不新增 transport SDK | go.mod 没有可复用 MCP HTTP client，标准库足够完成 HTTP/SSE response 解析 |
| D8 | 冲突 first-wins + log + runtime diagnostics，不自动改名 | 保持已有确定性和模型工具名稳定；诊断可排障但不污染工具协议 |
| D9 | ACP 放开 HTTP，但 `.env` 展开只发生在本地 config reader | ACP 请求可显式带 headers；不让远程客户端借 ACP 读取宿主秘密 |

## 待确认事项

| ID | 问题 | 建议结论 |
|---|---|---|
| Q1 | 站点名单为空时是否代理全部？ | 不代理任何站点；全代理显式写 `*`，避免一次配置改变所有出网。 |
| Q2 | 是否把 MCP HTTP 也纳入全局 proxy？ | 本批不纳入；MCP 是独立接入协议，另立 issue 才改变其网络边界。 |
| Q3 | legacy SSE 是否与 Streamable HTTP 同批？ | 不同批；当前版本没有可回退的 SSE 实现，配置错误应明确失败。 |
| Q4 | 能力声明是否包含 cache TTL/compact_at？ | 不包含；先交付影响请求形状的五类字段，TTL/压缩阈值等 provider-specific 需求另行设计。 |
| Q5 | standalone manifest 是否启动 MCP 以发现冲突？ | 不启动；保持现有 manifest 离线/不启动 MCP 语义，运行时 diagnostics 才报告实际冲突。 |

## 结论与人工计划 Gate

本设计为 `suga-gtr6`、`suga-8pvi`、`suga-48zd` 的合并 Draft 0.1，待用户审批。批准后按对应实施计划拆成独立任务；批准前不新增 `network.proxy`、model `capabilities` 或 MCP HTTP reader，不改变 ACP、provider、web_fetch 和 Registry 行为。
