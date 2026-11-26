<!-- template_id: review; template_version: 1.2.0 -->
# 可信记忆后端开关 设计评审（Spec/Executability 轴，第 2 轮闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `8639724b`；subject `docs/design/2026-11-26-trusted-memory-backend-design.md`；document revision Draft 0.3（上轮 candidate `d5dc67ce` / Draft 0.2）
- 修订版本: Draft 0.3
- 评审者: 独立 reviewer（Spec/Executability 轴），未参与该设计的编写与修订
- 日期: 2026-11-26
- 排除项: 用户已选定的“7s4v Q2 选 A”（project 按工作区根目录列表登记）本身不评；方案 A、D5 的取舍不评；Standards/Governance 轴不在本报告；REPORT_ONLY，不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（review-contract、review 模板 1.2.0、CORE_BLOCKING 五类与 necessity proof）。
- 上轮报告：`docs/review/2026-11-26-trusted-memory-backend/spec-review.md`（TF1–TF5）。
- 差异：`git diff d5dc67ce..8639724b -- docs/design/2026-11-26-trusted-memory-backend-design.md`；未变章节沿用上轮结论。
- 代码（实际读取）：
  - `internal/config/settings_registry.go:29,104-272,440-530,670-720`（`ConfigTypeStringList`、`ParseConfigValue`、`validateConfigValue`/`isStringList`）
  - `internal/config/protected_snapshot.go:55-200`（`DigestProtected`、`protectedFileMode`、`protectedKeyDigests`、`protectedValueDigest`）
  - `internal/config/approval_rules.go:114-130`、`internal/wiring/rules.go:350-375`（`project_trust` 先例）
  - `internal/wiring/memory.go:1-120,140-150,180-440`（`MemoryConfig`、`loadMemoryConfig`、`MemoryBackendChoice`、`buildMemoryWithProjectKey`、`selectProjectBackend`、`gitRoot`、`projectPath`）
  - `internal/wiring/memory_runtime.go:150-170`、`internal/wiring/manifest.go:155-180,445-460,700-725`、`internal/wiring/embed.go:15-45`
  - `internal/wiring/origin_guard.go:55-120`、`internal/personal/observe.go:340-360`、`internal/config/origin_judge.go`（grep）
  - `pkg/agentcore/tools/native/memory.go:328-333`
  - `test/ptye2e/chat_origin_judge_test.go`（grep）、`test/ptye2e/fakellm_test.go:438-520`
  - gofer：`inhere-tools/gofer/internal/config/project_worktree.go:16`（`DefaultWorktreeDir = ".worktrees/{name}"`）
- 工作区实况：`ls` 确认 WP 根、`inhere-tools/gofer`、`inhere-tools/suga`、`inhere-tools/xhen` 各自带 `.gofer/tracker`，且后三者是嵌套在 WP 根 git 仓库里的独立仓库（各有 `.git`）。
- 方法：逐条核对 TF1 的四个具体点与 TF2–TF5 的修订；对新增 D6 与方案 3/4/6 检查可实现性、与代码的一致性、对核心验收与安全边界的影响；最后按用户真实工作区布局推演登记行为。

## 验收标准

- 核心验收：已登记仓库（或 user 作用域 true）的 get/search 结果不进 ExternalOrigin、不污染会话；未登记仓库（含 clone 的第三方仓库自带 `.gofer/tracker`）照旧按外部；默认行为不变；会话内 `config set` 拒绝；文件改动启动提示报出键名；非法值启动失败。
- 合同：CORE_BLOCKING 限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 且须 necessity proof；同类穷尽。
- 本轮核对结果：
  1. **TF1 闭合（已关闭）**：
     - `ConfigTypeStringList` 存在（`settings_registry.go:29`），且已有多个 Protected 列表键（`tools.read_roots`、`memory.taint_tools` 等，258-272 行），注册方式可直接沿用。`config set` 的字符串列表解析支持逗号分隔与 `[a, b]` 流式写法（504-520 行）。
     - 整根相等匹配：`selectProjectBackend` 先 `filepath.Abs(workspaceRoot)` 再 `gitRoot(ws)`（`memory.go:263-270`），`gitRoot` 按“当前目录或上层第一个含 `.git`（目录或 worktree 文件）”返回。列表项 `filepath.Clean` 后与之比较，尾部分隔符、Windows 下 `/` 与 `\` 混写都会被 Clean 统一。第三方 clone（在任何目录，自带 `.git`）的 gitRoot 是它自己，和已登记的根不相等，所以不被信任。嵌在已登记 WP 根里的 clone 也因自带 `.git` 而有独立的 gitRoot，所以不被信任。WP 根下无 `.git` 的子目录（如 `tmp/codeql-src-*` 自带 `.gofer/tracker` 的源码快照）落到 WP 根，但 gofer 后端 `Dir` 取的是 WP 根（`memory.go:293-296`），读的是 WP 根自己的 tracker，不会读快照目录里的条目。结论：TF1 的因果链被切断。
     - Windows 大小写：`strings.EqualFold` 只在 Windows 使用，与 `projectPath` 在 Windows 下小写化（`memory.go:417-423`）的既有约定一致。盘符大小写不同、cwd 大小写不同都能匹配。8.3 短名、`subst`、junction 或符号链接、`\\?\` 前缀这些写法都不相等，都会失败关闭（照旧按外部），不构成安全问题。非 Windows 平台区分大小写，在大小写不敏感的挂载（如容器里的 `/d`）上，大小写写错也只会失败关闭。
     - 受保护快照：`protectedKeyDigests` 只对配置中出现的、模式不含 `<` 的键做 `lookupMap`，值经 `protectedValueDigest` 规范 JSON 后哈希，注释写明“list order kept”。列表的增删改与改顺序都会变更 digest 并报出键名，`memory.taint_tools` 已是同类先例。设计“普通 YAML 写法”的限定与 `protectedFileMode`（顶层带点键、锚点、别名、多文档时降为文件级）一致。
     - 非法值启动失败：`loadMemoryConfig` 的错误经 `loadRuntimeSettings`（`manifest.go:712-714`）直接返回，chat/run 启动失败。`OpenMemoryStore`（memory 维护命令）与 `OpenEmbeddedMemory`（`embed.go:26`）同样失败，是一致的失败关闭。`filepath.IsAbs` 在 Windows 下拒绝 `/d/...`、`\work`、`C:foo`，在 Linux 下拒绝 `D:\...`。user 键写成字符串 `"true"` 会按“不是布尔”报错，行为唯一。
  2. **TF2 已关闭**：方案 6 与“安全”第 2 条都改为“子 agent 不注册 memory 工具，不受影响”，与 `subagent.go:30-34` 一致。同类再查：全文已无其它子 agent 表述。
  3. **TF3 已关闭**：D5 补了 `taint_tools`/`taint_on_spill` 例外（ExternalOrigin 仍不记），与 `taint.go:58-70`、`origin_guard.go:47-48` 一致；链路验证加了对应用例。
  4. **TF4 已关闭（附 TF8 小修）**：GR110 写明替换 `configStdinIsTerminal`，与 `internal/cli/config.go:184-197` 一致。假 exec 后端改为 Go helper 并在测试开始时 `go build`，可在 Linux 与 ConPTY 下同跑。观测点改为审批事件的 `origin_judge` 属性，字段存在（`observe.go:350-358`）。询问文本与 judge 回复还有两处细节，见 TF8。
  5. **TF5 已关闭**：两键在 `loadMemoryConfig` 读取并放进 `MemoryConfig`。`selectProjectBackend` 局部已有 `root`，可直接算出匹配结果写进 `MemoryBackendChoice`。`newMemoryRuntime` 已持有 `setup`（`memory_runtime.go:158-168`），可据 `setup.User/Project` 构造 `Trusted` 回调，不读全局 gkconfig。host-key 路径（`selectExplicitProjectBackend`）不做匹配，即不可信，符合“不适用”。可实现。
  6. **新增 CORE_BLOCKING / 矛盾**：未发现。D6 只收窄 Draft 0.2 的信任范围，所有不匹配路径都失败关闭。注册、分类、快照、`config set` 拒绝都复用既有机制，没有新增 Module、Interface 或 state。表述层面的小矛盾归入 TF7。
- SR1301：鉴权与资源归属（信任对象现按仓库根绑定）已核对，无发现；入参校验（绝对路径、列表类型、空项）已核对，无发现；注入防护的放宽范围已与设计所述一致；秘密处理不适用。

## 发现

### TF6 [MEDIUM] 登记粒度是“会话所在的 git 根”，设计没告诉用户嵌套仓库和 worktree 要逐个登记、worktree 中的 job 不生效

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据:
  - `internal/wiring/memory.go:371-385` `gitRoot`：取最近的 `.git`（目录或 worktree 文件）。所以在 WP 根下的嵌套仓库里运行时，gitRoot 是嵌套仓库，不是 WP 根。
  - 本工作区实况：WP 根、`inhere-tools/gofer`、`inhere-tools/suga`、`inhere-tools/xhen` 各有 `.gofer/tracker`，后三者是嵌套独立仓库。只登记 WP 根时，在 suga 仓库里跑的 chat 或 `gofer job --cwd inhere-tools/suga -a suga` 会选 suga 仓库自己的 tracker，且不可信。
  - gofer `--worktree` job 在 `<repo>/.worktrees/<name>` 中运行（`inhere-tools/gofer/internal/config/project_worktree.go:16`）。该目录含 `.git` 文件，所以 gitRoot 是这个 worktree 目录，和已登记的仓库根不相等，可信不生效。worktree 是临时目录，用户也没法预先登记。
  - 设计第 35 行只写“列出自己的仓库根目录”。方案 3 提到嵌套仓库，只是用来说明为什么不用前缀匹配，没有面向用户说明“要逐个登记”。第 92 行运维说明仍写“才设 true”。全文没有 worktree 的表述。
  - `config set memory.project.trusted <root>` 会整体替换列表（`settings_registry.go:504-520` 解析出整个新列表），想追加第二个仓库时会丢掉第一个。
- 影响: 行为由代码唯一决定且失败关闭，不是安全问题，所以不阻断。但用户按设计只登记 WP 根后，在嵌套仓库或 worktree job 里照抄记忆仍会弹询问，看起来像“功能没生效”。用户原始场景是 chat 读 gofer 托管记忆，记忆在哪个 tracker 取决于会话所在的仓库，所以这个后果直接影响用户能否达成核心目标。
- 建议:
  - Gate 处向用户披露：(1) 每个会在里面跑 suga、并想信任其 tracker 的仓库都要单独登记，父仓库不覆盖嵌套仓库；按本工作区布局，至少要登记 WP 根，再按需加 gofer、suga、xhen 等仓库。(2) gofer `--worktree` job 与其它 git worktree 中不生效，照旧询问。是否让 worktree 沿用主仓库的信任（按 `git-common-dir` 归属）属于扩范围，默认不做，记为 deferred，由用户决定。
  - 设计“用户可见结果”与第 92 行改写成上述两点，README/usage 同步，并写明列表用逗号或 `[a, b]` 一次写全，`config set` 会覆盖原列表。
- 同类穷尽: submodule（`.git` 文件）与 worktree 同类，也是独立 gitRoot。没有 `.git` 的目录会落到上层仓库根，这种情况不会多信任（后端 `Dir` 也是该根）。本类只此一处。

### TF7 [MEDIUM] “按 `suga manifest` 显示的记忆位置登记”只对 gofer 与仓库 MEMORY.md 成立

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据:
  - 方案 3 写“按 `suga manifest` 显示的记忆位置登记”。`MemoryBackendChoice.Location`（`memory.go:146-148`）只在 gofer 和仓库 `MEMORY.md` 两种后端下才是仓库根（`memory.go:293-303`）。exec 后端下它是命令（`memory.go:288-290`），默认 files 下它是 `<SUGA_HOME>/projects/<key>/memory`（`memory.go:305-310`）。manifest 原样输出 `Location`（`manifest.go:448-453`）。
  - 非目标写了“manifest 不显示”是否可信，只能用 `suga config get` 查看。`config get` 只能看到列表本身，看不到当前仓库是否匹配。
- 影响: 使用 exec 项目后端的用户（设计链路与端到端验证用的正是 exec）照这句话会登记命令路径或 SUGA_HOME 路径。这样的值是绝对路径，能通过校验，但永远不匹配，可信静默不生效，用户也没有办法核对。失败关闭，不是安全问题。
- 建议: 改为“登记会话所在仓库的 `git rev-parse --show-toplevel` 结果（与 suga 的 gitRoot 一致，worktree 下为 worktree 目录；gofer 后端时即 manifest 的 location）”，README/usage 同步。若要让用户能核对，最小做法是在 manifest 的 memory 项增加一个只读 `trusted` 布尔。这会推翻“manifest 不显示”的非目标，需用户决定；不做时只在文档写清登记依据即可。

### TF8 [LOW] 端到端观测点：询问文本与 judge 回复需固定

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据:
  - 设计验证节写“屏幕不出现‘逐字复制自外部来源’询问”。实际文本是 `参数里有逐字复制自 %s 结果的文本`（`origin_guard.go:120`），即“逐字复制自 memory 结果的文本”，代码里没有“逐字复制自外部来源”这串字。照写的话，已登记一侧的否定断言恒真。
  - judge 默认开启（`origin_judge.go`，`tools.origin_judge.enabled` 默认 true）。flag 命中后：judge 判 `allow` 时不询问但有 `origin_judge` 属性；判 `deny` 时拒绝且不询问；judge 关闭时会询问但没有 `origin_judge` 属性（`chat_origin_judge_test.go` 的 allow/deny/disabled 分支）。设计要求未登记对照组“两者都有”，这只在 judge 回复为 `ask` 时成立。
  - `fakellm_test.go` 现有的 `memory` 模式是 shell_exec 加 memory add（438-447 行），没有“先 memory get 再照抄到 shell_exec”的调用序列。
- 影响: 未登记对照组要求“两者都有”，会迫使实施者发现前两点，所以不会出现反向验证假通过，不阻断。不写清的话会多一轮试错。
- 建议: 询问断言改为匹配 `逐字复制自 memory`；对照组固定 fake judge 回复为 `ask`（沿用 `JudgeReply`）；写明 fakeLLM 要新增“memory get 后照抄到 shell_exec”的模式。

## 覆盖缺口与限制

- 静态阅读，未运行任何测试或命令验证行为。
- Windows 上大小写敏感目录（per-directory case sensitivity）里可能存在仅大小写不同的兄弟目录，`EqualFold` 会把它们视为同一仓库。触发需要用户自己启用该特性并在其中放置仓库，判断为可忽略，未列为发现。
- 未读 gofer 侧 tracker 的查找实现。“gofermem 以 `Dir` 为 cwd、读该目录的 tracker”依据 gofermem 包注释与 `memory.go:293-296`。
- 只覆盖 Spec/Executability 轴，以及 diff 涉及的章节与它所依赖的代码；未变章节沿用第 1 轮结论。

## 结论

PASS：TF1 在代码层面已关闭。整根相等加上 gitRoot 的定义，排除了第三方 clone 与嵌套 clone，所有不匹配都失败关闭。`ConfigTypeStringList` 存在，受保护快照能按键名报出列表变化，非法值会导致启动失败。TF2–TF5 的修订正确，可以实施。本轮没有新的 CORE_BLOCKING。TF6、TF7 是 `user_visible_effect: true` 的 CORE_CORRECTIVE，建议计划批准前修订文字，并在 Gate 向用户披露；TF8 随计划一起修即可。

## 剩余风险与后续

- Gate 需向用户说明（TF6）：嵌套仓库要逐个登记；worktree 及 gofer `--worktree` job 中不生效；`config set` 会覆盖整个列表。
- `config set memory.project.trusted relative/path` 在 set 阶段只做通用列表校验，写入后下次启动才失败（报错含键名）。属于用户自己的操作，可恢复，未列为发现；实施时可顺带在 set 阶段校验绝对路径。
- 若 `SUGA_HOME` 的 config.yaml 被 Windows 主机与 Linux 容器共用，一方的绝对路径在另一方会被判为相对路径，导致启动失败。本机容器未共用 `SUGA_HOME`，未列为发现。
- 方案 A、D5、D6 已由用户接受（可信后端被写入恶意指令时无外部来源防护，可经记忆自我扩散），本轮不再列。
