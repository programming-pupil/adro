# ADRO 发布前专家级测试用例规范

版本：`v0.1.0`（以测试执行时检出的提交为准）
编写日期：2026-09-02
适用范围：ADRO 单机部署、Web 控制面、HTTP API、运行时 Provider，以及待接入的真实 Codex 执行链路

## 1. 目的与执行边界

这是一份给 QA 和发布负责人使用的“用例规格”，不是测试结果报告。本文件只描述如何测、测什么、什么算通过以及需要保留哪些证据；本轮没有启动 ADRO、没有调用真实 Codex，也没有把未执行的项目标为通过。

本规范以 ADRO 源码为准，当前源码事实包括：

- Web 菜单在 `apps/web/index.html` 与 `apps/web/enhancements.js` 中定义，共 18 个视图。
- API 契约在 `openapi/openapi.yaml` 中定义；下文的 API 矩阵覆盖其中全部 112 个 operation。
- Go 单元/集成测试位于 `internal/**`；浏览器测试位于 `e2e/**`；当前仓库提供的真实上游链路脚本是 `scripts/multica-conformance.mjs`，其余真实链路需要按本规范补齐。
- `make verify` 组合 Go、契约、构建、依赖和浏览器检查；当前 Makefile 没有 `real-e2e` 目标，真实 Provider/Codex 门禁必须显式调用脚本并在 CI 中单独接入。

明确不纳入本次单机发布门禁的能力：PostgreSQL/Redis/NATS 等生产 adapter、多副本跨节点 conformance。它们必须作为后续发布档案中的 `out of scope / blocked` 记录，不得被单机测试结果替代。

### 1.1 当前自动化基线的诚实结论

从当前源码配置可以确认：`package.json` 只有 `test:e2e` 和 `test:e2e:matrix` 两个 Playwright 入口；现有 `e2e/workbench.spec.js`、`platform-matrix.spec.js`、`visuals.spec.js` 仍是有限的冒烟/视觉用例，并未覆盖本文件的全部控件、Agent 拓扑、并发和故障组合。`.github/workflows/ci.yml` 也没有真实 Codex 专用 job。故当前代码库不能被描述为“企业级全量自动化已完成”；本文件定义的是必须补齐并接入门禁的目标测试集。

## 2. 结果、严重性和证据规则

每个 Case 必须填写：`结果(PASS/FAIL/BLOCKED/NA)`、执行人、时间、源码提交 SHA、配置摘要、证据位置和缺陷编号。`BLOCKED` 只能说明前置依赖缺失，不能算 PASS。

严重性：

| 等级 | 定义 | 发布处理 |
|---|---|---|
| S0 | 数据越权、凭据泄露、重复执行产生不可逆副作用、状态/事件账本损坏 | 立即停止发布 |
| S1 | 主流程不可用、恢复后丢失上下文/证据、真实 Codex 链路失败、幂等或一致性破坏 | 阻断发布 |
| S2 | 单一菜单/API 或边界错误，存在可控绕过 | 修复后回归；未修复需书面豁免 |
| S3 | 文案、布局、非关键诊断缺陷 | 记录并排期 |

证据最少包括：请求方法/URL/脱敏后的 header 和 body、响应状态和 body 摘要、事件流原文或哈希、数据库/JSON 快照前后哈希、浏览器截图/trace、Runner/Provider 日志、GitHub job URL。凭据、Cookie、token 和用户数据必须脱敏。

## 3. 单机执行基线

### 3.1 环境和启动

1. 使用干净的 ADRO checkout，记录 `git rev-parse HEAD`；Go、Node、npm、Ruby 和 Playwright 版本写入报告。
2. 使用独立临时 `ADRO_STATE_FILE`、`ADRO_ARTIFACT_ROOT`、`ADRO_RUNTIME_JOURNAL`，目录权限为仅执行用户可读写；验证重启后文件仍可读取。
3. 通过 `./start.sh` 或 `go run ./cmd/adro-api` 启动单进程服务；记录 `/healthz`、`/readyz` 返回和监听地址。
4. 浏览器执行前运行 `npm ci`、`npx playwright install --with-deps chromium firefox webkit`，静态服务器和 API base URL 写入报告。
5. 当前源码没有 `ADRO_REQUIRE_CODEX`、`ADRO_CODEX_BIN` 或直接启动本地 Codex 的实现。现有真实 conformance 使用 `ADRO_PROVIDER=multica`、`ADRO_MULTICA_URL`、`ADRO_MULTICA_TOKEN` 和 `ADRO_CONFORMANCE_*`；若目标是本地 Codex，必须先实现并测试真实 Provider adapter，再确认 `codex --version`、PID、工作目录、权限和网络策略。任何阶段都禁止以 mock、stub 或“伪造事件”代替。

### 3.2 固定测试数据

| 数据 | 说明 |
|---|---|
| `tenant-a/ws-a`、`tenant-b/ws-b` | 两个租户和工作空间，验证隔离 |
| `admin-a` | 唯一初始管理员；再创建第二管理员用于最后管理员保护 |
| `member-a`、`member-b`、`viewer-a`、`disabled-a` | 成员、查看者和禁用账号 |
| `repo-a`、`repo-b` | 一个可索引 Git 仓库、一个故意不可达或权限不足的仓库 |
| `agent-plan`、`agent-code`、`agent-test`、`agent-repair` | 分别承担方案、开发、测试、修复角色，使用最小权限 |
| `req-happy`、`req-concurrent-1/2` | 正常、同用户并行和跨用户并行需求 |
| 附件集合 | 0 字节、1 字节、1 MB、超限、重复 hash、恶意扩展名、损坏图片、分片乱序 |
| Codex 指令 | 成功、工具调用、需要审批、超时、退出码非零、进程被杀、输出含大 token 文本 |

## 4. 菜单/UI 覆盖矩阵

以下每行至少执行 `UI-001`（可见性/加载/刷新/空态/错误态）和对应的操作用例；管理员可见全部菜单，成员只看到授予的菜单，查看者不能出现写操作。

| Case ID | 菜单/视图 | 必测行为 | 主要入口 |
|---|---|---|---|
| UI-001 | 全部菜单 | 登录后逐一点击、深链接刷新、返回/前进、加载失败和空数据态；控制台无未处理异常 | Playwright `e2e/workbench.spec.js` |
| UI-002 | `workbench` 工作台 | 活跃交付、待审批、开放 Bug、最新事件数量与 API 一致；全局搜索命中/不命中 | `/api/v1/requirements`、`/api/v1/streams/workspaces/{workspace_id}` |
| UI-003 | `requirements` 需求中心 | 创建需求，绑定多个仓库，过滤标题/Key/状态，打开详情和状态时间线 | `POST/GET/PATCH /api/v1/requirements*` |
| UI-004 | `bugs` Bug 中心 | 创建 Bug、关联需求/工作项、triage、repair、verify；错误提示可重试 | `/api/v1/bugs*` |
| UI-005 | `humanQA` 人工验收 | 只显示待验收交付；接受、拒绝/阻塞时理由和 Evidence 可追踪 | requirement gates/approve/evidence |
| UI-006 | `designReview` 方案评审 | 方案门禁、审批前后按钮和状态正确；无权限用户不可决策 | gates/approvals |
| UI-007 | `executions` 研发执行 | 工作项、Run、Provider 状态和 session/workdir 关联；取消后状态不可回到运行 | work-items/runs |
| UI-008 | `diffs` 代码与 Diff | 版本化 diff、changed files、空 diff、二进制 diff、越权仓库不可见 | `/api/v1/work-items/{id}/diff` |
| UI-009 | `testing` 测试中心 | Gate、Evidence、失败重跑和人工验收状态；缺证据必须显示阻断 | evidence/requirements |
| UI-010 | `repositories` 项目与仓库 | 注册、编辑、删除、索引、依赖图；索引中/失败/无权限可见 | repositories/index/graph |
| UI-011 | `agents` Agent 与小队 | 创建四类 Agent、编辑/禁用、MCP/Skill 绑定，显示工作项路由 | agents/bindings |
| UI-012 | `mcp` MCP 服务器 | 注册、discover、health-check、approve、invoke、审计；不可达和 schema 变化明确 | mcp endpoints |
| UI-013 | `skills` Skills | 草稿版本、发布、回滚、不可变版本和并发发布冲突 | skills endpoints |
| UI-014 | `automations` 自动化 | 编辑触发器/节点、发布、暂停、手动触发、运行取消和 takeover | automations/runs |
| UI-015 | `integrations` 集成中心 | Provider/ArtifactStore/Identity/EventBus 状态、诊断刷新、未配置提示 | diagnostics/health |
| UI-016 | `artifacts` 扩展与存储 | 上传会话、分片、完成、内容读取、迁移暂停/恢复/回滚 | artifacts/migrations |
| UI-017 | `runners` Runner 管理 | 注册、心跳、execute、drain、quarantine；容量和安全域限制 | runners endpoints |
| UI-018 | `cost` 成本中心 | metrics/usage 与 Run 对账；无数据、负值、超大值不能破坏渲染 | `/metrics`、`/api/v1/runs/{id}/usage` |
| UI-019 | `admin` 系统管理 | 用户 CRUD、角色/菜单权限、禁用会话回收、审计查询；最后管理员保护 | users/directory/audit |
| UI-020 | 移动与跨浏览器 | Chromium/Firefox/WebKit 桌面，移动 viewport；横向滚动、键盘焦点、上传和 dialog 可用 | `e2e/platform-matrix.spec.js`、`visuals.spec.js` |

上一版自检结论：UI-001..020 只能证明“页面大致能打开”，不能证明每个菜单的按钮、表单分支、空态、错误态和状态驱动动作都被执行；也没有把每个需求的 Agent 组合定义成独立验收项。下面的控件级和编排级用例用于补足这一差距。

### 4.1 控件和交互级用例

每个 Case 都必须在至少一个成功数据集、一个空数据集和一个失败数据集执行；“点击后页面仍可见”不算通过，必须断言请求、响应、状态/事件变化和可见文案。

#### 全局壳层与会话

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| SHELL-001 | 未登录打开根路径、刷新、使用非法 `?api=` | 只显示登录门；API 不可达时显示离线态，不抛未处理异常 |
| SHELL-002 | 登录框空值、错误密码、停用账号、连续失败后正确密码 | 提交按钮状态、错误提示和限流符合 API；不泄露账号存在性 |
| SHELL-003 | 点击登录页语言切换，再登录后切换应用语言 | `lang`、按钮、表头、错误提示完整切换；不丢当前视图和表单数据 |
| SHELL-004 | 登录后点击刷新、全局搜索输入/清空、浏览器前进后退 | 仅触发预期请求；列表、计数和当前视图最终一致 |
| SHELL-005 | 检查用户头像、显示名、角色、退出登录 | 身份来自 `/auth/me`；退出关闭 WebSocket 并使旧 Cookie 失效 |
| SHELL-006 | member/viewer 登录，逐项检查 18 个导航节点和直接深链 | 未授权菜单隐藏，直接请求仍被后端拒绝；不能通过改 DOM 绕过 |
| SHELL-007 | 断开 API、断开 WebSocket、恢复网络 | 连接点、重试和错误态可见；恢复后 cursor 续接，不重复追加行 |
| SHELL-008 | 所有菜单运行键盘 Tab/Enter/Escape、屏幕阅读器属性和窄 viewport 检查 | 焦点顺序稳定；dialog 可关闭；无横向溢出、遮挡或不可访问控件 |

#### `workbench` 工作台

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| WB-001 | 创建不同状态的需求、Bug、待审批项，刷新工作台 | workflow rail 五阶段计数、四张 metrics 卡与 API 数据逐项相等 |
| WB-002 | 在最近需求表点击行、Enter、无需求空态 | 详情 dialog 打开且 ID/标题/状态/版本/关联数正确；空态不显示假数据 |
| WB-003 | 产生 10+ 事件并重连 stream | 活动列表按最新倒序显示；事件标签、aggregate、时间可追溯 |
| WB-004 | 全局搜索 Key、标题、执行人、大小写和特殊字符 | 只过滤匹配项；清空恢复；搜索不会向服务端注入任意路径 |
| WB-005 | 需求从 RECEIVED 到 RELEASED、CANCELLED、FAILED 的各状态刷新 | 计数只落在正确阶段；已终态不出现在 active delivery |
| WB-006 | provider diagnostics 不可达、未配置、认证失败、可达四种 profile | provider 卡和诊断条分别显示真实状态；不可把 mock/未验证显示为已连接 |

#### `requirements` 需求中心和详情

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| REQ-UI-001 | 打开新建需求 dialog，检查标题、描述、验收标准、优先级、仓库、执行人控件 | 必填、placeholder、选项来源和焦点符合源码；无仓库/无执行人有明确空态 |
| REQ-UI-002 | 标题/描述/验收标准为空、全空格、超长、多行 Unicode；优先级非法 | 前端阻止明显无效输入；后端 422；不创建残留需求 |
| REQ-UI-003 | 绑定 0/1/多个仓库和执行人，提交相同表单两次 | 服务器保存规范化集合；幂等键重放只产生一个需求 |
| REQ-UI-004 | 同时选择多个附件：0 字节、20 MiB 边界、超过 20 MiB、同名文件 | 需求创建与附件上传结果分别可见；部分失败有可重试提示，不伪造已上传 |
| REQ-UI-005 | 列表状态过滤、标题/Key 搜索、无匹配和刷新 | 过滤只改变列表，不改变后端状态；分页/limit 不丢数据 |
| REQ-UI-006 | 详情动作：start、confirm-assignees、begin-design、design gate、approve、accept | 每个状态只出现合法按钮；If-Match/version 正确；成功后详情和事件更新 |
| REQ-UI-007 | 详情动作重复点击、请求延迟时关闭 dialog、网络错误后重试 | 按钮禁用防双提交；错误后恢复可操作；不产生双重 transition |
| REQ-UI-008 | 用旧版本编辑标题/状态，另一个用户先更新 | 返回冲突；页面提示刷新；本地旧值不覆盖新值 |
| REQ-UI-009 | 关联仓库/执行人、影响报告生成和确认，版本过期/跨需求 ID | 关联去重；报告 version 严格匹配；越权或过期确认无副作用 |
| REQ-UI-010 | 需求由正常、失败、阻塞、暂停恢复后重新打开 | 状态、工作项、附件、事件、错误原因均可反查，不能显示“已完成”假象 |

#### `bugs` Bug 中心

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| BUG-UI-001 | 打开新建 Bug，分别填写仓库、需求、复现步骤、期望、实际、日志、附件 | 字段映射正确；关联需求必须属于同一 workspace；附件归属 Bug |
| BUG-UI-002 | 提交完全相同 fingerprint 两次、标题不同但指纹相同 | 第二次返回已存在 Bug，不增加 attempt、不重复事件 |
| BUG-UI-003 | OPEN -> repair，REPAIRING -> verify，HUMAN_TRIAGE_REQUIRED -> triage | 只显示状态允许的动作；按钮动作与 API 状态一致 |
| BUG-UI-004 | 连续 repair 直至上限，再次 repair；Provider 失败 | 达到上限进入人工接管；Provider 错误保留诊断和原 Bug，不假设修复成功 |
| BUG-UI-005 | 按状态筛选/空态/跨 workspace Bug ID | 列表和详情不泄露另一 workspace；空态不渲染动作按钮 |
| BUG-UI-006 | Bug 详情刷新、附件列表、repair attempt 列表 | attempt、session、context、workdir 和证据可追溯；刷新不重置状态 |

#### `humanQA`、`designReview`、`executions`、`diffs`、`testing`

| Case ID | 菜单 | 步骤 | 通过标准 |
|---|---|---|---|
| WF-UI-001 | humanQA | 构造 READY_FOR_HUMAN_QA、ACCEPTED、RELEASED、非验收状态并刷新 | 只列出适用状态；接受/拒绝/阻塞均要求合法 gate/evidence；非验收项不可操作 |
| WF-UI-002 | designReview | DESIGN_REVIEW、HUMAN_APPROVAL_REQUIRED、DESIGN_REWORK 三状态打开详情 | 审批按钮、重做路径和理由字段与状态机一致；无审批权限为只读 |
| WF-UI-003 | executions | 有/无 work item、queued/running/completed/failed/cancelled Run | 工作项和 Run 关联正确；取消后不能再次启动同一活动 Run |
| WF-UI-004 | diffs | 空 diff、文本 diff、二进制/超大 diff、不同 commit | changed files、baseline/head 和摘要正确；不把未发布快照显示为最终提交 |
| WF-UI-005 | testing | 有 Evidence、缺 Evidence、失败 Evidence、重复 Evidence | 测试门禁状态正确；缺关键证据时显示阻断而不是绿色通过 |
| WF-UI-006 | WF 通用 | 点击表格行、Enter、Esc、浏览器刷新和断网 | 所有可点击行可键盘触发；详情关闭/重连后数据不丢 |

#### `repositories` 项目与仓库

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| REPO-UI-001 | 新建仓库：名称、clone URL、provider、默认分支为空/非法/合法 | 必填和 URL 校验明确；保存后 canonical_name、branch、provider 可回读 |
| REPO-UI-002 | 同 URL/同名重复注册、跨 workspace 同名注册 | 按源码契约去重或返回冲突；workspace 隔离 |
| REPO-UI-003 | 点击 index，模拟 working-tree、指定 commit、不存在 commit | 状态 pending/ready/failed 正确；indexed_commit 只在成功后更新 |
| REPO-UI-004 | `GET/PATCH/DELETE /repositories/{id}` 后刷新需求选择器 | 更新即时反映；删除有引用时按契约阻止或级联，不能留下悬挂 ID |
| REPO-UI-005 | repository graph 无边、单边、环、跨 workspace 节点 | 图查询稳定；不存在或越权节点不返回 |
| REPO-UI-006 | 大量仓库、超长名称、特殊字符、不可达 clone URL | 列表性能可记录；错误可重试且不显示凭据/完整 URL token |
| REPO-UI-007 | 通过 API 创建/读取/列出 team workspace；检查当前 Web 菜单是否有对应入口 | API 数据完整隔离；若 UI 没有创建/切换入口，记录为可见产品缺口，不得宣称菜单功能已覆盖 |

#### `agents` Agent 与路由

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| AGENT-UI-001 | 新建 Agent，member/name/instructions/role 分别为空、重复、超长 | 422/冲突信息明确；成功返回 profile 和 provider binding 两个可关联 ID |
| AGENT-UI-002 | 列表刷新、无 Agent、同 member 多次创建、禁用/失效 Provider | profile/default binding 与诊断状态一致；不显示虚构的 provider agent |
| AGENT-UI-003 | 配置 member、role、workspace default、legacy 路由并创建工作项 | 路由来源和 config revision 可见，优先级符合源码；修改配置不改写已持久化 work item |
| AGENT-UI-004 | Agent 绑定 MCP/Skill，删除资源后重新读取绑定 | 绑定列表准确；删除/越权按契约拒绝；执行前检查能力版本 |
| AGENT-UI-005 | 以不同 AgentBindingID 启动同一 work item、跨 workspace binding、空 binding | 服务端只允许 work item 当前 binding；冲突无 Provider 副作用 |
| AGENT-UI-006 | 同一 Agent 串行两 Run、并行两 Run、一个失败一个成功 | session/workdir/事件/usage 隔离；并发策略和资源上限可观测 |

#### `mcp`、`skills`、`automations`

| Case ID | 菜单 | 步骤 | 通过标准 |
|---|---|---|---|
| CAP-UI-001 | mcp | 注册 HTTP/SSE/不支持 protocol，缺 endpoint、含 secret configuration | 合法协议保存；不支持/凭据明文被拒绝；secret_ref 不回显 |
| CAP-UI-002 | mcp | discover 成功、不可达、schema 改变后再次 discover | digest 只在成功时更新；不可达标记 unreachable，不伪造健康 |
| CAP-UI-003 | mcp | health-check、approve、invoke 成功/超时/工具不存在/重复 | 状态、调用审计、错误码和耗时正确；批准前不能调用受保护工具 |
| CAP-UI-004 | skills | 创建 draft、创建新版本、publish、rollback、并发发布 | 版本不可变、状态单调/回滚可解释；并发冲突不丢版本 |
| CAP-UI-005 | automations | 合法/非法 JSON trigger/nodes，publish、pause、trigger | JSON 错误不关闭整个页面；发布前校验图；运行状态可查询 |
| CAP-UI-006 | automations | 同一 automation 触发风暴、cancel、takeover 竞争 | 速率/并发上限和唯一 owner 生效；取消后不可继续写入结果 |

#### `integrations`、`artifacts`、`runners`、`cost`、`admin`

| Case ID | 菜单 | 步骤 | 通过标准 |
|---|---|---|---|
| OPS-UI-001 | integrations | provider diagnostics 的 unconfigured/unreachable/auth-failed/reachable；刷新按钮 | 诊断字段与 `/provider/diagnostics` 一致；不把本地 filesystem/in-process 写成外部生产能力 |
| OPS-UI-002 | artifacts | 选择图片、预览、取消、浏览器 `getDisplayMedia` 授权/拒绝 | 预览释放 object URL；拒绝显示失败；不上传未选择文件 |
| OPS-UI-003 | artifacts | target none/issue/comment/run/workspace，缺/错 target ID | 仅保存或正确投递；target 类型和 workspace 校验严格；delivery 状态真实 |
| OPS-UI-004 | artifacts | 上传会话、分片乱序/重复/缺片、complete、range 下载 | hash/size/version 校验；重复分片幂等；未完成对象不可读取 |
| OPS-UI-005 | runners | 注册、heartbeat、execute、drain、quarantine，健康/排空/隔离状态 | 非健康 Runner 不能执行；命令 argv、工作目录、环境和超时边界有效 |
| OPS-UI-006 | cost | 无 Run、正常 usage、超大 token、Provider 不支持 usage | 成本为可解释数值或明确 unavailable；不出现 NaN/负成本；能与 Run 对账 |
| OPS-UI-007 | admin | 创建/编辑 member/viewer/admin/disabled，勾选每个菜单，保存后重新登录 | 18 菜单权限和后端权限逐项一致；禁用即时回收会话 |
| OPS-UI-008 | admin | 编辑自己、删除/禁用最后管理员、两个管理员并发编辑 | 最后管理员保护；冲突可见；审计包含操作者、目标和结果 |
| OPS-UI-009 | admin | 审计空态、连续事件、篡改快照/链校验失败 | 只显示当前 workspace；chain_valid 真实；检测异常而非静默清空 |

### 4.4 前端实现函数到 Case 的反向核对

这是本轮自检清单，用于防止只按菜单名称统计覆盖。下表中的函数名来自 `apps/web/index.html` 和 `apps/web/enhancements.js`；执行报告必须至少引用右侧一个 Case，不能以“所在菜单已打开”替代。

| 源码函数/控件族 | 覆盖 Case |
|---|---|
| `applyTranslations`、`statusLabel`、`statusClass`、`eventLabel`、`localeToggle` | SHELL-003、WB-001、WB-003 |
| `api`、`performCoreLoad`、`loadCore`、`connectStream`、`refreshButton` | SHELL-001、SHELL-004、SHELL-007、WB-003 |
| `updateUserChip`、`showLogin`、`enterApplication`、`applyMenuAccess`、`loadIdentityData` | SHELL-002、SHELL-005、SHELL-006、OPS-UI-007 |
| `metric`、`renderMetrics`、`summaryCard`、`workflowRail`、`eventPanel` | WB-001、WB-003、WB-005、OPS-UI-006 |
| `requirementRows`、`requirementsTable`、`renderWorkbench`、`renderRequirements`、`globalSearch`、`statusFilter` | WB-001..005、REQ-UI-005 |
| `showDialog`、`uploadEntityFiles`、`requirementForm.onsubmit`、`openRequirement` | REQ-UI-001..010、FLOW-001、FLOW-006 |
| `requirementActionFor`、`applyRequirementAction`、`detailAction` | REQ-UI-006..008、STATE-001..003、FLOW-002..003 |
| `showBugDialog`、`bugForm.onsubmit`、`bugRows`、`renderBugs` | BUG-UI-001..006、FLOW-009 |
| `openResourceDialog`、`resourceConfigs`、`closeResourceDialog`、`actionButton` | REPO-UI-001、CAP-UI-001、CAP-UI-004..006、OPS-UI-005 |
| `applyResourceAction`、`localizedResourceStatus`、`renderRepositories`、`renderCapability`、`renderRunners` | REPO-UI-003..006、CAP-UI-002..006、OPS-UI-005 |
| `renderSimple`（humanQA/designReview/executions/diffs/testing） | WF-UI-001..006 |
| `renderAgents`、`showAgentDialog`、`agentForm.onsubmit`、`routeSourceLabel` | AGENT-UI-001..006、AGC-001..017 |
| `renderArtifacts`、`setScreenshotPreview`、`captureScreenshot`、`uploadScreenshot` | OPS-UI-001..004、FLOW-006 |
| `renderIntegrations`、`providerStateKey`、`refreshDiagnostics` | WB-006、OPS-UI-001 |
| `renderCost`、`renderAdmin`、`renderPermissionGrid`、`openUserDialog`、`userForm.onsubmit` | OPS-UI-006..009、AUTH-005..006 |
| `render`、`bindViewEvents`、所有 dialog close/cancel/Escape handlers | SHELL-004、SHELL-008、REQ-UI-007、WF-UI-006 |
| `genericTable`、`optionMarkup`、`formatBytes`、`closeDialog`、`closeAgentDialog`、`bootstrap` | SHELL-008、REQ-UI-001、OPS-UI-004、WF-UI-006、L0/L3 启动检查 |
| `enhancedTranslations`、`enhancedRequirementDialog`、`enhancedResourceDialog`、`enhancedBugTable`、`enhancedAdmin`、`enhancedViewEvents`、`enhancedRequirementDetails` | 对应 REQ-UI/BUG-UI/REPO-UI/CAP-UI/OPS-UI 全部交互回归；包装函数不得绕过基础断言 |

反向核对结果必须是：每个函数族至少有一个成功、一个错误或空态、一个刷新/重入 Case；若函数新增而没有对应 Case，CI 的 coverage ledger 应失败。

### 4.2 Agent 自由组合与编排拓扑用例

源码事实：`POST /api/v1/agents` 创建的是 Provider agent/profile；`developer-profiles` 为 member 保存一个 `default_agent_binding_id`；`ADRO_MULTICA_AGENT_MAP` 解析 member、role、workspace default 等路由优先级；`WorkItem` 目前只有一个 `developer_agent_binding_id`。OpenAPI 中没有按 requirement 保存“阶段 -> Agent -> 前置/后置关系”的 workflow graph operation。因此必须把“现有路由可用性”和“目标能力缺失检测”分开记录。

每个组合都要创建全新的 requirement/work item，记录期望拓扑、实际拓扑、每个节点的 agent binding/session/workdir/attempt，并在 API、事件、审计和最终报告四处核对；不能只看最终状态。

| Case ID | 组合与步骤 | 通过标准/当前判定 |
|---|---|---|
| AGC-001 | A 需求只配置开发 Agent + 测试 Agent；跳过方案设计，开发完成后进入测试 | 若产品允许跳过设计，则只产生 D->T 两节点且 gate 正确；若没有 per-requirement graph 配置入口，记录 S1 BLOCKED |
| AGC-002 | B 需求配置方案设计 -> 开发 -> 单测；每阶段不同 Agent | 三个节点顺序、输入输出、session/context lineage 和责任人可追溯；当前无图 API 时必须失败而非静默使用默认 Agent |
| AGC-003 | 方案 -> 设计评审 -> 开发 -> 单测 -> 人工验收 | 评审拒绝回到设计，不得直接开发；每次重做的 attempt 和 evidence 独立 |
| AGC-004 | 开发 -> 单测与静态检查并行 -> 汇总 | 并行节点不共享可写 workdir/lease；汇总等待全部完成且重复事件幂等 |
| AGC-005 | 开发 -> 测试失败 -> 修复 Agent -> 回归测试 | 只在失败分支调度 repair；同一 bug attempt 上限、原 session/context 和新 run 关系准确 |
| AGC-006 | 方案 Agent 缺失，要求自动跳过；开发 Agent 缺失，要求阻断 | 缺失节点策略显式、可审计；不得用任意默认 Agent 冒充指定角色 |
| AGC-007 | A/B 两个需求使用不同 Agent 集合、相同仓库；同时运行 | 每个需求只调用自己的集合；workspace、session、workdir、事件和 artifact 无串线 |
| AGC-008 | 同一个 Agent 在两个需求中复用；一个需求取消、另一个继续 | Agent 级能力可复用但 run 级状态隔离；取消不影响另一需求 |
| AGC-009 | 同角色多候选 Agent，按 member > role > workspace default 选择 | 路由优先级、config revision、最终 binding 全部一致；运行中配置变化不改写已选节点 |
| AGC-010 | 指定 Agent 绑定不存在、已删除、跨 workspace、无 MCP/Skill 权限 | 创建图或启动前 fail-closed；无 Provider 调用和副作用 |
| AGC-011 | 串行链、扇出、扇入、条件分支、重试边 | 拓扑验证拒绝环和悬空节点；每条边有触发条件和超时；条件未满足不误调度 |
| AGC-012 | 节点重复提交、网络重试、父节点事件乱序 | 节点幂等键/lease/fencing 防止双执行；子节点只在父节点满足条件后启动 |
| AGC-013 | 运行中修改 Agent graph、删除当前 Agent、更新 role route | 已开始 run 使用不可变编排快照；新 run 使用新 revision；审计记录变更影响范围 |
| AGC-014 | 组合中加入人工审批、MCP 工具、Skill 版本和截图证据 | 工具批准绑定到正确 Agent/node/run；证据归属正确；未经批准不能越权调用 |
| AGC-015 | 全链路故障：方案成功、开发进程崩溃、测试超时、修复失败 | 每节点状态和恢复策略独立；最终需求进入阻塞/人工接管，不得显示 released |
| AGC-016 | 同一用户同时提交 A(D,T) 和 B(Design,D,Unit)；两个用户同时提交 C、D | 至少四条独立 trace；资源竞争、锁、租约、事件 cursor、费用、审计互不污染 |
| AGC-017 | 重新打开需求详情，导出编排和执行报告 | 报告能重建“为什么选择这些 Agent、何时、哪个版本、结果如何”；缺字段即 S1 |

AGC-001..017 的目标验收不是“默认路由能跑通”。如果 QA 无法在 ADRO API/UI 中表达 A 与 B 两种不同拓扑，必须将能力缺口记录为发布阻断，并创建实现任务：持久化 immutable workflow graph、节点级 agent binding、边条件、版本、校验、并发 lease、事件和重放契约，再重新执行全部 AGC 用例。

### 4.2.1 Squad/小队专专项用例

这是对用户要求的“很多 Agent 按职责组成可复用小队，并在发布需求时选择 Agent 或小队”的独立验收集。它不能被单个 Agent 创建成功所替代。

| Case ID | 场景与步骤 | 通过标准 |
|---|---|---|
| SQUAD-001 | 新建“开发+测试”小队，成员分别绑定开发 Agent、测试 Agent，保存并重新打开 | 小队有稳定 ID、名称、版本、workspace、成员顺序/关系；两个 Agent 的职责和能力快照可回读 |
| SQUAD-002 | 新建“方案->开发->单测”小队，三个节点分别绑定 design/developer/unit-test Agent | 节点和边明确表达串行依赖；每个节点有输入/输出契约、超时、重试和 owner |
| SQUAD-003 | 从发布需求 dialog 快捷创建小队，再立即选择该小队执行 | 创建、选择、需求保存和 work item 生成在一个可追踪事务中；取消快捷创建不留下孤儿资源 |
| SQUAD-004 | 发布需求时选择单个 Agent；另一需求选择小队；第三需求覆盖小队中的一个节点 | 三种模式均可表达；最终编排快照写入需求/work item；覆盖行为有审计且不改写小队模板 |
| SQUAD-005 | 同一小队被 A、B 两需求复用，A 只跑开发/测试，B 跑完整方案/开发/单测 | 每个需求可选择小队子图或 profile；成员、顺序、上下文、session、workdir 和事件完全隔离 |
| SQUAD-006 | 小队成员增删、禁用、替换 Agent；已有需求运行中修改模板 | 已运行实例使用 immutable snapshot；新需求使用新版本；替换前检查能力和权限兼容 |
| SQUAD-007 | 配置串行、并行 fan-out/fan-in、条件边、失败转人工、重试边 | 拓扑校验拒绝环、悬空节点和未满足条件；并行节点不共享可写资源；汇总等待策略可观测 |
| SQUAD-008 | 小队中某 Agent 无 Provider binding、无 MCP/Skill、跨 workspace 或已删除 | 保存/发布/启动在明确阶段 fail-closed；不回退到默认 Agent，不创建半成品 Run |
| SQUAD-009 | 小队草稿、发布、归档、复制、回滚；两人并发编辑同一版本 | 版本不可变；发布有审批/审计；并发编辑 409；旧需求仍指向原版本 |
| SQUAD-010 | member/viewer/admin 对小队创建、编辑、执行、查看成员和模板的权限矩阵 | 最小权限生效；viewer 无写入；跨 tenant/workspace 返回 403/404 且不泄露成员信息 |
| SQUAD-011 | 空小队、单节点小队、重复成员、重复边、环、超深/超大图、非法角色 | 输入限制、深度/节点/边上限和错误码稳定；不发生栈溢出或无限调度 |
| SQUAD-012 | 方案成功、开发失败、单测超时、修复 Agent 介入、人工批准后继续 | 每个节点状态/attempt/evidence 独立；失败策略不跳过必需门禁；恢复只补偿一次 |
| SQUAD-013 | 同一小队同时运行 20 个需求；同一需求重复提交；两个用户抢同一节点 | lease/fencing/配额/背压生效；至多一次副作用；公平性和拒绝原因可观测 |
| SQUAD-014 | 小队节点调用 MCP、使用 Skill 版本、上传截图/附件、产生 Bug 并 repair | 能力批准绑定到具体小队节点/run；证据和附件归属正确；repair 延续正确 lineage |
| SQUAD-015 | 发布前 dry-run/模拟执行，展示预计节点、权限、资源、成本和风险 | 不执行真实副作用；报告与实际运行快照一致；缺失能力在发布前暴露 |
| SQUAD-016 | 导出小队模板、审计成员/版本/决策、删除/保留策略、跨环境导入 | 导出不含 secret；审计可重放；引用中的版本按保留策略处理；导入校验签名和能力 |
| SQUAD-017 | 从当前仅有 default agent 的旧需求升级到小队模型，再回滚版本 | 旧数据可读且语义不变；迁移可重入；回滚不丢旧 binding、事件和证据 |

### 4.2.2 当前支持结论和前瞻缺口

基于当前 `internal/domain/domain.go`、`internal/provider/routing.go`、`internal/api/server.go`、`openapi/openapi.yaml` 和 `apps/web` 的源码复核：

- 当前支持的是单个 Agent/profile 创建、Provider binding、MCP/Skill binding、开发者默认 Agent，以及 member/role/workspace-default 路由优先级。
- `TeamWorkspace` 只是 workspace 资源，字段是 `repository_ids`、`policy`、`status` 等，没有 Agent 成员、节点、边或执行顺序；不能当作 Squad 实现。
- Web UI 只有“创建 Agent”入口，没有“创建小队/编辑小队拓扑/在需求发布时选择小队或子图”的入口。
- Requirement/WorkItem 没有需求级 workflow graph 或节点级 Agent 列表；WorkItem 当前只有一个 `developer_agent_binding_id`。

因此，用户描述的 Squad/小队和自由流水线能力当前**不支持**，不是“隐藏支持”或“只差 UI”。SQUAD-001..017 在现状下应记录为 `BLOCKED/S1`，不能用 Agent 默认路由跑通来替代。要达到“AI agent 团队”产品目标，至少需要新增可版本化的 `Squad`、`SquadMember`、`WorkflowTemplate`、`WorkflowNode`、`WorkflowEdge`、`RequirementExecutionPlan` 和 `WorkflowRun` 契约，并提供 API、UI 快捷入口、权限、校验、调度、lease、事件、审计和回放。

建议一并纳入开发验收的超前能力：

1. 编排模板的静态 lint、dry-run 和能力/权限/成本预检，发布前发现不可执行节点。
2. 节点输入输出 schema、上下文选择和 memory namespace，防止不同 Agent 互相污染 prompt 或记忆。
3. 节点级预算、速率、并发、优先级、租约和公平调度，避免一个 Agent 拖垮整个小队。
4. immutable plan/version、灰度发布、回滚、兼容迁移和运行中快照，确保模板变更不影响已开始需求。
5. Agent 质量评分、评测集、失败归因、自动降级/替换和人工接管，而不是只依据“Run completed”。
6. 小队级安全域和工具策略，MCP/Runner/Artifact 权限按节点最小化，并对 secret、代码和网络实施隔离。
7. 预算与成本归集、SLO、节点级 tracing、事件重放和可解释报告，回答“为什么由这个 Agent 在这个版本执行”。

### 4.3 需求状态机逐边测试

`internal/domain/domain.go` 当前定义的允许边如下。每条允许边至少执行一次成功迁移；从每个状态随机选取两条未列出的目标执行拒绝测试，并验证 version、audit、event 和副作用均不变化。终态 `RELEASED`、`CANCELLED` 只能接受同状态幂等请求（若 API 契约允许），不能回退。

| 当前状态 | 允许的下一状态 |
|---|---|
| `RECEIVED` | `TRIAGED`、`CANCELLED` |
| `TRIAGED` | `ASSIGNEES_CONFIRMED`、`CANCELLED` |
| `ASSIGNEES_CONFIRMED` | `DESIGNING`、`CANCELLED` |
| `DESIGNING` | `DESIGN_REVIEW`、`DESIGN_REWORK`、`BLOCKED_PROVIDER` |
| `DESIGN_REVIEW` | `DEVELOPING`、`DESIGN_REWORK`、`HUMAN_APPROVAL_REQUIRED` |
| `DESIGN_REWORK` | `DESIGNING`、`CANCELLED` |
| `DEVELOPING` | `UNIT_VERIFIED`、`TEST_FAILED`、`BLOCKED_PROVIDER` |
| `UNIT_VERIFIED` | `API_DOC_READY`、`TEST_FAILED` |
| `API_DOC_READY` | `TEST_DEPLOYING`、`BLOCKED_ENVIRONMENT` |
| `TEST_DEPLOYING` | `TESTING`、`BLOCKED_ENVIRONMENT` |
| `TESTING` | `READY_FOR_HUMAN_QA`、`TEST_FAILED`、`BLOCKED_ENVIRONMENT` |
| `TEST_FAILED` | `AUTO_REPAIRING`、`HUMAN_TRIAGE_REQUIRED` |
| `AUTO_REPAIRING` | `DEVELOPING`、`TEST_FAILED`、`HUMAN_TRIAGE_REQUIRED` |
| `READY_FOR_HUMAN_QA` | `ACCEPTED`、`DEVELOPING`、`HUMAN_APPROVAL_REQUIRED` |
| `ACCEPTED` | `RELEASED` |
| `HUMAN_TRIAGE_REQUIRED` | `DEVELOPING`、`CANCELLED` |
| `HUMAN_APPROVAL_REQUIRED` | `DEVELOPING`、`ACCEPTED`、`CANCELLED` |
| `BLOCKED_PROVIDER` | `DESIGNING`、`DEVELOPING`、`CANCELLED` |
| `BLOCKED_ENVIRONMENT` | `TEST_DEPLOYING`、`TESTING`、`CANCELLED` |
| `BLOCKED_ARTIFACT_STORE` | `TESTING`、`READY_FOR_HUMAN_QA`、`CANCELLED` |
| `RELEASED`、`CANCELLED` | 终态；仅同状态幂等（不得回退） |

| Case ID | 补充断言 |
|---|---|
| STATE-001 | 对上表所有允许边逐条调用 `POST /api/v1/requirements/{id}/transition`，保存 from/to/version/事件；结果与 domain 表完全相等 |
| STATE-002 | 对每个状态尝试至少两条非法边、未知 enum、空 status、负/过大 version；统一 400/409 语义且状态、事件、审计不变 |
| STATE-003 | 同状态重复 transition、重复 action、并发 If-Match；验证幂等、冲突和 version 单调，不出现 lost update |

## 5. 认证、会话、租户和 RBAC

| Case ID | 场景与步骤 | 通过标准 | 入口/证据 |
|---|---|---|---|
| AUTH-001 | 正确账号登录；检查 Cookie、`/auth/me`，刷新后继续 | 返回 HttpOnly 会话 Cookie；密码不回显；me 身份正确 | `POST /api/v1/auth/login` |
| AUTH-002 | 错误密码、未知用户、空字段、超长字段、Unicode 字段和重复登录 | 统一错误语义，不泄露用户是否存在；限流按源码契约生效 | login response、audit |
| AUTH-003 | logout 后访问受保护 API，旧 Cookie 重放 | 401；服务端会话失效且审计记录登出 | logout + requirements |
| AUTH-004 | 会话过期、服务重启、状态文件恢复；并行请求同一 Cookie | 过期拒绝；允许的单机恢复行为与文档一致；无竞态崩溃 | auth state snapshot |
| AUTH-005 | admin 创建 member/viewer/disabled 用户；编辑密码、角色、菜单 | 生效范围精确；禁用用户所有既有会话失效；审计包含操作者 | users CRUD |
| AUTH-006 | 删除/禁用唯一管理员、并发修改两个管理员 | 最后管理员保护；乐观版本或冲突返回 409；不产生无管理员窗口 | users PATCH |
| AUTH-007 | member/viewer 对每个写 API、管理 API、跨 workspace ID 发起请求 | 403/404 符合资源隐藏策略；无副作用、无信息泄露 | 全 API 矩阵 |
| AUTH-008 | 同名资源在 `ws-a` 和 `ws-b`；切换租户参数、伪造 body workspace_id | 只能读写所属 workspace；服务端以会话/资源归属为准，不信任客户端字段 | requirements/repos/streams |
| AUTH-009 | CORS、CSRF、Origin、超大 Cookie、缺失/重复 Authorization header | 仅允许源码配置的来源和方法；拒绝跨站写入；错误不泄漏 token | HTTP capture |

## 6. 需求、工作项、Agent、Session、Run 全链路

每个正常链路必须生成一份唯一的 `trace_id`，并能从需求反查 `pipeline/work_item/agent/session/run/context/evidence/attachment/bug/repair`。

| Case ID | 场景与步骤 | 通过标准 |
|---|---|---|
| FLOW-001 | 创建需求，绑定 `repo-a`/`repo-b`、验收标准、责任人；查询详情和 work-items | 201；字段规范化；关联集合完整；重复 `Idempotency-Key` 只创建一个 |
| FLOW-002 | 需求 start，confirm-assignees，begin-design，gates，approve，transition 至 testing/ready/released | 仅允许 domain 合法状态迁移；非法跳转 409/422 且状态不变；每步有审计和事件 |
| FLOW-003 | pause/resume；暂停期间重复 start/run；恢复后继续同一上下文 | 只有一个活动执行；恢复不丢 session/context/lease；事件顺序可解释 |
| FLOW-004 | 为四个 Agent 建立不同角色、MCP/Skill 绑定；配置串行、并行、条件和失败转移关系 | 编排图无环（若契约要求）；无权限 Agent 被拒绝；每个节点输入输出和负责人可追踪 |
| FLOW-005 | 指定 Agent A 方案 -> B 开发 -> C 测试 -> D 修复；执行真实 Provider/Codex | 每节点实际调用真实执行进程；session/workdir/commit/check/submit/attachment 均有证据；当前无本地 Codex adapter 时必须 BLOCKED |
| FLOW-006 | 一个问答输入同时绑定项目、上传附件和截图，再发 follow-up | 上下文包含项目基线、附件 hash、截图 artifact、会话历史；响应引用正确资源 |
| FLOW-007 | Work item run，追加 message，查询 snapshot/events/usage，完成后读取 diff | snapshot 与事件最终一致；message 幂等；usage 可对账；diff 与提交一致 |
| FLOW-008 | Provider 需要审批；先拒绝再重试；批准后只执行一次 | 拒绝不会执行工具；批准绑定精确 run/tool/call；重复决定幂等或 409；审计完整 |
| FLOW-009 | 生成 Bug，triage -> repair -> verify；repair 必须复用原 session/context | repair attempt 递增且有上限；同 session/workdir/context lineage；验证失败转人工接管 |
| FLOW-010 | 影响报告生成并 confirm；报告版本过期、重复确认、跨需求确认 | 版本冲突拒绝；确认幂等；只能操作所属需求 |
| FLOW-011 | 相同用户同时启动 `req-concurrent-1/2`，各绑定不同仓库和 Agent | 两条 trace、session、workdir、events、锁和 artifacts 完全隔离；一条失败不污染另一条 |
| FLOW-012 | `member-a`、`member-b` 同时操作同一需求和不同需求 | 冲突返回可重试错误；无 lost update；审计操作者正确；最终状态符合显式胜者规则 |
| FLOW-013 | 浏览器刷新/断网重连时同时存在两个 Run；重新打开执行详情 | cursor/last-event 后续重放不丢不重；UI 最终收敛；连接关闭释放资源 |

## 7. Session、ContextManifest、压缩、记忆与恢复

| Case ID | 场景 | 通过标准与证据 |
|---|---|---|
| CTX-001 | 首次 run 创建 session；续跑传入 session/context/version | 服务端拒绝未知或跨 workspace session；manifest digest、selection digest、replay key、block lineage 全部校验 |
| CTX-002 | 同 session 追加输入；并发追加相同/不同消息 | 顺序由服务端定义；重复消息按幂等键不重复计费/执行；响应带版本 |
| CTX-003 | 触发 token hard budget；上下文达到压缩阈值 | 压缩前后保留系统约束、验收标准、未完成工具和 lineage；记录压缩事件、输入输出 token 和 digest |
| CTX-004 | 压缩过程中进程崩溃，重启后继续 | 从最后完整 checkpoint 恢复；半成品 manifest 不可见；不得重复工具副作用 |
| CTX-005 | 溢出恢复：压缩后仍超限、空历史、超大单块、Unicode/二进制内容 | 返回可诊断的 overflow/invalid_context；不静默截断关键约束；可按契约安全降级或阻断 |
| CTX-006 | 保存多个 ContextManifest 版本，读取指定版本和最新版本 | 版本单调、digest 可复算、旧版本不可变；`/work-items/{id}/context` 返回 provenance |
| CTX-007 | 篡改 manifest digest、selection、replay key、block lineage；跨 work item 复用 context_id | 400/409；不启动 Provider；安全日志含拒绝原因但不含敏感 prompt |
| CTX-008 | Memory 写入短期/长期/事实/摘要 tier；质量评分为空、负数、超范围、重复事实 | 生命周期、来源、置信度、TTL 和删除策略符合契约；低质量记忆不得污染后续 context |
| CTX-009 | 用户删除/导出记忆，workspace 隔离，重启恢复 | 删除后不可被检索或拼入 prompt；导出完整可审计；权限严格隔离 |
| CTX-010 | 同一上下文 repair/rerun；原 run 完成、失败、取消三种状态 | 允许的状态才可续跑；session reuse 标记和 context lineage 正确；禁止把取消 run 当成功基线 |

## 8. StreamEvents、顺序、一致性、幂等、租约和 outbox

| Case ID | 场景 | 通过标准 |
|---|---|---|
| EVT-001 | 订阅 workspace stream，记录 cursor；产生需求/Run/bug/attachment 事件 | 每事件有唯一 ID、aggregate、workspace、sequence、timestamp、schema version；无跨租户事件 |
| EVT-002 | 从空 cursor、有效 cursor、过期 cursor、非法 cursor 订阅 | 空 cursor 从明确起点；有效 cursor 只给后续；过期/非法返回可诊断错误或明确重置，不静默丢事件 |
| EVT-003 | 客户端断线后 ACK 前重连，重复拉取窗口 | 至少一次投递下客户端可去重；重放顺序稳定；ACK 不会跳过未确认事件 |
| EVT-004 | 人为制造乱序、重复、延迟、进程重启 | 投影最终一致；重复事件幂等；sequence gap 告警并触发补偿/阻断 |
| EVT-005 | snapshot 在 completion event 前后读取；本地 Provider 完成与发布竞态 | 对外 terminal 只在对应 completion event 发布后可见；重启恢复的 terminal 有明确标记 |
| EVT-006 | 同一 Idempotency-Key 重复相同 body、不同 body、并发请求 | 相同请求返回同一资源/响应并标识 replay；不同 body 409 `idempotency_key_conflict`；并发至多一个副作用 |
| EVT-007 | lease 获取、续租、过期、持有者错误释放、两个 worker 竞争 | 单一 owner；过期可接管且 fencing token 单调；旧 owner 写入被拒绝；释放幂等 |
| EVT-008 | 状态写入、outbox、audit、event publish 中任一步失败 | 事务边界符合源码设计；重试不重复副作用；outbox 可补发，诊断能定位阶段 |
| EVT-009 | 同一 Run 同时 cancel/complete/repair；工具 start/finish 乱序 | 终态只允许一次；非法转移 fail-closed；open tool、未授权 tool、重复 finish 被拒绝 |

## 9. API 全量操作矩阵

以下矩阵要求每个 operation 至少执行：正常请求、缺必填字段/类型错误、未认证、无权限、跨 workspace、重复幂等键（若支持）、资源不存在、并发/重试（适用时）。详细步骤可复用 `API-BASE-001`，但证据必须按 operation 单独留档。

| 分组 | OpenAPI operation（逐项覆盖） | Case 前缀 |
|---|---|---|
| 认证/目录 | `POST /api/v1/auth/login`；`GET /api/v1/auth/me`；`POST /api/v1/auth/logout`；`GET/POST /api/v1/users`；`PATCH /api/v1/users/{id}`；`GET /api/v1/directory` | API-AUTH-001..006 |
| 健康 | `GET /healthz`；`GET /readyz`；`GET /metrics`；`GET /api/v1/provider/diagnostics` | API-OPS-001..004 |
| 需求 | `GET/POST /api/v1/requirements`；`GET/PATCH /api/v1/requirements/{id}`；`POST /start`；`/transition`；`/gates`；`/approve`；`/confirm-assignees`；`/begin-design`；`/pause`；`/resume`；`GET /work-items`；`POST /assignees`；`POST /repositories`；`POST /impact-reports`；`POST /impact-reports/{version}/confirm` | API-REQ-001..018 |
| Bug | `GET/POST /api/v1/bugs`；`GET /api/v1/bugs/{id}`；`POST /triage`；`POST /verify`；`POST /repair` | API-BUG-001..006 |
| 上传/截图 | `GET/POST /api/v1/attachments`；`POST /api/v1/artifacts/uploads`；`PUT /artifacts/uploads/{upload_id}/parts/{part_no}`；`POST /artifacts/uploads/{upload_id}/complete`；`POST /api/v1/screenshots` | API-FILE-001..008 |
| 迁移/内容 | `POST /api/v1/artifact-migrations`；`GET /{id}`；`POST /pause`；`POST /resume`；`POST /rollback`；`GET /api/v1/artifacts/{id}/versions/{version}/content` | API-MIG-001..007 |
| 工作项/Run | `GET/POST /api/v1/work-items/{id}/diff`；`GET /api/v1/work-items`；`GET /api/v1/work-items/{id}`；`GET /context`；`GET /repair-attempts`；`POST /run`；`GET /api/v1/runs/{id}`；`GET /events`；`POST /messages`；`POST /cancel`；`GET /usage` | API-RUN-001..013 |
| 仓库/空间 | `GET/POST /api/v1/repositories`；`GET/PATCH/DELETE /api/v1/repositories/{id}`；`POST /index`；`GET /api/v1/repository-graph`；`GET/POST /api/v1/team-workspaces`；`GET /api/v1/team-workspaces/{id}` | API-REPO-001..012 |
| 开发者配置 | `GET /api/v1/developer-profiles`；`GET/POST/PATCH /api/v1/developer-profiles/{member_id}` | API-PROFILE-001..004 |
| 审批/证据 | `POST /api/v1/approvals`；`POST /api/v1/approvals/{id}/decide`；`GET/POST /api/v1/evidence` | API-EVID-001..005 |
| MCP | `GET/POST /api/v1/mcp`；`GET/POST /api/v1/mcp/servers`；`POST /discover`；`POST /approve`；`POST /health-check`；`GET /api/v1/mcp/invocations` | API-MCP-001..008 |
| Skills | `GET/POST /api/v1/skills`；`POST /api/v1/skills/{id}/versions`；`POST /publish`；`POST /rollback` | API-SKILL-001..006 |
| 自动化 | `GET/POST /api/v1/automations`；`POST /publish`；`POST /pause`；`POST /trigger`；`GET /automations/{id}/runs`；`POST /api/v1/automation-runs/{id}/cancel`；`POST /takeover` | API-AUTO-001..008 |
| Runner | `GET/POST /api/v1/runners`；`GET /api/v1/runners/{id}`；`POST /heartbeat`；`POST /drain`；`POST /quarantine`；`POST /execute` | API-RUNNER-001..008 |
| Agent | `GET/POST /api/v1/agents`；`GET/POST /api/v1/agents/{id}/mcp-bindings`；`GET/POST /api/v1/agents/{id}/skill-bindings` | API-AGENT-001..006 |
| 审计/事件 | `GET /api/v1/audit`；`GET /api/v1/streams/workspaces/{workspace_id}` | API-AUDIT-001..003 |

`API-BASE-001` 的统一步骤：用 admin 建立合法资源并保存 ID；以合法 JSON 调用 operation；重复请求和修改请求体；分别去掉 Cookie、替换为 viewer、换 workspace；使用未知 ID、空字符串、负数、超长字符串、错误 enum、错误 Content-Type；检查状态码、problem type、响应 schema、审计、事件和持久化快照。对上传接口追加 0/1MB/超限、分片重复/乱序/缺片、hash 不匹配和断点续传。

为避免分组表中的缩写造成漏测，下面是从当前 OpenAPI 文件逐项展开的 112 个 operation。按出现顺序编号为 `API-OP-001` 至 `API-OP-112`；QA 应为每一项建立独立结果行（可复用 `API-BASE-001`，但不能用一个接口的结果代表同组其它接口）：

```text
POST /api/v1/auth/login
GET /api/v1/auth/me
POST /api/v1/auth/logout
GET /api/v1/users
POST /api/v1/users
PATCH /api/v1/users/{id}
GET /api/v1/directory
GET /api/v1/attachments
POST /api/v1/attachments
GET /api/v1/provider/diagnostics
GET /healthz
GET /metrics
GET /readyz
GET /api/v1/requirements
POST /api/v1/requirements
GET /api/v1/requirements/{id}
PATCH /api/v1/requirements/{id}
POST /api/v1/requirements/{id}/start
POST /api/v1/requirements/{id}/transition
POST /api/v1/requirements/{id}/gates
POST /api/v1/requirements/{id}/approve
POST /api/v1/requirements/{id}/confirm-assignees
POST /api/v1/requirements/{id}/begin-design
POST /api/v1/requirements/{id}/pause
POST /api/v1/requirements/{id}/resume
GET /api/v1/requirements/{id}/work-items
POST /api/v1/requirements/{id}/assignees
POST /api/v1/requirements/{id}/repositories
POST /api/v1/requirements/{id}/impact-reports
POST /api/v1/requirements/{id}/impact-reports/{version}/confirm
GET /api/v1/bugs/{id}
POST /api/v1/bugs/{id}/triage
POST /api/v1/bugs/{id}/verify
GET /api/v1/bugs
POST /api/v1/bugs
POST /api/v1/bugs/{id}/repair
POST /api/v1/artifacts/uploads
POST /api/v1/screenshots
PUT /api/v1/artifacts/uploads/{upload_id}/parts/{part_no}
POST /api/v1/artifacts/uploads/{upload_id}/complete
POST /api/v1/artifact-migrations
GET /api/v1/artifact-migrations/{id}
POST /api/v1/artifact-migrations/{id}/pause
POST /api/v1/artifact-migrations/{id}/resume
POST /api/v1/artifact-migrations/{id}/rollback
GET /api/v1/artifacts/{id}/versions/{version}/content
GET /api/v1/work-items/{id}/diff
POST /api/v1/work-items/{id}/diff
GET /api/v1/work-items
GET /api/v1/work-items/{id}
GET /api/v1/work-items/{id}/context
GET /api/v1/work-items/{id}/repair-attempts
POST /api/v1/work-items/{id}/run
GET /api/v1/runs/{id}
GET /api/v1/runs/{id}/events
POST /api/v1/runs/{id}/messages
POST /api/v1/runs/{id}/cancel
GET /api/v1/runs/{id}/usage
GET /api/v1/repositories
POST /api/v1/repositories
POST /api/v1/repositories/{id}/index
GET /api/v1/repositories/{id}
PATCH /api/v1/repositories/{id}
DELETE /api/v1/repositories/{id}
GET /api/v1/repository-graph
GET /api/v1/team-workspaces
POST /api/v1/team-workspaces
GET /api/v1/team-workspaces/{id}
GET /api/v1/developer-profiles
GET /api/v1/developer-profiles/{member_id}
POST /api/v1/developer-profiles/{member_id}
PATCH /api/v1/developer-profiles/{member_id}
POST /api/v1/approvals
POST /api/v1/approvals/{id}/decide
GET /api/v1/evidence
POST /api/v1/evidence
GET /api/v1/mcp
POST /api/v1/mcp
GET /api/v1/mcp/servers
POST /api/v1/mcp/servers
POST /api/v1/mcp/servers/{id}/discover
POST /api/v1/mcp/servers/{id}/approve
POST /api/v1/mcp/servers/{id}/health-check
GET /api/v1/mcp/invocations
GET /api/v1/skills
POST /api/v1/skills
POST /api/v1/skills/{id}/versions
POST /api/v1/skills/{id}/publish
POST /api/v1/skills/{id}/rollback
GET /api/v1/automations
POST /api/v1/automations
POST /api/v1/automations/{id}/publish
POST /api/v1/automations/{id}/pause
POST /api/v1/automations/{id}/trigger
GET /api/v1/automations/{id}/runs
POST /api/v1/automation-runs/{id}/cancel
POST /api/v1/automation-runs/{id}/takeover
GET /api/v1/runners
POST /api/v1/runners
GET /api/v1/runners/{id}
POST /api/v1/runners/{id}/heartbeat
POST /api/v1/runners/{id}/drain
POST /api/v1/runners/{id}/quarantine
POST /api/v1/runners/{id}/execute
GET /api/v1/agents
POST /api/v1/agents
GET /api/v1/agents/{id}/mcp-bindings
POST /api/v1/agents/{id}/mcp-bindings
GET /api/v1/agents/{id}/skill-bindings
POST /api/v1/agents/{id}/skill-bindings
GET /api/v1/audit
GET /api/v1/streams/workspaces/{workspace_id}
```

### 9.1 operation 级断言基线

以下不是笼统的“返回 2xx”，而是每个 operation 必须在自动化断言中的最低字段和状态。实际状态码以 OpenAPI 和源码为准；若实现返回成功但缺少这些字段，应判 FAIL。

| Operation 家族 | 成功断言 | 必测失败断言 |
|---|---|---|
| auth/me/logout | 身份、会话失效和安全 Cookie 属性；logout 后旧 Cookie 401 | 未认证、过期、禁用用户、重复 logout |
| users/directory/profiles | 只返回授权 workspace；角色、菜单、binding/profile 关系完整 | 非 admin 写入 403；最后 admin 保护；跨 workspace 404/403 |
| healthz/readyz/metrics/diagnostics | healthz 反映进程；readyz 反映 Provider/存储；metrics 为合法 Prometheus；diagnostics 不含 secret | Provider 不可达、配置错误、空/损坏状态；错误码和健康字段一致 |
| requirements CRUD | 201/200 body 含 id、version、status、workspace；list 过滤/limit/cursor 正确 | 必填缺失 422、非法 JSON 400、旧 version 409、未知 ID 404 |
| requirement actions | 状态迁移、gate result、evidence/bug、version、audit/event 完整 | 非法迁移 409；重复 action 幂等或明确冲突；未满足 gate 不得前进 |
| bugs CRUD/actions | fingerprint 去重；repair attempt、status、run/context 返回可关联 | 跨 workspace 关系 422/404；attempt 上限 409；Provider 失败不改变为成功 |
| attachments/screenshots/uploads | owner、size、hash、media type、artifact/version、delivery 结果可回读 | 0/超限、缺片/乱序、hash mismatch、路径穿越、错误 owner |
| artifact migrations/content | migration 状态/版本/进度可查询；内容 Range/HEAD 与 hash 一致 | pause/resume/rollback 非法状态；未完成或越权内容 404/409 |
| work-items/diff/context/repair-attempts | route source、agent binding、baseline/head、manifest/provenance、attempt 列表完整 | 跨 work item context、digest/version 不匹配、未知 ID |
| runs/events/messages/cancel/usage | run snapshot 与 event stream 最终一致；cursor/replay/usage 可对账 | 重复 message/cancel、过期 cursor、Provider 不支持 501、终态冲突 |
| repositories/graph/team-workspaces | canonical name、branch、index status、图节点/边、workspace 归属正确 | URL/字段错误、删除引用、不可达索引、跨 workspace |
| approvals/evidence | approval 状态、决策人、理由、evidence hash/来源可验证 | 非授权决策、重复/过期决定、缺证据 |
| mcp | schema digest、health、approve、invocation request/response/status/duration 可审计 | secret 明文、未批准调用、schema 变更、超时/上游错误 |
| skills | version immutable；publish/rollback 状态和审计；绑定可回读 | 重复/并发发布、未知版本、回滚到不存在版本 |
| automations/runs | trigger/nodes 校验；publish/pause/trigger/cancel/takeover 状态和 owner 正确 | 非法 JSON/环路、重复触发、takeover 竞争、取消后写入 |
| runners | scope、status、heartbeat、argv、workdir、env、timeout、exit code 可核验 | 非健康/越权 workdir、非法 env key、超时、命令失败 |
| agents/bindings | profile、provider binding、MCP/Skill binding 和 route revision 可反查 | 空/跨 workspace binding、Provider 无 agent ID、删除后悬挂 |
| audit/streams | sequence/hash chain、tenant/workspace filter、cursor 后续事件稳定 | 篡改链、gap、过期/非法 cursor、跨租户事件 |

### 9.2 契约与实际路由漂移

测试代码必须从 `internal/api/server.go` 的 dispatch 和 OpenAPI 同时生成路由清单，发现“代码能访问但 OpenAPI 没声明”或反过来的 operation 直接失败。当前源码已知需要单独处理的漂移如下：

| Case ID | 实际入口 | 处理要求 |
|---|---|---|
| API-GAP-001 | `POST /api/v1/mcp/servers/{id}`（Web UI 的 invoke 动作） | 当前服务器支持但 OpenAPI 未声明；先执行成功、secret、超时、未批准用例，再补契约或明确废弃，未决为 S1 |
| API-GAP-002 | `HEAD /api/v1/artifacts/{id}/versions/{version}/content` | 服务器支持 HEAD；验证与 GET 相同的权限、长度、hash 和 Range 语义，并把 operation 加入 OpenAPI |
| API-GAP-003 | `OPTIONS` 预检和根路径 `GET /` | 验证 CORS、缓存和 root capability 文档；未声明的行为不能成为客户端隐式依赖 |
| API-GAP-004 | 所有 dispatch 路径的错误 method | 对每个资源发送 GET/POST/PUT/PATCH/DELETE/HEAD 组合，期望 405 + Allow（或明确 404），不得误触发副作用 |
| API-GAP-005 | OpenAPI operation 的响应 schema/headers | 对 112 个 operation 校验 `X-Request-ID`、problem+json、幂等重放 header 和 body schema；代码与契约不一致即 FAIL |

## 10. 能力与异常/故障注入矩阵

| Case ID | 注入 | 预期 |
|---|---|---|
| FI-001 | Codex 不存在、无执行权限、错误版本、环境变量缺失 | 启动或 run 明确 BLOCKED/配置错误；不伪造成功事件，不产生半成品提交 |
| FI-002 | Codex 返回非零、标准错误超大、输出乱码、工具调用参数非法 | Run 失败可诊断；敏感内容脱敏；工具不执行越权命令 |
| FI-003 | Codex 超时、网络断开、进程 SIGTERM/SIGKILL、Runner 心跳丢失 | lease 释放/接管规则生效；状态最终为 failed/cancelled/recoverable；可安全 rerun |
| FI-004 | Provider 在远端成功但 ADRO 本地响应丢失/进程崩溃 | 重启查询远端/本地证据后只补记一次；禁止重复 commit、attachment 或收费 |
| FI-005 | JSON 状态快照截断、版本未知、字段损坏、磁盘只读/满 | 启动拒绝或进入明确恢复模式；保留原文件；错误含修复建议 |
| FI-006 | Artifact 写入中断、内容 hash 冲突、路径穿越、符号链接 | 原子可见性；拒绝路径穿越和 symlink escape；垃圾临时文件可清理 |
| FI-007 | MCP discover schema 改变、health 超时、invoke 重复/拒绝 | schema digest 变化需重新批准；超时有上限；调用审计和幂等正确 |
| FI-008 | Automation 触发风暴、节点循环、cancel/takeover 竞争 | 有速率/并发上限；禁止环路或明确失败；只有一个 takeover owner |
| FI-009 | Event bus 延迟、重复、乱序、cursor 过期 | 客户端能 replay/reconcile；gap 告警；最终投影一致 |
| FI-010 | 并发 100 个轻量只读请求、20 个写请求、同资源竞争 | 无 data race、死锁、goroutine 泄漏；延迟和错误率记录；S0/S1 保护优先 |

## 11. 真实 Provider/Codex 和端到端证据包

以下为发布前必须由真实环境执行的单一验收链路 `E2E-REAL-001`，不能用 mock 替代：

1. 登录 `admin-a`，创建/选择 `ws-a`、`repo-a`，上传一个文本附件和一张截图。
2. 创建需求并绑定仓库、附件、截图和四个 Agent；配置方案 -> 开发 -> 测试 -> 修复关系。
3. 启动真实 Provider Run；若验收目标是本地 Codex，则保存实际二进制路径、版本、PID、命令行、run/session/workdir、commit、检查命令和 provider 原始响应摘要。当前仓库没有本地 Codex adapter，不能把 Multica adapter 或 MockProvider 误称为 Codex。
4. 通过 StreamEvents 订阅并故意断线；用 cursor 重连，保存原始事件、重放结果和 ACK。
5. 让测试 Agent 产生一个可复现 Bug；调用 repair/rerun，确认同 session/context lineage、不同 attempt、无重复副作用。
6. 完成后读取 diff、usage、attachment、evidence、audit 和最终需求状态；重启 ADRO，再次读取同一链路。
7. 运行 `node scripts/multica-conformance.mjs`（或按 `docs/operations/release-runbook.md` 的命令组合执行），提供脚本要求的 `ADRO_CONFORMANCE_*` 和真实 Provider 凭据；报告必须包含所有链路 ID。当前脚本未覆盖的本地 Codex、浏览器/并发/故障场景仍需按 E2E-REAL-002..004 执行。任一环节缺真实证据则 `BLOCKED`，不能写 PASS。

并行验收 `E2E-REAL-002`：同一用户同时启动两条需求；`E2E-REAL-003`：两个用户在两个 workspace 同时启动；`E2E-REAL-004`：运行中杀掉 API/Runner 后恢复。每条都要验证 session、workdir、事件 cursor、lease、artifact、audit 的隔离和最终一致性。

## 12. 自动化分层与代码归属

| 层级 | 自动化内容 | 触发频率 |
|---|---|---|
| L0 静态/契约 | `gofmt`、`go vet`、`go build`、`node --check`、OpenAPI/Compose/Helm 解析、`make contracts`、依赖/SBOM 校验 | 每次 push/PR |
| L1 Go 单元 | domain 状态机、store 持久化/幂等、auth、events、artifact、MCP、runner、observer、provider 错误映射 | 每次 push/PR，带 `-race` |
| L2 API 集成 | `internal/api/*_test.go` 覆盖 API 矩阵、错误码、快照重启、事件重放 | 每次 push/PR |
| L3 浏览器 | `npm run test:e2e`、`npm run test:e2e:matrix`、visual trace；桌面 Chromium/Firefox/WebKit 和移动 viewport | 每次 push/PR |
| L4 真实 Provider/Codex | `node scripts/multica-conformance.mjs` 加 E2E-REAL-001..004；本地 Codex 需先有真实 adapter；只在 self-hosted runner/受控凭据环境 | 受保护分支 push、发布候选；失败阻断 |
| L5 压力/故障 | FI-001..010、并发和磁盘/进程故障注入；保留趋势报告 | nightly、发布候选 |

### 12.1 自动化测试实现要求

上一版文档虽然列出了 Case，但没有规定如何让它们在每次改代码时真的执行。以下是实现约束，缺一项就不能称为“自动化发布测试套件”：

1. `API-OP-001..112` 从 `openapi/openapi.yaml` 生成参数化测试清单；测试启动时对照 OpenAPI operation 数量，发现新增 operation 未登记应直接失败。
2. API 集成测试使用真实启动的 ADRO HTTP server 和真实 filesystem state（可使用临时目录），不绕过路由直接调用 store；每个测试结束清理租户、文件和进程。
3. Playwright 测试覆盖本文件所有 `SHELL-*`、`WB-*`、`REQ-UI-*`、`BUG-UI-*`、`WF-UI-*`、`REPO-UI-*`、`AGENT-UI-*`、`CAP-UI-*`、`OPS-UI-*`；每个按钮必须断言网络请求和数据变化，禁止只断言 locator 可见。
4. L1/L2 可以使用明确标记的 deterministic MockProvider 来验证状态机和错误映射；L4 `E2E-REAL-*`、真实 Runner 和 Codex 禁止 mock/stub，必须检查实际 Provider、二进制（若为 Codex）、进程 PID、workdir、commit 和 provider evidence。
5. 每个 test fixture 必须有 `tenant/workspace/user/repository/agent/requirement/work-item/run` 的创建和销毁钩子；测试失败时保留现场快照，成功时删除凭据和临时文件。
6. 事件断言使用独立 collector，按 `event_id + sequence + aggregate_id` 去重并保存原文；不能通过等待固定时间或只看最终 UI 文案判断异步成功。
7. 并发测试使用 barrier/latch 同时发起请求；至少重复 20 次并启用 Go race。随机数据必须由固定 seed 记录，重跑同 seed 能复现。
8. 故障注入通过可替换 clock、Provider/Runner transport、文件系统 fault hook 和进程控制实现；每个注入点必须验证恢复后的快照、事件、审计和副作用计数。
9. 禁止全局重试掩盖失败；网络重试只由被测客户端按契约执行，Playwright `retries` 在发布门禁中设为 0。失败必须输出 request-id、Case ID 和最小脱敏上下文。
10. 新增/修改代码的 PR 必须同时新增或更新 Case ID、fixture 和断言；删除 Case 必须有风险评审和发布负责人批准。

推荐的仓库落地结构（当前若不存在，应作为测试工程任务实施）：

```text
tests/
  api/            # 真实 HTTP server + API-OP 参数化/权限/幂等
  browser/        # Playwright 菜单、控件、组合和 accessibility
  orchestration/  # AGC 图、路由快照、并发、lease、重放
  fault/          # Provider/Runner/fs/进程故障注入
  real/           # 仅真实 Provider/Codex，输出不可伪造 evidence.json
  fixtures/       # tenant/user/repo/agent/requirement 生命周期
```

建议提供一个新的 `make test-expert`（名称可调整但必须有单一入口），顺序执行 `make verify`、全部 API/UI/编排/故障套件，并在 `var/test-report/` 生成 JUnit、JSON、截图、trace、event hash 和 coverage ledger。当前仓库没有这个入口时，不能在发布说明中声称“全量自动化已接入”。

### 12.2 覆盖率门槛

| 指标 | 门槛 | 计算方式 |
|---|---:|---|
| OpenAPI operation | 112/112 | 每个 `API-OP-*` 至少一次成功和一次失败 |
| 菜单 | 18/18 | 每个菜单的加载、空态、错误态、全部可见动作 |
| UI 控件 Case | 100% | 本文 `SHELL/WB/REQ-UI/BUG-UI/WF-UI/REPO-UI/AGENT-UI/CAP-UI/OPS-UI` |
| 状态迁移 | 100% | `domain.transitions` 中允许和拒绝边各一条证据 |
| Agent 拓扑 | 17/17 | AGC-001..017；不支持的拓扑必须 BLOCKED/S1 |
| Squad/小队 | 17/17 | SQUAD-001..017；当前缺少实体/入口时必须 BLOCKED/S1 |
| 故障注入 | 10/10 | FI-001..010，包含恢复后的数据一致性 |
| 并发隔离 | 4 类 | 同用户双需求、跨用户、同资源竞争、事件重放 |
| 真实链路 | 4/4 | E2E-REAL-001..004，真实 Provider/Codex evidence 完整；本地 Codex adapter 缺失时为 BLOCKED |
| CLI/部署 | 16/16 | CLI-001..006、START-001..006、DEPLOY-001..003、CONFORM-CLI-001 |
| S0/S1 缺陷 | 0 | 任一未关闭即阻断发布 |

### 12.3 企业发布所需的非功能用例

功能 Case 全部通过仍不足以证明企业可直接使用；以下用例必须有可量化的原始数据和趋势对比。阈值应由发布负责人依据部署规模配置，不能用“本机很快”代替。

| Case ID | 场景 | 通过标准 |
|---|---|---|
| NFR-SEC-001 | Requirement、Bug、MCP endpoint、clone URL 输入 SSRF、内网地址、命令注入、XSS、路径穿越 payload | 请求被校验/隔离；响应、日志、事件和 artifact 不执行或回显危险 payload |
| NFR-SEC-002 | 上传伪造 MIME、双扩展名、压缩炸弹、20 MiB+1、恶意 SVG、符号链接 | 大小/类型/内容策略一致；不覆盖已有文件；拒绝 symlink escape |
| NFR-SEC-003 | 抓取浏览器、API、Provider、Runner、audit、artifact 日志和错误响应 | Cookie、token、secret_ref、环境变量、prompt 中敏感值均脱敏；CSP/HttpOnly/SameSite/CSRF 符合策略 |
| NFR-SEC-004 | viewer/member/admin 的横向、纵向越权和租户枚举；并发禁用账号 | 任何越权 403/404 且无副作用；禁用即时生效；审计包含 actor/resource/result |
| NFR-PERF-001 | 只读列表 100/1,000/10,000 项，分页、过滤、stream replay | 记录 p50/p95/p99、错误率、CPU/内存；满足发布 profile 的预算，无 O(n²) 或无界响应 |
| NFR-PERF-002 | 同时 20 个写 Run、100 个读请求、10 个事件订阅、4 个 Agent 拓扑 | 无死锁、race、goroutine 泄漏、事件丢失；背压和拒绝策略可见 |
| NFR-REL-001 | API/Runner 在每个 checkpoint 前后重启，状态文件截断、只读、磁盘满 | 启动拒绝或安全恢复；不丢已确认事件/证据；原子快照和恢复报告可验证 |
| NFR-REL-002 | 从上一版本单机数据升级、重复迁移、中途回滚、降级读取 | migration 可重入；旧数据不静默改变语义；失败可回滚并保留备份 |
| NFR-REL-003 | 进程时钟跳变、时区/DST、网络断开超过 lease TTL、长时间 idle | sequence/version/lease 不倒退；超时和接管有明确状态；时间统一 UTC |
| NFR-OBS-001 | 每个请求、Run、Agent 节点、工具调用、上传、迁移产生日志/metric | 可用 request-id/trace-id 串起 API -> event -> provider -> runner -> artifact；错误含阶段和可操作原因 |
| NFR-OBS-002 | metrics 高基数输入、异常 label、长时间运行、日志轮转 | metrics 不泄漏 prompt/secret；label 有界；日志轮转不阻塞主流程；告警规则能触发 |
| NFR-SUPPLY-001 | 依赖升级、SBOM、license、可复现构建、容器/Helm 配置扫描 | `make contracts`、`node scripts/release-assets.mjs verify` 和安全扫描结果可追溯；未知依赖阻断 |
| NFR-SUPPLY-002 | 备份/恢复 artifact、state、audit；恢复后重新运行 conformance | hash、版本、权限和关联 ID 一致；恢复不会重复工具副作用 |

### 12.4 CLI、启动脚本和单机部署用例

这些入口是企业安装和运维的实际产品面，不能只测 HTTP/UI。

| Case ID | 步骤 | 通过标准 |
|---|---|---|
| CLI-001 | `adroctl` 无参数、`--help`、未知子命令、`version` | usage、退出码和版本稳定；未知命令不启动进程 |
| CLI-002 | `adroctl up --demo` 合法/非法 `--addr`、端口冲突、无 `adro-api` PATH | 仅 demo profile 可启动；错误退出且不遗留孤儿进程/敏感日志 |
| CLI-003 | `adroctl install --profile single-node --dry-run`，缺 profile、compose 文件不存在 | 只打印确定的 compose 命令和 artifact root；dry-run 不创建容器 |
| CLI-004 | `adroctl install` Docker 缺失、daemon 未运行、compose 失败、重复执行 | 错误可诊断；成功后 volume/服务可复核；重复执行幂等或明确更新 |
| CLI-005 | `adroctl health --url` 返回 200、503、连接拒绝、重定向 | 只把 2xx 判健康；超时/非 2xx 非零退出；不泄露响应 body 中 secret |
| CLI-006 | `adroctl config-check` 合法配置、未知 enum、缺必需路径、错误 Agent map | 与 API 启动校验一致；错误返回非零且指出字段，不静默降级 |
| START-001 | `./start.sh --help`、未知 flag、`--status`、`--stop`、重复 stop | 帮助和状态准确；未知参数不修改状态；stop 可重入且不杀非本实例 PID |
| START-002 | `--without-multica`、`--no-docker`、`--non-interactive`、`--no-open` 的组合 | 依赖检查、provider profile、浏览器行为和输出准确；缺 Go/curl/openssl/docker 早失败 |
| START-003 | 端口已占用、PID 文件过期、API/WebUI 只启动一半、启动超时 | 安全退出并保留诊断；清理本实例文件；不连接到别的服务冒充 ready |
| START-004 | 首次生成/再次读取 `ADRO_HOME/adro.env`，密码、PAT、Agent map 和路径特殊字符 | 目录 0700、env 0600；键更新不重复；日志/终端不打印 secret；重启值一致 |
| START-005 | Docker Compose profile 的 `ADRO_REPLICA_COUNT>1`、错误 backend、未知 env | 单机 profile 拒绝不安全副本/后端组合；readyz 与配置状态一致 |
| START-006 | `--no-docker` 远程 Provider 仅 URL、仅 token、两者都有但 workspace/runtime 歧义 | 缺项/歧义 fail-closed；成功时 workspace/runtime/project 绑定可审计 |
| DEPLOY-001 | Compose volume 删除/重建、容器重启、宿主机重启、artifact 权限改变 | 持久状态和权限符合设计；不可恢复时 readyz 阻断而非丢数据继续服务 |
| DEPLOY-002 | Helm values/schema 非法、资源限制为 0/负数、secret 未配置、探针失败 | `values.schema.json` 拒绝非法配置；探针反映真实 readiness；不把单机 chart 当 HA |
| DEPLOY-003 | 升级前备份 state/event/audit/artifact，升级中断后恢复，再运行 API/UI/real conformance | 备份可恢复、hash 一致、迁移可重入；升级失败可回滚且不重复副作用 |
| CONFORM-CLI-001 | `cmd/adro-conformance` 缺 URL/run/token、WebSocket 断开、事件字段缺失/重复 | 非零退出和脱敏错误；成功只在真实 event schema、cursor 和 hash 满足时返回 passed |

## 13. GitHub push/PR 发布门禁

当前源码仅有 `.github/workflows/ci.yml`，它在 `push` 和 `pull_request` 执行大部分 L0-L3 检查；发布前需要扩展为上传 Playwright report、JUnit/JSON、截图、trace 和 Go race 日志。任何测试失败都阻断合并；不得通过 `continue-on-error` 隐藏失败。

仓库当前没有真实 Provider/Codex 专用 workflow；下面的 workflow 是本测试规范要求补建的门禁，而不是已经存在的实现：

- 仅允许受信任 self-hosted runner，凭据使用 GitHub Actions secrets/environment，日志自动脱敏。
- 校验 `ADRO_PROVIDER=multica`、`ADRO_MULTICA_URL`、`ADRO_MULTICA_TOKEN` 和实际 Provider 能力；如果后续接入本地 Codex，再校验真实二进制路径/版本。检测到 mock、stub、空 evidence 或无 workdir/commit 时立即失败。
- 运行 E2E-REAL-001..004，上传 `report.json`、事件流、审计链、快照哈希、Provider/Runner 日志和失败现场；artifact 保留期按组织策略执行。
- `main` 分支 branch protection 要求 `ci`、`browser`、`real-e2e`（或等价 job）成功；并发 workflow 使用 cancel-in-progress 只取消同一 PR 的旧 run，不得取消已开始的发布候选证据。
- 每次 push 记录提交 SHA、测试数据版本、配置 profile 和结果摘要；PR 必须包含新增/修改 Case ID 及风险说明。

## 14. 发布签字清单

- [ ] 18 个菜单均完成 UI-001..020 适用项，桌面/移动/三浏览器证据齐全。
- [ ] API 矩阵每个 OpenAPI operation 均有正常、鉴权、权限、输入边界、资源不存在和幂等结果。
- [ ] FLOW-001..013 至少一次完整执行；同用户并发和多用户隔离有独立证据。
- [ ] CTX、EVT、FI 全部执行；所有 S0/S1 为 0，S2 有关闭或书面豁免。
- [ ] 真实 Provider/Codex E2E-REAL-001..004 PASS；当前没有本地 Codex adapter 时发布状态必须为 BLOCKED。
- [ ] 重启前后资源、事件、session/context lineage、audit、attachment 可复核且 hash 对得上。
- [ ] GitHub push/PR 所有 required checks 通过，报告和 trace 可下载，未使用 mock 结果冒充真实执行。
- [ ] QA、研发、发布负责人分别签名并记录测试提交 SHA；签字不等于跳过失败 Case。

## 15. 缺陷记录模板

```text
Case ID:
源码提交 SHA:
环境/配置 profile:
执行时间/执行人:
前置资源 ID（tenant/workspace/requirement/run/session/workdir）:
步骤与实际请求:
期望:
实际:
结果: PASS | FAIL | BLOCKED | NA
严重性: S0 | S1 | S2 | S3
证据: GitHub job / Playwright trace / event hash / snapshot hash / log
缺陷链接与回归 Case:
```

最终判定只能依据上述证据包和门禁结果；“任务完成”“代码已合并”或单元测试通过，均不能替代真实 Provider/Codex 全链路、并发、故障和浏览器组合测试。
