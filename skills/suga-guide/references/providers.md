# provider 与模型配置细节

模型由两部分描述：「模型引用」`<provider>/<model>` 与「provider 定义」`providers.<id>`。旧的 `llm:` 配置段已移除（存在即启动报错）。键的完整列表见 `references/config.md`，本文讲清取值、解析规则与常见网关写法。

## 配置结构

```yaml
model: deepseek/deepseek-chat

providers:
  deepseek:                      # 内置预设，只需覆盖想改的字段
    api_key: "${DEEPSEEK_API_KEY}"
  bailian:                       # 自定义 provider 必须写 type
    type: openai                 # openai | openai-responses | anthropic
    base_url: "${BAILIAN_BASE_URL}"
    api_key: "${BAILIAN_API_KEY}"
    headers:
      x-extra: "value"
    models:
      glm-5:
        context_window: 128000

compact_model: bailian/glm-4-flash
fallback_models:
  - anthropic/claude-sonnet-4-6
```

## 字段

- `providers.<id>.type`：协议。`openai`（`/chat/completions`）、`openai-responses`（OpenAI Responses API，`/responses`）、`anthropic`（`/v1/messages`）。内置预设可省略；自定义 provider 必填；也可写在单个模型上。
- `openai-responses`：每次发送完整历史（`store: false`，不用 `previous_response_id`），系统提示放 `instructions`；思考档位作为 `reasoning.effort`（`off` 发 `none`）；推理项随工具调用保存并原样回传；会话 id 作为 `prompt_cache_key`。
- `providers.<id>.compat`：网关适配名列表（可选）。适配按 base_url 域名与模型 id 自动匹配（如 `openai`、`commandcode`、`deepseek`），自动匹配不到时在这里写名字追加。
- `providers.<id>.base_url` / `api_key`：地址与密钥，值支持 `${VAR}`。
- `providers.<id>.headers`：附加 HTTP 头，值支持 `${VAR}`；`{{session_id}}` 替换为当前 suga 会话 ID（没有会话 ID 时不发送这类头）；默认带 `User-Agent: suga`，可在 `headers` 里覆盖。
- `providers.<id>.context_window`：该 provider 下所有模型的默认上下文窗口（网关统一为 1M 时写一次即可）。
- `providers.<id>.models.<model>.context_window`：该模型的窗口。取值顺序：模型级 > provider 级 > 按模型名推断（claude 200k、gpt-4.1 1M、gpt-5 400k、gpt-4o/deepseek 128k，未知 128k）。
- `providers.<id>.max_output_tokens` 与 `providers.<id>.models.<model>.max_output_tokens`：模型回答的输出 token 上限，模型级覆盖 provider 级。命令行 `--max-output-tokens` > 环境变量 `SUGA_MAX_OUTPUT_TOKENS` > 模型级 > provider 级；未配置时 Anthropic 默认 32000，OpenAI chat/completions 与 Responses 不发送上限字段。
- `providers.<id>.models.<model>.model_id`：实际发给 provider 的模型 id；不设时发送键名本身。用它给很长的上游 id 起短名：引用、trace、统计、价格与窗口都按短名，只有请求里用 `model_id`。用完整上游 id 引用也会对应到这个短名。
- `providers.<id>.models.<model>.type` / `base_url`：该模型自己的协议与地址，覆盖 provider 的；`api_key`、`headers` 仍沿用 provider 的。同一网关常把 Claude 模型放在 Anthropic 端点、其余模型放在 OpenAI 兼容端点，这时只在 Claude 模型上写 `type: anthropic` 和对应 `base_url`，不必拆第二个 provider。
- `providers.<id>.models.<model>.price`：`{input, output, cache_read, cache_write}`，每百万 token 的美元价格；provider 不返回成本时 `suga inspect stats` 与 `usage_update` 用它算成本。
- `providers.<id>.prompt_cache`（仅 `anthropic` 类型，默认关）：加 `cache_control` 断点（system 变成块数组）；部分网关不接受。
- `compact_model`：自动压缩与 `:compact` 使用的模型，引用规则同 `model`；裸模型名用主模型的 provider；不设则用当前回合的模型。
- `fallback_models`：主模型失败后按顺序尝试的模型列表。

## 思考档位（thinking）

- 取值 `off`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`auto`（与 omp 相同）。
- 优先级：命令行 `--thinking`（只作用于主模型）> `providers.<id>.models.<model>.thinking` > `providers.<id>.thinking`；都不设或 `auto` 时不发送参数，用 provider 默认。
- `openai` 类型作为请求字段 `reasoning_effort` 原样发送，各 provider 支持的取值不同（如 command-code 为 `off|low|medium|high|xhigh|max`，OpenAI 为 `none|minimal|low|medium|high`），不支持的取值由 provider 报错。
- `anthropic` 类型走 extended thinking：Claude 4.6 及以后发送 `thinking: {type: adaptive, display: summarized}` 与 `output_config.effort`（`minimal` 按 `low`）；4.5 及更早发送 `thinking: {type: enabled, budget_tokens}`（minimal 1024 … max 31000）并带 interleaved-thinking beta 头。设置档位且请求未指定时 `max_tokens` 为 32000。`off`、`auto` 与不设都不发送思考参数（部分新模型默认就会思考，也不接受关闭）。
- 带签名的 thinking 块随工具调用保存并原样回传；签名绑定请求前缀，所以主模型为 `anthropic` 类型时不做历史折叠（`context.fold_every` 不生效）。
- `suga inspect manifest` 显示主模型生效的档位。

## 模型引用解析

1. 只按第一个 `/` 切分，且前缀必须是已定义的 provider（内置预设或 `providers:` 下的 id）。
2. 前缀不是已定义 provider 时，整串都是模型名，使用默认 provider。例如 `meta/llama-3` 是模型名，不是 provider `meta`。
3. 默认 provider：`--model` / `SUGA_MODEL` 与 `compact_model` / `fallback_models` 里的裸模型名，分别使用配置 `model` 的 provider 与主模型的 provider。
4. 简单匹配：provider 写了 `models` 而引用的模型不在其中时，按名字的一部分查找已配置的模型（不区分大小写，查键与 `model_id`；写了 `provider/` 前缀只查该 provider，否则查所有列了模型的 provider，有完全相同的只取它）。唯一匹配就用它（如 `--model haiku` 选中 `command-code/claude-haiku-5-5`）；多个匹配报错并列出候选；没有匹配时在 stderr 警告一次并照原样发送（多半是模型名写错）。没有 `models` 的 provider 不做匹配也不检查。

主模型优先级：`--model` > `SUGA_MODEL` > 配置 `model`。provider 未定义、没有模型名或没有配置模型时启动报错。

## base_url 拼接

- `openai` 类型：在 `base_url` 后拼 `/chat/completions`。
- `anthropic` 类型：`base_url` 后拼 `/v1/messages`；`base_url` 已以 `/v1` 结尾时拼 `/messages`。Claude 使用流式（SSE）请求。

所以配置里只写服务的根地址（如 `https://dashscope.aliyuncs.com/compatible-mode/v1`），不要自己补 `/chat/completions`。

## 内置预设

| id | type | 默认 base_url | 密钥环境变量 |
|---|---|---|---|
| `openai` | openai | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `anthropic` | anthropic | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` |
| `deepseek` | openai | `https://api.deepseek.com/v1` | `DEEPSEEK_API_KEY` |

同名的 `providers.<id>` 逐字段覆盖预设，空字段不覆盖；`headers` 与 `models` 整体替换。换用内置预设只需改 `model` 并提供对应环境变量。

## 示例：OpenCode Go（带会话头的网关）

OpenCode Go 要求每个对话带固定的 `x-opencode-session`，并提供 OpenAI 兼容与 Anthropic 兼容两个端点：

```yaml
model: opencode-go/deepseek-v4-flash

providers:
  opencode-go:                    # 请求 .../zen/go/v1/chat/completions
    type: openai
    base_url: "https://opencode.ai/zen/go/v1"
    api_key: "${OPENCODE_API_KEY}"   # 写在 $SUGA_HOME/.env
    headers:
      x-opencode-session: "{{session_id}}"
  opencode-go-claude:             # 请求 .../zen/go/v1/messages
    type: anthropic
    base_url: "https://opencode.ai/zen/go"
    api_key: "${OPENCODE_API_KEY}"
    headers:
      x-opencode-session: "{{session_id}}"
```

之后可用 `--model opencode-go-claude/<model>` 临时切到 Anthropic 端点。

## 示例：Codex 渠道（会话头决定上游 prompt 缓存）

走 ChatGPT/Codex 后端的网关（如 [AxonHub](https://github.com/looplj/axonhub) 的 `codex` 渠道）用**入站请求头**判断一次请求属于哪个上游会话：`Session_id` → `Session-Id` → `X-Codex-Turn-Metadata` → 服务端派生的 session，全都没有就**给每个请求生成一个随机 uuid**（`llm/transformer/openai/codex/outbound.go`，写进上游的 `Session-Id`/`X-Codex-Window-Id`）。上游按会话做 prompt 缓存，因此缺会话头的客户端每个请求都是“新会话”，`cached_tokens` 恒为 0：同一个 key、同一个渠道，Codex CLI 有命中而 suga 没有，就是这个原因。

suga 不伪造客户端身份，所以要在 `providers.<id>.headers` 里补上会话头（`{{session_id}}` 解析成当前会话 id，一个会话内稳定）：

```yaml
providers:
  axonhub-codex:
    type: openai-responses              # /responses
    base_url: "http://<网关>/v1"
    api_key: "${AXONHUB_API_KEY}"
    context_window: 500000
    headers:
      Session-Id: "{{session_id}}"
      Thread-Id: "{{session_id}}"
      Originator: "codex_exec"          # 可选：让网关按 Codex 客户端处理
```

自检效果：`suga inspect trace <session> --json` 里每次模型调用的 `cache_read_tokens` 应接近 `input_tokens`（首条冷启动除外）；不补头时多数请求为 0。

注意：`providers.<id>.prompt_cache` 只对 `anthropic` 类型生效（`cache_control` 断点）；OpenAI/Responses 的缓存由上游自动管理，客户端能做的是**让会话与前缀稳定**——suga 自己发的 `prompt_cache_key`（会话 id）和上面的会话头都属于这一类。网关不认识这两个头时通常直接忽略，无副作用。

## 示例：短模型名与同一网关的两种协议

```yaml
model: command-code/deepseek-v4.1-flash

providers:
  command-code:
    type: openai
    base_url: "https://api.commandcode.ai/provider/v1"
    api_key: "${COMMAND_CODE_API_KEY}"
    context_window: 1000000                  # 本 provider 所有模型的默认窗口
    thinking: high                           # 默认思考档位（--thinking 可临时覆盖）
    models:
      deepseek-v4.1-flash:                   # suga 里用的短名
        model_id: deepseek/deepseek-v4.1-flash  # 请求里发送的 id
      sonnet:
        type: anthropic                      # 覆盖 provider 的 openai
        base_url: "https://<网关的 Anthropic 端点>"
        model_id: claude-sonnet-4-5
```

`--model command-code/deepseek-v4.1-flash`（或 `model` 已指向 command-code 时写 `--model deepseek-v4.1-flash`）都会请求 `deepseek/deepseek-v4.1-flash`；`suga inspect manifest` 在主模型一行显示实际发送的 id。

## 超时、重试与常见排查

- 重试（所有 provider）：`retry.max_attempts`（默认 6，含首次；`1` 关闭）、`retry.base_delay`（`1s`）、`retry.max_delay`（`60s`）、`retry.reset_after`（`10m`）。触发条件：网络错误、HTTP 408/429/5xx、响应流断开。流式输出中途断开时整条消息重新请求（chat 显示 `↻` 提示行，stream-json 输出 `retry` 行）。
- 4xx（除 408/429）与流内 API 错误不重试；重试用尽后才按 `fallback_models` 换模型。
- 排查顺序建议：`.env` 里的变量名是否与配置里的 `${VAR}` 完全一致 → `base_url` 的路径段（是否多写/漏写 `/v1`）→ 模型 id（上游名字不同就用 `model_id`）→ 代理/网络是否可达 → 上下文窗口是否写对。
- `suga inspect manifest`（或 `--json`）能看到生效的 provider、模型、实际发送的 id、上下文窗口、思考档位；`suga inspect stats` 看用量与成本；`suga inspect trace <session>` 看每次模型调用的缓存、重试与失败原因。

## 从旧配置迁移

旧的 `llm:` 段、provider 选择环境变量与命令行选项已移除，仍存在时启动报错。trace/stats 里的 provider 名现在是配置里的 id（原 `claude` 变为 `anthropic` 或自定义 id）。

| 旧 | 新 |
|---|---|
| `llm.api_type` | `providers.<id>.type` |
| `llm.provider_name` + `llm.model` | `model: <id>/<model>` |
| `llm.api_key` / `llm.base_url` / `llm.headers` | `providers.<id>.api_key` / `base_url` / `headers` |
| `llm.compact_model` | 顶层 `compact_model: <provider>/<model>`（可用裸名沿用主 provider） |
| `llm.context_window` | `providers.<id>.models.<model>.context_window` |
| `SUGA_PROVIDER` / `--provider` | `SUGA_MODEL=<provider>/<model>` / `--model <provider>/<model>` |

对照示例（旧 → 新）：

```yaml
# 旧（已不支持）
llm:
  api_type: openai
  provider_name: bailian
  model: glm-5
  api_key: "${BAILIAN_API_KEY}"
  base_url: "${BAILIAN_BASE_URL}"
  compact_model: glm-4-flash
  context_window: 128000
```

```yaml
# 新
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
```

仓库 `docs/usage/llm-config-v0.md` 是同一份内容的原始文档（本文按用户视角重写，键名与代码一致）。
