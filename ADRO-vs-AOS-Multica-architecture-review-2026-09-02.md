## 执行摘要

本报告是对 ADRO、AOS、Multica 当前源码的重新复核，不是对任务状态、README 或上一轮模型回复的复述。结论先说：当前 ADRO 已经有一部分值得保留的单机执行内核，但还没有实现“可自由编排的 Agent 小队/图工作流”，也没有实现 Multica 风格的结构化评论区 @Agent/@Squad。以同一套 15 个维度、每项 10 分、总分 150 分计，ADRO 75.0，AOS 114.0，Multica 111.5。

这个分数不是“代码越多越高”，而是按可验证的能力、边界、测试证据和生产适用性评分。AOS 在上下文、记忆和运行时协调上领先；Multica 在 Squad、评论触发和实时分发上领先；ADRO 的优势是已经有较清晰的本地 runtime journal、工具生命周期约束和 harness 方向，但编排领域模型仍然被固定七阶段绑住。目标架构部分是待实现的设计稿，不能当成当前 ADRO 已有能力。

## 审计基线与证据规则

### 版本基线

| 项目 | 本地源码目录 | 对比 ref | 备注 |
| --- | --- | --- | --- |
| ADRO | `/Users/shareit/program/github/adro-clone` | `origin/main` = `4396b81d235d6c77975de15a0eea1c3165babc9a` | 当前远程唯一分支是 `main` |
| AOS | `/Users/shareit/program/github/aos` | `origin/main` = `4439ec77ac5bea05b703964c6093a80e49617063` | 已重新 fetch |
| Multica | `/Users/shareit/program/github/multica-source` | `origin/main` = `3d37828e9265fa36c10ec443e71deff79bfdca41` | grafted checkout，源码和 refs 可读 |

### 交付状态核实

- 远程 `origin` 只有 `refs/heads/main`，没有 `master`。
- 远程和本地都没有 `agent/adro/deba111c9f96`。
- 用户回复中声称的 `278184ab5b5409698dd70b9e859d9bbdf1aeb20b`、`eae29b08b60263088a04c26fd7e3a780cc91b7a4` 在 ADRO 的本地和远程 refs 中均不存在。
- 因此这两个 commit、`deba111` 分支及其“已实现”描述不能计入交付证据。当前 `main` 的可审计实现截止 `4396b81`。
- 本地未跟踪的 `description.md` 是已有文件，本报告不修改、不提交、不删除。
- 由于远程不存在 `deba111` 或其他待合并源分支，本轮没有可安全合并/删除的目标分支；不能猜测并删除其他分支。

### 评分规则

每个维度 0--10 分，三个项目使用相同权重。评分依据按优先级排列：

1. 当前 ref 中可阅读的源码和状态机约束；
2. 同一 ref 中的单元/集成/契约测试证据；
3. 持久化、恢复、权限、幂等和失败边界是否闭合；
4. 真实运行证据（本轮没有把 Mock、httptest、静态 fixture 或任务回复算作真实 Codex/浏览器/多副本证据）。

`部分` 表示有代码但边界不完整；`单机` 表示只在一个进程/本地文件成立；`缺失` 表示没有对应的一等模型或调用路径；`未验证` 表示代码可能存在，但当前没有真实环境证据。分数反映可交付能力，不奖励不必要的复杂度。

## 统一评分

| 维度（每项 10 分） | ADRO | AOS | Multica |
| --- | ---: | ---: | ---: |
| 1. Session 生命周期与恢复 | 5.5 | 8.0 | 7.0 |
| 2. Durable execution / runtime journal | 7.0 | 7.5 | 7.5 |
| 3. ContextEnvelope / ContextManifest | 6.5 | 9.0 | 5.5 |
| 4. 上下文压缩与溢出恢复 | 5.0 | 8.5 | 6.0 |
| 5. 上下文记忆生命周期与质量 | 5.0 | 9.0 | 5.5 |
| 6. Agent Loop 与工具执行 | 6.5 | 8.5 | 7.0 |
| 7. 多 Agent parent/child 编排 | 3.5 | 8.0 | 7.5 |
| 8. Squad / 小队一等模型 | 1.5 | 7.5 | 9.0 |
| 9. StreamEvents、重放与保留 | 5.5 | 6.5 | 9.0 |
| 10. 一致性、幂等、租约、fencing | 7.0 | 8.0 | 8.5 |
| 11. 可观测性与诊断 | 5.0 | 7.0 | 8.0 |
| 12. 分段 Prompt、trust、policy | 5.5 | 8.5 | 6.0 |
| 13. 真实测试与故障注入 | 5.0 | 7.0 | 7.5 |
| 14. 评论区 Agent/Squad mention | 2.0 | 4.0 | 9.5 |
| 15. 发布、安全与企业运维 | 4.5 | 7.0 | 8.0 |
| **合计 / 150** | **75.0** | **114.0** | **111.5** |

### 为什么这样打分

#### 1. Session 生命周期与恢复

- **ADRO 5.5，部分/单机。** `internal/provider/local.go` 的 `NewPersistentLocalProvider` 能恢复 run snapshot、session、workdir 和 provenance；Codex continuation 还要求输出 `thread.started` 作为原生会话连续性证据。但进程崩溃后不能恢复正在运行的子进程，只能把活动 run 标记失败并等待显式 repair；并且 runtime journal 只有在 `ADRO_RUNTIME_JOURNAL` 非空时才初始化（`NewPersistentLocalProvider`）。这不是原生 session 恢复，也没有跨进程 worker 接管证明。
- **AOS 8.0，较完整。** `rust/crates/runtime/src/session.rs`、`session_control.rs`、`execution_kernel.rs` 和 coordinator 共同维护会话、回合、控制动作和恢复边界；其 task 状态和 revision 使恢复后的继续执行可判定，但仍需部署级验证才能称为跨节点 HA。
- **Multica 7.0，较完整但目标不同。** Multica 以 issue/task/workdir/channel 为执行单元，任务队列和重试信息持久化；它更强在任务路由而非一个可任意恢复的模型 conversation kernel。

#### 2. Durable execution / runtime journal

- **ADRO 7.0，单机。** `internal/runtime/kernel.go` 有 event id、sequence、payload hash、previous hash、envelope hash、idempotency key、writer/fencing token、lease 和 `AppendBatch`；`internal/provider/local.go` 保存 run snapshot 并追加 `run.started`/`run.finished`。但当前证据是本地 JSON journal，不能推导 PostgreSQL/NATS/Redis 或多副本一致性。
- **AOS 7.5。** `execution_kernel.rs` 把运行时顺序与存储适配器解耦，配合 task registry 的 revision 和团队 worker lease，边界更清晰；本次不把未看到的生产数据库适配器计入满分。
- **Multica 7.5。** 业务任务和 Agent 绑定使用数据库生成层，realtime 另有 relay/retention；但它不是专门为 ADRO 这类模型 turn journal 设计的单一内核。

#### 3. ContextEnvelope / ContextManifest

- **ADRO 6.5，部分。** `internal/harness/store.go` 有 `ContextManifest`、`ContextEnvelope`、selection digest、replay key 和 block lineage，`CompileEnvelope` 有 hard token budget；pipeline/comment follow-up 会编译 prompt。但 provider 对外的 `StartRunCommand`/`ContinuationCommand` 当前仍以 `ContextID`、`ContextVersion` 和字符串输入为主，尚不能证明所有 dispatch 强制携带并消费完整 typed envelope。
- **AOS 9.0，强。** `rust/crates/semantic-core/src/context.rs` 的 `ContextPacket`、`ContextManifest`、`PromptLayer`、`ContextTrust`、mandatory/optional selection 和 hard budget 是 provider-facing 的明确契约，`execution_kernel.rs` 保存实际选择包和 prompt manifest 的 lineage。
- **Multica 5.5，部分。** 源码显示其上下文主要围绕 issue、comment、channel、task 和触发证据流转；本轮没有发现与 AOS 同等级的通用 ContextManifest/selection digest/replay contract，因此不按“有消息上下文”给高分。

#### 4. 上下文压缩与溢出恢复

- **ADRO 5.0。** harness 有摘要/上下文版本及超预算拒绝路径，但没有看到与 AOS 相同的分层压缩、溢出后重编译和恢复验证闭环。
- **AOS 8.5。** `summary_compression.rs` 负责受预算约束的摘要压缩，最近提交还针对 bounded history 和 adversarial turns 做了修复；其 manifest 能把压缩结果与具体快照关联。
- **Multica 6.0。** `stream_retention.rs`、channel revision 和 task history 提供保留边界，但它们解决的是事件/协作历史，不等价于模型上下文压缩。

#### 5. 上下文记忆生命周期与质量

- **ADRO 5.0，部分。** `internal/harness/store.go` 的 `AddMemory`、`TransitionMemory`、source turn 校验、fingerprint、supersedes 和 candidate/quarantine 类状态具备审计基础；`ReduceMemories` 主要按前缀、subject、fingerprint 和规则化文本抽取，缺少 evidence quality、污染 lineage、语义检索和冲突包等更完整质量机制。
- **AOS 9.0，强。** `rust/crates/memory-engine/src/lib.rs` 有 `MemoryFactDraft`、evidence/source hash、`FactLifecycle`、candidate/quarantined/confirmed/superseded/forgotten/rejected、pollution lineage、conflict package、scope 隔离和 repository TCK。
- **Multica 5.5。** Multica 的强项在 issue/task/mention/realtime 协作；本轮源码证据不足以证明其拥有 AOS 级记忆质量治理，因此保持保守分数。

#### 6. Agent Loop 与工具执行

- **ADRO 6.5，部分。** `internal/runtime/kernel.go` 的 `AuthorizeTool`、`StartTool`、`ApproveTool`、`FinishTool` 以及 open-tool 时禁止 `FinishTurn` 是正确的 fail-closed 方向；`local.go` 能解析 Codex/Claude JSONL tool 事件。但缺少真实 Codex 长循环、取消、重试副作用和工具结果回放证据。
- **AOS 8.5。** `execution_kernel.rs` 把权限请求、预算阶段、工具 contract、取消/重试/副作用策略纳入运行时边界，协调器还处理 approval、timeout、cancel 和 retry。
- **Multica 7.0。** Agent task 触发、重试、授权和工作目录链路成熟，但本轮没有发现与 ADRO/AOS 同粒度的通用 tool contract journal。

#### 7. 多 Agent parent/child 编排

- **ADRO 3.5。** pipeline 结果只接受当前 `PipelineStage` 和 active agent；`internal/pipeline/engine.go` 按固定状态切换。没有通用 parent/child task、fan-out/fan-in 或任意子图。
- **AOS 8.0。** `task_registry.rs` 有 parent_task_id、子任务状态约束、revision、approval、retry、timeout、cancel；`agent_coordinator.rs` 负责 parent/child lifecycle。
- **Multica 7.5。** Agent task 中有 `parent_task_id`、`delegated_from_task_id`、`retry_of_task_id`、`rerun_of_task_id` 和 leader routing，已形成可持久化的任务树，但业务流程图表达不如目标 ADRO 图模型明确。

#### 8. Squad / 小队一等模型

- **ADRO 1.5，缺失。** `PipelineAgentRoles` 固定 Designer/Developer/Tester/Arbitrator；`WorkflowStep` 绑定单个 Agent，没有 `SquadDefinition`、成员角色、版本化小队或小队路由实体。
- **AOS 7.5。** `web-server/src/agent_team.rs` 提供持久化 SQLite team、成员、嵌套深度、全局并发许可、spawn idempotency 和 worker lease；它是较强的 Agent Team 基础，但仍不是用户要求的任意业务 DAG 产品。
- **Multica 9.0，最强。** `server/internal/handler/squad.go` 有 Squad、leader、成员类型/角色、归档与权限；任务表面支持 `squad_id`、leader task、父任务、attempt 和委派链路。它已经是可用的小队协作模型，但不应误称为任意条件反馈图。

#### 9. StreamEvents、重放与保留

- **ADRO 5.5，单机/部分。** `internal/events` 和 runtime journal 有 sequence/hash，provider 有 stream replay 测试；但当前没有生产级 retention、consumer group、跨实例 fan-out、gap repair 和真实端到端 replay 证据。
- **AOS 6.5。** runtime/task events 与 revision 可重建状态，测试覆盖较多；本轮没有看到与 Multica Redis stream relay 同等的跨节点保留实现。
- **Multica 9.0。** `redis_relay.go`、`sharded_stream_relay.go`、`stream_retention.go` 明确实现 Redis relay、分片 stream、consumer group/ACK、replay grace、TTL、retention horizon 和 duplicate/gap 处理。

#### 10. 一致性、幂等、租约、fencing

- **ADRO 7.0，单机。** journal event 和 provider outbox 有 idempotency key、版本检查、lease/fencing、effect fence；但它们尚未由共享生产存储证明在多副本下成立。
- **AOS 8.0。** task revision 的乐观并发控制、team spawn/mailbox 幂等、worker lease 和 concurrency permit 均有源码测试。
- **Multica 8.5。** 任务触发去重、pending 检查、revision、attempt、relay ACK 和数据库约束形成较完整的一致性边界；仍需实际部署压测才能证明所有异常窗口。

#### 11. 可观测性与诊断

- **ADRO 5.0。** audit、event、run snapshot、tool event 和 error log 已存在，但缺少统一 trace/span、graph/node/attempt 维度、诊断查询和跨组件关联的成套界面。
- **AOS 7.0。** task events、approval/timeout/cancel 原因和 memory evidence 使诊断较可追溯，运行时状态仍偏模块内聚合。
- **Multica 8.0。** handler trigger outcomes、reason code、task failure_reason、trigger summary、originator 和 realtime 状态为运营诊断提供了更丰富的信号。

#### 12. 分段 Prompt、trust、policy

- **ADRO 5.5。** harness block lineage、manifest digest 和 hard budget 是基础，但尚未形成 AOS 那种稳定 system/domain/task/recent 分层、trust 等级和策略选择器的完整契约。
- **AOS 8.5。** `semantic-core/context.rs` 的 `PromptLayer`、`ContextTrust`、`ContextReference`、强制/可选 block 选择和预算隔离形成清晰模型。
- **Multica 6.0。** 有 squad instructions、comment/task context 和权限边界，但本轮未见同等级通用 prompt manifest。

#### 13. 真实测试与故障注入

- **ADRO 5.0。** 当前 main 的 Go 单元/集成测试和 `ADRO-release-expert-test-plan.zh-CN.md` 约 880 行用例覆盖面不错；但本轮没有执行真实 Codex 全链路、浏览器 `npm run test:e2e*`、跨进程恢复或生产 adapter conformance，因此这些不算 PASS。
- **AOS 7.0。** Rust runtime、memory TCK 和 agent-team 测试覆盖状态机及故障边界，仍不能替代真实多节点测试。
- **Multica 7.5。** comment trigger preview、squad routing、mention authority 和 realtime 相关测试较系统；本轮没有运行完整生产部署和浏览器矩阵。

#### 14. 评论区 Agent/Squad mention

- **ADRO 2.0，明显缺口。** `internal/api/comments.go` 的 `commentMentions` 只用 `strings.Fields` 识别普通 `@token`，最多 32 个；没有解析 `mention://agent/<uuid>` 或 `mention://squad/<uuid>`，没有 roster/权限/invoke gate、trigger preview、per-target outcome、coalesced/deferred/blocked 结果，也没有编辑评论后的重触发语义。`queueCommentFollowUp` 的 `agent_binding_id` 是受限 follow-up，不等于结构化 mention。
- **AOS 4.0。** AOS 有 task/team 消息和 parent/child 协作，但本轮没有发现 Multica 风格的评论 mention authority 和触发预览契约。
- **Multica 9.5，最强。** `comment.go` 的 `computeCommentAgentTriggers` 统一处理显式 Agent/Squad mention、`@all` 抑制规则、leader fallback、去重和 pending 检查；`PreviewCommentTriggers` 与 create/edit 共用计算逻辑，返回 queued/coalesced/deferred/blocked outcome，并支持 suppress、expected revision 和附件替换。

#### 15. 发布、安全与企业运维

- **ADRO 4.5，单机交付。** `cmd/adro-api/main.go` 配置多个本地状态文件；现有 runtime journal、审计和权限 contract 是基础，但 PostgreSQL/Redis/NATS/Temporal/S3、多副本、真实浏览器门禁和生产回滚证据均未交付。本轮不能把延期项算入分数。
- **AOS 7.0。** workspace/tenant scope、权限、SQLite 持久化 team、并发许可和状态恢复较完整，但生产化边界仍需环境验证。
- **Multica 8.0。** workspace 权限、数据库任务、Redis relay、归档、审计和运维状态较成熟；是否满足某一企业的 HA/SLO 仍取决于部署配置和实际演练。

## ADRO 当前能力与缺口清单

### 已有、可以继续保留的部分

1. `internal/runtime/kernel.go` 已有较好的事件完整性字段、append 顺序、租约/fencing 和工具生命周期拒绝规则。
2. `internal/harness/store.go` 已经建立 ContextManifest/ContextEnvelope、selection digest、replay key、block lineage 和 hard budget 的方向。
3. `internal/provider/local.go` 有 run snapshot、workdir/session provenance、Codex `thread.started` continuity 检查和本地 replay 测试。
4. `internal/api/pipeline.go`、`internal/pipeline/engine.go` 已把失败证据、repair attempt 和 provider session/workdir 关联起来。
5. 评论 follow-up 有 receipt、outbox 和 continuation 尝试，可作为新 mention 路由的底座。

### 当前不能宣称已经支持的部分

1. **任意 Agent/Squad 图编排：不支持。** `PipelineStage` 是 1--7 的固定枚举；workflow 仍按 stage 排序，禁止重复 stage，要求 report stage，且没有 edge/condition/fan-in/fan-out。
2. **可复用 Squad：不支持。** 没有 squad 表、版本、成员角色、leader、成员能力约束和选中 squad 的需求计划快照。
3. **双向反馈：仅有固定回路的部分语义。** 集成失败可走 arbitration/development，单测失败主要在本阶段重试；不能配置“任意节点失败回到指定节点”，也不能以结构化证据决定回退目标并保留每次 attempt lineage。
4. **评论区结构化 @Agent/@Squad：不支持。** 普通 `@名字` 不是可靠 roster 引用；`agent_binding_id` 也不能表达评论内多个 Agent/Squad 的显式触发关系。
5. **真实全链路 Codex/浏览器/多副本证据：本轮没有。** 不能把本地 mock、静态测试计划或未存在的 commit 当作证据。

## 目标架构：可版本化的有界图，而不是固定阶段

以下是建议的底层设计，目标是让 ADRO 成为可读、可验证、可扩展的开源执行平台。它是设计稿，除非实现并通过门禁，否则不应标记为已完成。

### 设计原则

- **Graph first。** 阶段只是 UI 视图或预置模板，运行时只认识节点、边、条件和 attempt。
- **Immutable plan。** 需求启动时冻结 `SquadDefinition`、`WorkflowGraph`、Agent policy、ContextManifest 和工具 contract 的版本；模板后续更新不改变正在运行的需求。
- **Evidence before transition。** 节点只有在输入 manifest、输出 artifact、结构化结果和事件提交完成后，才能触发下一条边。
- **Explicit feedback.** 回退目标必须由配置的 edge 指定，不能让模型自由猜“回到开发”。
- **Bounded autonomy。** 每个 loop group 同时受 traversal 次数、token/tool budget、deadline 和人工接管出口约束。
- **Provider neutral, executor real.** Codex/其他 executor 只执行已授权的 attempt；状态、幂等、重放和审计由 ADRO 内核负责。

### 核心数据模型

```text
AgentDefinition {
  agent_id, workspace_id, revision, role, instructions,
  capabilities[], tool_policy, memory_policy, executor_binding,
  concurrency_budget, status
}

SquadDefinition {
  squad_id, workspace_id, name, version, description,
  members[], graph_template, policy, status
}

SquadMember {
  member_id, agent_id, role, input_schema, output_schema,
  capability_constraints, max_attempts, budget
}

WorkflowGraph {
  graph_id, version, entry_nodes[], exit_nodes[], nodes[], edges[],
  validation_digest
}

WorkflowNode {
  node_id, kind(agent|squad|gate|human|merge|repair),
  agent_ref?, squad_ref?, input_contract, output_contract,
  context_policy, tool_policy, retry_policy, timeout, budget
}

WorkflowEdge {
  edge_id, from, to, on(success|failure|timeout|approval|bug),
  condition, priority, max_traversals, required_evidence, loop_group
}

RequirementExecutionPlan {
  plan_id, requirement_id, workspace_id, graph_snapshot,
  selected_agent_or_squad, policy_snapshot, context_root,
  plan_hash, status, created_at
}

NodeAttempt {
  attempt_id, plan_id, node_id, attempt_no, run_id, session_id,
  workdir, lease, input_manifest, output_artifacts, decision,
  status, reason, started_at, finished_at
}

FeedbackDecision {
  source_node, target_node, condition, structured_result,
  reason, evidence_ids[], plan_version, idempotency_key, loop_count
}

CommentMention {
  comment_id, target_type(agent|squad), target_id,
  parser_version, authority_snapshot, trigger_outcome, dedupe_key
}
```

`condition` 必须是受限的结构化 predicate，例如 `tests.failed > 0`、`severity >= high`、`approval.status == rejected`、`artifact.schema == v3`；禁止直接执行任意脚本。条件解释结果和命中的 edge 需要进入事件流，保证可诊断和可重放。

### 图验证规则

创建或发布 graph 时必须拒绝：孤立节点、不可达 exit、重复 node_id、未知 agent/squad revision、schema 不兼容、无界 loop、反馈边绕过必要验证、超过 workspace 并发/预算、权限不足的工具 contract，以及没有人工出口的高风险自动回路。验证结果产生 `validation_digest`，并在 plan 中冻结。

### 双向编排语义

用户给出的例子应表达为图，而不是写死在 `nextStage`：

```text
开发 --success--> 单测
单测 --pass--> 测试
单测 --failure(condition=compile_or_unit_bug)--> 开发
测试 --pass--> 发布门禁
测试 --bug(condition=severity>=medium)--> 开发
测试 --timeout--> 人工仲裁
```

当单测失败时：

1. 关闭当前 `NodeAttempt`，保存真实失败测试、日志、artifact hash 和 context lineage；
2. 依据显式 feedback edge 创建新的开发 `NodeAttempt(attempt_no + 1)`；
3. 开发成功后只允许回到该图中配置的单测节点，不能跳到测试或发布；
4. 单测通过后才允许沿 success edge 进入测试；
5. 测试发现 bug 时重复同样流程，是否需要重新单测由 graph edge 明确规定；
6. 超过 loop 上限、预算或 deadline 时转 `human` 节点并保留所有历史 attempt。

旧 attempt 的迟到结果必须因 `plan_hash + node_id + attempt_id + fencing_token` 不匹配而被拒绝，不能覆盖新 attempt。相同输入重试使用幂等键返回既有结果；不同 graph version 的结果不得混用。

### Agent 与 Squad 调度

发布需求时提供两条等价入口：选择一个 Agent，或选择一个已发布 Squad version。另提供“快捷创建小队”：在需求草稿中生成未发布的 Squad draft，用户补充成员、职责、图边和预算后先 validate/dry-run，再冻结到 execution plan。快捷入口不能绕过权限、能力、循环和成本验证。

Squad leader 只负责路由/汇总，不应拥有绕过成员 policy 的特权。成员可以是 Agent，也可以是受限子 Squad，但必须限制嵌套深度，避免递归爆炸。并行 fan-out 要有显式 join policy（all、quorum、first_success），每个分支都有独立 attempt 和 context child lineage。

### Session、ContextManifest 与记忆

每个 `NodeAttempt` 绑定不可变 `ContextManifest`：block source/hash/trust/policy、selection digest、hard token budget、replay key、parent lineage 和 semantic snapshot version。provider command 必须携带完整 envelope，而不是只携带 `ContextID` 后在下游重新猜选集。

压缩时产生新 manifest version，并记录被压缩 block、摘要模型/版本、输入 hash、预算和可恢复的 overflow reason。恢复必须从 manifest 加载同一 semantic snapshot，不能从当前数据库“最新内容”重建历史。

记忆写入使用 evidence source hash、scope（tenant/workspace/project/session）、生命周期、敏感信息过滤、冲突包和污染 lineage；只有满足质量门禁的 confirmed fact 才能进入后续 context。forget/supersede/reject 都要产生可重放事件。

### 事件、重放和一致性

统一 `ExecutionEvent` 至少包含：`event_id`、schema version、sequence、tenant/workspace/plan/run/node/attempt scope、correlation/causation、idempotency key、payload/envelope hash、writer、lease/fencing、committed_at 和 recovery state。

单机阶段可以使用 SQLite/JSON append journal，但必须保持同一接口：append 在锁内校验 sequence/hash/lease；snapshot 只有在 event commit 后才对外暴露 terminal；replay 按 sequence 重建 projection；事件保留、压缩和 artifact 引用有明确 TTL。未来集群再替换为 PostgreSQL + Redis/NATS relay，不改变领域事件契约。

### 评论区 @Agent/@Squad

推荐兼容 Multica 的结构化语法：

```markdown
[@方案设计](mention://agent/<agent-uuid>)
[@开发小队](mention://squad/<squad-uuid>)
[@all](mention://all/all)
```

ADRO 需要实现：

- Markdown mention parser，拒绝显示名冒充 UUID；
- workspace roster、成员权限、Agent capability 和 invoke gate 校验；
- `trigger-preview`，与 create/edit 使用同一个触发计算器；
- `queued`、`coalesced`、`deferred`、`blocked` 及 reason code 的逐目标结果；
- `suppress_agent_ids`、`expected_revision`、评论编辑重触发和附件替换；
- agent/squad 触发去重、originator/source task/parent comment lineage、审计事件；
- 方案 Agent 在评论中交付设计后，人类可以在同一线程 @研发 Agent；研发完成后可由评论或图边 @单测/测试；失败反馈必须进入 execution plan 的显式 feedback edge，而不是仅创建一个普通 follow-up。

`member` 和 `issue` mention 只做渲染，不应触发执行；`@all` 的抑制和显式 Agent/Squad 优先级要有契约测试。触发结果不能只写日志，必须持久化 receipt 并可查询、重放和审计。

### 建议的目标 API（设计契约，不代表当前已存在）

```text
POST   /api/v1/agents
GET    /api/v1/agents
PATCH  /api/v1/agents/{id}
POST   /api/v1/squads
GET    /api/v1/squads
GET    /api/v1/squads/{id}
PATCH  /api/v1/squads/{id}
POST   /api/v1/squads/{id}/versions
POST   /api/v1/squads/{id}/validate
POST   /api/v1/squads/{id}/dry-run
POST   /api/v1/requirements/{id}/execution-plan
GET    /api/v1/requirements/{id}/execution-plan
POST   /api/v1/requirements/{id}/execution-plan/validate
POST   /api/v1/requirements/{id}/comments/trigger-preview
POST   /api/v1/requirements/{id}/comments
PATCH  /api/v1/comments/{id}
GET    /api/v1/comments/{id}/trigger-outcomes
POST   /api/v1/runs/{id}/feedback
POST   /api/v1/runs/{id}/rerun
GET    /api/v1/runs/{id}/replay
```

所有 mutation 都应支持 idempotency key；并发更新使用 expected revision；响应中返回 plan hash、selected graph version、attempt id 和 trigger outcomes。目标 API 不能在实现前写入 OpenAPI 作为“已支持”证据。

## 单机部署与 Multica 集群问题

### ADRO 当前建议边界

本阶段可以把 ADRO 定义为单机部署：一个 API 进程、一个本地 executor、SQLite/JSON 状态和本地 workdir。该模式适合开发、演示和单租户受控生产，但必须明确单点故障、磁盘备份、进程重启后子进程不可恢复、没有跨节点抢占和有限的事件保留。PostgreSQL/Redis/NATS/Temporal/S3、多副本和跨节点 conformance 按用户指定继续 deferred，不应虚报完成。

### Multica 是否支持集群

从源码看，Multica **有条件地支持集群部署**：`server/internal/realtime/redis_relay.go`、`sharded_stream_relay.go` 和 `stream_retention.go` 提供 Redis relay、分片 stream、ACK/consumer group、重放宽限和保留策略；数据库生成层和任务字段支持跨实例共享业务状态。因此它具备多实例协作的基础。

但“源码有 relay”不等于任意环境下自动 HA。实际集群仍需要共享且高可用的数据库、Redis 拓扑、worker lease/锁、事件保留和网络故障演练，并应以官方部署配置和真实 conformance 结果确认 SLO。此次复核没有运行 Multica 的生产集群，所以结论只能是“有条件支持”，不是无条件保证。

## 测试与发布门禁设计

现有 `ADRO-release-expert-test-plan.zh-CN.md` 已覆盖菜单、OpenAPI、组合场景、双向回退和评论 mention 用例；本报告不把它当作执行结果。实现目标架构后，必须新增以下门禁，并在每次代码变更和 push 后自动运行：

### 图和生命周期契约

- 任意数量 Agent、零/多 Squad、Agent 与 Squad 混合节点、嵌套深度和能力不匹配；
- A（开发→单测→测试）与 B（方案→开发→单测）两个不同 graph 同时运行；
- 单测失败回开发，修复后强制重新单测；测试 bug 回开发并按 edge 重新进入回归；
- 并行 fan-out/fan-in、quorum、超时、审批、人工接管、循环上限和 deadline；
- 模板新版本发布后，旧 plan 仍使用旧 graph snapshot；
- 迟到结果、重复回调、同一幂等键不同 payload、租约过期和 fencing token 变化全部 fail-closed。

### Session、Context、Memory

- 同一用户两个需求并行时 session/workdir/memory/event 完全隔离；多个用户并发时无越权串线；
- 压缩前后 manifest digest、hard budget、selection digest、replay key 和 lineage 可重放；故意溢出后只允许有证据的 recovery；
- memory candidate/quarantine/confirmed/superseded/forgotten/rejected、污染证据、冲突和敏感内容过滤；
- 进程 kill、磁盘写失败、事件重复/乱序、恢复后旧 executor 输出均不能推进错误节点。

### Agent/Squad mention

- 合法 `mention://agent/<uuid>`、`mention://squad/<uuid>`、无效 UUID、跨 workspace、无 invoke 权限、重复 mention、`@all` 抑制规则；
- 方案 Agent 评论后，人类在同一线程 @研发 Agent；研发完成后 @单测/测试；每个目标返回独立 trigger outcome；
- 评论编辑使用 expected revision，内容改变时取消旧触发并重算；agent 自编辑保留 lineage，其他编辑按权限清除；
- coalesced/deferred/blocked 的 receipt 可查询、重试和重放，附件/截图与 comment action 同一审计链路。

### 真实执行和 CI

- 使用本地真实 Codex 可执行程序（如果命令路径不存在则门禁必须失败并报告环境缺口），执行真实 JSONL turn、tool approval、continuation、取消和 repair；不得用 mock 输出替代；
- 启动 ADRO API，执行 Issue→Agent/Squad→session→workdir→StreamEvents→attachment→repair/rerun 全链路；浏览器入口必须实际存在并运行 `npm run test:e2e*`；
- Go：`go test ./... -count=1 -p 1`、`go test -race ./... -count=1 -p 1`、`go vet ./...`、`go build ./...`、`git diff --check`；
- 发布前运行故障注入矩阵：executor kill、journal fsync 失败、网络断开、Redis/DB 不可用、重复消息、乱序消息、lease 抢占、超时、预算耗尽；
- GitHub Actions 必须在 push/PR 上运行同一脚本并上传 event/replay/artifact/log 证据；只显示绿色 job、没有附件证据的结果不得作为 release gate；
- 多副本和生产 adapter 按当前范围 deferred，但应保留明确的 `blocked/deferred` gate，防止误报为 PASS。

## 分阶段实施路线

1. **领域模型阶段：** 新增 AgentDefinition、SquadDefinition、WorkflowGraph、NodeAttempt、FeedbackDecision 和 plan snapshot；保留旧七阶段 API 作为迁移适配层，不让其继续成为内核状态。
2. **单机执行阶段：** 把 runtime journal、outbox、ContextEnvelope、artifact 和 graph transition 统一到同一 event/attempt contract；实现幂等、lease、fencing 和重放。
3. **编排阶段：** 支持条件边、反馈边、循环预算、并行 join、人工节点和 dry-run/validate；完成 A/B 示例以及失败回路的真实 Codex 测试。
4. **评论触发阶段：** 实现结构化 mention、roster/权限、preview、outcomes、编辑重触发和 comment-to-plan handoff。
5. **发布门禁阶段：** 接入真实浏览器和 Codex 全链路、故障注入、证据归档和 GitHub Actions；在此之前不得宣称对齐 AOS/Multica。
6. **未来集群阶段：** 在不改变领域事件契约的前提下接入 PostgreSQL 与 Redis/NATS relay，做跨节点 fencing、replay、failover 和 conformance；这是单机交付之后的独立里程碑。

## 最终诚实结论

- 当前强弱：AOS 在 Context、Memory、runtime coordination 上总体第一；Multica 在 Squad、评论 @触发和实时事件上总体第一；ADRO 在本地 runtime journal、工具 fail-closed 和 provider provenance 上有扎实起点，但总能力明显落后，尤其落后在自由编排、Squad、结构化 mention、真实全链路和生产运维。
- ADRO 当前 **没有** 满足“任意数量 Agent、可复用小队、条件双向回退、循环可控、评论区 @Agent/Squad”的完整目标。固定七阶段兼容层不能包装成自由图编排。
- 上一轮声称的 `deba111` 和两个 commit 未进入 origin，不能计入分数或验收。本轮新增的是源码审计和目标架构文档，不是这些能力的实现。
- 目标架构可以让 ADRO 在“可解释的业务图 + 强证据执行内核 + 灵活 Agent 小队”上形成差异化，但必须按上述路线实现并通过真实 Codex、浏览器、故障注入和发布门禁后，才有资格对外宣称顶级开源项目级别。
