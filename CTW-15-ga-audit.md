CTW-15 GA readiness audit
审计日期：2026-08-28
审计范围：ADRO-production-blueprint.zh-CN.md、当前 Go 服务端、OpenAPI、WebUI、启动/部署文件、SDK、E2E 与测试。

## 结论

当前交付物是可离线评估和 Provider 适配开发的 reference profile，不是生产 GA。核心领域规则、MockProvider、文件 ArtifactStore、本地身份、18 个菜单和本地 E2E 已可验证；生产所需的持久化、真实 Multica run/event 闭环、Runner 隔离、企业身份/密钥、外部集成和灾备仍未闭环。任何发布声明必须同时给出 adapter 版本、依赖版本、contract-test 结果、基准数据和安全控制配置。

状态含义：`implemented` = 本地代码与测试可复现；`partial` = 契约或本地路径存在，但真实依赖、持久化或闭环缺失；`absent` = 本仓库没有可运行实现。

## 验收矩阵

| ID | 验收项 | 状态 | 代码证据 | 真实测试方法 / 依赖 | 风险与优先级 |
| --- | --- | --- | --- | --- | --- |
| A01 | Requirement 状态机、版本和合法迁移 | implemented | `internal/domain/domain.go:1-130`; `internal/store/memory.go:139-175`; `internal/api/server.go:528-754` | `go test ./...`、并发 If-Match/transition API 测试 | 仅内存，生产需事务；P0 |
| A02 | 创建幂等和 Work Item 幂等 materialization | implemented | `internal/api/server.go:506-525`; `internal/store/memory.go:178-209` | 重放同一 `Idempotency-Key`，并发创建测试 | 幂等记录未持久化；P0 |
| A03 | 多负责人、多仓库 Work Item fan-out | implemented | `internal/api/server.go:2547-2624`（按仓库 fan-out、负责人轮转、原子插入） | 三仓三负责人 fixture，检查唯一 Work Item 和绑定 | 负责人策略需显式 Workspace Policy；P1 |
| A04 | Impact report、确认仓库集和 repository graph | partial | `internal/api/server.go:777-798`; `internal/store/memory.go:427-488`; OpenAPI `/repository-graph` | 真实 Git 图索引、候选证据和遗漏仓库扩展请求 | 当前为用户输入/内存模型，无索引 worker；P1 |
| A05 | Agent / Project / Employee 关系 | partial | `internal/domain/domain.go:353-375`; `internal/api/server.go:2290-2350`; `internal/provider/multica.go:195-228` | 真实 Multica workspace/runtime/project/agent 创建后核对 ID、成员映射和回读 | `EnsureTeamWorkspace` 调 `/api/projects` 但绑定回读契约未验证；需求 materialization 未自动创建 provider parent/project；P0 |
| A06 | Provider opaque binding、路由优先级、不可变诊断 | implemented | `internal/domain/domain.go:195-208`; `internal/api/server.go:2547-2623`; `internal/provider/routing.go` | 配置/成员/角色/默认优先级和配置变更重试测试 | Provider 对象生命周期/删除仍需外部回收策略；P1 |
| A07 | `ExecutionProvider` SPI 和 deterministic MockProvider | implemented | `internal/provider/provider.go:19-139,146-262`; `sdk/provider/spi.go` | Provider contract tests 覆盖 start/cancel/events/usage | SPI 版本未进入 manifest/兼容矩阵；P1 |
| A08 | 无 Docker 远程 Multica 配置、健康、Agent/Issue 创建 | partial | `start.sh:36-57,182-192`; `internal/provider/multica.go:159-329` | 针对 pin 的 Multica daemon/hosted API 运行 contract suite，验证 PAT、workspace/runtime 唯一选择 | 真实远程部署、首次凭证、daemon 可用性是外部前置；P0 |
| A09 | 真实 run snapshot/messages/cancel/usage | partial | `internal/provider/multica.go:331-376,447-477`; `internal/api/server.go:1432-1482` | 真实 gateway 逐项调用并验证错误码、超时、重试和最终状态 | 当前通用 `/api/runs` 依赖外部 gateway；公开 API 未提供完整契约；P0 |
| A10 | Provider 事件、游标回放、WebSocket | partial | `internal/provider/multica.go:378-445`; `internal/api/server.go:1571-1645`; `internal/events/events.go` | 真实 daemon 重复/乱序/断线事件，测 p95 <1s、去重和 cursor replay | 仅显式 `ADRO_MULTICA_WS_URL` 或本地总线；上游事件语义未 conformance；P0 |
| A11 | Gate/Evidence/Provenance/Bug fingerprint | implemented | `internal/workflow/workflow.go:11-84`; `internal/api/server.go:801-847,850-892`; `internal/domain/domain.go:210-250` | gate pass/fail、证据 hash、重复 fingerprint、审计链测试 | 记录落在内存；跨进程原子性缺失；P0 |
| A12 | 同 issue/session/workdir 的自动 Bug 修复闭环 | partial | `internal/api/server.go:920-957`; `internal/domain/domain.go:329-336` | 真实失败 -> 同 Work Item/Provider issue rerun -> 原 session/worktree 恢复 -> 新 Evidence -> verify | 只有 `attempt_count` 和 `repairBrief`；无持久 `RepairAttempt`/ContextManifest、session/workdir 恢复和自动闭环；P0 |
| A13 | 3 次修复上限和人工升级 | implemented | `internal/api/server.go:925-933`; `internal/workflow/workflow.go:69-83` | 第 4 次请求必须 409/HUMAN_TRIAGE_REQUIRED，审计原因可回放 | 尝试事件未单独持久化；P1 |
| A14 | Runner 注册、心跳、容量、排空、隔离状态 | partial | `internal/runner/supervisor.go:14-145`; `internal/api/server.go:1668-1722` | 多 Runner 能力/安全域选择、心跳过期、drain/quarantine API | Supervisor 是进程内控制面；无租户持久化和调度 lease；P0 |
| A15 | 真实 Runner 执行隔离、网络 allowlist、命令审计 | absent | 仅 `internal/runner/supervisor.go` 元数据，无 execute/sandbox worker | rootless/seccomp/AppArmor/VM、磁盘配额、命令/网络审计故障注入 | 代码执行边界不存在；P0 |
| A16 | 文件 ArtifactStore、hash、原子写、Range/HEAD、附件/截图 | implemented | `internal/artifact/store.go:52-145,147-243`; `internal/api/server.go:980-1192` | `internal/artifact/*_test.go`、Playwright screenshot/attachment | 单节点文件系统；无对象锁/加密；P1 |
| A17 | S3/cloud driver、加密、object lock、retention/legal hold | absent | `sdk/artifact-driver/spi.go:10-27` 只有接口；无 cloud driver | MinIO/S3 contract suite、加密/保留/法律保全测试 | 不能宣称云存储 GA；P0 |
| A18 | Artifact 在线迁移、双写、暂停/恢复/回滚窗口 | partial | `internal/artifact/migration.go`; `internal/api/server.go:1369-1430`; `migrations/001_init.sql:261-275` | 大对象中断/恢复、digest 校验、回滚窗口和读路径切换 | 只有状态/拷贝契约，无 worker 接线和双写；P1 |
| A19 | 全部公开 API 路径可路由 | partial | `internal/api/server.go:81-220`; `openapi/openapi.yaml:13-387` 覆盖 auth/users、requirements/bugs、artifacts、runs、repos/workspaces、MCP/Skills/Automation、runners/agents/audit | OpenAPI operation-driven smoke + schema validation + 每个写操作重放 | Handler 大多存在，但 response schema、统一幂等键、错误细节和权限边界不完整；P1 |
| A20 | 本地密码身份、会话、18 菜单 RBAC、最后管理员保护 | implemented | `internal/auth/service.go:24-33,94-169,195-280`; `internal/api/server.go:111-123` | `internal/auth/*_test.go`、Playwright 权限隔离 | 仅 local identity，不能替代企业身份；P1 |
| A21 | OIDC/ABAC/mTLS/OPA/SecretStore/企业凭证治理 | absent | 无 OIDC/mTLS/OPA adapter；`sdk/integrations/spi.go:25-30` 仅 SecretStore/Identity 接口 | Keycloak/OIDC claim、RLS/ABAC、mTLS rotation、secret redaction/egress 测试 | 跨租户和工作负载信任边界未建立；P0 |
| A22 | MCP 注册、发现、审批、health、绑定、调用审计 | partial | `internal/api/server.go:1905-2037,2290-2350` | 真实 stdio/SSE/HTTP server schema、签名 digest、拒绝越权调用 | 当前 invocation response 明确为 `{"status":"mocked"}`；无签名进程/真实调用/回滚；P0 |
| A23 | Skill 版本、发布、回滚、Agent binding | partial | `internal/api/server.go:2039-2142` | 签名包、兼容性 manifest、升级/回滚和 provider 注入测试 | 仅本地 CRUD 状态，不执行内容，不持久化历史 provenance；P1 |
| A24 | Automation 触发、暂停、run takeover/cancel | partial | `internal/api/server.go:2144-2287` | schedule/webhook 与 DAG/审批/重试/补偿真实运行 | 仅内存 run 状态；无 Temporal，不能把 Multica Autopilot 当业务状态机；P0 |
| A25 | WebUI 18 个菜单、表单、响应式工作台 | partial | `internal/auth/service.go:29-33`; `apps/web/index.html:1-59`（`pageMeta`/renderers） | `npm run test:e2e`（7 passed，含 18 菜单、流程、权限和截图） | 多数页面是 dependency-free reference shell，非 Next.js/React/Monaco 企业控制台；P1 |
| A26 | Provider attachment delivery capability-gated | partial | `internal/provider/multica.go:479-530`; `apps/web/index.html:23-25`; `internal/api/server.go:1130-1192` | remote Multica attachment 真实 multipart、拒绝/重试、artifact-only 降级 | 本地存储可交付；远程能力仍未 upstream conformance；P1 |
| A27 | PostgreSQL 事务持久化、RLS、恢复 | absent | `migrations/001_init.sql:1-306` 是 schema/RLS 边界；`internal/api/server.go:35-47` 注入 `*store.Memory` | Testcontainers PostgreSQL、多副本并发、RLS/IDOR、备份恢复 hash 校验 | 生产业务状态/事件/会话不持久，Helm 多副本会分裂；P0 |
| A28 | NATS JetStream、Temporal、outbox/replay workflow | absent | `migrations/001_init.sql:120-138` 只有 outbox 表；无客户端/worker | NATS at-least-once、Temporal retry/compensation/versioning、重放一致性 | 长流程、人工等待、事件恢复不可用；P0 |
| A29 | Compose/Helm/start.sh 单机启动 | partial | `deploy/compose/docker-compose.yml:1-34`; `charts/adro/templates/deployment.yaml:1-40`; `start.sh:256-289` | clean host zero-to-one、升级/停止/重启、无 Docker 远程 profile | Compose 是 ADRO + 文件卷；Helm 默认 `replicas: 2` 却无共享业务库；P0 |
| A30 | Git/CI/deploy/log/data integrations | absent | `sdk/integrations/spi.go:6-30` 只有 SourceControl/CIPipeline/Deployer/Evidence/Notifier/Secret/Identity 接口 | Git provider、CI、deploy、logs/data adapter contract + failure/retry/audit | 无真实 adapter，无法形成 commit/PR/deploy 事实；P0 |
| A31 | 可观测性、SLO、runbook、故障注入 | partial | `internal/api/server.go:453-477` 仅基础 metrics；`docs/architecture/ga-readiness.md` | OpenTelemetry/metrics/log correlation、Provider offline、磁盘满、WS 断线、迁移失败演练 | 运行手册和生产告警不完整；P1 |
| A32 | 单测、竞态、静态检查和 UI E2E | implemented | `internal/**/*_test.go`; `e2e/workbench.spec.js`; `e2e/visuals.spec.js` | `go test ./...` PASS；`go test -race ./...` PASS；`go vet ./...` PASS；`go build ./...` PASS；`bash -n start.sh` PASS；Playwright 7 passed | `node var/webui-syntax-check.js` FAIL：Node 无 `localStorage`；P1 |
| A33 | 负载、灾备、升级/回滚、外部依赖测试 | absent | 无 load/DR/fault-injection suite；`docs/architecture/ga-readiness.md:37` | k6/vegeta、跨 AZ 恢复、版本锁/迁移/回滚、真实 Multica/Postgres/NATS/Temporal | 不能以本地单测替代 GA；P0 |
| A34 | ADRO Bench 公开可复现结果、Provider conformance | partial | `bench/adro-bench/scenarios/three-repo-feign.json`; `bench/adro-bench/README.md`; `sdk/*/spi.go` | 固定三仓多次运行，发布 JSON/报告/置信区间和 adapter matrix | 只有 fixture/SDK，无公开结果和 CI gate；P1 |
| A35 | 开源许可证、治理、威胁模型、支持文档 | implemented | `LICENSE`, `GOVERNANCE.md`, `SECURITY.md`, `THREAT_MODEL.md`, `SUPPORT.md`, `CODEOWNERS` | license scan、SBOM、发布包核对 Multica NOTICE/商标边界 | 发布时仍需核对是否重新分发 Multica；P1 |
| A36 | 独立扩展进程、签名 manifest、conformance/回滚 | absent | Blueprint 7.4/7.5 要求独立 gateway；仓库无 extension runner | 签名/digest、权限 manifest、schema 变更重审、网络隔离、热升级/回滚 | 当前接口不能作为安全扩展边界；P0 |

## 全部公开接口的判定

`openapi/openapi.yaml:13-387` 声明的 operation 在 `internal/api/server.go:81-220` 有对应路由前缀，且本地 E2E 访问了核心读取和资源动作。这个事实只证明“路由/本地状态可用”，不证明外部副作用或生产契约：

- Auth/users/directory、requirements/bugs、attachments/artifacts/screenshots、work-items/runs/events/messages/usage、repositories/team-workspaces/developer-profiles/approvals/evidence、MCP/skills/automations、runners/agents/audit/provider diagnostics 均归入 A19 的 `partial`。
- 写请求的 `Idempotency-Key` 在 OpenAPI 只作为参数声明（`openapi/openapi.yaml:403-405`），服务端实际只对 requirement create 做内存幂等；应统一所有非幂等写操作并返回可重放的 Problem Details。
- Run 相关路径存在，但 Multica 公共 API 未证明通用 `/api/runs`、messages、cancel、usage、snapshot 契约；只能依据 A09 的 capability 探测选择实现或降级。

## Multica 版本化 SPI 与降级语义

适配器必须发布 `adro.provider.multica/v1` manifest，而不是凭 URL/版本号猜能力：

```json
{
  "protocol": "adro.provider.multica/v1",
  "adapter_version": "<semver>",
  "server_version": "<observed>",
  "features": {
    "config.v1": "supported",
    "agent.create.v1": "supported",
    "issue.create.v1": "supported",
    "issue.rerun.v1": "supported",
    "run.snapshot.v1": "unsupported",
    "run.messages.v1": "unsupported",
    "run.cancel.v1": "unsupported",
    "run.usage.v1": "unsupported",
    "events.message.v1": "supported",
    "events.websocket.v1": "unverified",
    "attachment.multipart.v1": "unverified"
  }
}
```

契约规则：

1. 启动时执行 `/api/config`、健康、workspace/runtime discovery 和每个声明 feature 的 contract test；把结果和上游版本写入 immutable provider binding。
2. 不支持的 capability 返回稳定 `501 capability_unavailable`（带 `capability`, `adapter_version`, `request_id`），不得伪造空 run/usage/event。
3. `issue.rerun.v1` 是通用 run 不可用时唯一允许的降级；必须带已持久化 `ProviderIssueID`，返回 task ID 并保留 `ProviderSessionID` 为空，UI 明示“issue rerun”。
4. 事件只有上游明确声明时才标记 websocket/token 级；否则标记 `message-level`，用 cursor + provider_event_id 去重。
5. Provider 对象 ID 只能进入 binding/provenance，不能成为 ADRO 主键或未经授权的诊断回显。

## 推荐实施顺序、回滚和验收门

| 阶段 | 交付 | 依赖 | 回滚 | 阶段验收门 |
| --- | --- | --- | --- | --- |
| 1. Durable control plane | PostgreSQL repository、事务 outbox、RLS、迁移/恢复、统一幂等 | PostgreSQL 17/Testcontainers | 保留 Memory profile，仅切换 feature flag；禁止双写不一致时读新库 | 多副本并发、RLS/IDOR、恢复 hash、contract tests 全绿 |
| 2. Provider gateway | `multica/v1` manifest、真实 Agent/project/Issue/rerun、capability error、事件 ingress | 已 pin 的 Multica 版本与测试 workspace | capability 不通过则 provider offline，Work Item 不推进状态；保留 MockProvider | 真实 API contract suite、重复/乱序事件、p95 <1s、opaque binding 回放 |
| 3. Runner/secret boundary | Runner lease、工作区恢复、rootless sandbox、网络 allowlist、命令审计、SecretStore/mTLS | Kubernetes/VM 安全域、企业 IdP | quarantine 新任务，旧 run 只读；按 runner/image 版本回滚 | 恶意脚本/越权网络/磁盘满/凭证泄漏故障注入通过 |
| 4. Workflow/event runtime | NATS JetStream + Temporal workflow/versioning、RepairAttempt/ContextManifest、补偿 | 阶段 1、2、3 的稳定 ID | 停止新 workflow，按 outbox cursor 重放；旧版本 worker drain 后回退 | 3 次修复、同 session/worktree、人工等待、重放一致性 |
| 5. Integrations/artifacts/UI | Git/CI/deploy/log/data adapter、S3 driver、迁移 worker、企业 console、OpenAPI schemas | Secret/identity 与 runner boundary | provider/artifact driver capability gate，保留 filesystem/local UI | 三仓 bench、负载/DR/升级回滚、真实截图和证据闭环 |

## 当前交付与后续交接

- 本审计未修改业务代码，工作区不是 Git checkout（`git status` 返回 `not a git repository`）。
- 先由父 issue 验证本附件矩阵和 P0 闭环，再拆 Stage 2 实现；每个实现 issue 必须保留 status、证据行号、外部依赖、真实测试、回滚和验收门。
- GA 判定保持为“本地 reference profile 可交付，生产 GA blocked”；不得把 MockProvider 或绿色本地单测写成真实 Multica/生产承诺。
