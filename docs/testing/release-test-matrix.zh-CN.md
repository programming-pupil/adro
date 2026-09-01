## 发布前测试目标

这份矩阵是 ADRO 发布前的最小验收门槛。测试分为三层：

1. Go 单元、竞态、静态检查，验证状态机、持久化、权限和错误契约。
2. Playwright 菜单回归，验证浏览器导航、响应式布局和表单交互。
3. 本地黑盒系统测试，真实启动 API/Web、真实本地执行器和持久化目录，验证跨功能组合、并发及恢复。

发布候选必须通过前两层；若声明“可自动交付”，还必须在提供 Codex 凭据的环境通过第三层。没有 Codex 时测试必须失败并显示前置条件，不得以静态 fixture 结果代替真实执行证据。

## 功能与场景矩阵

| 编号 | 功能 | 正常路径 | 边界/异常 | 验收证据 |
| --- | --- | --- | --- | --- |
| M01 | 登录与会话 | 登录、刷新、退出、`/auth/me` | 错误密码、锁定、过期会话 | 401/423、无敏感字段 |
| M02 | 菜单与权限 | 18 个菜单均可打开 | 无菜单成员访问被拒绝 | 页面无 JS 错误，API 403 |
| M03 | 租户隔离 | workspace A 读取自己的资源 | B 读取 A 的 ID | 404，不泄露存在性 |
| M04 | 项目/仓库 | 创建、索引、更新、图谱 | 重复幂等键、非法关系 | 资源与审计记录一致 |
| M05 | 需求 | 创建、分页、状态推进 | 空标题、空验收、非法迁移 | 422/409，版本单调递增 |
| M06 | 需求附件 | 文本附件上传、列表、详情 | 空文件、20 MiB+1、错误 owner | 不可变 artifact URI、大小正确 |
| M07 | 普通聊天 | 绑定项目创建会话、发送消息 | 空消息、非 user 角色、跨会话附件 | transcript 与消息同一 turn/hash |
| M08 | 聊天附件组合 | 上传文本附件后消息引用 attachment ID | 引用其他会话附件 | 合法引用 201，越权 422 |
| M09 | 截图证据 | PNG 上传并投递到 comment/run | 非图片、缺 file、非法 target | artifact 持久化，delivery 状态可审计 |
| M10 | Agent 管理 | 创建多个角色 Agent、列举绑定 | 缺 member/name、provider 失败 | binding/profile 一致且可追溯 |
| M11 | 自定义编排 | design -> developer -> test -> report | 跳过可选阶段、重复 stage、缺 report | 模板校验 422，规范化顺序稳定 |
| M12 | 自动需求流水线 | 需求到报告的 1-7 阶段 | 阶段乱序、错误 agent、旧版本结果 | 409，历史不可变 |
| M13 | Bug 打回 | 集成失败建 Bug，原 session 修复 | 超过重试上限、未知 Bug | session/workdir 相同，状态可恢复 |
| M14 | 幂等与重试 | 相同 key 重放得到相同响应 | 相同 key 不同 body | `Idempotency-Replayed` 或 409 |
| M15 | 并发需求 | 同一用户同时启动两个需求 | 两个结果交叉写入 | ID、session、work item 全部不同 |
| M16 | 多用户并发 | 两个 workspace 同时创建资源 | 互相读取、写入对方资源 | 无串租户，审计 workspace 正确 |
| M17 | 持久化重启 | 停止 API 后重新启动 | 中断写入、半成品上传 | 已确认数据可读，未确认数据不可见 |
| M18 | Harness 连续性 | transcript/checkpoint/context 编译 | hash 断裂、旧 checkpoint、预算耗尽 | integrity 明确失败，不能静默修复 |
| M19 | Lease/outbox | 重复 worker、丢响应重试 | lease 过期、重复副作用 | 单次 side effect，outbox 最终收敛 |
| M20 | 外部依赖 | provider、artifact、MCP 健康检查 | 不可达、超时、凭据缺失 | readiness fail-closed，错误码稳定 |
| M21 | Runner/执行 | 心跳、命令、快照、日志 | 超时、退出码非零、非法命令 | 运行快照含 session/workdir/result |
| M22 | 评论与跟进 | 需求/Bug 评论、回复、follow-up | 错误 parent、重复 dispatch | root/thread/follow-up 关系完整 |
| M23 | 发布资产 | SBOM、第三方许可、归档 | 缺 license、坏 JSON/YAML | `make supply-chain` 与合同检查通过 |
| M24 | API 合同 | OpenAPI、错误 envelope、request id | 未知路由、错误 method、坏 JSON | 400/404/405，含 request id |

## 真实执行命令

```bash
make test
make test-race
make vet
make contracts
make supply-chain
npm run test:e2e
npm run test:e2e:matrix
ADRO_REQUIRE_CODEX=1 make real-e2e
```

`make real-e2e` 会创建临时 Git 工作区，启动本地 API/Web，调用实际 Codex，执行真实代码修改、单测、集成失败、Bug 打回、重试和报告检查。系统黑盒组合检查由 `scripts/release-system-e2e.sh` 执行；它与真实流水线共享同一 Codex 前置条件。

## 证据要求

- 每次发布保存 commit、测试命令、执行器版本、request ID、session ID、workdir、状态历史和失败日志摘要。
- CI 的真实执行器 job 必须使用自托管 `adro-codex` runner 或等价受控环境，并通过 secret 注入凭据；凭据缺失时 job 明确标记为 blocked/failure。
- 浏览器 fixture 只证明 UI/控制面回归，不得被写成模型完成了交付。
- 任何失败都要保留最小可复现输入和脱敏日志；不得上传 token、cookie、完整 prompt 或用户代码密钥。
