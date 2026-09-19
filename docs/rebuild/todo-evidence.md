# Rebuild Todo Evidence Ledger

Updated: 2026-09-19.

This ledger mirrors every top-level checkbox in the authoritative rebuild TodoList attachment. All 451 entries remain open until their implementation, conformance, fault evidence, documentation, main-branch merge, and final acceptance conditions are satisfied. `evidenced` means repository evidence has been recorded; it does not mean the final issue acceptance gate is closed.

- Authoritative item count: **451**
- Source identity digest: `c1065cf81b5abe01266024367a74b8c58df1571e1f24e761ba26d7243d9bded6`
- Evidence tracking: **59 evidenced**, **118 partial**, **274 unverified**
- Checkbox rule: only final evidence review may change `[ ]` to `[x]`; deleting, merging, or renaming an item fails `scripts/verify-rebuild-ledger.py`.

## 1. 不可妥协的架构原则

- [ ] `P0-ARCH-001` [source:32] [state:unverified] 将 ADRO 的唯一主产品定义改为 `Durable Agent Runtime`，软件交付只是一个 example pack。

- [ ] `P0-ARCH-002` [source:35] [state:unverified] 保留 Go 作为主 Runtime 语言，暂不为了“高级感”重写 Rust。

- [ ] `P0-ARCH-003` [source:39] [state:unverified] 定义依赖方向并通过 CI 强制执行。

- [ ] `P0-ARCH-004` [source:45] [state:unverified] 所有能力必须回答四个问题：持久化什么、崩溃后如何恢复、权限在哪里判断、如何验证。

- [ ] `P0-ARCH-005` [source:47] [state:unverified] 禁止“只有接口没有实现却宣称支持”。

- [ ] `P0-ARCH-006` [source:51] [state:unverified] 一个事实只有一个 authoritative source；其他视图必须是可重建 projection。

- [ ] `P0-ARCH-007` [source:53] [state:unverified] 默认 fail-closed；任何降级为 full access、无审批或非持久模式都必须显式开启并记录审计事件。

- [ ] `P0-ARCH-008` [source:55] [state:unverified] 每个公开能力必须同时具备：协议、reference implementation、conformance test、故障测试、文档示例。

- [ ] `P0-ARCH-009` [source:57] [state:unverified] 新功能不能直接进入巨型文件。

- [ ] `P0-ARCH-010` [source:60] [state:unverified] WebUI 是 Runtime Inspector，不是架构真相来源；所有操作必须经过公开 API。

- [ ] `P0-ARCH-011` [source:62] [state:unverified] 建立 clean-room 来源规则；设计项只能标记为 `ADRO-origin`、`public-standard-derived` 或 `independent-design`。

- [ ] `P0-ARCH-012` [source:65] [state:partial] 禁止在公开代码、注释、文档、示例、测试夹具、标识符、UI 文案、API Schema、提交信息、变更日志和发布说明中出现竞争项目名称。
  - Recorded evidence state: `partial`. `scripts/verify-public-identity.py` checks tracked public text without printing forbidden values, with negative tests and optional private denylist; full historical scan, private policy and naming cleanup remain pending

- [ ] `P0-ARCH-013` [source:68] [state:partial] 建立“能力声明证据矩阵”，每项能力绑定实现符号、conformance、fault test、文档和稳定性等级。
  - Recorded evidence state: `partial`. `docs/rebuild/capability-evidence.md` records capability maturity and missing gates; `docs/rebuild/todo-evidence.md` mirrors all 451 authoritative checklist items with deletion-resistant source identity verification

- [ ] `P0-ARCH-014` [source:70] [state:unverified] 核心 Runtime 不嵌入通用事件/插件内核；生命周期、依赖、事件和扩展边界由 ADRO 的窄接口显式实现。

## 2.1 建立可回退基线

- [ ] `P0-BASE-001` [source:78] [state:evidenced] 给当前状态打只读 tag，例如 `legacy-delivery-control-plane`。
  - Recorded evidence state: `complete locally`. Annotated tag `legacy-delivery-control-plane-20260917` at `2d480a87f0ecdf974ab09cdece2729f209a8e813`

- [ ] `P0-BASE-002` [source:79] [state:partial] 保存当前数据库、workspace bundle、OpenAPI 和浏览器截图 fixture。
  - Recorded evidence state: `partial`. Git tag freezes database migrations, workspace fixtures, OpenAPI and tracked browser assets; generated screenshot fixture still pending

- [ ] `P0-BASE-003` [source:80] [state:evidenced] 记录当前 `go test ./...`、race、Playwright、fault matrix、benchmark 基线。
  - Recorded evidence state: `complete`. `docs/rebuild/baseline-report.md` records exact-tag serial test, race, fault, benchmark discovery and isolated browser reruns

- [ ] `P0-BASE-004` [source:81] [state:evidenced] 建立 `docs/rebuild/decision-log.md`，每次删除能力都记录原因和替代路径。
  - Recorded evidence state: `complete`. `docs/rebuild/decision-log.md`

## 2.2 从核心移出的业务功能

- [ ] `P0-TRIM-001` [source:85] [state:unverified] 将 `internal/pipeline` 的固定七阶段流程移动到 `examples/software-delivery`。

- [ ] `P0-TRIM-002` [source:89] [state:unverified] 将 `internal/compat/pipeline_stage` 移入 example compatibility adapter，核心不再识别业务阶段。

- [ ] `P0-TRIM-003` [source:91] [state:unverified] 将 Requirement、Bug、Comment、Release Evidence 从核心领域对象降为 example/application 对象。

- [ ] `P1-TRIM-004` [source:94] [state:unverified] 将 workspace ZIP/PostgreSQL 业务迁移工具移到 `tools/workspace-migrate`，不参与 Runtime 主调用链。

- [ ] `P1-TRIM-005` [source:96] [state:unverified] 删除与 Runtime 学习无关的业务 CRUD、菜单权限和交付状态枚举。

- [ ] `P1-TRIM-006` [source:98] [state:unverified] 将 Git/CI/Deploy 集成降为 `examples/software-delivery/adapters`。

- [ ] `P1-TRIM-007` [source:100] [state:unverified] 清理文档中“生产可用”但仓库未交付 adapter 的表述。

## 2.3 保留但重写组织方式

- [ ] `P0-TRIM-008` [source:104] [state:unverified] 保留现有测试语义，先建立 golden/conformance，再移动代码。

- [ ] `P0-TRIM-009` [source:105] [state:unverified] 保留 Agent、Squad、Graph、Chat 页面可复用交互，但重命名为 Runtime 概念。

- [ ] `P0-TRIM-010` [source:106] [state:partial] 保留 Apache-2.0、SBOM、第三方许可证、Security、Governance、Release 门禁。
  - Recorded evidence state: `partial`. SPDX SBOM, dependency notices, license copies and supply-chain verification are reproducible from the manifests; full security, governance and release gates remain pending

## 3.0 显式生命周期系统

- [ ] `P0-LIFE-001` [source:136] [state:evidenced] 定义 `Component`：`Name`、`Dependencies`、`Start(ctx)`、`Stop(ctx)`、`Health(ctx)`。
  - Recorded evidence state: `complete`. Lifecycle component contract

- [ ] `P0-LIFE-002` [source:137] [state:evidenced] 启动前验证依赖图不存在缺失节点和环，并生成稳定的拓扑顺序。
  - Recorded evidence state: `complete`. Missing dependency, duplicate and cycle validation with stable order

- [ ] `P0-LIFE-003` [source:138] [state:evidenced] 按拓扑顺序启动、逆序停止；中途启动失败时只回滚已成功启动的组件。
  - Recorded evidence state: `complete`. Topological start, reverse stop and partial-start rollback

- [ ] `P0-LIFE-004` [source:139] [state:unverified] 每个 goroutine、连接、临时目录、监听器和 worker pool 必须有唯一 owner，禁止无主后台任务。

- [ ] `P0-LIFE-005` [source:140] [state:partial] 使用 structured concurrency；父 context 取消必须有界传播到模型流、工具、子 Agent 和存储 worker。
  - Recorded evidence state: `partial`. Root cancellation propagation is implemented; model/tool/sub-agent integration pending

- [ ] `P0-LIFE-006` [source:141] [state:evidenced] 每个组件暴露 readiness、liveness、degraded reason 和最近状态变化时间。
  - Recorded evidence state: `complete locally for reference manager`. `Snapshot`, `Readiness` and `Liveness` derive independent probe state, reason and change time from component health in `runtime/lifecycle/manager.go`; production API wiring remains pending

- [ ] `P0-LIFE-007` [source:142] [state:unverified] 禁止环境变量、全局注册表或包级单例在构造完成后隐式改变组件行为。

- [ ] `P0-LIFE-008` [source:143] [state:unverified] 启停和健康变化写入结构化事件与 trace，但健康事件不能成为业务事实来源。

- [ ] `P0-LIFE-009` [source:144] [state:partial] 为部分启动失败、重复停止、超时停止、goroutine 泄漏和资源回收建立 conformance tests。
  - Recorded evidence state: `partial`. Core lifecycle conformance covers startup/shutdown timeout, rollback, repeated stop, aggregated errors and owned-worker cancellation; socket/file-descriptor leak tests and production integration remain pending

## 3.1 统一类型系统

- [ ] `P0-CORE-001` [source:148] [state:evidenced] 定义稳定 ID：Tenant、Workspace、Agent、Session、Turn、Step、Effect、ToolCall、Approval、Checkpoint、Event。
  - Recorded evidence state: `complete`. `core/ids` typed IDs and validation

- [ ] `P0-CORE-002` [source:149] [state:partial] 定义 `Command -> Events -> State` reducer，不允许 handler 直接修改 projection。
  - Recorded evidence state: `partial`. Generic reducer/replay contract exists; legacy handlers are not migrated

- [ ] `P0-CORE-003` [source:150] [state:partial] 定义统一 `EventEnvelope`：sequence、schema version、event ID、causation、correlation、actor、tenant、integrity。
  - Recorded evidence state: `partial`. Canonical envelope v1 exists; the legacy runtime journal now maps into it in shadow mode, while other producers remain pending

- [ ] `P0-CORE-004` [source:151] [state:partial] 将 Runtime、Harness、Orchestration、Audit 当前事件映射到统一 envelope。
  - Recorded evidence state: `partial`. Runtime journal mapping and restart backfill exist in `internal/runtime/shadow.go`; Harness, Orchestration and Audit mappings remain pending

- [ ] `P0-CORE-005` [source:152] [state:unverified] 明确哪些事件包含内容，哪些只保存 digest 与外部 blob reference。

- [ ] `P0-CORE-006` [source:153] [state:partial] 定义 canonical serialization，hash 不依赖 map 遍历顺序。
  - Recorded evidence state: `partial`. Canonical JSON v1, golden digest and fuzz smoke

- [ ] `P0-CORE-007` [source:154] [state:partial] 定义 schema upcaster、unknown-field、downgrade 和 migration policy。
  - Recorded evidence state: `partial`. `core/event/registry.go` provides adjacent explicit upcasters, reject/preserve unknown-field policy, future-version rejection and downgrade blocking; N-2 fixtures and producer migrations remain pending

- [ ] `P0-CORE-008` [source:155] [state:partial] 为所有状态机生成状态转移表和非法转换测试。
  - Recorded evidence state: `partial`. Session/Turn/Step transition tables and illegal-transition tests exist in `internal/runtime/lifecycle_state.go`; ModelCall, Approval, Timer and Delegation transition tables remain pending

## 3.2 消除多套事实来源

- [ ] `P0-CORE-009` [source:159] [state:partial] 合并/分工现有五套记录：
  - Recorded evidence state: `partial`. `docs/rebuild/event-source-inventory.md`; the runtime journal has a legacy-authoritative EventStore shadow, while source cutover and the other producers remain pending

- [ ] `P0-CORE-010` [source:166] [state:partial] 所有 projection 支持从 sequence 0 重建并比较 digest。
  - Recorded evidence state: `partial`. `core/event.ValidateChain` and `core/reducer.ReplayVerified` produce verified replay/state-digest evidence; runtime shadow projections rebuild from sequence zero, while projection workers and other views remain pending

- [ ] `P0-CORE-011` [source:167] [state:unverified] 删除无法重建或与 authoritative stream 冲突的 snapshot 字段。

- [ ] `P0-CORE-012` [source:168] [state:unverified] snapshot 只作为加速缓存；损坏时可从 event log 重建。

## 3.3 确定性基础设施与配置快照

- [ ] `P0-CORE-013` [source:172] [state:partial] 在 core/runtime 注入 `Clock`、`IDGenerator`、`Random`、`Sleeper` 和 `BackoffPolicy`，reducer 内禁止直接调用系统时钟或随机源。
  - Recorded evidence state: `partial`. Dependency interfaces and deterministic testkit exist; legacy reducers still call system sources

- [ ] `P0-CORE-014` [source:173] [state:partial] 测试使用虚拟时钟、序列 ID 和固定随机 seed，可无真实等待地验证 lease、retry、deadline 和 jitter。
  - Recorded evidence state: `partial`. Manual clock, sequence IDs/random and recording sleeper tests

- [ ] `P0-CORE-015` [source:174] [state:partial] 定义 canonical encoding 规范并固定版本；语义等价的 map、数字、时间和 Unicode 输入必须产生相同 digest。
  - Recorded evidence state: `partial`. Canonical map/number/Unicode encoding tests; cross-process fixtures pending

- [ ] `P0-CORE-016` [source:175] [state:partial] event、snapshot、blob 和 manifest 显式保存 `encoding_version`、`hash_algorithm` 与 `hash_version`。
  - Recorded evidence state: `partial`. Event envelope carries encoding and hash identities; snapshot/blob/manifest migration pending

- [ ] `P0-CORE-017` [source:176] [state:partial] reducer 只能消费 command、当前 state 与显式 dependencies；相同输入必须产生逐字节相同的 events。
  - Recorded evidence state: `partial`. Reducer byte-determinism test exists; runtime state machines pending

- [ ] `P0-CORE-018` [source:177] [state:partial] 每个 Turn/Plan 冻结不可变 `ConfigSnapshot`，包含 config version、digest、feature gates 和 adapter identities。
  - Recorded evidence state: `partial`. Turn and Step reference lifecycle freeze canonical config, adapter/protocol, policy, tokenizer, context/tool/policy digests with tamper tests; ExecutionPlan and production engine binding remain pending

- [ ] `P0-CORE-019` [source:178] [state:partial] replay 使用历史配置快照；运行中的全局配置变更只能影响下一边界，不能改变已提交 step。
  - Recorded evidence state: `partial`. Restart replay retains historical Turn/Step snapshot digests; hot-update boundary integration and authoritative EventStore cutover remain pending

- [ ] `P0-CORE-020` [source:179] [state:unverified] 配置解析、默认值展开、secret reference 解析和 capability negotiation 的结果都必须可审计且可重建。

## 3.4 拆分巨型模块

- [ ] `P0-SPLIT-001` [source:183] [state:unverified] 拆分 `internal/harness/store.go`：session、transcript、checkpoint、compaction、memory、lease、outbox、recovery。

- [ ] `P0-SPLIT-002` [source:184] [state:unverified] 拆分 `internal/provider/local.go`：process lifecycle、runtime protocol、session continuation、tool parsing、usage、worktree、persistence。

- [ ] `P0-SPLIT-003` [source:185] [state:unverified] 拆分 `internal/api/server.go`：route registration、middleware、resource handlers、streaming、diagnostics。

- [ ] `P0-SPLIT-004` [source:186] [state:unverified] 将 `apps/web/enhancements.js` 改为 TypeScript 模块，按页面和领域拆分。

- [ ] `P0-SPLIT-005` [source:187] [state:unverified] 将 WebUI 内联 CSS/JS 移出 `index.html`，建立明确 build 与 CSP。

## 4.1 生命周期

- [ ] `P0-LOOP-001` [source:195] [state:partial] 建立统一状态机：`Session -> Turn -> Step -> ModelCall/Effects -> Checkpoint -> Terminal`。
  - Recorded evidence state: `partial`. Reference Session/Turn/Step hierarchy, snapshots, checkpoint boundaries, parent/child settlement and restart replay exist in `internal/runtime/lifecycle_state.go`; pure reducer/EventStore/RuntimeEngine integration remains pending

- [ ] `P0-LOOP-002` [source:196] [state:partial] 每个 step 在模型调用前持久化 `StepContextFrozen`。
  - Recorded evidence state: `partial`. Reference Step rejects model request commit before durable `step.context_frozen` and replay verifies the frozen digest; production model dispatch path is not yet cut over

- [ ] `P0-LOOP-003` [source:197] [state:partial] 模型请求必须能完全由 committed events 重建并逐字节比较。
  - Recorded evidence state: `partial`. `ModelRequest` canonical prompt/config/context/policy digest and Journal replay tests exist; all provider adapters are not migrated

- [ ] `P0-LOOP-004` [source:198] [state:partial] 明确 stop 原因：completed、max steps、budget exhausted、cancelled、policy denied、provider failed、suspended。
  - Recorded evidence state: `partial`. Closed `StopReason` vocabulary and terminal-category validation cover reference Session/Turn/Step transitions; full engine error taxonomy and UI/API mapping remain pending

- [ ] `P0-LOOP-005` [source:199] [state:unverified] 支持用户 steering，但 steering 只在明确 step boundary 生效并持久化。

- [ ] `P0-LOOP-006` [source:200] [state:unverified] 支持 cancellation propagation，模型流、工具、子 Agent 和 sandbox 都必须响应。

- [ ] `P0-LOOP-007` [source:201] [state:unverified] 支持暂停/恢复与 session fork；fork 保存 lineage 和不可变父边界。

- [ ] `P1-LOOP-008` [source:202] [state:unverified] 支持 structured output schema 与校验失败修复回合。

- [ ] `P0-LOOP-009` [source:203] [state:partial] 每个模型请求在发送前持久化 request digest、adapter identity、attempt、idempotency key 和 config snapshot。
  - Recorded evidence state: `partial`. `ModelRequest` freezes request digest, adapter identity, attempt and idempotency key before dispatch; config snapshot persistence is still a contract-level digest

- [ ] `P0-LOOP-010` [source:204] [state:partial] 请求已发送但没有终态时进入 `MODEL_OUTCOME_UNKNOWN`，只能 resume、query 或人工裁决，不能盲目重发可能产生收费或远端状态的请求。
  - Recorded evidence state: `partial`. `model.outcome_unknown` blocks late stream frames and redispatch; provider query/human recovery implementation remains pending

- [ ] `P0-LOOP-011` [source:205] [state:partial] step reducer 明确区分模型传输失败、模型拒绝、流中断、输出无效和取消。
  - Recorded evidence state: `partial`. Model event validation distinguishes stream frames, finish and provider error; full step reducer and cancellation taxonomy remain pending

- [ ] `P0-LOOP-012` [source:206] [state:partial] 每个停止条件都生成稳定 reason code；不得仅依赖错误字符串判断恢复策略。
  - Recorded evidence state: `partial`. Reference lifecycle persists stable stop reason codes and rejects unknown/category-invalid values; remaining runtime call sites still need migration from error-string branching

- [ ] `P0-LOOP-013` [source:207] [state:partial] Turn/Step 读取不可变 policy、config、tool catalog 和 context snapshot，热更新在下一安全边界生效。
  - Recorded evidence state: `partial`. Turn freezes config and Step freezes config/context/tool/policy identities with canonical digests; hot-update and production adapter integration remain pending

- [ ] `P0-LOOP-014` [source:208] [state:unverified] 长任务恢复必须证明 continuation identity；无法证明时暂停而不是创建隐式新会话。

## 4.2 Model Gateway

- [ ] `P0-MODEL-001` [source:212] [state:partial] 定义统一 streaming event：text、reasoning metadata、tool request、usage、finish、provider error。
  - Recorded evidence state: `partial`. Versioned `ModelEvent` types and cursor validation in `internal/runtime/model.go`; adapter migration and API wire compatibility remain pending

- [ ] `P0-MODEL-002` [source:213] [state:unverified] 将现有具体模型执行适配器拆成独立 adapter package，核心只依赖统一模型协议。

- [ ] `P0-MODEL-003` [source:214] [state:evidenced] 模型能力通过 negotiation 暴露：tools、images、structured output、reasoning、continuation、streaming。
  - Recorded evidence state: `complete locally for reference contract`. `ProviderCapabilities` validates protocol/model/features fail-closed with tests

- [ ] `P0-MODEL-004` [source:215] [state:partial] 实现 provider retry 分类：transport、rate limit、server、invalid request、context overflow、auth。
  - Recorded evidence state: `partial`. `internal/runtime/model_retry.go` defines stable failure classes and explicit dispatch acceptance rules; provider adapter emission remains pending

- [ ] `P0-MODEL-005` [source:216] [state:partial] 支持指数退避、jitter、`Retry-After`、最大累计等待和持久 retry event。
  - Recorded evidence state: `partial`. Bounded deterministic backoff, Retry-After handling and durable `model.retry` TimerSpec exist; authoritative retry event/provider wiring remains pending

- [ ] `P0-MODEL-006` [source:217] [state:partial] 支持路由、fallback 和 circuit breaker，但禁止在有未知副作用后切换并重放。
  - Recorded evidence state: `partial`. Deterministic route evidence, historical route reuse, circuit breaker and unknown-dispatch fallback guard exist; live adapter pool integration remains pending

- [ ] `P1-MODEL-007` [source:218] [state:unverified] 建立 prompt cache identity 与 context hash，统计 cache hit/miss。

- [ ] `P1-MODEL-008` [source:219] [state:unverified] 统一 token、费用、延迟、首 token 时间和 retry cost。

- [ ] `P1-MODEL-009` [source:220] [state:unverified] 提供 deterministic fake model 与 record/replay model adapter。

- [ ] `P0-MODEL-010` [source:221] [state:evidenced] 定义 `ModelRequest`、`ModelEvent`、`ProviderCapabilities`、`ContinuationToken` 和 `Usage`，移除业务工单命名。
  - Recorded evidence state: `complete locally for reference contract`. `ModelRequest`, `ModelEvent`, `ProviderCapabilities`, `ContinuationToken` and `Usage` are defined and tested

- [ ] `P0-MODEL-011` [source:222] [state:evidenced] request digest 与 provider idempotency key 一一绑定；相同 key 不同 payload 必须拒绝。
  - Recorded evidence state: `complete locally for reference contract`. Canonical request digest and idempotency conflict tests

- [ ] `P0-MODEL-012` [source:223] [state:partial] 路由决策记录候选集、健康、限流、成本、能力和最终原因；replay 不重新计算历史路由。
  - Recorded evidence state: `partial`. `ModelRouteDecision` records candidate health, rate limit, capabilities, score and reason; API/event persistence remains pending

- [ ] `P0-MODEL-013` [source:224] [state:partial] provider health、限流和 circuit breaker 有独立状态机，half-open probe 不占用正常 session 配额。
  - Recorded evidence state: `partial`. `ModelCircuitBreaker` implements closed/open/half-open with one probe; provider health/admission composition remains pending

- [ ] `P0-MODEL-014` [source:225] [state:partial] fallback 只能发生在未 dispatch 或已证明无远端效果的失败后；未知结果禁止跨 provider 重放。
  - Recorded evidence state: `partial`. Retry and route replay refuse fallback after an unproven dispatch; provider query/reconcile wiring remains pending

- [ ] `P0-MODEL-015` [source:226] [state:evidenced] capability negotiation 与 API 版本协商失败时 fail-closed，不按 adapter 名称猜能力。
  - Recorded evidence state: `complete locally for reference contract`. Incompatible protocol/model/capability negotiation is rejected without adapter-name inference

- [ ] `P1-MODEL-016` [source:227] [state:unverified] 支持可替换的 prompt caching adapter，但缓存 key 必须包含模型、tokenizer、tool catalog、policy 与 manifest digest。

## 4.3 Streaming 与背压

- [ ] `P0-STREAM-001` [source:231] [state:partial] 定义版本化 `ModelEvent` 序列和单调 `StreamCursor`，支持断线 resume 与重复事件去重。
  - Recorded evidence state: `partial`. Monotonic event sequence and cursor with replay/gap tests; persistent stream adapter and all transport mappings remain pending

- [ ] `P0-STREAM-002` [source:232] [state:evidenced] 每条流使用有界 buffer；满载时阻塞、降采样或断开必须由显式策略决定。
  - Recorded evidence state: `complete locally for reference stream`. Bounded capacity and explicit block/drop/disconnect policies are implemented and tested

- [ ] `P0-STREAM-003` [source:233] [state:evidenced] 慢消费者超过 retention window 时返回结构化 gap，客户端必须走补偿查询而不是静默跳过。
  - Recorded evidence state: `complete locally for reference stream`. Retention and dropped-optional-delta conditions return structured gap errors

- [ ] `P0-STREAM-004` [source:234] [state:partial] chunk 边界不能切断 UTF-8 code point、结构化参数或工具调用帧。
  - Recorded evidence state: `partial`. UTF-8 and complete canonical tool argument validation exists; provider chunk assembler integration remains pending

- [ ] `P0-STREAM-005` [source:235] [state:evidenced] partial text、partial tool arguments 和 usage delta 采用不同事件类型，只有完整 frame 才能进入执行。
  - Recorded evidence state: `complete locally for reference contract`. Distinct text/reasoning/tool/usage/finish/error frame types and terminal validation

- [ ] `P0-STREAM-006` [source:236] [state:unverified] 重要 partial event 可批量持久化，批次边界、fsync 策略和允许丢失窗口必须公开。

- [ ] `P0-STREAM-007` [source:237] [state:partial] cancellation 关闭上游流、持久化终止原因并释放 buffer；禁止 goroutine 和 socket 泄漏。
  - Recorded evidence state: `partial`. Context cancellation is available at gateway boundary; adapter socket/goroutine cleanup evidence remains pending

- [ ] `P0-STREAM-008` [source:238] [state:unverified] WebSocket/SSE 都映射到同一 stream contract，不维护不同业务语义。

- [ ] `P0-STREAM-009` [source:239] [state:partial] 建立 slow consumer、断线重连、重复 chunk、乱序、截断 JSON、超大输出和取消风暴测试。
  - Recorded evidence state: `partial`. Reference tests cover retention gap, optional drop, overflow disconnect, duplicate/out-of-order rejection; provider matrix remains pending

- [ ] `P0-STREAM-010` [source:240] [state:unverified] 暴露 buffer occupancy、dropped optional deltas、resume count、gap count 和 consumer lag 指标。

## 5.1 Tool Contract

- [ ] `P0-TOOL-001` [source:248] [state:evidenced] 定义版本化 Tool Schema：name、input/output schema、capabilities、side-effect class、timeout、concurrency mode。
  - Recorded evidence state: `complete locally for reference contract`. Versioned `ToolContract` freezes name, schemas, capabilities, effect class, limits and concurrency mode in `internal/runtime/tool_contract.go` and `internal/runtime/kernel.go`

- [ ] `P0-TOOL-002` [source:249] [state:evidenced] 区分 `read_only`、`idempotent_write`、`reconcilable_write`、`non_retriable_write`。
  - Recorded evidence state: `complete locally for reference contract`. Effect classes and explicit reconciliation policy validation in `FreezeToolContract`; write retries fail closed

- [ ] `P0-TOOL-003` [source:250] [state:evidenced] 工具调用前冻结不可变 contract，执行中不能被插件热更新改变。
  - Recorded evidence state: `complete locally for reference contract`. Canonical frozen contract digest is stored in `tool.authorized`; digest changes conflict during a call

- [ ] `P0-TOOL-004` [source:251] [state:evidenced] 输入和输出做 schema 校验、大小限制、敏感字段分类和 digest。
  - Recorded evidence state: `complete locally for reference contract`. Bounded JSON Schema subset, byte limits, field classifications and canonical payload digests with negative tests

- [ ] `P0-TOOL-005` [source:252] [state:evidenced] 实现 bounded rolling pool、parallel-safe 和 exclusive barrier。
  - Recorded evidence state: `complete locally for reference executor`. `ToolLoop.RunBatch` uses a positive fixed worker bound with `parallel_safe` groups and exclusive barriers

- [ ] `P0-TOOL-006` [source:253] [state:evidenced] 无论并发完成顺序如何，结果按模型请求顺序写回。
  - Recorded evidence state: `complete locally for reference executor`. Batch results are returned by model request index regardless of callback completion order

- [ ] `P0-TOOL-007` [source:254] [state:evidenced] abort 时为未启动调用写入明确 `not_started`，不能伪装失败或成功。
  - Recorded evidence state: `complete locally for reference executor`. Cancellation persists `tool.not_started` for calls that never dispatch; dispatched calls retain receipt/unknown semantics

## 5.2 Durable Effect Protocol

- [ ] `P0-EFFECT-001` [source:258] [state:partial] 实现 `Intent -> Approved -> Dispatched -> Receipted | OutcomeUnknown -> Reconciled`。
  - Recorded evidence state: `partial`. Intent, prepare, dispatch, receipt, unknown-outcome and policy-valid reconciled facts in `internal/runtime/kernel.go`; external adapter reconcile implementations remain pending

- [ ] `P0-EFFECT-002` [source:259] [state:evidenced] 任何写工具执行前必须 durable commit `EffectIntent`。
  - Recorded evidence state: `complete for legacy ToolLoop`. Intent commits before `tool.started` and callback dispatch; transition APIs require positive lease fencing

- [ ] `P0-EFFECT-003` [source:260] [state:partial] effect ID、input digest、idempotency key、lease epoch 一并持久化。
  - Recorded evidence state: `partial`. Effect ID, input digest, class, explicit reconcile policy and fence are durable; adapter idempotency key contract pending

- [ ] `P0-EFFECT-004` [source:261] [state:unverified] 过期 fencing token 永远不能写 receipt。

- [ ] `P0-EFFECT-005` [source:262] [state:evidenced] 已 dispatch 未 receipt 的 effect 恢复为 `OUTCOME_UNKNOWN`，禁止自动重试。
  - Recorded evidence state: `complete for legacy ToolLoop`. Dispatched writes without receipts return `ErrEffectOutcomeUnknown` and are not replayed

- [ ] `P0-EFFECT-006` [source:263] [state:partial] 每个写工具必须提供 reconcile policy：query、compensate、human decision 或不可恢复。
  - Recorded evidence state: `partial`. Journal enforces query/compensate/human/unrecoverable decisions and idempotent resolution; concrete external reconcile adapters remain pending

- [ ] `P0-EFFECT-007` [source:264] [state:partial] terminal event 与最终 checkpoint 同事务提交。
  - Recorded evidence state: `partial`. Effect receipt and tool terminal event commit atomically; final checkpoint integration pending

- [ ] `P0-EFFECT-008` [source:265] [state:unverified] 加入支付、评论、Git commit、文件写入四类故障示例。

## 5.3 Approval 与 Policy

- [ ] `P0-POLICY-001` [source:269] [state:partial] 定义 capability-based policy，不按工具名称硬编码权限。
  - Recorded evidence state: `partial`. `core/policy` evaluates declared capabilities rather than adapter/tool names; migration of every dispatch path remains pending

- [ ] `P0-POLICY-002` [source:270] [state:partial] `approval/asked` 与 `approval/decided` 必须成对持久化。
  - Recorded evidence state: `partial`. New runtime approvals persist paired `approval.asked` and `approval.decided` events; legacy approval APIs and tool authorization paths still need migration

- [ ] `P0-POLICY-003` [source:271] [state:partial] 无 answerer、answerer 崩溃或返回未知值时 fail-closed。
  - Recorded evidence state: `partial`. Missing responders converge on durable timeout, actor/schema/version errors fail closed, and restart preserves pending requests; production deadline worker composition remains pending

- [ ] `P0-POLICY-004` [source:272] [state:partial] 子 Agent 权限只能继承或收缩，不能扩张。
  - Recorded evidence state: `partial`. `core/policy.ValidateChild` rejects tenant/workspace changes, added capabilities/destinations and higher sensitivity; orchestration delegation wiring remains pending

- [ ] `P0-POLICY-005` [source:273] [state:unverified] policy 版本和 decision evidence 进入 step snapshot。

- [ ] `P1-POLICY-006` [source:274] [state:unverified] 支持 session policy override，但必须受 tenant ceiling 限制。

- [ ] `P0-POLICY-007` [source:275] [state:partial] 每次决策持久化规范化 input digest、output、policy bundle digest、engine version 和 evaluation timestamp。
  - Recorded evidence state: `partial`. Canonical decision records bind scope, normalized input digest, outcome/reason, bundle digest, engine version and timestamp; `adapters/policy/eventstore` persists and idempotently reads them; production composition remains pending

- [ ] `P0-POLICY-008` [source:276] [state:partial] replay 默认读取历史 decision record；审计模式可用固定 policy bundle 复算并报告 divergence。
  - Recorded evidence state: `partial`. Historical `Replay` avoids current-policy evaluation and `Audit` reports recomputation divergence; persisted audit APIs remain pending

- [ ] `P0-POLICY-009` [source:277] [state:partial] 指令与数据分离；用户内容、检索内容、工具输出和网页内容携带 taint/trust labels。
  - Recorded evidence state: `partial`. Typed context provenance separates trusted instructions from tainted user/retrieved/tool/model data; provider/tool migration remains pending

- [ ] `P0-POLICY-010` [source:278] [state:partial] 数据外发在 dispatch 前执行 destination、sensitivity、tenant 和 purpose policy，不依赖 prompt 自我约束。
  - Recorded evidence state: `partial`. Extension dispatch evaluates tenant/workspace, capability, exact destination, purpose and sensitivity and persists the decision before dispatch; model/tool/MCP/HTTP integration remains pending

- [ ] `P0-POLICY-011` [source:279] [state:partial] policy engine 可内置或外接，但超时、不可达、版本不匹配和无结果都必须 fail-closed。
  - Recorded evidence state: `partial`. Evaluator errors, timeouts, malformed results and engine-version mismatch produce durable deny outcomes; external engine conformance remains pending

## 5.3.1 Human Interaction

- [ ] `P0-HUMAN-001` [source:283] [state:evidenced] 将通用人在回路输入与高风险 Approval 分离，定义 question、choice、freeform、artifact review、takeover。
  - Recorded evidence state: `complete locally for reference state machine`. `HumanInteractionRequest` is separate from `ApprovalRequest` and supports question, choice, freeform, artifact review and takeover kinds

- [ ] `P0-HUMAN-002` [source:284] [state:evidenced] 每个请求持久化 schema、deadline、eligible actors、claim policy、context digest 和 sensitivity。
  - Recorded evidence state: `complete locally for reference state machine`. Request events freeze schema, deadline, eligible actors, claim policy, context digest, sensitivity, version and definition digest

- [ ] `P0-HUMAN-003` [source:285] [state:evidenced] response 需要 actor identity、request version、idempotency key 和 schema validation。
  - Recorded evidence state: `complete locally for reference state machine`. Responses bind a verified actor, request version, idempotency key, canonical payload digest and bounded schema validation

- [ ] `P0-HUMAN-004` [source:286] [state:evidenced] 超时、撤回、重复回答、过期回答、并发 claim 和 takeover 都有确定状态机。
  - Recorded evidence state: `complete locally for reference state machine`. Deterministic tests cover timeout, withdrawal, duplicate/conflicting answers, concurrent claim, approved takeover and displaced claimants

- [ ] `P0-HUMAN-005` [source:287] [state:evidenced] 人工输入进入下一安全 step boundary，不得在模型流或 effect dispatch 中途隐式改变冻结上下文。
  - Recorded evidence state: `complete locally for reference state machine`. A response can be applied only to a pending step while its turn waits for the matching input class; active model/effect steps fail closed

## 5.4 MCP

- [ ] `P0-MCP-001` [source:291] [state:partial] 将当前 HTTP JSON-RPC client 扩展为标准 MCP capability negotiation。
  - Recorded evidence state: `partial`. `internal/mcp` performs explicit `initialize` capability negotiation and rejects omitted/unknown protocol versions; provider-wide negotiation persistence remains pending

- [ ] `P0-MCP-002` [source:292] [state:partial] 支持 stdio、streamable HTTP 和远程连接，但连接层与 tool contract 分离。
  - Recorded evidence state: `partial`. Shared transport contract supports streamable HTTP/SSE and explicitly enabled bounded stdio; production remote connection/session lifecycle remains pending

- [ ] `P0-MCP-003` [source:293] [state:partial] MCP tool 统一进入 approval、effect、timeout、sandbox 和 audit 链路。
  - Recorded evidence state: `partial`. `internal/runtime/MCPToolExecutor` routes MCP through the durable ToolLoop for approval, timeout, effect intent/dispatch/receipt and unknown-outcome semantics; shared policy/sandbox and Inspector integration remain pending

- [ ] `P0-MCP-004` [source:294] [state:partial] secret 只用 reference，经 secret broker 注入，禁止写入 event payload。
  - Recorded evidence state: `partial`. `SecretResolver` and `BrokerSecretResolver` keep references out of JSON-RPC and bind short-lived broker leases to an explicit scope; production broker wiring remains pending

- [ ] `P1-MCP-005` [source:295] [state:partial] 对 server schema 漂移、工具删除和版本不兼容 fail-closed。
  - Recorded evidence state: `partial`. Canonical tool schema digest, duplicate/deleted tool rejection, required-argument checks and protocol-version fail-closed tests exist; live catalog persistence and Inspector evidence remain pending

## 6.1 Context Assembly

- [ ] `P0-CTX-001` [source:303] [state:partial] 保留并重构当前 Context Manifest，作为 model-visible world 的唯一描述。
  - Recorded evidence state: `partial`. `internal/context/manifest.go` is the immutable model-visible manifest and provider boundary; API/provider-wide cutover remains pending

- [ ] `P0-CTX-002` [source:304] [state:partial] 明确层级：system、policy、agent、task、memory、history、tool transaction、steering。
  - Recorded evidence state: `partial`. Prompt manifest ranks system/policy/agent/task/memory/history/tool layers; steering and production assembly integration remain pending

- [ ] `P0-CTX-003` [source:305] [state:partial] 每个 block 带 source、digest、token count、mandatory、atomic group、sensitivity。
  - Recorded evidence state: `partial`. Context blocks carry source, hash, token estimate, mandatory, provenance, sensitivity, atomic metadata and selection reason

- [ ] `P0-CTX-004` [source:306] [state:partial] tokenizer ID 与模型路由绑定；provider boundary 检查 tokenizer drift。
  - Recorded evidence state: `partial`. Tokenizer identity is frozen and validated at provider boundary; every adapter migration remains pending

- [ ] `P0-CTX-005` [source:307] [state:partial] 未完成 tool transaction 永远作为 mandatory atomic group。
  - Recorded evidence state: `partial`. Compiler promotes paired tool transaction blocks into an atomic mandatory group; all harness producers remain pending

- [ ] `P0-CTX-006` [source:308] [state:partial] context overflow 不能截断原子事务或 Unicode 内容。
  - Recorded evidence state: `partial`. Compiler rejects overflow instead of truncating mandatory/atomic/Unicode blocks; provider stream integration remains pending

- [ ] `P0-CTX-007` [source:309] [state:partial] 输出 Context Diff：本 step 相比上 step 增删了什么、为什么。
  - Recorded evidence state: `partial`. `internal/context/diff.go` emits deterministic digest-only added/removed/changed block projections with token and reason accounting

## 6.2 Compaction

- [ ] `P0-COMP-001` [source:313] [state:partial] compaction 必须记录 source window、summary digest、coverage、quality 和 lineage。
  - Recorded evidence state: `partial`. `internal/context/compaction.go` records source window, summary, archive, quality and replay lineage; production archive wiring remains pending

- [ ] `P0-COMP-002` [source:314] [state:partial] summary 未减少 token 或丢失 mandatory facts 时拒绝提交。
  - Recorded evidence state: `partial`. Compaction lineage rejects non-reducing summaries and unverified mandatory recall; provider-specific summarizer gates remain pending

- [ ] `P0-COMP-003` [source:315] [state:partial] 原始内容进入 immutable archive，summary 不能覆盖证据。
  - Recorded evidence state: `partial`. Compaction requires an immutable archive reference and never embeds source bytes in lineage; object lifecycle integration remains pending

- [ ] `P0-COMP-004` [source:316] [state:partial] 建立 recall probe：压缩前后关键事实、约束、决策和未完成事务一致。
  - Recorded evidence state: `partial`. Deterministic recall probe checks required facts and mandatory block identity with an evidence digest

- [ ] `P1-COMP-005` [source:317] [state:partial] 支持 provider-native caching，但缓存身份由 manifest hash 决定。
  - Recorded evidence state: `partial`. Manifest digest and tokenizer identity provide a stable cache key contract; provider-native cache adapters remain pending

## 6.2.1 Skill 与 Prompt Bundle

- [ ] `P0-SKILL-001` [source:321] [state:partial] 定义版本化 `SkillBundle`：instructions、resources、tools、schemas、examples、capabilities、来源与 digest。
  - Recorded evidence state: `partial`. `internal/skills` defines canonical versioned SkillBundle with instructions, resources, schemas, tools, examples, capabilities, source and digest

- [ ] `P0-SKILL-002` [source:322] [state:partial] Skill 安装、启用、禁用和升级生成事件；运行中的 Step 使用冻结 revision。
  - Recorded evidence state: `partial`. Registry persists install/activate/disable/quarantine/revoke events and freezes active revisions; authoritative EventStore integration remains pending

- [ ] `P0-SKILL-003` [source:323] [state:partial] Skill 内容进入 ContextManifest 时保留 source、trust、sensitivity、license 和 selection reason。
  - Recorded evidence state: `partial`. `SkillBundle.ContextBlock` preserves source, trust, sensitivity, license, selection reason, revision and digest in context provenance

- [ ] `P0-SKILL-004` [source:324] [state:partial] Skill 不能直接获得工具或 secret；只能声明 capability，由 policy 决定 grant。
  - Recorded evidence state: `partial`. Activation checks explicit capability grants and bundles carry no secret/tool handles; all dispatch policy composition remains pending

- [ ] `P0-SKILL-005` [source:325] [state:partial] 冲突 instruction、重复 tool schema、循环依赖和超预算 bundle 必须在激活前拒绝。
  - Recorded evidence state: `partial`. Registry rejects duplicate schemas/capabilities, invalid dependencies and cycles, and enforces token ceilings

- [ ] `P0-SKILL-006` [source:326] [state:partial] 建立签名、来源、撤销、quarantine、兼容性和 clean-room 扫描。
  - Recorded evidence state: `partial`. Ed25519 signatures, key revocation, quarantine, durable reload and lifecycle evidence are covered; distribution compatibility scan remains pending

## 6.3 Memory

- [ ] `P0-MEM-001` [source:330] [state:unverified] 统一 harness memory 与 `internal/memory` repository，消除重复模型。

- [ ] `P0-MEM-002` [source:331] [state:unverified] 定义 working、episodic、semantic、procedural 四类 memory。

- [ ] `P0-MEM-003` [source:332] [state:unverified] memory 状态：candidate、confirmed、rejected、superseded、forgotten、conflicted。

- [ ] `P0-MEM-004` [source:333] [state:unverified] 所有 memory 带 provenance、evidence、scope、TTL、confidence、reviewer。

- [ ] `P0-MEM-005` [source:334] [state:unverified] 模型不能直接把自己的输出升级为 trusted memory。

- [ ] `P0-MEM-006` [source:335] [state:unverified] 检索分数由 repository 计算，不信任调用方提供的 score。

- [ ] `P0-MEM-007` [source:336] [state:unverified] 建立 memory poisoning、跨租户污染和冲突解决测试。

- [ ] `P1-MEM-008` [source:337] [state:unverified] embedding provider 可替换，索引 identity 和版本必须持久化。

- [ ] `P1-MEM-009` [source:338] [state:unverified] 建立离线 retrieval eval：precision、recall、MRR、污染率、过期命中率。

## 7.1 Sandbox Broker

- [ ] `P0-SBX-001` [source:346] [state:evidenced] 定义 enforcement levels：`none`、`process`、`filesystem`、`network`、`container`、`microvm`、`remote`。
  - Recorded evidence state: `complete locally for contract`. Seven cumulative enforcement levels and capability validation in `ports/sandbox`; unknown or inconsistent capabilities fail closed

- [ ] `P0-SBX-002` [source:347] [state:partial] production profile 最低要求由 policy 声明；能力不足时拒绝启动。
  - Recorded evidence state: `partial`. Local `Prepare` rejects insufficient enforcement and unsupported CPU/memory/disk/secret/output requirements; production policy wiring remains pending

- [ ] `P0-SBX-003` [source:348] [state:partial] local developer backend 支持 macOS Seatbelt、Linux Landlock/bwrap、Windows restricted token/ACL。
  - Recorded evidence state: `partial`. macOS Seatbelt deny-default implementation and real negative probes exist; Linux Landlock/bwrap and Windows restricted-token/ACL remain pending

- [ ] `P0-SBX-004` [source:349] [state:partial] 明确本地 OS sandbox 不是多租户隔离，UI 不得显示为“secure tenant sandbox”。
  - Recorded evidence state: `partial`. Local capability metadata explicitly reports no secure tenant isolation and the limitation is documented; Inspector/API wiring remains pending

- [ ] `P0-SBX-005` [source:350] [state:unverified] 交付 rootless OCI 或 remote worker backend，至少一个达到 production conformance。

- [ ] `P0-SBX-006` [source:351] [state:partial] 网络默认 deny，使用 domain/IP/port capability grant 和受控代理。
  - Recorded evidence state: `partial`. Strict network grant contract plus real Seatbelt default-deny evidence; controlled proxy, grant enforcement and DNS-rebinding tests remain pending

- [ ] `P0-SBX-007` [source:352] [state:partial] 文件规则区分 read、write、create、delete、execute，并解析 symlink/ancestor alias。
  - Recorded evidence state: `partial`. Separate file operations, containment and symlink/ancestor checks with Seatbelt allow/deny evidence; hard-link/mount/TOCTOU controls remain pending

- [ ] `P0-SBX-008` [source:353] [state:partial] 进程树、超时、取消、输出上限和孤儿进程清理纳入统一 runner contract。
  - Recorded evidence state: `partial`. Bounded streams, output termination, timeout, cancellation and Unix descendant cleanup are tested; Windows process trees and CPU/memory/disk enforcement remain pending

## 7.2 Secret 与数据安全

- [ ] `P0-SEC-001` [source:357] [state:partial] 实现 Secret Broker，事件和日志只保存 secret reference。
  - Recorded evidence state: `partial`. Metadata-only scope-bound secret leases and development memory broker with expiry/revocation/copy/canary tests; production storage and injection remain pending

- [ ] `P0-SEC-002` [source:358] [state:partial] 对 prompt、tool input/output、trace attribute 做敏感数据分类与脱敏。
  - Recorded evidence state: `partial`. Central sensitivity/redaction package now protects orchestration diagnostics and trace attributes; complete prompt/tool/log adapter integration remains pending

- [ ] `P0-SEC-003` [source:359] [state:partial] 定义 prompt injection trust zones：用户内容、retrieved content、tool output 均为 untrusted。
  - Recorded evidence state: `partial`. `internal/security/provenance.go` and prompt manifest v2 derive trust from runtime zones, preserve taint/sensitivity, reject cross-tenant input and structurally escape untrusted content; provider-wide migration remains pending

- [ ] `P0-SEC-004` [source:360] [state:partial] 插件 manifest 声明权限、网络、文件、secret 和数据外发能力。
  - Recorded evidence state: `partial`. Plugin manifests canonically sign generic, file, network, secret and data-egress permissions; registry/startup verify network-to-egress coverage and the extension supervisor enforces classified calls; adapter-wide integration remains pending

- [ ] `P0-SEC-005` [source:361] [state:partial] 保留签名、quarantine 能力，并增加 key rotation、revocation、rollback。
  - Recorded evidence state: `partial`. Durable Ed25519 trust store supports authenticated rotation, retirement, revocation-driven quarantine, compatible rollback and persistence rollback tests; artifact distribution/signing remains pending

- [ ] `P0-SEC-006` [source:362] [state:partial] 建立 threat model 到测试 ID 的双向追踪。
  - Recorded evidence state: `partial`. `docs/rebuild/threat-test-map.json` and `scripts/verify-threat-test-map.py` enforce 30 mapped threats and bidirectional test annotations; full threat coverage remains pending

- [ ] `P1-SEC-007` [source:363] [state:partial] 生成 SBOM、SLSA provenance、签名 artifact 和 reproducible build evidence。
  - Recorded evidence state: `partial`. `SBOM`, `THIRD_PARTY_NOTICES` and license copies now cover the current dependency graph and `make supply-chain` verifies them; SLSA provenance, signed release artifacts and reproducible binary evidence remain pending

- [ ] `P0-IDENT-001` [source:364] [state:partial] API 边界验证主体身份，Runtime 内只接收已验证的 `Actor` 与不可伪造 tenant context。
  - Recorded evidence state: `partial`. `core/identity.Actor` is bound to request context only after human-session or service-credential verification; authenticated handlers use immutable actor/tenant/workspace scope and spoofing tests fail closed, while non-HTTP runtime boundaries still need full migration

- [ ] `P0-IDENT-002` [source:365] [state:evidenced] 明确 human、service、agent、worker、extension 五类主体及其凭据、会话和撤销语义。
  - Recorded evidence state: `complete locally for reference boundary`. Human, service, agent, worker and extension actor types, human-session revocation, short-lived service credentials, key retirement/revocation and per-credential revocation are implemented and tested

- [ ] `P0-IDENT-003` [source:366] [state:partial] tenant/workspace membership 与 capability policy 分离；membership 不能自动授予工具权限。
  - Recorded evidence state: `partial`. Membership remains an API/menu boundary and `core/policy` remains the capability boundary; complete enforcement across every tool/model/MCP dispatch is still pending

- [ ] `P0-IDENT-004` [source:367] [state:evidenced] service-to-service 使用短期凭据和 audience binding；禁止共享静态管理员 token。
  - Recorded evidence state: `complete locally for reference boundary`. Ed25519 service credentials bind audience, actor, tenant, workspace and a maximum 15-minute lifetime; `ADRO_API_TOKEN` fails readiness and `adroctl service-credential` manages init/issue/rotation/revocation

- [ ] `P0-IDENT-005` [source:368] [state:partial] impersonation、delegation、takeover 和 break-glass 必须显式记录原始 actor 与代理链。
  - Recorded evidence state: `partial`. Delegation, impersonation, takeover and break-glass transitions preserve original/effective actors, require approval for privileged modes and emit audit-chain evidence; authoritative event propagation across all async work remains pending

- [ ] `P0-IDENT-006` [source:369] [state:partial] 所有 store、cache、queue、blob、trace 和 projection 验证 tenant boundary。
  - Recorded evidence state: `partial`. Authenticated API, artifact, runner, comments, audit and orchestration request paths use verified tenant/workspace context; full store/cache/queue/blob/trace/projection conformance remains pending

## 7.3 Extension Isolation

- [ ] `P0-EXT-001` [source:373] [state:partial] 可信、版本锁定的 Go adapter 可进程内运行；未知来源扩展不得进入核心进程。
  - Recorded evidence state: `partial`. Registry authorization plus explicit in-process factory and panic containment exist; production adapter wiring and isolation evidence remain pending

- [ ] `P0-EXT-002` [source:374] [state:partial] 非可信扩展通过独立进程与 gRPC、Connect 或 JSON-RPC 窄协议运行。
  - Recorded evidence state: `partial`. `runtime/extensions` runs reference external adapters over bounded JSON-RPC on `SandboxBroker` without EventStore/database handles; production isolation remains pending

- [ ] `P0-EXT-003` [source:375] [state:partial] 扩展握手包含协议版本、schema digest、capabilities、权限需求和最大消息尺寸。
  - Recorded evidence state: `partial`. Bounded reference handshake verifies protocol, adapter, schema, capability/permission subsets and message size; production adapter conformance remains pending

- [ ] `P0-EXT-004` [source:376] [state:partial] 扩展进程由 supervisor 管理启动、健康、崩溃、指数退避、最大重启和 quarantine。
  - Recorded evidence state: `partial`. Reference supervisor covers start/health/stop, cumulative restart budget, capped backoff, quarantine and bounded audit in crash/exit tests; production lifecycle integration remains pending

- [ ] `P0-EXT-005` [source:377] [state:partial] 扩展崩溃不能带崩 Runtime；进行中的写 effect 按 durable effect 状态机恢复。
  - Recorded evidence state: `partial`. Extension crashes are isolated and durable write effects already retain unknown-outcome semantics; production adapter/effect integration remains pending

- [ ] `P0-EXT-006` [source:378] [state:partial] 纯计算、无 I/O 的 transform 可选用 WASI；核心调度、事件存储和 effect 协议不得放入 WASI 插件。
  - Recorded evidence state: `partial`. WASI is represented in the signed contract and explicitly rejected without a dedicated runtime; a production WASI transform runner is absent

- [ ] `P0-EXT-007` [source:379] [state:partial] 扩展不能获得宿主环境变量；文件、网络与 secret 由 broker 按 capability 注入。
  - Recorded evidence state: `partial`. Host environment is not inherited and manifest file/network/secret grants map to broker requests; production secret injection and network proxy remain pending

- [ ] `P0-EXT-008` [source:380] [state:partial] adapter 升降级经过兼容性矩阵和滚动握手；不兼容实例不得接收新任务。
  - Recorded evidence state: `partial`. Registry compatibility matrix, signed activation/rollback and rolling handshake rejection exist; live rolling replacement orchestration remains pending

- [ ] `P0-EXT-009` [source:381] [state:partial] 提供恶意扩展测试：超时、内存膨胀、协议洪泛、伪造 receipt、越权访问和退出风暴。
  - Recorded evidence state: `partial`. Malicious suite covers timeout, protocol flood, forged IDs, overclaim, panic, invalid/oversized output and exit storms; memory/fork bomb and forged receipt integration remain pending

## 8.1 Agent 与 Delegation

- [ ] `P0-AGENT-001` [source:389] [state:unverified] Agent 定义只包含 identity、instructions、model policy、capability set、budgets、memory scope。

- [ ] `P0-AGENT-002` [source:390] [state:unverified] 子 Agent 使用独立 Session 和 event stream，并记录 parent/child lineage。

- [ ] `P0-AGENT-003` [source:391] [state:unverified] delegation 时显式传递 context selection，而不是共享全部父上下文。

- [ ] `P0-AGENT-004` [source:392] [state:unverified] budget、deadline、tool permission 和 recursion depth 只能收缩。

- [ ] `P0-AGENT-005` [source:393] [state:unverified] 子 Agent shutdown/dispose 有界，父任务取消必须传播。

- [ ] `P0-AGENT-006` [source:394] [state:unverified] 支持 result contract、partial result、failure、timeout、cancel 和 outcome unknown。

## 8.2 Graph Runtime

- [ ] `P0-GRAPH-001` [source:398] [state:unverified] 保留自由 DAG、fan-out/fan-in、quorum、conditional edge、bounded loop。

- [ ] `P0-GRAPH-002` [source:399] [state:unverified] 删除 Requirement 命名，将 `RequirementExecutionPlan` 泛化为 `ExecutionPlan`。

- [ ] `P0-GRAPH-003` [source:400] [state:unverified] 每个 node 明确 input/output schema、retry、timeout、compensation、required evidence。

- [ ] `P0-GRAPH-004` [source:401] [state:unverified] join 和 merge 使用确定性 reducer，冲突生成 artifact 而不是静默覆盖。

- [ ] `P0-GRAPH-005` [source:402] [state:unverified] human gate、takeover、repair 和 verification 都使用普通 node/edge 语义。

- [ ] `P0-GRAPH-006` [source:403] [state:unverified] scheduler 使用 lease/fencing；旧 worker 不能提交晚到结果。

- [ ] `P0-GRAPH-007` [source:404] [state:unverified] outbox 与 plan event 在同一事务边界。

- [ ] `P1-GRAPH-008` [source:405] [state:unverified] 提供 distributed worker claim：`SKIP LOCKED` 或等价 CAS、heartbeat、generation-aware requeue。

- [ ] `P1-GRAPH-009` [source:406] [state:evidenced] 添加背压、公平性、优先级反转和 workspace quota 测试。
  - Recorded evidence state: `complete locally`. Backpressure, weighted fairness, priority aging/inversion, workspace quota and non-sheddable recovery tests in `internal/orchestration/resource_accounting_test.go`

- [ ] `P0-GRAPH-010` [source:407] [state:evidenced] scheduler 建立 tenant/workspace/agent 多级 admission control，超额任务进入明确等待或拒绝状态。
  - Recorded evidence state: `complete locally for local scheduler`. `Scheduler.Tick` reserves tenant/workspace/agent capacity before provider dispatch and reports explicit admitted/waiting/rejected states; distributed SQL quota adapter remains pending

- [ ] `P0-GRAPH-011` [source:408] [state:evidenced] 使用 weighted fair queuing 或等价算法，定义可验证的 starvation bound。
  - Recorded evidence state: `complete locally for reference queue`. Integer weighted fair queuing exposes virtual finish and a conservative starvation deadline with deterministic tests

- [ ] `P0-GRAPH-012` [source:409] [state:evidenced] priority aging 防止低优先级永久饥饿；紧急优先级必须受 tenant ceiling 限制。
  - Recorded evidence state: `complete locally for reference queue`. Priority aging raises waiting work to a configured maximum; tenant emergency ceilings cap priority with an auditable decision

- [ ] `P0-GRAPH-013` [source:410] [state:evidenced] 过载时优先 load shed 未持久化、低优先级工作，不能丢弃已 dispatch effect 的恢复任务。
  - Recorded evidence state: `complete locally for reference queue`. Overload shedding only removes low-priority unpersisted work; persisted and recovery requests are protected

- [ ] `P0-GRAPH-014` [source:411] [state:evidenced] claim 排序键稳定且可解释；相同输入和时钟下调度结果可复现。
  - Recorded evidence state: `complete locally for reference queue`. Claim order uses effective priority, integer virtual finish and a stable tenant/workspace/agent/time/ID key

- [ ] `P0-GRAPH-015` [source:412] [state:evidenced] 建立 noisy-neighbor、突发流量、配额耗尽、worker 抖动和优先级反转基准。
  - Recorded evidence state: `complete locally`. `internal/orchestration/resource_accounting_benchmark_test.go` and `scripts/run-resource-scheduler-benchmarks.sh` benchmark noisy-neighbor fairness, bursts, quota exhaustion, worker jitter/recovery and priority inversion; sustained multi-process soak remains a release gate

## 8.2.1 Durable Timer

- [ ] `P0-TIMER-001` [source:416] [state:partial] timeout、retry、approval deadline、sleep、scheduled resume 使用持久 TimerStore，不能只依赖进程内 goroutine/timer。
  - Recorded evidence state: `partial`. Durable command contracts now cover effect timeout, sleep and scheduled resume in addition to approval/model retry; production call-site scheduling remains pending

- [ ] `P0-TIMER-002` [source:417] [state:evidenced] timer 记录 due time、command、stream expected sequence、generation、owner 和 state。
  - Recorded evidence state: `complete locally`. `Timer` persists UTC due time, command digest, stream sequence, generation, owner, state, lease expiry and fencing token in `internal/runtime/timer.go`

- [ ] `P0-TIMER-003` [source:418] [state:evidenced] worker claim 使用 lease/fencing；重复触发由 command idempotency 收敛。
  - Recorded evidence state: `complete locally`. Atomic `ClaimDue`, fencing-token takeover, occurrence-key idempotency and release/ack tests in `internal/runtime/timer_test.go`

- [ ] `P0-TIMER-004` [source:419] [state:partial] 虚拟时钟下可无真实等待测试 timer ordering、clock jump、DST 和 leap-second 输入。
  - Recorded evidence state: `partial`. Manual-clock ordering, clock jump, DST normalization and leap-second-shaped input tests exist; broader cross-process and calendar compatibility fixtures remain pending

- [ ] `P0-TIMER-005` [source:420] [state:evidenced] 进程长时间停机后按策略 catch-up、coalesce 或 expire，不能无界突发执行。
  - Recorded evidence state: `complete locally for reference backend`. Bounded catch-up, coalesce, suppression and expiry are persisted and tested; integration with every runtime retry/deadline path remains pending

- [ ] `P0-TIMER-006` [source:421] [state:partial] WebUI/CLI 可查看、取消和解释 timer，但不能直接修改数据库行。
  - Recorded evidence state: `partial`. Scoped `GET /api/v1/timers`, per-timer read/explain, permission-checked cancel, `adroctl timer` commands, and WebUI Timer Inspector now use the durable TimerStore; API scope/idempotency tests and Playwright Inspector coverage exist, while real timer seeding through every production retry/deadline call site remains pending

## 8.3 Resource Accounting

- [ ] `P0-RES-001` [source:425] [state:evidenced] 统一计量 CPU time、memory peak、disk bytes、output bytes、network bytes、tokens、tool calls、wall clock 和并发槽位。
  - Recorded evidence state: `complete locally`. `ResourceVector` unifies CPU time, memory peak, disk/output/network bytes, tokens, tool calls, wall time and concurrency slots

- [ ] `P0-RES-002` [source:426] [state:evidenced] 每项资源同时支持 request、reserved、consumed、released 和 overage 状态。
  - Recorded evidence state: `complete locally`. Every reservation preserves requested, reserved, consumed, released and overage vectors

- [ ] `P0-RES-003` [source:427] [state:evidenced] 父任务预算等于自身消耗加全部子任务保留额度，防止递归 delegation 超卖。
  - Recorded evidence state: `complete locally`. Child reservations are carved from the parent and child consumption rolls up, preventing recursive delegation oversell

- [ ] `P0-RES-004` [source:428] [state:evidenced] 执行前 reserve，结束后 settle；崩溃恢复必须回收孤儿 reservation。
  - Recorded evidence state: `complete locally for reference backend`. Scheduler reserve-before-dispatch, worker settlement, failed-dispatch release, atomic persistence rollback and deepest-first orphan recovery are tested

- [ ] `P0-RES-005` [source:429] [state:evidenced] usage 记录关联 tenant、session、step、model call、tool effect 和 cost center。
  - Recorded evidence state: `complete locally`. Usage attribution binds tenant, workspace, agent, session, step, model/tool effect and cost center

- [ ] `P0-RES-006` [source:430] [state:evidenced] 达到 soft limit 触发 warning/compaction/降级，达到 hard limit 进入可解释终止状态。
  - Recorded evidence state: `complete locally`. Soft limits return warning/compaction/degradation actions; hard limits produce explicit waiting/rejected decisions and terminal overage reason

- [ ] `P0-RES-007` [source:431] [state:evidenced] 不信任 provider 自报 usage；保存原始 usage、标准化 usage 和估算差异。
  - Recorded evidence state: `complete locally`. Usage retains raw provider JSON, normalized usage, independent estimate, signed discrepancy and explicit missing-provider fallback

- [ ] `P0-RES-008` [source:432] [state:evidenced] WebUI 展示预算燃尽、reservation、异常峰值和子 Agent 归因。
  - Recorded evidence state: `complete locally`. `/api/v1/resources` drives the Cost Center burn bars, reservation table, anomaly evidence and child-Agent attribution; `e2e/resource-accounting.spec.js` covers desktop refresh and narrow-screen overflow

- [ ] `P0-RES-009` [source:433] [state:evidenced] conformance 覆盖重复 usage event、延迟账单、缺失 usage、负数与溢出。
  - Recorded evidence state: `complete locally for reference backend`. Conformance covers duplicate/conflicting usage, delayed billing, missing usage, negative values and integer overflow rollback

## 9.1 Store Ports

- [ ] `P0-STORE-001` [source:441] [state:partial] 定义 EventStore、SnapshotStore、LeaseStore、BlobStore、SecretStore、ProjectionStore 独立接口。
  - Recorded evidence state: `partial`. EventStore and LeaseStore ports exist; Snapshot/Blob/Secret/Projection ports remain pending

- [ ] `P0-STORE-002` [source:442] [state:evidenced] SQLite 是 reference backend，不再伪装 production HA。
  - Recorded evidence state: `complete for EventStore`. `adapters/eventstore/sqlite` is explicitly single-node and does not claim HA

- [ ] `P0-STORE-003` [source:443] [state:evidenced] 完整交付 PostgreSQL backend，而不是只有 migration 与测试 driver。
  - Recorded evidence state: `complete for EventStore`. `adapters/eventstore/postgres`; migrations 001-015 apply together; real PostgreSQL 17 lock-wait, conformance and restore evidence

- [ ] `P0-STORE-004` [source:444] [state:evidenced] SQLite/PostgreSQL 运行同一 conformance suite。
  - Recorded evidence state: `complete for EventStore/LeaseStore`. SQLite and PostgreSQL run `conformance/eventstore`

- [ ] `P0-STORE-005` [source:445] [state:evidenced] append 使用 expected sequence/version CAS。
  - Recorded evidence state: `complete for both backends`. Expected-sequence CAS, concurrent single-winner and concurrent same-key replay evidence

- [ ] `P0-STORE-006` [source:446] [state:evidenced] event、outbox、terminal checkpoint 需要的原子边界必须在一个事务实现。
  - Recorded evidence state: `complete for both EventStores`. Event, outbox and terminal snapshot share one rollback-tested transaction

- [ ] `P0-STORE-007` [source:447] [state:unverified] blob 使用 content-addressed identity、encryption metadata 和 retention policy。

- [ ] `P0-STORE-008` [source:448] [state:unverified] BlobStore 支持流式 put/get、digest 校验、大小上限、去重和租户隔离。

- [ ] `P0-STORE-009` [source:449] [state:partial] blob 引用由 retained event、snapshot、artifact 和 legal hold 形成 GC root；GC 必须 mark-and-sweep 且可恢复。
  - Recorded evidence state: `partial`. `internal/artifact.Lifecycle` persists roots, legal holds and mark/sweep deletion proofs; event/snapshot/blob projection roots remain to be wired

- [ ] `P0-STORE-010` [source:450] [state:unverified] event 在线保留期与 authoritative archive 分离；删除 read model 不得破坏完整 replay。

- [ ] `P0-STORE-011` [source:451] [state:unverified] 敏感大内容使用 envelope encryption；事件保存 blob digest、key reference 和 classification，不保存明文。

- [ ] `P0-STORE-012` [source:452] [state:unverified] 隐私删除使用 tombstone、访问撤销和密钥销毁，不篡改 append-only event history。

- [ ] `P0-STORE-013` [source:453] [state:partial] legal hold 优先于普通 retention；所有保留与删除决策写审计事件。
  - Recorded evidence state: `partial`. Lifecycle roots distinguish retain-until and legal hold and preserve protected objects in every proof

- [ ] `P0-STORE-014` [source:454] [state:partial] 生成 proof-of-deletion 报告，覆盖在线库、对象存储、缓存、索引和备份到期状态。
  - Recorded evidence state: `partial`. GC reports are content-addressed, durable and revalidated after restart; backup/object-store proof adapters remain pending

- [ ] `P0-STORE-015` [source:455] [state:partial] 所有表、索引、blob key 和归档分区显式包含 tenant boundary。
  - Recorded evidence state: `partial`. Composite tenant/stream foreign keys reject cross-tenant EventStore rows; authenticated scoped read/RLS ports remain pending

## 9.1.1 Artifact

- [ ] `P0-ART-001` [source:459] [state:unverified] 定义 `Artifact`：immutable version、blob refs、media type、provenance、producer、inputs、classification、digest。

- [ ] `P0-ART-002` [source:460] [state:unverified] Artifact alias/tag 作为 projection；历史 version 不可覆盖。

- [ ] `P0-ART-003` [source:461] [state:unverified] 工具和子 Agent 输出通过 ArtifactRef 传递，禁止在 event payload 内嵌无界内容。

- [ ] `P0-ART-004` [source:462] [state:unverified] preview、download、export 和外发都经过 policy 与审计。

- [ ] `P0-ART-005` [source:463] [state:unverified] Artifact lineage 连接 context、effect、eval 和最终结果，可生成可验证 evidence bundle。

- [ ] `P0-ART-006` [source:464] [state:unverified] retention、legal hold、tombstone 和 key destruction 复用 Blob/Data Lifecycle 语义。

## 9.2 Recovery

- [ ] `P0-REC-001` [source:468] [state:unverified] 恢复前先取得 write ownership/lease，再做 tail repair。

- [ ] `P0-REC-002` [source:469] [state:unverified] 只容忍 torn tail；middle corruption、hash mismatch、sequence gap 一律 fail-closed。

- [ ] `P0-REC-003` [source:470] [state:unverified] recovery decision 必须成为事件，不能只写日志。

- [ ] `P0-REC-004` [source:471] [state:unverified] 建立进程 kill point matrix：event append、model request、tool dispatch、receipt、checkpoint 前后。

- [ ] `P0-REC-005` [source:472] [state:unverified] 恢复后 projection digest 与无故障执行一致。

- [ ] `P0-REC-006` [source:473] [state:unverified] schema migration 支持 crash resume、重复执行和 rollback policy。

- [ ] `P0-REC-007` [source:474] [state:unverified] 制定 backup/restore、PITR、定期恢复演练和校验流程，不以“备份任务成功”替代可恢复证明。

- [ ] `P0-REC-008` [source:475] [state:unverified] 为单节点与分布式 profile 分别定义 RPO、RTO 和允许的数据丢失窗口。

- [ ] `P0-REC-009` [source:476] [state:unverified] 明确跨区域一致性假设；一个 stream 同时只能有一个合法写 owner。

- [ ] `P0-REC-010` [source:477] [state:unverified] region failover 提升 fencing epoch，旧区域恢复后只能只读校验，不能提交晚到写入。

- [ ] `P0-REC-011` [source:478] [state:unverified] restore 后验证 event chain、blob digest、snapshot digest、projection digest 和 lease epoch。

- [ ] `P0-REC-012` [source:479] [state:unverified] unknown model call、unknown tool effect 和未知外部消息各有独立 reconcile queue。

- [ ] `P0-REC-013` [source:480] [state:unverified] Recovery Center 的人工操作本身必须幂等、授权、双人复核可选并进入 authoritative event stream。

## 10. Phase 8：标准可观测性与 Runtime Debugger

- [ ] `P0-OBS-001` [source:486] [state:evidenced] 删除当前自定义“OTLP HTTP JSON envelope”，接入正式 OpenTelemetry SDK/OTLP exporter。
  - Recorded evidence state: `complete locally`. `internal/telemetry` uses the official OpenTelemetry Go SDK and OTLP/HTTP protobuf exporter; the API owns one provider, injects it into orchestration, fails closed on invalid compatibility configuration and flushes on shutdown; see `docs/operations/opentelemetry.md`

- [ ] `P0-OBS-002` [source:487] [state:unverified] trace 层级：Task -> Session -> Turn -> Step -> ModelCall/ToolEffect/SubAgent。

- [ ] `P0-OBS-003` [source:488] [state:unverified] event correlation ID 与 trace/span ID 双向关联。

- [ ] `P0-OBS-004` [source:489] [state:unverified] 指标覆盖：active sessions、queue、lease recovery、model latency、tool latency、unknown outcomes、retries、tokens、cost。

- [ ] `P0-OBS-005` [source:490] [state:unverified] log 采用结构化字段并统一 error taxonomy。

- [ ] `P0-OBS-006` [source:491] [state:unverified] 提供 `adroctl replay`：按 event stream 重建状态并输出 divergence。

- [ ] `P0-OBS-007` [source:492] [state:unverified] 提供 `adroctl explain`：解释某个 policy decision、context selection、retry 或 recovery 决策。

- [ ] `P1-OBS-008` [source:493] [state:unverified] 支持 session bundle 导出，默认脱敏，便于离线复现。

- [ ] `P1-OBS-009` [source:494] [state:unverified] 建立 SLO：成功率、恢复时间、unknown outcome 数量、事件 append 延迟、cost budget。

## 11.1 Conformance

- [ ] `P0-EVAL-001` [source:502] [state:unverified] 创建公开 `runtime-conformance` 包，第三方 adapter 可独立运行。

- [ ] `P0-EVAL-002` [source:503] [state:unverified] Model adapter conformance：stream、retry、usage、cancel、structured output、resume。

- [ ] `P0-EVAL-003` [source:504] [state:unverified] Tool adapter conformance：schema、approval、timeout、ordered result、effect receipt、reconcile。

- [ ] `P0-EVAL-004` [source:505] [state:partial] Store conformance：CAS、atomic batch、lease、tail repair、corruption、migration。
  - Recorded evidence state: `partial`. Shared suite covers CAS, idempotency, atomicity, lease fencing, concurrency, restart, subscription, tenant isolation and corruption; migration crash/resume and tail repair pending

- [ ] `P0-EVAL-005` [source:506] [state:unverified] Sandbox conformance：filesystem、network、process、secret、cross-tenant negative tests。

## 11.2 Runtime 评测

- [ ] `P0-EVAL-006` [source:510] [state:unverified] deterministic fake model + deterministic tools，所有核心测试不依赖外部模型。

- [ ] `P0-EVAL-007` [source:511] [state:unverified] recorded session snapshot，格式变更必须显式 review。

- [ ] `P0-EVAL-008` [source:512] [state:unverified] crash matrix 覆盖所有 durable boundary。

- [ ] `P0-EVAL-009` [source:513] [state:unverified] trajectory evaluator 检查错误工具、重复 effect、无效 retry、权限绕过和上下文污染。

- [ ] `P0-EVAL-010` [source:514] [state:unverified] memory retrieval eval 与 compaction recall eval。

- [ ] `P0-EVAL-011` [source:515] [state:unverified] 长任务 soak：24h session、10k events、1k tool calls、频繁 compaction/recovery。

- [ ] `P0-EVAL-012` [source:516] [state:unverified] 性能预算：event append p95、replay time、context build time、idle memory、stream backpressure。

- [ ] `P0-EVAL-013` [source:517] [state:unverified] 模糊测试：event decoder、schema、manifest、MCP payload、migration、cursor。

- [ ] `P0-EVAL-014` [source:518] [state:unverified] race detector、deadlock watchdog、goroutine leak 和 file descriptor leak 门禁。

- [ ] `P1-EVAL-015` [source:519] [state:unverified] 在相同模型/任务集下建立 coding、research、tool-use benchmark，但与 Runtime correctness 分开评分。

- [ ] `P0-EVAL-016` [source:520] [state:unverified] benchmark 固定硬件、模型、adapter、数据集、seed、并发、warmup、重复次数和置信区间。

- [ ] `P0-EVAL-017` [source:521] [state:unverified] 对外结论只基于可复现实验；禁止用知名度、代码量、功能数或宣传材料替代结果。

- [ ] `P0-EVAL-018` [source:522] [state:unverified] 核心成功指标优先级：conformance、恢复正确性、安全隔离、确定性，其次才是延迟、吞吐和成本。

- [ ] `P0-EVAL-019` [source:523] [state:unverified] 建立 N-2 event/snapshot/config fixtures，当前版本必须能读取或给出明确迁移路径。

- [ ] `P0-EVAL-020` [source:524] [state:unverified] 滚动升级期间旧新 worker 并存，协议协商必须阻止不兼容任务分配。

- [ ] `P0-EVAL-021` [source:525] [state:unverified] 每个 release 执行 upgrade、rollback、PITR restore、region failover 和 stale worker suite。

- [ ] `P0-EVAL-022` [source:526] [state:unverified] 发布报告同时列出失败场景、置信区间和未验证项，不发布无法复核的“全面领先”结论。

- [ ] `P0-EVAL-023` [source:527] [state:partial] 定义版本化 `Evaluator` 接口与 `EvalRun` 状态机，输入为 session bundle/trajectory，输出为结构化 score、findings 和 evidence。
  - Recorded evidence state: `partial`. `internal/eval` defines versioned Evaluator, immutable SessionBundle and EvalRun state machine

- [ ] `P0-EVAL-024` [source:528] [state:partial] evaluator 不能直接修改被评 Session；复评使用相同 fixture、seed、rubric digest 和 evaluator identity。
  - Recorded evidence state: `partial`. Evaluators receive cloned bundles and results bind bundle/evaluator digests; external fixture catalog remains pending

- [ ] `P0-EVAL-025` [source:529] [state:partial] 区分规则 evaluator、模型 evaluator、人工 evaluator；模型裁判结果不得当作唯一发布门禁。
  - Recorded evidence state: `partial`. Rule evaluator is separate from model/human evaluators at the interface boundary; model/human adapter implementations remain pending

- [ ] `P0-EVAL-026` [source:530] [state:partial] 评测数据集具备版本、license、sensitivity、split、防污染和泄漏检查。
  - Recorded evidence state: `partial`. SessionBundle records schema/source digest, tenant scope and redaction state; dataset version/license/split registry remains pending

- [ ] `P0-EVAL-027` [source:531] [state:partial] EvalRun 自身可取消、恢复、限预算、追踪成本，并保存未通过 invariant 的最小复现 bundle。
  - Recorded evidence state: `partial`. EvalRun supports revision CAS, cancellation pause/resume, unit budget and minimal failing bundle evidence

## 12. Phase 10：API、协议与 SDK

- [ ] `P0-API-001` [source:537] [state:unverified] 重写 OpenAPI，以 Agent Runtime 对象为主，而不是 requirement/bug CRUD。

- [ ] `P0-API-002` [source:538] [state:unverified] 核心 API：agents、sessions、turns、effects、approvals、context、memory、plans、events、replay、evals。

- [ ] `P0-API-003` [source:539] [state:unverified] 实时协议统一为 versioned event stream，明确 cursor、resume、gap、ack 和 retention。

- [ ] `P0-API-004` [source:540] [state:unverified] 错误使用稳定 code、category、retryability、correlation ID，不泄露路径和 secret。

- [ ] `P0-API-005` [source:541] [state:unverified] 所有 mutation 支持 idempotency key 或明确声明不可幂等。

- [ ] `P0-API-006` [source:542] [state:unverified] 生成 Go client；TS client 供 WebUI 使用，禁止手写散落 fetch。

- [ ] `P1-API-007` [source:543] [state:unverified] 提供 Python SDK，重点覆盖 adapter、eval 和 session replay。

- [ ] `P1-API-008` [source:544] [state:unverified] wire protocol 版本与 Go 内部类型解耦。

- [ ] `P0-API-009` [source:545] [state:unverified] 客户端通过 media type、header 或握手协商 API/event/stream 版本；服务端不按 User-Agent 猜测。

- [ ] `P0-API-010` [source:546] [state:unverified] mutation 接口定义 expected version、idempotency key、request digest 和冲突返回。

- [ ] `P0-API-011` [source:547] [state:unverified] stream retention、cursor TTL、gap recovery 和最大未确认窗口写入公开协议。

- [ ] `P0-API-012` [source:548] [state:unverified] 所有列表接口使用稳定 cursor pagination，不允许 offset 在变化数据集上造成跳项。

- [ ] `P0-API-013` [source:549] [state:unverified] compatibility suite 覆盖旧客户端、新服务端与新客户端、旧服务端的明确支持矩阵。

## 13.1 现有 WebUI 可保留的场景

- [ ] `UI-KEEP-001` [source:557] [state:unverified] Agent 创建、Runtime/Model/Thinking/Service Tier 配置：保留并接入新 Agent schema。

- [ ] `UI-KEEP-002` [source:558] [state:unverified] Skills 与 MCP 管理：保留，增加 capability、权限和 schema 状态。

- [ ] `UI-KEEP-003` [source:559] [state:unverified] Squad/Graph 编辑器：保留，移除软件交付命名。

- [ ] `UI-KEEP-004` [source:560] [state:unverified] Chat：保留为最小交互场景，显示 session/turn identity。

- [ ] `UI-KEEP-005` [source:561] [state:unverified] Runner、Cost、Audit 页面：保留视觉结构，替换为真实 Runtime 数据。

- [ ] `UI-KEEP-006` [source:562] [state:unverified] Execution Timeline：保留并升级为统一 event timeline。

## 13.2 必须新增的核心页面

- [ ] `P0-UI-001 Runtime Overview` [source:566] [state:unverified] ：active session、queue、model/tool latency、unknown effects、recovery、tokens/cost。

- [ ] `P0-UI-002 Session Explorer` [source:567] [state:unverified] ：Session -> Turn -> Step 树，显示状态、stop reason、lineage、fork、子 Agent。

- [ ] `P0-UI-003 Event Log` [source:568] [state:unverified] ：sequence、type、causation、correlation、actor、digest；支持过滤与 replay position。

- [ ] `P0-UI-004 Context Inspector` [source:569] [state:unverified] ：每个 step 的 manifest、block 来源、token、mandatory、atomic group、context diff。

- [ ] `P0-UI-005 Compaction Inspector` [source:570] [state:unverified] ：source window、summary、coverage、recall probe、archive lineage。

- [ ] `P0-UI-006 Tool/Effect Timeline` [source:571] [state:unverified] ：intent、approval、dispatch、receipt、outcome unknown、reconcile、idempotency。

- [ ] `P0-UI-007 Approval Inbox` [source:572] [state:unverified] ：请求上下文、capability、风险、policy version、allow/deny/cancel、审计证据。

- [ ] `P0-UI-008 Memory Explorer` [source:573] [state:unverified] ：scope、type、status、evidence、TTL、conflict、supersede、forget、retrieval score。

- [ ] `P0-UI-009 Agent Delegation Graph` [source:574] [state:unverified] ：父子 session、预算继承、权限收缩、结果与失败传播。

- [ ] `P0-UI-010 Sandbox Inspector` [source:575] [state:unverified] ：backend、enforcement level、文件/网络/进程 grant、secret references、降级原因。

- [ ] `P0-UI-011 Provider Console` [source:576] [state:unverified] ：能力 negotiation、路由、fallback、circuit state、rate limit、model catalog。

- [ ] `P0-UI-012 Trace Waterfall` [source:577] [state:unverified] ：模型、工具、审批、存储、子 Agent span 与 event 关联。

- [ ] `P0-UI-013 Recovery Center` [source:578] [state:unverified] ：待 reconcile effect、损坏 stream、stale lease、失败 migration、人工决策。

- [ ] `P0-UI-014 Eval Lab` [source:579] [state:unverified] ：选择 scenario、kill point、fault、seed，运行并查看 invariant 结果。

- [ ] `P1-UI-015 Schema/Migration Viewer` [source:580] [state:unverified] ：event version、upcaster path、fixture、兼容性状态。

- [ ] `P1-UI-016 Plugin Security` [source:581] [state:unverified] ：签名、权限、版本、health、quarantine、revocation。

- [ ] `P0-UI-017 Policy/Config Snapshot Viewer` [source:582] [state:unverified] ：展示 step 使用的不可变配置、policy digest、decision record 和 divergence。

- [ ] `P0-UI-018 Stream Diagnostics` [source:583] [state:unverified] ：cursor、consumer lag、buffer occupancy、resume、gap 和断流原因。

- [ ] `P0-UI-019 Quota & Admission` [source:584] [state:unverified] ：tenant/workspace 配额、reservation、排队原因、priority aging 和 load shed。

- [ ] `P0-UI-020 Data Lifecycle` [source:585] [state:unverified] ：retention、legal hold、tombstone、key destruction、GC 与删除证明状态。

## 13.3 应删除或降级的页面

- [ ] `P0-UI-TRIM-001` [source:589] [state:unverified] Workbench/Delivery/Human QA 不再作为一级主导航，迁移到 software-delivery example。

- [ ] `P0-UI-TRIM-002` [source:590] [state:unverified] Diffs/Testing 页面改为 Artifact 与 Eval 的通用视图。

- [ ] `P0-UI-TRIM-003` [source:591] [state:unverified] Requirement/Bug 创建 dialog 从 Runtime 主应用移出。

- [ ] `P1-UI-TRIM-004` [source:592] [state:unverified] 菜单级用户权限改为 capability/policy 权限，不维护 UI 名称白名单。

## 13.4 前端工程重建

- [ ] `P0-UI-ENG-001` [source:596] [state:unverified] 使用 TypeScript + 现有轻量框架或清晰的 Web Components，禁止继续扩充单文件脚本。

- [ ] `P0-UI-ENG-002` [source:597] [state:unverified] 生成 OpenAPI client，统一 query cache、错误和 cancellation。

- [ ] `P0-UI-ENG-003` [source:598] [state:unverified] event stream 使用 resumable cursor，断线后补 gap。

- [ ] `P0-UI-ENG-004` [source:599] [state:unverified] 大 event/session 使用虚拟列表，避免 DOM 无界增长。

- [ ] `P0-UI-ENG-005` [source:600] [state:unverified] WebUI 不渲染 secret、完整敏感 prompt 或未经脱敏的 tool output。

- [ ] `P0-UI-ENG-006` [source:601] [state:unverified] Playwright 覆盖桌面/移动、长内容、断流、replay、approval、unknown effect。

- [ ] `P1-UI-ENG-007` [source:602] [state:unverified] 提供“教学模式”：从一次 Turn 高亮跳转对应源码、事件与不变量文档。

## 14. Phase 12：让项目真正“值得学习”

- [ ] `P0-DOC-001` [source:608] [state:unverified] 写 `Architecture Tour`，从 HTTP request 追踪到 model、tool、effect、checkpoint、recovery。

- [ ] `P0-DOC-002` [source:609] [state:unverified] 每个核心模块提供：职责、非职责、数据结构、不变量、失败模式、测试入口。

- [ ] `P0-DOC-003` [source:610] [state:unverified] 建立 `docs/invariants/`，每条 invariant 链接实现和测试。

- [ ] `P0-DOC-004` [source:611] [state:unverified] 建立 `docs/failure-catalog/`：crash、timeout、duplicate、late write、corruption、policy denial。

- [ ] `P0-DOC-005` [source:612] [state:unverified] 建立 3 个可运行教学场景：

- [ ] `P0-DOC-006` [source:617] [state:unverified] 每个场景同时提供 happy path、故障注入和 recovery walkthrough。

- [ ] `P0-DOC-007` [source:618] [state:unverified] 自动生成状态机图、event schema、OpenAPI 和 trace 示例，避免文档漂移。

- [ ] `P0-DOC-008` [source:619] [state:unverified] README 不列功能海洋，只展示核心不变量、5 分钟运行和源码阅读路径。

- [ ] `P1-DOC-009` [source:620] [state:unverified] 提供架构决策记录 ADR：为什么 event sourcing、为什么 unknown outcome、为什么不默认重试。

- [ ] `P1-DOC-010` [source:621] [state:unverified] 提供反例目录：错误的 callback tool、无 fence lease、非原子 checkpoint、共享全量 context。

## 15. Phase 13：开源治理与发布

- [ ] `P0-GOV-001` [source:627] [state:unverified] 保持 Apache-2.0；所有新增依赖通过许可证策略检查。

- [ ] `P0-GOV-002` [source:628] [state:unverified] 创建公开 RFC、ADR、兼容性和 deprecation 流程。

- [ ] `P0-GOV-003` [source:629] [state:unverified] 维护者不能只按目录所有权垄断，关键协议至少两人 review。

- [ ] `P0-GOV-004` [source:630] [state:unverified] 发布包含 source、binary、checksums、signature、SBOM、provenance、migration notes。

- [ ] `P0-GOV-005` [source:631] [state:unverified] 明确 experimental API 不承诺兼容，stable wire schema 遵循版本政策。

- [ ] `P0-GOV-006` [source:632] [state:unverified] 建立安全响应、embargo release、CVE 和 secret rotation 演练。

- [ ] `P1-GOV-007` [source:633] [state:unverified] 以第三方 adapter/conformance 贡献扩大社区，而不是不断向 core 塞能力。

- [ ] `P1-GOV-008` [source:634] [state:unverified] Apache 候选前证明多组织贡献、公开决策和独立 release 能力。

- [ ] `P0-GOV-009` [source:635] [state:unverified] 添加公开材料词法门禁，扫描源码、注释、文档、测试、UI、Schema、示例、提交和发布产物。

- [ ] `P0-GOV-010` [source:636] [state:unverified] denylist 由组织私有配置提供，CI 日志只报告规则 ID 与位置，不回显被禁名称。

- [ ] `P0-GOV-011` [source:637] [state:partial] PR 模板要求作者确认 clean-room 来源、许可证、能力证据和无外部项目命名污染。
  - Recorded evidence state: `partial`. `.github/PULL_REQUEST_TEMPLATE.md` asks authors to record clean-room provenance, license/SBOM impact, capability evidence and naming scan; independent review enforcement remains pending

- [ ] `P0-GOV-012` [source:638] [state:unverified] 设计讨论使用中性能力语言与公开标准编号，不以外部仓库结构作为 ADRO 公共 API 命名来源。

- [ ] `P0-GOV-013` [source:639] [state:unverified] commit message、changelog 和 release note 进入同一 lexical scan，历史迁移记录使用内部受控档案。

- [ ] `P0-GOV-014` [source:640] [state:unverified] 新 adapter 的公共名称只描述协议或能力；厂商/产品特定名称仅在独立可选仓库中出现。

- [ ] `P0-GOV-015` [source:641] [state:unverified] 对当前仓库全量执行私有 denylist 扫描，形成不提交到公开仓库的内部整改清单，并在下一公开版本前消除全部历史命中。

## 16. 删除清单

- [ ] `SOURCE-L0649` [source:649] [state:unverified] 旧固定 pipeline core 与 compatibility state machine。

- [ ] `SOURCE-L0650` [source:650] [state:unverified] Requirement/Bug/Comment 作为 core domain 的所有代码。

- [ ] `SOURCE-L0651` [source:651] [state:unverified] 五套互相重叠的 authoritative event/state persistence。

- [ ] `SOURCE-L0652` [source:652] [state:evidenced] 自定义伪 OTLP exporter。
  - Recorded evidence state: `complete locally`. The private JSON envelope exporter was removed; no custom OTLP wire implementation remains in the runtime

- [ ] `SOURCE-L0653` [source:653] [state:unverified] 未交付却可配置的 production/HA backend 开关。

- [ ] `SOURCE-L0654` [source:654] [state:unverified] 巨型 `server.go`、`store.go`、`local.go`、`enhancements.js`。

- [ ] `SOURCE-L0655` [source:655] [state:unverified] 手写、散落、无类型的 WebUI `fetch` 调用。

- [ ] `SOURCE-L0656` [source:656] [state:unverified] 只为了菜单展示存在、没有 Runtime invariant 的 CRUD。

- [ ] `SOURCE-L0657` [source:657] [state:unverified] 任何自动从 unknown outcome 重试写操作的路径。

- [ ] `SOURCE-L0658` [source:658] [state:unverified] sandbox 不可用时自动 full-access 的路径。

- [ ] `SOURCE-L0659` [source:659] [state:unverified] 文档中的未来能力冒充当前能力。

## 18. 第一批 30 个立即执行项

- [ ] `SOURCE-L0685` [source:685] [state:unverified] 1. Tag 当前 legacy 版本并保存全量测试基线。

- [ ] `SOURCE-L0686` [source:686] [state:unverified] 2. 重写一页项目定位：Durable Agent Runtime。

- [ ] `SOURCE-L0687` [source:687] [state:unverified] 3. 写 ADR-001：authoritative event log。

- [ ] `SOURCE-L0688` [source:688] [state:unverified] 4. 写 ADR-002：durable effect 与 unknown outcome。

- [ ] `SOURCE-L0689` [source:689] [state:unverified] 5. 写 ADR-003：core/ports/adapters 依赖规则。

- [ ] `SOURCE-L0690` [source:690] [state:unverified] 6. 盘点五套事件/状态记录并制作映射表。

- [ ] `SOURCE-L0691` [source:691] [state:unverified] 7. 定义统一 EventEnvelope v1。

- [ ] `SOURCE-L0692` [source:692] [state:unverified] 8. 定义 Session/Turn/Step 状态机。

- [ ] `SOURCE-L0693` [source:693] [state:unverified] 9. 定义 Effect 状态机。

- [ ] `SOURCE-L0694` [source:694] [state:unverified] 10. 为现有 runtime/harness/orchestration 建 golden event fixtures。

- [ ] `SOURCE-L0695` [source:695] [state:unverified] 11. 创建 deterministic fake model。

- [ ] `SOURCE-L0696` [source:696] [state:unverified] 12. 创建 deterministic fake tools。

- [ ] `SOURCE-L0697` [source:697] [state:unverified] 13. 建立 crash kill-point harness。

- [ ] `SOURCE-L0698` [source:698] [state:unverified] 14. 建立 replay digest 测试。

- [ ] `SOURCE-L0699` [source:699] [state:unverified] 15. 将固定 pipeline 标为 deprecated example。

- [ ] `SOURCE-L0700` [source:700] [state:unverified] 16. 泛化 `RequirementExecutionPlan` 命名。

- [ ] `SOURCE-L0701` [source:701] [state:unverified] 17. 拆 `harness/store.go` 第一部分：transcript/checkpoint。

- [ ] `SOURCE-L0702` [source:702] [state:unverified] 18. 拆 `provider/local.go` 第一部分：process/protocol。

- [ ] `SOURCE-L0703` [source:703] [state:evidenced] 19. 引入正式 OpenTelemetry SDK。
  - Recorded evidence state: `complete locally`. Official OpenTelemetry Go SDK dependencies, provider lifecycle and standard OTLP/HTTP protobuf export are implemented and tested

- [ ] `SOURCE-L0704` [source:704] [state:unverified] 20. 定义 Sandbox Broker 与 enforcement level。

- [ ] `SOURCE-L0705` [source:705] [state:unverified] 21. 完成 SQLite EventStore conformance。

- [ ] `SOURCE-L0706` [source:706] [state:unverified] 22. 完成 PostgreSQL EventStore 最小 append/CAS/transaction。

- [ ] `SOURCE-L0707` [source:707] [state:unverified] 23. 统一两套 memory 模型。

- [ ] `SOURCE-L0708` [source:708] [state:unverified] 24. 创建 Runtime Inspector 的信息架构和路由骨架。

- [ ] `SOURCE-L0709` [source:709] [state:unverified] 25. 实现 Session Explorer API。

- [ ] `SOURCE-L0710` [source:710] [state:unverified] 26. 实现 Event Log + replay API。

- [ ] `SOURCE-L0711` [source:711] [state:unverified] 27. 实现 Context Inspector API。

- [ ] `SOURCE-L0712` [source:712] [state:unverified] 28. 实现 Effect/Approval API。

- [ ] `SOURCE-L0713` [source:713] [state:unverified] 29. 将 WebUI 改用生成的 TypeScript client。

- [ ] `SOURCE-L0714` [source:714] [state:unverified] 30. 删除第一批已迁移的旧 CRUD 与重复 persistence。

## v0.2 Kernel Reset

- [ ] `SOURCE-L0722` [source:722] [state:unverified] 统一 event envelope、Session/Turn/Step、Effect、SQLite backend、fake model/tool。

- [ ] `SOURCE-L0723` [source:723] [state:unverified] crash matrix 与 deterministic replay 通过。

- [ ] `SOURCE-L0724` [source:724] [state:unverified] 旧 delivery 功能仍可通过 example 运行，但不再进入 core。

## v0.3 Inspectable Runtime

- [ ] `SOURCE-L0728` [source:728] [state:unverified] OTel、Session Explorer、Event Log、Context/Effect/Approval Inspector 可用。

- [ ] `SOURCE-L0729` [source:729] [state:unverified] Runtime 的每个重要决策都能从 UI/CLI 解释。

## v0.4 Secure Execution

- [ ] `SOURCE-L0733` [source:733] [state:unverified] Sandbox Broker、Secret Broker、capability policy、MCP conformance 可用。

- [ ] `SOURCE-L0734` [source:734] [state:unverified] 至少一个 production isolation backend 通过 negative suite。

## v0.5 Distributed Durable Runtime

- [ ] `SOURCE-L0738` [source:738] [state:unverified] PostgreSQL、distributed lease、worker claim/requeue、outbox 通过故障注入。

- [ ] `SOURCE-L0739` [source:739] [state:unverified] 旧 worker、重复 delivery 和网络分区不能破坏状态。

## v0.6 Multi-Agent Reference

- [ ] `SOURCE-L0743` [source:743] [state:unverified] delegation、子 Agent、DAG、join、repair、human gate、budget inheritance 完整。

- [ ] `SOURCE-L0744` [source:744] [state:unverified] coding/research/software-delivery 三个场景通过 conformance 和 E2E。

## v1.0 Stable Learning Architecture

- [ ] `SOURCE-L0748` [source:748] [state:unverified] stable wire/event schema 与迁移策略。

- [ ] `SOURCE-L0749` [source:749] [state:unverified] 所有核心能力均有 invariant、实现、conformance、故障测试和教学文档。

- [ ] `SOURCE-L0750` [source:750] [state:unverified] 无重复 authoritative state，无 interface-only 能力伪装为 stable。

- [ ] `SOURCE-L0751` [source:751] [state:unverified] 全量 CI、race、fuzz、fault、browser、security、license、release gates 通过。

## 20. 最终 Definition of Done

- [ ] `SOURCE-L0759` [source:759] [state:unverified] 任意模型请求可从事件日志确定性重建。

- [ ] `SOURCE-L0760` [source:760] [state:unverified] 任意写工具都有 durable intent、receipt 与 unknown-outcome 处理。

- [ ] `SOURCE-L0761` [source:761] [state:unverified] 任意崩溃点都有明确恢复决策，不依赖内存猜测。

- [ ] `SOURCE-L0762` [source:762] [state:unverified] 任意权限决策有 capability、policy version 与审计证据。

- [ ] `SOURCE-L0763` [source:763] [state:unverified] 任意 session 可 replay、explain、fork、export 和脱敏复现。

- [ ] `SOURCE-L0764` [source:764] [state:unverified] Context、Memory、Compaction 均有来源和质量验证。

- [ ] `SOURCE-L0765` [source:765] [state:unverified] 子 Agent 不能扩大权限、预算或数据范围。

- [ ] `SOURCE-L0766` [source:766] [state:unverified] production profile 不允许无强隔离的 runner。

- [ ] `SOURCE-L0767` [source:767] [state:unverified] SQLite 与 PostgreSQL 通过相同核心一致性测试。

- [ ] `SOURCE-L0768` [source:768] [state:unverified] WebUI 能观察核心 Runtime 语义，而不是只展示业务 CRUD。

- [ ] `SOURCE-L0769` [source:769] [state:unverified] 三个真实场景证明 Runtime 通用性，但场景代码不污染 core。

- [ ] `SOURCE-L0770` [source:770] [state:unverified] 文档声明与实际交付能力完全一致。

- [ ] `SOURCE-L0771` [source:771] [state:unverified] core reducer 在虚拟时钟、固定 ID 和固定随机源下逐字节确定。

- [ ] `SOURCE-L0772` [source:772] [state:unverified] 模型流和事件流在断线、背压、重复与 gap 条件下可恢复且无无界内存增长。

- [ ] `SOURCE-L0773` [source:773] [state:unverified] scheduler 能证明 tenant 公平性、starvation bound 和过载降级策略。

- [ ] `SOURCE-L0774` [source:774] [state:unverified] event archive、blob retention、隐私删除和 legal hold 不互相破坏。

- [ ] `SOURCE-L0775` [source:775] [state:unverified] 生命周期系统能够回滚部分启动并证明没有 goroutine、连接或临时资源泄漏。

- [ ] `SOURCE-L0776` [source:776] [state:unverified] N-2 fixture、滚动升级、rollback、PITR 和跨区域故障演练全部通过。

- [ ] `SOURCE-L0777` [source:777] [state:unverified] 公开仓库与发布产物通过竞争项目名称禁入扫描。
