为父场景设计“多 Multica Agent 路由与可审计诊断”实现方案。

先读取 `ADRO-production-blueprint.zh-CN.md`、`internal/provider`、`internal/api`、`cmd/adro-api`、README 和 GA readiness。明确：

- 配置格式、校验、优先级和向后兼容；
- WorkItem/DeveloperProfile/Provider binding 如何携带稳定 agent ID；
- API/WebUI 诊断不得泄露 token 或把可配置误报为已连通；
- 单元、API contract、真实 Multica conformance 与失败路径；
- QA 可执行验收矩阵和回滚方案。

只产出方案与验收标准，不修改生产代码。完成后在本 issue 留下完整交接并设为 `in_review`。
