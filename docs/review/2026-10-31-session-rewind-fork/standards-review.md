<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 设计 规范轴评审

## 评审目标与范围

- 目标: `docs/design/2026-10-31-session-rewind-fork-design.md`（git root `inhere-tools/suga`，candidate commit `b7b33526`）
- 修订版本: 0.2
- 评审者: 规范轴独立评审子 agent（只读，与可执行轴隔离）；axis = Standards/Governance；round 2（confirmation）
- 日期: 2026-10-31
- 排除项: 算法与可执行性（归可执行轴）；`docs/review/` 下评审产物；未运行测试
- 第 1 轮报告见本文件 git 历史（提交 `b71dd04e`）

## 输入与方法

- 沿用第 1 轮已完整读取并核验 hash 的规范 payload（task/mode 未变）。
- 读 `git diff 0ae0a20c..b7b33526 -- <subject>`；因近乎重写，完整读 0.2（289 行）。
- 同类穷尽复核：列出 `internal/personal/store` 全部 entry/turn 读方法并 grep `internal`、`pkg` 非测试调用点；看 `ledger.go:100-125` 作用域。
- 新增引用抽查属实（行号偏差 ≤2）；`validate_document --kind design` ok。

## 验收标准

第 1 轮 S1–S11 逐条闭合；修订不引入新的规范问题（读者同类穷尽、R11/R12 理由、待确认如实、无新能力、SR1301、代码组织与结构条目）。

## 发现

### 第 1 轮闭合情况

S1（BUG，读者清单）CLOSED：适用规则改为“进入模型请求、交接包或用户可见当前状态”，已列全；剩余未列读者属非目标或不读内容。S2 CLOSED（R11）。S3 CLOSED（移入非目标）。S4 CLOSED（失败处理表）。S5 CLOSED（校验与 `:rw` 理由）。S6 CLOSED（created_at 比较，含 tainted_by）。S7 CLOSED（装配不变）。S8 CLOSED（budget 与确认）。S9 CLOSED（引用修正）。S10 CLOSED（回滚披露）。S11 CLOSED（按 OUT_OF_SCOPE）。

### N1 [LOW] `:checkpoints`/`:undo`（`undo.go:347` ListTurns）与 `segment_clear.go:100`（LastEntrySeq）未在“不受影响的读者”中归类（CORE_CORRECTIVE）

- 影响: 实施时可能误把 `:undo` 计数改为经屏蔽，致屏蔽轮文件改动无法撤销。
- 建议: 在计划中写明理由（文件改动仍在磁盘；LastEntrySeq 不读内容）。

### N2 [LOW] R11 只排除当前会话，workspace/all 作用域下其他会话（含 fork 源会话）屏蔽条目是否可检索未写明（CORE_CORRECTIVE）

- 建议: 计划中一句话写明取舍与理由。

## 覆盖缺口与限制

未逐行推演 fork 映射、分页补足与嵌套 cut（归可执行轴）；记忆自动提取与空闲整理只 grep；未运行测试。

## 结论

PASS（规范与治理轴，round 2）。无开放 CORE_BLOCKING；N1、N2 为 LOW CORE_CORRECTIVE，在实施计划落实。

## 剩余风险与后续

R11、R12 需用户批准时确认；回滚说明随 0.54 CHANGELOG 交付；两轴结论合并并提交 ledger 后形成可复用 PASS。
