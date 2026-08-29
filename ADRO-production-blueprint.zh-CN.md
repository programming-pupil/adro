# ADRO

## Agentic Delivery & Release Orchestrator

中文定位：企业级智能研发交付编排平台

版本：1.1

状态：Architecture Baseline

目标读者：后端、前端、基础设施、测试、安全、开源维护人员

本文是可直接进入研发排期、架构评审和开源仓库的技术规格。实现团队可以将每个章节拆成 Epic、Service、API、UI 和测试任务。

---

## 1. 产品定义

### 1.1 名称与项目身份

项目正式工作名为 **ADRO**，展开为 **Agentic Delivery & Release Orchestrator**。

```text
产品名        ADRO
仓库名        adro
CLI           adroctl
协议包前缀    adro.*
镜像前缀      adro-*
默认命名空间  adro
```

ADRO 是产品名称，不是 “AI DevOps” 的泛化缩写。它只承诺一件事：把需求可靠地编排为可审查、可验证、可恢复的多仓研发交付过程。

公开发布前必须完成 GitHub 组织、主流包管理器、OCI registry、域名、社交账号和主要市场商标清查。工作名通过清查后冻结；冻结后协议包名和数据库标识不再随品牌变化。显示名称与内部稳定标识必须分离，避免未来品牌调整破坏 API。

### 1.2 产品目标

建设一个独立的企业 AI 研发交付控制面，覆盖：

```text
需求/bug 进入
  -> 指定人员
  -> 自动映射 Agent、团队和多仓工作区
  -> 方案设计
  -> 自动或人工 Review
  -> 多仓开发
  -> 单测与接口文档
  -> Test 环境部署
  -> curl、日志、数据验证
  -> 测试失败自动回到原开发上下文
  -> 通过后生成自动测试报告
  -> 交给专业测试人员人工验收
```

平台的业务事实、审批、证据、上下文、成本和实时事件由本系统拥有。Multica 是首个 Agent 执行 Provider，可以被替换为其他执行后端。

### 1.3 核心价值

产品核心不是复制一个任务看板，也不是为 Agent 编写更长的 Prompt，而是：

- 将一个需求可靠地拆分为多个项目、仓库和责任人。
- 让设计、开发、测试和 Bug 修复成为可恢复的确定性流程。
- 用真实 Commit、测试报告、部署版本、curl 响应、日志和数据校验作为质量证据。
- 在测试失败或人工提 Bug 时，自动定位原需求、原 Work Item 和原开发 Agent。
- 为任何 Provider 提供统一的实时会话、代码 Diff、运行状态和 Token 成本视图。

### 1.4 产品内核与旗舰体验

ADRO 的开源辨识度必须集中在三个可演示、可测量的承诺，而不是菜单数量：

1. **一个需求，一张跨仓交付图。** 指定人员和候选项目后，ADRO 建立版本化多仓工作区，实时显示每个仓库为什么需要修改、正在修改什么以及依赖顺序。
2. **每个“通过”都有证据。** Commit、测试命令、JUnit、curl 请求和响应、日志查询、数据断言、部署版本共同形成不可变 EvidenceBundle；Agent 的自述不能代替证据。
3. **每个 Bug 回到原交付上下文。** 无论原会话是否仍在，都能通过 Provenance、ContextManifest、Commit 和失败差异恢复到原开发责任、原变更范围和最新证据。

首个公开版本必须提供一个可复现的旗舰示例：`feign-contract`、`provider-service`、`caller-service` 三仓联动。示例需完整演示接口变更、影响分析、并行改造、故意引入测试失败、自动回流修复和最终证据报告。演示数据、脚本、期望事件和基准结果都放入仓库，禁止只发布录屏。

开箱体验分为两条路径：

- `adroctl up --demo`：不需要云存储、Git 或模型 Key，使用本地仓库和 MockProvider，在 10 分钟内走完完整流程，用于评估产品和验证部署。
- `adroctl install --profile single-node`：完成可持久运行的服务器安装；平台本身零配置启动，连接真实 Git、模型、CI 和测试环境时，只要求配置对应外部系统凭证。不能把外部系统天然需要的凭证宣传成“完全零 Key”。

### 1.5 非目标

以下内容不属于平台第一版的独立核心，避免重复建设或形成升级负担：

- 不复制 Multica 的全部聊天、Issue、Runtime 和低层管理页面。
- 不直接读取或修改 Multica PostgreSQL 数据库。
- 不依赖 Multica 未版本化的前端内部组件或私有表结构。
- 不把完整聊天记录永久复制给每一个 Agent。
- 不允许无限自动修复循环。
- 不把 Git Author 伪装成真人身份作为审计依据。

---

## 2. Multica 集成事实与边界

### 2.1 当前可复用能力

Multica 当前适合作为执行底座的能力包括：

- Agent：绑定 Runtime、模型、指令、MCP、Skill 和并发上限。
- Project/Resource：一个 Project 可以挂多个 `github_repo` 或 `local_directory` 资源。
- Issue：父子任务、状态、评论、运行历史和 PR 关联。
- Stage：子任务屏障和阶段完成唤醒。
- Squad：由 leader 路由任务，成员不会自动 fan-out。
- Autopilot：schedule、webhook、manual 触发，以及 `create_issue`、`run_only` 两种执行模式。
- Runtime/Daemon：在指定机器运行 Agent CLI、创建 Worktree、执行工具命令。
- WebSocket 和 run messages：提供运行消息和增量读取能力。
- Token usage：运行记录包含 input、output、cache token、模型和 Agent 等信息。
- Plugin/Skill/MCP：可接入部署、日志、测试和企业系统。

### 2.2 必须由控制面补齐的能力

- 一个需求关联多个 Project、多个仓库和多个责任人。
- 人员到 Agent、Team Workspace、仓库权限、Git identity 的映射。
- 业务级工作流状态机、审批、超时、幂等、重试和熔断。
- 测试证据模型和质量门禁。
- 自动测试失败 Bug 的归因、去重和回流。
- 人工 Bug 文档到原开发 Agent 的定位。
- 统一的实时事件协议和断线补偿。
- 多租户成本、审计、配额和权限。
- 集中的 Runner/Worker 安全隔离和容量调度。
- 企业级页面和对 Multica 低层对象的聚合视图。

### 2.3 Provider 可替换原则

业务代码禁止直接调用 Multica API。所有执行后端都实现以下 SPI：

```text
ExecutionProvider
  ├── MulticaProvider
  ├── MockProvider
  └── Future providers: native-agent, remote-runner, CI-agent
```

Multica 的 ID 只出现在 `provider_bindings`，不得作为业务主键。业务主键统一使用 UUID/ULID。

---

## 3. 总体架构

```text
Browser / Feishu / DingTalk / OpenAPI Client
                       |
                       | HTTPS / WebSocket
                       v
                 Edge Gateway
                       |
       +---------------+----------------+
       |                                |
       v                                v
 Web Console                       Control API
                                           |
        +------------------+---------------+------------------+
        |                  |                                  |
        v                  v                                  v
 Delivery Domain     Workflow Engine                    Execution Gateway
 Requirement/Bug     Temporal                           Provider SPI
 Project/Approval                                      MulticaProvider
        |                  |                                  |
        v                  v                             Multica API/WS
 Control PostgreSQL  Temporal DB                             |
        |                                             Multica Backend
        v                                                     |
 Event Gateway <-------- NATS JetStream <----------- Daemon/Runtime
        |                                                     |
        v                                                     v
 Read Models                                        Central Runner Pool
        |                                                     |
        v                                                     v
 Browser updates                         Git / CI / Test / K8s / Logs

 Artifact Service -> ArtifactStore SPI -> filesystem/S3-compatible/OBS/OSS/COS/BOS
 Observability    -> OpenTelemetry/Prometheus/Loki/Tempo
 Secrets          -> Vault/KMS/Kubernetes Secrets
```

### 3.1 部署单元与责任

| 部署单元 | 责任 | 明确不负责 |
| --- | --- | --- |
| `edge-gateway` | TLS、HTTP 路由、限流、WebSocket Upgrade、静态资源 | 业务状态 |
| `web-console` | 企业 UI、会话、Diff、审批和报告展示 | 直接访问 Multica |
| `control-api` | 需求、Bug、项目、成员、权限和 Read Model | 运行 Agent |
| `workflow-worker` | Temporal Workflow/Activity、等待、重试、补偿 | 页面渲染 |
| `execution-gateway` | Provider SPI、幂等、能力探测、版本兼容 | 业务决策 |
| `event-gateway` | Provider 事件采集、标准化、去重、回放 | 业务审批 |
| `artifact-service` | 方案、日志、报告、Diff、文档存储 | 任务调度 |
| `repository-indexer` | 仓库清单、符号、API、依赖和配置索引 | 决定需求范围 |
| `impact-resolver` | 根据索引和运行证据生成可解释 ImpactGraph | 未经策略批准自动扩仓 |
| `extension-gateway` | 扩展注册、协议、权限、健康和隔离 | 允许扩展直连控制数据库 |
| `runner-supervisor` | Runner 注册、容量、租户隔离、任务目录 | 需求状态 |
| `workspace-observer` | 文件监听、Git Diff、测试进度采集 | 修改代码 |
| `integration-hub` | Git、CI、部署、日志、飞书、钉钉适配 | 流程事实 |
| `usage-service` | Token、时长、成本、归集和配额 | Agent 选择 |
| `policy-audit` | RBAC/ABAC、审批策略、审计 | 身份认证本身 |

初始版本可以将 `control-api`、`artifact-service`、`usage-service`、`policy-audit` 作为模块化单体，保持模块和数据边界不变，后续再按负载拆分。

---

## 4. 推荐技术栈

### 4.1 服务端

| 领域 | 选择 | 说明 |
| --- | --- | --- |
| 语言 | Go 1.26 | 并发、部署简单、与 Multica backend/daemon 生态一致 |
| HTTP | chi + oapi-codegen | OpenAPI 3.1 先定义，生成服务接口和类型 |
| RPC | Connect RPC + Protobuf | 浏览器友好、mTLS、版本兼容性明确 |
| Workflow | Temporal | 长流程、人工等待、重试、补偿、版本化 |
| 主库 | PostgreSQL 17 | 事务、JSONB、全文和审计索引 |
| 数据访问 | pgx + sqlc | 显式 SQL、编译期类型检查、避免重量 ORM |
| 事件 | NATS JetStream | 低延迟、持久化、at-least-once、回放 |
| 代码索引 | Zoekt + tree-sitter + SCIP | 文本检索、语法事实和跨仓符号引用 |
| 依赖图 | PostgreSQL adjacency tables | 数百到数千仓规模先避免引入独立图数据库 |
| 产物存储 | ArtifactStore SPI；默认本地文件系统 | 领域层不感知 S3、OBS、OSS、COS、BOS 或 MinIO |
| 身份 | OIDC；SAML 由 IdP 桥接 | 单机 profile 内置预配置 Keycloak，企业可替换现有 IdP |
| 策略 | OPA/Rego | 项目和环境级策略可审计 |
| Schema | golang-migrate | 控制面独立、前向兼容迁移，不触碰 Multica schema |

### 4.2 前端

| 领域 | 选择 |
| --- | --- |
| Framework | Next.js 16 App Router |
| 语言 | TypeScript |
| UI | Radix UI + Tailwind CSS；封装为 ADRO Design System |
| 编辑器 | Monaco Editor / Monaco Diff |
| 数据请求 | TanStack Query |
| 表单 | React Hook Form + Zod |
| 实时 | 原生 WebSocket 客户端，带 cursor 恢复 |
| 图形 | React Flow，用于任务 DAG 和项目依赖图 |
| 测试 | Playwright、Vitest、Storybook |

### 4.3 Runner/Agent

| 领域 | 选择 |
| --- | --- |
| Runner supervisor | Go |
| 隔离 | rootless container；高风险场景使用 VM/独立 Unix 用户 |
| 工作区 | Git bare cache + per-run Worktree |
| 文件事件 | fsnotify + Git status/diff |
| 任务通信 | mTLS gRPC streaming |
| 供应商 | Codex、Claude Code 等由 Multica Runtime 驱动 |
| 命令证据 | JSONL command envelope + exit code |

### 4.4 运维与安全

```text
OpenTelemetry -> Prometheus -> Grafana
                           -> Loki
                           -> Tempo/Jaeger
Vault/KMS       -> Secret references
Cosign          -> image signatures
Syft/Grype      -> SBOM and vulnerability scan
SLSA            -> build provenance
```

### 4.5 ArtifactStore SPI 与零配置存储

#### 领域边界

需求、上下文、事件和 EvidenceBundle 只能保存平台逻辑地址：

```text
artifact://<tenant>/<artifact-id>/<version>
```

业务表、事件和 Prompt 禁止保存 `s3://`、厂商 bucket URL 或本地绝对路径。`artifact-service` 负责把逻辑地址解析到实际后端，校验租户和权限，并以统一的 HTTP Range/stream 接口向浏览器和 Runner 提供内容。这样更换存储不需要改 Workflow、ContextManifest 或历史业务数据。

元数据、hash、分类、保留策略和后端定位符保存在 PostgreSQL；二进制内容保存在所选 ArtifactStore。Evidence 类产物默认不可覆盖，只能创建新版本。

#### 稳定接口

```go
type ArtifactStore interface {
    Capabilities(context.Context) (ArtifactCapabilities, error)
    Put(context.Context, ArtifactKey, io.Reader, PutOptions) (ObjectMeta, error)
    Open(context.Context, ArtifactKey, ByteRange) (io.ReadCloser, ObjectMeta, error)
    Stat(context.Context, ArtifactKey) (ObjectMeta, error)
    Delete(context.Context, ArtifactKey, DeleteOptions) error
    Health(context.Context) error
}
```

预签名 URL、multipart、object lock 和 server-side encryption 是 capability，不得成为所有实现的强制前提。浏览器下载优先由 Artifact Service 签发短期平台 Token；底层支持时才使用厂商预签名 URL。

#### 内置驱动与默认策略

| Driver | 适用场景 | 凭证方式 | 发布要求 |
| --- | --- | --- | --- |
| `filesystem` | 默认单机、离线环境、开发 | 无；使用持久卷和 OS 权限 | 必须内置，零配置 |
| `s3-compatible` | AWS S3、MinIO、Ceph RGW、Garage、R2 等 | Workload Identity/实例角色优先，其次 Secret Reference | 核心发行版内置 |
| `aliyun-oss` | 阿里云 OSS | RAM Role 或 Secret Reference | 官方适配器 |
| `huawei-obs` | 华为云 OBS | 委托/临时凭证或 Secret Reference | 官方适配器 |
| `tencent-cos` | 腾讯云 COS | CAM Role 或 Secret Reference | 官方适配器 |
| `baidu-bos` | 百度智能云 BOS | 临时凭证或 Secret Reference | 官方适配器 |
| `remote` | 社区或企业自研存储 | 由扩展声明 | 通过 Artifact Driver SDK 接入 |

不能假定所有国内对象存储都完整兼容 S3。兼容时可配置 `s3-compatible`，不兼容或语义有差异时使用原生 Driver。官方适配器必须通过同一套 contract test，包括 Range、并发上传、hash、超时、重试、租户隔离和故障恢复。

`filesystem` 只承诺单节点语义。若 Artifact Service 配置多副本，readiness 必须拒绝普通本地卷；生产 HA 必须使用具备共享一致性的外部 Driver。不能让两个副本各自写本地目录后仍报告健康。

#### 零配置单机配置

```yaml
artifact_store:
  driver: filesystem
  filesystem:
    root: /var/lib/adro/artifacts
```

`adroctl install --profile single-node` 自动创建具名持久卷、目录、权限和备份提示，不启动 MinIO，也不要求 Access Key。MinIO 作为可选 `s3-compatible` 部署配置；只有用户显式选择时才启动或连接，并在发行时单独审查其当期许可证、镜像来源和 NOTICE。

生产配置示例：

```yaml
artifact_store:
  driver: s3-compatible
  s3:
    endpoint: https://s3.example.com
    region: cn-east-1
    bucket: adro-artifacts
    credential_ref: vault://adro/artifact-store
    path_style: false
```

云环境优先使用实例角色、Pod Identity 或 Workload Identity；此时 `credential_ref` 可以省略。静态 AK/SK 只能以 Secret Reference 提供，不能写入 YAML、数据库明文字段或 UI 回显。

#### 第三方扩展协议

社区 Driver 使用独立进程的版本化 gRPC/Connect 协议，通过 Unix Socket 或 mTLS 连接。核心进程不得动态加载不受信任的 Go `.so`。每个扩展提供：

```text
extension.yaml
OCI image + digest/signature
protocol version
capabilities
configuration JSON Schema
secret fields
network/filesystem permissions
health endpoint
SBOM and license
```

仓库必须发布 `artifact-driver-sdk`、参考 Driver 和可独立运行的兼容性测试套件。未通过 contract test 的 Driver 在 UI 中标记为 `unverified`，且默认不能用于 production workspace。

#### 在线迁移

`adroctl artifact migrate --from <source> --to <target>` 执行：冻结配置版本、开启新写双写、分页回填、逐对象 hash 校验、切换读优先级、观察错误率、停止双写。回滚窗口内保留源对象，不自动删除。迁移状态和校验失败必须可恢复，不能要求一次长事务完成。

#### 安全、生命周期和备份

- backend key 使用平台生成的 opaque ID，禁止拼接用户文件名，防止路径穿越和跨租户猜测。
- 上传先写临时对象，完成 size/hash/恶意文件检查后原子发布；失败临时对象由可审计 GC 清理。
- `classification=confidential/restricted` 的内容使用 envelope encryption；KEK 来自 SecretStore/KMS，数据密钥不与对象放在同一位置。
- Evidence 的保留和删除由 Policy 决定；legal hold 或 immutable 内容不能被普通管理员删除。
- Artifact 下载必须重新做租户、项目、需求和 classification 授权，知道 URI 不等于有下载权限。
- 备份必须记录 PostgreSQL snapshot 与 ArtifactStore checkpoint 的一致性水位；恢复后全量或抽样验证 hash。
- Artifact 不可用时 Workflow 进入可恢复的 `BLOCKED_ARTIFACT_STORE`，不得把缺失证据当作测试通过。

---

## 5. 核心领域模型

### 5.1 关键实体

```text
Tenant
  -> Workspace
      -> Member
      -> Team
      -> DeveloperProfile
      -> AgentBinding
      -> Repository
      -> RepositorySnapshot
      -> ImpactReport
      -> TeamWorkspace
      -> Requirement
      -> Bug
      -> WorkflowRun
      -> ExecutionRun
      -> EvidenceBundle
      -> Artifact
      -> Approval
```

### 5.2 关系模型

```text
DeveloperProfile
  -> one default Agent
  -> many TeamWorkspace
  -> many Repository responsibility edges
  -> one Git identity policy

Requirement
  -> many RequirementAssignees
  -> many RequirementRepositories
  -> many versioned ImpactReports
  -> one DeliveryManifest
  -> many WorkItems

WorkItem
  -> one Requirement or Bug
  -> one Project/Repository
  -> one responsible Member
  -> one developer Agent
  -> many ExecutionRuns
  -> many Artifacts/EvidenceBundles

Bug
  -> one source Requirement (optional for production defects)
  -> one source WorkItem (optional until triage)
  -> one original Agent (resolved through provenance)
  -> many RepairAttempts
```

### 5.3 最小 SQL 表设计

下面是逻辑表，不要求一开始完全按物理表拆分，但字段含义必须保留。

```sql
create table developer_profiles (
  id uuid primary key,
  workspace_id uuid not null,
  member_id uuid not null,
  default_agent_binding_id uuid,
  git_identity jsonb not null,
  status text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (workspace_id, member_id)
);

create table team_workspaces (
  id uuid primary key,
  workspace_id uuid not null,
  name text not null,
  version bigint not null,
  policy jsonb not null,
  status text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
);

create table repositories (
  id uuid primary key,
  workspace_id uuid not null,
  canonical_name text not null,
  clone_url text not null,
  provider text not null,
  default_branch text not null,
  language_set jsonb not null,
  metadata jsonb not null,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (workspace_id, canonical_name)
);

create table repository_snapshots (
  id uuid primary key,
  repository_id uuid not null,
  commit_sha text not null,
  index_version text not null,
  manifests jsonb not null,
  api_contracts jsonb not null,
  symbol_artifact_uri text,
  indexed_at timestamptz not null,
  unique (repository_id, commit_sha, index_version)
);

create table repository_edges (
  workspace_id uuid not null,
  source_repository_id uuid not null,
  target_repository_id uuid not null,
  edge_type text not null, -- build, api, symbol, config, runtime, ownership
  evidence jsonb not null,
  confidence numeric(5,4) not null,
  snapshot_commit text not null,
  updated_at timestamptz not null,
  primary key (source_repository_id, target_repository_id, edge_type, snapshot_commit)
);

create table impact_reports (
  id uuid primary key,
  requirement_id uuid not null,
  version bigint not null,
  input_snapshot jsonb not null,
  candidate_repositories jsonb not null,
  confirmed_repositories jsonb not null,
  unresolved_risks jsonb not null,
  produced_by_run_id uuid,
  created_at timestamptz not null,
  unique (requirement_id, version)
);

create table requirements (
  id uuid primary key,
  workspace_id uuid not null,
  key text not null,
  title text not null,
  description text not null,
  acceptance_criteria jsonb not null,
  priority text not null,
  status text not null,
  created_by uuid not null,
  version bigint not null,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (workspace_id, key)
);

create table requirement_assignees (
  requirement_id uuid not null,
  member_id uuid not null,
  role text not null,
  is_primary boolean not null default false,
  primary key (requirement_id, member_id, role)
);

create table requirement_repositories (
  requirement_id uuid not null,
  repository_id uuid not null,
  relation text not null, -- primary, dependency, test_only, review_only
  source text not null,   -- user, analyzer, reviewer
  confidence numeric(5,4),
  primary key (requirement_id, repository_id)
);

create table work_items (
  id uuid primary key,
  requirement_id uuid,
  bug_id uuid,
  repository_id uuid not null,
  member_id uuid not null,
  developer_agent_binding_id uuid not null,
  provider_issue_id text,
  status text not null,
  stage int not null,
  baseline_commit text,
  head_commit text,
  branch_name text,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  check (requirement_id is not null or bug_id is not null)
);

create table provenance (
  id uuid primary key,
  work_item_id uuid not null,
  requirement_id uuid,
  bug_id uuid,
  agent_binding_id uuid not null,
  provider text not null,
  provider_agent_id text,
  provider_task_id text,
  provider_session_id text,
  repository_id uuid not null,
  baseline_commit text,
  head_commit text,
  context_version bigint not null,
  created_at timestamptz not null
);

create table evidence_bundles (
  id uuid primary key,
  workspace_id uuid not null,
  work_item_id uuid not null,
  kind text not null, -- unit, deploy, api_test, log, data, review
  status text not null,
  summary jsonb not null,
  artifact_uri text,
  content_sha256 text not null,
  producer_run_id uuid not null,
  created_at timestamptz not null
);

create table artifact_stores (
  id uuid primary key,
  workspace_id uuid, -- null means platform default
  driver text not null,
  config_version bigint not null,
  config jsonb not null, -- secret references only
  capabilities jsonb not null,
  status text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
);

create table artifacts (
  id uuid not null,
  tenant_id uuid not null,
  version bigint not null,
  store_id uuid not null,
  backend_locator text not null, -- only artifact-service may interpret
  media_type text not null,
  size_bytes bigint not null,
  content_sha256 text not null,
  classification text not null,
  retention_until timestamptz,
  immutable boolean not null default false,
  metadata jsonb not null,
  created_at timestamptz not null,
  primary key (id, version)
);

create table artifact_migrations (
  id uuid primary key,
  source_store_id uuid not null,
  target_store_id uuid not null,
  state text not null,
  cursor text,
  copied_count bigint not null default 0,
  verified_count bigint not null default 0,
  failed_count bigint not null default 0,
  started_at timestamptz,
  updated_at timestamptz not null
);

create table repair_attempts (
  id uuid primary key,
  bug_id uuid not null,
  attempt_no int not null,
  fingerprint text not null,
  work_item_id uuid not null,
  agent_binding_id uuid not null,
  input_evidence_id uuid not null,
  output_evidence_id uuid,
  status text not null,
  created_at timestamptz not null,
  unique (bug_id, attempt_no),
  unique (bug_id, fingerprint)
);
```

### 5.4 业务状态机

需求状态：

```text
RECEIVED
  -> TRIAGED
  -> ASSIGNEES_CONFIRMED
  -> DESIGNING
  -> DESIGN_REVIEW
  -> DEVELOPING
  -> UNIT_VERIFIED
  -> API_DOC_READY
  -> TEST_DEPLOYING
  -> TESTING
  -> READY_FOR_HUMAN_QA
  -> ACCEPTED
  -> RELEASED
```

失败和人工介入状态：

```text
DESIGN_REWORK
TEST_FAILED
AUTO_REPAIRING
HUMAN_TRIAGE_REQUIRED
HUMAN_APPROVAL_REQUIRED
BLOCKED_PROVIDER
BLOCKED_ENVIRONMENT
BLOCKED_ARTIFACT_STORE
CANCELLED
```

每次状态变更必须有：actor、reason、evidence references、correlation_id 和 optimistic version。

---

## 6. 服务 API 与通信协议

### 6.1 外部 REST API

所有 API 前缀为 `/api/v1`，请求必须带 `Authorization` 和 `X-Request-ID`。写请求必须带 `Idempotency-Key`。租户身份来自经过验证的 OIDC claim；`X-Tenant-ID` 只允许作为多租户成员的选择器，并且必须与 token 权限求交集，绝不能被直接信任为授权依据。

#### 需求

```http
POST /api/v1/requirements
GET  /api/v1/requirements
GET  /api/v1/requirements/{id}
PATCH /api/v1/requirements/{id}
POST /api/v1/requirements/{id}/start
POST /api/v1/requirements/{id}/approve
POST /api/v1/requirements/{id}/pause
POST /api/v1/requirements/{id}/resume
```

创建请求示例：

```json
{
  "title": "新增邀请接口",
  "description": "activity-service 提供邀请接口并接入奖励规则",
  "assignee_member_ids": ["member-a", "member-b"],
  "repository_ids": ["activity", "common-feign", "user"],
  "acceptance_criteria": [
    "POST /invite 返回 code=0",
    "重复邀请幂等",
    "日志不出现 ERROR"
  ],
  "workflow_template_id": "standard-backend-change"
}
```

#### Bug

```http
POST /api/v1/bugs
GET  /api/v1/bugs
GET  /api/v1/bugs/{id}
POST /api/v1/bugs/{id}/triage
POST /api/v1/bugs/{id}/repair
POST /api/v1/bugs/{id}/verify
```

#### 运行和实时事件

```http
GET /api/v1/runs/{id}
GET /api/v1/runs/{id}/events?cursor=...
POST /api/v1/runs/{id}/cancel
GET /api/v1/work-items/{id}/diff
WS  /api/v1/streams/workspaces/{workspace_id}
```

#### 产物

```http
POST /api/v1/artifacts/uploads
PUT  /api/v1/artifacts/uploads/{upload_id}/parts/{part_no}
POST /api/v1/artifacts/uploads/{upload_id}/complete
GET  /api/v1/artifacts/{id}/versions/{version}
HEAD /api/v1/artifacts/{id}/versions/{version}/content
GET  /api/v1/artifacts/{id}/versions/{version}/content
POST /api/v1/artifact-migrations
GET  /api/v1/artifact-migrations/{id}
POST /api/v1/artifact-migrations/{id}/pause
POST /api/v1/artifact-migrations/{id}/resume
```

上传使用平台 upload session，不向 Runner 暴露底层 bucket 凭证。小文件可单次上传，大文件分片；complete 时校验总大小、每片 hash 和最终 SHA-256。下载端支持 Range、ETag 和条件请求。

#### 项目、仓库和责任人

```http
GET/POST /api/v1/repositories
GET/PATCH/DELETE /api/v1/repositories/{id}
POST /api/v1/repositories/{id}/index
GET  /api/v1/repository-graph?repository_id=...
GET/POST /api/v1/team-workspaces
GET/PATCH /api/v1/developer-profiles/{member_id}
POST /api/v1/requirements/{id}/repositories
POST /api/v1/requirements/{id}/assignees
POST /api/v1/requirements/{id}/impact-reports
POST /api/v1/requirements/{id}/impact-reports/{version}/confirm
```

### 6.2 内部 RPC

内部服务使用 Protobuf，包名包含版本：`adro.execution.v1`、`adro.events.v1`。

```protobuf
service ExecutionGateway {
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);
  rpc StartRun(StartRunRequest) returns (StartRunResponse);
  rpc AppendInput(AppendInputRequest) returns (AppendInputResponse);
  rpc CancelRun(CancelRunRequest) returns (CancelRunResponse);
  rpc GetRun(GetRunRequest) returns (RunSnapshot);
  rpc StreamRunEvents(StreamRunEventsRequest) returns (stream RunEvent);
}

service RunnerService {
  rpc RegisterRunner(RegisterRunnerRequest) returns (RegisterRunnerResponse);
  rpc Heartbeat(stream RunnerHeartbeat) returns (stream ControlCommand);
  rpc Execute(stream RunnerMessage) returns (stream RunnerMessage);
}
```

### 6.3 事件信封

```json
{
  "event_id": "01J...",
  "event_type": "execution.tool.completed.v1",
  "aggregate_type": "execution_run",
  "aggregate_id": "run-uuid",
  "aggregate_version": 42,
  "tenant_id": "tenant-uuid",
  "workspace_id": "workspace-uuid",
  "correlation_id": "requirement-uuid",
  "causation_id": "previous-event-id",
  "provider": "multica",
  "provider_event_id": "provider-task:seq",
  "occurred_at": "2026-08-27T10:00:00Z",
  "classification": "internal",
  "payload": {}
}
```

事件规则：

- JetStream 使用 at-least-once；消费端必须幂等。
- `provider_event_id` 作为 Provider 侧去重键。
- 数据库事务写入和事件发布使用 Transactional Outbox。
- 浏览器事件使用 `(aggregate_id, aggregate_version)` 排序。
- UI 断线使用 cursor 补偿，不依赖页面刷新。
- 大文本和完整 Diff 只在事件中传 Artifact URI、hash 和摘要。

事件类型：

```text
requirement.created.v1
requirement.status.changed.v1
repository.indexed.v1
impact.report.generated.v1
impact.expansion.requested.v1
approval.requested.v1
approval.decided.v1
work_item.created.v1
execution.queued.v1
execution.started.v1
execution.message.delta.v1
execution.tool.started.v1
execution.tool.completed.v1
execution.completed.v1
execution.failed.v1
workspace.file.changed.v1
workspace.diff.updated.v1
test.case.completed.v1
evidence.created.v1
bug.detected.v1
bug.assigned.v1
repair.attempted.v1
usage.updated.v1
report.generated.v1
```

### 6.4 API、事件与扩展兼容规则

- REST 列表统一使用 opaque cursor，响应包含 `items`、`next_cursor`，禁止用 page number 承诺稳定顺序。
- 错误统一为 RFC 9457 Problem Details，增加稳定的 `error_code`、`request_id` 和可选字段错误；客户端不得解析自然语言错误。
- OpenAPI、AsyncAPI 和 Protobuf 在 CI 中做 breaking-change 检查。
- REST v1 只允许增加可选字段；删除、改名和语义改变必须进入新版本。
- 事件消费者忽略未知字段和未知事件类型；同一 `event_type.v1` 内不得改变既有字段语义。
- Extension handshake 必须交换协议版本、capability 和最小/最大兼容平台版本。
- 所有 webhook 都带 timestamp、delivery ID 和 HMAC 签名，并提供重放窗口与去重键。
- API Key 只用于机器集成，按 workspace、scope 和过期时间签发；人类登录统一使用 OIDC。

---

## 7. Multica Provider 适配器

### 7.1 SPI

```go
type ExecutionProvider interface {
    Capabilities(context.Context) (Capabilities, error)
    EnsureAgent(context.Context, AgentSpec) (AgentBinding, error)
    EnsureTeamWorkspace(context.Context, WorkspaceSpec) (WorkspaceBinding, error)
    CreateWorkItem(context.Context, WorkItemSpec) (ProviderWorkItem, error)
    StartRun(context.Context, StartRunCommand) (RunBinding, error)
    AppendInput(context.Context, AppendInputCommand) error
    CancelRun(context.Context, string) error
    GetRun(context.Context, string) (RunSnapshot, error)
    StreamEvents(context.Context, string, string) (EventStream, error)
    GetUsage(context.Context, string) (Usage, error)
    Health(context.Context) (ProviderHealth, error)
}
```

### 7.2 Multica 映射

```text
DeveloperProfile        -> Multica Agent
TeamWorkspace           -> Multica Project + resources
Repository              -> Project github_repo/local_directory resource
Requirement             -> Multica parent Issue
WorkItem                -> Multica child Issue
Workflow stage          -> Issue status + child stage
ExecutionRun            -> Multica task/run
Agent input             -> Issue comment or task input
Provider event          -> WebSocket/run-messages -> normalized event
```

### 7.3 能力探测

Provider 启动时返回：

```json
{
  "provider": "multica",
  "adapter_version": "1.2.0",
  "capabilities": [
    "agent.v1",
    "project.resources.v1",
    "issue.child.v1",
    "issue.stage.v1",
    "run.messages.v1",
    "runtime.worktree.v1",
    "usage.tokens.v1"
  ],
  "server_version": "0.4.35"
}
```

不支持的能力必须在 UI 中显示为“不可用/降级”，不能静默假装实现。

### 7.4 Multica 约束处理

- Project 可以挂多个仓库；控制面维护更细的 `TeamWorkspace` 和需求仓库子集。
- 一个 Multica Issue 只有一个 Project/assignee；多负责人通过父需求和多个 Work Item 表达。
- Squad 任务只路由给 leader；需要 fan-out 时由控制面显式创建多个 Work Item。
- Workspace MCP 库添加后不会自动赋给 Agent；控制面必须显式绑定。
- Workspace Skill 创建后不会自动绑定 Agent；控制面必须显式绑定。
- Autopilot 的 `issue-title-template` 只能使用 `{{date}}`；业务变量由控制面自己渲染。
- `mcp_config`、`custom_env` 是敏感信息，控制面只保存 Secret Reference，不回显明文。

### 7.5 通用 Extension SDK

“Multica 可替换”不能只实现一个 Provider 接口。ADRO 对外发布以下稳定扩展面：

```text
ExecutionProvider   Agent/Runtime/Issue/run events
ArtifactStore       报告、日志、Diff、文档和证据内容
SourceControl       GitHub/GitLab/Gitea/Forgejo/企业 Git
CIPipeline          构建、测试和制品状态
Deployer            Kubernetes/Argo CD/企业发布平台
EvidenceCollector   日志、指标、数据库和消息验证
Notifier            飞书、钉钉、邮件和通用 Webhook
SecretStore         Vault、KMS、Kubernetes Secret 和企业密钥系统
IdentityProvider    OIDC claim/组织/成员同步
```

核心仓库定义协议、SDK、Mock、参考实现和 contract test；厂商实现放在独立 adapter 模块。扩展通过版本化 gRPC/Connect + manifest 运行于独立进程或容器，具备独立资源限制、网络策略和升级周期。禁止扩展直接访问控制面数据库或 NATS；所有调用都经过授权后的 Extension Gateway。

扩展生命周期：

```text
DISCOVERED -> INSTALLED -> CONFIGURED -> VERIFIED -> ENABLED
                                      -> DEGRADED
                                      -> QUARANTINED
                                      -> DISABLED
```

每种 SPI 必须提供 conformance suite。平台升级前，所有已启用扩展先在隔离环境运行兼容性检查；不兼容扩展被阻止升级或自动降级，不能在生产任务中首次发现协议不兼容。

---

## 8. 多仓工作区设计

### 8.1 TeamWorkspace

一个 TeamWorkspace 是可复现的多仓版本快照，不等同于 Multica Project。

```json
{
  "id": "tw-uuid",
  "version": 7,
  "repositories": [
    {
      "repository_id": "common-feign",
      "ref": "main",
      "mount_path": "repos/common-feign",
      "required": true,
      "change_policy": "may_change"
    },
    {
      "repository_id": "activity-service",
      "ref": "release/3.x",
      "mount_path": "repos/activity-service",
      "required": true,
      "change_policy": "must_change"
    }
  ],
  "credential_ref": "vault://git/team-a",
  "network_policy": "test-activity",
  "test_environment": "test-01"
}
```

### 8.2 Runner 生命周期

```text
REGISTERED
  -> HEALTHY
  -> DRAINING
  -> OFFLINE
  -> QUARANTINED
```

Runner 必须报告：能力标签、Provider、版本、CPU、内存、磁盘、并发、工作区根目录和安全域。

### 8.3 工作区执行规则

- 每个 Work Item 使用独立 Worktree 和分支。
- 分支格式：`agent/<member>/<requirement>/<repository>`。
- 基准 Commit、当前 Commit 和 PR 必须写入 `provenance`。
- 同一仓库同一环境默认使用锁，避免部署和测试互相覆盖。
- 多仓库任务完成时输出 `DeliveryManifest`，列出每个仓库的 Commit、PR、测试和状态。
- Worktree 结束后保留 Diff/报告，清理源代码和临时凭证。

### 8.4 Feign 影响校验

成熟版可以接入以下索引，但不能把“调用 A 的所有项目都必须修改”作为规则：

- Maven/Gradle 依赖和版本。
- `@FeignClient`、接口继承、DTO、OpenAPI。
- RestTemplate/WebClient 等 HTTP 调用。
- API Gateway、服务注册和配置中心。
- 链路追踪和网关访问日志。

校验结果示例：

```text
A-service: Provider 新增 POST /invite，必须修改
B-service: 引用了 InviteResponse，需要升级依赖并改造
C-service: 是调用方但未使用新接口，仅需回归测试
D-service: 未发现关联，不创建开发任务
```

### 8.5 Repository Knowledge Graph 与选仓策略

ADRO 支持三种输入，不强迫用户先建设完整索引：

```text
explicit repositories  用户明确给出 1-N 个仓库，直接作为 confirmed seed
assignees only          从责任人的 TeamWorkspace 得到候选集合，不等于全部都修改
automatic discovery    从 workspace 全量索引检索候选，必须给出证据和置信度
```

索引数据按仓库 Commit 版本化，包含：构建清单、内部包依赖、OpenAPI/Proto/Feign 契约、数据库 migration、消息 topic、配置 key、服务注册名、SCIP 符号和可选运行时调用边。Repository Indexer 通过 Git webhook 增量更新，并用低频 reconciliation 修复漏事件；禁止每个需求都把几百个仓库完整发送给模型。

解析流程：

```text
requirement terms + explicit repositories + assignees
  -> deterministic search (Zoekt/manifests/API/symbol graph)
  -> bounded graph expansion
  -> Agent reads only top candidates and exact evidence
  -> ImpactReport version
  -> policy: auto-confirm / human-confirm / explicit-only
  -> immutable confirmed repository set
  -> TeamWorkspace snapshot and WorkItems
```

ImpactReport 的每个候选必须包含 `relation`、`evidence_refs`、`confidence`、`recommended_action` 和 `why_not_change`。推荐动作只允许：`must_change`、`may_change`、`test_only`、`no_change`、`unknown`。置信度不是模型随口给出的数字，而是由可校准规则、历史反馈和模型判断共同产生，并在 ADRO Bench 中测量 precision/recall。

一旦进入开发，Confirmed Repository Set 默认冻结。Agent 发现遗漏仓库时发出 `impact.expansion.requested.v1`，说明代码证据、风险和所需权限；Workflow 按节点策略自动批准或等待人工，不允许 Runner 静默克隆和修改额外仓库。

这套能力是“遗漏检测和解释”，不是用户必须购买的 ImpactReport 流程。用户已经明确指定全部仓库时，系统可以直接执行，只在后台做快速一致性检查；索引不可用时降级为 explicit-only，不阻塞已明确范围的需求。

---

## 9. 研发交付 Workflow

### 9.1 标准流程

```text
1. CreateRequirement
2. ResolveAssigneesAndWorkspace
3. GenerateImpactReport
4. ConfirmRepositories
5. DesignAgent
6. DesignReviewGate
7. CreateWorkItems
8. DeveloperAgent
9. UnitTestGate
10. GenerateApiDoc
11. DeployTestEnvironment
12. ApiLogDataVerification
13. AutoRepairLoop
14. GenerateTestReport
15. ReadyForHumanQA
16. HumanAcceptance
17. OptionalReleaseApproval
```

### 9.2 节点策略

每个节点由 Workflow Template 配置：

```yaml
nodes:
  design_review:
    mode: human
    timeout: 24h
  unit_test:
    mode: auto
    retry: 2
  api_doc:
    mode: auto
  test_deploy:
    mode: auto
    timeout: 30m
  api_test:
    mode: auto
  release:
    mode: human
```

支持模式：

- `auto`：满足机器门禁后自动继续。
- `human`：创建审批请求，等待 Signal。
- `auto_if_confident`：达到置信度和证据阈值才自动继续，否则人工。

### 9.3 质量门禁

每个门禁必须返回机器可判断的 `GateResult`：

```json
{
  "gate": "unit_test",
  "decision": "pass",
  "checks": [
    {"name": "exit_code", "actual": 0, "expected": 0},
    {"name": "junit_failures", "actual": 0, "expected": 0},
    {"name": "coverage", "actual": 0.87, "expected": 0.80}
  ],
  "evidence_ids": ["evidence-uuid"]
}
```

Agent 的自然语言“已通过”不能单独作为通过条件。

### 9.4 自动 Bug 闭环

```text
TestAgent -> EvidenceBundle(failed)
           -> fingerprint
           -> find existing Bug by (requirement, work_item, fingerprint)
           -> create Bug if absent
           -> resolve original WorkItem/Agent from provenance
           -> append structured repair input to original thread
           -> resume session/workdir if available
           -> create new run with RepairBrief if not available
           -> DeveloperAgent fixes
           -> UnitTest
           -> Deploy/Test again
```

保护规则：

- 相同 fingerprint 不重复创建 Bug。
- 自动修复次数默认最多 3 次，可按模板配置到 5 次。
- 超过上限进入 `HUMAN_TRIAGE_REQUIRED`。
- 任何跨仓改动都重新生成 DeliveryManifest。
- 测试环境版本和 Commit 必须固定并可追溯。
- 原 Agent 会话不是唯一记忆来源，必须有 provenance 和短 RepairBrief。

### 9.5 人工 Bug

人工 Bug 文档建议必填：

```text
requirement_key (optional for legacy defects)
repository
environment
version/commit
endpoint or job
steps_to_reproduce
expected
actual
log_excerpt
```

归因顺序：

```text
explicit requirement/work-item key
  -> repository + commit
  -> PR metadata
  -> deployment version
  -> recent provenance
  -> human triage if ambiguous
```

不能根据模糊自然语言盲目选择 Agent。

---

## 10. 上下文与记忆设计

### 10.1 原则

上下文不是一段无限增长的聊天记录，而是“索引 + 版本化产物 + 按需读取”。

```text
ContextManifest
  -> stable summary
  -> requirement/bug references
  -> approved contract
  -> repository/commit references
  -> original developer binding
  -> latest evidence
  -> artifact URIs
```

### 10.2 Context Manifest

```json
{
  "context_id": "ctx-uuid",
  "version": 12,
  "requirement_id": "req-uuid",
  "bug_id": null,
  "stable_summary": "activity-service 新增邀请接口并接入奖励规则",
  "approved_change_contract_id": "contract-uuid",
  "repositories": [
    {"id": "activity", "baseline": "abc123", "head": "def456"}
  ],
  "original_developer": {
    "member_id": "member-a",
    "agent_binding_id": "binding-a",
    "provider_run_id": "run-a"
  },
  "latest_evidence_ids": ["evidence-1", "evidence-2"],
  "artifact_refs": ["artifact://tenant-a/design-uuid/3"],
  "token_budget": 12000
}
```

### 10.3 按阶段注入

| 阶段 | 注入内容 |
| --- | --- |
| 方案设计 | 需求、验收标准、仓库摘要、相关接口和代码搜索结果 |
| 方案 Review | 方案、ChangeContract、风险、依赖和影响证据 |
| 开发 | 已批准契约、目标仓库、精确文件范围、测试要求 |
| 单测 | Commit、测试命令、验收标准、失败差异 |
| 提测 | API 文档、部署参数、环境、测试用例 |
| Bug 修复 | 稳定摘要、Bug、失败 Evidence、原 Commit、相关 Diff |

### 10.4 Token 控制

- 大日志只保留摘要、错误片段和 Artifact URI。
- 代码通过工具按需读取，不把整个仓库放入 Prompt。
- 同一版本的稳定摘要不重复发送。
- 重试只发送新增失败差异。
- 每个节点定义 token budget，超预算触发摘要压缩。
- cache_read、input、output 分开计量。
- 记录每次输入的 `context_hash`，支持成本和效果分析。

评论仍然保留作为人类时间线和 Agent 触发输入，但不能作为唯一结构化事实来源。

---

## 11. MCP、Skills、自动化菜单与实现

这三个能力必须成为一级菜单，但后端模型需要分清：MCP 是工具连接，Skill 是可复用知识/操作手册，Automation 是触发和流程规则。

### 11.1 MCP 管理

#### 菜单

```text
能力中心
  -> MCP 服务器
  -> Skills
  -> 自动化
```

#### MCP 页面

| 页面 | 功能 |
| --- | --- |
| MCP 列表 | 名称、传输类型、状态、拥有者、分配 Agent 数、最近健康检查 |
| 添加 MCP | stdio / SSE / Streamable HTTP、Schema、Secret Reference、网络域名 |
| MCP 详情 | 工具列表、输入 Schema、版本、审批状态、调用统计 |
| Agent 分配 | 给指定 Agent 绑定/禁用/移除 MCP |
| 审批 | 工具级允许、拒绝、变更后重新审批 |
| 健康 | 连通性、延迟、错误率、schema digest |
| 审计 | 谁添加、谁批准、谁调用、调用结果 |

#### MCP 数据模型

```text
mcp_servers
mcp_server_versions
mcp_tools
mcp_agent_bindings
mcp_approvals
mcp_health_checks
mcp_invocation_logs
```

MCP URL、Header、Command、Environment 和 Token 只存 Secret Reference 或加密值；任何列表和详情 API 默认不回显。

#### MCP API

```http
GET/POST /api/v1/mcp/servers
GET/PATCH/DELETE /api/v1/mcp/servers/{id}
POST /api/v1/mcp/servers/{id}/discover
POST /api/v1/mcp/servers/{id}/approve
POST /api/v1/mcp/servers/{id}/health-check
POST /api/v1/agents/{id}/mcp-bindings
PATCH/DELETE /api/v1/agents/{id}/mcp-bindings/{binding_id}
GET /api/v1/mcp/invocations
```

#### 安全规则

- MCP Server 必须声明 exact network scopes。
- 工具 Schema 变化后，旧审批自动失效。
- 默认 deny，未审批工具不能发送到 Agent。
- 运行时临时文件 0600，任务结束清理。
- Tool output 进入敏感信息扫描器。
- 禁止把 MCP Secret 写入 Issue、评论、Prompt、日志或 Git。

### 11.2 Skills 管理

#### Skill 类型

```text
procedure      操作流程，例如部署+curl+查日志
knowledge      领域知识，例如支付服务规范
validator      门禁规则，例如接口文档检查
template       方案、Bug、测试报告模板
```

#### 页面

| 页面 | 功能 |
| --- | --- |
| Skill 市场 | 本地、组织内、外部导入、版本和签名 |
| Skill 编辑器 | `SKILL.md`、支持文件、输入输出契约、版本发布 |
| 分配矩阵 | Workspace/Team/Agent 绑定、禁用和覆盖 |
| 兼容性 | Provider、语言、操作系统、网络权限要求 |
| 使用分析 | 使用次数、成功率、失败原因、Token 增量 |
| 审核发布 | Reviewer、签名、变更说明、回滚版本 |

#### Skill 契约

每个 Skill 必须声明：

```yaml
name: test-and-verify
version: 1.3.0
kind: procedure
inputs:
  repository: repository-ref
  environment: environment-ref
outputs:
  - evidence_bundle
permissions:
  - network:test.api.internal
  - logs:read:test
provider_compatibility:
  - multica
  - native-agent
entrypoint: SKILL.md
```

Skill 内容可以进入 Agent 上下文，但大文件通过引用加载；安装和升级必须可回滚。

#### Skill API

```http
GET/POST /api/v1/skills
GET/PATCH/DELETE /api/v1/skills/{id}
POST /api/v1/skills/{id}/versions
POST /api/v1/skills/{id}/publish
POST /api/v1/skills/{id}/rollback
POST /api/v1/agents/{id}/skill-bindings
```

### 11.3 自动化管理

自动化不是一个 Prompt，而是触发器、条件、动作和重试策略。

#### 页面

| 页面 | 功能 |
| --- | --- |
| 自动化列表 | 状态、触发器、目标、最近运行、成功率 |
| 流程设计器 | DAG 节点、并行、条件、审批、循环上限 |
| 触发器 | Issue created/status changed、Git webhook、CI webhook、schedule、manual |
| 策略 | 自动/人工、置信度、项目/环境限制 |
| 运行历史 | 输入、节点、事件、失败、重试、人工接管 |
| 回放 | 只读重放事件，禁止直接重复外部副作用 |
| 版本 | Draft、Published、Deprecated、Rollback |

#### 自动化数据模型

```text
automation_definitions
automation_versions
automation_triggers
automation_nodes
automation_edges
automation_runs
automation_approvals
automation_dead_letters
```

#### 自动化 API

```http
GET/POST /api/v1/automations
GET/PATCH/DELETE /api/v1/automations/{id}
POST /api/v1/automations/{id}/publish
POST /api/v1/automations/{id}/trigger
POST /api/v1/automations/{id}/pause
GET /api/v1/automations/{id}/runs
POST /api/v1/automation-runs/{id}/cancel
POST /api/v1/automation-runs/{id}/takeover
```

#### 与 Multica Autopilot 的映射

简单 schedule/webhook 可以投影到 Multica Autopilot；复杂 DAG、审批、循环、证据门禁必须由 Temporal 管理。控制面不得把 Autopilot 当作完整业务状态机。

---

## 12. Web UI 菜单与详细设计

### 12.1 全局布局

```text
左侧导航：
交付
  工作台
  需求中心
  Bug 中心
  人工验收
执行
  方案评审
  研发执行
  代码与 Diff
  测试中心
资产
  项目与仓库
  Agent 与小队
能力
  MCP
  Skills
  自动化
  集成中心
  扩展与存储
运营
  Runner 管理
  成本中心
管理
  系统管理

顶部：workspace、全局搜索、事件状态、通知、当前用户
主体：列表/详情 split view
右侧：时间线、证据、审批、关联对象
底部：实时连接状态、Provider、版本
```

导航按角色收敛：开发默认展开“交付/执行”，平台管理员默认展开“能力/运营/管理”。全局搜索和 Command Palette 可以直接进入任何对象；不能为了展示功能把十六个入口同时铺满侧栏。

### 12.2 工作台

显示：

- 进行中需求和当前阶段。
- 阻塞、待审批、待人工测试。
- 最近失败和自动修复轮次。
- Runner 健康和队列。
- 今日 Token、成本和异常。

交互：点击卡片直接跳到需求详情的对应节点；所有数字都能下钻到事件和证据。

### 12.3 需求中心

列表过滤：负责人、项目、仓库、状态、优先级、创建人、时间、是否有 Bug。

详情页：

```text
需求描述 / 验收标准
负责人和 Agent
关联项目与仓库
ImpactGraph、选仓证据和冻结版本
研发 DAG
当前阶段和门禁
方案与 Review
代码 Commit/PR/Diff
测试证据
Bug 回流
实时 Agent 对话
评论和审计
```

创建需求必须能够：

- 指定 1-N 个人。
- 按人自动加载其负责的多个项目。
- 调整本需求的仓库子集。
- 选择 `explicit-only`、`impact-assisted` 或 `auto-if-confident` 选仓策略。
- 对每个候选仓库查看“必须改/可能改/仅测试/不需要改/未知”和代码证据。
- 选择工作流模板。
- 选择是否自动设计、自动 Review、自动修复。
- 设置自动修复次数上限。

### 12.4 Bug 中心

列表显示：来源（自动/人工）、关联需求、项目、原 Agent、当前尝试次数、指纹、严重级别、状态。

详情显示：

- 复现步骤和实际/预期。
- 测试 EvidenceBundle。
- 原需求、Work Item、Commit、PR、部署版本。
- 原开发 Agent 和恢复方式（session/workdir/RepairBrief）。
- 修复原因、修复手段、改动 Diff。
- 单测和回归测试结果。
- 人工接管和转派。

### 12.5 人工验收

只展示自动测试已通过的不可变报告：

- 测试版本和 Commit。
- 环境和部署 ID。
- 测试用例、curl 请求、响应和断言。
- 日志查询和异常计数。
- 数据库/消息队列校验。
- 未解决风险。

操作：通过、打回研发、标记环境问题、要求补充证据、取消需求。

### 12.6 项目与仓库

功能：仓库注册、负责人、默认分支、语言、构建命令、测试命令、部署方式、日志查询、索引状态、依赖关系、Secret Reference。

多仓工作区页面必须显示版本化挂载清单和每个仓库的 `may_change/must_change/test_only` 政策。

Repository Graph 子页展示 API、构建、符号、配置和运行时边；每条边可回溯到 Commit 和证据。允许手工纠正错误边并作为后续校准反馈，但不能直接篡改历史 ImpactReport。

### 12.7 方案评审

显示：

- 方案文档。
- 变更契约。
- 影响仓库和接口。
- 顺序和并行依赖。
- 风险、兼容性和回滚方案。
- 代码证据和搜索命中。

Review 结果必须是结构化的：批准、驳回、要求修改、仅允许部分仓库、转人工专家。

### 12.8 研发执行

这是类似 Codex 的核心页面：

```text
左栏：运行列表和阶段
中栏：Agent 消息、工具调用、终端输出、测试流
右栏：Changed Files、Diff、Commit、PR、证据
底栏：评论、暂停、继续、取消、人工接管
```

要求：

- 消息级实时，能力允许时显示 token delta。
- 事件断线可按 cursor 补偿。
- 文件变化 2 秒内显示 Diff（目标 p95）。
- 支持按仓库、文件、事件类型过滤。
- Tool output 脱敏后再展示。

### 12.9 代码与 Diff

功能：文件树、统一 Diff、并排 Diff、版本切换、Commit/PR、CI 状态、下载 Patch。

代码事实来源是 Git/Provider；Agent 文本只能作为解释，不作为变更事实。

### 12.10 测试中心

功能：环境、部署、测试套件、curl 模板、日志查询模板、数据库校验、EvidenceBundle、报告版本、重试和失败指纹。

测试模板必须声明输入、权限、输出 Schema 和可接受断言。

### 12.11 Agent 与小队

功能：Agent 创建/编辑、Runtime 绑定、模型、指令、MCP、Skills、并发、权限、Squad leader/member、负责人映射。

注意：Squad 成员不是自动 fan-out；页面需要明确显示 leader 路由规则。

### 12.12 Runner 管理

功能：在线状态、版本、能力、并发、队列、磁盘、工作区、隔离策略、排空、隔离、升级和最近错误。

### 12.13 成本中心

维度：租户、workspace、发起人、负责人、需求、Bug、项目、Agent、Provider、模型、日期、自动修复轮次。

显示字段：input、output、cache read、cache write、估算金额、实际金额、任务时长、重试次数。

### 12.14 集成中心

GitHub/GitLab/Gitea/Forgejo、CI、Kubernetes、部署平台、日志、监控、飞书、钉钉、邮件和 Webhook。

每个集成有连接测试、权限范围、Secret Reference、事件订阅和最近错误。

### 12.15 扩展与存储

页面分为：Extension Catalog、已安装扩展、ArtifactStore、兼容性和迁移任务。

- Extension Catalog 显示类型、协议版本、来源、签名、SBOM、许可证、权限和验证状态。
- 安装流程必须先显示网络、文件、Secret 和数据 scope，再由管理员批准。
- ArtifactStore 显示当前 Driver、容量、错误率、延迟、数据量、加密、保留策略和健康状态。
- 切换 Driver 必须创建可暂停/恢复的迁移任务，实时显示双写、回填、hash 校验和切换进度。
- `filesystem` 默认项不要求配置表单；云 Driver 使用动态 JSON Schema 渲染配置，只保存 Secret Reference。
- 扩展升级支持 canary、contract test、回滚和 quarantine，不能直接覆盖正在运行的版本。

### 12.16 系统管理

租户、workspace、成员、角色、项目权限、环境权限、审批人、审计、密钥策略、Provider 版本、升级、备份和恢复。

---

## 13. 实时体验与 Workspace Observer

### 13.1 Provider 事件链

```text
Multica WebSocket
  -> MulticaProvider event adapter
  -> Event Gateway
  -> schema validation + redaction + dedupe
  -> JetStream
  -> Read Model consumers
  -> Browser WebSocket
```

补偿优先级：

1. 官方 WebSocket 实时事件。
2. run messages 的 sequence/cursor 增量读取。
3. Plugin lifecycle hook。
4. 定时 reconciliation 纠偏。

### 13.2 Workspace Observer

Runner 内运行 Observer：

- 监听 Worktree 文件变化。
- 300-500ms debounce。
- 计算 `git diff --stat`、文件 Diff 和 hash。
- 过滤二进制、密钥、`.env` 和超大文件。
- 上传完整 Diff 到 Artifact Store。
- 事件携带摘要、URI、hash 和 changed files。

### 13.3 实时验收目标

| 指标 | 目标 |
| --- | ---: |
| Provider 事件到浏览器 p95 | < 1 秒 |
| 文件变化到 Diff p95 | < 2 秒 |
| WebSocket 重连补偿 | 0 个已确认事件丢失 |
| 事件去重 | 相同 Provider event 只入 Read Model 一次 |
| UI 事件顺序 | 单 aggregate 单调递增 |

如果 Multica 当前只提供消息级事件，产品必须标明“消息级实时”。不能通过解析私有协议冒充 token 级稳定 API；应通过 Provider capability flag 和上游版本化事件协议解决。

---

## 14. 一键部署、无 UI Linux 部署与运行时

### 14.1 产品发行物

```text
adroctl
docker-compose.yml
docker-compose.single-node.yml
charts/adro/
images:
  adro-api
  adro-web
  adro-worker
  adro-event-gateway
  adro-runner
  adro-repository-indexer
  adro-artifact-service
  adro-extension-gateway
  multica-backend (pinned)
  multica-web (optional admin)
  postgres
  temporal
  nats
  keycloak (single-node identity profile)
volumes:
  adro-artifacts (default filesystem driver)
optional profiles:
  s3-compatible object store
```

### 14.2 单机安装

```bash
adroctl install --profile single-node
```

安装器完成：

1. 检查 Docker、CPU、内存、磁盘和端口。
2. 创建配置目录和随机密钥。
3. 拉取锁定版本和镜像 digest。
4. 启动 PostgreSQL、Temporal、NATS 和预配置 Keycloak，创建本地 Artifact 持久卷；不要求对象存储 Key。
5. 启动 Multica backend；管理 Web 可选，不是终端用户入口。
6. 启动 Multica daemon/Runtime 和中央 Runner。
7. 启动控制面 API、Web、Event Gateway、Workflow Worker、Artifact Service、Repository Indexer 和 Extension Gateway。
8. 执行控制面迁移和 Multica 启动检查。
9. 创建初始 workspace 和管理员引导。
10. 等待所有 readiness probe 通过，输出 URL、版本和登录方式。

控制面必须内置 Multica 的启动、健康检查、版本锁定和 Provider 注册；用户不应先手动启动 Multica 再启动控制面。

默认 `filesystem` Driver 足以完成单机生产部署和离线部署。用户显式配置 `s3-compatible`、OBS、OSS、COS、BOS 或远程 Driver 时，安装器先做 capability/权限/读写校验，再切换 Artifact Service；未配置时绝不因为缺少云 Key 阻塞启动。

首次启动生成的本地密钥只用于平台内部服务，并写入权限为 0600 的配置或容器 Secret。真实 Git、模型、CI、部署和日志系统仍需各自授权，初始化向导必须逐项说明，不得混淆“平台开箱即用”和“替用户绕过外部认证”。

### 14.3 Kubernetes 安装

```bash
helm install adro ./charts/adro \
  --namespace adro --create-namespace \
  -f values.production.yaml
```

生产环境建议：

- PostgreSQL 使用托管 HA 或 Patroni。
- NATS JetStream 三节点。
- Temporal 多副本和独立持久化。
- Artifact 使用经验证的外部 ArtifactStore Driver；可选 S3-compatible、OBS、OSS、COS、BOS 或企业扩展。
- API/Event Gateway/Worker 至少两个副本。
- Runner 按能力和安全域水平扩展。

### 14.4 程序员是否需要安装 Multica

目标部署模式下，程序员只需要浏览器；Agent CLI、Multica daemon 和仓库工作区运行在中央 Runner。前提是中央环境可以访问：

- Git 仓库。
- 模型 Provider。
- CI/部署环境。
- Test 环境。
- 日志和监控。

如果某个仓库只能从开发者电脑访问，则需要在该安全域部署 Runner/Daemon，但用户仍然使用同一 Web UI。

---

## 15. 升级、兼容和回滚

### 15.1 版本锁

仓库必须包含：

```yaml
platform: 1.0.0
multica:
  version: 0.4.35
  backend_digest: sha256:...
  web_digest: sha256:...
  cli_version: 0.4.35
  capabilities:
    - issue.child.v1
    - run.messages.v1
    - runtime.worktree.v1
providers:
  schema: v1
```

### 15.2 升级流程

```text
check release notes/license
  -> backup control DB, Multica DB, artifacts
  -> stop accepting new runs
  -> drain active runners
  -> run provider contract tests in staging
  -> upgrade Multica backend
  -> wait migrations/health
  -> upgrade daemon/CLI/runtime
  -> smoke test Issue/Agent/comment/multi-repo/events/usage
  -> canary one workspace
  -> resume traffic
```

### 15.3 回滚

- 应用镜像可以回滚，但数据库迁移未必可逆。
- 数据库升级前必须有可验证快照。
- 出现不可逆 schema 变化时恢复快照，不只回滚镜像。
- Provider 降级由 Adapter capability test 决定，不能凭版本号猜测。
- 自动化不能在升级期间产生重复外部副作用；所有命令使用 idempotency key。

### 15.4 兼容测试

每个 Multica 版本至少测试：

```text
create/get/update Issue
create child Issue and stage
create/assign Agent
bind MCP and Skill
create/assign Project resources
run Agent task
stream events
read run-messages cursor
resume comment/session
multi-repository checkout
read token usage
cancel/retry task
```

---

## 16. 安全、权限和审计

### 16.1 访问控制

使用 RBAC + ABAC：

```text
tenant_admin
workspace_admin
product_manager
tech_lead
developer
tester
agent_operator
security_reviewer
auditor
```

ABAC 条件：项目、仓库、环境、安全域、Agent、MCP scope、数据分类和审批角色。

所有业务查询必须同时约束 `tenant_id/workspace_id`；PostgreSQL RLS 作为纵深防御，应用在每个事务开始设置不可由请求正文覆盖的 tenant context。缓存 key、JetStream subject、Artifact key 和搜索索引同样包含不可伪造的租户分区。CI 必须包含跨租户 IDOR、cursor、搜索、WebSocket 和 Artifact 下载测试。

### 16.2 凭证

- Multica PAT、Git token、模型 key、CI token、K8s credential 只存 Secret Reference。
- Runtime 使用临时任务 Token，不给 Agent 发行长期管理员 PAT。
- 控制面和 Runner 使用 mTLS。
- Secret 注入进程环境或 0600 临时文件，任务结束清理。
- 禁止 Token 出现在命令行、Issue、评论、Prompt、日志和 PR。

### 16.3 Runner 隔离

- rootless container 或独立 Unix 用户。
- 高风险项目使用专用 VM。
- 默认禁止 Docker Socket。
- Git 和 Kubernetes 凭证按项目最小权限。
- 网络 egress allowlist。
- CPU、内存、磁盘、并发、最大运行时间配额。
- Runner 异常时可隔离，不影响控制面。

### 16.4 审计事件

审计记录：真人、Agent、Provider、任务、命令、输入摘要、输出摘要、Commit、PR、审批、Secret 使用、策略决策和结果。

审计日志追加写，使用 hash chain 或 WORM 存储；管理员也不能无痕修改。

### 16.5 Agent 与扩展威胁模型

仓库源码、Issue、评论、MCP 返回、日志和网页内容都属于不可信输入。主要威胁和最低控制如下：

| 威胁 | 最低控制 |
| --- | --- |
| 仓库内容中的 Prompt Injection | 系统指令隔离、工具 scope、敏感动作 Policy Gate、输出不能自行提升权限 |
| Agent 通过命令窃取 Secret | 任务级短期凭证、网络 allowlist、命令审计、Secret Broker、禁止继承宿主环境 |
| 恶意 MCP/Extension | 签名和 digest、权限 manifest、独立容器、schema 变更重审、egress policy |
| 跨租户数据泄露 | OIDC claim 授权、RLS、租户化缓存/事件/Artifact、持续 IDOR 测试 |
| 构建脚本逃逸 Runner | rootless、seccomp/AppArmor、只读基础镜像、无 Docker Socket、高风险任务 VM |
| 日志和 Diff 泄密 | pre-upload secret scan、分类、redaction、下载再授权、保留策略 |
| Webhook 重放或伪造 | HMAC、timestamp window、delivery ID 去重、来源 allowlist |
| 自动化供应链攻击 | 锁定 Commit/digest、SBOM、签名验证、双人发布、可复现构建 |

高风险动作包括生产部署、数据写入、权限变更、Secret 读取、外部通知和跨仓范围扩大。默认必须经过 Policy Gate；即使工作流节点配置为 `auto`，也不能绕过 workspace 的不可降级安全策略。

---

## 17. Token、成本和 Git 归属

### 17.1 Token 计量

每个 ExecutionRun 记录：

```text
provider
model
agent_binding_id
initiator_member_id
responsible_member_id
requirement_id
bug_id
work_item_id
input_tokens
output_tokens
cache_read_tokens
cache_write_tokens
duration_ms
repair_attempt_no
estimated_cost
actual_cost (optional)
```

“发起人”和“责任研发”必须分开。自动测试触发修复时，成本归集规则由 Workspace Policy 决定：可以归到需求负责人、原开发负责人或公共自动化成本中心，但不能因为 Provider 自动触发就丢失责任归属。

### 17.2 Git identity

推荐：

```text
Author: ADRO Agent <adro-agent@company.example>
Committer: ADRO Runner <adro-runner@company.example>
Responsible-Developer: Zhang San
Requested-By: Product Owner
ADRO-Requirement: REQ-1024
ADRO-Run: run-uuid
```

如果企业要求 Author 对应责任研发，可设置任务级 `user.name/email`，但责任证据必须来自控制面 provenance、PR Reviewer 和审计，不能只相信 Git Author。

不要在中央服务器保存员工个人签名私钥。需要签名时使用企业 Bot/GPG/KMS 签名。

---

## 18. 可观测性与运维

### 18.1 指标

```text
workflow_runs_total{template,status}
workflow_node_duration_seconds{node}
provider_runs_total{provider,status}
provider_event_lag_seconds{provider}
event_dedupe_total{provider}
runner_capacity{runner}
runner_queue_depth{security_domain}
workspace_diff_latency_seconds
test_gate_failures_total{gate,reason}
auto_repair_attempts_total{result}
bug_fingerprint_dedup_total
token_usage_total{member,project,model}
artifact_upload_failures_total
websocket_reconnect_total
```

### 18.2 日志

JSON structured logging，字段至少包含：`timestamp`、`service`、`tenant_id`、`workspace_id`、`correlation_id`、`causation_id`、`run_id`、`provider_run_id`、`error_code`。

禁止记录 Secret、完整 Prompt、完整 Tool Output 和未脱敏日志。

### 18.3 运行手册

必须提供：Provider offline、事件积压、Temporal 卡住、Runner 磁盘不足、Worktree 冲突、测试环境不可用、数据库迁移失败、Artifact 存储不可用、WebSocket 断线和自动修复超限的 runbook。

---

## 19. 测试与质量标准

### 19.1 测试层级

```text
unit tests
integration tests (PostgreSQL/NATS/Temporal/Testcontainers)
provider contract tests
workflow replay tests
event dedupe/reconnect tests
runner isolation tests
security/permission tests
prompt-injection and secret-exfiltration tests
Playwright E2E
k6 load tests
disaster recovery tests
upgrade/rollback tests
```

### 19.2 关键端到端场景

1. 创建需求，指定两个人，自动映射多个仓库。
2. 方案 Agent 读取多仓并产出方案。
3. Review 驳回后只重做方案，不重复创建开发任务。
4. 两个仓库并行开发并在 UI 实时显示 Diff。
5. 单测失败，测试证据被持久化并生成唯一 Bug。
6. Bug 自动回到原 Work Item 和原 Agent。
7. 原 session 不可用时，RepairBrief 恢复任务。
8. 3 次修复失败后进入人工接管。
9. 自动测试通过后生成不可变报告，转人工测试。
10. 浏览器断线后按 cursor 恢复所有事件。
11. Provider 重复发送同一事件，Read Model 不重复。
12. Multica 升级后契约测试通过，旧需求仍可查看和恢复。

### 19.3 性能目标

| 指标 | GA 目标 |
| --- | ---: |
| 控制面 API 可用性 | 99.9% |
| Provider 事件到 UI p95 | < 1 秒 |
| 文件变化到 Diff p95 | < 2 秒 |
| Workflow Worker 故障恢复 | < 60 秒 |
| 事件确认丢失 | 0 |
| 单 workspace 并发 | 由 Runner 配置，发布时实测声明 |
| RPO | <= 5 分钟 |
| RTO | <= 60 分钟 |

不允许在未压测情况下宣称“支持几十/几百并发任务”。

### 19.4 ADRO Bench：公开效果基准

要获得业界信任，不能只展示 UI 和成功案例。仓库必须发布 `adro-bench`，任何 Provider、模型和平台版本都能在固定多仓样例上复现结果。

基准场景至少包含：

1. Feign/OpenAPI 向后兼容新增接口，正确修改 provider 和真实调用方。
2. DTO breaking change，识别所有编译依赖并生成迁移顺序。
3. 只需回归、不应修改的关联仓库，验证误改率。
4. 单测假阳性：Agent 声称通过但命令失败，门禁必须拒绝。
5. API 成功但日志或数据异常，测试必须发现。
6. 首次自动修复失败、第二次成功，仍回到同一 Provenance。
7. Provider 会话丢失后用 RepairBrief 恢复。
8. Workflow Worker、WebSocket 和 ArtifactStore 故障后的恢复。

发布指标：

| 维度 | 指标 |
| --- | --- |
| 选仓 | required-repo recall、non-required-repo precision、人工修正次数 |
| 交付 | acceptance pass rate、跨仓构建成功率、首次通过率 |
| 证据 | 无机器证据误放行数、证据完整率、报告可复现率 |
| 修复 | Bug 归因准确率、自动修复成功率、平均修复轮次 |
| 上下文 | input/output/cache token、上下文重复率、恢复成功率 |
| 体验 | time-to-first-event、time-to-first-diff、重连丢失事件数 |
| 稳定性 | workflow recovery、重复副作用数、artifact hash mismatch |

基准输出 JSON 和人类报告，记录 ADRO、Multica、Provider、模型、Prompt/Skill、仓库 Commit 和环境版本。主分支 CI 对确定性平台指标做硬门禁；模型结果使用统计区间和多次运行，禁止挑选最好的一次。README 的性能和效果声明只能引用公开基准或可下载报告。

---

## 20. 开源仓库结构

```text
adro/
  apps/
    web/
    api/
    event-gateway/
    workflow-worker/
    runner-supervisor/
    artifact-service/
    repository-indexer/
    extension-gateway/
  internal/
    domain/
    application/
    adapters/
      multica/
      artifact/
      git/
      ci/
      deploy/
      logs/
      notification/
  proto/
    execution/v1/
    runner/v1/
    events/v1/
    extensions/v1/
  sdk/
    provider/
    artifact-driver/
    source-control/
    evidence-collector/
  web/
    components/
    routes/
    features/
  migrations/
  charts/adro/
  deploy/
    compose/
    systemd/
  skills/
  examples/
    three-repo-feign/
  bench/
    adro-bench/
  docs/
    architecture/
    api/
    operations/
    security/
    rfcs/
  tests/
    contract/
    e2e/
    load/
  adroctl/
  LICENSE
  THIRD_PARTY_NOTICES
  SBOM
  SECURITY.md
  THREAT_MODEL.md
  CODEOWNERS
  CONTRIBUTING.md
  GOVERNANCE.md
  MAINTAINERS.md
  ROADMAP.md
  SUPPORT.md
  RELEASE.md
  CHANGELOG.md
```

### 20.1 开源工程要求

- OpenAPI 3.1、AsyncAPI、Protobuf 均版本化。
- Semantic Versioning。
- ADR 记录关键技术决策。
- DCO 或 CLA。
- CI 包含 lint、unit、integration、contract、E2E、SBOM 和漏洞扫描。
- 发布 OCI 镜像签名、digest、可复现构建和 SLSA provenance。
- 提供升级矩阵、迁移指南、备份恢复和安全响应流程。
- 提供无 Multica 的 MockProvider，保证社区可以运行控制面测试。
- 提供 `adroctl up --demo`、三仓旗舰样例和逐步教程，首次成功路径必须由全新 Linux 环境 CI 每日验证。
- 默认不采集外发遥测；匿名产品遥测必须显式 opt-in、公开 Schema，并可在设置和配置中彻底关闭。
- 每个公开 SPI 提供 SDK、参考实现、conformance suite、版本策略和弃用周期。
- 维护者权限、RFC 决策、发布签名、安全披露和行为准则必须公开，不能依赖公司内部流程才能贡献。
- 支持矩阵明确列出最近稳定版本、Multica 版本、数据库、浏览器、Kubernetes 和各扩展协议版本。

### 20.2 Multica 许可证与品牌合规

ADRO 自有控制面代码建议采用 Apache-2.0；Provider、SDK、示例和文档分别维护清晰的 SPDX 标识。只要发行物重新分发 Multica 二进制、镜像、源码或品牌资产，就必须随相应发行物提供 Multica 完整许可证和 NOTICE，并在文档中说明执行层来源。需要特别复核：

- 内部组织部署与对外托管服务的区别。
- 将 Multica 嵌入商业产品的许可要求。
- 复用其 Web UI 时的名称、Logo、版权和归属要求。
- 公开发布源码与对外提供托管实例不是同一件事。

发行渠道拆为两个清晰 profile：

- `adro-core`：OSI-only，包含控制面、MockProvider 和开放 SDK，不重新分发 Multica。
- `adro-full`：一键安装 ADRO 和锁定版本的 Multica；构建、下载或镜像分发方式必须经过许可证复核并展示第三方归属。

如果目标是完全 OSI-only 的发行版，必须保证 Multica 是可选 Provider，并允许用户使用 MockProvider 或其他 Provider；不得把受附加条件的 Multica License 误标为纯 Apache-2.0。项目名、Logo 和域名也必须在公开发布前完成独立商标清查；GitHub 名称可用不等于商标可用。

---

## 21. 研发拆解与交付顺序

虽然产品目标是成熟版，但研发仍应按可验证的垂直切片交付，而不是最后一次性集成。

### Wave 1：平台基础

- 仓库初始化、许可证、CI、SBOM、代码规范。
- Control API、PostgreSQL、身份和审计。
- Provider SPI、MockProvider、MulticaProvider。
- NATS、Temporal、Artifact Service、filesystem/S3-compatible Driver 和迁移骨架。
- Docker Compose/Helm 和 `adroctl`。

### Wave 2：核心交付域

- 需求、Bug、项目、成员、Agent 映射。
- 多仓 TeamWorkspace 和 Runner Supervisor。
- Workflow Template、审批和状态机。
- Multica Issue/Agent/Project/Runtime 映射。

### Wave 3：实时和代码体验

- Provider WebSocket/cursor 适配。
- Event Gateway、Outbox、JetStream。
- Workspace Observer、Diff 和 Monaco 页面。
- 运行详情、工具调用、测试流。

### Wave 4：测试与自动修复

- Skill Registry、test-and-verify Skill。
- EvidenceBundle、GateResult、报告。
- Bug fingerprint、原 Agent provenance、RepairBrief。
- 自动修复循环、上限和人工接管。

### Wave 5：能力中心

- MCP 管理、工具审批、Schema digest、健康和调用审计。
- Skills 管理、版本、签名、绑定和回滚。
- Automation Designer、触发器、版本和运行回放。
- Extension SDK、Catalog、权限清单和 conformance suite。

### Wave 6：生产硬化

- HA、隔离、配额、灾备、升级灰度。
- Git/CI/K8s/日志/飞书/钉钉集成。
- 负载和故障注入。
- `adro-bench`、三仓旗舰样例和每日 clean-install 验证。
- 许可证和安全审计。
- GA 发布和社区文档。

---

## 22. 交付验收清单

### 功能

- [ ] 用户可在一个需求中指定多个责任人。
- [ ] 每个责任人可映射到多个项目和仓库。
- [ ] 一个需求可创建多个并行/串行 Work Item。
- [ ] Agent 可以在多仓 Workspace 中工作。
- [ ] 方案、Review、开发、单测、提测、人工验收完整闭环。
- [ ] 测试失败自动生成唯一 Bug 并回到原开发 Agent。
- [ ] 原 session 失效时可通过 RepairBrief 恢复。
- [ ] 自动修复循环有最大次数并能人工接管。
- [ ] 测试报告包含可验证证据，而非只有自然语言。
- [ ] MCP、Skill、Automation 是可管理、可审计、可回滚的菜单。

### 实时

- [ ] 运行状态、消息、工具调用和测试输出实时显示。
- [ ] Changed Files 和 Diff 实时显示。
- [ ] WebSocket 断线支持 cursor 补偿。
- [ ] Provider 事件重复不会造成重复业务动作。

### 部署

- [ ] 单条命令同时启动控制面、Multica、Runtime、Runner 和依赖服务。
- [ ] 支持无浏览器的 Linux 服务器部署。
- [ ] 支持 Docker Compose 和 Helm。
- [ ] 配置、Secret、数据库迁移和健康检查自动完成。
- [ ] 未配置对象存储时使用本地 Artifact 持久卷，启动过程不要求云 Key。
- [ ] 至少通过 filesystem、S3-compatible 和一个原生云 Driver 的 Artifact contract test。
- [ ] Artifact Driver 在线迁移支持双写、hash 校验、暂停、恢复和回滚窗口。
- [ ] 支持精确版本升级、契约测试、灰度和回滚。

### 安全和合规

- [ ] Secret 不出现在 Prompt、评论、日志、Issue 和 Git。
- [ ] Runner 有 OS/容器/VM 隔离。
- [ ] 项目、仓库、环境和 MCP scope 有权限控制。
- [ ] 关键动作有不可篡改审计。
- [ ] 任何重新分发 Multica 的发行物均提供其完整许可证、NOTICE、归属和第三方 SBOM。

### 工程质量

- [ ] Provider Contract Test、E2E、负载、灾备和升级测试全部纳入 CI。
- [ ] 所有公开 Extension SDK 有参考实现、权限模型、版本协商和可独立运行的 conformance suite。
- [ ] MockProvider 可以脱离 Multica 运行控制面测试。
- [ ] 每个发布版本有兼容矩阵、迁移说明和可回滚方案。
- [ ] 发布文档声明经过实测的并发、仓库数和 Runner 规模。
- [ ] `adro-bench` 的固定多仓场景、原始 JSON 和版本信息可公开复现。

---

## 23. 最终架构决策

1. **控制面独立拥有业务状态、工作流、事件、证据、上下文和 UI。**
2. **Multica 通过 Provider SPI 接入，不能成为业务数据库或不可替换依赖。**
3. **多仓能力由 TeamWorkspace、Work Item 和 Runner 实现；不修改 Multica 的单 Project/单 assignee 模型。**
4. **评论是人类时间线和触发入口；Provenance、EvidenceBundle 和 ContextManifest 才是机器事实。**
5. **MCP、Skills、Automation 作为能力中心一级菜单，分别治理工具、知识/流程和触发编排。**
6. **Temporal 管长流程，JetStream 管实时事件，PostgreSQL 管业务事实，Artifact Service 通过可插拔 Driver 管大产物。**
7. **中央 Linux 环境可以做到用户只用浏览器，但必须建设 Runner 隔离、凭证治理和容量管理。**
8. **实时体验以版本化事件和 Diff Observer 为基础，禁止依赖私有前端协议。**
9. **升级通过锁版本、契约测试、备份、灰度和回滚实现；不 Fork Multica 核心。**
10. **开源发行必须同时满足软件许可、品牌归属、SBOM、安全和可复现构建要求。**
11. **Artifact 使用 `artifact://` 逻辑地址和可插拔 Driver；单机默认本地持久卷，任何云存储都不是启动前提。**
12. **选仓以用户明确范围为种子，以版本化 Repository Knowledge Graph 做可解释的遗漏检测，禁止 Agent 静默扩仓。**
13. **产品效果以公开可复现的 ADRO Bench 证明，不以录屏、Agent 自述或挑选的成功案例证明。**

这份蓝图的实现结果应当是一个独立的企业 AI 研发交付平台，而不是 Multica Web UI 的复制品。Multica 可以升级、替换或在某些部署中被关闭，控制面仍然保持自己的业务模型和产品价值。
