CTW-25 ADRO release-readiness audit
审计日期：2026-08-31
审计范围：独立 ADRO 控制面、LocalProvider、OpenAPI、WebUI、启动脚本、SDK、E2E 与测试。

## 结论

ADRO 当前交付的是一个可直接启动的本地执行 profile。它不下载、嵌入、修改或调用任何外部编排产品；运行时只发现并启动管理员选择的本地编码客户端（Codex、Claude Code 或兼容 CLI）。需求、Bug、项目、Agent、Session、Pipeline、Evidence、Artifact、审计和 UI 事实全部由 ADRO 自己持有。

本地 profile 已通过确定性单测、竞态检查、静态检查、浏览器验收和真实客户端发现/版本探测。模型凭证、仓库访问、CI、部署、企业身份、云存储和多节点高可用仍是插件/部署输入，不能仅凭本地测试宣称生产 GA。

状态含义：`implemented` = 本仓库代码和测试可复现；`reference-only` = SPI/契约已定义，生产适配器需单独安装并验收；`blocked` = 缺少外部部署证据，不是本地服务无法启动。

## 验收矩阵

| 区域 | 状态 | 证据 | 下一步门槛 |
| --- | --- | --- | --- |
| Requirement、Bug、Work Item、状态迁移、幂等 | implemented | `internal/domain`、`internal/store`、`internal/api` 测试 | PostgreSQL 事务和多副本并发 |
| 多租户、成员/项目多对多和菜单 RBAC | implemented | auth、IDOR、持久化和 Playwright 测试 | OIDC/mTLS/ABAC 适配器 |
| Agent 路由和角色编排 | implemented | `internal/provider/routing.go` 及路由测试 | 社区 Agent manifest、签名和容量调度 |
| 七阶段 Pipeline 和失败回流 | implemented | `internal/pipeline`、`internal/api/pipeline_test.go` | 外部测试/CI 证据驱动的真实自动闭环 |
| 本地客户端扫描和执行 | implemented | `internal/provider/local.go`、`start.sh`、真实 Claude 探测 | Codex/Claude 版本矩阵与凭证 smoke |
| Session、workdir、Git 基线/head 快照 | implemented | LocalProvider 持久化、clone 和 repair 测试 | 分布式 Runner、隔离和配额 |
| Evidence、Artifact、附件和审计 | implemented | filesystem/API/浏览器测试 | S3 加密、保留、法律保全 |
| MCP、Skill、Knowledge 和 Automation 治理 | reference-only | 自有 CRUD、SPI、权限边界 | 签名插件进程、真实调用和回滚 |
| Git/CI/部署/钉钉/飞书 | reference-only | provider-neutral SPI 和数据模型 | 配置真实凭证并跑外部系统契约测试 |
| 单节点重启恢复 | implemented | JSON 原子快照、LocalProvider run 恢复测试 | 跨节点共享存储和灾备 |
| Web Workbench 和 18 个菜单 | implemented | `npm run test:e2e`（7 项通过） | 企业浏览器矩阵和无障碍审计 |
| 生产身份、Secret、Runner 隔离、事件/工作流集群 | blocked | 配置校验 fail-closed，未伪装为已交付 | 安装对应插件并完成安全/故障注入验收 |

## 本轮硬门禁

```text
go test ./...       PASS
go test -race ./... PASS
go vet ./...        PASS
go build ./...      PASS
npm run test:e2e    PASS (7 tests)
bash -n start.sh    PASS
git diff --check    PASS
```

真实客户端验证只报告可观察事实：本机发现 `/usr/local/bin/claude`，版本探测成功；没有模型凭证或预算时，真实调用会失败并保留错误快照，绝不以假执行结果替代。完整推广验收应在配置真实模型、仓库、测试环境和 CI 后，从提交 Requirement/Bug 开始跑完 1～7、失败回流、复测和报告。

## 开源边界

- `internal/provider` 是稳定、厂商中立的 SPI；LocalProvider 是本仓库唯一内置执行实现。
- 外部项目只允许作为公开能力的黑盒参考，不进入 ADRO 业务主键、数据库、启动脚本或运行时依赖。
- 所有插件通过 manifest、权限、签名、能力协商、健康检查、隔离、升级/回滚和 conformance suite 接入。
- 发布前生成 SBOM、许可证清单和安全报告；未安装的企业适配器必须在支持矩阵中标为 `reference-only` 或 `blocked`。
