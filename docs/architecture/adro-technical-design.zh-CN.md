# ADRO 技术方案

版本：1.0（开发基线）<br>
状态：需求文档 `docs/product-requirements.zh-CN.md` 的配套技术设计<br>
适用范围：开源单机版、企业私有化单租户、多租户演进<br>
更新时间：2026-08-30

> 本文是可以拆解为代码、Migration、API、插件和测试任务的技术方案。它定义 ADRO 如何独立拥有业务事实和自动交付闭环，同时允许执行器、Git、CI、通知、存储和知识能力通过插件替换。

## 1. 设计结论

ADRO 的核心是“持久化流水线 + 可插拔执行器 + 独立证据链 + 可恢复 harness”。用户提交 Requirement、Bug 或 Analysis 后，系统创建一条不可变的 `session_id`，编排 Agent 完成方案、研发/分析、测试、部署、失败回流、复测和报告。员工负责目标、权限、策略和例外，实际执行始终由 Agent 完成。

当前版本不包含任何外部编排产品的适配器、API、启动器或源码副本。社区兼容插件必须在独立仓库通过 SPI、签名和许可证审查后安装，不改变业务状态模型。

最重要的三个不变量：

1. 每个模块都通过版本化 SPI 插件化，核心内核只依赖稳定协议，不依赖某个厂商实现。
2. 测试失败回到原 ADRO `session_id` 和同一逻辑开发上下文；`run_id`、`attempt_id` 可以变化，但不能创建无关联的新 session。
3. 只有可独立核验的 Commit、测试结论、部署版本、报告和 submission 才能通过质量门禁；Agent 自报成功不能替代证据。

## 2. 范围、原则与 clean-room 规则

### 2.1 技术范围

P0 必须实现：

- 多租户/单租户隔离、成员与项目多对多、仓库和环境授权。
- Requirement、Bug、Analysis 的生命周期、评论、附件、关联和审计。
- Planner、Developer、Tester、Analyst、Repairer、Arbiter Agent 角色及可配置 Agent Team。
- 自动扫描本机 Codex、Claude Code 等客户端，完成能力探测、权限校验和可用性展示。
- 长流程状态机、队列 lease、幂等、超时、重试、暂停、人工升级和重启恢复。
- 上下文清单、工作记忆、会话记忆、项目记忆、精确归档、压缩和可验证召回。
- Git、CI、部署、测试、Artifact、EvidenceBundle、钉钉和飞书的 provider-neutral SPI、证据模型和契约测试；具体生产适配器必须作为外部插件安装并通过真实凭证验收，本地 profile 不宣称内置这些连接器。
- 自有 API/UI、实时事件、操作审计、成本计量和插件安装/回滚。

P1 可增加：远程 Runner、GitLab/Forgejo、更多代码客户端、云存储、移动端和更复杂的调度策略。

### 2.2 设计原则

| 原则 | 工程约束 |
| --- | --- |
| ADRO owns facts | WorkItem、PipelineRun、Evidence、Context 和审计事实只写 ADRO Store；外部 provider 的 ID 只能写绑定表。 |
| plugin first | 执行、发现、记忆、压缩、Git、CI、部署、通知、存储、身份、调度都实现 SPI；核心不能 import 厂商 SDK。 |
| fail closed | 能力缺失、证据关联不唯一、签名无效、session 连续性无法证明时，暂停并说明原因，不能把未知当成功。 |
| durable before side effect | dispatch、工具调用、状态迁移、通知和压缩先写幂等记录/Outbox，再执行外部副作用。 |
| least privilege | 插件、Agent 和 Runner 只获得被授权的 tenant/project/repository、工具、网络和 Secret 引用。 |
| observable by default | 每个 attempt、context 版本、provider 调用、事件和 Artifact 都可追溯到 hash、时间和策略版本。 |
| real evidence | 发布验收使用真实客户端、GitHub/CI、测试环境和 IM；测试 double 只用于 SPI 单测，不能作为生产 profile。 |

### 2.3 实现边界

- 只使用 Git、OCI、OpenTelemetry、CloudEvents、MCP、HTTP 和 POSIX 等公开标准；核心不依赖某个供应商的私有协议。
- 外部适配器必须通过版本化 SPI、签名验证、权限审查、SBOM 和 conformance test 后才能启用。
- 外部系统的数据库、任务 ID、UI 和运行时状态不能成为 ADRO 的业务主键或事实来源。
- 发布前生成 SPDX SBOM、NOTICE、依赖许可证报告，并由维护者做商标、专利和许可证审阅。

## 3. 总体架构

```text
Web UI / OpenAPI / DingTalk / Feishu / Webhook
                    |
              Edge Gateway
                    |
        +-----------+-----------+
        |                       |
     Control API           Event Gateway
        |                       |
        +-----------+-----------+
                    |
             ADRO Kernel
     domain / policy / idempotency / audit
        |          |             |
        |      Workflow       Context
        |       Engine        Compiler
        |          |             |
   Plugin Runtime--+--------Execution Gateway
        |                         |
  Registry/Health/Policy     Provider SPI
        |                  Codex / Claude / other plugins
        |                         |
   Git/CI/Test/Deploy/Notify/Artifact/Knowledge plugins
                    |
             Runner Supervisor
                    |
      isolated worktree + local or remote process
```

### 3.1 分层和依赖方向

| 层 | 责任 | 依赖限制 |
| --- | --- | --- |
| Kernel | ID、时钟、错误、策略、状态迁移、幂等、Outbox、审计 | 只依赖标准库和协议类型 |
| Domain | WorkItem、Pipeline、Context、Evidence、Repair 规则 | 不依赖 HTTP、数据库驱动或插件实现 |
| Store | PostgreSQL/SQLite/文件的 Repository 实现 | 只能通过 domain repository 接口访问 |
| Plugin runtime | manifest、加载、权限、版本、健康、升级、隔离 | 不可读取控制数据库，不可绕过 policy |
| Workflow | 阶段图、队列、lease、等待、重试、回退、补偿 | 通过 SPI 发起副作用 |
| Context | 记忆提取、检索、压缩、归档、预算、上下文编译 | 不直接调用模型；压缩器是插件 |
| Execution | 启动 session/attempt、收集 stdout、工具事件和退出原因 | 不决定业务门禁，不写业务状态 |
| Evidence | 从 Git/CI/部署/测试/Artifact 独立核对结论 | 禁止使用 Agent 文本作为唯一来源 |
| Edge/UI | 对外接口、实时投影、权限过滤、下载 | 不直连 provider 或 Runner |

初期可以把这些层编译进一个 Go 模块化单体，以 package 和 contract test 固定边界；吞吐上升后再拆成 control-api、workflow-worker、execution-gateway、event-gateway 和 artifact-service。拆分不能改变协议和数据不变量。

### 3.2 推荐技术栈

| 组件 | P0 选择 | 生产替换 |
| --- | --- | --- |
| 服务端 | Go、chi、OpenAPI 3.1、sqlc/pgx | 保持 API/SQL contract |
| 状态库 | 本地 SQLite 或原子 JSON 单节点 | PostgreSQL 17 + RLS |
| 工作流 | 可测试的数据库状态机 | Temporal 或等价 durable workflow |
| 事件 | Outbox + 内存事件总线 | NATS JetStream/Kafka |
| Artifact | 文件系统 driver | S3/OSS/OBS/COS 等对象存储 |
| 实时 | SSE/WebSocket cursor | 同协议的事件网关 |
| 观测 | OpenTelemetry、Prometheus、结构化日志 | Loki/Tempo/集中 SIEM |
| 前端 | ADRO 自有 React/TypeScript 或现有无依赖工作台 | 同一 API，不嵌入外部 UI |
| 策略 | Go policy evaluator | OPA/Rego 或企业策略服务 |

## 4. 插件化平台

### 4.1 插件形态

支持三种运行形态，功能契约一致：

1. **in-process**：经过签名和静态审查的 Go module，适合内置核心驱动，性能最好。
2. **local-process**：插件 SDK 通过 stdio/Unix socket 或 loopback gRPC 通信，适合本地 CLI 和社区插件。无 Docker 也可运行。
3. **isolated-worker**：生产环境可选 rootless container、VM 或 Kubernetes worker，具备 CPU、内存、磁盘、网络和 syscall 限制。Docker 不是本地 profile 的前置条件。

插件不能因运行形态不同而获得额外业务权限。所有调用携带 tenant、project、work_item、session、attempt 和 capability token；服务端再次核验。

### 4.2 Manifest

插件目录只允许包含 manifest、签名、实现入口、迁移说明、README、许可证和 conformance 测试，不允许带入控制面数据库文件或私有密钥。

```json
{
  "id": "com.example.codex-executor",
  "display_name": "Codex CLI executor",
  "kind": "execution_provider",
  "version": "1.0.0",
  "api_version": "adro.plugin.v1",
  "platform": ["darwin-arm64", "linux-amd64"],
  "entrypoint": {"type": "local_process", "command": "adro-codex-plugin"},
  "capabilities": ["session.start", "session.resume", "stream.events", "cancel"],
  "permissions": ["workspace.read", "workspace.write", "process.exec", "git.commit"],
  "network": {"egress": ["api.openai.com"]},
  "config_schema": "schemas/config.json",
  "signature": {"algorithm": "ed25519", "key_id": "community-key-1", "file": "plugin.sig"}
}
```

`id` 全局稳定且不可复用；`version` 遵守 SemVer；`api_version` 只在兼容窗口内协商；display name 可以改但不可作为标识。manifest 的 capability、permission、配置 schema 和二进制 digest 必须写入 `plugin_installations`，并进入发布清单。

### 4.3 生命周期

```text
discovered -> downloaded -> verified -> installed -> configured -> enabled
                                              |             |
                                      incompatible      disabled
enabled -> draining -> upgraded/rolled_back -> enabled
any state -> quarantined (crash, signature, policy, conformance failure)
```

安装流程：校验来源 allowlist 和签名 -> 解包到版本目录 -> 解析 manifest/schema -> 静态风险扫描 -> 跑 contract test -> 写安装记录 -> 由管理员显式 enable。插件不可在下载后自动获得执行权。

健康检查至少返回 `healthy/degraded/unavailable/quarantined`、版本、能力、最近错误、检查时间和建议动作。连续 crash、超时、越权或 hash 变化自动 drain/quarantine，已有 attempt 保留证据，新任务不再分配。

### 4.4 Capability negotiation

每次 dispatch 前比较：平台协议版本、插件声明、实际探测结果、租户策略、项目策略和本次任务所需能力。能力取交集，不支持的动作显式返回 `capability_unavailable`。例如 provider 没有 `session.resume` 时，修复请求必须暂停，不能偷偷新建独立 session。

能力结果缓存必须带 `plugin_digest`、配置版本和探测时间；二进制或配置变化立即失效。API 提供能力矩阵和拒绝原因，方便 UI 显示“为什么未执行”。

### 4.5 安全、信任和权限

- 默认只信任内置 key、管理员配置的社区 key 和组织私有 key；未知签名进入 quarantine。
- permission 是 allowlist，包含资源范围、读/写、命令、网络、Secret 引用、Artifact 类型和最大并发；不能用通配符越过 tenant/project 边界。
- 插件永远拿不到原始 token，只能拿短时 capability token 或 secret reference；日志、事件和上下文做脱敏。
- local-process profile 使用显式工作目录、环境白名单、命令 argv（不经过 shell）、超时和进程组回收；生产 profile 还必须使用 rootless/VM 隔离。
- 插件升级采用新目录 + 健康检查 + 灰度分配；保留上一版本和 manifest，可按安装记录回滚。不能覆盖正在运行的版本。

### 4.6 社区 SDK、注册表和兼容套件

公开 `sdk/` 提供 Go/TypeScript/Python（按需要）类型、manifest 模板、错误码、签名工具、测试 fixture 和本地运行器。插件注册表只保存元数据、digest、许可证、签名、兼容版本和安全公告，不保存租户密钥。

每个插件提交必须通过：manifest schema、静态扫描、权限最小性、重复/乱序事件、超时/取消、重启、敏感信息、版本升级/回滚和真实 adapter smoke。平台版本升级先运行全部已安装插件的 conformance；不兼容的插件会被禁用并给出迁移说明。

## 5. 插件模块清单

| 插件 SPI | 第一方实现 | 作用 |
| --- | --- | --- |
| `ExecutionProvider` | Codex、Claude Code 及社区插件 | 创建/恢复 session，流式事件，取消，usage，运行快照 |
| `ClientDetector` | 本地 CLI detector | 发现可执行文件、版本、hash、能力和权限 |
| `AgentRouter` | rule/capacity router | 按角色、项目、客户端能力和并发路由 Agent |
| `WorkflowEngine` | DB state machine | 阶段、transition、lease、等待、重试和补偿 |
| `MemoryStore` | SQLite/PostgreSQL/file | working/session/project/semantic/artifact memory |
| `ContextCompressor` | deterministic + model-assisted | 抽取、预算、压缩、精确归档和恢复 |
| `SourceControl` | GitHub Git、GitLab Git | clone、branch、diff、commit、PR/review |
| `CIEvidence` | GitHub Actions、通用 webhook | check run、suite、coverage、结论和链接 |
| `TestRunner` | shell/pytest/go/browser runner | 单测、集成、契约、E2E、日志和报告 |
| `Deployer` | local/k8s/ssh | 测试环境版本部署、健康、回滚 |
| `ArtifactDriver` | filesystem、S3-compatible | immutable、hash、Range、retention、下载 |
| `NotificationSink` | DingTalk、Feishu、email | 通知、交互回执、重试、签名和去重 |
| `Skill/MCP/Knowledge` | 内置 registry + 外部 server | Agent 工具和知识访问；数据源不作为核心菜单 |
| `Identity/Secret` | local、OIDC、Vault/KMS | 登录、租户声明、短时凭证和密钥引用 |
| `Scheduler` | manual、webhook、cron | 自动化触发、去重和配额 |
| `Audit/Observability` | OTel + append-only audit | 指标、trace、审计链和合规留存 |

## 6. 本地客户端自动发现

### 6.1 扫描流程

```text
管理员授权扫描
  -> 收集 PATH + 配置目录 + 显式路径（不递归整个磁盘）
  -> 识别文件类型/权限/平台/架构
  -> 执行 --version 或协议探针
  -> 计算二进制 sha256 与来源
  -> 读取 capability/auth 状态（不打印 secret）
  -> 匹配 ClientDetector/ExecutionProvider
  -> 写发现快照，等待策略批准
```

默认扫描：`PATH`；用户/系统配置目录；管理员显式的目录列表。禁止扫描 SSH key、token、浏览器 profile 等敏感目录。扫描器只读取 metadata 和版本输出；任何带 secret 的 stdout/stderr 都在入库前过滤。

### 6.2 发现记录

```go
type ClientInstallation struct {
    ID             string
    TenantID       string
    Name           string // codex, claude-code, ...
    Executable     string
    Version        string
    Platform       string
    BinarySHA256   string
    Source         string // PATH, configured, package-manager
    Capabilities   []string
    AuthState      string // unknown, missing, valid, expired, denied
    PermissionState string // pending, approved, denied
    DetectorVersion string
    ObservedAt     time.Time
}
```

`Executable + BinarySHA256 + detector_version` 是一次探测的 provenance。PATH 顺序不决定信任级别；hash 变化、版本变化、文件消失或权限变化会产生 `client.changed` 事件并重新审批。Unsupported client 仍在 UI 显示为“已发现但不可用”，不会悄悄当成 Codex 执行。

### 6.3 Codex/Claude Code adapter 要求

两种 adapter 都实现相同的 `ExecutionProvider`：启动时传入 session、attempt、workspace、context manifest digest、工具权限和预算；事件统一为 ADRO envelope；退出时返回结构化 result、usage、stdout/stderr Artifact 和 provider provenance。

适配器不得假设私有内部 session 文件格式。若客户端支持 resume，保存其公开的 thread/session 引用和版本；若不支持 resume，必须由 ADRO harness 以 ContextManifest 重建并标记 `provider_process_continuity=false`，然后由策略决定是否允许继续。无证据时不能声称“同 session”。

### 6.4 扫描与执行策略

- 首次发现只创建候选，不自动执行；管理员按租户/项目批准并配置 allowlist。
- 每个 AgentDefinition 指定 executor、版本范围、能力要求、预算、工具和网络策略。
- 同一个人可在不同项目并行；公平调度按 tenant/project/member/agent capacity 和资源锁计算，不把成员当执行进程。
- 运行中 binary hash 变化立即阻止新 attempt；当前进程完成或被安全终止，旧证据保留。

## 7. Harness：记忆、上下文压缩与编排

### 7.1 三类记忆

| 层级 | 生命周期 | 内容 | 读写规则 |
| --- | --- | --- | --- |
| Working memory | 单次 turn/attempt | 当前目标、工具调用、临时变量、预算 | 进程内可变，但 checkpoint 前必须持久化摘要和引用 |
| Session memory | 一条 ADRO session | 需求、设计、对话摘要、失败证据、决策、repair history | tenant + session 隔离；删 session 按 retention 和审计规则处理 |
| Project/semantic memory | 跨 session | 项目规范、架构约束、稳定事实、偏好、知识引用 | 必须有来源、scope、confidence、version 和 supersession |

所有大文本、日志、截图和报告放 ArtifactStore；Memory 只存可检索摘要、结构化事实和 Artifact 引用。`ContextManifest` 是某次 provider 调用的完整输入目录，不把进程内缓存当作事实源。

### 7.2 ContextManifest

```json
{
  "id": "ctx_01...",
  "session_id": "ses_01...",
  "version": 12,
  "parent_version": 11,
  "purpose": "developer.repair",
  "objective": "修复集成测试失败并保持 API 兼容",
  "baseline": [{"repository_id":"repo-a","commit":"abc"}],
  "head": [{"repository_id":"repo-a","commit":"def"}],
  "memory_refs": ["mem-constraint-1", "mem-decision-3"],
  "archive_refs": ["archive-window-7"],
  "failure_refs": ["failure-42"],
  "artifact_refs": ["design-1", "test-report-8"],
  "tool_policy_digest": "sha256:...",
  "budget": {"input_tokens": 50000, "output_tokens": 12000},
  "compiled_hash": "sha256:..."
}
```

Manifest 创建后不可变；修复、压缩、策略变化只创建新 version。每次 provider dispatch 持久化 `prompt_hash`、`tool_policy_digest`、`context_hash`、`wire_request_hash` 和 parent attempt，便于重放审计且不落原始密钥。

### 7.3 压缩前关键信息抽取

压缩顺序固定为“抽取 -> 验证 -> 落库 -> 精确归档 -> 生成替代上下文 -> checkpoint”。抽取器从消息、工具结果和阶段产物中识别：

- Fact：已经确认的值、接口、版本、代码约束。
- Constraint：不可违反的安全、兼容、性能和范围限制。
- Decision：为何采用某方案、被拒绝的方案和决策人。
- Preference：用户明确的交互/输出偏好。
- FailureDigest：失败命令、环境、堆栈、输入 commit 和重现步骤。
- AttachmentDigest：附件类型、hash、页/行范围和检索索引。

候选 memory 必须带 source message/Artifact 引用、evidence hash、confidence、scope 和 `supersedes`。敏感信息、未经授权的个人数据和模型猜测不得进入长期 memory。结构化 fact、search projection 和 memory event 通过同一事务提交；任一失败全部回滚。

### 7.4 压缩策略

触发条件：预估 token 达到模型安全阈值、阶段切换、管理员手动请求、provider 返回 context overflow，或上下文项超过大小/数量预算。压缩器按以下顺序构造替代窗口：

1. 永久保留 pinned constraint、当前目标、未解决失败、基线/head、验收标准和未完成 transition。
2. 以精确 archive 保存被移除的原始消息、工具结果和附件范围；原文永不被摘要覆盖。
3. 读取已验证 memory 和上一次 summary，生成去重、带来源的结构化摘要。
4. 保留最近若干 turn 的原文，避免正在进行的工具调用被截断。
5. 计算 replacement hash，检查覆盖的 archive window 是否完整；不完整或摘要小于必要边界时 fail closed。

压缩记录包括 `window_id`、精确起止 cursor、strategy、source hash、replacement hash、removed count、retained tail、summary tokens、parent compaction、触发原因和结果。多次压缩形成 DAG，不允许重叠窗口、缺失父节点或 cycle。压缩完成后立即更新 Context Status，显示预估 token、窗口、剩余预算、次数和最近摘要。

### 7.5 检索和上下文编译

Context Compiler 按任务目的选择内容，而不是把整个历史塞给 Agent：

- Planner：需求、项目规范、影响图、开放问题、相关历史决策。
- Developer：设计、接口契约、baseline/head、代码 diff、失败证据、修复历史。
- Tester：验收标准、部署版本、测试命令、已通过/失败用例、环境和数据断言。
- Analyst：问题定义、指标口径、授权 Skill/MCP、查询结果 Artifact、假设/结论证据。
- Repairer：原 session manifest、失败 digest、最小复现、最近 diff、上次修复结果和重试预算。

选择器先按强制 pinned/constraint，再按 scope、evidence、时效、语义/词法相关性和 token cost 排序；每个注入项附 citation。编译结果超预算时先压缩可选项；若仍超预算或缺关键项，拒绝 dispatch 并说明缺口。

### 7.6 崩溃恢复

turn 开始、每次工具调用前后、压缩前后、外部副作用前后写 checkpoint。恢复时从最后一个合法 checkpoint 验证 event hash、context version、outbox 和 attempt lease：

- 未 dispatch 的副作用重新以相同 idempotency key 尝试。
- 已 dispatch 但无完成事件的调用先查询 provider 状态，禁止盲目重复。
- 运行进程丢失时保留 `provider_process_continuity`，按能力决定 resume、重建或人工升级。
- session 删除遵循 retention/legal hold；session memory、archive、citation、context 和 trace 不产生孤儿记录。

本仓库的单节点 profile 已提供上述 harness 的可运行基线：
`internal/harness` 以原子快照持久化完整 turn 链（`prev_hash`/`hash`）、
checkpoint、精确 archive window、带 source turn 的 memory item，以及 lease
和 outbox 的 claim/ack/nack/过期回收；`internal/harness.Dispatcher` 在发布
成功后才确认副作用。`/api/v1/sessions/*` 暴露增量 transcript、context
compile/status、compact 和 recover。PostgreSQL/NATS/Temporal 版本对应
`migrations/004_harness.sql` 与 `sdk/harness` 契约，仍需目标企业提供真实
适配器、故障注入和 RTO/RPO 证据后才能解除生产 gate。

## 8. 流程编排与同 session 自动修复

### 8.1 逻辑模型

```text
Requirement/Analysis/Bug
        |
   intake -> plan -> develop_or_analyze -> unit_test
        -> deploy_test -> integration_test
        -> evidence_gate -> report -> accepted
                              |
                         failure/bug
                              v
                     arbiter -> repair(develop)
                              |
                  retry cap -> human_exception
```

`PipelineRun.session_id` 在创建时生成并全程不变。每一次阶段执行有新的 `stage_attempt_id`；每次 provider 调用有 `provider_attempt_id`。`session_id` 表示业务上下文连续性，`run_id` 表示一次流程实例，`attempt_id` 表示一次尝试，不能混用。

### 8.2 失败回流协议

1. Tester 写入不可变 `failure_evidence`：用例、命令、环境、日志 Artifact、失败时间、commit/head、fingerprint、严重级别和重现步骤。
2. Arbiter 只依据 Evidence 和策略分类：可自动修复、需要补充信息、基础设施失败、权限/安全失败或人工升级。
3. 可自动修复时创建新的 `repair_attempt`，但 `session_id`、`logical_work_item_id` 和 `origin_development_attempt_id` 必须等于原链路；`parent_session_id` 不得被替换为新随机 session。
4. Repairer/Developer 收到新的 ContextManifest version，里面必须包含原设计、对话摘要、baseline/head、失败日志、通过用例、当前 diff、所有 repair history 和明确修复目标。
5. 可以创建新的 worktree 或 provider process 来隔离尝试，但必须将其挂到原 session，并记录 `provider_session_id`、`provider_work_dir` 和 `continuity_proof`。只有 provider 返回的 session 与原绑定一致，或 ADRO 明确完成可验证的 harness 重建，才允许继续。
6. 修复提交后重新执行受影响单测、完整单测、集成测试和必要的回归；每轮结果生成新 EvidenceBundle，不覆盖失败证据。
7. 达到 `max_repair_attempts`、预算、时间或风险阈值后进入 `human_exception`，通知负责人并保留“为何没有继续”的证据。禁止无限循环。

### 8.3 连续性证明

```json
{
  "session_id": "ses-1",
  "origin_provider_session_id": "codex-thread-9",
  "repair_provider_session_id": "codex-thread-9",
  "origin_context_hash": "sha256:a",
  "repair_parent_context_hash": "sha256:a",
  "continuity_mode": "provider_resume",
  "provider_process_continuity": true,
  "worktree_id": "wt-2",
  "proof_hash": "sha256:p",
  "verified_at": "2026-08-30T...Z"
}
```

`continuity_mode` 取 `provider_resume`、`harness_rebuild`、`new_unrelated_session`。最后一种永远不能作为自动 repair 通过；前两种都要能重放 manifest、hash 和 provenance。若适配器只返回一个状态字符串，ADRO 必须标记 `continuity_unproven` 而暂停。

### 8.4 并行和资源锁

不同项目、不同仓库和无冲突阶段可以并行；同一 repository/branch 的写操作必须持有 lease。Agent capacity、Runner capacity、tenant quota 和 provider rate limit 在 dispatch 前原子预留，在完成、取消和重试时只结算一次。并行任务共享只读项目 memory，不能共享可变 working memory 或 Secret。

## 9. 持久化 schema 和不变量

以下表是逻辑 schema；P0 单节点可以映射到现有 Store，企业版迁移到 PostgreSQL + RLS。所有表含 `tenant_id`，所有时间使用 UTC，所有可变记录含 `revision`/`updated_at`。

| 表 | 核心字段 | 不变量 |
| --- | --- | --- |
| `work_items` | id、kind、project_ids、acceptance、status | kind/status 有限状态；删除只软删 |
| `pipeline_runs` | id、session_id、work_item_id、state、policy_version | 一个逻辑输入只能有确定的 active run；session 不变 |
| `stage_attempts` | id、run_id、stage、attempt_no、agent、executor、status | `(run_id,stage,attempt_no)` 唯一；终态不可覆盖 |
| `context_manifests` | id、session_id、version、parent、compiled_hash | version 单调；parent 必须存在且不形成 cycle |
| `memory_items` | scope、type、content/projection、source、confidence | source/hash 必填；supersession 可追溯；tenant 隔离 |
| `context_archives` | window/cursor、raw artifact、digest | 原文 immutable；窗口不可重叠或缺失 |
| `failure_evidence` | fingerprint、commit、tests、log_artifact、conclusion | 失败记录不可删除/覆盖；可引用多个 attempt |
| `repair_attempts` | origin attempt、session、context、reason、outcome | origin session 必须一致；次数受策略约束 |
| `provider_bindings` | kind、native_id、capabilities、version | native ID 只在本表/adapter 内出现 |
| `plugin_installations` | plugin_id、version、digest、signature、state | 同一 id/version/digest 不重复；启用前 verified |
| `plugin_capabilities` | plugin、capability、observed_at、probe_hash | 二进制/配置变化使旧结果失效 |
| `client_installations` | executable、version、binary_hash、auth_state | hash 变化产生 change event 并重新审批 |
| `evidence_bundles` | kind、source、digest、observed_at、refs | 结论必须有来源；同一 source 不可静默覆盖 |
| `artifacts` | key、version、sha256、media_type、retention | immutable write；租户和策略校验下载 |
| `outbox_events` | idempotency_key、payload_hash、published_at | 同 key 不可写入不同语义；可重放 |
| `audit_events` | actor、action、resource、before/after、prev_digest | append-only hash chain；敏感值脱敏 |

关键唯一约束：`(tenant_id, idempotency_key)`、`(tenant_id, session_id, version)`、`(run_id, stage, attempt_no)`、`(plugin_id, version, digest)`。任何状态迁移必须在事务中校验当前 revision、写 transition、审计和 Outbox；失败全部回滚。

## 10. API 与事件契约

### 10.1 对外 API

稳定路径为 `/api/v1`，响应统一包含 `request_id`、`trace_id`、`revision` 和错误分类。核心接口：

```text
POST /work-items                         创建需求/Bug/分析
POST /work-items/{id}/runs               启动流水线（幂等）
GET  /runs/{id}                          ADRO provider-neutral snapshot
GET  /runs/{id}/events?cursor=           cursor 增量事件
POST /runs/{id}/cancel                   取消
POST /bugs/{id}/repair                   触发同 session repair
GET  /work-items/{id}/evidence           EvidenceBundle
GET  /sessions/{id}/context/status       预算/压缩/记忆状态
POST /sessions/{id}/compact               手动压缩
GET  /plugins / POST /plugins/install     插件治理
GET  /clients/discovered                 本地客户端发现结果
POST /clients/scan                       授权扫描
GET  /agents / GET /projects              自有配置和路由
POST /webhooks/{provider}                签名 webhook 入口
```

`GET /runs/{id}` 只有在 ADRO 自己的状态、session provenance 和必要 evidence 完整时才返回 `conclusion=passed`。缺失字段时返回结构化 `capability_unavailable`/`evidence_pending`，不能从 provider task message 猜测。

### 10.2 ExecutionProvider SPI

```go
type ExecutionProvider interface {
    Manifest(context.Context) provider.Manifest
    Health(context.Context) provider.Health
    Capabilities(context.Context, CapabilityRequest) (CapabilitySet, error)
    StartSession(context.Context, StartSessionCommand) (SessionBinding, error)
    ResumeSession(context.Context, ResumeSessionCommand) (SessionBinding, error)
    Dispatch(context.Context, DispatchCommand) (ProviderAttempt, error)
    StreamEvents(context.Context, EventCursor) (EventStream, error)
    Snapshot(context.Context, string) (ProviderSnapshot, error)
    Cancel(context.Context, string) error
    Usage(context.Context, string) (Usage, error)
}
```

`DispatchCommand` 只接受 `ContextManifest` digest、权限 token、workspace binding、budget 和 idempotency key，不接受任意 shell 字符串。Provider 返回的 session、attempt、workdir、exit reason、usage 和 raw artifact URI 必须可核验；不能把 provider 的业务状态直接当 ADRO 状态。

### 10.3 其他 SPI 样例

```go
type ClientDetector interface {
    Detect(context.Context, ScanScope) ([]ClientInstallation, error)
    Probe(context.Context, ClientInstallation, ProbePolicy) (CapabilitySet, error)
}

type ContextCompressor interface {
    Extract(context.Context, ExtractInput) ([]MemoryCandidate, error)
    Compact(context.Context, CompactInput) (CompactionResult, error)
    Compile(context.Context, CompileInput) (CompiledContext, error)
}

type EvidenceCollector interface {
    Collect(context.Context, EvidenceRequest) (EvidenceBundle, error)
    Verify(context.Context, EvidenceBundle) (VerificationResult, error)
}

type NotificationSink interface {
    Send(context.Context, Notification) (DeliveryReceipt, error)
    VerifyWebhook(context.Context, WebhookRequest) (WebhookEvent, error)
}
```

### 10.4 事件 envelope

事件采用 ADRO 自有 envelope，借鉴公开事件思想但不复制任何上游格式：

```json
{
  "event_id": "evt_01...",
  "event_type": "stage.attempt.completed",
  "schema_version": 1,
  "tenant_id": "tenant_1",
  "session_id": "ses_1",
  "run_id": "run_1",
  "attempt_id": "att_3",
  "sequence": 48,
  "occurred_at": "2026-08-30T...Z",
  "idempotency_key": "attempt:att_3:completed",
  "payload_hash": "sha256:...",
  "payload": {"stage":"integration_test","status":"failed"}
}
```

消费端按 `event_id`/idempotency 去重，按 `sequence` 检测乱序和缺口；缺口通过 cursor replay 补齐。旧 schema 只能在兼容窗口内解码，不能静默丢字段。事件发布失败由 Outbox 重试，状态事实已经提交但通知未送达时显示 delivery pending。

## 11. Git、CI、部署、Artifact 与证据

### 11.1 Git/CI 证据桥

Git provider 插件负责 branch、commit、diff、PR/review；CI 插件负责 check run/suite、coverage、日志和结论。API polling 用于补偿，webhook 用于实时收集；每个 webhook 校验签名、时间窗和 replay nonce。Run 与 branch/PR 通过 ADRO 生成的 correlation ID、repository、head SHA 和 attempt 关联，关联不唯一则 fail closed。

外部执行插件如果没有中立 snapshot，只能读取其已公开的 status/session/workdir；Git Evidence Bridge 在 ADRO 侧独立查询 commit、checks、PR，再组装自己的 `RunSnapshot`。未来插件原生提供同一能力时可直通，但仍保留证据验证层。

本地执行器的终态至少区分 `completed`、`failed`、`cancelled` 和
`timed_out`。`ADRO_EXECUTOR_TIMEOUT` 只约束真实子进程；本地 pipeline
collector 另有 `ADRO_PIPELINE_WATCH_TIMEOUT`（默认 30 分钟）约束
`waiting_provider`。watchdog 到期会先读取最后快照：有合法
`ADRO_RESULT_JSON` 就按正常状态机处理，否则取消仍在运行的 provider，保存
session/worktree/输出证据，并将 pipeline 挂起且写入可审计原因，绝不留下无限
等待状态。超时不是模型声称失败，也不会被归并成普通退出码错误。

### 11.2 EvidenceBundle

一个可推广的通过结论至少包含：

- Requirement/Bug/Analysis、session、run、attempt 和 policy version。
- 每个仓库 baseline/head commit、branch、作者身份和时间。
- 运行的命令 argv、环境 digest、测试版本、单测/集成/E2E 结果、覆盖率。
- 测试环境 deployment version、健康检查、curl/日志/数据断言 Artifact。
- CI check conclusion、PR/submission URL、webhook/API 观察时间和 source hash。
- 失败/修复轮次、修复 session continuity proof、前后 diff 和最终报告。

Agent result 只能作为待验证输入；EvidenceCollector 必须向 Git/CI/部署系统重新读取并核对。任何缺失、过期、签名无效或 source 不可信都进入 `evidence_pending`，而不是绿色通过。

### 11.3 ArtifactStore

Artifact key 必含 tenant、session、kind、version；写入后计算 SHA-256、媒体类型、大小和 lineage。支持 immutable、Range/HEAD、保留期、legal hold、访问审计和脱敏预览。插件只收到临时下载 token，不收到 bucket root credential。上传分片在 complete 时校验每片和最终 hash；迁移采用 double-write、digest 对账、可恢复 checkpoint 和回滚窗口。

## 12. 身份、安全与隔离

- 所有查询绑定认证上下文的 `tenant_id`、`project_id` 和资源 scope；PostgreSQL 使用 RLS，缓存 key、事件 subject、Artifact key 同样分区。
- Agent 是执行主体，但权限由人/项目策略授权；不能因 Agent 身份绕过审批。员工不是 shell 操作员，UI 不提供无审计的任意命令入口。
- Secret 只以 reference 进入 ContextManifest；插件通过短时 lease 解析，过期立即失效。日志、prompt、事件和截图执行 secret/PII redaction。
- Runner 每个 attempt 使用独立 worktree、临时目录和进程组；命令采用 argv，禁止未审计 shell 拼接；网络使用 provider/项目 allowlist。
- GitHub App/PAT、CI token、钉钉/飞书 webhook secret 分离权限和轮换；webhook 需 HMAC/签名、nonce、时间窗和重复检测。
- 插件安装、权限变更、客户端发现批准、dispatch、resume、repair、证据结论、下载和管理员操作写入 append-only audit chain。

威胁测试必须覆盖：恶意插件、恶意 Skill/MCP、prompt injection、跨租户 IDOR、路径穿越、命令注入、Artifact 越权、伪造 webhook、重放事件、二进制替换、超额并发和 context 污染。

## 13. UI 与对外操作面

ADRO 使用自己的 UI，不 iframe、跳转或复用任何外部 WebUI。执行角色不做成“研发/测试/数分”人工菜单；这些是 Agent role，员工在统一交付图中查看结果、配置策略和处理例外。核心菜单与需求基线保持一致：

| 菜单 | 主要操作 |
| --- | --- |
| 工作台 | 跨项目运行、阻塞、质量门禁和待处理例外 |
| 需求 | Requirement/Analysis 创建、指定项目/成员/Agent、验收标准 |
| Bug | 从测试证据建 Bug、关联原需求、查看 repair timeline |
| 人工验收 | 只展示需要人决策的风险、证据和批准动作 |
| 方案评审 | 查看 Planner 产物、影响图和版本差异 |
| 执行 | Agent session、阶段、实时事件、暂停/取消 |
| Diff | 多仓 baseline/head、修复前后和 PR |
| 测试 | 用例、命令、部署、覆盖率、失败堆栈、CI 结论 |
| 项目与仓库 | 成员多对多、分支策略、环境和权限 |
| Agent | 自定义 AgentDefinition、角色、模型、Skill/MCP、预算 |
| MCP | MCP server、工具 schema、审批、健康和调用审计 |
| Skills | Skill/Knowledge 安装、版本、权限和热加载 |
| 自动化 | cron/webhook/manual 触发、去重、策略和运行历史 |
| 集成 | Git、CI、部署、钉钉、飞书及社区插件 |
| Artifact | 方案、日志、报告、截图、hash、保留期和下载 |
| Runner | 本地/远程执行器、客户端发现、容量和健康 |
| 成本 | token、时长、模型、插件、项目和预算 |
| 系统管理 | 租户、成员、OIDC、插件信任、审计、备份、升级 |

运行详情页必须同时显示 `session_id`、`run_id`、`stage_attempt_id`、上下文版本、provider continuity、证据状态和下一动作。没有证据时显示具体缺口，不显示“完成”绿标。

## 14. 真实测试和发布门禁

### 14.1 测试层级

1. Unit：状态机、hash、压缩窗口、错误分类、权限和 serializer；允许 test doubles。
2. Contract：每个插件运行相同 conformance suite，覆盖 manifest、能力协商、幂等、乱序、取消、重启、secret redaction 和版本升级。
3. Integration：真实本地 Codex/Claude Code（已配置时）、真实 Git repository、真实 GitHub App/PAT、真实 CI、真实测试环境和真实钉钉/飞书 webhook。当前仓库只提供本地执行器和 provider-neutral 契约；未安装外部连接器或没有凭证的用例只能标记 `blocked_external_prerequisite`，不能伪造 PASS。
4. Failure injection：进程崩溃、数据库断电、Outbox 重复、网络超时、provider 消失、webhook 乱序、客户端升级、上下文超预算、测试失败和 repair cap。
5. E2E：从空租户提交 Requirement/Bug/Analysis，到方案、Agent 执行、提交、部署、测试失败、同 session repair、复测、EvidenceBundle、报告和通知。
6. Security/performance：跨租户 IDOR、插件逃逸、并发公平、事件吞吐、Artifact Range、长 session 压缩、恢复 RTO/RPO 和成本预算。

### 14.2 不可伪造的验收矩阵

| 场景 | 必须证明 |
| --- | --- |
| 自动发现 | PATH/显式目录发现 Codex/Claude；version/hash/capability/auth 可见；未批准不执行 |
| 需求闭环 | 单次提交产生一个稳定 session；阶段按策略自动推进，无人工创建子任务 |
| 多项目并行 | 同一成员、不同成员、不同项目可并行；仓库写锁和配额生效 |
| 记忆 | 压缩前抽取隐含约束/决策；原文 archive 可回取；ContextManifest 可重放 |
| Bug 回流 | 测试失败生成 fingerprint/evidence；repair 使用原 session 和 context parent；新 attempt 不新建逻辑 session |
| 连续性失败 | provider 无 resume/证据时 `continuity_unproven`，流程暂停并告警 |
| 代码/CI | baseline/head、check conclusion、coverage、PR URL 均由真实 Git/CI API 或验签 webhook 核对 |
| 重启 | 任意 checkpoint 后杀进程，恢复不重复提交/通知/结算，不丢失败证据 |
| 插件治理 | 签名、权限、隔离、健康、禁用、升级、回滚和兼容失败均可审计 |
| 通知 | 钉钉/飞书真实签名发送、重试、去重、交互回执和失败补偿 |

### 14.3 质量声明边界

本地单测和 deterministic fixture 只能证明机制。只有在固定模型、预算、工具、权限、客户端版本和真实仓库上完成可重复实验，才能声明召回率、成功率、延迟或成本。任何尚未完成的 adapter、隔离、HA、备份、真实外部服务在发布报告中必须标为 `reference-only` 或 `blocked`。

## 15. 交付阶段

| 阶段 | 交付物 | 退出条件 |
| --- | --- | --- |
| Wave 0 | clean-room 记录、SPI、manifest、内核状态机、单节点存储 | contract test、许可证和边界扫描通过 |
| Wave 1 | Agent/Client discovery、Codex executor、Runner、安全策略 | 真实本地客户端探测并完成一次 session |
| Wave 2 | Context memory、compression、archive、compiler、恢复 | 压缩/重启/召回探针与 hash 账本通过 |
| Wave 3 | GitHub/CI/Test/Deploy/Evidence、Requirement/Bug pipeline | 真实代码提交、测试和 PR 证据通过 |
| Wave 4 | same-session repair、并行调度、报告、Artifact | 故意失败后原 session 多轮修复闭环通过 |
| Wave 5 | DingTalk/Feishu、OIDC、PostgreSQL/NATS/Temporal、远程 Runner | 外部适配器安装、真实凭证、企业安全、故障和迁移门禁通过；未完成时保持 `reference-only`/`blocked` |
| Wave 6 | 社区注册表、更多 provider、开源发布 | 插件 conformance、SBOM、文档和支持流程齐全 |

## 16. 风险与决策

| 风险 | 影响 | 处理 |
| --- | --- | --- |
| 客户端没有公开 resume 或 snapshot | 无法证明同 session/代码证据 | 使用 harness rebuild 或 fail closed；不伪造 provider continuity |
| 模型输出不稳定 | 方案/修复质量波动 | 结构化 schema、证据门禁、失败分类、预算和人工升级 |
| 插件恶意或质量差 | 数据泄露/执行逃逸 | 签名、权限、隔离、静态扫描、quarantine、conformance |
| 上下文过大/压缩失真 | 修复回归、错误决策 | 先抽取再压缩、精确 archive、pinned、召回探针和 fail closed |
| Git/CI webhook 丢失或乱序 | 错误通过/重复触发 | API 补偿、cursor、签名、nonce、idempotency 和 source hash |
| 单机存储故障 | 任务和审计丢失 | checkpoint、备份恢复；生产升级 PostgreSQL/NATS/对象存储 |
| 外部生态范围过大 | 延期且无法验证 | 只承诺交付闭环 P0；外围能力以插件和 P1 排期 |

明确决策：不修改或依赖任何外部编排产品，不把父子任务树作为 ADRO 核心，不把数据源做成独立重点菜单，不把员工当研发/测试执行人，不用 Docker 作为本地启动前提，不用 mock provider 作为真实产品验收，不把一次成功的 Agent 文本当作交付证据。

## 17. 实现检查表

- [ ] `sdk/` 中所有 SPI 有版本、错误码、manifest schema 和 contract test。
- [ ] 所有业务表带 tenant scope，所有外部 ID 收敛在 binding 表。
- [ ] dispatch 前有 context/policy/tool hash，副作用前有 Outbox 和 idempotency。
- [ ] session、run、stage attempt、provider attempt 的语义和数据库约束已落地。
- [ ] 压缩前抽取、精确 archive、pinned、DAG 防重叠和召回率探针已覆盖。
- [ ] 测试失败自动写 Evidence 并回到原 session；连续性不明时 fail closed。
- [ ] Codex/Claude Code 扫描不读 secret，版本/hash 变化会重新审批。
- [ ] GitHub API/webhook 独立核对 commit/check/PR；报告带 source/time/hash。
- [ ] 钉钉/飞书、Artifact、Runner、插件升级和审计均能重试、回滚和追踪。
- [ ] 无 Docker 的本地 profile 可启动；生产 profile 对未提供的隔离/HA/Secret adapter 明确阻断。
- [ ] 真实外部测试结果与 unit/mock 结果分离，未满足的项不会被写成 PASS。

本文件与 `docs/product-requirements.zh-CN.md` 同时作为开发基线：需求文档定义要解决什么问题和 UI/范围，本文定义如何实现、如何扩展以及如何证明没有丢失核心能力。实现过程中如需偏离，必须更新两份文档、补充迁移和验收证据后再合并。
