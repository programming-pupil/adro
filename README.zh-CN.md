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
Agent 或 Squad 完成方案、研发、测试、修复和报告，并为每一步保存可复核的
证据。工作流不是固定七阶段：可以自由组合节点、条件、并行、汇合和反馈边，
让执行既能向前推进，也能按显式路径回到任意前置节点修复。Session、
Transcript、Checkpoint、Memory、Lease、Outbox、Artifact 和审计事实都由
ADRO 持久化；Git、CI、部署、身份和通知通过版本化 SPI 接入。

## 如何理解 ADRO

分层架构图把系统分成三层：上层是交互与自由编排，中层是 orchestration
与 session harness，下层是持久化底座和外部适配器。Harness 放在中层，负责
在图决策与副作用之间承载可恢复上下文、attempt lineage 和连续性证明。

![ADRO 分层架构图](docs/architecture/adro-layered-architecture.svg)

下面两张图补充不同视角：交付流图解释一次运行如何从意图走到证据，能力图
展示 Agent、Squad、记忆、质量门禁、评论触发和治理能力。三者分别回答“系统
由什么组成”“一次运行怎么走”“能做什么”，不再把流图当作架构图。

![ADRO 交付流图](docs/architecture/adro-architecture.svg)

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

核心亮点：自由双向编排 Agent/Squad、typed session harness 连续性、显式反馈
与有界修复、并行与汇合，以及以证据为依据的完成判定。

发布和安全策略见 `RELEASE.md`、`SECURITY.md`；仓库 About 文案见
`ABOUT.md`。
