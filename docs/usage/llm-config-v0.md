# LLM 配置

模型由「模型引用」和「provider 定义」两部分描述：`model: <provider>/<model>` 选择模型，`providers.<id>` 定义协议、地址与凭据。旧的 `llm` 配置段已移除。

## 配置结构

```yaml
model: deepseek/deepseek-chat

providers:
  deepseek:                      # 内置预设，只需覆盖想改的字段
    api_key: "${DEEPSEEK_API_KEY}"
  bailian:                       # 自定义 provider 必须写 type
    type: openai                 # openai | anthropic
    base_url: "${BAILIAN_BASE_URL}"
    api_key: "${BAILIAN_API_KEY}"
    headers:
      x-extra: "value"
    models:
      glm-5:
        context_window: 128000
        max_output_tokens: 64000

compact_model: bailian/glm-4-flash
fallback_models:
  - anthropic/claude-sonnet-4-6
```

## 字段

- `model`：主模型，`<provider>/<model>`。
- `providers.<id>.type`：协议，`openai`（`/chat/completions`）、`openai-responses`（OpenAI Responses API，`/responses`）或 `anthropic`（`/v1/messages`）。内置预设可省略；自定义 provider 必填。也可写在单个模型上（见下）。
- `openai-responses`：每次发送完整历史（`store: false`，不用 `previous_response_id`），系统提示放在 `instructions`；思考档位作为 `reasoning.effort`（`off` 发送 `none`），推理项随工具调用保存并在之后的请求里原样回传；会话 id 作为 `prompt_cache_key`。
- `providers.<id>.compat`：网关适配名列表（可选）。适配按 base_url 域名与模型 id 自动匹配：`openai`（api.openai.com）、`commandcode`（api.commandcode.ai：推理摘要与加密推理、不发 tool_choice、输出上限 64000）、`deepseek`（模型 id 含 deepseek 或 api.deepseek.com：思考模式下每个助手回合回传推理文本、缓存命中读 `prompt_cache_hit_tokens`）；自动匹配不到时在这里写名字追加。适配实现在 `pkg/agentcore/llm/compat_*.go`，新增网关加一个文件。
- `providers.<id>.base_url`、`api_key`：地址与密钥，值支持 `${VAR}`。
- `providers.<id>.headers`：附加的 HTTP 请求头，值支持 `${VAR}`。
- `providers.<id>.context_window`：该 provider 下所有模型的默认上下文窗口（如网关统一为 1M 时写一次即可）。
- `providers.<id>.models.<model>.context_window`：该模型的上下文窗口。取值顺序：模型级 > provider 级 > 按模型名推断（claude 200k、gpt-4.1 1M、gpt-5 400k、gpt-4o/deepseek 128k，未知为 128k）。
- `providers.<id>.max_output_tokens` 与 `providers.<id>.models.<model>.max_output_tokens`：模型回答的输出 token 上限，取值顺序为模型级 > provider 级；未配置时由协议决定。Anthropic/Claude 默认发送 32000，OpenAI chat/completions 与 Responses 不发送该字段而使用服务端默认。`--max-output-tokens` > `SUGA_MAX_OUTPUT_TOKENS` > 模型级 > provider 级，可覆盖本次运行。
- `providers.<id>.models.<model>.model_id`：实际发给 provider 的模型 id；不设时发送 `<model>` 本身。用它给很长的上游 id 起一个短名：引用、trace、统计、价格与上下文窗口都按短名，只有请求里用 `model_id`。用完整的上游 id 引用也会对应到这个短名。
- `providers.<id>.models.<model>.type`、`base_url`：该模型自己的协议与地址，覆盖 provider 的。同一个网关（command-code、opencode 等）常把 Claude 模型放在 Anthropic 协议的端点、其余模型放在 OpenAI 兼容端点，这时在 Claude 模型上写 `type: anthropic` 和对应的 `base_url` 即可，不必再拆一个 provider；`api_key`、`headers` 等仍沿用 provider 的。
- `providers.<id>.thinking_style` 与 `providers.<id>.models.<model>.thinking_style`：声明思考请求形状而不按模型名推断，取值 `budget`（`thinking: {type: enabled, budget_tokens}`）、`adaptive`（`thinking: {type: adaptive}` 与 `output_config.effort`）与 `none`（不发送思考参数，即使档位已设置）。优先级：模型级 > provider 级；都不设时保持原来的模型名推断（Claude 4.5 及更早用 budget，更新的用 adaptive）。只影响 `anthropic` 类型的请求形状；`suga inspect manifest` 显示主模型生效的值。
- `providers.<id>.stream_required` 与 `providers.<id>.models.<model>.stream_required`：布尔值，声明只接受流式请求的网关；为 true 时该 provider（或该模型）的所有请求都带 `stream: true`，包括压缩摘要等内部请求（`stream_required: true` 自身隐含支持流式）。模型级显式写 `false` 可关掉 provider 级的默认；不声明时保持原来的调用路径。`suga models` 与 `suga inspect manifest` 显示生效值。
- `providers.<id>.thinking` 与 `providers.<id>.models.<model>.thinking`：思考档位，取值 `off`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`auto`（与 omp 相同）。优先级：命令行 `--thinking`（只作用于主模型）> 模型级 > provider 级；都不设或 `auto` 时不发送，用 provider 的默认。`openai` 类型作为请求字段 `reasoning_effort` 原样发送，各 provider 支持的取值不同（如 command-code 为 `off|low|medium|high|xhigh|max`，OpenAI 为 `none|minimal|low|medium|high`），不支持的取值由 provider 报错。`anthropic` 类型走 extended thinking：Claude 4.6 及以后的模型发送 `thinking: {type: adaptive, display: summarized}` 与 `output_config.effort`（`minimal` 按 `low`），4.5 及更早的模型发送 `thinking: {type: enabled, budget_tokens}`（minimal 1024 … max 31000）并带 interleaved-thinking beta 头；设置档位且请求未指定时 `max_tokens` 为 32000；`off`、`auto` 与不设都不发送思考参数（部分新模型默认就会思考，也不接受关闭）。带签名的 thinking 块随工具调用保存，并在之后的请求里原样回传。签名绑定请求前缀，所以主模型为 `anthropic` 类型时不做历史折叠（`context.fold_every` 不生效）。`suga inspect manifest` 显示主模型生效的档位。
- 简单匹配：provider 写了 `models` 而引用的模型不在其中（既不是某个键，也不是某个 `model_id`）时，按名字的一部分查找已配置的模型（不区分大小写，查键与 `model_id`；写了 `provider/` 前缀只查该 provider，否则查所有列了模型的 provider；有完全相同的只取它）。唯一匹配就用它，如 `--model haiku` 选中 `command-code/claude-haiku-5-5`；多个匹配时报错并列出候选；没有匹配时在 stderr 警告一次并照原样发送，多半是模型名写错。没有 `models` 的 provider 不做匹配也不检查。未配置 `model` 时，裸名同样按此在所有 provider 中查找。`model`、`--model`、`SUGA_MODEL`、`compact_model`、`fallback_models` 都适用。
- `compact_model`：自动压缩与 `:compact` 使用的模型，引用规则同 `model`；裸模型名使用主模型的 provider；不设则用当前回合的模型。
- `fallback_models`：主模型失败后按顺序尝试的模型列表，引用规则同 `compact_model`。只在另一模型还有机会时换（瞬时故障，或 401/403/404 这类点名本 provider 的拒绝），见 `retry` 一段。
- `retry`：模型请求遇到临时故障时的重试，对所有 provider 生效。临时故障指网络错误、HTTP 408/429/5xx，以及读响应流时连接断开。`providers.<id>.retry` 可按 provider 覆盖全局 retry 的字段；`max_attempts` 是一次请求的总次数，含第一次，默认 6，设为 1 即关闭重试。等待从 `base_delay`（默认 1s）起每次翻倍，最多到 `max_delay`（默认 60s），带 ±20% 抖动。服务端给了 `Retry-After` 时按它等，同样不超过 `max_delay`。等待档位由同一 provider 的请求共用，连续 `reset_after`（默认 10m）没有失败就回到 `base_delay`。这样故障持续时退避逐渐拉长，过后偶发的抖动又能很快重试。时长写成 `500ms`、`30s`、`2m`，或直接写秒数。某个 provider 需要在更短的次数或退避后放弃（或相反）时写 `providers.<id>.retry`，字段同上，只写要改的，未写的沿用全局，例如某个免费网关 `max_attempts: 2`。
  - `Retry-After`（秒数或 HTTP 日期）与 `Retry-After-Ms`（毫秒）会影响等待；后者优先。响应头 `X-Should-Retry: false` 表示本次错误不再重试。provider 在错误消息中给出 `try again in 1.5s`、`try again in 20ms` 或 `try again in 2 seconds` 时也会作为等待提示，超过 `max_delay` 会截断；实际等待仍记在 trace 的 `retry_wait_ms`。
  - `context_length_exceeded`（OpenAI code）与 Anthropic 的 `prompt is too long` 归为 `provider_rejected`，不重试，并提示“上下文超长，可 :compact 或开新会话”。它们不会换同一 provider 的其它模型；若配置了其它 provider，仍可按 `ShouldFallback` 规则尝试有更大上下文窗口的 provider。

  - 流式输出中途断开时整条消息重新请求。如果断开前已输出部分文本，前端会先收到一个 `retry` 事件（`restart: true`）：chat 显示提示行，stream-json 输出 `retry` 行，ACP 发一段 thought，然后从头重新输出这条消息。其它 4xx 与流内的 API 错误不重试；重试用尽之后才考虑按 `fallback_models` 换模型，而且只在另一模型真有机会时换：瞬时故障（网络错误、408/429/5xx、响应头超时、流中断）与 401/403/404（凭据、模型名属本 provider）会换，400/422（请求本身的问题）与无法使用的响应（`provider_bad_response`）直接把错误返回给调用方。
  - 每次模型调用的重试次数、等待时长与最后原因记在 trace 的 model 事件上（`retries`、`retry_wait_ms`、`retry_reason`），换/不换下一个模型的原因记在同一条事件的 `fallback` 上（如 `next, provider_transient`、`stop, provider_rejected (HTTP 400) would repeat on the next model`），`suga inspect trace --analyze` 汇总；provider 返回的 request id 也写入 model 事件的 `request_id`，失败文本末尾带 `request-id: ...`，便于向 provider 报障。

- `timeouts`：一次模型请求的超时，对所有 provider 生效。`request`（默认 `120s`）限制**非流式**请求的整体耗时（连接、TLS、响应头与读正文）；`response_header`（默认 `60s`）限制**流式**请求等待响应头的时间，流式正文本身不设超时（长回答可以流很久，中止仍可用 Esc/Ctrl-C）。两者都可写 `off` 或 `0` 表示不限制；`providers.<id>.timeouts.request` / `.response_header` 覆盖该 provider 的对应值。时长写成 `90s`、`2m`，或直接写秒数。
  - 超时后按上面的 `retry` 重试：一次请求最多 `max_attempts` 次尝试，每次都可能等满超时，所以一次卡住的时长约为“超时 × 尝试次数 + 退避”。默认值下（响应头 60s、`reset_after` 10m）连续超时会正常退避（1s→2s→4s→8s→16s），最坏约 6×60s + 31s ≈ 6.5 分钟。注意两个值的相对大小：若某个超时大于 `retry.reset_after`，退避档位会在每次尝试前被重置（表现为每次重试只等约 `base_delay`）——这就是为什么默认响应头超时取 60s 而 `reset_after` 取 10m。

### 内置预设

| id | type | 默认 base_url | 密钥环境变量 |
|---|---|---|---|
| `openai` | openai | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `anthropic` | anthropic | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` |
| `deepseek` | openai | `https://api.deepseek.com/v1` | `DEEPSEEK_API_KEY` |

同名的 `providers.<id>` 逐字段覆盖预设，空字段不覆盖；`headers` 与 `models` 整体替换。

## 模型引用解析

1. 只按第一个 `/` 切分，且前缀必须是已定义的 provider（预设或 `providers` 下的 id）。
2. 前缀不是已定义 provider 时，整串都是模型名，使用默认 provider。例如 `meta/llama-3` 是模型名，不是 provider `meta`。
3. 默认 provider：`--model` / `SUGA_MODEL` 与 `compact_model` / `fallback_models` 中的裸模型名，分别使用配置 `model` 的 provider 与主模型的 provider。

主模型优先级：`--model` > `SUGA_MODEL` > 配置 `model`。`--model gpt-5` 这类裸名使用配置 `model` 的 provider。provider 未定义、没有模型名或没有配置模型时启动报错。

## base_url 拼接

- `openai` 类型：在 `base_url` 后拼 `/chat/completions`。
- `anthropic` 类型：`base_url` 后拼 `/v1/messages`；`base_url` 已以 `/v1` 结尾时拼 `/messages`。Claude 使用流式（SSE）请求。

## headers

- 值中的 `{{session_id}}` 替换为当前 suga 会话 ID，同一会话固定不变；没有会话 ID 时（不经过会话的调用）这类头不发送。
- 默认带 `User-Agent: suga`，可在 `headers` 中覆盖。

## 出网代理（network.proxy）

模型请求、`web_fetch` 和 `web_search` 可以统一走一个全局代理。不配置时保持各自现状：`web_fetch` 忽略环境代理并检查目标 IP，模型请求与 `web_search` 跟随 `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`。

```yaml
network:
  proxy:
    url: "${SUGA_PROXY_URL}"      # http://、https:// 或 socks5://，凭据放 .env
    sites: ["api.openai.com", ".anthropic.com", "serpapi.com"]
```

- `network.proxy.url` 是唯一代理地址，可用 `${VAR}` 引用环境变量或 `$SUGA_HOME/.env`；引用解析不出值、scheme/host 非法或含路径时启动失败，不会静默直连。该键标记为 sensitive，`suga config get/list` 只显示掩码。
- `network.proxy.sites` 是代理白名单：命中才走代理，未命中直连。写法为精确 `host`、子域后缀 `.example.com`（含该域本身及其子域）、`host:port` 或 `*`（全部）。空名单等于没有命中项，不是“代理全部”。
- 配置了 url 后，这三类客户端都不再读取环境代理变量，避免同一进程里两套规则同时生效。
- `tools.web_fetch.proxy` 是仅作用于 `web_fetch` 的旧配置，只在 `network.proxy.url` 为空时生效（已废弃，建议迁移到全局配置）。
- `web_fetch` 的私网限制不因代理放松：走代理前仍按目标 host 做本地 DNS 检查（IP 直写也检查），每次跳转重新检查；代理自身是否为可信配置由用户负责。

## 环境变量

- `${VAR}` 在 `api_key`、`base_url`、`headers` 中展开，来源为进程环境或 `$SUGA_HOME/.env`（默认 `~/.suga/.env`）。
- 配置文件为 `$SUGA_HOME/config.yaml`，`SUGA_CONFIG` 可指定其它文件；不读取当前工作目录的配置与 `.env`。
- 真实密钥放在 `.env` 或环境变量，不写进命令行参数。

## 示例：OpenCode Go

OpenCode Go 要求每个对话带固定的 `x-opencode-session`。它有 OpenAI 兼容和 Anthropic 兼容两个端点（各模型走哪个端点见其官方文档）：

```yaml
model: opencode-go/deepseek-v4-flash

providers:
  opencode-go:                    # /chat/completions
    type: openai
    base_url: "https://opencode.ai/zen/go/v1"
    api_key: "${OPENCODE_API_KEY}"   # 写在 $SUGA_HOME/.env
    headers:
      x-opencode-session: "{{session_id}}"
  opencode-go-claude:             # 实际请求 https://opencode.ai/zen/go/v1/messages
    type: anthropic
    base_url: "https://opencode.ai/zen/go"
    api_key: "${OPENCODE_API_KEY}"
    headers:
      x-opencode-session: "{{session_id}}"
```

之后可用 `--model opencode-go-claude/<model>` 临时切换到 Anthropic 端点。

## 示例：短模型名（model_id）

```yaml
model: command-code/deepseek-v4.1-flash

providers:
  command-code:
    type: openai
    base_url: "https://api.commandcode.ai/provider/v1"
    api_key: "${COMMAND_CODE_API_KEY}"
    context_window: 1000000                # 本 provider 所有模型的默认窗口
    thinking: high                         # 默认思考档位（--thinking 可临时覆盖）
    thinking_style: adaptive               # 可选的请求形状：budget | adaptive | none
    stream_required: true                  # 可选的：该 provider 只接受流式请求
    models:
      deepseek-v4.1-flash:                 # suga 里用的短名
        model_id: deepseek/deepseek-v4.1-flash   # 请求里发送的 id
```

同一网关的 Claude 模型走 Anthropic 协议时，在模型上覆盖 `type` 与 `base_url`（地址以网关文档为准）：

```yaml
    models:
      deepseek-v4.1-flash:
        model_id: deepseek/deepseek-v4.1-flash
      sonnet:
        type: anthropic                    # 覆盖 provider 的 openai
        base_url: "https://<网关的 Anthropic 端点>"
        model_id: claude-sonnet-4-5
```

`--model command-code/deepseek-v4.1-flash` 或（`model` 已指向 command-code 时）`--model deepseek-v4.1-flash` 都会请求 `deepseek/deepseek-v4.1-flash`；`--model command-code/deepseek/deepseek-v4.1-flash` 同样可用。`suga inspect manifest` 在主模型一行显示实际发送的 id。

## 从旧配置迁移

旧的 `llm` 配置段、provider 选择环境变量与命令行选项已移除（见下表）；仍存在时启动报错并提示改用新写法。trace/stats 中的 provider 名现在是配置里的 id（原 `claude` 变为 `anthropic` 或自定义 id）。

旧写法（仅作对照，已不再支持）：

    llm:
      api_type: openai
      provider_name: bailian
      model: glm-5
      api_key: "${BAILIAN_API_KEY}"
      base_url: "${BAILIAN_BASE_URL}"
      compact_model: glm-4-flash
      context_window: 128000

新写法：

```yaml
model: bailian/glm-5
compact_model: bailian/glm-4-flash
providers:
  bailian:
    type: openai
    api_key: "${BAILIAN_API_KEY}"
    base_url: "${BAILIAN_BASE_URL}"
    models:
      glm-5:
        context_window: 128000
        max_output_tokens: 64000
```

对照：

| 旧 | 新 |
|---|---|
| `llm.api_type` | `providers.<id>.type` |
| `llm.provider_name` + `llm.model` | `model: <id>/<model>` |
| `llm.api_key` / `llm.base_url` / `llm.headers` | `providers.<id>.api_key` / `base_url` / `headers` |
| `llm.compact_model` | 顶层 `compact_model: <provider>/<model>`（可用裸名沿用主 provider） |
| `llm.context_window` | `providers.<id>.models.<model>.context_window` |
| `SUGA_PROVIDER` / `--provider` | `SUGA_MODEL=<provider>/<model>` / `--model <provider>/<model>` |
