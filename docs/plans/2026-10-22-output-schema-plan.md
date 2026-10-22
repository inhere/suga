<!-- template_id: plan; template_version: 1.2.0 -->
# 输出契约（OutputSchema）一期 实施计划

> 状态：Draft 0.1 / 待人工计划批准

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-22 | Claude（监督会话） | 初稿：按已批准设计 0.2 拆为 6 个任务、4 个波次 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

思考模式：严谨型（rigorous），独立于设计阶段。核心目标：`suga run --output-schema` 与 `sugasdk.Prompt.OutputSchema` 让最终答复要么符合调用方的 JSON Schema，要么以明确失败结束；不传契约时一切逐字节不变。scope freeze：只实施设计 0.2 第 7.1 节“一期”；review budget：SR1409 低暴露度（未投产新功能、无生产数据、可回滚、无出机器动作）封顶一轮合并轴评审；停止条件：完成 Gate 全部满足，或出现 `Semantic Amendment` / `Ownership Conflict`。

## 目标与完成定义

目标：实现设计 `docs/design/2026-10-22-output-schema-design.md`（Approved 0.2）的一期。

完成定义（全部满足）：

1. `suga run -p … --output-schema <file|json|->`：合格时退出码 0、stdout 为答复原文；一次纠正后仍不合格退出码 **6**；非法 schema 在创建会话前以退出码 2 拒绝；`suga chat` 拒绝该参数。
2. `--output-format stream-json` 的 result 行带 `output_schema`（`passed/rounds/source/errors/value` 或 `checked:false,reason`）；不传契约时 result 行与现状逐字节一致。
3. `sugasdk.Prompt.OutputSchema` 生效：通过时 `Result.OutputValue` 为提取出的原始 JSON 片段经 `json.Compact` 的文本；失败时 `errors.Is(err, sugasdk.ErrOutputContract)` 且 `Result.OutputContract.Passed=false`，`Result.State` 仍为 `COMPLETED`。
4. schema 只出现在本次模型请求的临时上下文（追加位置与 hook 上下文同层：历史之后、本轮消息之前），不写入会话记录；系统提示词与工具前缀不变；纠正消息只含校验错误。
5. trace 有 `output_schema` 事件（`round`、`action`、`passed`、`source`、`errors`≤5、`schema_sha256` 前 12 位、`schema_bytes`），不含 schema 全文；`suga inspect trace` 能显示。
6. 大整数保真：`{"id": 9007199254740993}` 原样出现在 `OutputValue` 与 stream-json `value` 中。
7. 真实模型验证完成（一轮合格 + 一轮故意不可满足 → 退出码 6），或在交付说明中如实写明未完成及原因。

## 范围、排除项与授权

- 范围：设计 0.2 第 7.1 节列出的改动（新包 `internal/personal/outschema`、共用执行、临时上下文、CLI 入口与退出码 6、stream-json 字段、SDK 入口、trace 事件、依赖 `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2`）。
- 排除：chat 支持、ACP `_meta`、provider 原生结构化输出、多轮纠正、替模型修 JSON、schema 持久化、子 agent（设计 6.4 / 7.3）。
- 授权：用户 2026-10-22 已批准设计 0.2（“批准输出契约设计 0.2”），该批准只授权规划。**实施需要用户批准本计划并给出执行请求。** 本地按任务提交是默认授权；push、tag、发版需另行授权。
- `host_or_non_offline_action=NOT_APPLICABLE`

（真实模型验证经用户已配置的 provider 发起只读模型调用，与以往批次的真实模型评测相同，不涉及 push、发版、部署或对外消息。）

## 输入与批准证据

- 设计：`docs/design/2026-10-22-output-schema-design.md`，Approved 0.2（提交 `2d73ffa5`，用户原话“批准输出契约设计 0.2”）。
- 评审：`docs/review/2026-10-22-output-schema-design-review.md`（PASS，F1–F3 已并入 0.2）。
- 参考实现形态：`internal/cli/verify.go`（`verifyAfterTurn`、`ErrVerifyFailed` 退出码 5）、`internal/cli/nudge.go`（`nudgeAfterTurn`）、`internal/personal/nudge.go:290`（`RecordNudge`）、`internal/output/streamjson.go:115`（`WithVerify`）、`internal/personal/context.go:401`（`hookContext` 的投影位置）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | JSON Schema 编译与校验 | 仓库内无 JSON Schema 实现（grep `jsonschema`/`gojsonschema` 无命中）；设计 3.1 比较了 santhosh-tekuri v6/v5、xeipuuv、自写子集 | 否 | 否：新包 `internal/personal/outschema`，内部使用已安装依赖 `santhosh-tekuri/jsonschema/v6`（唯一间接依赖 `golang.org/x/text` 已在 go.mod） | `MINIMAL_NEW_MODULE` | 无现成实现；自写子集不满足标准 schema | owner=outschema;lifecycle=suga；新增一个直接依赖，deny loader 禁外部资源 |
| CAP-02 | 本次请求专用、不落库的上下文 | `RunRequest.SystemContext`（落库）、`wiring.Options.HostContext`（段快照稳定上下文）、系统提示词（runner 原样发送） | 否（三者都会持久或破坏前缀缓存，设计 4.2） | 是：`contextBuilder` 已有 `hookContext` 的同层渲染点，新增 `RunRequest.EphemeralContext` + `contextBuilder.ephemeral` | `OWNER_EXTENSION` | none | owner=internal/personal;lifecycle=existing；为空时投影逐字节不变，由测试守护 |
| CAP-03 | 回合结束后的推回回合 | `verifyAfterTurn`、`nudgeAfterTurn` 用 `runner.Run(Injected: true)` 推回 | 是（同一机制） | 共用执行放 `internal/personal/contract.go`，供 CLI 与 SDK 共用 | `OWNER_EXTENSION` | none | owner=internal/personal;lifecycle=existing；与 verify/nudge 同构，回合上限有界 |
| CAP-04 | trace 事件 | `store.EventNudge` 与 `RecordNudge` | 是（同形状） | 新增事件类型常量与记录函数 | `OWNER_EXTENSION` | none | owner=internal/personal/store;lifecycle=existing；无迁移，事件表通用 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-01 | CAP-01 | `internal/personal/outschema`（纯函数：编译、提取、校验、报告、提示文本） | 仓库无 JSON 提取/校验代码；`internal/personal` 已 869+ 行级文件众多 | 无现成实现 | 放进 `internal/personal` 根包会把库依赖与纯函数混进 runner 包，且难以单测隔离 | 需要独立、无 store 依赖、可表驱动单测的纯函数层（设计 7.1） | 唯一 owner：本包；被 `internal/personal/contract.go` 与 CLI 报告层调用；生命周期同 suga | 若后续二期改用 provider 原生结构化输出，本包仍负责校验，不删除 |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| `--fake` 运行开关 | CAP-03 | 删除测试 PASS：端到端用 `internal/cli` 测试内的 httptest 假 provider 走真实装配路径即可（设计 7.2 第 4 条），不新增开关 |

## 前置检查与 fail-closed 条件

运行目录均为 Git root `inhere-tools/suga`（下文命令相对它）。

- 基线：分支 `main`，HEAD `2d73ffa5`，`git status --short` 为空（2026-10-22 规划时核对）。实施在 gofer worktree 分支进行，主目录不改。
- 依赖可离线获取：`go mod download github.com/santhosh-tekuri/jsonschema/v6@v6.0.2` 成功；失败则停止并汇报（不换库，换库是 `Semantic Amendment`）。
- **数字保真探针（T1 第一步）**：确认 v6 对 `json.Number`（或库自带的保数字解码）的校验结果正确（`multipleOf`、`maximum` 对大整数）。探针不成立时停止并汇报，不得退回 `float64`。
- fail-closed：
  - 不传契约时，`suga run` 的 stdout、stream-json 行、trace 与提示词投影必须逐字节不变（T2、T4 的回归测试）；任何差异即失败。
  - 现有退出码 0/1/2/3/4/5/130 语义不得变化。
  - 需要改动设计 0.2 范围外的模块/接口（例如 `pkg/agentcore`、chat、ACP、provider 请求体），或改变设计决定（退出码、纠正次数、位置、不落库）→ `Semantic Amendment`，停止并请求人工 Gate。
  - 命中他人未提交改动 → `Ownership Conflict`，停止。

## 波次与依赖

| 波次 | 任务 | 依赖 |
|---|---|---|
| W1 | T1 纯函数包与依赖；T2 临时上下文 | 无（可并行，单 agent 时顺序做） |
| W2 | T3 共用执行与 trace 事件 | T1、T2 |
| W3 | T4 CLI 入口、退出码、结果行；T5 SDK 入口 | T3 |
| W4 | T6 文档、真实模型验证、收尾 | T4、T5 |

每个任务完成后单独提交（提交信息带任务号）。波次末运行 `GOWORK=off go test -count=1 ./internal/... ./pkg/...` 一次。

## 任务

### T1 依赖与纯函数包 `internal/personal/outschema`

- 文件: `go.mod`、`go.sum`；新增 `internal/personal/outschema/{contract.go,extract.go,report.go}` 与对应 `_test.go`
- 动作:
  1. 数字保真探针（见前置检查），结论写进汇报。
  2. `Compile(raw []byte) (*Contract, error)`：大小 ≤32 KiB；顶层必须是 object 或 bool；`DefaultDraft(Draft2020)`；`UseLoader` 换为只认 `urn:suga:output-schema` 的 deny loader；未知 `$schema` 的错误改写为“不支持的 draft”；schema 自身语法错误压成一行。错误类型 `*CompileError{Kind, Message}`（Kind: `size|toplevel|syntax|external_ref|unknown_draft|invalid_schema`），文案由本包拥有。
  3. `(*Contract).Check(answer string) Result`：按 whole → fenced（先 `json` 标记块再无标记块，均从后往前）→ substring（平衡括号，跳过字符串字面量，从后往前）→ `no_json` 提取；第一个通过者胜出，全不通过报最后候选的错误。`Result{Passed bool; Source string; Value json.RawMessage; Errors []Error{Path, Message}}`；`Value` 为原始片段 `json.Compact` 后的文本，绝不经过 `any` 重新编码。
  4. 错误渲染：`InstanceLocation` → `$`、`.name`、`[n]`；最多 8 条、每条 ≤200 字符、超出注明“还有 N 条”；`LocalizedString` 用 `message.NewPrinter(language.English)`。
  5. `PromptBlock(schema []byte) string`（临时上下文文本，设计 4.2 的首轮块）与 `CorrectionText(res Result) string`（只含错误与重答要求，不含 schema）。
- 验证: `GOWORK=off go test -count=1 ./internal/personal/outschema/`；表驱动覆盖设计 7.2 第 1 条全部用例 + `{"id": 9007199254740993}` 原样返回 + deny loader 拦外部 `$ref` + `$defs` 内部引用可用。
- 完成标准: 上述测试通过；`go.mod` 只新增这一个直接依赖。
- 依赖: 无

### T2 本次请求专用的临时上下文

- 文件: `internal/personal/runner.go`（`RunRequest`）、`internal/personal/context.go`（`contextBuilder`、`newContextBuilder`、`compose`），测试放 `internal/personal/context_ephemeral_test.go`
- 动作: `RunRequest` 新增 `EphemeralContext string`（注释：只进入本次模型请求，不写会话记录、不随 resume 继承）；`contextBuilder` 新增 `ephemeral` 字段，在 `compose` 中紧接 `hookContext` 之后以 `role=user` 追加；不调用 `transcript.AppendUser`；不影响 `items()`/`segmentCut`/估算以外的持久状态（token 估算需计入，保证窗口判断正确）。
- 验证: `GOWORK=off go test -count=1 -run 'Ephemeral' ./internal/personal/`：①为空时 compose 输出与改动前逐字节一致（对同一 fixture 比较）；②非空时出现在请求中且位于历史之后；③会话记录与 FTS 中查不到该文本；④同会话下一回合（不带）请求中不再出现。
- 完成标准: 四个用例通过；系统提示词与工具 schema 未改动（diff 中 `PromptBuilder` 相关代码零改动）。
- 依赖: 无

### T3 共用执行与 trace 事件

- 文件: 新增 `internal/personal/contract.go` 与 `contract_test.go`；`internal/personal/store/types.go`（`EventOutputSchema = "output_schema"`）；`internal/personal/trace.go`（渲染该事件，与 nudge 同形状）
- 动作:
  - `func (r *Runner) RunWithContract(ctx context.Context, req RunRequest, c *outschema.Contract, schema []byte, first RunResult, hooks ContractHooks) (RunResult, ContractReport, error)`：输入为已完成的首轮结果（调用方先跑 Run、后台任务、nudge、verify），按设计 4.5 守卫（未完成/WrapUp/Truncated/空答复 → `Checked=false` + `Reason`）；不合格时以 `Injected: true`、`EphemeralContext: PromptBlock(schema)`、`Prompt: CorrectionText(res)` 跑一次纠正回合，再终判。`ContractHooks{BeforeCorrection func()}` 供 CLI 收尾流式输出。
  - `ContractReport{Checked, Passed bool; Rounds int; Source, Reason string; Errors []outschema.Error; Value json.RawMessage}`。
  - `RecordContract(ctx, rec, sessionID string, round int, action string, res outschema.Result, schema []byte)`：事件 attrs 见完成定义第 5 条；每次判定一条（最多 2 条）。
- 验证: `GOWORK=off go test -count=1 -run 'Contract' ./internal/personal/`（mock provider：首轮不合格 → 纠正 → 合格；两轮都不合格；守卫四种情形；纠正回合请求中含临时上下文、会话记录中不含 schema）。
- 完成标准: 用例通过；trace 事件不含 schema 全文（断言）。
- 依赖: T1、T2

### T4 CLI 入口、退出码 6、stream-json 字段

- 文件: `internal/cli/run.go`（参数、流水线最后一步）、新增 `internal/cli/schema.go`（参数解析、报告 → stderr/stream-json、`ErrOutputContract`）、`internal/cli/errors.go`（退出码注释补 6）、`internal/output/streamjson.go`（`WithOutputSchema(line map[string]any, rep ContractView) map[string]any`）、新增 `internal/cli/run_schema_test.go`
- 动作: `--output-schema` 取值判定 `-`/内联/文件（设计 2.1）；在创建/加载会话前编译，错误退出码 2；`suga chat` 拒绝；首轮 `RunRequest.EphemeralContext = outschema.PromptBlock(schema)`；流水线 `Run → 后台任务 → nudge → verify → RunWithContract`；纠正前 `live.Finish()`；text 模式失败打印答复原文 + stderr 一行摘要 + 退出码 6；stream-json result 行加 `output_schema`。
- 验证: `GOWORK=off go test -count=1 -run 'OutputSchema|RunSchema' ./internal/cli/ ./internal/output/`，覆盖设计 7.2 第 2 条四个场景 + 非法 schema 退出码 2 且不建会话 + chat 拒绝 + 不传契约时 stream-json result 行逐字节不变（对照现有 fixture）。
- 完成标准: 用例通过；`suga run --help` 列出新参数；现有 `verify_test.go`、`nudge_test.go` 全部仍通过。
- 依赖: T3

### T5 SDK 入口

- 文件: `pkg/sugasdk/types.go`（`Prompt.OutputSchema json.RawMessage`、`Result.OutputValue json.RawMessage`、`Result.OutputContract *OutputContract`）、`pkg/sugasdk/engine.go`（`ErrOutputContract`）、`pkg/sugasdk/session.go`（`Send` 中编译、首轮临时上下文、调用 `RunWithContract`）、`pkg/sugasdk/e2e_test.go`（新增用例）
- 动作: 非法 schema 时 `Send` 在跑回合前返回错误（包装 `ErrInvalidArgument` 一类现有哨兵，若无则新增 `ErrInvalidOutputSchema`，在汇报中说明）；`OutputContract{Passed bool; Rounds int; Source, Reason string; Errors []ContractError{Path, Message}}` 定义在 sugasdk 内，不暴露 `internal/` 类型；`RequestID` 记录仍指向首回合。
- 验证: `GOWORK=off go test -count=1 -run 'OutputSchema' ./pkg/sugasdk/` 与 `go test -count=1 -run 'API' ./pkg/sugasdk/`（`api_test.go` 守护公开类型）。
- 完成标准: 通过与失败两条 e2e 用例通过；大整数保真断言通过；`api_test.go` 通过。
- 依赖: T3

### T6 文档、真实模型验证与收尾

- 文件: `README.md`（run 一节）、`docs/usage/` 中 run/SDK 的使用文档（存在则补）、`CHANGELOG.md`（`## [未发布]` 新增）、`internal/personal/trace.go` 若 T3 未覆盖 `inspect trace` 白名单则补
- 动作: 写清参数、提取顺序、退出码 6、`format` 不断言、schema 只当数据、不持久化；真实模型验证（隔离 `SUGA_HOME`，只读真实配置）：①带工具调用的一轮 + 合格契约 → 退出码 0、`rounds=1`；②不可满足的契约（`enum` 中不存在的值）→ 只纠正一次、退出码 6、trace 两条事件。
- 验证: 真实模型两条命令及输出贴进汇报；`GOWORK=off go vet ./... && GOOS=windows go vet ./...`；`gofmt -l`；`staticcheck ./...`（Linux 与 Windows 各一次，由监督的 suga-verify workflow 覆盖）。
- 完成标准: 文档与 CHANGELOG 已更新；真实模型结果已记录或如实写明未完成。
- 依赖: T4、T5

## 回滚与恢复

- 每个任务一个提交；回滚按任务 `git revert`。T1 的依赖随 T1 回滚。
- 不改存储结构、不加迁移；trace 新事件类型对旧版本只是未知事件，回滚后无需清理数据。
- 恢复点：基线 `2d73ffa5`；实施在独立 worktree 分支，合入前由监督审核，未合入则直接丢弃分支。

## 人工 Gate

1. 计划批准（本文件 0.1 → Approved）。
2. 当前执行请求（批准时一并给出即可）。
3. 外部动作 Gate：push、tag、发版不在本计划内，另行授权。

## 可追溯性

| 需求（设计 0.2） | 任务 | 验证 |
|---|---|---|
| 结论 1、2.1 CLI 入口与取值判定 | T4 | run_schema_test：内联/文件/stdin、非法 schema 退出码 2、chat 拒绝 |
| 结论 2、3.1–3.2 校验库与沙箱 | T1 | outschema 表驱动：deny loader、未知 draft、大小、顶层类型 |
| 结论 3、3.3 提取与 OutputValue 原文 | T1 | 提取顺序用例、大整数保真 |
| 结论 4、4.3 只纠正一次、新回合 | T3 | contract_test：两轮上限、injected 回合 |
| 结论 5、4.1 流水线最后一步 | T4 | 与 `--verify` 同开的用例，回合有界 |
| 结论 6、4.2 临时上下文、不碰系统提示词 | T2、T3 | Ephemeral 四用例；纠正回合请求含临时上下文、记录不含 schema |
| 结论 7、4.4 退出码 6 与 stream-json 字段 | T4 | 失败场景退出码 6、result 行字段、无契约逐字节不变 |
| 结论 8、2.2 SDK 形态 | T5 | e2e 通过/失败、api_test |
| 4.5 守卫与 `checked:false` | T3 | 守卫四种情形 |
| 5. trace 事件、不存全文 | T3、T6 | 事件 attrs 断言、inspect trace 显示 |
| 7.2 第 5 条真实模型 | T6 | 两条真实模型命令输出 |

## 完成 Gate 与剩余工作

- T1–T6 全部完成并提交；各任务验证命令输出贴在汇报中。
- 监督方：容器 race、`make smoke-chat`（本计划不改 chat，冒烟用于回归）、suga-verify workflow 全部通过后合并。
- 反向验证至少两项：去掉纠正回合 → “首轮不合格即失败”的用例失败；去掉 deny loader → 外部 `$ref` 用例失败。
- 剩余（不在本计划）：ACP、原生结构化输出、chat 提示（设计 7.3）。
