## 文档定位

本文是 ADRO Agent/Squad 自由编排能力的开发实施规格，不是宣传稿，也不是“任务完成”说明。目标读者是负责后端、前端、数据库、执行器和 QA 的工程师。实现前，本文中标记为“目标”的接口和模型都不存在；只有代码、迁移、测试和真实运行证据同时满足验收门禁，才能标记为完成。

本规格解决四个核心问题：

1. Agent 可以单独执行，也可以作为 Squad（小队）成员执行；
2. 每个需求可以选择不同的 Agent/Squad，并使用不同的拓扑，不受固定 1--7 阶段限制；
3. 任意节点可以配置成功、失败、超时、审批、Bug 等边，形成有边界的双向反馈和循环；
4. 评论区可以使用结构化 mention 将上下文交给指定 Agent 或 Squad，触发结果可预览、审计、重试和重放。

这不是要求一次性拆成微服务。第一阶段允许保持 Go 模块化单体，但必须先建立下面的领域边界和持久化契约，未来拆分服务时不改变事件语义。

## 1. 现状、源码依据和改造边界

### 1.1 当前 ADRO 基线

代码基线：ADRO `origin/main` = `4396b81d235d6c77975de15a0eea1c3165babc9a`。此前声称的 `agent/adro/deba111c9f96` 及两个相关 commit 不在 refs 中，不能作为本规格的实现依据。

| 现有能力 | 复用源码 | 当前结论 | 本次改造动作 |
| --- | --- | --- | --- |
| 运行时事件、幂等、租约、fencing | `internal/runtime/kernel.go` | 单机 durable kernel，方向正确 | 抽象为 `ExecutionLedger`，增加 plan/node/attempt scope 和 projection rebuild |
| LocalProvider、run snapshot、workdir/session provenance | `internal/provider/local.go` | 能保存本地运行证据；崩溃不能恢复子进程 | 接入 attempt lease、完整 ContextEnvelope、真实 executor capability |
| ContextManifest/Envelope、selection digest | `internal/harness/store.go` | 有类型和校验，但部分 dispatch 仍以 ContextID/字符串为主 | 让 envelope 成为 provider command 的必需字段，禁止隐式重建 |
| pipeline 状态机 | `internal/domain/pipeline.go`、`internal/pipeline/engine.go` | 固定 `PipelineStage` 1--7，只有顺序切换和少量固定回退 | 新增 graph engine；旧 stage 仅作为兼容适配器 |
| pipeline API | `internal/api/pipeline.go` | workflow 仍按 stage normalize，必须有 report stage | 新增 execution-plan API；旧 API 转换为 graph 后执行 |
| 评论和 follow-up | `internal/api/comments.go` | `strings.Fields` 解析普通 `@token`；有受限 `agent_binding_id` follow-up | 实现结构化 `mention://agent`/`mention://squad`、preview 和 per-target outcome |
| 审计、事件总线、附件 | `internal/audit`、`internal/events`、`internal/artifact` | 可作为事实和证据底座 | 将 comment、edge decision、artifact commit 纳入统一相关 ID |

### 1.2 AOS/Multica 参考点（只借鉴契约，不复制代码）

| 参考能力 | 源码位置 | 应借鉴的工程点 |
| --- | --- | --- |
| typed context、prompt layer、trust、预算 | AOS `rust/crates/semantic-core/src/context.rs` | ContextPacket/Manifest、mandatory/optional 选择、hard budget、trust 和 snapshot lineage |
| 压缩和恢复 | AOS `rust/crates/runtime/src/summary_compression.rs` | 压缩结果可解释、受预算约束、能从同一快照重放 |
| task lifecycle、revision、parent/child | AOS `rust/crates/runtime/src/task_registry.rs`、`agent_coordinator.rs` | 乐观版本、显式 approval/timeout/cancel/retry、父子终态约束 |
| durable Agent Team | AOS `rust/crates/web-server/src/agent_team.rs` | team scope、spawn idempotency、worker lease、并发许可、嵌套深度 |
| evidence memory | AOS `rust/crates/memory-engine/src/lib.rs` | evidence hash、lifecycle、污染隔离、冲突包、repository TCK |
| Squad 和成员权限 | Multica `server/internal/handler/squad.go` | Squad/leader/member/role、归档和 workspace 权限 |
| mention 触发计算 | Multica `server/internal/handler/comment.go` | 结构化 mention、显式优先级、权限 gate、去重和触发结果 |
| mention preview/edit | Multica `server/internal/handler/comment_trigger_preview_test.go` | preview 与 create/edit 共用计算器、revision、suppress、blocked reason |
| realtime replay/retention | Multica `server/internal/realtime/redis_relay.go`、`sharded_stream_relay.go`、`stream_retention.go` | relay、ACK、retention、TTL、duplicate/gap 处理 |

这些参考项目也不能被无条件复制：AOS 的部分组件是 in-memory 或 SQLite profile，Multica 的 realtime 需要 Redis。开发时必须标注 profile 和证据级别。

## 2. 设计目标和不变量

### 2.1 功能目标

- AgentDefinition 可独立管理职责、能力、工具、记忆、执行器和并发预算。
- SquadDefinition 是可版本化的一等实体，包含成员和 graph template；成员可以是 Agent，也可以是受限子 Squad。
- 需求启动时选择 Agent 或已发布 Squad version；也可以在需求草稿中快捷创建 Squad draft，验证通过后冻结。
- graph 支持串行、并行 fan-out/fan-in、条件分支、人工 gate、显式 feedback edge 和有界循环；节点数量不设 1--7 上限。
- 每次重新执行节点都创建新的 NodeAttempt，保留旧 attempt 的输入、输出、artifact 和失败原因。
- 评论 mention 能指定 Agent/Squad，返回逐目标 queued/coalesced/deferred/blocked 结果，并可查询、重试、重放。
- 任何 terminal 状态都必须同时通过 evidence gate、event commit、artifact commit 和权限审计。

### 2.2 不变量（代码必须有测试）

1. `RequirementExecutionPlan` 发布后不可变；模板或 Agent 新版本只影响新 plan。
2. 只有入边条件满足、输入 contract 校验通过且前驱 evidence 已提交的节点才能进入 `ready`。
3. feedback edge 必须显式配置，模型输出不能隐式选择回退目标。
4. 回退创建新 attempt，不修改旧 attempt；旧 attempt 的迟到结果不得覆盖新状态。
5. 单测失败回开发后，开发成功只能沿图中配置的边重新进入单测，不能跳过必要验证。
6. 每个 `loop_group` 受 traversal、token/tool budget、deadline 和人工出口共同限制。
7. 幂等键相同且 payload 相同返回原结果；相同键不同 payload 必须 `idempotency_conflict`。
8. lease/fencing token 失效时，状态写入、副作用和事件提交均 fail-closed。
9. plan、run、node、attempt、session、workdir、memory、event、artifact 都必须带 tenant/workspace scope。
10. 所有对外的 terminal/trigger outcome 都能通过 event sequence 和 receipt 重放得到同一结果。

## 3. 目标模块布局

第一阶段仍可在一个进程中编译，建议建立如下包边界：

```text
internal/
  orchestration/
    model.go          # AgentDefinition, SquadDefinition, Graph, Plan, Attempt
    validate.go       # graph/schema/policy validation
    transition.go     # pure transition reducer and edge predicate evaluator
    scheduler.go      # ready queue, leases, fan-out/fan-in, join policy
    feedback.go       # failure/bug/timeout/approval routing
    repository.go     # repository interfaces, no DB imports
  execution/
    provider.go       # provider-neutral typed command/result contracts
    capability.go     # detector/negotiation/health
    ledger.go         # adapter to runtime Journal
  mentions/
    parser.go         # mention:// parser and AST
    resolver.go       # roster, permission, invoke gate
    triggers.go       # shared preview/create/edit trigger computation
  context/
    manifest.go       # immutable envelope, selection, lineage, budget
    compiler.go       # deterministic selection and compression boundary
  memory/
    repository.go     # evidence-backed lifecycle and retrieval contract
  api/
    agents.go squads.go execution_plan.go comments.go runs.go
```

`internal/domain/pipeline.go` 的旧类型可以保留在 `internal/compat/pipeline_stage.go`，但新 scheduler 不得直接依赖 `PipelineStage` 或 `NextSelectedStage`。

## 4. Agent 管理模型

### 4.1 AgentDefinition

建议 Go 类型（字段名可按现有项目风格调整，但语义不能减少）：

```go
type AgentDefinition struct {
    ID                string
    WorkspaceID       string
    Revision          int64
    Name              string
    Role              string
    Instructions      string
    Capabilities      []CapabilityRef
    ToolPolicy        ToolPolicy
    MemoryPolicy      MemoryPolicy
    ExecutorBinding   ExecutorBinding
    ConcurrencyBudget Budget
    InputSchema       SchemaRef
    OutputSchema      SchemaRef
    Status            AgentStatus // draft, active, disabled, archived
    CreatedBy         string
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

约束：`ID` 不可复用；更新必须带 `expected_revision`；active Agent 必须有已验证的 executor binding、输入/输出 schema、权限和工具策略；不能把显示名作为路由主键。Agent 禁用不影响已冻结 plan，但阻止新 attempt，并提供迁移或人工接管路径。

### 4.2 ExecutorBinding 和能力

```go
type ExecutorBinding struct {
    ProviderID      string
    ProviderVersion string
    BinaryDigest    string
    RequiredCaps    []string // session.start, session.resume, stream.events, cancel...
    ConfigVersion   string
}
```

调度前取以下集合的交集：Agent 声明、executor 探测、租户策略、项目策略、节点要求和当前配额。缺任意必需能力返回 `capability_unavailable`，不得悄悄换 provider 或创建不连续的 session。

### 4.3 Agent 管理 API

```text
POST   /api/v1/workspaces/{workspace_id}/agents
GET    /api/v1/workspaces/{workspace_id}/agents?status=&capability=
GET    /api/v1/agents/{agent_id}
PATCH  /api/v1/agents/{agent_id}                 # expected_revision required
POST   /api/v1/agents/{agent_id}/validate
POST   /api/v1/agents/{agent_id}/disable
POST   /api/v1/agents/{agent_id}/enable
GET    /api/v1/agents/{agent_id}/capabilities
```

响应必须返回 `revision`、`status`、有效 policy digest、executor health 和不可用能力原因。删除采用 archive，不物理删除被历史 plan 引用的定义。

## 5. Squad 和自由编排模型

### 5.1 SquadDefinition

```go
type SquadDefinition struct {
    ID               string
    WorkspaceID      string
    Name             string
    Description      string
    Revision         int64
    PublishedVersion int64
    Members          []SquadMember
    Graph            WorkflowGraph
    Policy           SquadPolicy
    Status           SquadStatus // draft, published, disabled, archived
}

type SquadMember struct {
    ID                    string
    AgentID               string
    Role                  string
    InputSchema           SchemaRef
    OutputSchema          SchemaRef
    CapabilityConstraints []CapabilityConstraint
    MaxAttempts           int
    Budget                Budget
    Optional              bool
}
```

同一个 Agent 可以出现在多个 Squad，但每个 Squad member 必须有自己的 role、schema、预算和 capability constraint。leader 是路由/汇总角色，不得绕过成员 policy。子 Squad 必须声明最大嵌套深度和独立预算。

### 5.2 WorkflowGraph

```go
type WorkflowGraph struct {
    ID               string
    Version          int64
    EntryNodeIDs     []string
    ExitNodeIDs      []string
    Nodes            []WorkflowNode
    Edges            []WorkflowEdge
    ValidationDigest string
}

type WorkflowNode struct {
    ID              string
    Kind            NodeKind // agent, squad, gate, human, merge, repair
    AgentRef        *VersionedRef
    SquadRef        *VersionedRef
    InputContract   ContractRef
    OutputContract  ContractRef
    ContextPolicy   ContextPolicy
    ToolPolicy      ToolPolicy
    RetryPolicy     RetryPolicy
    Timeout         time.Duration
    Budget          Budget
    JoinPolicy      JoinPolicy // merge: all, quorum, first_success
}

type WorkflowEdge struct {
    ID               string
    From             string
    To               string
    On               EdgeEvent // success, failure, timeout, approval, bug, cancel
    Predicate        Predicate
    Priority         int
    MaxTraversals    int
    RequiredEvidence []EvidenceRequirement
    LoopGroup        string
}
```

`Predicate` 必须是受限 AST，不允许执行任意 Go、shell、JavaScript 或模型生成脚本。最小节点包括：`field_eq`、`number_cmp`、`contains`、`all`、`any`、`not`、`exists`；最大深度、节点数和执行时间都有限制。谓词评估输出 `matched_edge_id`、变量快照和解释文本，写入事件。

### 5.3 图验证

发布 graph 时必须检查：

- node/edge ID 唯一，from/to 都存在，entry/exit 可达；
- 每个 agent/squad ref 指向同 workspace 的已发布 revision；
- input/output schema 可连通，join 的分支有明确 join policy；
- 不存在无 `max_traversals` 的 cycle；所有高风险 cycle 有 human exit；
- feedback edge 不得绕过声明的必经 gate/evidence；
- 节点预算总量、最大并发、嵌套深度和超时在 workspace policy 内；
- 工具、网络、Secret、仓库和 artifact 权限可由执行者满足；
- 任何 exit 都有 terminal evidence policy。

验证失败返回稳定错误码和 path，例如 `graph.edges[3].predicate.depth_exceeded`，供 UI 和 CI 直接定位。

### 5.4 需求启动与快捷创建 Squad

```text
POST /api/v1/requirements/{requirement_id}/execution-plan
```

请求支持二选一：

```json
{
  "agent_id": "agent-dev-v4",
  "graph_version": "graph-single-agent-v1",
  "idempotency_key": "req-123-plan-1"
}
```

或：

```json
{
  "squad_id": "squad-delivery",
  "squad_version": 7,
  "graph_overrides": {"variables": {"coverage": 85}},
  "idempotency_key": "req-123-plan-1"
}
```

快捷入口使用 draft：

```text
POST /api/v1/requirements/{id}/execution-plan/quick-squad
POST /api/v1/squads/{id}/validate
POST /api/v1/squads/{id}/dry-run
POST /api/v1/requirements/{id}/execution-plan/publish
```

quick-squad 只创建草稿并返回 validation errors，不直接执行。publish 时将 Agent/Squad revision、graph、policy、context root 和工具 contract digest 冻结为 `RequirementExecutionPlan`。

## 6. 执行计划、状态机和双向反馈

### 6.1 Plan 和 Attempt

```go
type RequirementExecutionPlan struct {
    ID             string
    RequirementID  string
    WorkspaceID    string
    GraphSnapshot  WorkflowGraph
    SelectedRef    VersionedRef
    PolicySnapshot PolicySnapshot
    ContextRoot    ContextRef
    PlanHash       string
    Status         PlanStatus // draft, validating, ready, waiting, terminal
    Revision       int64
}

type NodeAttempt struct {
    ID              string
    PlanID          string
    NodeID          string
    AttemptNo       int
    RunID           string
    SessionID       string
    WorkDir         string
    Lease           Lease
    InputManifest   ContextEnvelope
    OutputArtifacts []ArtifactRef
    Result          StructuredResult
    Status          AttemptStatus // pending, ready, running, waiting, passed, failed, cancelled
    FailureReason   *FailureReason
    StartedAt       *time.Time
    FinishedAt      *time.Time
}

type FeedbackDecision struct {
    ID               string
    PlanID           string
    SourceAttempt    string
    SourceNode       string
    TargetNode       string
    EdgeID           string
    StructuredResult StructuredResult
    EvidenceIDs      []string
    Reason           string
    LoopCount        int
    IdempotencyKey   string
}
```

每个 attempt 的 `InputManifest`、provider session/workdir、Git baseline/head、artifact hash、实际退出码和 event sequence 都必须持久化。attempt 完成后由纯 reducer 计算下一状态，不能在 HTTP handler 中散落业务分支。

### 6.2 状态转换

```text
plan.draft -> validating -> ready -> running
node.pending -> ready -> running -> waiting|passed|failed|cancelled|timed_out
failed/bug/timeout -> feedback_deciding -> ready(new attempt)|human_wait|terminal_failed
all exit evidence committed -> terminal_succeeded
```

每次 transition 需要：`expected_plan_revision`、当前 attempt ID、lease/fencing token、idempotency key 和触发 event。状态不匹配返回 `stale_attempt` 或 `invalid_transition`，而不是自动纠正。

### 6.3 用户示例的精确图

```text
dev --success--> unit
unit --pass--> test
unit --failure(predicate=compile_or_unit_bug)--> dev
test --pass--> release_gate
test --bug(predicate=severity>=medium)--> dev
test --timeout--> human_review
dev --failure(predicate=needs_design)--> design
design --success--> dev
```

单测失败后，旧 unit attempt 进入 failed 并保存失败证据；feedback edge 创建新 dev attempt。新 dev attempt 成功后，图只允许将结果交给 unit；unit 通过后才可进入 test。测试 bug 回开发时，是否重新经过 unit 由边配置决定，默认必须经过 unit。每个回路都有 `max_traversals`、总预算、deadline 和人工出口。

### 6.4 调度器算法

1. 读取 plan snapshot 和当前 projection，找出所有前驱边已满足的节点。
2. 对每个 ready 节点校验能力、配额、权限和上下文预算；不能执行的节点进入 `blocked` 并写 reason code。
3. 用 `plan_id/node_id/attempt_no` 生成稳定 idempotency key，先写 attempt.started outbox，再调用 executor。
4. 收到 provider stream 时只追加事件和临时 projection；只有结构化 completion + evidence commit 后才生成 attempt.finished。
5. reducer 根据 on/predicate/priority 选择唯一 edge；多条同优先级命中必须报 `ambiguous_transition`，不能按 map 顺序猜。
6. 对 feedback edge 增加 loop counter、创建新 attempt，并将旧 attempt 设为 immutable。
7. merge 节点按 join policy 汇聚分支；任何分支失败是否短路由 graph 明确配置。

同一用户的两个需求必须拥有不同 plan/run/session/workdir；同一 plan 的并行节点可以共享只读 artifact，但写入必须使用独立目录和 artifact namespace。

## 7. Provider、Context 和 Memory

### 7.1 Provider command 必须携带完整 envelope

将现有 provider command 扩展为：

```go
type StartRunCommand struct {
    PlanID           string
    NodeID           string
    AttemptID        string
    SessionID        string
    WorkDir          string
    Input            string
    ContextEnvelope  harness.ContextEnvelope
    IdempotencyKey   string
    ExpectedRevision int64
}

type ContinuationCommand struct {
    PlanID            string
    NodeID            string
    AttemptID         string
    IssueID           string
    AgentID           string
    Input             string
    ContextEnvelope   harness.ContextEnvelope
    ExpectedSessionID string
    ExpectedWorkDir   string
    IdempotencyKey    string
}
```

`ContextEnvelope` 的 manifest、selection digest、replay key、block lineage、hard token budget、prompt manifest hash 和 semantic snapshot version 必须由 provider 校验。只给 `ContextID` 或将 manifest 拼进字符串的路径必须删除或标为 legacy，并在 CI 禁止新代码调用。

### 7.2 压缩和溢出

压缩器输入和输出都写 manifest：被压缩 block IDs、摘要算法/版本、源 hash、目标 token、丢弃原因、overflow reason 和新 replay key。超过预算时先执行确定性裁剪，再尝试压缩；两者失败则 `context_overflow` 等待人工或重新规划，不能默默截断系统指令。恢复按旧 semantic snapshot version 重建，不能读“当前最新记忆”替代历史。

### 7.3 记忆质量

沿用 ADRO 的 `AddMemory`/`TransitionMemory` 入口，但增加：evidence source hash、scope、敏感级别、污染 lineage、冲突包、embedding/lexical score、reviewer 和 TTL。生命周期至少为 `candidate -> quarantined -> confirmed -> superseded|forgotten|rejected`；未 confirmed 的事实不得进入稳定 system context。每次 promotion/reject/forget 产生 audit event 和可追溯 reason。

## 8. 评论区 @Agent/@Squad

### 8.1 语法和解析

采用稳定 URI，不使用显示名路由：

```markdown
[@方案设计](mention://agent/550e8400-e29b-41d4-a716-446655440000)
[@交付小队](mention://squad/6ba7b810-9dad-11d1-80b4-00c04fd430c8)
[@all](mention://all/all)
```

解析器输出 AST：`target_type`、`target_id`、字符范围、parser version、display text 和 source hash。无效 URI、跨 workspace、归档目标和超过数量上限必须保留评论但返回 blocked，不得按显示名猜测 Agent。

### 8.2 触发计算器

实现单一纯函数：

```go
func ComputeTriggers(ctx context.Context, in TriggerInput) (TriggerPlan, error)
```

输入包括 workspace、issue/requirement、comment、parent thread、editing comment ID、suppress agent IDs、当前用户权限、roster、pending tasks 和 runtime health。create、edit、preview 三个 API 必须调用同一个函数。

规则：

- 显式 Agent/Squad mention 优先于隐式 assignee/thread owner 路由；
- `@all` 抑制隐式路由，但不抑制显式 Agent/Squad；
- member/issue mention 只渲染，不触发执行；
- squad mention 先校验 Squad active/version，再按 leader/member policy 生成任务；
- 相同 comment、target、plan version、parser version 只允许一个有效触发；
- pending 相同任务返回 `coalesced`，等待运行时返回 `deferred`，权限/能力失败返回 `blocked`；
- 每个 outcome 记录 reason code、authority snapshot、dedupe key、source comment 和 parent task。

### 8.3 评论 API

```text
POST /api/v1/requirements/{id}/comments/trigger-preview
POST /api/v1/requirements/{id}/comments
PATCH /api/v1/comments/{id}                 # expected_revision, content_base
GET  /api/v1/comments/{id}/trigger-outcomes
POST /api/v1/comments/{id}/trigger-retry
```

创建/编辑响应示例：

```json
{
  "comment_id": "comment-1",
  "revision": 3,
  "trigger_outcomes": [
    {"target_type":"agent","target_id":"agent-dev","status":"queued","reason_code":"explicit_mention"},
    {"target_type":"squad","target_id":"squad-test","status":"blocked","reason_code":"runtime_unavailable"}
  ]
}
```

编辑评论必须使用 `expected_revision` 防止覆盖；内容变化时取消旧触发并重新计算。Agent 自己编辑自己的评论可以保留 lineage；其他用户编辑要重新校验权限并清理不再可信的 agent lineage。附件、截图和 comment action 使用同一 audit/correlation chain。

## 9. 持久化设计

第一阶段 SQLite 和 PostgreSQL 都实现同一 repository contract。以下是逻辑表，字段可用 sqlc 生成：

```text
agent_definitions(id, workspace_id, revision, name, role, spec_json,
                  status, created_by, created_at, updated_at,
                  UNIQUE(workspace_id, id, revision))
squad_definitions(id, workspace_id, revision, name, spec_json, status, ...)
squad_members(squad_id, squad_revision, member_id, agent_id, role, spec_json,
              UNIQUE(squad_id, squad_revision, member_id))
workflow_graphs(id, workspace_id, version, graph_json, validation_digest, ...)
execution_plans(id, requirement_id, workspace_id, plan_hash, snapshot_json,
                revision, status, idempotency_key, UNIQUE(workspace_id, idempotency_key))
workflow_nodes(plan_id, node_id, kind, status, projection_json, PRIMARY KEY(plan_id,node_id))
node_attempts(id, plan_id, node_id, attempt_no, run_id, session_id, workdir,
              status, input_manifest_json, result_json, lease_json,
              UNIQUE(plan_id,node_id,attempt_no))
workflow_edge_decisions(id, plan_id, source_attempt_id, edge_id, target_node_id,
                        predicate_json, evidence_json, loop_count, idempotency_key)
execution_events(event_id, workspace_id, plan_id, run_id, node_id, attempt_id,
                 sequence, event_type, payload_hash, previous_hash, envelope_hash,
                 idempotency_key, writer_id, fencing_token, committed_at,
                 UNIQUE(plan_id, idempotency_key), UNIQUE(plan_id, sequence))
comment_mentions(comment_id, target_type, target_id, parser_version,
                  authority_json, dedupe_key, outcome_json,
                  UNIQUE(comment_id,target_type,target_id,parser_version))
```

状态 projection 可以重建；业务事实只来自 immutable event、attempt、artifact 和审计记录。PostgreSQL profile 再加入 RLS、事务 outbox、advisory lock/lease 和备份恢复演练；SQLite profile 必须显式标记单机限制。

## 10. 运行时一致性、租约和副作用

### 10.1 写入顺序

对于 dispatch、tool call、transition、comment trigger 和 artifact：

1. 校验权限、revision、能力和幂等 payload hash；
2. 在 ledger/outbox 中写入 intent（包含 plan/node/attempt scope）；
3. 持有有效 lease/fencing 后执行外部副作用；
4. 写入结果、artifact hash 和 completion event；
5. reducer 更新 projection；
6. 最后发布对外 SSE/WebSocket/notification。

如果第 2、4 或 5 步失败，不能返回成功；重试依靠同一幂等键收敛。旧 lease 的结果必须拒绝，即使 provider 报告 completed。

### 10.2 工具 contract

从现有 `internal/runtime/kernel.go` 的 `AuthorizeTool/StartTool/ApproveTool/FinishTool` 延伸到 attempt scope。每个工具 contract 需要 input/output schema、side effect class、risk、capability、secret scope、network/filesystem policy、timeout、retry、cancellation、compensation 和 evidence policy。未授权、未 start、denied 或 open tool 时 finish turn 必须 fail-closed。

## 11. 可观测性和诊断

所有日志、指标和 trace 至少带：tenant、workspace、requirement、plan、graph version、node、attempt、session、provider、comment、correlation、causation、idempotency key 和 lease fencing。禁止把 prompt 原文或 Secret 写入日志。

必须提供：

- `GET /api/v1/plans/{id}/timeline`：按 sequence 展示节点、attempt、edge、evidence、阻塞原因；
- `GET /api/v1/runs/{id}/replay`：重建 projection 和 stream cursor；
- `GET /api/v1/runs/{id}/diagnostics`：能力、预算、lease、工具、上下文、artifact、失败分类；
- 指标：ready/running/waiting/blocked 数量、transition latency、retry/feedback 次数、loop exhaustion、context overflow、tool denial、duplicate/coalesced、event gap、lease conflict、成本和 token。

每个失败必须有稳定 `reason_code` 和人类可读说明；“Agent 返回失败”不是充分诊断。

## 12. 安全和权限

- 所有 Agent、Squad、plan、comment、attachment、event 和 workdir 按 workspace/tenant 隔离；所有 URL 参数再次做 scope 查询。
- 创建/发布 Squad 需要 manage 权限；触发 Agent/Squad 需要 invoke 权限；查看私有 context、artifact、日志和 replay 各自单独授权。
- Squad leader 不能获得成员没有的工具、Secret、网络或仓库权限。
- mention URI 只接受 roster 中的真实 UUID；显示名冲突、归档目标、跨 workspace 目标和无权限目标返回 blocked。
- Agent、插件和 executor 使用短时 capability token；Secret 通过 reference 读取，永不写入 prompt、事件或附件。
- 高风险工具、生产部署、不可逆副作用必须经过 human gate 或额外 policy；自动回退不能扩大权限。
- 审计记录 append-only，包含操作者、authority snapshot、旧/新 revision、decision、reason 和相关 artifact。

## 13. 迁移策略：从固定七阶段到 graph

### Phase 0：冻结现状

- 给现有 `PipelineStage`、`WorkflowStep`、`PipelineRun` 增加 `legacy_adapter_version` 标记；
- 建立 graph/plan/attempt repository 和 reducer 的纯单测；
- 不改变旧 API 响应，先把默认七阶段转换成等价 graph snapshot，记录 hash。

### Phase 1：双写但单读

- 创建 pipeline 时同时写 legacy run 和 execution plan；
- graph engine 只在 shadow mode 计算 next edge，与旧 `nextStage` 对比并记录差异；
- 差异必须可解释，不能直接改变用户行为。

### Phase 2：graph 读路径

- 新 execution-plan API 走 graph engine；
- 旧 pipeline API 通过 compat adapter 读取 graph projection；
- 禁止新增代码依赖 `PipelineStage`；静态检查或 review gate 发现即失败。

### Phase 3：真实反馈和 Squad

- 支持 Agent/Squad 选择、quick-squad draft、条件 edge、feedback、loop、join、human gate；
- 完成 A/B 图和真实 Codex 运行；
- 迁移旧 run 时只允许生成等价 graph，不自动推断缺失 edge。

### Phase 4：mention、重放和发布门禁

- 上线结构化 mention、preview、edit/retry/outcomes；
- 完成 comment→plan、plan→attempt、attempt→comment 的 correlation chain；
- GitHub Actions 执行全套 contract、真实 Codex/browser E2E 和故障注入。

旧七阶段兼容层只有在所有历史 run 归档、API deprecation 周期结束、projection/replay 校验通过后才能删除。

## 14. 测试实施清单

### 14.1 单元和属性测试

- graph validator：不可达节点、无界 cycle、schema 不匹配、重复 ID、权限不足、歧义 edge、预算超限；
- predicate evaluator：类型错误、深度/节点/时间上限、未知字段、all/any/not 组合；
- reducer：每种状态转换、重复 event、乱序 event、旧 attempt、错误 fencing、terminal 保护；
- scheduler：串行、并行、join all/quorum/first_success、容量不足、lease 过期、幂等冲突；
- feedback：单测失败回开发、开发后强制回单测、测试 bug 回开发、最大回路、人工出口；
- context：manifest hash、selection digest、hard budget、压缩 lineage、overflow recovery；
- memory：evidence、污染、冲突、lifecycle、scope、forget 和 supersede；
- mention：URI、Unicode、重复、跨 workspace、`@all`、suppress、编辑 revision、per-target outcomes。

### 14.2 Repository/契约测试

同一测试套分别运行 SQLite、PostgreSQL（未来 profile）和 file journal：事务回滚、唯一约束、outbox、重启加载、projection rebuild、backup/restore、TTL/retention 和大 payload。所有 provider、artifact、CI、notification 插件都运行同一 SPI conformance。

### 14.3 组合场景

1. A 需求：开发 Agent → 单测 Agent → 测试 Agent；单测失败两次后回开发，第三次通过，再进入测试。
2. B 需求：方案 Squad → 开发 Agent → 单测 Squad；方案评论中人类 @研发 Agent 询问，研发回复后继续同一 plan。
3. 测试发现高严重度 Bug：测试 → 开发 → 单测 → 测试；旧测试 attempt 结果不能覆盖新 attempt。
4. 一个需求选择 Agent，另一个需求选择包含同一 Agent 的 Squad，同时运行，session/workdir/memory/artifact 隔离。
5. Squad 内两个成员并行，一个超时，一个成功；join policy 分别验证 all、quorum、first_success。
6. 评论同时 @开发 Agent、测试 Squad 和 `@all`；显式目标触发，隐式路由被抑制，每个目标 outcome 独立。
7. 评论编辑后内容变化、附件替换、旧触发尚未执行；旧任务取消，新触发使用新 revision。
8. provider 在 attempt 完成后进程崩溃、事件重复、租约被抢占、磁盘写失败；恢复后不能推进错误 edge。

### 14.4 真实 E2E 和故障注入

必须启动真实 ADRO API、真实本地 Codex 可执行程序和真实浏览器，不得用 mock 输出代替：

```text
Requirement/Issue -> Agent/Squad -> execution plan -> session/workdir
-> real Codex stream -> attachment/artifact -> unit/test failure
-> feedback edge -> repair attempt -> rerun -> replay/audit
```

故障注入至少包括：executor kill、continuation 缺 thread.started、journal fsync 失败、outbox 重复、event gap/乱序、lease 抢占、context overflow、工具 approval denial、附件 hash 不一致、评论并发编辑、Redis/DB 不可用（仅在相应 profile 启用时）。没有真实 Codex 二进制或凭证时，CI 必须显示 `blocked`，不能标记 PASS。

## 15. CI、发布门禁和 DoD

### 15.1 Pull Request 门禁

```text
go test ./... -count=1 -p 1
go test -race ./... -count=1 -p 1
go vet ./...
go build ./...
git diff --check
go test ./internal/orchestration ./internal/mentions ./internal/context -count=1
npm run test:e2e:adro
```

`npm run test:e2e:adro` 必须是仓库真实存在的入口；入口缺失时 job 失败，不能用空脚本通过。真实 Codex、外部 adapter 和多副本 job 可以按环境拆分，但必须把证据 artifact 上传，并把 unavailable 明确标记为 blocked/deferred。

### 15.2 发布前必须具备的证据

- graph validator、reducer、repository、provider、mention、artifact、replay 的测试报告；
- A/B 图的真实 run timeline、ContextManifest、event replay、failure/repair attempt 和最终 artifact；
- 每个 mention target 的 authority snapshot 和 trigger outcome；
- 故障注入报告、恢复时间、丢失/重复事件结果和未覆盖风险；
- SBOM、许可证、签名、权限清单、备份恢复结果和回滚演练；
- 代码、数据库 migration、OpenAPI、UI、CLI 和测试计划版本一致。

### 15.3 Definition of Done

一个功能只有同时满足以下条件才算完成：

1. domain model、migration、API、UI/CLI、事件和审计都已实现；
2. 有正常、边界、并发、权限、故障和重放测试；
3. 真实 executor/browser 或明确 profile 的真实 adapter 已执行并保存证据；
4. 旧数据迁移、回滚、兼容和运维文档已验证；
5. 没有 `TODO`、隐式 stage、字符串 mention 路由或无界 retry；
6. reviewer 能从 plan hash 找到每个 attempt、edge、context、tool、artifact 和最终结论。

## 16. 给开发团队的首批任务拆分

1. `ORCH-001`：新增 orchestration model、repository、graph validator 和 predicate AST；不接执行器。
2. `ORCH-002`：实现 reducer、attempt、feedback、loop/join 和 event projection；完成纯状态机测试。
3. `ORCH-003`：把 LocalProvider 接入 plan/node/attempt 和完整 ContextEnvelope；补真实 capability 探测。
4. `ORCH-004`：实现 Agent/Squad CRUD、版本发布、quick-squad、validate/dry-run 和权限。
5. `ORCH-005`：实现 execution-plan API、旧 pipeline compat adapter 和 shadow comparison。
6. `ORCH-006`：实现 mention parser、roster resolver、trigger preview、create/edit/retry/outcomes。
7. `ORCH-007`：实现 replay/timeline/diagnostics、OTel 字段和审计 projection。
8. `ORCH-008`：补 SQLite/file profile 的故障注入，接入真实 Codex 和浏览器 E2E。
9. `ORCH-009`：接入 PostgreSQL/Redis/NATS 或等价生产 profile；在单机门禁通过后单独做多副本 conformance。

每个子任务必须在 PR 描述中填写：变更的 invariant、event schema、migration、测试命令、真实证据位置、已知 blocked/deferred 项和回滚步骤。

## 17. 诚实的起点和终点

按当前源码，ADRO 不能宣称已经拥有本规格的自由编排或评论 mention；这是需要开发的目标。现有 `PipelineStage`、`WorkflowStep`、`nextCustomStage`、`commentMentions` 和 `queueCommentFollowUp` 只能作为兼容和迁移起点。

完成本规格后，ADRO 的差异化应来自“可验证的有界图 + 强证据执行内核 + 可复用 Agent/Squad + 双向反馈 + 结构化评论触发”，而不是堆叠更多固定阶段。任何未通过真实执行、故障注入和发布门禁的能力，都必须在版本说明中标为 partial、blocked 或 deferred。
