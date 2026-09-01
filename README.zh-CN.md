<p align="center">
  <img src="docs/branding/adro-cover.svg" alt="ADRO 可审计 Agent 与可恢复交付流程" width="100%">
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="docs/product-requirements.zh-CN.md">产品需求</a> ·
  <a href="docs/architecture/adro-technical-design.zh-CN.md">技术方案</a> ·
  <a href="ABOUT.md">About</a>
</p>

# ADRO

ADRO 是面向软件交付的开源控制面：从需求、Bug 或分析目标开始，编排
Agent 完成方案、研发、测试、修复和报告，并为每一步保存可复核的证据。
Session、Transcript、Checkpoint、Memory、Lease、Outbox、Artifact 和审计
事实都由 ADRO 持久化；Git、CI、部署、身份和通知通过版本化 SPI 接入。

![ADRO 架构图](docs/architecture/adro-architecture.svg)

![ADRO 功能图](docs/architecture/adro-capability-map.svg)

## 无 Docker 启动

环境要求：Go 1.24+、Git、curl，以及一个已安装并完成认证的代码客户端。

```bash
ADRO_EXECUTOR="$(command -v codex)" \
ADRO_ADMIN_PASSWORD='change-this-password' \
./start.sh --no-docker --no-open
```

启动后访问 `http://127.0.0.1:8081`，API 就绪检查为
`http://127.0.0.1:8080/readyz`。

```bash
./start.sh --status
./start.sh --stop
```

可通过 `ADRO_HOME`、`ADRO_API_PORT`、`ADRO_WEB_PORT` 隔离状态目录和端口；
`ADRO_EXECUTOR_COMMAND` 支持带 `{input}` 占位符的自定义 argv 命令。

## 自检与真实流程

```bash
go test ./...
make verify
make real-e2e   # 需要已认证的真实代码客户端
```

`make verify` 包含单测、race、vet、build、API/HTML/OpenAPI 契约、启动检查、
SPDX 许可证/SBOM 校验和 Playwright 浏览器矩阵。浏览器测试使用仓库内仅供
测试的 no-op executor，不依赖开发者机器；`make real-e2e` 才是真实模型客户端
验收路径。

## 目录

- `internal/`：领域、工作流、Provider、Harness、存储、API 和审计实现。
- `apps/web/`：ADRO 自有中英文工作台。
- `docs/`：产品、架构、运维、兼容性和发布文档。
- `sdk/`：Provider、Harness、集成和 Artifact 扩展契约。
- `migrations/`：版本化持久化边界。

发布和安全策略见 `RELEASE.md`、`SECURITY.md`；仓库 About 文案见
`ABOUT.md`。
