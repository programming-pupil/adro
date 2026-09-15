(() => {
  const menuIDs = [
    'workbench', 'delivery', 'humanQA', 'diffs', 'testing', 'chats',
    'repositories', 'agents', 'mcp', 'skills', 'automations',
    'integrations', 'artifacts', 'runners', 'cost', 'admin'
  ];

  Object.assign(translations.zh, {
    chats: '普通聊天', chatSubtitle: '把项目、文件与 Agent 放进同一段持续上下文', newChat: '新建会话', chatTitle: '会话标题', chatProject: '绑定项目', chatMessagePlaceholder: '描述你想解决的问题，或把上下文交给 Agent...', sendMessage: '发送', noChats: '还没有聊天会话', noMessages: '从一个问题开始', chatSendFailed: '消息发送失败', chatCreateFailed: '会话创建失败', chatAttachments: '添加附件', chatSearchPlaceholder: '搜索会话', chatContext: '上下文', chatNoProject: '未绑定项目', chatChooseProject: '选择项目', chatProjectReady: '项目上下文已接入', chatAgentReady: 'Agent 已就绪', chatNoAgent: '使用默认执行器', chatRecent: '最近会话', chatWorkspace: 'AI 项目工作区', chatWorkspaceHint: '选择项目，上传文件，然后开始一段有记忆的问答。', chatSuggested: '你可以先问', chatSuggestionOne: '总结这个项目当前的风险', chatSuggestionTwo: '根据附件给出实现建议', chatSuggestionThree: '帮我梳理下一步研发任务', chatDropHint: '拖入文件，或直接粘贴图片', chatFilesReady: '个文件已加入上下文', chatUploadHint: '图片可预览，文件会随消息发送', chatRemoveFile: '移除附件', chatPreviewFile: '预览附件', chatSending: '正在交给 Agent...', chatEmptyTitle: '让项目成为对话的一部分', chatEmptyBody: '绑定一个项目后，Agent 会在同一条上下文里理解仓库、附件和你的问题。', chatConversation: '对话', chatRuntimeState: '运行态', chatPersisted: '已持久化', chatProjectFiles: '项目与附件', chatNoFiles: '发送附件后会显示在这里', chatMessages: '条消息', chatStart: '开始对话', chatNewTitlePlaceholder: '例如：支付发布讨论', agentCreateKicker: 'CREATE / ASSISTANT', agentStepDescribe: '先说清楚它要帮你做什么', agentStepDescribeHelp: '不用写技术配置，直接描述目标、输入和你期待的结果。', agentReadyState: '可开始创建', agentCreatingState: '正在创建', agentCreatedState: '已创建', agentNeedsAttentionState: '需要处理', agentCreateRunning: '创建任务已开始，关闭窗口也会继续。', agentCreateDone: '助手已创建，可以在列表中继续启用或编辑。', agentCreateError: '创建没有完成', agentRetry: '重试', agentView: '查看', agentUploadAvatar: '上传头像', agentAvatarHelp: 'PNG、JPG 或 WebP，最大 5 MiB。', agentUseLocalExecution: '使用本地执行环境', agentNoProviderError: '当前执行环境不可用，请先确认本地执行程序已安装且可运行。',
    authSystemName: '智能研发交付控制系统', secureAccess: '安全访问 / 身份边界', loginTitle: '进入交付控制面',
    loginSubtitle: '使用你的 ADRO 工作空间账号登录。可见菜单、执行权限与审计身份均由管理员分配。',
    username: '用户名', password: '密码', signIn: '登录控制台', signOut: '退出登录',
    loginSecurity: '会话令牌仅由服务端安全保存，所有变更写入审计链', loginFailed: '用户名或密码错误，或账号已停用',
    roleAdmin: '管理员', roleMember: '成员', roleViewer: '只读成员', requirementRecord: '需求记录 / 可验证交付',
    bugRecord: '缺陷记录 / 修复闭环', requirementTitlePlaceholder: '一句话说明需要交付什么',
    requirementDescriptionPlaceholder: '补充背景、范围、约束以及非目标', acceptancePlaceholder: '每行一条可独立验证的验收标准',
    acceptanceHelpLines: '支持多条验收标准，每行一条', properties: '结构化属性', project: '项目 / 仓库', executor: '执行人',
    attachments: '附件', selectFiles: '选择或拖入文件', attachmentHelp: '支持多文件，单文件最大 20 MiB',
    createRequirement: '创建并上传附件', noProjects: '请先在“项目与仓库”登记项目', noExecutors: '没有可用执行人',
    bugCreateSubtitle: '记录可复现、可关联、可自动修复的真实缺陷', bugTitlePlaceholder: '简明说明故障现象',
    bugStepsPlaceholder: '逐步写明如何稳定复现', relatedRequirement: '关联需求', noRelatedRequirement: '不关联需求',
    uploadFailed: '实体已创建，但部分附件上传失败', accessControl: '身份与访问控制', userDialogSubtitle: '账号角色决定基础权限，菜单授权决定实际产品入口',
    createUser: '创建用户', editUser: '编辑用户', saveUser: '保存用户', displayName: '显示名称', role: '角色',
    accountStatus: '账号状态', activeAccount: '启用', disabledAccount: '停用', passwordHelp: '新账号至少 10 位；编辑时留空则不修改密码',
    menuPermissions: '菜单权限', menuPermissionsHelp: '管理员拥有全部菜单；其他角色可按人精确分配。',
    userManagement: '用户与菜单权限', identityCount: '个身份', permissionSummary: '菜单', edit: '编辑', userSaveFailed: '用户保存失败，请检查用户名、密码和管理员约束',
    requirementRelation: '关联需求', executorColumn: '执行人', fileCount: '个附件', authLoading: '正在验证会话',
    runnerWorkspaceRoot: '工作区根目录', executeRunner: '执行命令', runnerCommand: '命令', runnerCommandPlaceholder: '例如 go test ./...', runnerWorkDir: '工作目录', runnerWorkDirPlaceholder: '留空使用 Runner 根目录', runnerEnv: '环境变量', runnerAddEnv: '添加变量', runnerEnvName: '变量名', runnerEnvValue: '变量值', runnerRemoveEnv: '移除变量', runnerTimeout: '超时（毫秒）', runnerExecuteFailed: 'Runner 执行失败，请检查命令、路径和权限', automationEvent: '触发条件', automationEventRequirement: '需求发生变化', automationEventFailure: '执行失败', automationEventComment: '收到新评论', automationAction: '执行动作', automationActionNotify: '通知相关人员', automationActionAgent: '调用 Agent', automationActionRepair: '进入修复流程',
    workspaceMigration: '工作区迁移', exportWorkspace: '导出工作区', chooseBundle: '选择迁移包', preflightBundle: '预检', importWorkspace: '导入工作区', migrationReady: '预检通过，可以导入', migrationFailed: '迁移失败，请检查文件和冲突策略', migrationDone: '工作区导入完成', conflictMode: '冲突策略', conflictRename: '重映射 ID', conflictSkip: '跳过冲突', conflictFail: '发现冲突即停止', migrationEmpty: '尚未选择迁移包', migrationEntities: '项实体', migrateExisting: '迁移已有工作区',
    agentEditTitle: '编辑 Agent', agentSave: '保存 Agent', agentAvatarLabel: '头像 URL', agentSkillsLabel: '可用 Skills', agentMCPServersLabel: 'MCP 服务', agentResourceCatalogHelp: '这里是已登记在 ADRO 工作区的共享资源；运行时 Skills 是本机执行器发现的安装项，二者相互独立。', agentRuntimeSkillsHelp: '运行时 Skills 来自当前本地执行器的安装目录，不等同于工作区共享 Skills。', agentRuntimeConfigLabel: '运行时配置', agentRuntimeConfigPlaceholder: '每行 key=value，例如 sandbox_mode=workspace-write', agentEnvironmentLabel: '密钥环境变量', agentEnvironmentPlaceholder: '每行 NAME=env:SECRET_NAME，不填写明文密钥', agentNoResources: '当前没有可选择的资源'
    ,nativeAgents: '版本化 Agent', nativeSquads: '已定义小队', executionPlans: '执行计划', newSquad: '新建小队', newPlan: '新建计划', validate: '校验', dryRun: 'Dry run', publish: '发布', enable: '启用', disable: '停用', archive: '归档', timeline: '时间线', replay: '重放', revision: '修订', graphNodes: '图节点', selectedTarget: '执行目标', squadName: '小队名称', squadDescription: '职责说明', squadLeader: 'Leader Agent', squadCreateFailed: '小队创建失败', planRequirement: '需求', planTarget: 'Agent / 小队', planCreateFailed: '执行计划创建失败', orchestrationReady: '原生自由编排控制面', orchestrationHelp: 'Agent 与 Squad 使用冻结 revision；发布计划后可从 timeline 重放每个 attempt、edge 与 evidence。', legacyBindings: '兼容责任人绑定', nativeAgentHelp: '此表直接读取 revisioned AgentDefinition，不再以显示名或旧 developer profile 作为编排主键。', lifecycleActionFailed: '生命周期操作失败', noPublishedTarget: '请先启用 Agent 或发布 Squad', planHash: 'Plan hash', openTimeline: '查看不可变事件时间线', closeTimeline: '关闭时间线', editGraph: '编辑图', forkSquad: '复制模板', graphEditor: 'Workflow Graph 编辑器', graphJSON: 'Graph JSON', graphJSONHelp: '导入/导出同一份 WorkflowGraph；发布前必须校验。', formatGraph: '格式化', validateGraph: '校验图', saveGraph: '保存图', graphSaved: '图已保存', graphValidationFailed: '图校验失败', graphNodeHint: '节点与边可任意增删；条件、回退、重试和汇聚保存在 JSON 契约中。', graphCanvas: '可视化画布', addAgentNode: 'Agent 节点', addGateNode: 'Gate 节点', connectNodes: '连接节点', removeNode: '移除节点', nodeKind: '节点类型', noOutgoingEdges: '暂无出边', comments: '评论', commentPlaceholder: '输入评论，使用 @ 选择 Agent 或 Squad', preview: '预览触发', sendComment: '发布评论', commentSent: '评论已发布', commentPreviewFailed: '触发预览失败', noComments: '暂无评论', triggerOutcomes: '触发结果', invokeAgent: '调用 Agent', invokeSquad: '调用 Squad'
  });
  Object.assign(translations.zh, { agentAvatarLabel: '头像', agentCreateKicker: '创建 / 助手', agentStepDescribe: '先说清楚它要帮你做什么', agentStepDescribeHelp: '直接描述目标、输入和期待结果，技术配置可以稍后再调整。', agentReadyState: '可开始创建', agentCreatingState: '正在创建', agentCreatedState: '已创建', agentNeedsAttentionState: '需要处理', agentCreateRunning: '创建任务已开始，关闭窗口也会继续。', agentCreateDone: '助手已创建，可以在列表中继续操作。', agentCreateError: '创建没有完成', agentRetry: '重试', agentView: '查看', agentUploadAvatar: '上传头像', agentAvatarHelp: '支持 PNG、JPG、WebP，最大 5 MiB。', agentUseLocalExecution: '使用本地执行环境', agentNoProviderError: '当前执行环境不可用，请确认本地运行程序已安装并可运行。', agentActive: '可使用', agentDisabled: '已暂停', agentArchived: '已归档', agentDraft: '待启用', agentCreateJobs: '创建任务', agentCreateJobsHelp: '关闭窗口不会取消任务，完成后状态会保留在这里。' });
  Object.assign(translations.en, {
    agentAvatarLabel: 'Avatar',
    agentActive: 'Ready', agentDisabled: 'Paused', agentArchived: 'Archived', agentDraft: 'Pending', agentCreateJobs: 'Creation tasks', agentCreateJobsHelp: 'Closing the window does not cancel a task. Its result stays here.',
    agentCreateKicker: 'CREATE / ASSISTANT', agentStepDescribe: 'Start with what you want it to do', agentStepDescribeHelp: 'Describe the goal, inputs, and expected result. Technical setup stays optional.', agentReadyState: 'Ready to create', agentCreatingState: 'Creating', agentCreatedState: 'Created', agentNeedsAttentionState: 'Needs attention', agentCreateRunning: 'Creation has started and continues after you close this window.', agentCreateDone: 'Assistant created. You can continue from the list.', agentCreateError: 'Creation did not finish', agentRetry: 'Retry', agentView: 'View', agentUploadAvatar: 'Upload avatar', agentAvatarHelp: 'PNG, JPG, or WebP, up to 5 MiB.', agentUseLocalExecution: 'Use the local execution environment', agentNoProviderError: 'The selected execution environment is unavailable. Check that the local runtime is installed and ready.',
    chats: 'Chat', chatSubtitle: 'A persistent workspace for project context, files, and Agent Q&A', newChat: 'New conversation', chatTitle: 'Conversation title', chatProject: 'Project binding', chatMessagePlaceholder: 'Describe the problem, or hand the context to your Agent...', sendMessage: 'Send', noChats: 'No conversations yet', noMessages: 'Start with a question', chatSendFailed: 'Could not send the message', chatCreateFailed: 'Could not create the conversation', chatAttachments: 'Add attachments', chatSearchPlaceholder: 'Search conversations', chatContext: 'Context', chatProjectContext: 'Project context', chatNoProject: 'No project bound', chatChooseProject: 'Choose a project', chatProjectReady: 'Project context connected', chatAgentReady: 'Agent ready', chatNoAgent: 'Using the default executor', chatRecent: 'Recent conversations', chatWorkspace: 'AI project workspace', chatWorkspaceHint: 'Choose a project, add files, and start a conversation with memory.', chatSuggested: 'Try asking', chatSuggestionOne: 'Summarize the current project risks', chatSuggestionTwo: 'Suggest an implementation from these files', chatSuggestionThree: 'Map the next engineering tasks', chatDropHint: 'Drop files here, or paste an image', chatFilesReady: 'files added to context', chatUploadHint: 'Images can be previewed; files travel with the message', chatRemoveFile: 'Remove attachment', chatPreviewFile: 'Preview attachment', chatSending: 'Handing off to Agent...', chatEmptyTitle: 'Make the project part of the conversation', chatEmptyBody: 'Bind a project and the Agent will reason over the repository, attachments, and your question in one context.', chatConversation: 'Conversation', chatRuntimeState: 'Runtime', chatPersisted: 'Persisted', chatProjectFiles: 'Project and files', chatNoFiles: 'Attachments will appear here after you send them', chatMessages: 'messages', chatStart: 'Start conversation', chatNewTitlePlaceholder: 'For example: Payment release discussion',
    authSystemName: 'Agentic delivery control system', secureAccess: 'Secure access / identity boundary', loginTitle: 'Enter the delivery control plane',
    loginSubtitle: 'Sign in with your ADRO workspace account. Menu visibility, execution access, and audit identity are assigned by an administrator.',
    username: 'Username', password: 'Password', signIn: 'Sign in to console', signOut: 'Sign out',
    loginSecurity: 'Sessions stay server-side and every mutation is recorded in the audit chain', loginFailed: 'Incorrect credentials or a disabled account',
    roleAdmin: 'Administrator', roleMember: 'Member', roleViewer: 'Read-only member', requirementRecord: 'Requirement / verifiable delivery',
    bugRecord: 'Defect / repair loop', requirementTitlePlaceholder: 'State the delivery outcome in one sentence',
    requirementDescriptionPlaceholder: 'Add context, scope, constraints, and non-goals', acceptancePlaceholder: 'Enter one independently verifiable criterion per line',
    acceptanceHelpLines: 'Multiple criteria supported, one per line', properties: 'Structured properties', project: 'Project / repository', executor: 'Executor',
    attachments: 'Attachments', selectFiles: 'Choose or drop files', attachmentHelp: 'Multiple files supported, 20 MiB per file',
    createRequirement: 'Create and upload files', noProjects: 'Register a project under Projects & repositories first', noExecutors: 'No active executor available',
    bugCreateSubtitle: 'Record a reproducible, related defect that can enter the repair loop', bugTitlePlaceholder: 'Summarize the failure',
    bugStepsPlaceholder: 'List the exact steps that reproduce it', relatedRequirement: 'Related requirement', noRelatedRequirement: 'No related requirement',
    uploadFailed: 'The record was created, but one or more attachments failed', accessControl: 'Identity and access control', userDialogSubtitle: 'Roles provide the baseline; per-user menu access controls the actual product surface',
    createUser: 'Create user', editUser: 'Edit user', saveUser: 'Save user', displayName: 'Display name', role: 'Role',
    accountStatus: 'Account status', activeAccount: 'Active', disabledAccount: 'Disabled', passwordHelp: 'At least 10 characters for new users; leave blank when editing to keep the password',
    menuPermissions: 'Menu access', menuPermissionsHelp: 'Administrators receive every menu; other roles can be assigned per user.',
    userManagement: 'Users and menu access', identityCount: 'identities', permissionSummary: 'menus', edit: 'Edit', userSaveFailed: 'Could not save the user; check the username, password, and administrator constraints',
    requirementRelation: 'Requirement', executorColumn: 'Executor', fileCount: 'attachments', authLoading: 'Validating session',
    runnerWorkspaceRoot: 'Workspace root', executeRunner: 'Execute command', runnerCommand: 'Command', runnerCommandPlaceholder: 'For example: go test ./...', runnerWorkDir: 'Working directory', runnerWorkDirPlaceholder: 'Leave blank to use the runner root', runnerEnv: 'Environment variables', runnerAddEnv: 'Add variable', runnerEnvName: 'Variable name', runnerEnvValue: 'Variable value', runnerRemoveEnv: 'Remove variable', runnerTimeout: 'Timeout (ms)', runnerExecuteFailed: 'Runner execution failed; check the command, path, and permissions', automationEvent: 'Trigger condition', automationEventRequirement: 'Requirement changes', automationEventFailure: 'Execution fails', automationEventComment: 'A new comment arrives', automationAction: 'Action', automationActionNotify: 'Notify people', automationActionAgent: 'Invoke an agent', automationActionRepair: 'Start repair',
    workspaceMigration: 'Workspace migration', exportWorkspace: 'Export workspace', chooseBundle: 'Choose bundle', preflightBundle: 'Preflight', importWorkspace: 'Import workspace', migrationReady: 'Preflight passed; ready to import', migrationFailed: 'Migration failed; check the bundle and conflict policy', migrationDone: 'Workspace import completed', conflictMode: 'Conflict policy', conflictRename: 'Remap IDs', conflictSkip: 'Skip conflicts', conflictFail: 'Stop on conflict', migrationEmpty: 'No migration bundle selected', migrationEntities: 'entities', migrateExisting: 'Migrate an existing workspace',
    agentEditTitle: 'Edit agent', agentSave: 'Save agent', agentAvatarLabel: 'Avatar URL', agentSkillsLabel: 'Available skills', agentMCPServersLabel: 'MCP servers', agentResourceCatalogHelp: 'These are shared resources registered in the ADRO workspace. Runtime skills are installed items discovered by the local executor; the two catalogs are independent.', agentRuntimeSkillsHelp: 'Runtime skills come from the selected local executor installation and are not the same as shared workspace skills.', agentRuntimeConfigLabel: 'Runtime configuration', agentRuntimeConfigPlaceholder: 'One key=value per line, for example sandbox_mode=workspace-write', agentEnvironmentLabel: 'Secret-backed environment', agentEnvironmentPlaceholder: 'One NAME=env:SECRET_NAME per line; never enter plaintext secrets', agentNoResources: 'No selectable resources yet'
    ,nativeAgents: 'Revisioned agents', nativeSquads: 'Squad definitions', executionPlans: 'Execution plans', newSquad: 'New squad', newPlan: 'New plan', validate: 'Validate', dryRun: 'Dry run', publish: 'Publish', enable: 'Enable', disable: 'Disable', archive: 'Archive', timeline: 'Timeline', replay: 'Replay', revision: 'Revision', graphNodes: 'Graph nodes', selectedTarget: 'Execution target', squadName: 'Squad name', squadDescription: 'Responsibility', squadLeader: 'Leader agent', squadCreateFailed: 'Could not create squad', planRequirement: 'Requirement', planTarget: 'Agent / squad', planCreateFailed: 'Could not create execution plan', orchestrationReady: 'Native free-form orchestration', orchestrationHelp: 'Agents and squads pin immutable revisions; a published plan can replay every attempt, edge, and evidence receipt from its timeline.', legacyBindings: 'Compatibility member bindings', nativeAgentHelp: 'This table reads revisioned AgentDefinition records directly; display names and legacy developer profiles are not orchestration identities.', lifecycleActionFailed: 'Lifecycle action failed', noPublishedTarget: 'Enable an agent or publish a squad first', planHash: 'Plan hash', openTimeline: 'Open immutable event timeline', closeTimeline: 'Close timeline', editGraph: 'Edit graph', forkSquad: 'Copy template', graphEditor: 'Workflow Graph editor', graphJSON: 'Graph JSON', graphJSONHelp: 'Import or export the same WorkflowGraph contract; validate before publishing.', formatGraph: 'Format', validateGraph: 'Validate graph', saveGraph: 'Save graph', graphSaved: 'Graph saved', graphValidationFailed: 'Graph validation failed', graphNodeHint: 'Nodes and edges are free-form; predicates, feedback, retries, and joins stay in the JSON contract.', graphCanvas: 'Visual canvas', addAgentNode: 'Agent node', addGateNode: 'Gate node', connectNodes: 'Connect nodes', removeNode: 'Remove node', nodeKind: 'Node type', noOutgoingEdges: 'No outgoing edges', comments: 'Comments', commentPlaceholder: 'Write a comment; use @ to choose an Agent or Squad', preview: 'Preview triggers', sendComment: 'Post comment', commentSent: 'Comment posted', commentPreviewFailed: 'Could not preview triggers', noComments: 'No comments yet', triggerOutcomes: 'Trigger outcomes', invokeAgent: 'Invoke agent', invokeSquad: 'Invoke squad'
  });
  Object.assign(translations.zh, {
    autoGeneratePlan: '自动生成方案',
    autoGeneratePlanHelp: '创建需求后立即用选定 Agent 生成冻结方案。',
    planAgent: '方案生成 Agent',
    planAgentRequired: '开启自动生成方案后必须选择 Agent。',
    planCreated: '需求已创建，方案已生成',
    planCreateAfterRequirementFailed: '需求已创建，但方案生成失败，请在执行页重试。',
    executionCockpit: '执行舱',
    executionCockpitSubtitle: '按 Squad 图实时呈现节点、事件与本地执行器输出',
    runGraph: '运行图',
    runConsole: '实时事件终端',
    runEvidence: '证据与状态',
    runSelect: '选择一个执行计划查看运行态',
    runNoPlans: '还没有执行计划',
    runNoEvents: '等待真实执行事件',
    runNodes: '图节点',
    runEvents: '事件',
    runRevision: '冻结版本',
    runLive: '实时',
    runRefresh: '刷新时间线',
    runSelectedBy: '执行目标',
    runPlanHash: '计划摘要',
    runStatus: '运行状态',
    runNoStagePipeline: '执行由 Squad 图决定，不使用固定阶段。'
  });
  Object.assign(translations.en, {
    autoGeneratePlan: 'Generate plan automatically',
    autoGeneratePlanHelp: 'Create the requirement and immediately freeze a plan with the selected Agent.',
    planAgent: 'Plan-generation Agent',
    planAgentRequired: 'Choose an Agent when automatic plan generation is enabled.',
    planCreated: 'Requirement created and plan generated',
    planCreateAfterRequirementFailed: 'Requirement created, but plan generation failed. Retry from Engineering runs.',
    executionCockpit: 'Execution cockpit',
    executionCockpitSubtitle: 'Live graph nodes, events, and local executor output from the selected Squad graph',
    runGraph: 'Run graph',
    runConsole: 'Live event terminal',
    runEvidence: 'Evidence and state',
    runSelect: 'Select an execution plan to inspect its live state',
    runNoPlans: 'No execution plans yet',
    runNoEvents: 'Waiting for real execution events',
    runNodes: 'Graph nodes',
    runEvents: 'Events',
    runRevision: 'Frozen revision',
    runLive: 'LIVE',
    runRefresh: 'Refresh timeline',
    runSelectedBy: 'Selected target',
    runPlanHash: 'Plan digest',
    runStatus: 'Run status',
    runNoStagePipeline: 'Execution follows the Squad graph. There is no fixed stage pipeline.'
  });
  Object.assign(translations.zh, {
    agentRuntimeControls: '运行时策略',
    agentCodexSandbox: 'Codex 沙箱',
    agentCodexApproval: 'Codex 审批策略',
    agentSandboxReadOnly: '只读',
    agentSandboxWorkspace: '可写工作区',
    agentSandboxUnrestricted: '不限制',
    agentApprovalUntrusted: '仅可信命令免审批',
    agentApprovalOnRequest: '按需审批',
    agentApprovalOnFailure: '失败后审批',
    agentApprovalNever: '从不审批',
    agentOpenClawMode: 'OpenClaw 模式',
    agentOpenClawLocal: '本地',
    agentOpenClawGateway: '网关',
    agentGatewayHost: '网关地址',
    agentGatewayPort: '网关端口',
    agentGatewayTLS: '使用 TLS',
    agentGatewayAuthEnv: '运行时令牌变量',
    agentGatewaySecretEnv: '本机密钥变量',
    agentRuntimeSkills: '本地运行时 Skills',
    agentRuntimeSkillsLoading: '正在读取本地 Skills',
    agentRuntimeSkillsEmpty: '此运行时没有发现本地 Skill',
    agentRuntimeSkillsUnavailable: '无法读取此运行时的本地 Skills',
    agentRuntimeSkillLocked: '由运行时管理，始终启用'
  });
  Object.assign(translations.en, {
    agentRuntimeControls: 'Runtime policy',
    agentCodexSandbox: 'Codex sandbox',
    agentCodexApproval: 'Codex approval policy',
    agentSandboxReadOnly: 'Read only',
    agentSandboxWorkspace: 'Workspace write',
    agentSandboxUnrestricted: 'Unrestricted',
    agentApprovalUntrusted: 'Approve untrusted commands',
    agentApprovalOnRequest: 'On request',
    agentApprovalOnFailure: 'After failure',
    agentApprovalNever: 'Never approve',
    agentOpenClawMode: 'OpenClaw mode',
    agentOpenClawLocal: 'Local',
    agentOpenClawGateway: 'Gateway',
    agentGatewayHost: 'Gateway host',
    agentGatewayPort: 'Gateway port',
    agentGatewayTLS: 'Use TLS',
    agentGatewayAuthEnv: 'Runtime token variable',
    agentGatewaySecretEnv: 'Host secret variable',
    agentRuntimeSkills: 'Local runtime skills',
    agentRuntimeSkillsLoading: 'Reading local skills',
    agentRuntimeSkillsEmpty: 'No local skills were found for this runtime',
    agentRuntimeSkillsUnavailable: 'Local skills are unavailable for this runtime',
    agentRuntimeSkillLocked: 'Managed by the runtime and always enabled'
  });
  Object.assign(translations.zh, {
    agentMemberLabel: '负责人',
    agentAccessMembersLabel: '可使用成员',
    agentBuilderCreate: '生成并创建',
    agentExistingOwner: '现有负责人',
    agentNoMembers: '当前没有可选择的成员'
  });
  Object.assign(translations.en, {
    agentMemberLabel: 'Owner',
    agentAccessMembersLabel: 'Allowed members',
    agentBuilderCreate: 'Generate and create',
    agentExistingOwner: 'Existing owner',
    agentNoMembers: 'No members are available'
  });
  Object.assign(translations.en, {
    requirementOrchestration: 'Orchestration', requirementTarget: 'Execution target', noExecutionPlan: 'Create without an execution plan', temporarySquad: 'Temporary squad', temporaryMembers: 'Temporary squad members', temporaryMembersHelp: 'Choose active revisioned Agents; the graph can be edited after creation.', openGraphStudio: 'Open Graph Studio after creation', requirementOrchestrationHelp: 'An Agent or published Squad creates a frozen plan. A temporary squad persists an editable draft and uses its graph for the initial plan.'
  });
  Object.assign(translations.zh, {
    requirementOrchestration: '编排设置', requirementTarget: '执行目标', noExecutionPlan: '仅创建需求，不立即编排', temporarySquad: '临时小队', temporaryMembers: '临时小队成员', temporaryMembersHelp: '选择已启用的版本化 Agent；创建后可继续在 Graph Studio 编辑图。', openGraphStudio: '创建后打开 Graph Studio', requirementOrchestrationHelp: '选择 Agent 或已发布 Squad 会创建冻结计划；临时小队会保留可编辑草稿，并用其图创建初始计划。'
  });
  Object.assign(translations.zh, { reply: '回复', cancelReply: '取消回复', retryTrigger: '重试触发', attachComment: '添加附件', attachmentReady: '附件已准备', commentReplyingTo: '正在回复', commentEmpty: '评论内容不能为空', commentSendFailed: '评论发布失败', commentLoadFailed: '评论加载失败', commentPreview: '触发预览', commentPreviewReady: '预览已更新', commentPreviewNoTargets: '没有可触发的结构化 mention', mentionAgent: 'Agent', mentionSquad: 'Squad', commentOutcome: '触发结果', commentFollowUp: '执行收据', commentNoOutcome: '暂无触发结果', outcomeQueued: '已排队', outcomeCoalesced: '已合并', outcomeDeferred: '已延迟', outcomeBlocked: '已阻止', outcomeBroadcast: '仅广播', outcomeStarted: '已启动', outcomeRunning: '运行中', outcomeCompleted: '已完成', outcomeFailed: '失败', outcomeRetrying: '重试中', outcomeCancelled: '已取消', outcomeTimedOut: '已超时', outcomeNotRequested: '未请求', addSquadNode: 'Squad 节点', addMergeNode: 'Merge 节点', addRepairNode: 'Repair 节点', addHumanNode: 'Human 节点', nodeReference: '版本化引用', nodeBudget: 'Token 预算', nodeTimeout: '超时 (ms)', nodeRetry: '最大尝试', edgeEditor: '边配置', edgeEvent: '事件', edgePriority: '优先级', edgeLoop: '循环组', edgeMaxTraversals: '最大遍历', edgePredicate: 'Predicate', edgePredicateField: '字段', edgePredicateOp: '操作', edgePredicateValue: '值', edgeFanOut: '并行分发', planGraph: '计划图', planGraphHelp: '可在提交前用画布编辑 Agent 或 Squad 图。', planGraphLoad: '载入图', planGraphValidate: '校验并预览', planGraphStatus: '提交前检查', planGraphReady: '计划图已通过检查', planGraphInvalid: '计划图校验失败', graphDiagnostics: '节点/边/循环/并发', squadMembers: '成员 Agent', squadMemberHelp: '可选择多个 Agent；Leader 负责路由，其余成员按图中的边执行。', squadMemberRole: '成员角色' });
  Object.assign(translations.en, { reply: 'Reply', cancelReply: 'Cancel reply', retryTrigger: 'Retry trigger', attachComment: 'Attach files', attachmentReady: 'Files attached', commentReplyingTo: 'Replying to', commentEmpty: 'Comment cannot be empty', commentSendFailed: 'Could not post the comment', commentLoadFailed: 'Could not load comments', commentPreview: 'Preview triggers', commentPreviewReady: 'Preview updated', commentPreviewNoTargets: 'No structured mentions to invoke', mentionAgent: 'Agent', mentionSquad: 'Squad', commentOutcome: 'Trigger outcome', commentFollowUp: 'Execution receipt', commentNoOutcome: 'No trigger outcome', outcomeQueued: 'Queued', outcomeCoalesced: 'Coalesced', outcomeDeferred: 'Deferred', outcomeBlocked: 'Blocked', outcomeBroadcast: 'Broadcast only', outcomeStarted: 'Started', outcomeRunning: 'Running', outcomeCompleted: 'Completed', outcomeFailed: 'Failed', outcomeRetrying: 'Retrying', outcomeCancelled: 'Cancelled', outcomeTimedOut: 'Timed out', outcomeNotRequested: 'Not requested', addSquadNode: 'Squad node', addMergeNode: 'Merge node', addRepairNode: 'Repair node', addHumanNode: 'Human node', nodeReference: 'Versioned reference', nodeBudget: 'Token budget', nodeTimeout: 'Timeout (ms)', nodeRetry: 'Max attempts', edgeEditor: 'Edge configuration', edgeEvent: 'Event', edgePriority: 'Priority', edgeLoop: 'Loop group', edgeMaxTraversals: 'Max traversals', edgePredicate: 'Predicate', edgePredicateField: 'Field', edgePredicateOp: 'Operator', edgePredicateValue: 'Value', edgeFanOut: 'Fan out', planGraph: 'Plan graph', planGraphHelp: 'Edit the selected Agent or Squad graph with canvas controls before submitting.', planGraphLoad: 'Load graph', planGraphValidate: 'Validate and preview', planGraphStatus: 'Pre-submit checks', planGraphReady: 'Plan graph passed checks', planGraphInvalid: 'Plan graph validation failed', graphDiagnostics: 'nodes / edges / loops / concurrency', squadMembers: 'Member agents', squadMemberHelp: 'Select multiple agents; the leader routes work and other members execute graph edges.', squadMemberRole: 'Member role' });
  Object.assign(translations.zh, { joinPolicy: '汇聚策略', joinQuorum: '汇聚数量', joinFailurePolicy: '失败策略', predicateKind: '条件类型', predicateChild: '条件分支', predicateAddChild: '添加条件', predicateRemoveChild: '删除条件', predicateNoChildren: '暂无条件分支', requiredEvidence: '必需证据', failureCode: '失败代码', mergePolicy: '汇聚配置', conflictPolicy: '冲突策略', keyFields: '键字段', requireEvidence: '必须有证据', repairPolicy: '修复配置', repairTarget: '修复目标', verificationNodes: '验证节点', repairRounds: '最大轮次', repairBudget: '修复预算', repairScope: '修复范围', selectNode: '选择节点' });
  Object.assign(translations.en, { joinPolicy: 'Join policy', joinQuorum: 'Join quorum', joinFailurePolicy: 'Join failure policy', predicateKind: 'Predicate type', predicateChild: 'Predicate branch', predicateAddChild: 'Add condition', predicateRemoveChild: 'Remove condition', predicateNoChildren: 'No predicate branches', requiredEvidence: 'Required evidence', failureCode: 'Failure code', mergePolicy: 'Merge configuration', conflictPolicy: 'Conflict policy', keyFields: 'Key fields', requireEvidence: 'Require evidence', repairPolicy: 'Repair configuration', repairTarget: 'Repair target', verificationNodes: 'Verification nodes', repairRounds: 'Maximum rounds', repairBudget: 'Repair budget', repairScope: 'Repair scope', selectNode: 'Select node' });

  Object.assign(translations.zh, {
    logoutConfirm: '确定退出当前账号吗？',
    logoutConfirmTitle: '退出当前账号',
    logoutConfirmMessage: '退出后需要重新登录才能继续使用当前工作区。',
    logoutCancel: '取消',
    logoutConfirmAction: '确认退出',
    requirementDescriptionOnly: '需求描述',
    requirementDescriptionPlaceholder: '直接描述你希望交付的结果、背景、约束和验收重点...',
    requirementDescriptionHelp: '支持粘贴图片；文件和图片会显示在这里，可随时删除。',
    bugDescriptionOnly: 'Bug 描述',
    bugDescriptionPlaceholder: 'Bug 描述\n\n复现步骤\n\n预期结果\n\n实际结果\n\n相关日志',
    bugDescriptionHelp: '支持粘贴图片；文件和图片会显示在这里，可随时删除。',
    attachmentRemove: '删除附件', attachmentImage: '图片', attachmentFile: '文件',
    chatCreateKicker: 'NEW / CONVERSATION',
    chatCreateSubtitle: '先选一个项目和执行 Agent，再开始一段持久化讨论。',
    chatTitlePlaceholder: '例如：支付发布讨论',
    repositoryLocalPath: '本地目录路径', repositoryOwner: '负责人',
    repositorySourceHelp: '项目必须使用绝对路径。原生桌面选择器会自动填入；普通浏览器出于安全限制只返回目录名，请手动粘贴完整路径。',
    repositoryOwnerPlaceholder: '成员 ID（可选）', repositorySource: '项目来源',
    localProject: '本地项目', remoteProject: '远程仓库', createProject: '新建项目', newRequirement: '创建交付项', submitResource: '保存', repositoryChooseFolder: '选择本地目录', repositoryFolderChosen: '已选择目录', repositoryPathAbsoluteRequired: '请输入绝对路径，例如 /Users/me/project；仅有目录名称无法浏览。', repositoryPathPickerUnavailable: '浏览器只返回目录名称，未写入路径；请在输入框粘贴绝对路径。', repositorySourceAuto: '项目类型会根据来源自动判断', repositoryOwnerChoose: '选择负责人', repositoryOwnerEmpty: '暂无可选负责人', repositoryEdit: '编辑', repositoryDelete: '删除', repositoryBrowse: '浏览', repositoryDeleteConfirm: '确定删除这个项目吗？删除后项目绑定关系也会移除。', repositoryIndexHelp: '索引中：正在记录项目文件的可检索快照状态，不会下载远程仓库。', repositoryReadyHelp: '已就绪：项目快照状态已记录。', repositoryRemoteUnavailable: '这是远程地址元数据，当前未下载到本机，暂时无法浏览。', repositoryBrowseTitle: '浏览项目', repositoryBrowseEmpty: '目录为空', repositoryBinary: '二进制文件，无法在线预览', repositoryTruncated: '文件过大，仅显示前 1 MiB', repositoryLoadFailed: '项目内容加载失败', repositoryPathFallback: '也可以直接粘贴完整本地路径'
  });
  Object.assign(translations.en, {
    logoutConfirm: 'Sign out of the current account?',
    logoutConfirmTitle: 'Sign out of this account',
    logoutConfirmMessage: 'You will need to sign in again to continue using this workspace.',
    logoutCancel: 'Cancel',
    logoutConfirmAction: 'Sign out',
    requirementDescriptionOnly: 'Requirement description',
    requirementDescriptionPlaceholder: 'Describe the outcome, context, constraints, and acceptance focus...',
    requirementDescriptionHelp: 'Images can be pasted here. Files and images appear below and can be removed anytime.',
    bugDescriptionOnly: 'Bug description',
    bugDescriptionPlaceholder: 'Bug description\n\nReproduction steps\n\nExpected result\n\nActual result\n\nRelevant logs',
    bugDescriptionHelp: 'Images can be pasted here. Files and images appear below and can be removed anytime.',
    attachmentRemove: 'Remove attachment', attachmentImage: 'Image', attachmentFile: 'File',
    chatCreateKicker: 'NEW / CONVERSATION',
    chatCreateSubtitle: 'Choose a project and execution agent before starting a durable discussion.',
    chatTitlePlaceholder: 'For example: Payment release discussion',
    repositoryLocalPath: 'Local directory path', repositoryOwner: 'Owner',
    repositorySourceHelp: 'Projects require an absolute path. Native desktop pickers fill it automatically; regular browsers expose only the folder name, so paste the full path manually.',
    repositoryOwnerPlaceholder: 'Member ID (optional)', repositorySource: 'Project source',
    localProject: 'Local project', remoteProject: 'Remote repository', createProject: 'New project', newRequirement: 'Create delivery item', submitResource: 'Save', repositoryChooseFolder: 'Choose local folder', repositoryFolderChosen: 'Folder selected', repositoryPathAbsoluteRequired: 'Enter an absolute path, such as /Users/me/project; a folder name alone cannot be browsed.', repositoryPathPickerUnavailable: 'The browser returned only the folder name, so it was not used as a path. Paste the absolute path into the field.', repositorySourceAuto: 'Project type is detected from the selected source', repositoryOwnerChoose: 'Choose an owner', repositoryOwnerEmpty: 'No owners available', repositoryEdit: 'Edit', repositoryDelete: 'Delete', repositoryBrowse: 'Browse', repositoryDeleteConfirm: 'Delete this project? Its project bindings will also be removed.', repositoryIndexHelp: 'Indexing: recording a searchable snapshot state for the project files. It does not download a remote repository.', repositoryReadyHelp: 'Ready: the project snapshot state has been recorded.', repositoryRemoteUnavailable: 'This is remote URL metadata. It has not been downloaded locally, so it cannot be browsed yet.', repositoryBrowseTitle: 'Browse project', repositoryBrowseEmpty: 'Directory is empty', repositoryBinary: 'Binary file cannot be previewed online', repositoryTruncated: 'File is large; showing the first 1 MiB only', repositoryLoadFailed: 'Could not load project contents', repositoryPathFallback: 'You can also paste the full local path manually'
  });

  Object.assign(translations.zh, {
    deliveryComposerKicker: '交付项 / 统一入口',
    requirementType: '需求', bugType: 'Bug',
    deliveryParentHelp: 'Bug 会继承父需求的项目与执行人，并沿同一条交付链追踪。',
    deliveryInheritedContext: '已从父需求继承项目与执行人',
    deliveryTitle: '交付台', deliverySubtitle: '需求、方案、开发、验证与 Bug 在同一个交付上下文中完成。',
    deliveryCreate: '新建交付项', deliveryTotal: '交付项', deliveryRequirements: '需求', deliveryBugs: 'Bug', deliveryUnresolved: '未解决 Bug', deliveryPlan: '方案 / Run',
    deliveryAll: '全部类型', deliveryRequirementFilter: '仅需求', deliveryBugFilter: '仅 Bug', deliveryExpand: '展开 Bug', deliveryCollapse: '收起 Bug',
    deliveryAddBug: '在此需求下创建 Bug', deliveryUnlinkedBugs: '未关联需求的 Bug', deliveryOpenDetail: '打开交付详情', deliveryCanvas: '交付画布',
    deliveryContext: '上下文', deliveryDesign: '方案', deliveryDevelopment: '开发', deliveryValidation: '验证', deliveryRelatedBugs: '关联 Bug', deliveryNoPlan: '尚未生成方案', deliveryPlanReady: '方案已生成', deliveryPlanRunning: '执行中', deliveryNoChildren: '暂无关联 Bug',
    deliveryBugCreated: 'Bug 已创建并关联到父需求', deliveryRequirementCreated: '需求已创建', deliveryTypeLabel: '交付类型', deliveryDescriptionLabel: '描述', deliveryRequirementTitle: '创建需求', deliveryBugTitle: '创建 Bug', deliveryRequirementSubtitle: '记录一个可验证的交付结果', deliveryBugSubtitle: '在父需求下记录一个可复现的缺陷',
    deliveryChooseParent: '请选择所属需求', deliveryParentRequired: '创建 Bug 前必须选择所属需求',
    deliveryBugDescriptionHelp: '先写故障现象，再补充复现步骤、预期与实际结果。', deliveryRequirementDescriptionHelp: '描述要交付的结果、背景、约束和验收重点。',
    deliverySearch: '搜索交付项、Key 或负责人', deliveryStatusFilter: '按状态筛选', deliveryStatusAll: '全部状态', deliveryStartDevelopment: '开始开发', deliveryOpenExecution: '打开执行舱', deliveryBack: '返回交付台', deliveryPlanNone: '未建立方案', deliveryPlanReady: '方案已就绪', deliveryPlanRunning: '方案执行中', deliveryPlanFailed: '方案执行失败', deliveryValidationPending: '等待验证证据', deliveryValidationFromStatus: '由交付状态驱动', deliveryProject: '项目', deliveryOwner: '负责人', deliveryTeam: '执行小队', deliveryBugSummary: '共 {total} 个 Bug · {open} 个未解决', deliveryUnlinkedHint: '这些 Bug 尚未绑定父需求，先保留在交付台以便补齐上下文。', deliveryContextSummary: '所有上下文都沿同一条交付链保留。', deliveryPlanSummary: '方案生成、评审与开发 Run 共用同一交付项。', deliveryDevelopmentSummary: '开发由冻结的 Agent / Squad 图执行。', deliveryValidationSummary: '验证结果与 Bug 修复状态回写到父需求。', deliveryNoPlanAction: '生成方案后即可开始开发。', deliveryOpenBug: '打开 Bug', deliveryNoRelatedBugs: '暂无关联 Bug', deliveryNoUnlinkedBugs: '暂无未关联 Bug'
  });
  Object.assign(translations.en, {
    deliveryComposerKicker: 'DELIVERY / SINGLE ENTRY',
    requirementType: 'Requirement', bugType: 'Bug',
    deliveryParentHelp: 'Bugs inherit the parent requirement project and executor, then stay on the same delivery thread.',
    deliveryInheritedContext: 'Project and executor inherited from parent requirement',
    deliveryTitle: 'Delivery', deliverySubtitle: 'Context, plan, development, validation, and bugs live in one delivery workspace.',
    deliveryCreate: 'New delivery item', deliveryTotal: 'delivery items', deliveryRequirements: 'requirements', deliveryBugs: 'bugs', deliveryUnresolved: 'unresolved bugs', deliveryPlan: 'Plan / run',
    deliveryAll: 'All types', deliveryRequirementFilter: 'Requirements only', deliveryBugFilter: 'Bugs only', deliveryExpand: 'Expand bugs', deliveryCollapse: 'Collapse bugs',
    deliveryAddBug: 'Create a bug under this requirement', deliveryUnlinkedBugs: 'Bugs without a requirement', deliveryOpenDetail: 'Open delivery detail', deliveryCanvas: 'Delivery canvas',
    deliveryContext: 'Context', deliveryDesign: 'Plan', deliveryDevelopment: 'Development', deliveryValidation: 'Validation', deliveryRelatedBugs: 'Related bugs', deliveryNoPlan: 'No plan generated yet', deliveryPlanReady: 'Plan ready', deliveryPlanRunning: 'Running', deliveryNoChildren: 'No related bugs',
    deliveryBugCreated: 'Bug created and linked to the parent requirement', deliveryRequirementCreated: 'Requirement created', deliveryTypeLabel: 'Delivery type', deliveryDescriptionLabel: 'Description', deliveryRequirementTitle: 'Create requirement', deliveryBugTitle: 'Create bug', deliveryRequirementSubtitle: 'Record a verifiable delivery outcome', deliveryBugSubtitle: 'Record a reproducible defect under its parent requirement',
    deliveryChooseParent: 'Choose a parent requirement', deliveryParentRequired: 'Choose a parent requirement before creating a bug',
    deliveryBugDescriptionHelp: 'Start with the failure, then add reproduction steps, expected behavior, and actual behavior.', deliveryRequirementDescriptionHelp: 'Describe the outcome, context, constraints, and acceptance focus.',
    deliverySearch: 'Search delivery items, keys, or owners', deliveryStatusFilter: 'Filter by status', deliveryStatusAll: 'All statuses', deliveryStartDevelopment: 'Start development', deliveryOpenExecution: 'Open execution cockpit', deliveryBack: 'Back to delivery', deliveryPlanNone: 'No plan yet', deliveryPlanReady: 'Plan ready', deliveryPlanRunning: 'Plan running', deliveryPlanFailed: 'Plan failed', deliveryValidationPending: 'Waiting for validation evidence', deliveryValidationFromStatus: 'Driven by delivery status', deliveryProject: 'Project', deliveryOwner: 'Owner', deliveryTeam: 'Execution team', deliveryBugSummary: '{total} bugs total · {open} unresolved', deliveryUnlinkedHint: 'These bugs do not have a parent requirement yet; keep them visible until context is restored.', deliveryContextSummary: 'All context stays on one delivery thread.', deliveryPlanSummary: 'Plan generation, review, and development runs share the same delivery item.', deliveryDevelopmentSummary: 'Development runs from a frozen Agent / Squad graph.', deliveryValidationSummary: 'Validation and bug repair status roll back into the parent requirement.', deliveryNoPlanAction: 'Generate a plan before starting development.', deliveryOpenBug: 'Open bug', deliveryNoRelatedBugs: 'No related bugs', deliveryNoUnlinkedBugs: 'No unlinked bugs'
  });

  let currentUser = null;
  const entityDraftFiles = { requirement: [], bug: [] };
  let directory = [];
  let managedUsers = [];
  let availableMenus = menuIDs.slice();
  let nativeAgents = [];
  let nativeSquads = [];
  let nativePlans = [];
  let agentCreationJobs = loadAgentCreationJobs();
  let activeGraphEditor = null;
  let activeExecutionPlanID = '';
  const executionTimelineCache = new Map();
  let executionTimelineRefreshTimer = null;
  let commentReplyParentID = '';
  let commentMentionIndex = -1;
  let commentMentionOptions = [];
  let commentMentionStart = -1;
  let commentMentionTargetID = '';
  let commentDraftFiles = [];
  let commentMentionRoster = [];
  let commentMentionRosterPromise = null;
  let activeCommentTargetType = '';
  let activeCommentTargetID = '';
  let activeCommentItems = [];
  let commentActivity = new Map();
  let workspaceMigrationFile = null;
  let workspaceMigrationReport = null;
  let agentRuntimeSkillItems = [];
  let agentDisabledRuntimeSkills = [];
  let agentRuntimeSkillRequest = 0;
  let agentRuntimeConfigs = new Map();
  let agentRuntimeEnvironments = new Map();
  let agentPreservedCustomArgs = [];
  let deliveryComposerKind = 'requirement';
  let deliveryComposerParentID = '';
  const deliveryExpandedRequirements = new Set();
  let deliveryFilterKind = 'all';
  let deliveryFilterStatus = '';
  let deliverySearchTerm = '';
  let orchestrationStatusState = {message: '', bad: false};

  const baseOrchestrationLoadCore = loadCore;
  loadCore = async function loadCoreWithOrchestration(force = false) {
    await baseOrchestrationLoadCore(force);
    await loadAllRequirementPages();
    await loadAllBugPages();
    if (window.adroCanAccessMenu?.('agents')) await loadOrchestrationData();
  };

  async function loadAllRequirementPages() {
    if (typeof window.adroCanAccessMenu === 'function' && !window.adroCanAccessMenu('delivery')) return;
    let cursor = '';
    const seen = new Set();
    const all = [];
    while (true) {
      const suffix = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
      let response;
      try {
        response = await api(`/api/v1/requirements?limit=250${suffix}`);
      } catch (_) {
        return;
      }
      for (const item of response?.items || []) {
        if (!item?.id || seen.has(item.id)) continue;
        seen.add(item.id);
        all.push(item);
      }
      const next = String(response?.next_cursor || '');
      if (!next || next === cursor || seen.has(next)) break;
      seen.add(next);
      cursor = next;
    }
    if (all.length !== requirements.length || all.some(item => !requirements.some(existing => existing.id === item.id))) {
      requirements = all;
      render();
    }
  }

  async function loadAllBugPages() {
    if (typeof window.adroCanAccessMenu === 'function' && !window.adroCanAccessMenu('delivery')) return;
    let cursor = '';
    const seen = new Set();
    const all = [];
    while (true) {
      const suffix = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
      let response;
      try {
        response = await api(`/api/v1/bugs?limit=250${suffix}`);
      } catch (_) {
        return;
      }
      for (const item of response?.items || []) {
        if (!item?.id || seen.has(item.id)) continue;
        seen.add(item.id);
        all.push(item);
      }
      const next = String(response?.next_cursor || '');
      if (!next || next === cursor || seen.has(next)) break;
      seen.add(next);
      cursor = next;
    }
    if (all.length !== bugs.length || all.some(item => !bugs.some(existing => existing.id === item.id))) {
      bugs = all;
      render();
    }
  }

  async function loadOrchestrationData() {
    const settled = await Promise.allSettled([
      api('/api/v1/workspaces/local/agents'),
      api('/api/v1/workspaces/local/squads'),
      api('/api/v1/execution-plans?workspace_id=local')
    ]);
    if (settled[0].status === 'fulfilled') nativeAgents = settled[0].value.items || [];
    if (settled[1].status === 'fulfilled') nativeSquads = settled[1].value.items || [];
    if (settled[2].status === 'fulfilled') nativePlans = settled[2].value.items || [];
    if (currentView === 'agents' || currentView === 'executions') render();
  }

  const focusIfPresent = selector => {
    const element = $(selector);
    if (element) element.focus();
  };
  const idempotencyKey = () => typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : String(Date.now());

  function loadAgentCreationJobs() {
    try {
      const items = JSON.parse(localStorage.getItem('adro.agentCreationJobs') || '[]');
      return Array.isArray(items) ? items.slice(0, 8) : [];
    } catch (_) { return []; }
  }

  function saveAgentCreationJobs() {
    try { localStorage.setItem('adro.agentCreationJobs', JSON.stringify(agentCreationJobs.slice(0, 8))); } catch (_) {}
  }

  function upsertAgentCreationJob(job) {
    agentCreationJobs = [job, ...agentCreationJobs.filter(item => item.id !== job.id)].slice(0, 8);
    saveAgentCreationJobs();
  }

  function agentCreationState(job) {
    if (job.status === 'done') return {className: 'good', label: t('agentCreatedState')};
    if (job.status === 'failed') return {className: 'bad', label: t('agentNeedsAttentionState')};
    return {className: 'warn', label: t('agentCreatingState')};
  }

  function renderAgentCreationJobs() {
    const jobs = agentCreationJobs.slice(0, 5);
    if (!jobs.length) return;
    const host = document.querySelector('.orchestration-studio');
    if (!host || host.querySelector('.agent-job-panel')) return;
    const panel = document.createElement('section');
    panel.className = 'agent-job-panel';
    panel.innerHTML = `<div class="agent-job-panel-head"><div><strong>${escapeHTML(t('agentCreateJobs'))}</strong><small>${escapeHTML(t('agentCreateJobsHelp'))}</small></div></div>${jobs.map(job => { const state = agentCreationState(job); const action = job.status === 'failed' ? `<button class="secondary" data-agent-job-retry="${escapeHTML(job.id)}" type="button">${escapeHTML(t('agentRetry'))}</button>` : job.agent_id ? `<button class="secondary" data-agent-job-view="${escapeHTML(job.agent_id)}" type="button">${escapeHTML(t('agentView'))}</button>` : ''; return `<div class="agent-job ${state.className}"><div><strong>${escapeHTML(job.name || t('agentCreateTitle'))}</strong><small>${escapeHTML(job.error || (job.status === 'running' ? t('agentCreateRunning') : t('agentCreateDone')))}</small></div><span class="status ${state.className}">${escapeHTML(state.label)}</span>${action}</div>`; }).join('')}`;
    const anchor = host.querySelector('#orchestrationStatus');
    anchor?.after(panel);
    panel.querySelectorAll('[data-agent-job-view]').forEach(button => { button.onclick = () => { const agent = nativeAgents.find(item => item.id === button.dataset.agentJobView); if (agent) showAgentDialog(false, agent); }; });
    panel.querySelectorAll('[data-agent-job-retry]').forEach(button => { button.onclick = async () => { const job = agentCreationJobs.find(item => item.id === button.dataset.agentJobRetry); if (!job) return; await showAgentDialog(false); $('#agentBuilderPrompt').value = job.prompt || ''; }; });
  }

  function entityFileInput(kind) {
    return $(`#${kind === 'requirement' ? 'requirementAttachments' : 'bugAttachments'}`);
  }

  function entityFilePreview(kind) {
    return $(`#${kind === 'requirement' ? 'requirementAttachmentPreview' : 'bugAttachmentPreview'}`);
  }

  function releaseEntityFile(file) {
    if (file?.previewURL) URL.revokeObjectURL(file.previewURL);
  }

  function normalizeEntityFile(file, index = 0) {
    if (!file) return null;
    const fallbackName = `pasted-image-${Date.now()}-${index + 1}.png`;
    const name = String(file.name || '').trim() || fallbackName;
    if (name === file.name) return file;
    return new File([file], name, {type: file.type || 'application/octet-stream', lastModified: file.lastModified || Date.now()});
  }

  function syncEntityFileInput(kind) {
    const input = entityFileInput(kind);
    if (!input || typeof DataTransfer === 'undefined') return;
    const transfer = new DataTransfer();
    entityDraftFiles[kind].forEach(item => transfer.items.add(item.file));
    input.files = transfer.files;
  }

  function renderEntityFilePreview(kind) {
    const target = entityFilePreview(kind);
    if (!target) return;
    const files = entityDraftFiles[kind];
    target.innerHTML = files.map((item, index) => {
      const image = item.file.type.startsWith('image/')
        ? `<button class="entity-attachment-preview-button" type="button" data-preview-entity-attachment="${escapeHTML(kind)}" data-entity-attachment-index="${index}" aria-label="${escapeHTML(t('preview'))}"><img src="${escapeHTML(item.previewURL)}" alt="${escapeHTML(item.file.name)}"></button>`
        : `<span class="entity-attachment-icon" aria-hidden="true">▤</span>`;
      return `<div class="entity-attachment" data-entity-attachment="${kind}" data-entity-attachment-index="${index}"><div class="entity-attachment-media">${image}</div><div class="entity-attachment-copy"><strong title="${escapeHTML(item.file.name)}">${escapeHTML(item.file.name)}</strong><small>${escapeHTML(formatBytes(item.file.size))}</small></div><button class="entity-attachment-remove" type="button" data-remove-entity-attachment="${kind}" data-entity-attachment-index="${index}" title="${escapeHTML(t('attachmentRemove'))}" aria-label="${escapeHTML(t('attachmentRemove'))}">×</button></div>`;
    }).join('');
  }

  function addEntityFiles(kind, files) {
    const existing = new Set(entityDraftFiles[kind].map(item => `${item.file.name}:${item.file.size}:${item.file.lastModified}`));
    for (const [index, source] of Array.from(files || []).entries()) {
      const file = normalizeEntityFile(source, index);
      if (!file || existing.has(`${file.name}:${file.size}:${file.lastModified}`)) continue;
      existing.add(`${file.name}:${file.size}:${file.lastModified}`);
      entityDraftFiles[kind].push({file, previewURL: file.type.startsWith('image/') ? URL.createObjectURL(file) : ''});
    }
    syncEntityFileInput(kind);
    renderEntityFilePreview(kind);
  }

  function clearEntityFiles(kind) {
    entityDraftFiles[kind].forEach(releaseEntityFile);
    entityDraftFiles[kind] = [];
    const input = entityFileInput(kind);
    if (input) input.value = '';
    renderEntityFilePreview(kind);
  }

  function entityFiles(kind) {
    return entityDraftFiles[kind].map(item => item.file);
  }

  function bindEntityAttachments(kind, textareaSelector) {
    const input = entityFileInput(kind);
    const textarea = $(textareaSelector);
    const dropZone = input?.closest('.file-drop');
    if (input && input.dataset.bound !== 'true') {
      input.dataset.bound = 'true';
      input.onchange = () => addEntityFiles(kind, input.files);
    }
    if (textarea && textarea.dataset.bound !== 'true') {
      textarea.dataset.bound = 'true';
      textarea.addEventListener('paste', event => {
        const images = Array.from(event.clipboardData?.items || [])
          .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
          .map(item => item.getAsFile())
          .filter(Boolean);
        if (images.length) {
          event.preventDefault();
          addEntityFiles(kind, images);
        }
      });
    }
    if (dropZone && dropZone.dataset.bound !== 'true') {
      dropZone.dataset.bound = 'true';
      const setDragging = value => dropZone.classList.toggle('is-dragging', value);
      dropZone.addEventListener('dragenter', event => {
        event.preventDefault();
        setDragging(true);
      });
      dropZone.addEventListener('dragover', event => {
        event.preventDefault();
        if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
        setDragging(true);
      });
      dropZone.addEventListener('dragleave', event => {
        if (!dropZone.contains(event.relatedTarget)) setDragging(false);
      });
      dropZone.addEventListener('drop', event => {
        event.preventDefault();
        setDragging(false);
        addEntityFiles(kind, event.dataTransfer?.files || []);
      });
    }
    renderEntityFilePreview(kind);
  }

  document.addEventListener('click', event => {
    const button = event.target.closest?.('[data-remove-entity-attachment]');
    if (!button) return;
    const kind = button.dataset.removeEntityAttachment;
    const index = Number(button.dataset.entityAttachmentIndex);
    const item = entityDraftFiles[kind]?.[index];
    if (!item) return;
    releaseEntityFile(item);
    entityDraftFiles[kind].splice(index, 1);
    syncEntityFileInput(kind);
    renderEntityFilePreview(kind);
  });

  function ensureAttachmentPreviewDialog() {
    if ($('#attachmentPreviewDialog')) return;
    document.body.insertAdjacentHTML('beforeend', `<dialog id="attachmentPreviewDialog" class="attachment-preview-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / ATTACHMENT</p><h2 id="attachmentPreviewTitle"></h2></div><button class="dialog-close" id="closeAttachmentPreview" type="button" aria-label="${escapeHTML(t('close'))}">×</button></div><div id="attachmentPreviewBody" class="attachment-preview-body"></div></dialog>`);
    const dialog = $('#attachmentPreviewDialog');
    $('#closeAttachmentPreview').onclick = () => dialog.close();
    dialog.addEventListener('click', event => { if (event.target === event.currentTarget) dialog.close(); });
  }

  function openAttachmentPreview(file, title = '') {
    if (!file) return;
    ensureAttachmentPreviewDialog();
    const dialog = $('#attachmentPreviewDialog');
    $('#attachmentPreviewTitle').textContent = title || file.name || t('preview');
    const body = $('#attachmentPreviewBody');
    if (file.type?.startsWith('image/') && (file instanceof Blob || file.previewURL)) {
      const source = file.previewURL || URL.createObjectURL(file);
      body.innerHTML = `<img src="${escapeHTML(source)}" alt="${escapeHTML(file.name || '')}">`;
      if (!file.previewURL) body.dataset.revokeURL = source;
    } else {
      body.innerHTML = `<div class="attachment-file-preview"><strong>${escapeHTML(file.name || t('attachmentFile'))}</strong><span>${escapeHTML(formatBytes(file.size || 0))}</span></div>`;
    }
    dialog.showModal();
  }

  document.addEventListener('click', event => {
    const button = event.target.closest?.('[data-preview-entity-attachment]');
    if (!button) return;
    const kind = button.dataset.previewEntityAttachment;
    const item = entityDraftFiles[kind]?.[Number(button.dataset.entityAttachmentIndex)];
    if (item) openAttachmentPreview(item.file, item.file.name);
  });

  window.adroCanAccessMenu = menu => {
    if (currentUser && currentUser.role === 'admin') return true;
    if (['delivery', 'requirements', 'bugs', 'designReview', 'executions'].includes(menu)) {
      return availableMenus.some(item => ['delivery', 'requirements', 'bugs', 'designReview', 'executions'].includes(item));
    }
    return availableMenus.includes(menu);
  };

  const roleLabel = role => t(role === 'admin' ? 'roleAdmin' : role === 'viewer' ? 'roleViewer' : 'roleMember');
  const userLabel = id => {
    const item = directory.find(candidate => candidate.id === id || candidate.username === id);
    return item ? `${item.display_name} · ${item.username}` : id || '-';
  };
  const repositoryLabel = id => {
    const item = repositories.find(candidate => candidate.id === id);
    return item ? item.canonical_name : id || '-';
  };
  const requirementLabel = id => {
    const item = requirements.find(candidate => candidate.id === id);
    return item ? `${item.key} · ${item.title}` : id || '-';
  };

  const baseApplyTranslations = applyTranslations;
  applyTranslations = function enhancedTranslations() {
    baseApplyTranslations();
    updateUserChip();
    if ($('#requirementDialog')?.open) syncDeliveryComposer(deliveryComposerKind, deliveryComposerParentID);
  };

  function updateUserChip() {
    if (!currentUser) return;
    $('#userName').textContent = currentUser.display_name;
    $('#userRole').textContent = roleLabel(currentUser.role);
    $('#userAvatar').textContent = (currentUser.display_name || currentUser.username || 'A').slice(0, 1).toUpperCase();
  }

  function showLogin() {
    $('#appShell').hidden = true;
    $('#loginGate').hidden = false;
    document.title = t('appTitle');
    setTimeout(() => focusIfPresent('#loginIdentity'), 0);
  }

  async function enterApplication(user) {
    currentUser = user;
    availableMenus = user.role === 'admin' ? menuIDs.slice() : (user.menu_ids || []).slice();
    $('#loginGate').hidden = true;
    $('#appShell').hidden = false;
    updateUserChip();
    applyMenuAccess();
    await loadIdentityData();
    await loadCore(true);
  }

  function applyMenuAccess() {
    document.querySelectorAll('.nav-item, .nav-chat').forEach(item => {
      item.hidden = !window.adroCanAccessMenu(item.dataset.view);
    });
    document.querySelectorAll('.nav-section').forEach(section => {
      let sibling = section.nextElementSibling;
      let visible = false;
      while (sibling && !sibling.classList.contains('nav-section') && !sibling.classList.contains('sidebar-foot')) {
        if (sibling.classList.contains('nav-item') && !sibling.hidden) visible = true;
        sibling = sibling.nextElementSibling;
      }
      section.hidden = !visible;
    });
    if (!window.adroCanAccessMenu(currentView)) {
      const firstVisible = document.querySelector('.nav-item:not([hidden]), .nav-chat:not([hidden])');
      currentView = firstVisible?.dataset.view || 'workbench';
    }
    const activeView = ['requirements', 'bugs', 'designReview', 'executions'].includes(currentView) ? 'delivery' : currentView;
    document.querySelectorAll('.nav-item, .nav-chat').forEach(item => item.classList.toggle('active', item.dataset.view === activeView));
  }

  async function loadIdentityData() {
    const calls = [api('/api/v1/directory')];
    if (currentUser && currentUser.role === 'admin') calls.push(api('/api/v1/users'));
    const results = await Promise.allSettled(calls);
    if (results[0] && results[0].status === 'fulfilled') directory = results[0].value.items || [];
    if (results[1] && results[1].status === 'fulfilled') {
      managedUsers = results[1].value.items || [];
      availableMenus = results[1].value.menus || menuIDs.slice();
    }
  }

  $('#loginForm').onsubmit = async event => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const error = $('#loginError');
    const submit = formElement.querySelector('button[type="submit"]');
    error.textContent = '';
    submit.disabled = true;
    try {
      const session = await api('/api/v1/auth/login', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: String(form.get('username')).trim(), password: String(form.get('password')) })
      });
      formElement.reset();
      await enterApplication(session.user);
    } catch (_) {
      error.textContent = t('loginFailed');
    } finally {
      submit.disabled = false;
    }
  };

  function confirmLogout() {
    const dialog = $('#logoutConfirmDialog');
    if (!dialog?.showModal) return Promise.resolve(window.confirm(t('logoutConfirm')));
    return new Promise(resolve => {
      const finish = () => resolve(dialog.returnValue === 'confirm');
      dialog.addEventListener('close', finish, {once: true});
      dialog.showModal();
    });
  }

  $('#logoutConfirmButton').onclick = () => $('#logoutConfirmDialog').close('confirm');
  $('#logoutConfirmCancel').onclick = () => $('#logoutConfirmDialog').close('cancel');
  $('#logoutConfirmClose').onclick = () => $('#logoutConfirmDialog').close('cancel');
  $('#logoutButton').onclick = async () => {
    if (!await confirmLogout()) return;
    try { await api('/api/v1/auth/logout', { method: 'POST' }); } catch (_) {}
    if (stream) {
      stream.onclose = null;
      stream.close();
      stream = null;
    }
    currentUser = null;
    requirements = [];
    bugs = [];
    showLogin();
  };

  function optionMarkup(items, value, label, emptyKey, optional = false) {
    const options = [];
    if (optional) options.push(`<option value="">${escapeHTML(t(emptyKey))}</option>`);
    if (!items.length && !optional) options.push(`<option value="" disabled selected>${escapeHTML(t(emptyKey))}</option>`);
    for (const item of items) options.push(`<option value="${escapeHTML(value(item))}">${escapeHTML(label(item))}</option>`);
    return options.join('');
  }

  function squadMembersForAgents(agents) {
    return agents.map((agent, index) => ({
      id: `member-${agent.id}`,
      agent_id: agent.id,
      role: index === 0 ? 'leader' : (agent.role || `member-${index + 1}`),
      leader: index === 0,
      input_schema: agent.input_schema,
      output_schema: agent.output_schema,
      max_attempts: 3,
      budget: {tokens: 120000, tool_calls: 200, concurrent: 1}
    }));
  }

  function graphForRequirementTarget(value) {
    const [kind, id] = String(value || '').split(':', 2);
    if (kind === 'agent') {
      const agent = nativeAgents.find(item => item.id === id && item.status === 'active');
      return agent ? {graph: graphForNativeAgent(agent), body: {agent_id: agent.id, agent_revision: agent.revision}} : null;
    }
    if (kind === 'squad') {
      const squad = nativeSquads.find(item => item.id === id && item.status === 'published');
      return squad ? {graph: squad.graph, body: {squad_id: squad.id, squad_version: squad.published_version || squad.revision}} : null;
    }
    return null;
  }

  function populateRequirementPlanAgents() {
    const select = $('#planAgent');
    if (!select) return;
    const active = nativeAgents.filter(agent => agent.status === 'active');
    select.innerHTML = `<option value="">${escapeHTML(t('noExecutors'))}</option>${active.map(agent => `<option value="${escapeHTML(agent.id)}">${escapeHTML(agent.name || agent.id)} · r${escapeHTML(String(agent.revision || 0))}</option>`).join('')}`;
  }

  function ensureRequirementOrchestrationControls() {
    const checkbox = $('#autoGeneratePlan');
    const field = $('#planAgentField');
    const select = $('#planAgent');
    if (!checkbox || !field || !select) return;
    populateRequirementPlanAgents();
    const sync = () => {
      field.hidden = !checkbox.checked;
      select.required = checkbox.checked;
      select.disabled = !checkbox.checked;
    };
    checkbox.onchange = sync;
    sync();
  }

  function syncDeliveryComposer(kind = deliveryComposerKind, parentID = deliveryComposerParentID) {
    const form = $('#requirementForm');
    if (!form) return;
    const bug = kind === 'bug';
    deliveryComposerKind = bug ? 'bug' : 'requirement';
    deliveryComposerParentID = parentID || '';
    form.dataset.deliveryKind = deliveryComposerKind;
    form.querySelectorAll('[data-delivery-kind]').forEach(button => {
      const active = button.dataset.deliveryKind === deliveryComposerKind;
      button.classList.toggle('active', active);
      button.setAttribute('aria-selected', String(active));
    });
    const parentField = $('#deliveryParentField');
    const parentSelect = $('#bugRequirement');
    const orchestration = $('#deliveryOrchestration');
    const repository = $('#requirementRepository');
    const assignee = $('#requirementAssignee');
    const inheritanceHint = $('#deliveryInheritanceHint');
    const description = $('#requirementDescription');
    const descriptionLabel = $('#deliveryDescriptionLabel');
    const descriptionHelp = $('#deliveryDescriptionHelp');
    const status = $('#deliveryStatus');
    const submitLabel = $('#deliverySubmitLabel');
    const submit = form.querySelector('button[type="submit"]');
    const dialogTitle = $('#deliveryDialogTitle');
    const dialogSubtitle = $('#deliveryDialogSubtitle');
    if (parentField) parentField.hidden = !bug;
    if (parentSelect) parentSelect.required = bug;
    if (orchestration) orchestration.hidden = bug;
    if (repository) repository.disabled = bug;
    if (assignee) assignee.disabled = bug;
    const selected = bug ? requirements.find(item => item.id === (parentSelect?.value || parentID)) : null;
    if (inheritanceHint) inheritanceHint.hidden = !selected;
    if (descriptionLabel) descriptionLabel.textContent = t('deliveryDescriptionLabel');
    if (descriptionHelp) descriptionHelp.textContent = t(bug ? 'deliveryBugDescriptionHelp' : 'deliveryRequirementDescriptionHelp');
    if (dialogTitle) dialogTitle.textContent = t(bug ? 'deliveryBugTitle' : 'deliveryRequirementTitle');
    if (dialogSubtitle) dialogSubtitle.textContent = t(bug ? 'deliveryBugSubtitle' : 'deliveryRequirementSubtitle');
    if (description) {
      description.placeholder = t(bug ? 'bugDescriptionPlaceholder' : 'requirementDescriptionPlaceholder');
      description.setAttribute('aria-label', t(bug ? 'bugDescriptionOnly' : 'requirementDescriptionOnly'));
    }
    if (status) {
      status.className = `status ${bug ? 'bad' : 'active'}`;
      status.textContent = t(bug ? 'open' : 'statusReceived');
    }
    if (submitLabel) submitLabel.textContent = t(bug ? 'createBug' : 'createRequirement');
    if (submit) submit.disabled = bug && !selected;
    if (parentSelect) parentSelect.setAttribute('aria-invalid', String(bug && !selected));
    if (selected) {
      const repositoryID = selected.repository_ids?.[0] || '';
      const assigneeID = selected.assignee_member_ids?.[0] || '';
      if (repository && repositoryID && [...repository.options].some(option => option.value === repositoryID)) repository.value = repositoryID;
      if (assignee && assigneeID && [...assignee.options].some(option => option.value === assigneeID)) assignee.value = assigneeID;
    }
  }

  function populateDeliveryComposer(parentID = deliveryComposerParentID) {
    const repository = $('#requirementRepository');
    const assignee = $('#requirementAssignee');
    const parent = $('#bugRequirement');
    if (repository) repository.innerHTML = optionMarkup(repositories, item => item.id, item => item.canonical_name, 'noProjects');
    if (assignee) assignee.innerHTML = optionMarkup(directory, item => item.id, item => `${item.display_name} · ${item.username}`, 'noExecutors');
    if (parent) {
      const placeholder = requirements.length ? t('deliveryChooseParent') : t('noRequirements');
      parent.innerHTML = `<option value="" disabled selected>${escapeHTML(placeholder)}</option>${requirements.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(`${item.key} · ${item.title}`)}</option>`).join('')}`;
      if (parentID && [...parent.options].some(option => option.value === parentID)) parent.value = parentID;
    }
    syncDeliveryComposer(deliveryComposerKind, parent?.value || parentID);
  }

  function bindDeliveryComposer() {
    const form = $('#requirementForm');
    if (!form || form.dataset.deliveryBound === 'true') return;
    form.dataset.deliveryBound = 'true';
    form.querySelectorAll('[data-delivery-kind]').forEach(button => {
      button.addEventListener('click', () => {
        const kind = button.dataset.deliveryKind || 'requirement';
        syncDeliveryComposer(kind, kind === 'bug' ? ($('#bugRequirement')?.value || '') : '');
      });
    });
    $('#bugRequirement')?.addEventListener('change', () => syncDeliveryComposer('bug', $('#bugRequirement').value));
  }

  showDialog = function enhancedDeliveryDialog(kind = 'requirement', parentID = '') {
    const form = $('#requirementForm');
    if (!form) return;
    deliveryComposerKind = kind === 'bug' ? 'bug' : 'requirement';
    deliveryComposerParentID = parentID || '';
    $('#formError').textContent = '';
    clearEntityFiles('requirement');
    form.reset();
    bindDeliveryComposer();
    populateDeliveryComposer(deliveryComposerParentID);
    ensureRequirementOrchestrationControls();
    syncDeliveryComposer(deliveryComposerKind, deliveryComposerParentID);
    applyTranslations();
    syncDeliveryComposer(deliveryComposerKind, deliveryComposerParentID);
    $('#requirementDialog').showModal();
    bindEntityAttachments('requirement', '#requirementDescription');
    setTimeout(() => focusIfPresent('#requirementDescription'), 0);
    void loadIdentityData().then(() => {
      if ($('#requirementDialog').open) {
        populateDeliveryComposer(deliveryComposerParentID);
        populateRequirementPlanAgents();
        applyTranslations();
        syncDeliveryComposer(deliveryComposerKind, deliveryComposerParentID);
      }
    });
  };

  async function uploadEntityFiles(ownerType, ownerID, files) {
    const results = [];
    for (const file of files) {
      const body = new FormData();
      body.set('owner_type', ownerType);
      body.set('owner_id', ownerID);
      body.set('file', file, file.name);
      results.push(await api('/api/v1/attachments', { method: 'POST', body }));
    }
    return results;
  }

  $('#requirementForm').onsubmit = async event => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const data = new FormData(formElement);
    const submit = formElement.querySelector('button[type="submit"]');
    const description = String(data.get('description') || '').trim();
    const kind = formElement.dataset.deliveryKind || deliveryComposerKind;
    const title = description.split(/\r?\n/).map(item => item.trim()).find(Boolean)?.slice(0, 120) || t(kind === 'bug' ? 'bugDescriptionOnly' : 'requirementDescriptionOnly');
    const files = entityFiles('requirement');
    const parentID = String(data.get('requirement') || '').trim();
    const parent = requirements.find(item => item.id === parentID);
    const autoGeneratePlan = Boolean(data.get('auto_generate_plan'));
    const planAgentID = String(data.get('plan_agent_id') || '').trim();
    const planAgent = nativeAgents.find(agent => agent.id === planAgentID && agent.status === 'active');
    $('#formError').textContent = '';
    if (kind === 'bug' && !parent) {
      $('#formError').textContent = t('deliveryParentRequired');
      return;
    }
    if (autoGeneratePlan && !planAgent) {
      $('#formError').textContent = t('planAgentRequired');
      return;
    }
    submit.disabled = true;
    try {
      const creationKey = idempotencyKey();
      const created = kind === 'bug'
        ? await api('/api/v1/bugs', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Idempotency-Key': creationKey },
          body: JSON.stringify({
            workspace_id: 'local', title,
            repository_id: parent.repository_ids?.[0] || String(data.get('repository') || ''),
            assignee_member_id: parent.assignee_member_ids?.[0] || String(data.get('assignee') || ''),
            requirement_id: parent.id, steps_to_reproduce: description, expected: '', actual: description, log_excerpt: ''
          })
        })
        : await api('/api/v1/requirements', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Idempotency-Key': creationKey },
          body: JSON.stringify({
            workspace_id: 'local', title, description,
            acceptance_criteria: [description], assignee_member_ids: [String(data.get('assignee'))],
            repository_ids: [String(data.get('repository'))], priority: String(data.get('priority') || 'normal')
          })
        });
      try { await uploadEntityFiles(kind === 'bug' ? 'bug' : 'requirement', created.id, files); } catch (_) { $('#formError').textContent = t('uploadFailed'); return; }
      if (kind === 'requirement' && autoGeneratePlan) {
        try {
          await api(`/api/v1/requirements/${encodeURIComponent(created.id)}/execution-plan`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Idempotency-Key': `requirement-plan:${created.id}:${planAgent.id}` },
            body: JSON.stringify({agent_id: planAgent.id, agent_revision: planAgent.revision, idempotency_key: `requirement-plan:${created.id}:${planAgent.id}`})
          });
        } catch (_) {
          $('#formError').textContent = t('planCreateAfterRequirementFailed');
          return;
        }
      }
      if (kind === 'bug' && parent) deliveryExpandedRequirements.add(parent.id);
      closeDialog();
      formElement.reset();
      clearEntityFiles('requirement');
      await loadCore(true);
    } catch (_) {
      $('#formError').textContent = t('createFailed');
    } finally {
      submit.disabled = false;
    }
  };

  const baseOpenResourceDialog = openResourceDialog;
  const baseResourceSubmit = $('#resourceForm').onsubmit;
  let editingRepositoryID = null;
  const baseCloseResourceDialog = closeResourceDialog;
  closeResourceDialog = function enhancedCloseResourceDialog() {
    editingRepositoryID = null;
    baseCloseResourceDialog();
  };
  resourceConfigs.repository.fields = [
    ['name', 'repositoryName', 'text', true],
    ['local_path', 'repositoryLocalPath', 'text', true],
    ['owner_id', 'repositoryOwner', 'text', false]
  ];
  translations.zh.createRepository = translations.zh.createProject;
  translations.en.createRepository = translations.en.createProject;
  openResourceDialog = function enhancedResourceDialog(kind) {
    if (kind === 'bug') {
      showDialog('bug');
      return;
    }
    baseOpenResourceDialog(kind);
    if (kind !== 'repository') return;
    const localPath = $('#resourceFields input[name="local_path"]');
    const owner = $('#resourceFields input[name="owner_id"]');
    const existing = editingRepositoryID ? repositories.find(item => item.id === editingRepositoryID) : null;
    const isAbsoluteLocalPath = value => /^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(String(value || '').trim());
    let setOwner = () => {};
    let setPathDisplay = () => {};
    if (localPath) localPath.placeholder = t('repositoryLocalPath');
    if (owner) {
      const picker = document.createElement('div');
      picker.className = 'resource-owner-picker';
      const hidden = document.createElement('input');
      hidden.type = 'hidden';
      hidden.name = 'owner_id';
      const trigger = document.createElement('button');
      trigger.type = 'button';
      trigger.className = 'resource-owner-trigger';
      trigger.setAttribute('aria-haspopup', 'listbox');
      trigger.setAttribute('aria-expanded', 'false');
      const menu = document.createElement('div');
      menu.className = 'resource-owner-menu';
      menu.setAttribute('role', 'listbox');
      const initials = item => String(item.display_name || item.username || '?').trim().slice(0, 1).toUpperCase();
      const label = item => `${item.display_name || item.username || item.id} · ${item.username || item.id}`;
      const setTrigger = item => {
        hidden.value = item?.id || '';
        trigger.innerHTML = item
          ? `<span class="resource-owner-avatar">${escapeHTML(initials(item))}</span><span class="resource-owner-copy"><strong>${escapeHTML(item.display_name || item.username || item.id)}</strong><small>${escapeHTML(item.username || item.id)}</small></span><span class="resource-owner-caret" aria-hidden="true">⌄</span>`
          : `<span class="resource-owner-placeholder">${escapeHTML(t('repositoryOwnerChoose'))}</span><span class="resource-owner-caret" aria-hidden="true">⌄</span>`;
      };
      setOwner = setTrigger;
      const closeMenu = () => { picker.classList.remove('open'); trigger.setAttribute('aria-expanded', 'false'); };
      menu.innerHTML = directory.length
        ? directory.map(item => `<button type="button" class="resource-owner-option" role="option" data-owner-id="${escapeHTML(item.id)}"><span class="resource-owner-avatar">${escapeHTML(initials(item))}</span><span class="resource-owner-copy"><strong>${escapeHTML(item.display_name || item.username || item.id)}</strong><small>${escapeHTML(label(item))}</small></span></button>`).join('')
        : `<span class="resource-owner-empty">${escapeHTML(t('repositoryOwnerEmpty'))}</span>`;
      menu.querySelectorAll('[data-owner-id]').forEach(option => {
        option.onclick = () => {
          const item = directory.find(candidate => candidate.id === option.dataset.ownerId);
          setTrigger(item);
          closeMenu();
        };
      });
      trigger.onclick = () => {
        const open = picker.classList.toggle('open');
        trigger.setAttribute('aria-expanded', String(open));
      };
      picker.append(hidden, trigger, menu);
      owner.replaceWith(picker);
      setTrigger(existing ? directory.find(item => item.id === existing.owner_id) : null);
    }
    if (localPath) {
      localPath.required = true;
      localPath.classList.add('resource-path-value');
      localPath.setAttribute('aria-describedby', 'repositoryPathHelp');
      localPath.addEventListener('input', () => localPath.setCustomValidity(''));
      const pathLabel = localPath.parentElement;
      if (pathLabel) {
        const picker = document.createElement('div');
        picker.className = 'resource-path-picker';
        const choose = document.createElement('button');
        choose.type = 'button';
        choose.className = 'secondary resource-path-button';
        choose.innerHTML = `<span aria-hidden="true">⌂</span><span>${escapeHTML(t('repositoryChooseFolder'))}</span>`;
        const selected = document.createElement('small');
        selected.className = 'resource-path-selected';
        const input = document.createElement('input');
        input.type = 'file';
        input.multiple = true;
        input.setAttribute('webkitdirectory', '');
        input.setAttribute('directory', '');
        input.className = 'sr-only';
        input.setAttribute('aria-label', t('repositoryChooseFolder'));
        setPathDisplay = path => {
          const value = String(path || '').trim();
          selected.textContent = value ? `${t('repositoryFolderChosen')}: ${value.split(/[\\/]/).filter(Boolean).pop() || value}` : '';
          selected.hidden = !value;
        };
        const updatePath = path => {
          if (!path) return false;
          if (!isAbsoluteLocalPath(path)) {
            selected.textContent = t('repositoryPathPickerUnavailable');
            selected.hidden = false;
            localPath.setCustomValidity(t('repositoryPathAbsoluteRequired'));
            return false;
          }
          localPath.setCustomValidity('');
          localPath.value = path;
          setPathDisplay(path);
          return true;
        };
        const updateFilesPath = files => {
          const first = files?.[0];
          if (!first) return;
          const nativePath = first.path || first.filePath;
          if (nativePath && updatePath(nativePath)) return;
          selected.textContent = t('repositoryPathPickerUnavailable');
          selected.hidden = false;
          localPath.setCustomValidity(t('repositoryPathAbsoluteRequired'));
        };
        choose.onclick = async () => {
          if (window.adroNative?.chooseDirectory) {
            try {
              const selected = await window.adroNative.chooseDirectory();
              const path = typeof selected === 'string' ? selected : selected?.path;
              if (path) {
                updatePath(path);
                return;
              }
            } catch (_) {}
          }
          if (window.showDirectoryPicker) {
            try {
              const handle = await window.showDirectoryPicker();
              updatePath(handle.path || handle.filePath || handle.name);
              return;
            } catch (_) {}
          }
          input.click();
        };
        input.onchange = () => updateFilesPath(input.files);
        picker.append(choose, input);
        pathLabel.append(picker, selected, Object.assign(document.createElement('small'), {className: 'form-help', textContent: t('repositoryPathAbsoluteRequired')}), Object.assign(document.createElement('small'), {className: 'form-help', textContent: t('repositoryPathFallback')}));
      }
    }
    const hint = document.createElement('small');
    hint.className = 'form-help resource-source-help';
    hint.id = 'repositoryPathHelp';
    hint.textContent = t('repositorySourceHelp');
    $('#resourceFields').prepend(hint);
    if (existing) {
      $('#resourceFields input[name="name"]').value = existing.canonical_name || '';
      if (localPath) {
        localPath.value = existing.metadata?.local_path || '';
        setPathDisplay(localPath.value);
      }
      setOwner(directory.find(item => item.id === existing.owner_id));
    }
  };

  function openRepositoryEditor(repository) {
    editingRepositoryID = repository?.id || null;
    openResourceDialog('repository');
    $('#resourceDialogTitle').textContent = t('repositoryEdit');
  }

  $('#resourceForm').onsubmit = async event => {
    if (resourceDialogKind !== 'repository') {
      return baseResourceSubmit(event);
    }
    event.preventDefault();
    const form = event.currentTarget;
    const values = new FormData(form);
    const name = String(values.get('name') || '').trim();
    const localPath = String(values.get('local_path') || '').trim();
    const ownerID = String(values.get('owner_id') || '').trim();
    $('#resourceFormError').textContent = '';
    if (!name || !localPath || !/^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(localPath)) {
      $('#resourceFormError').textContent = t('resourceSaveFailed');
      return;
    }
    const existing = editingRepositoryID ? repositories.find(item => item.id === editingRepositoryID) : null;
    const metadata = {...(existing?.metadata || {})};
    metadata.local_path = localPath;
    if (ownerID) metadata.owner_id = ownerID;
    const body = {
      workspace_id: 'local', canonical_name: name, owner_id: ownerID,
      provider: 'local', metadata
    };
    const submit = form.querySelector('button[type="submit"]');
    if (submit) submit.disabled = true;
    try {
      const saved = await api(existing ? `/api/v1/repositories/${encodeURIComponent(existing.id)}` : '/api/v1/repositories', {method: existing ? 'PATCH' : 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(body)});
      if (saved) repositories = [saved, ...repositories.filter(item => item.id !== saved.id)];
      if (saved?.id) {
        try {
          const indexed = await api(`/api/v1/repositories/${encodeURIComponent(saved.id)}/index`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': `repository-index:${saved.id}:${idempotencyKey()}`}, body: JSON.stringify({commit: 'working-tree'})});
          if (indexed) repositories = [indexed, ...repositories.filter(item => item.id !== indexed.id)];
        } catch (_) {
          // The project remains saved; the next refresh can retry indexing.
        }
      }
      closeResourceDialog();
      form.reset();
      editingRepositoryID = null;
      await loadCore(true);
    } catch (_) {
      $('#resourceFormError').textContent = t('resourceSaveFailed');
    } finally {
      if (submit) submit.disabled = false;
    }
  };

  function repositoryStatus(item) {
    const ready = item.index_status === 'ready';
    return {label: localizedResourceStatus('repository', item.index_status), help: ready ? t('repositoryReadyHelp') : t('repositoryIndexHelp'), className: ready ? 'good' : 'active'};
  }

  const repositoryLanguageGroups = {
    javascript: 'c', typescript: 'c', jsx: 'c', tsx: 'c', java: 'c', kotlin: 'c', swift: 'c', go: 'c', rust: 'c', c: 'c', cpp: 'c', 'c++': 'c', csharp: 'c', 'c#': 'c', php: 'c',
    python: 'hash', py: 'hash', ruby: 'hash', shell: 'hash', bash: 'hash', zsh: 'hash', yaml: 'hash', yml: 'hash', toml: 'hash', ini: 'hash', dockerfile: 'hash',
    sql: 'sql', html: 'html', xml: 'html', vue: 'html', css: 'css', scss: 'css', less: 'css', markdown: 'markdown', md: 'markdown', json: 'json'
  };
  const repositoryKeywords = {
    c: 'as|async|await|break|case|catch|class|const|continue|default|defer|else|enum|export|extends|false|fn|for|from|func|go|if|implements|import|in|interface|let|match|new|null|package|private|public|range|return|self|static|struct|super|switch|this|throw|trait|true|try|type|var|while|yield',
    hash: 'and|as|assert|async|await|class|def|do|elif|else|end|except|false|for|from|function|if|import|in|is|lambda|module|next|nil|not|or|pass|print|raise|range|require|return|self|then|true|unless|until|var|when|while|with|yield',
    sql: 'alter|and|as|asc|begin|between|by|case|create|delete|desc|drop|else|end|from|group|having|in|insert|into|is|join|like|limit|not|null|on|or|order|select|set|table|then|true|union|update|values|when|where|with',
    json: 'true|false|null',
    html: 'DOCTYPE|class|id|href|lang|name|rel|src|style|title|type',
    css: 'and|as|class|else|for|from|function|if|import|important|media|not|or|supports|var|while',
    markdown: 'TODO|FIXME|NOTE'
  };

  function repositoryGrammar(language) {
    const normalized = String(language || '').toLowerCase().replace(/^text\//, '');
    const group = repositoryLanguageGroups[normalized] || 'plain';
    return {group, keywords: new Set((repositoryKeywords[group] || '').split('|').filter(Boolean))};
  }

  function highlightRepositoryLine(rawLine, grammar, state) {
    const line = String(rawLine || '');
    if (grammar.group === 'markdown') {
      const heading = line.match(/^(\s*#{1,6})(\s+)(.*)$/);
      if (heading) return `<span class="code-heading">${escapeHTML(heading[1])}</span>${escapeHTML(heading[2])}<span class="code-heading">${escapeHTML(heading[3])}</span>`;
    }
    const commentTokens = grammar.group === 'sql' ? ['--'] : grammar.group === 'html' ? ['<!--'] : grammar.group === 'css' ? ['/*'] : grammar.group === 'hash' ? ['#'] : grammar.group === 'c' ? ['//', '/*'] : [];
    let html = '';
    let index = 0;
    const add = (className, value) => { html += className ? `<span class="${className}">${escapeHTML(value)}</span>` : escapeHTML(value); };
    while (index < line.length) {
      if (state.blockComment) {
        const end = line.indexOf(state.blockComment.end, index);
        if (end < 0) { add('code-comment', line.slice(index)); return html; }
        add('code-comment', line.slice(index, end + state.blockComment.end.length));
        index = end + state.blockComment.end.length;
        state.blockComment = null;
        continue;
      }
      const comment = commentTokens.find(token => line.startsWith(token, index));
      if (comment) {
        if (comment === '/*' || comment === '<!--') {
          const endToken = comment === '/*' ? '*/' : '-->';
          const end = line.indexOf(endToken, index + comment.length);
          if (end < 0) { add('code-comment', line.slice(index)); state.blockComment = {end: endToken}; return html; }
          add('code-comment', line.slice(index, end + endToken.length));
          index = end + endToken.length;
        } else { add('code-comment', line.slice(index)); return html; }
        continue;
      }
      const quote = line[index];
      if (quote === '"' || quote === "'" || quote === '`') {
        let end = index + 1;
        while (end < line.length) {
          if (line[end] === '\\') { end += 2; continue; }
          if (line[end] === quote) { end += 1; break; }
          end += 1;
        }
        add('code-string', line.slice(index, end));
        index = end;
        continue;
      }
      const number = line.slice(index).match(/^(?:0x[\da-f]+|\d+(?:\.\d+)?(?:e[+-]?\d+)?)/i);
      if (number) { add('code-number', number[0]); index += number[0].length; continue; }
      const word = line.slice(index).match(/^[A-Za-z_$][\w$-]*/);
      if (word) {
        const value = word[0];
        const rest = line.slice(index + value.length);
        if (grammar.keywords.has(value)) add('code-keyword', value);
        else if (/^\s*\(/.test(rest)) add('code-function', value);
        else add('', value);
        index += value.length;
        continue;
      }
      if (/^[{}()[\];,.:+*%!=<>/?&|~-]/.test(line.slice(index))) { add('code-operator', line[index]); } else add('', line[index]);
      index += 1;
    }
    return html || ' ';
  }

  function highlightRepositoryCode(content, language) {
    const grammar = repositoryGrammar(language);
    const state = {blockComment: null};
    return String(content || '').split('\n').map(line => `<span class="code-line">${highlightRepositoryLine(line, grammar, state)}</span>`).join('');
  }

  async function openRepositoryBrowser(repository) {
    const dialog = document.createElement('dialog');
    dialog.className = 'repository-browser-dialog';
    dialog.innerHTML = `<div class="dialog-head"><div><p class="dialog-kicker">PROJECT / BROWSER</p><h2>${escapeHTML(t('repositoryBrowseTitle'))}: ${escapeHTML(repository.canonical_name || '-')}</h2></div><button class="dialog-close" type="button" data-close-browser aria-label="${escapeHTML(t('close'))}"><span aria-hidden="true">×</span></button></div><div class="repository-browser-status" role="status"></div><div class="repository-browser-grid"><aside class="repository-tree" aria-label="${escapeHTML(t('repositoryBrowseTitle'))}"></aside><section class="repository-file-panel"><header><strong data-browser-file-name>${escapeHTML(t('repositoryBrowseTitle'))}</strong><small data-browser-file-meta></small></header><pre class="repository-code"><code data-browser-code>${escapeHTML(t('repositoryBrowseTitle'))}</code></pre></section></div>`;
    document.body.append(dialog);
    const status = dialog.querySelector('.repository-browser-status');
    const tree = dialog.querySelector('.repository-tree');
    const fileName = dialog.querySelector('[data-browser-file-name]');
    const fileMeta = dialog.querySelector('[data-browser-file-meta]');
    const code = dialog.querySelector('[data-browser-code]');
    const loadPath = async path => {
      try {
        const result = await api(`/api/v1/repositories/${encodeURIComponent(repository.id)}/files${path ? `?path=${encodeURIComponent(path)}` : ''}`);
        if (result.available === false) {
          status.textContent = result.reason || t('repositoryRemoteUnavailable');
          tree.innerHTML = '';
          code.textContent = t('repositoryRemoteUnavailable');
          return;
        }
        status.textContent = result.kind === 'directory' ? result.path === '.' ? repository.metadata?.local_path || '' : result.path : '';
        if (result.kind === 'directory') {
          tree.innerHTML = `<button type="button" class="repository-tree-root" data-tree-path="">${escapeHTML(repository.canonical_name || '.')}</button>${result.items?.length ? result.items.map(item => `<button type="button" class="repository-tree-item ${item.kind}" data-tree-path="${escapeHTML(item.path)}"><span aria-hidden="true">${item.kind === 'directory' ? '▸' : '·'}</span>${escapeHTML(item.name)}</button>`).join('') : `<p class="repository-tree-empty">${escapeHTML(t('repositoryBrowseEmpty'))}</p>`}`;
          tree.querySelectorAll('[data-tree-path]').forEach(button => { button.onclick = () => loadPath(button.dataset.treePath); });
          fileName.textContent = path || repository.canonical_name || '-';
          fileMeta.textContent = '';
          code.textContent = t('repositoryBrowseTitle');
          return;
        }
        fileName.textContent = result.name || path;
        fileMeta.textContent = `${result.path} · ${result.language} · ${result.size} B`;
        if (result.binary) code.textContent = t('repositoryBinary');
        else code.innerHTML = highlightRepositoryCode(result.content || '', result.language);
        if (result.truncated) status.textContent = t('repositoryTruncated');
      } catch (error) {
        status.textContent = error.detail || t('repositoryLoadFailed');
        tree.innerHTML = '';
        code.textContent = status.textContent;
      }
    };
    dialog.querySelector('[data-close-browser]').onclick = () => { dialog.close(); dialog.remove(); };
    dialog.addEventListener('click', event => { if (event.target === dialog) { dialog.close(); dialog.remove(); } });
    dialog.showModal();
    await loadPath('');
  }

  renderRepositories = function enhancedRepositories() {
    const rows = repositories.map(item => {
      const source = item.metadata?.local_path || '-';
      const owner = item.owner_id ? userLabel(item.owner_id) : '-';
      const state = repositoryStatus(item);
      const updated = item.updated_at ? new Date(item.updated_at).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') : '-';
      return `<tr data-repository-id="${escapeHTML(item.id)}"><td class="mono">${escapeHTML(item.id)}</td><td><strong>${escapeHTML(item.canonical_name || '-')}</strong><div class="muted repository-source" title="${escapeHTML(source)}">${escapeHTML(source)}</div><small class="repository-source-kind">${escapeHTML(t('localProject'))}</small></td><td class="muted">${escapeHTML(owner)}</td><td><span class="status ${state.className}" title="${escapeHTML(state.help)}">${escapeHTML(state.label)}</span><small class="repository-status-help">${escapeHTML(state.help)}</small></td><td class="muted">${escapeHTML(updated)}</td><td><div class="row-actions"><button type="button" class="action-button" data-repository-action="edit" data-repository-id="${escapeHTML(item.id)}">${escapeHTML(t('repositoryEdit'))}</button><button type="button" class="action-button" data-repository-action="browse" data-repository-id="${escapeHTML(item.id)}">${escapeHTML(t('repositoryBrowse'))}</button><button type="button" class="action-button danger" data-repository-action="delete" data-repository-id="${escapeHTML(item.id)}">${escapeHTML(t('repositoryDelete'))}</button></div></td></tr>`;
    });
    return `<div class="view-stack"><div class="menu-intro"><strong>${escapeHTML(t('menuOwned'))}</strong><span>${escapeHTML(t('menuActionHint'))}</span></div>${genericTable(t('repositoriesTitle'), [t('key'), t('name'), t('repositoryOwner'), t('status'), t('updated'), t('actions')], rows, t('noItems'))}</div>`;
  };

  renderBugs = function enhancedBugTable() {
    const rows = bugs.map(item => `<tr data-bug-id="${escapeHTML(item.id)}" tabindex="0"><td class="mono">${escapeHTML((item.id || '').slice(0, 10))}</td><td class="title-cell">${escapeHTML(item.title || '-')}</td><td><span class="status ${statusClass(item.status)}">${escapeHTML(statusLabel(item.status))}</span></td><td>${escapeHTML(repositoryLabel(item.repository_id))}</td><td class="muted">${escapeHTML(requirementLabel(item.requirement_id))}</td><td class="muted">${escapeHTML(userLabel(item.assignee_member_id))}</td><td><div class="row-actions">${item.status === 'OPEN' ? actionButton(item.id, 'bug', 'repair', 'accent') : ''}${item.status === 'HUMAN_TRIAGE_REQUIRED' ? actionButton(item.id, 'bug', 'triage') : ''}${item.status === 'REPAIRING' ? actionButton(item.id, 'bug', 'verify', 'accent') : ''}</div></td></tr>`);
    return `<div class="view-stack"><div class="menu-intro"><strong>${escapeHTML(t('menuOwned'))}</strong><span>${escapeHTML(t('menuActionHint'))}</span></div><div class="view-grid">${summaryCard(t('openBugs'), bugs.filter(item => item.status === 'OPEN').length, t('needsAttention'))}${summaryCard(t('repairingTitle'), bugs.filter(item => item.status === 'REPAIRING').length, t('repairing'))}${summaryCard(t('escalatedTitle'), bugs.filter(item => item.status === 'HUMAN_TRIAGE_REQUIRED').length, t('escalated'))}</div>${genericTable(t('bugs'), [t('key'), t('title'), t('status'), t('project'), t('requirementRelation'), t('executorColumn'), t('actions')], rows, t('noBugs'))}</div>`;
  };

  function deliveryPlanFor(requirementID) {
    return nativePlans.slice().reverse().find(item => item.requirement_id === requirementID) || null;
  }

  function deliveryPlanLabel(plan) {
    if (!plan) return {text: t('deliveryPlanNone'), className: 'warn'};
    const status = executionPlanStatus(plan, executionTimelineCache.get(plan.id)?.projection);
    if (['failed', 'cancelled', 'timed_out', 'blocked'].includes(status)) return {text: t('deliveryPlanFailed'), className: 'bad'};
    if (['running', 'ready', 'waiting'].includes(status)) return {text: status === 'running' ? t('deliveryPlanRunning') : t('deliveryPlanReady'), className: 'active'};
    return {text: status, className: executionStatusClass(status)};
  }

  function deliveryBugIsOpen(item) {
    return !['VERIFIED', 'CLOSED', 'RESOLVED', 'ACCEPTED', 'RELEASED'].includes(String(item.status || '').toUpperCase());
  }

  function deliveryRequirementMatches(requirement, children) {
    const term = deliverySearchTerm.trim().toLowerCase();
    if (!term) return true;
    const requirementText = `${requirement.key || ''} ${requirement.title || ''} ${requirement.description || ''} ${(requirement.assignee_member_ids || []).join(' ')}`.toLowerCase();
    return requirementText.includes(term) || children.some(item => `${item.id || ''} ${item.title || ''} ${item.actual || ''} ${item.steps_to_reproduce || ''}`.toLowerCase().includes(term));
  }

  function deliveryBugMatches(item) {
    const term = deliverySearchTerm.trim().toLowerCase();
    const statusMatches = !deliveryFilterStatus || String(item.status || '') === deliveryFilterStatus;
    const textMatches = !term || `${item.id || ''} ${item.title || ''} ${item.actual || ''} ${item.steps_to_reproduce || ''}`.toLowerCase().includes(term);
    return statusMatches && textMatches;
  }

  function deliveryPlanTarget(plan) {
    if (!plan?.selected_ref?.id) return '-';
    const version = plan.selected_ref.version || plan.selected_ref.revision;
    return `${plan.selected_ref.id}${version ? `@${version}` : ''}`;
  }

  function deliveryRequirementRow(requirement, allChildren, expanded) {
    const plan = deliveryPlanFor(requirement.id);
    const planLabel = deliveryPlanLabel(plan);
    const total = allChildren.length;
    const open = allChildren.filter(deliveryBugIsOpen).length;
    const owner = requirement.assignee_member_ids?.[0] ? userLabel(requirement.assignee_member_ids[0]) : '-';
    const team = deliveryPlanTarget(plan);
    const toggleLabel = expanded ? t('deliveryCollapse') : t('deliveryExpand');
    return `<tr class="delivery-parent-row" data-delivery-requirement-id="${escapeHTML(requirement.id)}" tabindex="0"><td><span class="delivery-type-mark requirement">REQ</span><span class="mono delivery-key">${escapeHTML(requirement.key || requirement.id || '-')}</span></td><td class="title-cell"><strong>${escapeHTML(requirement.title || '-')}</strong><small>${escapeHTML(t('deliveryContextSummary'))}</small></td><td><span class="status ${statusClass(requirement.status)}">${escapeHTML(statusLabel(requirement.status))}</span></td><td class="muted">${escapeHTML(owner)}</td><td class="muted delivery-team" title="${escapeHTML(team)}">${escapeHTML(team)}</td><td><span class="status ${planLabel.className}">${escapeHTML(planLabel.text)}</span></td><td><div class="delivery-bug-summary"><strong>${escapeHTML(String(total))}</strong><span>${escapeHTML(t('deliveryBugs'))}</span><small>${escapeHTML(t('deliveryBugSummary').replace('{total}', String(total)).replace('{open}', String(open)))}</small></div><div class="row-actions"><button class="action-button accent" type="button" data-delivery-toggle="${escapeHTML(requirement.id)}" aria-expanded="${String(expanded)}" title="${escapeHTML(toggleLabel)}">${escapeHTML(toggleLabel)}</button><button class="action-button" type="button" data-delivery-add-bug="${escapeHTML(requirement.id)}" title="${escapeHTML(t('deliveryAddBug'))}">＋</button></div></td></tr>`;
  }

  function deliveryBugRow(item, requirement) {
    const owner = item.assignee_member_id ? userLabel(item.assignee_member_id) : requirement?.assignee_member_ids?.[0] ? userLabel(requirement.assignee_member_ids[0]) : '-';
    const plan = requirement ? deliveryPlanFor(requirement.id) : null;
    const team = deliveryPlanTarget(plan);
    const actionMarkup = item.status === 'OPEN' ? actionButton(item.id, 'bug', 'repair', 'accent') : item.status === 'HUMAN_TRIAGE_REQUIRED' ? actionButton(item.id, 'bug', 'triage') : item.status === 'REPAIRING' ? actionButton(item.id, 'bug', 'verify', 'accent') : '';
    return `<tr class="delivery-bug-row" data-bug-id="${escapeHTML(item.id)}" tabindex="0"><td><span class="delivery-type-mark bug">BUG</span><span class="mono delivery-key">${escapeHTML((item.id || '').slice(0, 10))}</span></td><td class="title-cell delivery-child-title"><span aria-hidden="true">↳</span><strong>${escapeHTML(item.title || '-')}</strong><small>${escapeHTML(requirement ? requirementLabel(requirement.id) : t('deliveryUnlinkedBugs'))}</small></td><td><span class="status ${statusClass(item.status)}">${escapeHTML(statusLabel(item.status))}</span></td><td class="muted">${escapeHTML(owner)}</td><td class="muted delivery-team" title="${escapeHTML(team)}">${escapeHTML(team)}</td><td><span class="status ${deliveryBugIsOpen(item) ? 'warn' : 'good'}">${escapeHTML(deliveryBugIsOpen(item) ? t('open') : t('verified'))}</span></td><td><div class="row-actions">${actionMarkup}</div></td></tr>`;
  }

  function deliveryStatusOptions() {
    const statuses = [...new Set([...requirements.map(item => item.status), ...bugs.map(item => item.status)].filter(Boolean))];
    return statuses.sort().map(status => `<option value="${escapeHTML(status)}" ${status === deliveryFilterStatus ? 'selected' : ''}>${escapeHTML(statusLabel(status))}</option>`).join('');
  }

  function renderDelivery() {
    const term = deliverySearchTerm.trim().toLowerCase();
    const grouped = new Map(requirements.map(item => [item.id, []]));
    const unlinked = [];
    for (const bug of bugs) {
      if (grouped.has(bug.requirement_id)) grouped.get(bug.requirement_id).push(bug);
      else unlinked.push(bug);
    }
    const rows = [];
    for (const requirement of requirements) {
      const allChildren = grouped.get(requirement.id) || [];
      const matchingChildren = allChildren.filter(deliveryBugMatches);
      const requirementStatusMatches = !deliveryFilterStatus || requirement.status === deliveryFilterStatus;
      const statusMatches = requirementStatusMatches || matchingChildren.length > 0;
      const kindMatches = deliveryFilterKind !== 'bug' || matchingChildren.length > 0;
      const searchMatches = deliveryRequirementMatches(requirement, allChildren);
      if (!statusMatches || !kindMatches || !searchMatches) continue;
      const children = deliveryFilterKind === 'requirement' ? [] : (deliverySearchTerm || deliveryFilterStatus ? matchingChildren : allChildren);
      const expanded = deliveryExpandedRequirements.has(requirement.id);
      rows.push(deliveryRequirementRow(requirement, allChildren, expanded));
      if (expanded || deliveryFilterKind === 'bug' || Boolean(term)) rows.push(...children.map(item => deliveryBugRow(item, requirement)));
    }
    const visibleUnlinked = unlinked.filter(deliveryBugMatches);
    const unlinkedMarkup = (deliveryFilterKind !== 'requirement' && visibleUnlinked.length) ? `<section class="panel delivery-unlinked"><div class="panel-head"><div><h2>${escapeHTML(t('deliveryUnlinkedBugs'))}</h2><small>${escapeHTML(t('deliveryUnlinkedHint'))}</small></div><span class="status warn">${escapeHTML(String(visibleUnlinked.length))}</span></div><div class="table-scroll"><table class="delivery-table"><thead><tr><th>${escapeHTML(t('key'))}</th><th>${escapeHTML(t('title'))}</th><th>${escapeHTML(t('status'))}</th><th>${escapeHTML(t('deliveryOwner'))}</th><th>${escapeHTML(t('deliveryTeam'))}</th><th>${escapeHTML(t('deliveryPlan'))}</th><th>${escapeHTML(t('actions'))}</th></tr></thead><tbody>${visibleUnlinked.map(item => deliveryBugRow(item, null)).join('')}</tbody></table></div></section>` : '';
    const openBugCount = bugs.filter(deliveryBugIsOpen).length;
    const plannedCount = requirements.filter(item => deliveryPlanFor(item.id)).length;
    const visibleRequirementCount = requirements.filter(requirement => {
      const allChildren = grouped.get(requirement.id) || [];
      return (!deliveryFilterStatus || requirement.status === deliveryFilterStatus || allChildren.some(deliveryBugMatches))
        && (deliveryFilterKind !== 'bug' || allChildren.some(deliveryBugMatches))
        && deliveryRequirementMatches(requirement, allChildren);
    }).length;
    return `<div class="view-stack delivery-view"><div class="delivery-intro"><div><span class="menu-kicker">DELIVERY GRAPH / ONE CONTEXT</span><p>${escapeHTML(t('deliverySubtitle'))}</p></div><div class="delivery-intro-stats"><span><strong>${escapeHTML(String(requirements.length + bugs.length))}</strong>${escapeHTML(t('deliveryTotal'))}</span><span><strong>${escapeHTML(String(openBugCount))}</strong>${escapeHTML(t('deliveryUnresolved'))}</span><span><strong>${escapeHTML(String(plannedCount))}</strong>${escapeHTML(t('deliveryPlan'))}</span></div></div><div class="view-grid delivery-summary-grid">${summaryCard(t('deliveryRequirements'), requirements.length, t('deliveryContextSummary'))}${summaryCard(t('deliveryBugs'), bugs.length, t('deliveryBugSummary').replace('{total}', String(bugs.length)).replace('{open}', String(openBugCount)))}${summaryCard(t('deliveryPlan'), plannedCount, t('deliveryPlanSummary'))}</div><section class="panel delivery-board"><div class="panel-head"><div><h2>${escapeHTML(t('deliveryTitle'))}</h2><small>${escapeHTML(t('deliveryDevelopmentSummary'))}</small></div><span class="status active">${escapeHTML(String(visibleRequirementCount))}</span></div><div class="toolbar delivery-toolbar"><input id="deliverySearch" type="search" value="${escapeHTML(deliverySearchTerm)}" placeholder="${escapeHTML(t('deliverySearch'))}" aria-label="${escapeHTML(t('deliverySearch'))}"><select id="deliveryKindFilter" aria-label="${escapeHTML(t('deliveryTypeLabel'))}"><option value="all" ${deliveryFilterKind === 'all' ? 'selected' : ''}>${escapeHTML(t('deliveryAll'))}</option><option value="requirement" ${deliveryFilterKind === 'requirement' ? 'selected' : ''}>${escapeHTML(t('deliveryRequirementFilter'))}</option><option value="bug" ${deliveryFilterKind === 'bug' ? 'selected' : ''}>${escapeHTML(t('deliveryBugFilter'))}</option></select><select id="deliveryStatusSelect" aria-label="${escapeHTML(t('deliveryStatusFilter'))}"><option value="">${escapeHTML(t('deliveryStatusAll'))}</option>${deliveryStatusOptions()}</select></div><div class="table-scroll"><table class="delivery-table"><thead><tr><th>${escapeHTML(t('deliveryTypeLabel'))}</th><th>${escapeHTML(t('title'))}</th><th>${escapeHTML(t('status'))}</th><th>${escapeHTML(t('deliveryOwner'))}</th><th>${escapeHTML(t('deliveryTeam'))}</th><th>${escapeHTML(t('deliveryPlan'))}</th><th>${escapeHTML(t('deliveryRelatedBugs'))}</th></tr></thead><tbody>${rows.length ? rows.join('') : `<tr><td colspan="7" class="empty">${escapeHTML(t('noItems'))}</td></tr>`}</tbody></table></div></section>${unlinkedMarkup}</div>`;
  }

  function bindDeliveryView() {
    const root = $('#appView');
    if (!root) return;
    const rerender = () => { root.innerHTML = renderDelivery(); bindDeliveryView(); };
    const search = $('#deliverySearch');
    if (search) search.oninput = event => { deliverySearchTerm = event.currentTarget.value; rerender(); const next = $('#deliverySearch'); next?.focus(); next?.setSelectionRange(deliverySearchTerm.length, deliverySearchTerm.length); };
    const kind = $('#deliveryKindFilter');
    if (kind) kind.onchange = event => { deliveryFilterKind = event.currentTarget.value; rerender(); };
    const status = $('#deliveryStatusSelect');
    if (status) status.onchange = event => { deliveryFilterStatus = event.currentTarget.value; rerender(); };
    root.querySelectorAll('[data-delivery-toggle]').forEach(button => {
      button.onclick = event => { event.stopPropagation(); const id = button.dataset.deliveryToggle; if (deliveryExpandedRequirements.has(id)) deliveryExpandedRequirements.delete(id); else deliveryExpandedRequirements.add(id); rerender(); };
    });
    root.querySelectorAll('[data-delivery-add-bug]').forEach(button => {
      button.onclick = event => { event.stopPropagation(); showDialog('bug', button.dataset.deliveryAddBug); };
    });
    root.querySelectorAll('[data-delivery-requirement-id]').forEach(row => {
      row.onclick = event => { if (event.target.closest('button')) return; openRequirement(row.dataset.deliveryRequirementId); };
      row.onkeydown = event => { if ((event.key === 'Enter' || event.key === ' ') && !event.target.closest('button')) { event.preventDefault(); openRequirement(row.dataset.deliveryRequirementId); } };
    });
    root.querySelectorAll('[data-resource-action]').forEach(button => {
      button.onclick = event => { event.stopPropagation(); applyResourceAction(button.dataset.resourceKind, button.dataset.resourceId, button.dataset.resourceAction); };
    });
  }

  function renderDeliveryDetailCanvas(requirement, detail = {}) {
    const children = bugs.filter(item => item.requirement_id === requirement.id);
    const openCount = children.filter(deliveryBugIsOpen).length;
    const plan = deliveryPlanFor(requirement.id);
    const planLabel = deliveryPlanLabel(plan);
    const cache = plan ? executionTimelineCache.get(plan.id) || {} : {};
    const statusText = plan ? planLabel.text : t('deliveryPlanNone');
    const target = deliveryPlanTarget(plan);
    const workItemIDs = (detail.work_items || []).map(item => item.id).filter(Boolean);
    const validation = ['TEST_FAILED', 'AUTO_REPAIRING', 'BLOCKED'].includes(String(requirement.status || '').toUpperCase()) ? t('deliveryPlanFailed') : ['ACCEPTED', 'RELEASED'].includes(String(requirement.status || '').toUpperCase()) ? t('verified') : t('deliveryValidationPending');
    return `<section class="delivery-detail-canvas"><header class="delivery-canvas-head"><div><span class="menu-kicker">DELIVERY CANVAS / ${escapeHTML(requirement.key || '')}</span><h3>${escapeHTML(t('deliveryCanvas'))}</h3><p>${escapeHTML(t('deliveryContextSummary'))}</p></div><div class="delivery-canvas-head-meta"><span class="status ${statusClass(requirement.status)}">${escapeHTML(statusLabel(requirement.status))}</span><span class="status ${openCount ? 'warn' : 'good'}">${escapeHTML(String(children.length))} ${escapeHTML(t('deliveryBugs'))}</span></div></header><div class="delivery-canvas-grid"><article class="delivery-canvas-stage context"><span class="delivery-stage-index">01</span><h4>${escapeHTML(t('deliveryContext'))}</h4><p>${escapeHTML(requirement.description || '-')}</p><div class="delivery-stage-meta"><span>${escapeHTML(t('deliveryProject'))}<strong>${escapeHTML(requirement.repository_ids?.map(repositoryLabel).join(', ') || '-')}</strong></span><span>${escapeHTML(t('deliveryOwner'))}<strong>${escapeHTML(requirement.assignee_member_ids?.map(userLabel).join(', ') || '-')}</strong></span></div></article><article class="delivery-canvas-stage"><span class="delivery-stage-index">02</span><h4>${escapeHTML(t('deliveryDesign'))}</h4><strong class="delivery-stage-state"><span class="status ${planLabel.className}">${escapeHTML(statusText)}</span></strong><p>${escapeHTML(plan ? t('deliveryPlanSummary') : t('deliveryNoPlanAction'))}</p><div class="delivery-stage-meta"><span>${escapeHTML(t('deliveryTeam'))}<strong title="${escapeHTML(target)}">${escapeHTML(target)}</strong></span><span>${escapeHTML(t('revision'))}<strong>${escapeHTML(String(plan?.revision || plan?.selected_ref?.version || '-'))}</strong></span></div></article><article class="delivery-canvas-stage"><span class="delivery-stage-index">03</span><h4>${escapeHTML(t('deliveryDevelopment'))}</h4><p>${escapeHTML(t('deliveryDevelopmentSummary'))}</p><div class="delivery-stage-actions">${plan ? `<button class="secondary" type="button" data-delivery-open-execution="${escapeHTML(plan.id)}">${escapeHTML(t('deliveryOpenExecution'))}</button>` : `<span class="form-help">${escapeHTML(t('deliveryNoPlanAction'))}</span>`}</div><small class="mono">${escapeHTML(cache.projection?.status || plan?.status || '-')}</small></article><article class="delivery-canvas-stage"><span class="delivery-stage-index">04</span><h4>${escapeHTML(t('deliveryValidation'))}</h4><strong class="delivery-stage-state"><span class="status ${validation === t('verified') ? 'good' : validation === t('deliveryPlanFailed') ? 'bad' : 'warn'}">${escapeHTML(validation)}</span></strong><p>${escapeHTML(t('deliveryValidationSummary'))}</p><div class="delivery-stage-meta"><span>${escapeHTML(t('workItems'))}<strong>${escapeHTML(workItemIDs.join(', ') || '-')}</strong></span><span>${escapeHTML(t('deliveryRelatedBugs'))}<strong>${escapeHTML(`${openCount}/${children.length}`)}</strong></span></div></article></div><section class="delivery-related-bugs"><div class="delivery-related-head"><div><span class="menu-kicker">LINKED DEFECTS</span><h4>${escapeHTML(t('deliveryRelatedBugs'))}</h4></div><button class="action-button accent" type="button" data-delivery-detail-add-bug="${escapeHTML(requirement.id)}">＋ ${escapeHTML(t('deliveryAddBug'))}</button></div>${children.length ? `<div class="delivery-related-list">${children.map(item => `<button type="button" class="delivery-related-item" data-delivery-open-bug="${escapeHTML(item.id)}"><span class="delivery-type-mark bug">BUG</span><span><strong>${escapeHTML(item.title || '-')}</strong><small>${escapeHTML(statusLabel(item.status))}</small></span><span class="status ${statusClass(item.status)}">${escapeHTML(statusLabel(item.status))}</span></button>`).join('')}</div>` : `<p class="delivery-related-empty">${escapeHTML(t('deliveryNoRelatedBugs'))}</p>`}</section></section>`;
  }

  renderAdmin = function enhancedAdmin() {
    const userRows = managedUsers.map(user => `<tr><td><strong>${escapeHTML(user.display_name)}</strong><div class="mono">${escapeHTML(user.username)}</div></td><td><span class="status ${user.role === 'admin' ? 'active' : ''}">${escapeHTML(roleLabel(user.role))}</span></td><td><span class="status ${user.status === 'active' ? 'good' : 'bad'}">${escapeHTML(t(user.status === 'active' ? 'activeAccount' : 'disabledAccount'))}</span></td><td><span class="permission-count">${user.menu_ids.length} / ${menuIDs.length}</span></td><td><button class="action-button" type="button" data-edit-user="${escapeHTML(user.id)}">${escapeHTML(t('edit'))}</button></td></tr>`);
    const usersPanel = `<section class="panel"><div class="panel-head"><h2>${escapeHTML(t('userManagement'))}</h2><small>${managedUsers.length} ${escapeHTML(t('identityCount'))}</small></div><div class="admin-toolbar"><p>${escapeHTML(t('menuPermissionsHelp'))}</p><button class="primary" id="newUser" type="button"><span aria-hidden="true">＋</span>${escapeHTML(t('createUser'))}</button></div><div class="table-scroll"><table><thead><tr><th>${escapeHTML(t('displayName'))}</th><th>${escapeHTML(t('role'))}</th><th>${escapeHTML(t('accountStatus'))}</th><th>${escapeHTML(t('permissionSummary'))}</th><th>${escapeHTML(t('actions'))}</th></tr></thead><tbody>${userRows.length ? userRows.join('') : `<tr><td colspan="5" class="empty">${escapeHTML(t('noItems'))}</td></tr>`}</tbody></table></div></section>`;
    const audit = genericTable(t('auditChain'), [t('key'), t('eventType'), t('source'), t('time')], auditItems.slice(-25).reverse().map(item => `<tr><td class="mono">${escapeHTML(String(item.sequence || '').padStart(4, '0'))}</td><td>${escapeHTML(item.action || '-')}</td><td class="muted">${escapeHTML(item.actor_id || '-')}</td><td class="muted">${escapeHTML(new Date(item.created_at || Date.now()).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US'))}</td></tr>`), t('noEvents'));
    const migration = workspaceMigrationPanel();
    return `<div class="view-stack"><div class="menu-intro"><strong>${escapeHTML(t('accessControl'))}</strong><span>${escapeHTML(t('menuActionHint'))}</span></div>${migration}${usersPanel}${audit}</div>`;
  };

  function workspaceMigrationPanel(onboarding = false) {
    return `<section class="panel workspace-migration ${onboarding ? 'onboarding-migration' : ''}" data-workspace-migration><div class="panel-head"><h2>${escapeHTML(t(onboarding ? 'migrateExisting' : 'workspaceMigration'))}</h2><small data-migration-name>${escapeHTML(workspaceMigrationFile?.name || t('migrationEmpty'))}</small></div><div class="migration-controls">${onboarding ? '' : `<button class="secondary" type="button" data-migration-export><span aria-hidden="true">↓</span>${escapeHTML(t('exportWorkspace'))}</button>`}<label class="secondary migration-file-button"><span aria-hidden="true">↑</span>${escapeHTML(t('chooseBundle'))}<input type="file" accept=".zip,application/zip,application/vnd.adro.workspace+zip" data-migration-file hidden></label><label><span>${escapeHTML(t('conflictMode'))}</span><select data-migration-conflict><option value="rename">${escapeHTML(t('conflictRename'))}</option><option value="skip">${escapeHTML(t('conflictSkip'))}</option><option value="fail">${escapeHTML(t('conflictFail'))}</option></select></label><button class="secondary" type="button" data-migration-preflight disabled>${escapeHTML(t('preflightBundle'))}</button><button class="primary" type="button" data-migration-import disabled>${escapeHTML(t('importWorkspace'))}</button></div><div class="migration-report" data-migration-report role="status"></div></section>`;
  }

  function bindWorkspaceMigration(root = document) {
    root.querySelectorAll('[data-workspace-migration]').forEach(panel => {
      if (panel.dataset.bound === 'true') return;
      panel.dataset.bound = 'true';
      const fileInput = panel.querySelector('[data-migration-file]');
      const preflight = panel.querySelector('[data-migration-preflight]');
      const importButton = panel.querySelector('[data-migration-import]');
      const exportButton = panel.querySelector('[data-migration-export]');
      fileInput.onchange = () => {
        workspaceMigrationFile = fileInput.files?.[0] || null;
        workspaceMigrationReport = null;
        panel.querySelector('[data-migration-name]').textContent = workspaceMigrationFile?.name || t('migrationEmpty');
        preflight.disabled = !workspaceMigrationFile;
        importButton.disabled = true;
        panel.querySelector('[data-migration-report]').textContent = '';
      };
      panel.querySelector('[data-migration-conflict]').onchange = () => { workspaceMigrationReport = null; importButton.disabled = true; };
      preflight.onclick = () => preflightWorkspaceMigration(panel);
      importButton.onclick = () => importWorkspaceMigration(panel);
      if (exportButton) exportButton.onclick = exportWorkspaceMigration;
    });
  }

  async function migrationRequest(action, panel) {
    if (!workspaceMigrationFile) throw new Error('bundle required');
    const policy = panel.querySelector('[data-migration-conflict]').value;
    const response = await fetch(`/api/v1/workspaces/local/migration/${action}?conflict=${encodeURIComponent(policy)}`, {method: 'POST', headers: {'X-Workspace-ID': 'local', ...(action === 'import' ? {'Idempotency-Key': idempotencyKey()} : {})}, credentials: 'include', body: workspaceMigrationFile});
    if (!response.ok) throw new Error(`${response.status}`);
    return response.json();
  }

  async function preflightWorkspaceMigration(panel) {
    const status = panel.querySelector('[data-migration-report]');
    const button = panel.querySelector('[data-migration-preflight]');
    button.disabled = true;
    try {
      workspaceMigrationReport = await migrationRequest('preflight', panel);
      const counts = workspaceMigrationReport.counts || {};
      const total = Object.values(counts).reduce((sum, value) => sum + Number(value || 0), 0);
      status.className = 'migration-report good';
      status.textContent = `${t('migrationReady')} · ${total} ${t('migrationEntities')} · ${String(workspaceMigrationReport.digest || '').slice(0, 12)}`;
      panel.querySelector('[data-migration-import]').disabled = false;
    } catch (_) {
      status.className = 'migration-report bad'; status.textContent = t('migrationFailed'); workspaceMigrationReport = null;
    } finally { button.disabled = !workspaceMigrationFile; }
  }

  async function importWorkspaceMigration(panel) {
    if (!workspaceMigrationReport) return;
    const status = panel.querySelector('[data-migration-report]');
    const button = panel.querySelector('[data-migration-import]');
    button.disabled = true;
    try {
      await migrationRequest('import', panel);
      status.className = 'migration-report good'; status.textContent = t('migrationDone');
      const agentForm = $('#agentForm');
      if (agentForm?.dataset.onboarding === 'true') { delete agentForm.dataset.onboarding; document.body.classList.remove('onboarding-active'); closeAgentDialog(); }
      await loadCore(true);
    } catch (_) { status.className = 'migration-report bad'; status.textContent = t('migrationFailed'); button.disabled = false; }
  }

  async function exportWorkspaceMigration() {
    const response = await fetch('/api/v1/workspaces/local/migration/export', {headers: {'X-Workspace-ID': 'local'}, credentials: 'include'});
    if (!response.ok) return;
    const blob = await response.blob(); const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = 'adro-workspace-local.zip'; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  function orchestrationAction(id, kind, action, label, variant = '') {
    return `<button class="action-button ${variant}" type="button" data-orchestration-kind="${escapeHTML(kind)}" data-orchestration-id="${escapeHTML(id)}" data-orchestration-action="${escapeHTML(action)}">${escapeHTML(t(label || action))}</button>`;
  }

  function ensureGraphDialog() {
    if ($('#graphEditorDialog')) return;
    document.body.insertAdjacentHTML('beforeend', `<dialog id="graphEditorDialog" class="orchestration-dialog graph-editor-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / WORKFLOW GRAPH</p><h2>${escapeHTML(t('graphEditor'))}</h2><p id="graphEditorTarget" class="mono"></p></div><button class="dialog-close" id="closeGraphEditor" type="button" aria-label="${escapeHTML(t('close'))}">×</button></div><form id="graphEditorForm"><section class="graph-studio-toolbar"><strong>${escapeHTML(t('graphCanvas'))}</strong><div><button class="secondary" id="graphAddAgent" type="button">+ ${escapeHTML(t('addAgentNode'))}</button><button class="secondary" id="graphAddSquad" type="button">+ ${escapeHTML(t('addSquadNode'))}</button><button class="secondary" id="graphAddGate" type="button">+ ${escapeHTML(t('addGateNode'))}</button><button class="secondary" id="graphAddMerge" type="button">+ ${escapeHTML(t('addMergeNode'))}</button><button class="secondary" id="graphAddRepair" type="button">+ ${escapeHTML(t('addRepairNode'))}</button><button class="secondary" id="graphAddHuman" type="button">+ ${escapeHTML(t('addHumanNode'))}</button><button class="secondary" id="graphConnect" type="button">${escapeHTML(t('connectNodes'))}</button></div></section><div id="graphEditorCanvas" class="graph-editor-canvas" role="application" aria-label="${escapeHTML(t('graphCanvas'))}"></div><section id="graphEditorEdges" class="graph-edge-editor"></section><label class="graph-json-fallback"><span>${escapeHTML(t('graphJSON'))}</span><textarea id="graphEditorJSON" spellcheck="false"></textarea><small class="form-help">${escapeHTML(t('graphJSONHelp'))}</small></label><div id="graphEditorSummary" class="graph-editor-summary"></div><p id="graphEditorStatus" class="form-error" role="status"></p><div class="form-actions"><button class="secondary" id="graphEditorFormat" type="button">${escapeHTML(t('formatGraph'))}</button><button class="secondary" id="graphEditorValidate" type="button">${escapeHTML(t('validateGraph'))}</button><button class="primary" id="graphEditorSave" type="submit">${escapeHTML(t('saveGraph'))}</button></div></form></dialog>`);
    const graphJSONField = $('#graphEditorJSON');
    graphJSONField.hidden = true;
    graphJSONField.setAttribute('aria-hidden', 'true');
    graphJSONField.closest('label').hidden = true;
    $('#graphEditorFormat').hidden = true;
    $('#closeGraphEditor').onclick = () => $('#graphEditorDialog').close();
    $('#graphEditorDialog').addEventListener('click', event => { if (event.target === event.currentTarget) event.currentTarget.close(); });
    $('#graphEditorFormat').onclick = () => {
      try { const graph = JSON.parse($('#graphEditorJSON').value); $('#graphEditorJSON').value = JSON.stringify(graph, null, 2); renderGraphEditorSummary(graph); renderGraphCanvas(graph); setGraphEditorStatus(''); } catch (_) { setGraphEditorStatus(t('graphValidationFailed'), true); }
    };
    $('#graphEditorJSON').addEventListener('input', () => { const graph = graphEditorInput(false); if (graph) { renderGraphEditorSummary(graph); renderGraphCanvas(graph); } });
    $('#graphAddAgent').onclick = () => addGraphNode('agent');
    $('#graphAddSquad').onclick = () => addGraphNode('squad');
    $('#graphAddGate').onclick = () => addGraphNode('gate');
    $('#graphAddMerge').onclick = () => addGraphNode('merge');
    $('#graphAddRepair').onclick = () => addGraphNode('repair');
    $('#graphAddHuman').onclick = () => addGraphNode('human');
    $('#graphConnect').onclick = () => { graphConnectMode = !graphConnectMode; $('#graphEditorCanvas').classList.toggle('connect-mode', graphConnectMode); setGraphEditorStatus(graphConnectMode ? t('connectNodes') : ''); };
    $('#graphEditorValidate').onclick = () => validateGraphEditor();
    $('#graphEditorForm').onsubmit = saveGraphEditor;
  }

  function setGraphEditorStatus(message, bad = false) {
    const target = $('#graphEditorStatus');
    if (!target) return;
    target.textContent = message;
    target.className = `form-error ${bad ? 'graph-status-bad' : 'graph-status-good'}`;
  }

  function renderGraphEditorSummary(graph) {
    const target = $('#graphEditorSummary');
    if (!target) return;
    const nodes = Array.isArray(graph?.nodes) ? graph.nodes : [];
    const edges = Array.isArray(graph?.edges) ? graph.edges : [];
    target.innerHTML = `<div class="graph-summary-head"><strong>${escapeHTML(t('graphNodes'))} ${nodes.length}</strong><span>${escapeHTML(t('edges'))} ${edges.length}</span></div><div class="graph-node-list">${nodes.map(node => `<span class="graph-node-chip"><b>${escapeHTML(node.id || '?')}</b><small>${escapeHTML(node.kind || '?')}</small></span>`).join('') || `<span class="muted">${escapeHTML(t('noItems'))}</span>`}</div><p class="form-help">${escapeHTML(t('graphNodeHint'))}</p>`;
  }

  let graphConnectMode = false;
  let graphConnectSource = '';

  function graphEditorInput(showError = true) {
    try {
      const graph = JSON.parse($('#graphEditorJSON').value);
      if (!graph || typeof graph !== 'object' || Array.isArray(graph)) throw new Error('graph must be an object');
      return graph;
    } catch (error) {
      if (showError) setGraphEditorStatus(error.message || t('graphValidationFailed'), true);
      return null;
    }
  }

  function predicateEditorHTML(predicate = {}, path = '', depth = 0) {
    const p = predicate || {};
    const kind = p.kind || '';
    const options = ['', 'field_eq', 'number_cmp', 'contains', 'exists', 'all', 'any', 'not'];
    const optionMarkup = options.map(value => `<option value="${value}"${kind === value ? ' selected' : ''}>${value || 'none'}</option>`).join('');
    const children = Array.isArray(p.children) ? p.children : [];
    const childMarkup = (kind === 'all' || kind === 'any' || kind === 'not')
      ? `<div class="predicate-children">${children.map((child, index) => `<div class="predicate-child"><div class="predicate-child-head"><span>${escapeHTML(`${t('predicateChild')} ${index + 1}`)}</span><button type="button" class="graph-node-remove" data-predicate-remove="${escapeHTML(path ? `${path}.children.${index}` : `children.${index}`)}" aria-label="${escapeHTML(t('predicateRemoveChild'))}">×</button></div>${predicateEditorHTML(child, path ? `${path}.children.${index}` : `children.${index}`, depth + 1)}</div>`).join('') || `<span class="muted">${escapeHTML(t('predicateNoChildren'))}</span>`}${kind === 'all' || kind === 'any' ? `<button type="button" class="secondary predicate-add" data-predicate-add="${escapeHTML(path)}">+ ${escapeHTML(t('predicateAddChild'))}</button>` : ''}</div>`
      : '';
    const scalar = kind && kind !== 'exists' && kind !== 'all' && kind !== 'any' && kind !== 'not';
    return `<div class="predicate-editor ${depth ? 'predicate-nested' : ''}" data-predicate-path="${escapeHTML(path)}"><label><span>${escapeHTML(t('predicateKind'))}</span><select data-predicate-field="kind" data-predicate-path="${escapeHTML(path)}">${optionMarkup}</select></label>${kind ? `<label><span>${escapeHTML(t('edgePredicateField'))}</span><input data-predicate-field="field" data-predicate-path="${escapeHTML(path)}" value="${escapeHTML(p.field || '')}"></label>` : ''}${scalar ? `<label><span>${escapeHTML(t('edgePredicateOp'))}</span><select data-predicate-field="op" data-predicate-path="${escapeHTML(path)}">${['', 'eq', 'ne', 'lt', 'lte', 'gt', 'gte'].map(op => `<option value="${op}"${p.op === op ? ' selected' : ''}>${op || 'default'}</option>`).join('')}</select></label><label><span>${escapeHTML(t('edgePredicateValue'))}</span><input data-predicate-field="value" data-predicate-path="${escapeHTML(path)}" value="${escapeHTML(p.value == null ? '' : String(p.value))}"></label>` : ''}${childMarkup}</div>`;
  }

  function predicateAt(root, path) {
    if (!path) return root;
    return path.split('.').reduce((current, part) => {
      if (part === 'children') return current?.children;
      return Array.isArray(current) ? current[Number(part)] : current?.[part];
    }, root);
  }

  function replacePredicateAt(root, path, value) {
    if (!path) return value;
    const parts = path.split('.');
    const index = Number(parts.pop());
    const parent = predicateAt(root, parts.join('.'));
    if (Array.isArray(parent) && Number.isInteger(index)) parent[index] = value;
    else if (parent?.children && Number.isInteger(index)) parent.children[index] = value;
    return root;
  }

  function parsePredicateValue(value) {
    const raw = String(value || '').trim();
    if (!raw) return undefined;
    if (raw === 'true') return true;
    if (raw === 'false') return false;
    if (raw !== '' && Number.isFinite(Number(raw))) return Number(raw);
    return raw;
  }

  function mutatePredicate(root, path, field, value) {
    const current = predicateAt(root, path) || {};
    if (field === 'kind') {
      const next = {kind: value};
      if (value === 'all' || value === 'any') next.children = [];
      if (value === 'not') next.children = [{kind: 'exists', field: 'outcome'}];
      return replacePredicateAt(root, path, next);
    }
    if (!current.kind) current.kind = 'field_eq';
    if (value === '') delete current[field];
    else current[field] = field === 'value' ? parsePredicateValue(value) : value;
    return root;
  }

  function bindPredicateEditor(host, read, write) {
    host.querySelectorAll('[data-predicate-field]').forEach(input => input.addEventListener('change', () => {
      const next = mutatePredicate(read(), input.dataset.predicatePath || '', input.dataset.predicateField, input.value);
      write(next);
    }));
    host.querySelectorAll('[data-predicate-add]').forEach(button => button.addEventListener('click', () => {
      const root = read();
      const parent = predicateAt(root, button.dataset.predicateAdd || '');
      if (!parent) return;
      parent.children = Array.isArray(parent.children) ? parent.children : [];
      parent.children.push({kind: 'field_eq', field: '', op: 'eq', value: ''});
      write(root);
    }));
    host.querySelectorAll('[data-predicate-remove]').forEach(button => button.addEventListener('click', () => {
      const root = read();
      const path = button.dataset.predicateRemove || '';
      const parts = path.split('.');
      const index = Number(parts.pop());
      const parent = predicateAt(root, parts.join('.'));
      if (Array.isArray(parent) && Number.isInteger(index)) parent.splice(index, 1);
      else if (parent?.children && Number.isInteger(index)) parent.children.splice(index, 1);
      write(root);
    }));
  }

  function graphNodePolicyHTML(node, graph) {
    const join = node.join_policy || '';
    const repair = node.repair_policy || {};
    const targets = (graph.nodes || []).filter(candidate => candidate.id !== node.id);
    const targetOptions = targets.map(candidate => `<option value="${escapeHTML(candidate.id)}"${repair.target_node_id === candidate.id ? ' selected' : ''}>${escapeHTML(candidate.id)}</option>`).join('');
    const verification = Array.isArray(repair.verification_node_ids) ? repair.verification_node_ids : [];
    const verificationOptions = targets.map(candidate => `<option value="${escapeHTML(candidate.id)}"${verification.includes(candidate.id) ? ' selected' : ''}>${escapeHTML(candidate.id)}</option>`).join('');
    return `<div class="graph-node-policy" data-graph-node-policy="${escapeHTML(node.id || '')}"><div class="graph-policy-grid"><label><span>${escapeHTML(t('joinPolicy'))}</span><select data-node-field="join_policy"><option value=""${!join ? ' selected' : ''}>default</option><option value="all"${join === 'all' ? ' selected' : ''}>all</option><option value="quorum"${join === 'quorum' ? ' selected' : ''}>quorum</option><option value="first_success"${join === 'first_success' ? ' selected' : ''}>first_success</option></select></label><label><span>${escapeHTML(t('joinQuorum'))}</span><input type="number" min="0" data-node-field="join_quorum" value="${escapeHTML(String(node.join_quorum || ''))}"></label><label><span>${escapeHTML(t('joinFailurePolicy'))}</span><select data-node-field="join_failure_policy"><option value=""${!node.join_failure_policy ? ' selected' : ''}>wait</option><option value="short_circuit"${node.join_failure_policy === 'short_circuit' ? ' selected' : ''}>short_circuit</option></select></label><label><span>${escapeHTML(t('nodeTimeout'))}</span><input type="number" min="0" data-node-field="timeout_ms" value="${escapeHTML(String(node.timeout ? Math.round(Number(node.timeout) / 1000000) : ''))}"></label></div>${node.kind === 'gate' ? `<fieldset><legend>${escapeHTML(t('edgePredicate'))}</legend><div data-node-predicate-editor="${escapeHTML(node.id || '')}">${predicateEditorHTML(node.gate_policy?.predicate || {}, '')}</div><label><span>${escapeHTML(t('requiredEvidence'))}</span><input data-node-field="gate_required_evidence" value="${escapeHTML((node.gate_policy?.required_evidence || []).join(', '))}"></label><label><span>${escapeHTML(t('failureCode'))}</span><input data-node-field="gate_failure_code" value="${escapeHTML(node.gate_policy?.failure_code || '')}"></label></fieldset>` : ''}${node.kind === 'merge' ? `<fieldset><legend>${escapeHTML(t('mergePolicy'))}</legend><label><span>${escapeHTML(t('conflictPolicy'))}</span><select data-node-field="merge_conflict_policy"><option value="collect"${(node.merge_policy?.conflict_policy || 'collect') === 'collect' ? ' selected' : ''}>collect</option><option value="prefer_priority"${node.merge_policy?.conflict_policy === 'prefer_priority' ? ' selected' : ''}>prefer_priority</option><option value="fail"${node.merge_policy?.conflict_policy === 'fail' ? ' selected' : ''}>fail</option></select></label><label><span>${escapeHTML(t('keyFields'))}</span><input data-node-field="merge_key_fields" value="${escapeHTML((node.merge_policy?.key_fields || []).join(', '))}"></label><label class="graph-edge-check"><input type="checkbox" data-node-field="merge_require_evidence"${node.merge_policy?.require_evidence ? ' checked' : ''}><span>${escapeHTML(t('requireEvidence'))}</span></label></fieldset>` : ''}${node.kind === 'repair' ? `<fieldset><legend>${escapeHTML(t('repairPolicy'))}</legend><label><span>${escapeHTML(t('repairTarget'))}</span><select data-node-field="repair_target_node_id"><option value="">${escapeHTML(t('selectNode'))}</option>${targetOptions}</select></label><label><span>${escapeHTML(t('verificationNodes'))}</span><select multiple size="3" data-node-field="repair_verification_node_ids">${verificationOptions}</select></label><div class="graph-policy-grid"><label><span>${escapeHTML(t('repairRounds'))}</span><input type="number" min="0" data-node-field="repair_max_rounds" value="${escapeHTML(String(repair.max_rounds || ''))}"></label><label><span>${escapeHTML(t('repairBudget'))}</span><input type="number" min="0" data-node-field="repair_budget_tokens" value="${escapeHTML(String(repair.budget?.tokens || ''))}"></label></div><label><span>${escapeHTML(t('repairScope'))}</span><input data-node-field="repair_scope" value="${escapeHTML((repair.scope || []).join(', '))}"></label></fieldset>` : ''}</div>`;
  }

  function renderGraphCanvas(graph) {
    const canvas = $('#graphEditorCanvas');
    if (!canvas) return;
    const nodes = Array.isArray(graph?.nodes) ? graph.nodes : [];
    const edges = Array.isArray(graph?.edges) ? graph.edges : [];
    const outgoing = new Map(nodes.map(node => [node.id, []]));
    edges.forEach(edge => { if (outgoing.has(edge.from)) outgoing.get(edge.from).push(edge); });
    canvas.innerHTML = nodes.map((node, index) => {
      const edgeText = (outgoing.get(node.id) || []).map(edge => `${edge.on || 'success'} -> ${edge.to}`).join(' · ');
      const referenceKind = node.kind === 'agent' ? 'agent' : node.kind === 'squad' ? 'squad' : '';
      const referenceID = referenceKind === 'agent' ? (node.agent_ref?.id || '') : (node.squad_ref?.id || '');
      const references = referenceKind === 'agent' ? nativeAgents.filter(agent => agent.status === 'active') : nativeSquads.filter(squad => squad.status === 'published');
      const referenceOptions = references.map(reference => {
        const version = referenceKind === 'agent' ? reference.revision : reference.published_version;
        const label = `${reference.name || reference.id} · ${referenceKind === 'agent' ? 'r' : 'v'}${version || 0}`;
        return `<option value="${escapeHTML(reference.id)}" data-revision="${escapeHTML(String(version || 1))}" ${reference.id === referenceID ? 'selected' : ''}>${escapeHTML(label)}</option>`;
      }).join('');
      const referenceControl = referenceKind ? `<label><span>${escapeHTML(t('nodeReference'))}</span><select data-graph-ref="${escapeHTML(node.id || '')}"><option value="">${escapeHTML(t('noPublishedTarget'))}</option>${referenceOptions}</select></label>` : '';
      return `<article class="graph-canvas-node graph-kind-${escapeHTML(node.kind || 'agent')}" draggable="true" data-graph-node="${escapeHTML(node.id || '')}"><header><strong>${escapeHTML(node.id || '?')}</strong><button type="button" class="graph-node-remove" data-graph-remove="${escapeHTML(node.id || '')}" aria-label="${escapeHTML(t('removeNode'))}">×</button></header><label><span>${escapeHTML(t('nodeKind'))}</span><select data-graph-kind="${escapeHTML(node.id || '')}"><option value="agent" ${node.kind === 'agent' ? 'selected' : ''}>Agent</option><option value="squad" ${node.kind === 'squad' ? 'selected' : ''}>Squad</option><option value="gate" ${node.kind === 'gate' ? 'selected' : ''}>Gate</option><option value="merge" ${node.kind === 'merge' ? 'selected' : ''}>Merge</option><option value="repair" ${node.kind === 'repair' ? 'selected' : ''}>Repair</option><option value="human" ${node.kind === 'human' ? 'selected' : ''}>Human</option></select></label>${referenceControl}<div class="graph-node-tuning"><label><span>${escapeHTML(t('nodeBudget'))}</span><input type="number" min="0" data-graph-budget="${escapeHTML(node.id || '')}" value="${escapeHTML(String(node.budget?.tokens || ''))}"></label><label><span>${escapeHTML(t('nodeRetry'))}</span><input type="number" min="0" data-graph-retry="${escapeHTML(node.id || '')}" value="${escapeHTML(String(node.retry_policy?.max_attempts || ''))}"></label></div>${graphNodePolicyHTML(node, graph)}<small>${escapeHTML(edgeText || t('noOutgoingEdges'))}</small><span class="graph-node-position">${index + 1}</span></article>`;
    }).join('') || `<p class="graph-canvas-empty">${escapeHTML(t('noItems'))}</p>`;
    renderGraphEdgeEditor(graph);
    canvas.querySelectorAll('[data-graph-kind]').forEach(select => select.addEventListener('change', () => {
      const current = graphEditorInput();
      const node = current?.nodes?.find(item => item.id === select.dataset.graphKind);
      if (!node) return;
      node.kind = select.value;
      if (node.kind === 'agent' && !node.agent_ref) node.agent_ref = {id: '', revision: 1};
      if (node.kind === 'squad') delete node.agent_ref;
      $('#graphEditorJSON').value = JSON.stringify(current, null, 2);
      renderGraphEditorSummary(current);
      renderGraphCanvas(current);
    }));
    canvas.querySelectorAll('[data-graph-ref]').forEach(input => input.addEventListener('change', () => updateGraphNode(input.dataset.graphRef, node => {
      const option = input.selectedOptions?.[0];
      const revision = Number(option?.dataset.revision || 1);
      if (node.kind === 'agent') node.agent_ref = {id: input.value.trim(), revision};
      else node.squad_ref = {id: input.value.trim(), revision: 0, version: revision};
    })));
    canvas.querySelectorAll('[data-graph-budget]').forEach(input => input.addEventListener('change', () => updateGraphNode(input.dataset.graphBudget, node => {
      node.budget = {...(node.budget || {}), tokens: Math.max(0, Number(input.value) || 0)};
    })));
    canvas.querySelectorAll('[data-graph-retry]').forEach(input => input.addEventListener('change', () => updateGraphNode(input.dataset.graphRetry, node => {
      node.retry_policy = {...(node.retry_policy || {}), max_attempts: Math.max(0, Number(input.value) || 0)};
    })));
    canvas.querySelectorAll('[data-graph-node-policy]').forEach(panel => {
      const id = panel.dataset.graphNodePolicy;
      panel.querySelectorAll('[data-node-field]').forEach(input => input.addEventListener('change', () => updateGraphNode(id, node => {
        const field = input.dataset.nodeField;
        node.gate_policy = node.gate_policy || {};
        node.merge_policy = node.merge_policy || {};
        node.repair_policy = node.repair_policy || {};
        if (field === 'merge_require_evidence') node.merge_policy.require_evidence = input.checked;
        else if (field === 'repair_verification_node_ids') node.repair_policy.verification_node_ids = Array.from(input.selectedOptions).map(option => option.value);
        else if (field === 'gate_required_evidence') node.gate_policy.required_evidence = input.value.split(',').map(value => value.trim()).filter(Boolean);
        else if (field === 'merge_key_fields') node.merge_policy.key_fields = input.value.split(',').map(value => value.trim()).filter(Boolean);
        else if (field === 'repair_scope') node.repair_policy.scope = input.value.split(',').map(value => value.trim()).filter(Boolean);
        else if (field.startsWith('repair_')) {
          const key = field.slice('repair_'.length);
          if (key === 'budget_tokens') node.repair_policy.budget = {...(node.repair_policy.budget || {}), tokens: Math.max(0, Number(input.value) || 0)};
          else if (key === 'max_rounds') node.repair_policy.max_rounds = Math.max(0, Number(input.value) || 0);
          else node.repair_policy[key] = input.value;
        } else if (field.startsWith('gate_')) node.gate_policy[field.slice('gate_'.length)] = input.value;
        else if (field === 'timeout_ms') node.timeout = Math.max(0, Number(input.value) || 0) * 1000000;
        else if (field === 'join_quorum') node.join_quorum = Math.max(0, Number(input.value) || 0);
        else node[field] = input.value;
      })));
      const predicateHost = panel.querySelector('[data-node-predicate-editor]');
      if (predicateHost) bindPredicateEditor(predicateHost, () => graphEditorInput()?.nodes?.find(node => node.id === id)?.gate_policy?.predicate || {}, predicate => updateGraphNode(id, node => { node.gate_policy = {...(node.gate_policy || {}), predicate}; }));
    });
    canvas.querySelectorAll('[data-graph-remove]').forEach(button => button.addEventListener('click', () => removeGraphNode(button.dataset.graphRemove)));
    let draggedNode = '';
    canvas.querySelectorAll('[data-graph-node]').forEach(node => {
      node.addEventListener('dragstart', () => { draggedNode = node.dataset.graphNode || ''; node.classList.add('dragging'); });
      node.addEventListener('dragend', () => { draggedNode = ''; node.classList.remove('dragging'); });
      node.addEventListener('dragover', event => event.preventDefault());
      node.addEventListener('drop', event => {
        event.preventDefault();
        if (!draggedNode || draggedNode === node.dataset.graphNode) return;
        const current = graphEditorInput();
        if (!current || !Array.isArray(current.nodes)) return;
        const from = current.nodes.findIndex(item => item.id === draggedNode);
        const to = current.nodes.findIndex(item => item.id === node.dataset.graphNode);
        if (from < 0 || to < 0) return;
        const [moved] = current.nodes.splice(from, 1);
        current.nodes.splice(to, 0, moved);
        writeGraphEditor(current);
      });
    });
    canvas.querySelectorAll('[data-graph-node]').forEach(node => node.addEventListener('click', () => {
      if (!graphConnectMode) return;
      const id = node.dataset.graphNode;
      if (!graphConnectSource) { graphConnectSource = id; node.classList.add('connect-source'); setGraphEditorStatus(`${t('connectNodes')}: ${id}`); return; }
      if (graphConnectSource !== id) addGraphEdge(graphConnectSource, id);
      graphConnectSource = '';
      graphConnectMode = false;
      canvas.classList.remove('connect-mode');
    }));
  }

  function updateGraphNode(id, mutate) {
    const graph = graphEditorInput();
    const node = graph?.nodes?.find(item => item.id === id);
    if (!node) return;
    mutate(node);
    writeGraphEditor(graph);
  }

  function updateGraphEdge(id, mutate) {
    const graph = graphEditorInput();
    const edge = graph?.edges?.find(item => item.id === id);
    if (!edge) return;
    mutate(edge);
    writeGraphEditor(graph);
  }

  function renderGraphEdgeEditor(graph) {
    const host = $('#graphEditorEdges');
    if (!host) return;
    const edges = Array.isArray(graph?.edges) ? graph.edges : [];
    const events = ['success', 'failure', 'bug', 'timeout', 'approval', 'cancel'];
    const rows = edges.map(edge => {
      const eventOptions = events.map(event => `<option value="${event}"${edge.on === event ? ' selected' : ''}>${event}</option>`).join('');
      return `<article class="graph-edge-row" data-graph-edge="${escapeHTML(edge.id || '')}">
        <div class="graph-edge-route"><strong>${escapeHTML(edge.from || '?')}</strong><span>→</span><strong>${escapeHTML(edge.to || '?')}</strong></div>
        <label><span>${escapeHTML(t('edgeEvent'))}</span><select data-edge-field="on">${eventOptions}</select></label>
        <label><span>${escapeHTML(t('edgePriority'))}</span><input type="number" data-edge-field="priority" value="${escapeHTML(String(edge.priority || 0))}"></label>
        <label><span>${escapeHTML(t('edgeMaxTraversals'))}</span><input type="number" min="0" data-edge-field="max_traversals" value="${escapeHTML(String(edge.max_traversals || 0))}"></label>
        <label><span>${escapeHTML(t('edgeLoop'))}</span><input data-edge-field="loop_group" value="${escapeHTML(edge.loop_group || '')}"></label>
        <label class="graph-edge-check"><input type="checkbox" data-edge-field="fan_out"${edge.fan_out ? ' checked' : ''}><span>${escapeHTML(t('edgeFanOut'))}</span></label>
        <fieldset><legend>${escapeHTML(t('edgePredicate'))}</legend><div data-edge-predicate-editor="${escapeHTML(edge.id || '')}">${predicateEditorHTML(edge.predicate || {}, '')}</div></fieldset>
      </article>`;
    }).join('');
    host.innerHTML = `<div class="graph-edge-editor-head"><strong>${escapeHTML(t('edgeEditor'))}</strong><span>${edges.length}</span></div>${rows || `<p class="graph-canvas-empty">${escapeHTML(t('noOutgoingEdges'))}</p>`}`;
    host.querySelectorAll('[data-graph-edge]').forEach(row => {
      const id = row.dataset.graphEdge;
      row.querySelectorAll('[data-edge-field]').forEach(input => input.addEventListener('change', () => updateGraphEdge(id, edge => {
        const field = input.dataset.edgeField;
        if (field === 'fan_out') edge[field] = input.checked;
        else if (field === 'priority' || field === 'max_traversals') edge[field] = Math.max(0, Number(input.value) || 0);
        else edge[field] = input.value;
      })));
      const predicateHost = row.querySelector('[data-edge-predicate-editor]');
      if (predicateHost) bindPredicateEditor(predicateHost, () => graphEditorInput()?.edges?.find(edge => edge.id === id)?.predicate || {}, predicate => updateGraphEdge(id, edge => { edge.predicate = predicate; }));
    });
  }

  function writeGraphEditor(graph) {
    $('#graphEditorJSON').value = JSON.stringify(graph, null, 2);
    renderGraphEditorSummary(graph);
    renderGraphCanvas(graph);
  }

  function addGraphNode(kind) {
    const graph = graphEditorInput();
    if (!graph) return;
    graph.nodes = Array.isArray(graph.nodes) ? graph.nodes : [];
    const base = kind === 'agent' ? 'agent' : kind;
    let id = base;
    let counter = 2;
    while (graph.nodes.some(node => node.id === id)) id = `${base}-${counter++}`;
    const node = {id, kind, retry_policy: {max_attempts: 1}, budget: {tokens: 0}};
    if (kind === 'agent') node.agent_ref = {id: '', revision: 1};
    if (kind === 'squad') node.squad_ref = {id: '', revision: 1};
    if (kind === 'gate') node.gate_policy = {predicate: {kind: 'exists', field: 'outcome'}};
    if (kind === 'merge') node.merge_policy = {conflict_policy: 'collect'};
    if (kind === 'repair') node.repair_policy = {target_node_id: '', verification_node_ids: [], max_rounds: 1};
    graph.nodes.push(node);
    if (!Array.isArray(graph.entry_node_ids) || graph.entry_node_ids.length === 0) graph.entry_node_ids = [id];
    if (!Array.isArray(graph.exit_node_ids) || graph.exit_node_ids.length === 0) graph.exit_node_ids = [id];
    writeGraphEditor(graph);
  }

  function removeGraphNode(id) {
    const graph = graphEditorInput();
    if (!graph) return;
    graph.nodes = (graph.nodes || []).filter(node => node.id !== id);
    graph.edges = (graph.edges || []).filter(edge => edge.from !== id && edge.to !== id);
    graph.entry_node_ids = (graph.entry_node_ids || []).filter(nodeID => nodeID !== id);
    graph.exit_node_ids = (graph.exit_node_ids || []).filter(nodeID => nodeID !== id);
    writeGraphEditor(graph);
  }

  function addGraphEdge(from, to) {
    const graph = graphEditorInput();
    if (!graph) return;
    graph.edges = Array.isArray(graph.edges) ? graph.edges : [];
    if (graph.edges.some(edge => edge.from === from && edge.to === to && (edge.on || 'success') === 'success')) return;
    graph.edges.push({id: `${from}-${to}-success`, from, to, on: 'success'});
    writeGraphEditor(graph);
    setGraphEditorStatus(`${from} -> ${to}`);
  }

  async function validateGraphEditor() {
    const graph = graphEditorInput();
    if (!graph) return false;
    try {
      const response = await api('/api/v1/execution-plans/validate', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({graph})});
      if (!response.valid) { setGraphEditorStatus(response.error || t('graphValidationFailed'), true); return false; }
      $('#graphEditorJSON').value = JSON.stringify(response.graph, null, 2);
      renderGraphEditorSummary(response.graph);
      const d = response.diagnostics || {};
      setGraphEditorStatus(`${t('validate')}: ${d.node_count || 0} ${t('graphNodes')} / ${d.edge_count || 0} ${t('edges')}`);
      return true;
    } catch (_) {
      setGraphEditorStatus(t('graphValidationFailed'), true);
      return false;
    }
  }

  function openGraphEditor(target, targetType = 'squad') {
    if (!target) return;
    ensureGraphDialog();
    activeGraphEditor = {id: target.id, revision: target.revision, name: target.name, kind: targetType};
    const graph = targetType === 'agent' ? (target.graph || graphForNativeAgent(target)) : (target.graph || {});
    $('#graphEditorTarget').textContent = `${target.name || target.id} · ${targetType} · r${target.revision}`;
    $('#graphEditorJSON').value = JSON.stringify(graph, null, 2);
    renderGraphEditorSummary(graph);
    renderGraphCanvas(graph);
    setGraphEditorStatus('');
    $('#graphEditorDialog').showModal();
    setTimeout(() => $('#graphEditorCanvas')?.focus(), 0);
  }

  async function saveGraphEditor(event) {
    event.preventDefault();
    if (!activeGraphEditor) return;
    if (!(await validateGraphEditor())) return;
    const graph = graphEditorInput();
    try {
      const collection = activeGraphEditor.kind === 'agent' ? 'agents' : 'squads';
      await api(`/api/v1/workspaces/local/${collection}/${encodeURIComponent(activeGraphEditor.id)}/graph`, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({expected_revision: activeGraphEditor.revision, graph})});
      $('#graphEditorDialog').close();
      await loadOrchestrationData();
      setOrchestrationStatus(t('graphSaved'));
    } catch (_) {
      setGraphEditorStatus(t('graphValidationFailed'), true);
    }
  }

  function graphForNativeAgent(agent) {
    const graphID = crypto.randomUUID?.() || `graph-${Date.now()}`;
    return {id: graphID, version: agent.revision || 1, entry_node_ids: ['agent'], exit_node_ids: ['agent'], nodes: [{id: 'agent', kind: 'agent', agent_ref: {id: agent.id, revision: agent.revision}, retry_policy: {max_attempts: 3}, budget: {tokens: 120000, tool_calls: 200, concurrent: 1}}], edges: []};
  }

  function graphForNativeSquadMembers(agents) {
    const graphID = crypto.randomUUID?.() || `graph-${Date.now()}`;
    const nodes = agents.map((agent, index) => ({id: `agent-${index + 1}`, kind: 'agent', agent_ref: {id: agent.id, revision: agent.revision}, retry_policy: {max_attempts: 3}, budget: {tokens: 120000, tool_calls: 200, concurrent: 1}}));
    const nodeIDs = nodes.map(node => node.id);
    return {id: graphID, version: Math.max(1, ...agents.map(agent => agent.revision || 1)), entry_node_ids: nodeIDs, exit_node_ids: nodeIDs, nodes, edges: []};
  }

  function nativePlanSelectionBody() {
    const value = String($('#nativePlanTarget')?.value || '');
    const [kind, id] = value.split(':', 2);
    const body = {};
    if (kind === 'agent') {
      const agent = nativeAgents.find(item => item.id === id);
      body.agent_id = id;
      body.agent_revision = agent?.revision || 0;
    } else if (kind === 'squad') {
      const squad = nativeSquads.find(item => item.id === id);
      body.squad_id = id;
      body.squad_version = squad?.published_version || 0;
    }
    return body;
  }

  function setNativePlanGraphStatus(message, bad = false) {
    const status = $('#nativePlanGraphStatus');
    if (!status) return;
    status.textContent = message;
    status.className = `form-help ${bad ? 'graph-status-bad' : 'graph-status-good'}`;
  }

  function renderNativePlanGraphSummary(graph, diagnostics = {}) {
    const target = $('#nativePlanGraphSummary');
    if (!target) return;
    const nodes = Array.isArray(graph?.nodes) ? graph.nodes : [];
    const edges = Array.isArray(graph?.edges) ? graph.edges : [];
    const loopCount = diagnostics.loop_edge_count ?? edges.filter(edge => edge.max_traversals || edge.loop_group).length;
    const concurrency = diagnostics.max_concurrency ?? nodes.reduce((max, node) => Math.max(max, node.budget?.concurrent || 0), 0);
    target.innerHTML = `<div class="graph-summary-head"><strong>${escapeHTML(t('graphNodes'))} ${nodes.length}</strong><span>${escapeHTML(t('edges'))} ${edges.length}</span></div><div class="graph-node-list">${nodes.map(node => `<span class="graph-node-chip"><b>${escapeHTML(node.id || '?')}</b><small>${escapeHTML(node.kind || '?')}</small></span>`).join('') || `<span class="muted">${escapeHTML(t('noItems'))}</span>`}</div><p class="form-help">${escapeHTML(t('graphDiagnostics'))}: ${escapeHTML(String(loopCount))} / ${escapeHTML(String(concurrency || 0))}</p>`;
  }

  function nativePlanGraphInput() {
    const textarea = $('#nativePlanGraph');
    if (!textarea || !textarea.value.trim()) return null;
    try {
      const graph = JSON.parse(textarea.value);
      if (!graph || typeof graph !== 'object' || Array.isArray(graph)) throw new Error('graph must be an object');
      return graph;
    } catch (error) {
      setNativePlanGraphStatus(error.message || t('planGraphInvalid'), true);
      return undefined;
    }
  }

  async function loadNativePlanGraph() {
    const value = String($('#nativePlanTarget')?.value || '');
    const [kind, id] = value.split(':', 2);
    const target = kind === 'agent' ? nativeAgents.find(item => item.id === id) : nativeSquads.find(item => item.id === id);
    if (!target) return;
    const graph = kind === 'agent' ? graphForNativeAgent(target) : target.graph;
    $('#nativePlanGraph').value = JSON.stringify(graph || {}, null, 2);
    renderNativePlanGraphSummary(graph || {});
    setNativePlanGraphStatus('');
  }

  async function validateNativePlanGraph(requirementID) {
    const graph = nativePlanGraphInput();
    if (graph === undefined) return null;
    if (!graph) {
      setNativePlanGraphStatus(t('planGraphInvalid'), true);
      return null;
    }
    try {
      const body = {...nativePlanSelectionBody(), graph, idempotency_key: `preview-${idempotencyKey()}`};
      const response = await api(`/api/v1/requirements/${encodeURIComponent(requirementID)}/execution-plan/dry-run`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
      if (!response.valid) {
        setNativePlanGraphStatus((response.errors || [t('planGraphInvalid')]).join('; '), true);
        return null;
      }
      $('#nativePlanGraph').value = JSON.stringify(response.graph || graph, null, 2);
      renderNativePlanGraphSummary(response.graph || graph, response.diagnostics || {});
      setNativePlanGraphStatus(`${t('planGraphReady')} · ${(response.ready_nodes || []).length} ${t('nodes')}`);
      return response.graph || graph;
    } catch (_) {
      setNativePlanGraphStatus(t('planGraphInvalid'), true);
      return null;
    }
  }

  function ensureNativePlanGraphControls() {
    const host = $('#nativePlanGraphControls');
    if (!host || host.dataset.ready === 'true') return;
    host.dataset.ready = 'true';
    host.innerHTML = `<div class="plan-graph-head"><strong>${escapeHTML(t('planGraph'))}</strong><button class="secondary" id="nativePlanGraphLoad" type="button">${escapeHTML(t('planGraphLoad'))}</button></div><textarea id="nativePlanGraph" hidden aria-hidden="true" tabindex="-1"></textarea><div id="nativePlanGraphSummary" class="graph-editor-summary"></div><p id="nativePlanGraphStatus" class="form-help" role="status"></p><div class="form-actions plan-graph-actions"><button class="secondary" id="nativePlanGraphValidate" type="button">${escapeHTML(t('planGraphValidate'))}</button></div>`;
    $('#nativePlanTarget').addEventListener('change', loadNativePlanGraph);
    $('#nativePlanGraphLoad').onclick = loadNativePlanGraph;
    $('#nativePlanGraphValidate').onclick = () => validateNativePlanGraph(String($('#nativePlanRequirement')?.value || ''));
  }

  renderAgents = function nativeOrchestrationStudio() {
    const agentRows = nativeAgents.map(agent => {
      const lifecycle = agent.status === 'active'
        ? orchestrationAction(agent.id, 'agent', 'disable', 'disable')
        : agent.status !== 'archived' ? orchestrationAction(agent.id, 'agent', 'enable', 'enable', 'accent') : '';
      const archive = agent.status !== 'archived' ? orchestrationAction(agent.id, 'agent', 'archive', 'archive', 'danger') : '';
      const runtime = agent.executor_binding?.runtime_id || agent.executor_binding?.provider_id || '-';
      const owner = directory.find(item => item.id === agent.owner_id || item.username === agent.owner_id);
      const ownerLabel = owner?.display_name || owner?.username || agent.owner_id || '-';
      const stateLabel = agent.status === 'active' ? t('agentActive') : agent.status === 'archived' ? t('agentArchived') : agent.status === 'disabled' ? t('agentDisabled') : t('agentDraft');
      return `<tr><td><strong>${escapeHTML(agent.name || agent.id)}</strong><div class="mono orchestration-id">${escapeHTML(agent.id)}</div></td><td><strong>${escapeHTML(ownerLabel)}</strong><span hidden>${escapeHTML(agent.owner_id || '')}</span><div class="muted">${escapeHTML(agent.role || '-')}</div></td><td><span class="status ${agent.status === 'active' ? 'good' : agent.status === 'archived' ? 'bad' : 'warn'}">${escapeHTML(stateLabel)}</span><span hidden>${escapeHTML(agent.status)}</span></td><td><span class="mono">${escapeHTML(runtime)}</span><div class="muted">r${escapeHTML(String(agent.revision || 0))}</div></td><td class="muted">${escapeHTML((agent.capabilities || []).map(item => item.name).join(', ') || '-')}</td><td><div class="row-actions">${orchestrationAction(agent.id, 'agent', 'edit', 'edit')}${orchestrationAction(agent.id, 'agent', 'edit-graph', 'editGraph', 'accent')}${orchestrationAction(agent.id, 'agent', 'validate', 'validate')}${orchestrationAction(agent.id, 'agent', 'capabilities', 'capabilities')}${lifecycle}${archive}</div></td></tr>`;
    });
    const squadRows = nativeSquads.map(squad => {
      const publish = squad.status === 'draft' ? orchestrationAction(squad.id, 'squad', 'publish', 'publish', 'accent') : '';
      const disable = squad.status === 'published' ? orchestrationAction(squad.id, 'squad', 'disable', 'disable') : '';
      const archive = squad.status !== 'archived' ? orchestrationAction(squad.id, 'squad', 'archive', 'archive', 'danger') : '';
      return `<tr><td><strong>${escapeHTML(squad.name || squad.id)}</strong><div class="mono orchestration-id">${escapeHTML(squad.id)}</div></td><td><span class="status ${squad.status === 'published' ? 'good' : squad.status === 'archived' ? 'bad' : 'active'}">${escapeHTML(squad.status)}</span></td><td class="mono">r${escapeHTML(String(squad.revision || 0))} · v${escapeHTML(String(squad.published_version || 0))}</td><td>${escapeHTML(String((squad.members || []).length))}</td><td>${escapeHTML(String((squad.graph?.nodes || []).length))}</td><td><div class="row-actions">${orchestrationAction(squad.id, 'squad', 'edit-graph', 'editGraph', 'accent')}${orchestrationAction(squad.id, 'squad', 'fork', 'forkSquad')}${orchestrationAction(squad.id, 'squad', 'validate', 'validate')}${orchestrationAction(squad.id, 'squad', 'dry-run', 'dryRun')}${publish}${disable}${archive}</div></td></tr>`;
    });
    const planRows = nativePlans.slice().reverse().map(plan => `<tr><td><strong>${escapeHTML(plan.requirement_id || '-')}</strong><div class="mono orchestration-id">${escapeHTML(plan.id)}</div></td><td><span class="status ${plan.status === 'ready' ? 'good' : 'active'}">${escapeHTML(plan.status || '-')}</span></td><td class="mono">${escapeHTML(plan.selected_ref?.id || '-')}@${escapeHTML(String(plan.selected_ref?.version || plan.selected_ref?.revision || '-'))}</td><td>${escapeHTML(String((plan.graph_snapshot?.nodes || []).length))}</td><td class="mono digest-cell" title="${escapeHTML(plan.plan_hash || '')}">${escapeHTML((plan.plan_hash || '-').slice(0, 16))}</td><td><div class="row-actions">${orchestrationAction(plan.id, 'plan', 'timeline', 'timeline', 'accent')}${orchestrationAction(plan.id, 'plan', 'replay', 'replay')}</div></td></tr>`);
    const legacyRows = agentProfiles.map(profile => `<tr><td class="mono">${escapeHTML(profile.member_id || '-')}</td><td class="mono">${escapeHTML(profile.default_agent_binding_id || '-')}</td><td>${escapeHTML(profile.default_role || '-')}</td><td><span class="status warn">compat</span></td></tr>`);
    const statusClass = orchestrationStatusState.message ? (orchestrationStatusState.bad ? 'bad' : 'good') : '';
    return `<div class="view-stack orchestration-studio"><section class="orchestration-hero"><div><span class="orchestration-kicker">GRAPH-NATIVE / REVISION-LOCKED</span><h2>${escapeHTML(t('orchestrationReady'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><div class="orchestration-hero-actions"><button class="secondary" id="newSquad" type="button"><span aria-hidden="true">◇</span>${escapeHTML(t('newSquad'))}</button><button class="primary" id="newPlan" type="button"><span aria-hidden="true">▶</span>${escapeHTML(t('newPlan'))}</button></div></section><div id="orchestrationStatus" class="orchestration-status ${statusClass}" role="status">${escapeHTML(orchestrationStatusState.message)}</div><div class="view-grid orchestration-metrics">${summaryCard(t('nativeAgents'), nativeAgents.length, t('nativeAgentHelp'))}${summaryCard(t('nativeSquads'), nativeSquads.length, t('graphNodes'))}${summaryCard(t('executionPlans'), nativePlans.length, t('planHash'))}</div>${genericTable(t('nativeAgents'), [t('name'), t('assignees'), t('status'), t('runtime'), t('capabilities'), t('actions')], agentRows, t('noItems'))}${genericTable(t('nativeSquads'), [t('name'), t('status'), t('revision'), t('agents'), t('graphNodes'), t('actions')], squadRows, t('noItems'))}${genericTable(t('executionPlans'), [t('planRequirement'), t('status'), t('selectedTarget'), t('graphNodes'), t('planHash'), t('actions')], planRows, t('noItems'))}${legacyRows.length ? genericTable(t('legacyBindings'), [t('assignees'), t('agentBinding'), t('role'), t('status')], legacyRows, t('noItems')) : ''}</div>`;
  };

  function ensureOrchestrationDialogs() {
    if ($('#squadDialog')) return;
    document.body.insertAdjacentHTML('beforeend', `<dialog id="squadDialog" class="orchestration-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / SQUAD</p><h2>${escapeHTML(t('newSquad'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><button class="dialog-close" data-close-orchestration="squadDialog" type="button">×</button></div><form id="squadForm"><label><span>${escapeHTML(t('squadName'))}</span><input name="name" required></label><label><span>${escapeHTML(t('squadDescription'))}</span><textarea name="description"></textarea></label><label><span>${escapeHTML(t('squadLeader'))}</span><select name="leader" id="squadLeader" required></select></label><p class="form-error" id="squadFormError" role="alert"></p><div class="form-actions"><button class="secondary" data-close-orchestration="squadDialog" type="button">${escapeHTML(t('cancel'))}</button><button class="primary" type="submit">${escapeHTML(t('create'))}</button></div></form></dialog><dialog id="nativePlanDialog" class="orchestration-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / IMMUTABLE PLAN</p><h2>${escapeHTML(t('newPlan'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><button class="dialog-close" data-close-orchestration="nativePlanDialog" type="button">×</button></div><form id="nativePlanForm"><label><span>${escapeHTML(t('planRequirement'))}</span><select name="requirement" id="nativePlanRequirement" required></select></label><label><span>${escapeHTML(t('planTarget'))}</span><select name="target" id="nativePlanTarget" required></select></label><div id="nativePlanGraphControls"></div><p class="form-error" id="nativePlanFormError" role="alert"></p><div class="form-actions"><button class="secondary" data-close-orchestration="nativePlanDialog" type="button">${escapeHTML(t('cancel'))}</button><button class="primary" type="submit">${escapeHTML(t('publish'))}</button></div></form></dialog><dialog id="timelineDialog" class="orchestration-dialog timeline-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / REPLAY</p><h2>${escapeHTML(t('timeline'))}</h2><p id="timelinePlanID" class="mono"></p></div><button class="dialog-close" data-close-orchestration="timelineDialog" type="button">×</button></div><pre id="timelineContent" class="timeline-content"></pre><div class="form-actions"><button class="secondary" data-close-orchestration="timelineDialog" type="button">${escapeHTML(t('closeTimeline'))}</button></div></dialog>`);
    const leaderLabel = $('#squadLeader')?.closest('label');
    if (leaderLabel && !$('#squadMembers')) {
      const memberLabel = document.createElement('label');
      memberLabel.innerHTML = `<span>${escapeHTML(t('squadMembers'))}</span><select name="members" id="squadMembers" multiple size="5"></select><small class="form-help">${escapeHTML(t('squadMemberHelp'))}</small>`;
      leaderLabel.after(memberLabel);
    }
    document.querySelectorAll('[data-close-orchestration]').forEach(button => {
      button.onclick = () => $(`#${button.dataset.closeOrchestration}`)?.close();
    });
    $('#squadDialog').addEventListener('click', event => { if (event.target === event.currentTarget) event.currentTarget.close(); });
    $('#nativePlanDialog').addEventListener('click', event => { if (event.target === event.currentTarget) event.currentTarget.close(); });
    $('#timelineDialog').addEventListener('click', event => { if (event.target === event.currentTarget) event.currentTarget.close(); });
    $('#squadForm').onsubmit = createNativeSquad;
    $('#nativePlanForm').onsubmit = createNativePlan;
    ensureNativePlanGraphControls();
  }

  function setOrchestrationStatus(message, bad = false) {
    orchestrationStatusState = {message, bad};
    const target = $('#orchestrationStatus');
    if (!target) return;
    target.textContent = message;
    target.className = `orchestration-status ${bad ? 'bad' : 'good'}`;
  }

  function openSquadDialog() {
    ensureOrchestrationDialogs();
    $('#squadFormError').textContent = '';
    const activeAgents = nativeAgents.filter(agent => agent.status === 'active');
    $('#squadLeader').innerHTML = activeAgents.map(agent => `<option value="${escapeHTML(agent.id)}">${escapeHTML(agent.name)} · r${escapeHTML(String(agent.revision))}</option>`).join('');
    $('#squadMembers').innerHTML = activeAgents.map(agent => `<option value="${escapeHTML(agent.id)}">${escapeHTML(agent.name)} · ${escapeHTML(agent.role || 'agent')} · r${escapeHTML(String(agent.revision))}</option>`).join('');
    if (!$('#squadLeader').options.length) {
      setOrchestrationStatus(t('noPublishedTarget'), true);
      return;
    }
    $('#squadDialog').showModal();
    setTimeout(() => $('#squadForm input[name="name"]')?.focus(), 0);
  }

  function openNativePlanDialog() {
    ensureOrchestrationDialogs();
    $('#nativePlanFormError').textContent = '';
    $('#nativePlanRequirement').innerHTML = requirements.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.key || item.id)} · ${escapeHTML(item.title)}</option>`).join('');
    const targets = [
      ...nativeAgents.filter(agent => agent.status === 'active').map(agent => `<option value="agent:${escapeHTML(agent.id)}">Agent · ${escapeHTML(agent.name)} · r${escapeHTML(String(agent.revision))}</option>`),
      ...nativeSquads.filter(squad => squad.status === 'published').map(squad => `<option value="squad:${escapeHTML(squad.id)}">Squad · ${escapeHTML(squad.name)} · v${escapeHTML(String(squad.published_version))}</option>`)
    ];
    $('#nativePlanTarget').innerHTML = targets.join('');
    if (!requirements.length || !targets.length) {
      setOrchestrationStatus(t('noPublishedTarget'), true);
      return;
    }
    loadNativePlanGraph();
    $('#nativePlanDialog').showModal();
  }

  async function createNativeSquad(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const leader = nativeAgents.find(agent => agent.id === String(data.get('leader')));
    if (!leader) return;
    const selectedIDs = Array.from($('#squadMembers').selectedOptions || []).map(option => option.value);
    const selectedAgents = nativeAgents.filter(agent => selectedIDs.includes(agent.id));
    if (!selectedAgents.some(agent => agent.id === leader.id)) selectedAgents.unshift(leader);
    const members = selectedAgents.map((agent, index) => ({id: `member-${agent.id}`, agent_id: agent.id, role: agent.id === leader.id ? 'leader' : (agent.role || `member-${index + 1}`), leader: agent.id === leader.id, input_schema: agent.input_schema, output_schema: agent.output_schema, max_attempts: 3, budget: {tokens: 120000, tool_calls: 200, concurrent: 1}}));
    const body = {
      name: String(data.get('name')).trim(), description: String(data.get('description')).trim(), status: 'draft',
      members,
      graph: graphForNativeSquadMembers(selectedAgents),
      policy: {max_nesting_depth: 2, budget: {tokens: 120000, tool_calls: 200, concurrent: 1}, human_exit_required: true}
    };
    try {
      await api('/api/v1/workspaces/local/squads', {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(body)});
      form.reset(); $('#squadDialog').close(); await loadOrchestrationData(); setOrchestrationStatus(t('newSquad'));
    } catch (_) { $('#squadFormError').textContent = t('squadCreateFailed'); }
  }

  async function createNativePlan(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const requirementID = String(data.get('requirement'));
    const [kind, id] = String(data.get('target')).split(':', 2);
    const body = {...nativePlanSelectionBody(), idempotency_key: idempotencyKey()};
    try {
      const graph = await validateNativePlanGraph(requirementID);
      if (!graph) return;
      body.graph = graph;
      await api(`/api/v1/requirements/${encodeURIComponent(requirementID)}/execution-plan`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': body.idempotency_key}, body: JSON.stringify(body)});
      form.reset(); $('#nativePlanDialog').close(); await loadOrchestrationData(); setOrchestrationStatus(t('newPlan'));
    } catch (_) { $('#nativePlanFormError').textContent = t('planCreateFailed'); }
  }

  async function applyOrchestrationAction(kind, id, action) {
    try {
      if (kind === 'plan') {
        const path = action === 'timeline' ? `/api/v1/plans/${encodeURIComponent(id)}/timeline` : `/api/v1/execution-plans/${encodeURIComponent(id)}/replay`;
        const result = await api(path);
        ensureOrchestrationDialogs();
        $('#timelinePlanID').textContent = id;
        $('#timelineContent').textContent = JSON.stringify(result, null, 2);
        $('#timelineDialog').showModal();
        return;
      }
      if (kind === 'squad' && action === 'edit-graph') {
        openGraphEditor(nativeSquads.find(item => item.id === id));
        return;
      }
      if (kind === 'agent' && action === 'edit-graph') {
        openGraphEditor(nativeAgents.find(item => item.id === id), 'agent');
        return;
      }
      if (kind === 'squad' && action === 'fork') {
        const source = nativeSquads.find(item => item.id === id);
        const result = await api(`/api/v1/workspaces/local/squads/${encodeURIComponent(id)}/fork`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({name: `${t('forkSquad')}: ${source?.name || id}`})});
        await loadOrchestrationData();
        if (result?.squad) openGraphEditor(result.squad);
        return;
      }
      const result = await api(`/api/v1/${kind === 'agent' ? 'agents' : 'squads'}/${encodeURIComponent(id)}/${encodeURIComponent(action)}?workspace_id=local`, {method: action === 'capabilities' ? 'GET' : 'POST'});
      const message = result.valid === false ? `${action}: ${result.error || 'invalid'}` : `${action}: ${id}`;
      await loadOrchestrationData();
      setOrchestrationStatus(message, result.valid === false);
    } catch (_) { setOrchestrationStatus(t('lifecycleActionFailed'), true); }
  }

  function renderPermissionGrid(selected, role) {
    const allSelected = role === 'admin';
    $('#menuPermissionGrid').innerHTML = menuIDs.map(menu => `<label class="permission-option"><input type="checkbox" name="menus" value="${escapeHTML(menu)}" ${(allSelected || selected.includes(menu)) ? 'checked' : ''} ${allSelected ? 'disabled' : ''}><span>${escapeHTML(t(menu))}</span></label>`).join('');
  }

  function openUserDialog(user = null) {
    const form = $('#userForm');
    form.reset();
    $('#userFormError').textContent = '';
    form.elements.id.value = (user && user.id) || '';
    form.elements.username.value = (user && user.username) || '';
    form.elements.username.disabled = Boolean(user);
    form.elements.display_name.value = (user && user.display_name) || '';
    form.elements.role.value = (user && user.role) || 'member';
    form.elements.status.value = (user && user.status) || 'active';
    form.elements.password.required = !user;
    $('#userDialogTitle').textContent = t(user ? 'editUser' : 'createUser');
    renderPermissionGrid((user && user.menu_ids) || ['workbench', 'delivery'], form.elements.role.value);
    $('#userDialog').showModal();
    setTimeout(() => {
      const element = form.querySelector('input:not([type="hidden"]):not(:disabled)');
      if (element) element.focus();
    }, 0);
  }

  const closeUserDialog = () => $('#userDialog').close();
  $('#closeUserDialog').onclick = closeUserDialog;
  $('#cancelUserDialog').onclick = closeUserDialog;
  $('#userDialog').addEventListener('click', event => { if (event.target === event.currentTarget) closeUserDialog(); });
  $('#userForm select[name="role"]').onchange = event => {
    const selected = Array.from($('#userForm').querySelectorAll('input[name="menus"]:checked')).map(input => input.value);
    renderPermissionGrid(selected, event.currentTarget.value);
  };
  $('#userForm').onsubmit = async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const id = String(data.get('id') || '');
    const body = {
      display_name: String(data.get('display_name')).trim(), role: String(data.get('role')),
      status: String(data.get('status')), menu_ids: data.getAll('menus').map(String)
    };
    if (!id) body.username = String(form.elements.username.value).trim();
    if (String(data.get('password') || '')) body.password = String(data.get('password'));
    $('#userFormError').textContent = '';
    try {
      await api(id ? `/api/v1/users/${encodeURIComponent(id)}` : '/api/v1/users', {
        method: id ? 'PATCH' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
      });
      closeUserDialog();
      await loadIdentityData();
      render();
    } catch (_) {
      $('#userFormError').textContent = t('userSaveFailed');
    }
  };

  const closeRunnerExecuteDialog = () => $('#runnerExecuteDialog').close();
  $('#closeRunnerExecuteDialog').onclick = closeRunnerExecuteDialog;
  $('#cancelRunnerExecuteDialog').onclick = closeRunnerExecuteDialog;
  $('#runnerExecuteDialog').addEventListener('click', event => {
    if (event.target === event.currentTarget) closeRunnerExecuteDialog();
  });
  function addRunnerEnvRow(name = '', value = '') {
    const host = $('#runnerEnvRows');
    if (!host) return;
    const row = document.createElement('div');
    row.className = 'runner-env-row';
    row.dataset.runnerEnvRow = 'true';
    row.innerHTML = `<input data-runner-env-name type="text" autocomplete="off" placeholder="${escapeHTML(t('runnerEnvName'))}"><input data-runner-env-value type="text" autocomplete="off" placeholder="${escapeHTML(t('runnerEnvValue'))}"><button class="icon-button" type="button" data-runner-env-remove title="${escapeHTML(t('runnerRemoveEnv'))}" aria-label="${escapeHTML(t('runnerRemoveEnv'))}"><span aria-hidden="true">×</span></button>`;
    row.querySelector('[data-runner-env-name]').value = name;
    row.querySelector('[data-runner-env-value]').value = value;
    row.querySelector('[data-runner-env-remove]').onclick = () => {
      row.remove();
      if (!host.children.length) addRunnerEnvRow();
    };
    host.append(row);
  }
  function renderRunnerEnvRows(entries = []) {
    const host = $('#runnerEnvRows');
    if (!host) return;
    host.replaceChildren();
    const values = Object.entries(entries || {});
    (values.length ? values : [['', '']]).forEach(([name, value]) => addRunnerEnvRow(name, value));
  }
  $('#runnerAddEnv').onclick = () => addRunnerEnvRow();
  window.adroOpenRunnerExecuteDialog = runnerID => {
    const form = $('#runnerExecuteForm');
    form.reset();
    form.elements.runner_id.value = runnerID;
    renderRunnerEnvRows();
    $('#runnerExecuteID').textContent = runnerID;
    $('#runnerExecuteError').textContent = '';
    $('#runnerExecuteDialog').showModal();
    setTimeout(() => form.elements.command.focus(), 0);
  };
  const parseRunnerCommand = value => {
    const tokens = String(value || '').match(/(?:[^\s"]+|"[^"]*")+/g) || [];
    return tokens.map(token => token.startsWith('"') && token.endsWith('"') ? token.slice(1, -1) : token);
  };
  $('#runnerExecuteForm').onsubmit = async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const command = parseRunnerCommand(data.get('command'));
    const env = {};
    const names = new Set();
    for (const row of form.querySelectorAll('[data-runner-env-row]')) {
      const name = String(row.querySelector('[data-runner-env-name]')?.value || '').trim();
      const value = String(row.querySelector('[data-runner-env-value]')?.value || '');
      if (!name && !value.trim()) continue;
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || names.has(name)) {
        $('#runnerExecuteError').textContent = t('runnerExecuteFailed');
        return;
      }
      names.add(name);
      env[name] = value;
    }
    if (!command.length) {
      $('#runnerExecuteError').textContent = t('runnerExecuteFailed');
      return;
    }
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    $('#runnerExecuteError').textContent = '';
    try {
      await api(`/api/v1/runners/${encodeURIComponent(String(data.get('runner_id')))}/execute`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()},
        body: JSON.stringify({command, work_dir: String(data.get('work_dir') || '').trim(), env, timeout_ms: Number(data.get('timeout_ms') || 900000)})
      });
      closeRunnerExecuteDialog();
      await loadCore(true);
    } catch (_) {
      $('#runnerExecuteError').textContent = t('runnerExecuteFailed');
    } finally {
      submit.disabled = false;
    }
  };

  const baseBindViewEvents = bindViewEvents;
  bindViewEvents = function enhancedViewEvents() {
    baseBindViewEvents();
    if (currentView === 'agents') renderAgentCreationJobs();
    document.querySelectorAll('[data-repository-action]').forEach(button => {
      button.onclick = async () => {
        const repository = repositories.find(item => item.id === button.dataset.repositoryId);
        if (!repository) return;
        const action = button.dataset.repositoryAction;
        if (action === 'edit') {
          openRepositoryEditor(repository);
          return;
        }
        if (action === 'browse') {
          await openRepositoryBrowser(repository);
          return;
        }
        if (action === 'delete') {
          if (!window.confirm(t('repositoryDeleteConfirm'))) return;
          button.disabled = true;
          try {
            await api(`/api/v1/repositories/${encodeURIComponent(repository.id)}`, {method: 'DELETE', headers: {'Idempotency-Key': idempotencyKey()}});
            repositories = repositories.filter(item => item.id !== repository.id);
            await loadCore(true);
          } catch (_) {
            button.disabled = false;
            button.title = t('actionFailed');
          }
        }
      };
    });
    const newUserButton = $('#newUser');
    if (newUserButton) newUserButton.addEventListener('click', () => openUserDialog());
    document.querySelectorAll('[data-edit-user]').forEach(button => {
      button.onclick = () => openUserDialog(managedUsers.find(user => user.id === button.dataset.editUser));
    });
    const newSquad = $('#newSquad');
    if (newSquad) newSquad.onclick = openSquadDialog;
    const newPlan = $('#newPlan');
    if (newPlan) newPlan.onclick = openNativePlanDialog;
    document.querySelectorAll('[data-orchestration-action]').forEach(button => {
      button.onclick = () => {
        if (button.dataset.orchestrationKind === 'agent' && button.dataset.orchestrationAction === 'edit') {
          const agent = nativeAgents.find(item => item.id === button.dataset.orchestrationId);
          if (agent) showAgentDialog(false, agent);
          return;
        }
        applyOrchestrationAction(button.dataset.orchestrationKind, button.dataset.orchestrationId, button.dataset.orchestrationAction);
      };
    });
    bindWorkspaceMigration();
  };

  function ensureAgentConfigurationFields() {
    const form = $('#agentForm');
    if (!form || form.elements.avatar_url) return;
    form.elements.description.maxLength = 255;
    const ownerInput = form.elements.member;
    const ownerSelect = document.createElement('select');
    ownerSelect.name = 'member';
    ownerSelect.required = true;
    ownerInput.replaceWith(ownerSelect);
    const modelInput = form.elements.model;
    const modelSelect = document.createElement('select');
    modelSelect.name = 'model';
    modelSelect.id = 'agentModel';
    modelInput.replaceWith(modelSelect);
    $('#agentModelOptions')?.remove();
    const accessField = $('#agentAccessMembersField');
    accessField.outerHTML = `<fieldset id="agentAccessMembersField" class="agent-access-members" hidden><legend data-i18n="agentAccessMembersLabel"></legend><div id="agentAccessMemberOptions" class="agent-resource-options"></div></fieldset>`;
    const builderActions = form.querySelector('.agent-builder-actions');
    builderActions.insertAdjacentHTML('beforebegin', `<div class="agent-step"><span>01</span><div><strong data-i18n="agentStepDescribe"></strong><small data-i18n="agentStepDescribeHelp"></small></div></div>`);
    if (!$('#composeAndCreateAgent')) {
      builderActions.insertAdjacentHTML('afterbegin', `<button class="primary" id="composeAndCreateAgent" type="button"><span aria-hidden="true">＋</span><span data-i18n="agentBuilderCreate"></span></button>`);
    }
    $('#composeAgentDraft')?.remove();
    const starters = form.querySelector('.agent-starters');
    starters?.remove();
    const insertionPoint = form.querySelector('.agent-advanced');
    insertionPoint.insertAdjacentHTML('beforebegin', `
      <label class="agent-avatar-field"><span data-i18n="agentAvatarLabel"></span><div class="agent-avatar-picker"><input name="avatar_url" type="hidden"><input name="avatar_file" type="file" accept="image/png,image/jpeg,image/webp" hidden><button class="secondary" id="agentAvatarButton" type="button"><span aria-hidden="true">⌁</span><span data-i18n="agentUploadAvatar"></span></button><div id="agentAvatarPreview" class="agent-avatar-preview"><span data-i18n="agentAvatarHelp"></span></div></div></label>
      <div class="two-fields agent-resource-fields">
        <fieldset><legend data-i18n="agentSkillsLabel"></legend><div id="agentSkillOptions" class="agent-resource-options"></div></fieldset>
        <fieldset><legend data-i18n="agentMCPServersLabel"></legend><div id="agentMCPServerOptions" class="agent-resource-options"></div></fieldset>
      </div><p class="form-help agent-resource-help" data-i18n="agentResourceCatalogHelp"></p>`);
    form.elements.thinking.closest('.two-fields').insertAdjacentHTML('afterend', `
      <section id="agentRuntimeControls" class="agent-runtime-controls" aria-labelledby="agentRuntimeControlsTitle">
        <h3 id="agentRuntimeControlsTitle" data-i18n="agentRuntimeControls"></h3>
        <div class="two-fields agent-runtime-policy" data-runtime-policy="codex" hidden>
          <label><span data-i18n="agentCodexSandbox"></span><select name="codex_sandbox">
            <option value="" data-i18n="runtimeDefault"></option>
            <option value="read-only" data-i18n="agentSandboxReadOnly"></option>
            <option value="workspace-write" data-i18n="agentSandboxWorkspace"></option>
            <option value="danger-full-access" data-i18n="agentSandboxUnrestricted"></option>
          </select></label>
          <label><span data-i18n="agentCodexApproval"></span><select name="codex_approval">
            <option value="" data-i18n="runtimeDefault"></option>
            <option value="untrusted" data-i18n="agentApprovalUntrusted"></option>
            <option value="on-request" data-i18n="agentApprovalOnRequest"></option>
            <option value="on-failure" data-i18n="agentApprovalOnFailure"></option>
            <option value="never" data-i18n="agentApprovalNever"></option>
          </select></label>
        </div>
        <div class="agent-runtime-policy" data-runtime-policy="openclaw" hidden>
          <div class="two-fields">
            <label><span data-i18n="agentOpenClawMode"></span><select name="openclaw_mode">
              <option value="local" data-i18n="agentOpenClawLocal"></option>
              <option value="gateway" data-i18n="agentOpenClawGateway"></option>
            </select></label>
            <label class="toggle-line"><input name="openclaw_tls" type="checkbox"><span data-i18n="agentGatewayTLS"></span></label>
          </div>
          <div class="two-fields" data-openclaw-gateway>
            <label><span data-i18n="agentGatewayHost"></span><input name="openclaw_host" type="text" autocomplete="off"></label>
            <label><span data-i18n="agentGatewayPort"></span><input name="openclaw_port" type="number" min="1" max="65535" inputmode="numeric"></label>
          </div>
          <div class="two-fields" data-openclaw-gateway>
            <label><span data-i18n="agentGatewayAuthEnv"></span><input name="openclaw_auth_env" type="text" pattern="[A-Za-z_][A-Za-z0-9_]*" autocomplete="off"></label>
            <label><span data-i18n="agentGatewaySecretEnv"></span><input name="openclaw_secret_env" type="text" pattern="[A-Za-z_][A-Za-z0-9_]*" autocomplete="off"></label>
          </div>
        </div>
        <fieldset class="agent-runtime-skills"><legend data-i18n="agentRuntimeSkills"></legend><p class="form-help agent-runtime-skills-help" data-i18n="agentRuntimeSkillsHelp"></p><div id="agentRuntimeSkillOptions" class="agent-resource-options" aria-live="polite"></div></fieldset>
      </section>`);
    const customArgs = form.elements.custom_args.closest('label');
    customArgs.insertAdjacentHTML('beforebegin', `
      <label><span data-i18n="agentRuntimeConfigLabel"></span><textarea name="runtime_config" data-i18n-placeholder="agentRuntimeConfigPlaceholder"></textarea></label>
      <label><span data-i18n="agentEnvironmentLabel"></span><textarea name="environment" data-i18n-placeholder="agentEnvironmentPlaceholder"></textarea></label>`);
    // Keep provider-specific raw launch fields as an internal compatibility
    // buffer for imported/existing Agents. End users configure the supported
    // settings through the runtime controls above; they should not need to
    // write key=value or secret-reference syntax by hand.
    customArgs.hidden = true;
    customArgs.setAttribute('aria-hidden', 'true');
    form.elements.runtime_config.closest('label').hidden = true;
    form.elements.runtime_config.closest('label').setAttribute('aria-hidden', 'true');
    form.elements.environment.closest('label').hidden = true;
    form.elements.environment.closest('label').setAttribute('aria-hidden', 'true');
    $('#agentAvatarButton').onclick = () => form.elements.avatar_file.click();
    form.elements.avatar_file.onchange = () => {
      const file = form.elements.avatar_file.files?.[0];
      const preview = $('#agentAvatarPreview');
      if (!file || !preview) return;
      if (file.size > 5 * 1024 * 1024 || !/^image\/(png|jpeg|webp)$/.test(file.type)) {
        form.elements.avatar_file.value = '';
        preview.textContent = t('agentAvatarHelp');
        $('#agentFormError').textContent = t('agentSaveFailed');
        return;
      }
      preview.innerHTML = `<img src="${escapeHTML(URL.createObjectURL(file))}" alt="${escapeHTML(file.name)}"><span>${escapeHTML(file.name)}</span>`;
    };
    applyTranslations();
  }

  function renderAgentOwnerOptions(agent = null) {
    const select = $('#agentForm').elements.member;
    const members = directory.filter(item => item.status !== 'disabled');
    const currentMember = members.find(item => item.id === currentUser?.id || item.username === currentUser?.username);
    const selected = agent?.owner_id || currentMember?.id || members[0]?.id || currentUser?.id || currentUser?.username || '';
    select.innerHTML = members.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(`${item.display_name || item.username} · ${item.username}`)}</option>`).join('');
    if (selected && !members.some(item => item.id === selected || item.username === selected)) {
      select.insertAdjacentHTML('beforeend', `<option value="${escapeHTML(selected)}">${escapeHTML(t('agentExistingOwner'))}</option>`);
    }
    select.value = members.find(item => item.id === selected || item.username === selected)?.id || selected;
  }

  function renderAgentAccessMemberOptions(selectedIDs = []) {
    const target = $('#agentAccessMemberOptions');
    const selected = new Set(selectedIDs || []);
    const members = directory.filter(item => item.status !== 'disabled');
    target.innerHTML = members.length ? members.map(item => `<label class="agent-resource-option"><input type="checkbox" name="agent_access_member_ids" value="${escapeHTML(item.id)}" ${selected.has(item.id) ? 'checked' : ''}><span><strong>${escapeHTML(item.display_name || item.username)}</strong><small>${escapeHTML(item.username)}</small></span></label>`).join('') : `<span class="form-help">${escapeHTML(t('agentNoMembers'))}</span>`;
  }

  function renderAgentResourceOptions(agent = null) {
    const renderOptions = (target, name, items, selectedIDs, label) => {
      const selected = new Set(selectedIDs || []);
      target.innerHTML = items.length ? items.map(item => {
        const status = String(item.status || '');
        return `<label class="agent-resource-option"><input type="checkbox" name="${name}" value="${escapeHTML(item.id)}" ${selected.has(item.id) ? 'checked' : ''}><span><strong>${escapeHTML(item.name || item.id)}</strong><small>${escapeHTML(label(item))}${status ? ` · ${escapeHTML(status)}` : ''}</small></span></label>`;
      }).join('') : `<span class="form-help">${escapeHTML(t('agentNoResources'))}</span>`;
    };
	const executableSkills = skills.filter(item => item.status === 'active' || item.status === 'published');
	const executableMCPServers = mcpServers.filter(item => !['disabled', 'unreachable', 'failed'].includes(item.status));
	renderOptions($('#agentSkillOptions'), 'agent_skill_ids', executableSkills, agent?.skill_ids, item => item.version || item.kind || 'Skill');
	renderOptions($('#agentMCPServerOptions'), 'agent_mcp_server_ids', executableMCPServers, agent?.mcp_server_ids, item => item.protocol || 'MCP');
  }

  function parseAgentKeyValueLines(value, secretReferences = false) {
    const result = secretReferences ? [] : {};
    const seen = new Set();
    for (const rawLine of String(value || '').split(/\r?\n/)) {
      const line = rawLine.trim();
      if (!line) continue;
      const separator = line.indexOf('=');
      if (separator < 1) throw new Error('invalid key/value line');
      const key = line.slice(0, separator).trim();
      const entryValue = line.slice(separator + 1).trim();
      if (!/^[A-Za-z_][A-Za-z0-9_.-]*$/.test(key) || !entryValue || seen.has(key)) throw new Error('invalid key/value line');
      if (secretReferences) {
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) || !/^env:[A-Za-z_][A-Za-z0-9_]*$/.test(entryValue)) throw new Error('invalid secret reference');
        result.push({name: key, secret_ref: entryValue});
      } else {
        result[key] = entryValue;
      }
      seen.add(key);
    }
    return result;
  }

  function parseAgentCustomArgs(value) {
    const args = [];
    for (const rawLine of String(value || '').split(/\r?\n/)) {
      const line = rawLine.trim();
      if (!line) continue;
      if (line.includes('\u0000')) throw new Error('invalid custom argument');
      args.push(line);
    }
    return args;
  }

  function selectedAgentResourceIDs(name) {
    return Array.from(document.querySelectorAll(`#agentForm input[name="${name}"]:checked`)).map(input => input.value);
  }

  const runtimeManagedConfigKeys = {
    codex: new Set(['sandbox_mode', 'approval_policy']),
    openclaw: new Set(['mode', 'gateway.host', 'gateway.port', 'gateway.tls', 'gateway.auth_env'])
  };

  function runtimeSkillIdentity(skill) {
    return [skill.runtime_id || skill.provider, skill.provider, skill.root, skill.key, skill.plugin || ''].join('\u0000');
  }

  function captureAgentRuntimeSkills() {
    const visible = new Set(agentRuntimeSkillItems.filter(item => item.can_disable).map(item => runtimeSkillIdentity({...item, runtime_id: item.runtime_id || item.provider})));
    agentDisabledRuntimeSkills = agentDisabledRuntimeSkills.filter(item => !visible.has(runtimeSkillIdentity(item)));
    document.querySelectorAll('#agentRuntimeSkillOptions input[data-runtime-skill-index]').forEach(input => {
      if (input.checked) return;
      const item = agentRuntimeSkillItems[Number(input.dataset.runtimeSkillIndex)];
      if (!item?.can_disable) return;
      agentDisabledRuntimeSkills.push({runtime_id: item.runtime_id || item.provider, provider: item.provider, root: item.root, key: item.key, name: item.name || '', plugin: item.plugin || ''});
    });
  }

  async function loadAgentRuntimeSkills(runtimeID) {
    const target = $('#agentRuntimeSkillOptions');
    const requestID = ++agentRuntimeSkillRequest;
    agentRuntimeSkillItems = [];
    target.innerHTML = `<span class="form-help">${escapeHTML(t('agentRuntimeSkillsLoading'))}</span>`;
    if (!runtimeID) {
      target.innerHTML = `<span class="form-help">${escapeHTML(t('agentRuntimeSkillsEmpty'))}</span>`;
      return;
    }
    try {
      const response = await api(`/api/v1/runtimes/${encodeURIComponent(runtimeID)}/skills`);
      if (requestID !== agentRuntimeSkillRequest) return;
      agentRuntimeSkillItems = (response.items || []).map(item => ({...item, runtime_id: runtimeID}));
      const disabled = new Set(agentDisabledRuntimeSkills.map(runtimeSkillIdentity));
      target.innerHTML = agentRuntimeSkillItems.length ? agentRuntimeSkillItems.map((item, index) => {
        const identity = runtimeSkillIdentity(item);
        const checked = !disabled.has(identity);
        const detail = [item.description, item.source_path, item.can_disable ? '' : t('agentRuntimeSkillLocked')].filter(Boolean).join(' · ');
        return `<label class="agent-resource-option ${item.can_disable ? '' : 'locked'}"><input type="checkbox" data-runtime-skill-index="${index}" ${checked ? 'checked' : ''} ${item.can_disable ? '' : 'disabled'}><span><strong>${escapeHTML(item.name || item.key)}</strong><small>${escapeHTML(detail || item.key)}</small></span></label>`;
      }).join('') : `<span class="form-help">${escapeHTML(t('agentRuntimeSkillsEmpty'))}</span>`;
    } catch (_) {
      if (requestID !== agentRuntimeSkillRequest) return;
      target.innerHTML = `<span class="form-help">${escapeHTML(t('agentRuntimeSkillsUnavailable'))}</span>`;
    }
  }

  function updateOpenClawGatewayFields() {
    const form = $('#agentForm');
    if (!form?.elements.openclaw_mode) return;
    const enabled = form.elements.openclaw_mode.value === 'gateway';
    form.querySelectorAll('[data-openclaw-gateway]').forEach(element => { element.hidden = !enabled; });
    form.elements.openclaw_tls.disabled = !enabled;
    for (const name of ['openclaw_host', 'openclaw_port', 'openclaw_auth_env', 'openclaw_secret_env']) {
      form.elements[name].disabled = !enabled;
    }
  }

  function renderAgentRuntimeConfiguration(form, runtimeID, config = {}, environment = []) {
    form.querySelectorAll('[data-runtime-policy]').forEach(panel => { panel.hidden = panel.dataset.runtimePolicy !== runtimeID; });
    const managed = runtimeManagedConfigKeys[runtimeID] || new Set();
    form.elements.runtime_config.value = Object.entries(config).filter(([key]) => !managed.has(key)).sort(([left], [right]) => left.localeCompare(right)).map(([key, value]) => `${key}=${value}`).join('\n');
    form.elements.environment.value = (environment || []).map(item => `${item.name}=${item.secret_ref}`).join('\n');
    form.elements.codex_sandbox.value = runtimeID === 'codex' ? (config.sandbox_mode || '') : '';
    form.elements.codex_approval.value = runtimeID === 'codex' ? (config.approval_policy || '') : '';
    form.elements.openclaw_mode.value = runtimeID === 'openclaw' ? (config.mode || 'local') : 'local';
    form.elements.openclaw_host.value = runtimeID === 'openclaw' ? (config['gateway.host'] || '') : '';
    form.elements.openclaw_port.value = runtimeID === 'openclaw' ? (config['gateway.port'] || '') : '';
    form.elements.openclaw_tls.checked = runtimeID === 'openclaw' && config['gateway.tls'] === 'true';
    const authEnv = runtimeID === 'openclaw' ? (config['gateway.auth_env'] || '') : '';
    form.elements.openclaw_auth_env.value = authEnv;
    const authReference = (environment || []).find(item => item.name === authEnv)?.secret_ref || '';
    form.elements.openclaw_secret_env.value = authReference.startsWith('env:') ? authReference.slice(4) : '';
    updateOpenClawGatewayFields();
  }

  function collectAgentRuntimeConfiguration(form, runtimeID) {
    const config = parseAgentKeyValueLines(form.elements.runtime_config.value);
    const environment = parseAgentKeyValueLines(form.elements.environment.value, true);
    if (runtimeID === 'codex') {
      delete config.sandbox_mode;
      delete config.approval_policy;
      if (form.elements.codex_sandbox.value) config.sandbox_mode = form.elements.codex_sandbox.value;
      if (form.elements.codex_approval.value) config.approval_policy = form.elements.codex_approval.value;
    }
    if (runtimeID === 'openclaw') {
      config.mode = form.elements.openclaw_mode.value || 'local';
      for (const key of ['gateway.host', 'gateway.port', 'gateway.tls', 'gateway.auth_env']) delete config[key];
      if (config.mode === 'gateway') {
        const host = form.elements.openclaw_host.value.trim();
        const port = form.elements.openclaw_port.value.trim();
        const authEnv = form.elements.openclaw_auth_env.value.trim();
        const secretEnv = form.elements.openclaw_secret_env.value.trim();
        if (host) config['gateway.host'] = host;
        if (port) config['gateway.port'] = port;
        if (form.elements.openclaw_tls.checked) config['gateway.tls'] = 'true';
        if (authEnv) config['gateway.auth_env'] = authEnv;
        if (authEnv && secretEnv) {
          const index = environment.findIndex(item => item.name === authEnv);
          const reference = {name: authEnv, secret_ref: `env:${secretEnv}`};
          if (index >= 0) environment[index] = reference;
          else environment.push(reference);
        }
      }
    }
    return {config, environment};
  }

  function saveCurrentAgentRuntimeState(form) {
    const runtimeID = form.dataset.loadedRuntime || '';
    if (!runtimeID) return;
    captureAgentRuntimeSkills();
    try {
      const state = collectAgentRuntimeConfiguration(form, runtimeID);
      agentRuntimeConfigs.set(runtimeID, state.config);
      agentRuntimeEnvironments.set(runtimeID, state.environment);
    } catch (_) {}
  }

  async function changeAgentRuntime() {
    const form = $('#agentForm');
    saveCurrentAgentRuntimeState(form);
    const runtimeID = form.elements.runtime.value;
    form.dataset.loadedRuntime = runtimeID;
    form.elements.model.value = '';
    form.elements.thinking.value = '';
    form.elements.service_tier.value = '';
    renderAgentRuntimeConfiguration(form, runtimeID, agentRuntimeConfigs.get(runtimeID) || {}, agentRuntimeEnvironments.get(runtimeID) || []);
    await Promise.all([loadAgentModelCatalog(), loadAgentRuntimeSkills(runtimeID)]);
  }

  ensureAgentConfigurationFields();
  renderAgentModelOptions = function renderAgentModelSelectOptions() {
    const model = $('#agentModel');
    const thinking = $('#agentThinking');
    const tier = $('#agentServiceTier');
    const previous = model.value;
    model.innerHTML = `<option value="">${escapeHTML(t('runtimeDefault'))}</option>` + agentModelCatalog.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.label || item.id)}</option>`).join('');
    if (agentModelCatalog.some(item => item.id === previous)) model.value = previous;
    else model.value = agentModelCatalog.find(item => item.default)?.id || '';
    const refresh = () => {
      const selected = agentModelCatalog.find(item => item.id === model.value);
      thinking.innerHTML = `<option value="">${escapeHTML(t('runtimeDefault'))}</option>` + ((selected?.thinking?.supported_levels) || []).map(item => `<option value="${escapeHTML(item.value)}">${escapeHTML(item.label || item.value)}</option>`).join('');
      tier.innerHTML = `<option value="">${escapeHTML(t('runtimeDefault'))}</option>` + (selected?.service_tiers || []).map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name || item.id)}</option>`).join('');
      thinking.value = selected?.thinking?.default_level || '';
    };
    model.onchange = refresh;
    refresh();
  };
  showAgentDialog = async function showAgentDialogWithMigration(onboarding = false, agent = null) {
    onboarding = onboarding === true;
    const form = $('#agentForm');
    form.reset();
    delete form.dataset.agentId;
    delete form.dataset.agentRevision;
    delete form.dataset.loadedRuntime;
    agentRuntimeConfigs = new Map();
    agentRuntimeEnvironments = new Map();
    agentPreservedCustomArgs = (agent?.executor_binding?.custom_args || []).slice();
    agentDisabledRuntimeSkills = (agent?.disabled_runtime_skills || []).map(item => ({...item}));
    agentRuntimeSkillItems = [];
    form.dataset.onboarding = onboarding ? 'true' : '';
    document.body.classList.toggle('onboarding-active', onboarding);
    $('#closeAgentDialog').hidden = onboarding;
    $('#cancelAgentDialog').hidden = onboarding;
    $('#agentFormError').textContent = '';
    renderAgentOwnerOptions(agent);
    renderAgentAccessMemberOptions(agent?.access_policy?.member_ids || []);
    if (onboarding) {
      form.elements.name.value = t('defaultAgentName');
      form.elements.role.value = 'generalist';
      form.elements.instructions.value = t('defaultAgentInstructions');
    }
    const runtimeSelect = form.elements.runtime;
    runtimeSelect.innerHTML = '';
    const submitButton = form.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    form.dataset.agentId = agent?.id || '';
    form.dataset.agentRevision = agent?.revision ? String(agent.revision) : '';
    $('#agentBuilderStatus').textContent = '';
    const title = $('#agentDialog h2');
    title.dataset.i18n = agent ? 'agentEditTitle' : 'agentCreateTitle';
    title.textContent = t(title.dataset.i18n);
    const dialogState = $('#agentDialogState');
    if (dialogState) dialogState.textContent = t('agentReadyState');
    const submitLabel = form.querySelector('button[type="submit"] [data-i18n]');
    submitLabel.dataset.i18n = agent ? 'agentSave' : 'agentCreate';
    submitLabel.textContent = t(submitLabel.dataset.i18n);
    let panel = form.querySelector('[data-workspace-migration]');
    if (onboarding && !panel) {
      form.insertAdjacentHTML('afterbegin', workspaceMigrationPanel(true));
      panel = form.querySelector('[data-workspace-migration]');
    }
    if (panel) panel.hidden = !onboarding;
    bindWorkspaceMigration(form);
    renderAgentResourceOptions(agent);
    if (!$('#agentDialog').open) $('#agentDialog').showModal();
    try {
      const result = await api('/api/v1/runtimes/discovered');
      for (const runtime of result.items || []) {
        const option = document.createElement('option');
        option.value = runtime.id;
        option.dataset.modelUnsupported = runtime.model_selection_unsupported ? 'true' : 'false';
        option.textContent = `${runtime.name}${runtime.installed ? '' : ' — ' + t('notInstalled')}${runtime.adapter_available ? '' : ' — ' + t('adapterUnavailable')}`;
        option.disabled = !runtime.installed || !runtime.adapter_available;
        runtimeSelect.append(option);
      }
      if (!agent) {
        const localRuntime = Array.from(runtimeSelect.options).find(option => option.value === 'codex' && !option.disabled) || Array.from(runtimeSelect.options).find(option => !option.disabled);
        if (localRuntime) runtimeSelect.value = localRuntime.value;
      }
      await loadAgentModelCatalog();
      submitButton.disabled = !Array.from(runtimeSelect.options).some(option => !option.disabled);
    } catch (_) {
      $('#agentFormError').textContent = t('runtimeDiscoveryFailed');
      submitButton.disabled = true;
    }
    if (agent) {
      form.elements.name.value = agent.name || '';
      form.elements.description.value = agent.description || '';
      form.elements.avatar_url.value = agent.avatar_url || '';
      form.elements.role.value = agent.role || '';
      form.elements.instructions.value = agent.instructions || '';
      form.elements.runtime.value = agent.executor_binding?.runtime_id || '';
      await loadAgentModelCatalog();
      form.elements.model.value = agent.executor_binding?.model || '';
      renderAgentModelOptions();
      form.elements.thinking.value = agent.executor_binding?.thinking_level || '';
      form.elements.service_tier.value = agent.executor_binding?.service_tier || '';
      form.elements.custom_args.value = agentPreservedCustomArgs.join('\n');
      form.elements.access_mode.value = agent.access_policy?.mode || 'private';
      form.elements.concurrent.value = String(agent.concurrency_budget?.concurrent || 1);
      form.elements.tokens.value = String(agent.concurrency_budget?.tokens || 120000);
      form.elements.tool_calls.value = String(agent.concurrency_budget?.tool_calls || 200);
      form.elements.network.checked = agent.tool_policy?.network === true;
      agentRuntimeConfigs.set(form.elements.runtime.value, {...(agent.executor_binding?.runtime_config || {})});
      agentRuntimeEnvironments.set(form.elements.runtime.value, (agent.executor_binding?.environment || []).map(item => ({...item})));
      applyAgentDraft(form, {...agent, network_access: agent.tool_policy?.network, max_concurrent_tasks: agent.concurrency_budget?.concurrent, token_budget: agent.concurrency_budget?.tokens, tool_call_budget: agent.concurrency_budget?.tool_calls});
    }
    const runtimeID = form.elements.runtime.value;
    form.dataset.loadedRuntime = runtimeID;
    renderAgentRuntimeConfiguration(form, runtimeID, agentRuntimeConfigs.get(runtimeID) || {}, agentRuntimeEnvironments.get(runtimeID) || []);
    form.elements.runtime.onchange = changeAgentRuntime;
    form.elements.openclaw_mode.onchange = updateOpenClawGatewayFields;
    await loadAgentRuntimeSkills(runtimeID);
    setTimeout(() => form.elements.builder_prompt.focus(), 0);
  };

  function agentFormStarters(form) {
    const starters = [];
    for (let index = 1; index <= 3; index += 1) {
      const label = String(form.elements[`starter_label_${index}`]?.value || '').trim();
      const prompt = String(form.elements[`starter_prompt_${index}`]?.value || '').trim();
      if (!label && !prompt) continue;
      if (!label || !prompt) throw new Error('incomplete conversation starter');
      starters.push({label, prompt});
    }
    return starters;
  }

  function currentAgentDraft(form) {
    return {
      name: String(form.elements.name.value || '').trim(),
      description: String(form.elements.description.value || '').trim(),
      role: String(form.elements.role.value || '').trim(),
      instructions: String(form.elements.instructions.value || '').trim(),
      conversation_starters: agentFormStarters(form),
      access_policy: {mode: String(form.elements.access_mode.value || 'private')},
      skill_ids: selectedAgentResourceIDs('agent_skill_ids'),
      mcp_server_ids: selectedAgentResourceIDs('agent_mcp_server_ids'),
      network_access: form.elements.network.checked,
      max_concurrent_tasks: Number(form.elements.concurrent.value || 1),
      token_budget: Number(form.elements.tokens.value || 120000),
      tool_call_budget: Number(form.elements.tool_calls.value || 200)
    };
  }

  function applyAgentDraft(form, draft) {
    form.elements.name.value = draft.name || '';
    form.elements.description.value = draft.description || '';
    form.elements.role.value = draft.role || '';
    form.elements.instructions.value = draft.instructions || '';
    form.elements.access_mode.value = draft.access_policy?.mode || 'private';
    form.elements.network.checked = draft.network_access === true;
    form.elements.concurrent.value = String(draft.max_concurrent_tasks || 1);
    form.elements.tokens.value = String(draft.token_budget || 120000);
    form.elements.tool_calls.value = String(draft.tool_call_budget || 200);
    const starters = Array.isArray(draft.conversation_starters) ? draft.conversation_starters : [];
    for (let index = 1; index <= 3; index += 1) {
      const label = form.elements[`starter_label_${index}`];
      const prompt = form.elements[`starter_prompt_${index}`];
      if (label) label.value = starters[index - 1]?.label || '';
      if (prompt) prompt.value = starters[index - 1]?.prompt || '';
    }
    if (Array.isArray(draft.skill_ids)) {
      form.querySelectorAll('input[name="agent_skill_ids"]').forEach(input => { input.checked = draft.skill_ids.includes(input.value); });
    }
    if (Array.isArray(draft.mcp_server_ids)) {
      form.querySelectorAll('input[name="agent_mcp_server_ids"]').forEach(input => { input.checked = draft.mcp_server_ids.includes(input.value); });
    }
    updateAgentAccessFields();
  }

  function updateAgentAccessFields() {
    const field = $('#agentAccessMembersField');
    if (field) field.hidden = $('#agentAccessMode').value !== 'members';
  }

  async function composeAgentDraft(createAfter = false) {
    const form = $('#agentForm');
    const buttons = [$('#composeAndCreateAgent')].filter(Boolean);
    const status = $('#agentBuilderStatus');
    const prompt = String(form.elements.builder_prompt.value || '').trim();
    if (!prompt) {
      status.textContent = t('agentBuilderFailed');
      return false;
    }
    const jobID = createAfter ? idempotencyKey() : '';
    if (jobID) {
      form.dataset.creationJobID = jobID;
      upsertAgentCreationJob({id: jobID, name: String(form.elements.name.value || '').trim() || t('agentCreateTitle'), prompt, status: 'running', started_at: new Date().toISOString()});
    }
    buttons.forEach(button => { button.disabled = true; });
    status.textContent = t('agentBuilderRunning');
    const dialogState = $('#agentDialogState');
    if (dialogState) dialogState.textContent = t('agentCreatingState');
    $('#agentFormError').textContent = '';
    try {
      const body = {
        prompt,
        runtime_id: String(form.elements.runtime.value || '').trim(),
        model: String(form.elements.model.value || '').trim(),
        thinking_level: String(form.elements.thinking.value || '').trim(),
        service_tier: String(form.elements.service_tier.value || '').trim(),
        current_draft: currentAgentDraft(form)
      };
      const response = await api('/api/v1/workspaces/local/agents/compose', {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(body)});
      applyAgentDraft(form, response.draft || {});
      status.textContent = t('agentBuilderDone');
      if (createAfter) form.requestSubmit();
      return true;
    } catch (error) {
      status.textContent = t('agentBuilderFailed');
      $('#agentFormError').textContent = error.detail || error.message || t('agentCreateError');
      if (dialogState) dialogState.textContent = t('agentNeedsAttentionState');
      if (jobID) upsertAgentCreationJob({id: jobID, name: String(form.elements.name.value || '').trim() || t('agentCreateTitle'), prompt, status: 'failed', error: error.detail || error.message || t('agentCreateError')});
      return false;
    } finally {
      buttons.forEach(button => { button.disabled = false; });
    }
  }

  ensureOrchestrationDialogs();
	$('#composeAndCreateAgent').onclick = () => composeAgentDraft(true);
	$('#agentAccessMode').onchange = updateAgentAccessFields;
	updateAgentAccessFields();
  $('#agentForm').onsubmit = async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const member = String(data.get('member') || '').trim();
    const name = String(data.get('name') || '').trim();
	const description = String(data.get('description') || '').trim();
    const instructions = String(data.get('instructions') || '').trim();
    const role = String(data.get('role') || '').trim() || 'developer';
    const runtime = String(data.get('runtime') || '').trim();
    const model = String(data.get('model') || '').trim();
    const thinking = String(data.get('thinking') || '').trim();
    const serviceTier = String(data.get('service_tier') || '').trim();
    let customArgs, runtimeConfig, environment;
    try {
      customArgs = parseAgentCustomArgs(String(data.get('custom_args') || ''));
      const runtimeState = collectAgentRuntimeConfiguration(form, runtime);
      runtimeConfig = runtimeState.config;
      environment = runtimeState.environment;
      captureAgentRuntimeSkills();
    } catch (_) {
      $('#agentFormError').textContent = t('agentSaveFailed');
      return;
    }
    const accessMode = String(data.get('access_mode') || 'private').trim();
    const accessMembers = selectedAgentResourceIDs('agent_access_member_ids');
    let starters;
    try { starters = agentFormStarters(form); } catch (_) { $('#agentFormError').textContent = t('agentSaveFailed'); return; }
    const nativeBody = {
      name, owner_id: member, description, avatar_url: String(data.get('avatar_url') || '').trim(), role, instructions, conversation_starters: starters,
      access_policy: {mode: accessMode, member_ids: accessMode === 'members' ? accessMembers : []},
      skill_ids: selectedAgentResourceIDs('agent_skill_ids'), disabled_runtime_skills: agentDisabledRuntimeSkills, mcp_server_ids: selectedAgentResourceIDs('agent_mcp_server_ids'),
      status: 'active', created_by: currentUser?.id || member,
      capabilities: [{name: 'session.start', version: 'v1'}, {name: 'stream.events', version: 'v1'}],
      executor_binding: {provider_id: rootInfo.provider || 'local', runtime_id: runtime, model, thinking_level: thinking, service_tier: serviceTier, custom_args: customArgs, runtime_config: runtimeConfig, environment, required_caps: ['run.snapshot.v1'], config_version: 'web-v4'},
      concurrency_budget: {tokens: Number(data.get('tokens') || 120000), tool_calls: Number(data.get('tool_calls') || 200), concurrent: Number(data.get('concurrent') || 1)},
      input_schema: {id: 'adro.context-envelope', version: 1}, output_schema: {id: 'adro.structured-result', version: 1},
      tool_policy: {network: data.get('network') === 'on'}, memory_policy: {require_evidence: true}
    };
    $('#agentFormError').textContent = '';
    try {
      const agentID = String(form.dataset.agentId || '');
      const requestBody = agentID ? {...nativeBody, expected_revision: Number(form.dataset.agentRevision)} : nativeBody;
      if (agentID) delete requestBody.created_by;
      const savedAgent = await api(agentID ? `/api/v1/workspaces/local/agents/${encodeURIComponent(agentID)}` : '/api/v1/workspaces/local/agents', {method: agentID ? 'PATCH' : 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(requestBody)});
      const avatarFile = form.elements.avatar_file?.files?.[0];
      if (avatarFile && savedAgent?.id) {
        const avatarURL = await uploadAgentAvatar(savedAgent.id, avatarFile);
        if (avatarURL) await api(`/api/v1/workspaces/local/agents/${encodeURIComponent(savedAgent.id)}`, {method: 'PATCH', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify({expected_revision: savedAgent.revision, avatar_url: avatarURL})});
      }
      const jobID = form.dataset.creationJobID;
      if (jobID) upsertAgentCreationJob({id: jobID, name: savedAgent?.name || name, agent_id: savedAgent?.id, status: 'done', finished_at: new Date().toISOString()});
      if ($('#agentDialogState')) $('#agentDialogState').textContent = t('agentCreatedState');
      agentPreservedCustomArgs = customArgs;
      delete form.dataset.onboarding; delete form.dataset.agentId; delete form.dataset.agentRevision; delete form.dataset.loadedRuntime; document.body.classList.remove('onboarding-active'); closeAgentDialog(); form.reset(); await loadCore(true);
    } catch (error) {
      const jobID = form.dataset.creationJobID;
      if (jobID) upsertAgentCreationJob({id: jobID, name, status: 'failed', error: error.detail || error.message || t('agentCreateError')});
      if ($('#agentDialogState')) $('#agentDialogState').textContent = t('agentNeedsAttentionState');
      $('#agentFormError').textContent = error.detail || error.message || t('agentSaveFailed');
    }
  };

  function commentRosterLabel(item) {
    return `${item.name || item.id} · ${item.type === 'squad' ? t('mentionSquad') : t('mentionAgent')}`;
  }

  async function loadCommentMentionRoster() {
    if (commentMentionRosterPromise) return commentMentionRosterPromise;
    commentMentionRosterPromise = Promise.allSettled([
      api('/api/v1/workspaces/local/agents'),
      api('/api/v1/workspaces/local/squads')
    ]).then(results => {
      const agents = results[0].status === 'fulfilled' ? results[0].value.items || [] : nativeAgents;
      const squads = results[1].status === 'fulfilled' ? results[1].value.items || [] : nativeSquads;
      commentMentionRoster = [
        ...agents.filter(item => item.status === 'active').map(item => ({type: 'agent', id: item.id, name: item.name || item.id, revision: item.revision})),
        ...squads.filter(item => item.status === 'published').map(item => ({type: 'squad', id: item.id, name: item.name || item.id, revision: item.published_version}))
      ];
      return commentMentionRoster;
    }).finally(() => { commentMentionRosterPromise = null; });
    return commentMentionRosterPromise;
  }

  function commentMentionContext(textarea) {
    if (!textarea) return null;
    const cursor = textarea.selectionStart;
    const before = textarea.value.slice(0, cursor);
    const match = before.match(/(?:^|\s)@([^\s@]*)$/);
    if (!match) return null;
    return {start: cursor - match[1].length - 1, end: cursor, query: match[1].toLowerCase()};
  }

  function hideCommentMentionMenu() {
    const menu = $('#commentMentionMenu');
    if (menu) menu.hidden = true;
    commentMentionOptions = [];
    commentMentionIndex = -1;
    commentMentionStart = -1;
    commentMentionTargetID = '';
  }

  function renderCommentMentionMenu() {
    const menu = $('#commentMentionMenu');
    if (!menu) return;
    if (!commentMentionOptions.length) {
      menu.hidden = true;
      return;
    }
    menu.innerHTML = commentMentionOptions.map((item, index) => `<button type="button" class="comment-mention-option ${index === commentMentionIndex ? 'selected' : ''}" data-comment-mention-index="${index}"><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(commentRosterLabel(item))}</small></button>`).join('');
    menu.hidden = false;
    menu.querySelectorAll('[data-comment-mention-index]').forEach(button => {
      button.onclick = () => insertCommentMention(Number(button.dataset.commentMentionIndex));
    });
  }

  function updateCommentMentionMenu() {
    const textarea = $('#commentInput');
    const context = commentMentionContext(textarea);
    if (!context) {
      hideCommentMentionMenu();
      return;
    }
    commentMentionStart = context.start;
    commentMentionOptions = commentMentionRoster.filter(item => `${item.name} ${item.id}`.toLowerCase().includes(context.query)).slice(0, 8);
    if (!commentMentionOptions.length) {
      hideCommentMentionMenu();
      return;
    }
    commentMentionIndex = Math.min(Math.max(commentMentionIndex, 0), commentMentionOptions.length - 1);
    renderCommentMentionMenu();
  }

  function insertCommentMention(index) {
    const textarea = $('#commentInput');
    const option = commentMentionOptions[index];
    if (!textarea || !option || commentMentionStart < 0) return;
    const before = textarea.value.slice(0, commentMentionStart);
    const after = textarea.value.slice(textarea.selectionEnd);
    const display = String(option.name || option.id).replace(/[\[\]()]/g, '');
    const markup = `[@${display}](mention://${option.type}/${option.id})`;
    textarea.value = `${before}${markup} ${after}`;
    const cursor = before.length + markup.length + 1;
    textarea.setSelectionRange(cursor, cursor);
    commentMentionTargetID = option.id;
    hideCommentMentionMenu();
    textarea.focus();
  }

  function commentStatusKey(status) {
    return {
      queued: 'outcomeQueued', coalesced: 'outcomeCoalesced', deferred: 'outcomeDeferred', blocked: 'outcomeBlocked',
      started: 'outcomeStarted', dispatching: 'outcomeQueued', running: 'outcomeRunning', completed: 'outcomeCompleted',
      failed: 'outcomeFailed', retrying: 'outcomeRetrying', cancelled: 'outcomeCancelled', timed_out: 'outcomeTimedOut',
      not_requested: 'outcomeNotRequested', broadcast: 'outcomeBroadcast', unavailable: 'outcomeBlocked', rejected: 'outcomeBlocked'
    }[String(status || '').toLowerCase()] || '';
  }

  function commentStatusLabel(status) {
    const key = commentStatusKey(status);
    return key ? t(key) : String(status || '-');
  }

  function commentStatusClass(status) {
    if (['completed'].includes(String(status).toLowerCase())) return 'good';
    if (['blocked', 'failed', 'cancelled', 'timed_out', 'unavailable', 'rejected'].includes(String(status).toLowerCase())) return 'bad';
    if (['deferred', 'retrying'].includes(String(status).toLowerCase())) return 'warn';
    return 'active';
  }

  function renderCommentContent(content) {
    const value = String(content || '');
    const mention = /\[([^\]]*)\]\(mention:\/\/(agent|squad)\/([^\)]+)\)/g;
    let output = '';
    let cursor = 0;
    let match;
    while ((match = mention.exec(value))) {
      output += escapeHTML(value.slice(cursor, match.index));
      output += `<span class="comment-mention" title="${escapeHTML(`${match[2]}:${match[3]}`)}">${escapeHTML(match[1])}</span>`;
      cursor = match.index + match[0].length;
    }
    return output + escapeHTML(value.slice(cursor));
  }

  function commentAuthorLabel(comment) {
    if (comment.author_type === 'agent') return `${t('mentionAgent')} · ${comment.author_id}`;
    if (comment.author_type === 'system') return comment.author_id || 'system';
    return userLabel(comment.author_id);
  }

  function commentActivityHTML(comment) {
    const activity = commentActivity.get(comment.id) || {};
    const outcomes = activity.outcomes || comment.trigger_outcomes || [];
    const followUps = activity.followUps || [];
    const attachments = activity.attachments || [];
    const outcomeMarkup = outcomes.map(outcome => `<span class="comment-outcome ${commentStatusClass(outcome.status)}"><b>${escapeHTML(outcome.target_type || 'target')}</b><em>${escapeHTML(commentStatusLabel(outcome.status))}</em></span>`).join('');
    const receiptMarkup = followUps.map(receipt => `<div class="comment-receipt"><span class="comment-outcome ${commentStatusClass(receipt.status)}"><b>${escapeHTML(receipt.dispatch_target_type || 'agent')}:${escapeHTML(receipt.dispatch_target_id || receipt.agent_binding_id || '-')}</b><em>${escapeHTML(commentStatusLabel(receipt.status))}</em></span><small>${escapeHTML(receipt.provider_run_id || receipt.outbox_id || receipt.reason || '')}</small></div>`).join('');
    const attachmentMarkup = attachments.map(item => `<div class="comment-attachment"><span>${escapeHTML(item.filename || item.id)}</span><small>${escapeHTML(formatBytes(item.size_bytes || 0))}</small></div>`).join('');
    const retryable = outcomes.some(item => ['blocked', 'deferred'].includes(item.status)) || followUps.some(item => ['failed', 'retrying', 'cancelled', 'timed_out', 'unavailable', 'rejected'].includes(item.status));
    if (!outcomeMarkup && !receiptMarkup && !attachmentMarkup && !activity.loading) return '';
    return `<div class="comment-activity">${outcomeMarkup ? `<div class="comment-activity-row"><strong>${escapeHTML(t('commentOutcome'))}</strong><div class="comment-outcomes">${outcomeMarkup}</div></div>` : ''}${receiptMarkup ? `<div class="comment-activity-row"><strong>${escapeHTML(t('commentFollowUp'))}</strong><div class="comment-receipts">${receiptMarkup}</div></div>` : ''}${attachmentMarkup ? `<div class="comment-attachments">${attachmentMarkup}</div>` : ''}${activity.loading ? `<small class="form-help">${escapeHTML(t('loading'))}</small>` : ''}${retryable ? `<button type="button" class="comment-retry" data-comment-retry="${escapeHTML(comment.id)}">${escapeHTML(t('retryTrigger'))}</button>` : ''}</div>`;
  }

  function commentTreeMarkup(items) {
    const byParent = new Map();
    items.forEach(item => {
      const parent = item.parent_id || '';
      if (!byParent.has(parent)) byParent.set(parent, []);
      byParent.get(parent).push(item);
    });
    const rendered = new Set();
    const renderNode = (comment, depth) => {
      if (!comment || rendered.has(comment.id)) return '';
      rendered.add(comment.id);
      const children = (byParent.get(comment.id) || []).map(child => renderNode(child, Math.min(depth + 1, 4))).join('');
      const time = comment.created_at ? new Date(comment.created_at).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') : '';
      return `<article class="comment-item" style="--comment-depth:${depth}" data-comment-id="${escapeHTML(comment.id)}"><header><div><strong>${escapeHTML(commentAuthorLabel(comment))}</strong><small>${escapeHTML(time)} · r${escapeHTML(String(comment.revision || 1))}</small></div><button type="button" class="comment-reply" data-comment-reply="${escapeHTML(comment.id)}">${escapeHTML(t('reply'))}</button></header><p>${renderCommentContent(comment.content)}</p>${commentActivityHTML(comment)}${children}</article>`;
    };
    const roots = items.filter(item => !item.parent_id || !items.some(candidate => candidate.id === item.parent_id));
    return roots.map(item => renderNode(item, 0)).join('') || `<p class="comment-empty">${escapeHTML(t('noComments'))}</p>`;
  }

  function renderCommentThread() {
    const thread = $('#commentThread');
    if (!thread) return;
    thread.innerHTML = commentTreeMarkup(activeCommentItems);
    thread.querySelectorAll('[data-comment-reply]').forEach(button => {
      button.onclick = () => startCommentReply(button.dataset.commentReply);
    });
    thread.querySelectorAll('[data-comment-retry]').forEach(button => {
      button.onclick = () => retryCommentTriggers(button.dataset.commentRetry);
    });
  }

  function renderCommentPreview(outcomes) {
    const target = $('#commentPreview');
    if (!target) return;
    const list = Array.isArray(outcomes) ? outcomes : [];
    target.hidden = false;
    target.innerHTML = `<div class="comment-preview-head"><strong>${escapeHTML(t('commentPreview'))}</strong><span>${escapeHTML(list.length ? `${list.length} ${t('triggerOutcomes')}` : t('commentPreviewNoTargets'))}</span></div>${list.length ? `<div class="comment-outcomes">${list.map(outcome => `<span class="comment-outcome ${commentStatusClass(outcome.status)}"><b>${escapeHTML(`${outcome.target_type || 'target'}:${outcome.target_id || '-'}`)}</b><em>${escapeHTML(commentStatusLabel(outcome.status))}</em></span>`).join('')}</div><p class="form-help">${escapeHTML(list.map(item => item.reason).filter(Boolean).join('; '))}</p>` : ''}`;
  }

  function setCommentComposerContext() {
    const context = $('#commentComposerContext');
    const label = $('#commentComposerContextText');
    if (!context || !label) return;
    const parent = activeCommentItems.find(item => item.id === commentReplyParentID);
    context.hidden = !parent;
    label.textContent = parent ? `${t('commentReplyingTo')}: ${commentAuthorLabel(parent)}` : '';
  }

  function startCommentReply(commentID) {
    commentReplyParentID = commentID;
    setCommentComposerContext();
    $('#commentInput')?.focus();
  }

  function cancelCommentReply() {
    commentReplyParentID = '';
    setCommentComposerContext();
    $('#commentInput')?.focus();
  }

  async function loadCommentActivity(commentID) {
    const existing = commentActivity.get(commentID) || {};
    commentActivity.set(commentID, {...existing, loading: true});
    renderCommentThread();
    const results = await Promise.allSettled([
      api(`/api/v1/comments/${encodeURIComponent(commentID)}/trigger-outcomes`),
      api(`/api/v1/comments/${encodeURIComponent(commentID)}/follow-up`),
      api(`/api/v1/attachments?owner_type=comment&owner_id=${encodeURIComponent(commentID)}`)
    ]);
    const outcomeResponse = results[0].status === 'fulfilled' ? results[0].value : {};
    const followUpResponse = results[1].status === 'fulfilled' ? results[1].value : {};
    const attachmentResponse = results[2].status === 'fulfilled' ? results[2].value : {};
    commentActivity.set(commentID, {loading: false, outcomes: outcomeResponse.trigger_outcomes || [], followUps: followUpResponse.follow_ups || (followUpResponse.follow_up ? [followUpResponse.follow_up] : []), attachments: attachmentResponse.items || []});
    if (activeCommentTargetID) renderCommentThread();
  }

  function commentTargetPath(targetType, targetID, suffix = '') {
    return `/api/v1/${targetType === 'bug' ? 'bugs' : 'requirements'}/${encodeURIComponent(targetID)}${suffix}`;
  }

  async function loadCommentThread(targetType, targetID, initialItems = []) {
    if (activeCommentTargetType !== targetType || activeCommentTargetID !== targetID) return;
    try {
      let cursor = '';
      const seen = new Set();
      const items = [];
      while (true) {
        const suffix = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
        const response = await api(`${commentTargetPath(targetType, targetID, '/comments')}?limit=250${suffix}`);
        for (const item of response?.items || []) {
          if (item?.id && !items.some(existing => existing.id === item.id)) items.push(item);
        }
        const next = String(response?.next_cursor || '');
        if (!next || next === cursor || seen.has(next)) break;
        seen.add(next);
        cursor = next;
      }
      if (activeCommentTargetType !== targetType || activeCommentTargetID !== targetID) return;
      activeCommentItems = items.length ? items : initialItems || [];
      commentActivity = new Map();
      renderCommentThread();
      await Promise.all(activeCommentItems.map(item => loadCommentActivity(item.id)));
    } catch (_) {
      const thread = $('#commentThread');
      if (thread) thread.innerHTML = `<p class="comment-empty bad">${escapeHTML(t('commentLoadFailed'))}</p>`;
    }
  }

  async function previewCommentTriggers() {
    const input = $('#commentInput');
    const status = $('#commentComposerStatus');
    if (!input || !activeCommentTargetID) return;
    try {
      const response = await api(commentTargetPath(activeCommentTargetType, activeCommentTargetID, '/comments/trigger-preview'), {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({comment_id: `preview-${idempotencyKey()}`, revision: 1, content: input.value})});
      renderCommentPreview(response.trigger_outcomes || []);
      if (status) status.textContent = t('commentPreviewReady');
    } catch (_) {
      if (status) status.textContent = t('commentPreviewFailed');
    }
  }

  async function retryCommentTriggers(commentID) {
    const button = document.querySelector(`[data-comment-retry="${CSS.escape(commentID)}"]`);
    if (button) button.disabled = true;
    const status = $('#commentComposerStatus');
    try {
      const response = await api(`/api/v1/comments/${encodeURIComponent(commentID)}/trigger-retry`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: '{}'});
      const comment = activeCommentItems.find(item => item.id === commentID);
      if (comment && response.trigger_outcomes) comment.trigger_outcomes = response.trigger_outcomes;
      await loadCommentActivity(commentID);
      if (status) status.textContent = t('commentPreviewReady');
    } catch (_) {
      if (status) status.textContent = t('commentSendFailed');
      if (button) button.disabled = false;
    }
  }

  async function submitComment(event) {
    event.preventDefault();
    const input = $('#commentInput');
    const status = $('#commentComposerStatus');
    const form = event.currentTarget;
    const content = String(input?.value || '').trim();
    if (!content) {
      if (status) status.textContent = t('commentEmpty');
      input?.focus();
      return;
    }
    const submit = form.querySelector('button[type="submit"]');
    if (submit) submit.disabled = true;
    if (status) status.textContent = '';
    try {
      const body = {content};
      if (commentReplyParentID) body.parent_id = commentReplyParentID;
      const created = await api(commentTargetPath(activeCommentTargetType, activeCommentTargetID, '/comments'), {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(body)});
      let comment = created.comment || created;
      let uploaded = [];
      let uploadFailures = 0;
      for (const file of commentDraftFiles) {
        try {
          const payload = new FormData();
          payload.append('owner_type', 'comment');
          payload.append('owner_id', comment.id);
          payload.append('file', file);
          const attachment = await api('/api/v1/attachments', {method: 'POST', body: payload});
          uploaded.push(attachment.id);
        } catch (_) { uploadFailures++; }
      }
      if (uploaded.length) {
        const patched = await api(`/api/v1/comments/${encodeURIComponent(comment.id)}`, {method: 'PATCH', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify({content: comment.content, expected_revision: comment.revision, attachment_ids: uploaded})});
        comment = patched.comment || comment;
      }
      form.reset();
      commentDraftFiles = [];
      commentReplyParentID = '';
      setCommentComposerContext();
      hideCommentMentionMenu();
      const preview = $('#commentPreview');
      if (preview) preview.hidden = true;
      await loadCommentThread(activeCommentTargetType, activeCommentTargetID);
      if (status) status.textContent = uploadFailures ? t('uploadFailed') : t('commentSent');
    } catch (_) {
      if (status) status.textContent = t('commentSendFailed');
    } finally {
      if (submit) submit.disabled = false;
    }
  }

  function renderCommentSection(targetType, targetID, initialItems) {
    const body = $('#detailBody');
    if (!body) return;
    body.querySelector('#commentSection')?.remove();
    activeCommentTargetType = targetType;
    activeCommentTargetID = targetID;
    activeCommentItems = initialItems || [];
    commentActivity = new Map();
    commentReplyParentID = '';
    body.insertAdjacentHTML('beforeend', `<section class="detail-block comment-section" id="commentSection"><div class="comment-section-head"><div><h3>${escapeHTML(t('comments'))}</h3><small>${escapeHTML(t('commentPreview'))} · ${escapeHTML(t('triggerOutcomes'))}</small></div><span class="mono">${escapeHTML(String(activeCommentItems.length))}</span></div><div id="commentThread" class="comment-thread"></div><form id="commentComposer" class="comment-composer"><div id="commentComposerContext" class="comment-compose-context" hidden><span id="commentComposerContextText"></span><button type="button" id="commentCancelReply">${escapeHTML(t('cancelReply'))}</button></div><div class="comment-input-wrap"><textarea id="commentInput" required placeholder="${escapeHTML(t('commentPlaceholder'))}" aria-label="${escapeHTML(t('comments'))}"></textarea><div id="commentMentionMenu" class="comment-mention-menu" hidden></div></div><div class="comment-compose-toolbar"><label class="comment-file-label" title="${escapeHTML(t('attachComment'))}"><span aria-hidden="true">↥</span><span id="commentFilesSummary">${escapeHTML(t('attachComment'))}</span><input id="commentFiles" type="file" multiple hidden></label><div class="comment-compose-actions"><span id="commentComposerStatus" role="status"></span><button class="secondary" id="commentPreviewButton" type="button">${escapeHTML(t('preview'))}</button><button class="primary" type="submit">${escapeHTML(t('sendComment'))}</button></div></div><div id="commentPreview" class="comment-preview" hidden></div></form></section>`);
    renderCommentThread();
    $('#commentInput').addEventListener('input', updateCommentMentionMenu);
    $('#commentInput').addEventListener('keydown', event => {
      if ($('#commentMentionMenu')?.hidden || !commentMentionOptions.length) return;
      if (event.key === 'ArrowDown') { event.preventDefault(); commentMentionIndex = (commentMentionIndex + 1) % commentMentionOptions.length; renderCommentMentionMenu(); }
      else if (event.key === 'ArrowUp') { event.preventDefault(); commentMentionIndex = (commentMentionIndex - 1 + commentMentionOptions.length) % commentMentionOptions.length; renderCommentMentionMenu(); }
      else if (event.key === 'Enter' && commentMentionIndex >= 0) { event.preventDefault(); insertCommentMention(commentMentionIndex); }
      else if (event.key === 'Escape') { event.preventDefault(); hideCommentMentionMenu(); }
    });
    $('#commentInput').addEventListener('blur', () => setTimeout(hideCommentMentionMenu, 120));
    $('#commentFiles').addEventListener('change', event => {
      commentDraftFiles = Array.from(event.currentTarget.files || []);
      $('#commentFilesSummary').textContent = commentDraftFiles.length ? `${t('attachmentReady')} · ${commentDraftFiles.length}` : t('attachComment');
    });
    $('#commentCancelReply').onclick = cancelCommentReply;
    $('#commentPreviewButton').onclick = previewCommentTriggers;
    $('#commentComposer').onsubmit = submitComment;
    loadCommentMentionRoster().then(updateCommentMentionMenu);
    loadCommentThread(targetType, targetID, initialItems);
  }

  function attachmentContentURL(item) {
    const parts = String(item?.artifact_uri || '').split('/');
    const artifactID = parts.at(-2);
    const version = parts.at(-1) || '1';
    return artifactID ? `/api/v1/artifacts/${encodeURIComponent(artifactID)}/versions/${encodeURIComponent(version)}/content` : '';
  }

  async function uploadAgentAvatar(agentID, file) {
    if (!agentID || !file) return '';
    const body = new FormData();
    body.append('owner_type', 'agent');
    body.append('owner_id', agentID);
    body.append('file', file, file.name);
    const attachment = await api('/api/v1/attachments', {method: 'POST', body});
    return attachmentContentURL(attachment);
  }

  function renderStoredAttachments(items) {
    return items.map(item => {
      const url = attachmentContentURL(item);
      const previewable = String(item.media_type || '').startsWith('image/') && url;
      return `<button type="button" class="attachment-item ${previewable ? 'attachment-preview-trigger' : ''}" ${previewable ? `data-attachment-url="${escapeHTML(url)}" data-attachment-title="${escapeHTML(item.filename)}"` : ''}><span>${escapeHTML(item.filename)}</span><span class="mono">${escapeHTML(formatBytes(item.size_bytes))}</span></button>`;
    }).join('');
  }

  document.addEventListener('click', event => {
    const trigger = event.target.closest?.('[data-attachment-url]');
    if (!trigger) return;
    if (event.target.closest?.('button[data-chat-remove-file]')) return;
    ensureAttachmentPreviewDialog();
    const title = trigger.dataset.attachmentTitle || t('preview');
    const url = trigger.dataset.attachmentUrl;
    $('#attachmentPreviewTitle').textContent = title;
    if (trigger.classList.contains('is-image') || trigger.querySelector('img')) {
      $('#attachmentPreviewBody').innerHTML = `<img src="${escapeHTML(url)}" alt="${escapeHTML(title)}">`;
    } else {
      $('#attachmentPreviewBody').innerHTML = `<div class="attachment-file-preview"><span class="chat-attachment-icon">FILE</span><strong>${escapeHTML(title)}</strong><a class="primary" href="${escapeHTML(url)}" target="_blank" rel="noreferrer">${escapeHTML(t('chatPreviewFile'))}</a></div>`;
    }
    $('#attachmentPreviewDialog').showModal();
  });

  document.addEventListener('keydown', event => {
    const trigger = event.target.closest?.('[data-attachment-url]');
    if (trigger && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      trigger.click();
    }
  });

  const baseOpenRequirement = openRequirement;
  openRequirement = async function enhancedRequirementDetails(id) {
    await baseOpenRequirement(id);
    try {
      const detail = await api(`/api/v1/requirements/${encodeURIComponent(id)}`);
      const body = $('#detailBody');
      if (!body) return;
      body.insertAdjacentHTML('afterbegin', renderDeliveryDetailCanvas(requirements.find(item => item.id === id) || detail, detail));
      const items = detail.attachments || [];
      if (items.length) {
        const block = document.createElement('div');
        block.className = 'detail-block';
        block.innerHTML = `<h3>${escapeHTML(t('attachments'))} · ${items.length}</h3><div class="attachment-list">${renderStoredAttachments(items)}</div>`;
        body.appendChild(block);
      }
      renderCommentSection('requirement', id, detail.comments || []);
    } catch (_) {
      const body = $('#detailBody');
      if (body) renderCommentSection('requirement', id, []);
    }
  };

  async function openBug(id) {
    const local = bugs.find(item => item.id === id);
    if (!local) return;
    $('#detailTitle').textContent = local.title || id;
    $('#detailKey').textContent = id;
    $('#detailBody').innerHTML = `<div class="detail-grid"><div><div class="detail-block"><h3>${escapeHTML(t('description'))}</h3><p>${escapeHTML(local.actual || local.steps_to_reproduce || '-')}</p></div><div class="detail-block"><h3>${escapeHTML(t('bugSteps'))}</h3><p>${escapeHTML(local.steps_to_reproduce || '-')}</p></div><div class="detail-block"><h3>${escapeHTML(t('bugExpected'))}</h3><p>${escapeHTML(local.expected || '-')}</p></div><div class="detail-block"><h3>${escapeHTML(t('bugLog'))}</h3><p class="mono">${escapeHTML(local.log_excerpt || '-')}</p></div></div><div class="detail-side"><div class="detail-block"><h3>${escapeHTML(t('status'))}</h3><p><span class="status ${statusClass(local.status)}">${escapeHTML(statusLabel(local.status))}</span></p><div class="kpi-row"><span>${escapeHTML(t('version'))}</span><strong>${escapeHTML(String(local.attempt_count || 0))}</strong></div><div class="kpi-row"><span>${escapeHTML(t('repositorySet'))}</span><strong>${escapeHTML(repositoryLabel(local.repository_id))}</strong></div><div class="kpi-row"><span>${escapeHTML(t('requirementRelation'))}</span><strong>${escapeHTML(requirementLabel(local.requirement_id))}</strong></div></div></div></div>`;
    $('#detailDialog').showModal();
    try {
      const detail = await api(`/api/v1/bugs/${encodeURIComponent(id)}`);
      const body = $('#detailBody');
      if (body && (detail.attachments || []).length) {
        const attachments = detail.attachments;
        body.insertAdjacentHTML('beforeend', `<div class="detail-block"><h3>${escapeHTML(t('attachments'))} · ${attachments.length}</h3><div class="attachment-list">${renderStoredAttachments(attachments)}</div></div>`);
      }
      renderCommentSection('bug', id, detail.comments || []);
    } catch (_) {
      renderCommentSection('bug', id, []);
    }
  }

  // Bug rows are rendered by both the base page and the enhanced table. Event
  // delegation keeps the durable detail/comment entry point attached after
  // every refresh without coupling the two renderers.
  window.openBug = openBug;
  document.addEventListener('click', event => {
    const row = event.target.closest?.('[data-bug-id]');
    if (row && !event.target.closest('button')) openBug(row.dataset.bugId);
  });
  document.addEventListener('keydown', event => {
    const row = event.target.closest?.('[data-bug-id]');
    if (row && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      openBug(row.dataset.bugId);
    }
  });

  document.addEventListener('click', event => {
    const addBug = event.target.closest?.('[data-delivery-detail-add-bug]');
    if (addBug) {
      event.preventDefault();
      showDialog('bug', addBug.dataset.deliveryDetailAddBug);
      return;
    }
    const openBugButton = event.target.closest?.('[data-delivery-open-bug]');
    if (openBugButton) {
      event.preventDefault();
      openBug(openBugButton.dataset.deliveryOpenBug);
      return;
    }
    const openExecution = event.target.closest?.('[data-delivery-open-execution]');
    if (openExecution) {
      event.preventDefault();
      activeExecutionPlanID = openExecution.dataset.deliveryOpenExecution;
      if ($('#detailDialog')?.open) $('#detailDialog').close();
      currentView = 'executions';
      applyMenuAccess();
      render();
      void loadExecutionTimeline(activeExecutionPlanID);
    }
  });

  function formatBytes(value) {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / 1024 / 1024).toFixed(1)} MiB`;
  }

  let activeChatID = '';
  let activeChatData = null;
  let chatAttachmentItems = [];
  let chatDraftFiles = [];
  let chatSearchTerm = '';
  let chatSending = false;
  let chatRepositories = [];
  let chatAgents = [];
  let chatRuntimes = [];
  let chatRuntimeDiscoveryComplete = false;
  let chatResourceRequest = 0;
  let chatStateRequest = 0;
  let chatCreateRequest = 0;
  let chatCreateProjectDraft = '';
  let chatCreateAgentDraft = '';
  let chatPendingCreateID = '';
  let chatPendingCreateProjectID = '';
  let chatPendingCreateAgentID = '';
  let chatCreatingProjectID = '';
  let chatCreatingAgentID = '';
  const chatCreationBindings = new Map();

  function releaseChatDraftFile(item) {
    if (item?.previewURL) URL.revokeObjectURL(item.previewURL);
  }

  function clearChatDraftFiles() {
    chatDraftFiles.forEach(releaseChatDraftFile);
    chatDraftFiles = [];
  }

  function addChatDraftFiles(files) {
    const existing = new Set(chatDraftFiles.map(item => `${item.file.name}:${item.file.size}:${item.file.lastModified}`));
    for (const [index, file] of Array.from(files || []).entries()) {
      if (!file || !file.name) continue;
      const key = `${file.name}:${file.size}:${file.lastModified}`;
      if (existing.has(key)) continue;
      existing.add(key);
      chatDraftFiles.push({file, previewURL: file.type.startsWith('image/') ? URL.createObjectURL(file) : ''});
    }
    renderChatPage();
  }

  function chatProject() {
    const projectID = activeChatData?.chat?.project_id;
    return [...chatRepositories, ...repositories].find(item => item.id === projectID);
  }

  function chatAgent() {
    const agentID = activeChatData?.chat?.agent_id;
    return [...chatAgents, ...nativeAgents].find(item => item.id === agentID);
  }

  function chatAttachmentTile(item, index, draft = false) {
    const file = draft ? item.file : item;
    const mediaType = file.type || file.media_type || '';
    const image = mediaType.startsWith('image/');
    const url = draft ? item.previewURL : attachmentContentURL(item);
    const title = file.name || file.filename || t('attachmentFile');
    const remove = draft ? `<button type="button" class="chat-attachment-remove" data-chat-remove-file="${index}" aria-label="${escapeHTML(t('chatRemoveFile'))}" title="${escapeHTML(t('chatRemoveFile'))}">×</button>` : '';
    const preview = url ? `data-attachment-url="${escapeHTML(url)}" data-attachment-title="${escapeHTML(title)}"` : '';
    return `<div class="chat-attachment-tile ${image ? 'is-image' : ''} ${url ? 'is-previewable' : ''}" ${preview} role="${url ? 'button' : 'group'}" tabindex="${url ? '0' : '-1'}">${image && url ? `<img src="${escapeHTML(url)}" alt="${escapeHTML(title)}">` : `<span class="chat-attachment-icon">${image ? 'IMG' : 'FILE'}</span>`}<span class="chat-attachment-copy"><strong>${escapeHTML(title)}</strong><small>${escapeHTML(image ? t('attachmentImage') : formatBytes(file.size || file.size_bytes || 0))}</small></span>${remove}</div>`;
  }

  function setChatContextSelectValue(selector, value, label) {
    const select = $(selector);
    const normalized = String(value || '').trim();
    if (!select || !normalized) return;
    if (![...select.options].some(option => option.value === normalized)) {
      select.append(new Option(label || normalized, normalized));
    }
    select.value = normalized;
  }

  function chatAgentOptionMarkup(items) {
    return items.filter(item => item.status === 'active').map(item => {
      const runtimeID = item.executor_binding?.runtime_id || 'local';
      const runtime = chatRuntimes.find(candidate => candidate.id === runtimeID);
      const unavailable = chatRuntimeDiscoveryComplete && (!runtime || !runtime.installed || !runtime.adapter_available);
      const unavailableLabel = unavailable ? ` · ${t(!runtime || !runtime.installed ? 'notInstalled' : 'adapterUnavailable')}` : '';
      return `<option value="${escapeHTML(item.id)}" ${unavailable ? 'disabled' : ''}>${escapeHTML(item.name || item.id)} · ${escapeHTML(runtimeID)}${escapeHTML(unavailableLabel)}</option>`;
    }).join('');
  }

  function chatMessageHTML(item) {
    const attachments = (item.attachment_ids || []).map(id => chatAttachmentItems.find(candidate => candidate.id === id)).filter(Boolean);
    const attachmentMarkup = attachments.length ? `<div class="chat-message-attachments">${attachments.map(attachment => chatAttachmentTile(attachment, 0)).join('')}</div>` : '';
    const fallback = item.attachment_ids?.length && !attachments.length ? `<small class="chat-message-attachment-count">${escapeHTML(String(item.attachment_ids.length))} ${escapeHTML(t('attachments'))}</small>` : '';
    const isAssistant = item.role === 'assistant';
    const agent = chatAgent();
    return `<article class="chat-message ${isAssistant ? 'assistant' : 'user'}"><header><span class="chat-message-author"><i class="chat-avatar ${isAssistant ? 'agent' : 'human'}">${isAssistant ? 'AI' : 'ME'}</i><b>${escapeHTML(isAssistant ? (agent?.name || 'Agent') : 'You')}</b></span><time>${escapeHTML(new Date(item.created_at).toLocaleTimeString(locale === 'zh' ? 'zh-CN' : 'en-US', {hour: '2-digit', minute: '2-digit'}))}</time></header><p>${escapeHTML(item.content)}</p>${attachmentMarkup || fallback}</article>`;
  }

  function renderChatPage() {
    $('#pageTitle').textContent = t('chats');
    $('#pageSubtitle').textContent = t('chatSubtitle');
    $('#pageActions').innerHTML = `<button class="primary" id="chatNew"><span aria-hidden="true">＋</span>${escapeHTML(t('newChat'))}</button>`;
    const visibleChats = chats.filter(item => `${item.title || ''} ${item.project_id || ''}`.toLowerCase().includes(chatSearchTerm.toLowerCase()));
    const list = visibleChats.map(item => {
      const agent = [...chatAgents, ...nativeAgents].find(candidate => candidate.id === item.agent_id);
      const project = [...chatRepositories, ...repositories].find(candidate => candidate.id === item.project_id);
      const detail = [project?.canonical_name || item.project_id || t('chatNoProject'), agent?.name || item.runtime_id || 'local'].filter(Boolean).join(' · ');
      return `<button type="button" class="chat-list-item ${item.id === activeChatID ? 'active' : ''}" data-chat-id="${escapeHTML(item.id)}"><span class="chat-list-item-top"><i class="chat-list-dot"></i><small>${escapeHTML(project?.canonical_name || t('chatNoProject'))}</small></span><strong>${escapeHTML(item.title)}</strong><small class="chat-list-meta">${escapeHTML(detail)}</small></button>`;
    }).join('');
    const messages = activeChatData?.messages || [];
    const messageHTML = messages.length ? messages.map(chatMessageHTML).join('') : `<div class="chat-empty-state"><div class="chat-empty-orbit"><span></span><b>✦</b></div><h2>${escapeHTML(t('chatEmptyTitle'))}</h2><p>${escapeHTML(t('chatEmptyBody'))}</p><div class="chat-suggestions"><span>${escapeHTML(t('chatSuggested'))}</span>${[t('chatSuggestionOne'), t('chatSuggestionTwo'), t('chatSuggestionThree')].map(text => `<button type="button" data-chat-suggestion="${escapeHTML(text)}">${escapeHTML(text)}</button>`).join('')}</div></div>`;
    const projectSourceItems = [...chatRepositories, ...repositories].filter((item, index, items) => items.findIndex(candidate => candidate.id === item.id) === index);
    const agentSourceItems = [...chatAgents, ...nativeAgents].filter((item, index, items) => items.findIndex(candidate => candidate.id === item.id) === index);
    const pendingProject = activeChatID === chatPendingCreateID ? chatPendingCreateProjectID : '';
    const creatingProject = activeChatID && activeChatID === chatPendingCreateID ? chatCreatingProjectID : '';
    const creationBinding = activeChatID ? chatCreationBindings.get(activeChatID) : null;
    const selectedProject = creationBinding?.projectID || pendingProject || creatingProject || activeChatData?.chat?.project_id || '';
    const selectedProjectKnown = projectSourceItems.some(item => item.id === selectedProject);
    const projectOptions = `${selectedProject && !selectedProjectKnown ? `<option value="${escapeHTML(selectedProject)}">${escapeHTML(selectedProject)}</option>` : ''}${projectSourceItems.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.canonical_name || item.id)}</option>`).join('')}`;
    const agentOptions = chatAgentOptionMarkup(agentSourceItems);
    const pendingAgent = activeChatID === chatPendingCreateID ? chatPendingCreateAgentID : '';
    const creatingAgent = activeChatID && activeChatID === chatPendingCreateID ? chatCreatingAgentID : '';
    const selectedAgent = creationBinding?.agentID || pendingAgent || creatingAgent || activeChatData?.chat?.agent_id || '';
    const project = chatProject();
    const agent = chatAgent();
    const runtimeLabel = activeChatData?.chat?.runtime_id || 'local';
    const continuityLabel = activeChatData?.chat?.continuity_mode === 'native_session' ? t('chatNativeContinuity') : activeChatData?.chat?.provider_session_id ? t('chatCompiledContinuity') : t('chatReadyContinuity');
    const contextFiles = chatAttachmentItems.length ? chatAttachmentItems.map(item => chatAttachmentTile(item, 0)).join('') : `<p class="chat-context-empty">${escapeHTML(t('chatNoFiles'))}</p>`;
    const projectSource = project?.metadata?.local_path || project?.clone_url || project?.canonical_name || '-';
    $('#appView').innerHTML = `<div class="chat-workspace"><aside class="chat-sidebar"><div class="chat-sidebar-head"><div><span class="chat-eyebrow">${escapeHTML(t('chatWorkspace'))}</span><strong>${escapeHTML(t('chatRecent'))}</strong></div><span class="chat-count">${escapeHTML(String(chats.length).padStart(2, '0'))}</span></div><label class="chat-search"><span aria-hidden="true">⌕</span><input id="chatSearch" type="search" value="${escapeHTML(chatSearchTerm)}" placeholder="${escapeHTML(t('chatSearchPlaceholder'))}" aria-label="${escapeHTML(t('chatSearchPlaceholder'))}"></label><div class="chat-list">${list || `<p class="chat-empty">${escapeHTML(t('noChats'))}</p>`}</div></aside><section class="chat-panel"><header class="chat-panel-head"><div class="chat-panel-title"><span class="chat-eyebrow">${escapeHTML(t('chatConversation'))} / ${escapeHTML(activeChatData?.chat?.id?.slice(0, 8) || 'NEW')}</span><h2>${escapeHTML(activeChatData?.chat?.title || t('chatWorkspace'))}</h2><div class="chat-context-line"><span class="chat-status-pulse"></span>${escapeHTML(continuityLabel)}<span>·</span>${escapeHTML(runtimeLabel)}</div></div><div class="chat-panel-actions"><span class="chat-live-pill"><i></i>${escapeHTML(t('chatPersisted'))}</span><button type="button" class="icon-button" id="chatNewTop" title="${escapeHTML(t('newChat'))}" aria-label="${escapeHTML(t('newChat'))}">＋</button></div></header><div class="chat-history" id="chatHistory">${messageHTML}</div><form id="chatComposer" class="chat-composer" data-chat-dropzone="true"><div class="chat-drop-hint">${escapeHTML(t('chatDropHint'))}</div><div class="chat-draft-files" id="chatDraftFiles">${chatDraftFiles.map((item, index) => chatAttachmentTile(item, index, true)).join('')}</div><textarea id="chatInput" required placeholder="${escapeHTML(t('chatMessagePlaceholder'))}"></textarea><div class="chat-compose-footer"><div class="chat-compose-context"><label class="chat-select-control"><span>${escapeHTML(t('chatProject'))}</span><select id="chatProject" aria-label="${escapeHTML(t('chatProject'))}"><option value="">${escapeHTML(t('chatChooseProject'))}</option>${projectOptions}</select></label><label class="chat-select-control"><span>${escapeHTML(t('chatAgent'))}</span><select id="chatAgent" aria-label="${escapeHTML(t('chatAgent'))}"><option value="">${escapeHTML(t('chatNoAgent'))}</option>${agentOptions}</select></label></div><div class="chat-compose-actions"><label class="chat-file-label" title="${escapeHTML(t('chatAttachments'))}"><span aria-hidden="true">⊕</span><span>${escapeHTML(t('chatAttachments'))}</span><input id="chatFiles" type="file" multiple hidden></label><span id="chatComposerStatus" role="status"></span><button class="primary chat-send" type="submit" ${chatSending ? 'disabled' : ''}><span>${escapeHTML(chatSending ? t('chatSending') : t('sendMessage'))}</span><b aria-hidden="true">↗</b></button></div></div></form></section><aside class="chat-context-panel"><div class="chat-context-header"><span class="chat-eyebrow">${escapeHTML(t('chatContext'))}</span><span class="chat-context-signal"><i></i>LIVE</span></div><div class="chat-project-card"><span class="chat-project-glyph">${project ? '◎' : '○'}</span><div><small>${escapeHTML(t('chatProjectContext'))}</small><strong>${escapeHTML(project?.canonical_name || t('chatNoProject'))}</strong></div></div><div class="chat-context-block"><span>${escapeHTML(t('chatProjectReady'))}</span><strong>${escapeHTML(projectSource)}</strong></div><div class="chat-context-block"><span>${escapeHTML(t('chatAgentReady'))}</span><strong>${escapeHTML(agent?.name || t('chatNoAgent'))}</strong><small>${escapeHTML(agent?.executor_binding?.runtime_id || runtimeLabel)}</small></div><div class="chat-context-files"><div class="chat-context-block-head"><span>${escapeHTML(t('chatProjectFiles'))}</span><b>${escapeHTML(String(chatAttachmentItems.length))}</b></div>${contextFiles}</div><div class="chat-context-foot"><span class="chat-mini-ring"></span><div><strong>${escapeHTML(t('chatRuntimeState'))}</strong><small>${escapeHTML(continuityLabel)}</small></div></div></aside></div>`;
    setChatContextSelectValue('#chatProject', selectedProject);
    setChatContextSelectValue('#chatAgent', selectedAgent);
    if ($('#chatProject') && activeChatData?.chat) $('#chatProject').onchange = event => {
      const binding = chatCreationBindings.get(activeChatID) || {};
      chatCreationBindings.set(activeChatID, {...binding, projectID: event.currentTarget.value});
      persistChatField('project_id', event.currentTarget.value, activeChatData.chat.project_id || '');
    };
    if ($('#chatAgent') && activeChatData?.chat) $('#chatAgent').onchange = event => {
      const binding = chatCreationBindings.get(activeChatID) || {};
      chatCreationBindings.set(activeChatID, {...binding, agentID: event.currentTarget.value});
      persistChatField('agent_id', event.currentTarget.value, selectedAgent);
    };
    $('#chatSearch').oninput = event => { chatSearchTerm = event.currentTarget.value; renderChatPage(); focusIfPresent('#chatSearch'); const input = $('#chatSearch'); if (input) input.setSelectionRange(chatSearchTerm.length, chatSearchTerm.length); };
    document.querySelectorAll('[data-chat-id]').forEach(button => { button.onclick = () => { if (button.dataset.chatId === activeChatID) return; clearChatDraftFiles(); chatPendingCreateID = ''; chatPendingCreateProjectID = ''; chatPendingCreateAgentID = ''; chatCreatingProjectID = ''; chatCreatingAgentID = ''; activeChatID = button.dataset.chatId; const requestID = ++chatStateRequest; void loadChatDetail(activeChatID, null, requestID); }; });
    $('#chatNew').onclick = showChatCreateDialog;
    $('#chatNewTop').onclick = showChatCreateDialog;
    $('#chatComposer').onsubmit = sendChatFromUI;
    const fileInput = $('#chatFiles');
    if (fileInput) fileInput.onchange = event => { addChatDraftFiles(event.currentTarget.files); event.currentTarget.value = ''; };
    document.querySelectorAll('[data-chat-remove-file]').forEach(button => { button.onclick = event => { event.stopPropagation(); const index = Number(button.dataset.chatRemoveFile); releaseChatDraftFile(chatDraftFiles[index]); chatDraftFiles.splice(index, 1); renderChatPage(); }; });
    document.querySelectorAll('[data-chat-suggestion]').forEach(button => { button.onclick = () => { const input = $('#chatInput'); if (input) { input.value = button.dataset.chatSuggestion; input.focus(); } }; });
    const dropZone = $('#chatComposer');
    if (dropZone) {
      dropZone.ondragover = event => { event.preventDefault(); dropZone.classList.add('is-dragging'); };
      dropZone.ondragleave = event => { if (!dropZone.contains(event.relatedTarget)) dropZone.classList.remove('is-dragging'); };
      dropZone.ondrop = event => { event.preventDefault(); dropZone.classList.remove('is-dragging'); addChatDraftFiles(event.dataTransfer?.files || []); };
      $('#chatInput').onpaste = event => { const files = Array.from(event.clipboardData?.items || []).filter(item => item.kind === 'file').map(item => item.getAsFile()).filter(Boolean); if (files.length) { event.preventDefault(); addChatDraftFiles(files); } };
      $('#chatInput').oninput = event => { event.currentTarget.style.height = 'auto'; event.currentTarget.style.height = `${Math.min(event.currentTarget.scrollHeight, 220)}px`; };
    }
  }

  async function persistChatField(field, value, previous) {
    const status = $('#chatComposerStatus');
    try {
      if (!activeChatID) return;
      if (activeChatData?.chat) activeChatData.chat[field] = value;
      if (status) status.textContent = '...';
      const saved = await api(`/api/v1/chats/${encodeURIComponent(activeChatID)}`, {method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({[field]: value})});
      if (activeChatData?.chat) activeChatData.chat = saved;
      renderChatPage();
    } catch (_) {
      if (activeChatData?.chat) activeChatData.chat[field] = previous;
      renderChatPage();
      if ($('#chatComposerStatus')) $('#chatComposerStatus').textContent = t('chatSendFailed');
    }
  }

  async function loadChatDetail(id, fallbackChat = null, requestID = chatStateRequest, creationRequestID = 0) {
    const isCurrent = () => id === activeChatID && (creationRequestID ? creationRequestID === chatCreateRequest : requestID === chatStateRequest && !chatPendingCreateID);
    try {
      const [detail, attachments] = await Promise.all([api(`/api/v1/chats/${encodeURIComponent(id)}`), api(`/api/v1/attachments?owner_type=chat_session&owner_id=${encodeURIComponent(id)}`)]);
      if (!isCurrent()) return;
      const chat = {...(fallbackChat || {}), ...(detail?.chat || {})};
      for (const field of ['project_id', 'agent_id', 'title']) {
        if (!chat[field] && fallbackChat?.[field]) chat[field] = fallbackChat[field];
      }
      const creationBinding = chatCreationBindings.get(id);
      if (creationBinding) {
        if (!chat.project_id && creationBinding.projectID) chat.project_id = creationBinding.projectID;
        if (!chat.agent_id && creationBinding.agentID) chat.agent_id = creationBinding.agentID;
      }
      activeChatData = {...detail, chat};
      chatAttachmentItems = attachments?.items || [];
      renderChatPage();
      requestAnimationFrame(() => { const history = $('#chatHistory'); if (history) history.scrollTop = history.scrollHeight; });
    } catch (_) {
      if (!isCurrent()) return;
      activeChatData = fallbackChat ? {chat: fallbackChat, messages: []} : null;
      chatAttachmentItems = [];
      renderChatPage();
    }
  }

  async function loadChatList() {
    const requestID = ++chatStateRequest;
    try {
      const response = await api('/api/v1/chats');
      if (requestID !== chatStateRequest) return;
      const serverChats = response.items || [];
      const pendingChat = chatPendingCreateID && chats.find(item => item.id === chatPendingCreateID);
      const optimisticActiveChat = activeChatID && chatCreationBindings.has(activeChatID) ? chats.find(item => item.id === activeChatID) : null;
      const optimisticChat = pendingChat || optimisticActiveChat;
      const mergedChats = optimisticChat
        ? serverChats.map(item => item.id === optimisticChat.id ? {
          ...optimisticChat,
          ...item,
          project_id: item.project_id || optimisticChat.project_id,
          agent_id: item.agent_id || optimisticChat.agent_id
        } : item)
        : serverChats;
      chats = optimisticChat && !mergedChats.some(item => item.id === optimisticChat.id) ? [optimisticChat, ...mergedChats] : mergedChats;
      if (chatPendingCreateID) {
        if (!chats.some(item => item.id === chatPendingCreateID)) chats = [pendingChat, ...chats].filter(Boolean);
        renderChatPage();
        return;
      }
      if (activeChatID && !chats.some(item => item.id === activeChatID) && !chatCreationBindings.has(activeChatID)) { activeChatID = ''; clearChatDraftFiles(); }
      if (!activeChatID && chats[0]) activeChatID = chats[0].id;
      if (activeChatID) await loadChatDetail(activeChatID, chats.find(item => item.id === activeChatID) || null, requestID); else { activeChatData = null; renderChatPage(); }
    } catch (_) {
      if (requestID === chatStateRequest) renderChatPage();
    }
  }

  async function showChatCreateDialog() {
    const projectSelect = $('#chatCreateProject');
    const agentSelect = $('#chatCreateAgent');
    const form = $('#chatCreateForm');
    if (!projectSelect || !agentSelect || !form) return;
    form.reset();
    chatCreateProjectDraft = '';
    chatCreateAgentDraft = '';
    $('#chatCreateError').textContent = '';
    $('#chatCreateDialog').showModal();
    setTimeout(() => focusIfPresent('#chatCreateTitle'), 0);

    const requestID = ++chatResourceRequest;
    projectSelect.disabled = true;
    agentSelect.disabled = true;
    projectSelect.innerHTML = `<option value="">${escapeHTML(t('loading'))}</option>`;
    agentSelect.innerHTML = `<option value="">${escapeHTML(t('loading'))}</option>`;
    projectSelect.onchange = event => { chatCreateProjectDraft = String(event.currentTarget.value || '').trim(); };
    agentSelect.onchange = event => { chatCreateAgentDraft = String(event.currentTarget.value || '').trim(); };
    const [projectResult, agentResult, runtimeResult] = await Promise.allSettled([
      api('/api/v1/repositories'),
      api('/api/v1/workspaces/local/agents'),
      api('/api/v1/runtimes/discovered')
    ]);
    if (requestID !== chatResourceRequest || !$('#chatCreateDialog')?.open) return;
    if (projectResult.status === 'fulfilled') chatRepositories = projectResult.value.items || [];
    if (agentResult.status === 'fulfilled') chatAgents = agentResult.value.items || [];
    chatRuntimeDiscoveryComplete = runtimeResult.status === 'fulfilled';
    chatRuntimes = chatRuntimeDiscoveryComplete ? (runtimeResult.value.items || []) : [];
    const projectItems = [...chatRepositories, ...repositories].filter((item, index, items) => items.findIndex(candidate => candidate.id === item.id) === index);
    const agentItems = [...chatAgents, ...nativeAgents].filter((item, index, items) => items.findIndex(candidate => candidate.id === item.id) === index);
    projectSelect.innerHTML = `<option value="">${escapeHTML(t('chatProject'))}</option>${projectItems.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.canonical_name || item.id)}</option>`).join('')}`;
    agentSelect.innerHTML = `<option value="">${escapeHTML(t('chatAgent'))}</option>${chatAgentOptionMarkup(agentItems)}`;
    projectSelect.disabled = false;
    agentSelect.disabled = false;
  }

  function closeChatCreateDialog() {
    const dialog = $('#chatCreateDialog');
    if (dialog?.open) dialog.close();
  }

  async function createChatFromUI() {
    const title = String($('#chatCreateTitle')?.value || '').trim();
    if (!title) return;
    const projectID = chatCreateProjectDraft || String($('#chatCreateProject')?.value || '').trim();
    const agentID = chatCreateAgentDraft || String($('#chatCreateAgent')?.value || '').trim();
    const submit = $('#chatCreateForm button[type="submit"]');
    if (submit) submit.disabled = true;
    $('#chatCreateError').textContent = '';
    const creationRequestID = ++chatCreateRequest;
    ++chatStateRequest;
    chatCreatingProjectID = projectID;
    chatCreatingAgentID = agentID;
    let created;
    const requestKey = idempotencyKey();
    const requestBody = JSON.stringify({workspace_id: 'local', project_id: projectID, agent_id: agentID, title: title.trim()});
    const createChatRequest = (signal) => api('/api/v1/chats', {method: 'POST', ...(signal ? {signal} : {}), headers: {'Content-Type': 'application/json', 'Idempotency-Key': requestKey}, body: requestBody});
    const requestController = new AbortController();
    const requestTimeout = setTimeout(() => requestController.abort(), 10000);
    try {
      created = await createChatRequest(requestController.signal);
      const record = created?.chat || created?.item || created?.data || created;
      if (!record?.id) throw new Error('chat creation response did not include an id');
      created = record;
    } catch (_) {
      // The POST may have committed before the browser lost its response body.
      // Replaying the same key asks the API for the durable response and avoids
      // guessing the new session from a title or a stale list snapshot.
      try {
        const replay = await createChatRequest();
        created = replay?.chat || replay?.item || replay?.data || replay;
        if (!created?.id) throw new Error('chat creation replay did not include an id');
      } catch (_) {
        // A replay can race the first request's persistence. Keep a bounded
        // authoritative-list fallback for that narrow window.
        try {
          for (let attempt = 0; attempt < 20 && !created?.id; attempt += 1) {
            if (attempt > 0) await new Promise(resolve => setTimeout(resolve, 250));
            const response = await api('/api/v1/chats');
            const items = Array.isArray(response?.items) ? response.items : [];
            const matches = items.filter(item => item.title === title && (!projectID || item.project_id === projectID) && (!agentID || item.agent_id === agentID));
            created = matches.sort((left, right) => String(right.created_at || '').localeCompare(String(left.created_at || '')))[0];
          }
          if (!created?.id) throw new Error('chat creation recovery did not find a session');
        } catch (_) {
          $('#chatCreateError').textContent = t('chatCreateFailed');
          chatCreatingProjectID = '';
          chatCreatingAgentID = '';
          return;
        }
      }
    } finally {
      clearTimeout(requestTimeout);
      if (submit) submit.disabled = false;
    }
    const createdWithContext = {
      ...created,
      id: String(created.id),
      title: created.title || title,
      project_id: String(created.project_id || projectID),
      agent_id: String(created.agent_id || agentID)
    };
    chatCreationBindings.set(createdWithContext.id, {projectID, agentID});
    chats = [createdWithContext, ...chats.filter(item => item.id !== createdWithContext.id)];
    activeChatID = createdWithContext.id;
    chatPendingCreateID = createdWithContext.id;
    chatPendingCreateProjectID = projectID;
    chatPendingCreateAgentID = agentID;
    activeChatData = {chat: createdWithContext, messages: []};
    chatAttachmentItems = [];
    closeChatCreateDialog();
    $('#chatCreateForm').reset();
    renderChatPage();
    setChatContextSelectValue('#chatProject', createdWithContext.project_id);
    setChatContextSelectValue('#chatAgent', createdWithContext.agent_id);
    const detailLoad = loadChatDetail(activeChatID, createdWithContext, chatStateRequest, creationRequestID);
    void detailLoad.then(() => {
      if (creationRequestID !== chatCreateRequest || chatPendingCreateID !== createdWithContext.id) return;
      chatPendingCreateID = '';
      chatPendingCreateProjectID = '';
      chatPendingCreateAgentID = '';
      chatCreatingProjectID = '';
      chatCreatingAgentID = '';
      renderChatPage();
    });
  }

  $('#closeChatCreateDialog').onclick = closeChatCreateDialog;
  $('#cancelChatCreateDialog').onclick = closeChatCreateDialog;
  $('#chatCreateDialog').addEventListener('click', event => { if (event.target === event.currentTarget) closeChatCreateDialog(); });
  $('#chatCreateForm').onsubmit = async event => { event.preventDefault(); await createChatFromUI(); };

  async function sendChatFromUI(event) {
    event.preventDefault();
    if (!activeChatID) { showChatCreateDialog(); return; }
    const form = event.currentTarget; const input = $('#chatInput'); const files = chatDraftFiles.map(item => item.file); const status = $('#chatComposerStatus');
    if (!String(input?.value || '').trim()) return;
    chatSending = true;
    renderChatPage();
    const attachmentIDs = [];
    try {
      for (const file of files) { const payload = new FormData(); payload.append('owner_type', 'chat_session'); payload.append('owner_id', activeChatID); payload.append('file', file); const attachment = await api('/api/v1/attachments', {method: 'POST', body: payload}); attachmentIDs.push(attachment.id); }
      await api(`/api/v1/chats/${encodeURIComponent(activeChatID)}/messages`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify({content: input.value, attachment_ids: attachmentIDs})});
      form.reset(); clearChatDraftFiles(); chatSending = false; await loadChatDetail(activeChatID);
    } catch (_) {
      // The user turn is durable before provider execution starts. Reload the
      // authoritative transcript so a provider failure does not make the
      // message appear lost in the browser.
      chatSending = false;
      await loadChatDetail(activeChatID);
      if ($('#chatComposerStatus')) $('#chatComposerStatus').textContent = t('chatSendFailed');
    }
  }

  function executionPlanStatus(plan, projection) {
    const terminal = projection?.terminal_outcome || projection?.status;
    return String(terminal || plan?.status || 'draft').toLowerCase();
  }

  function executionNodeStatus(projection, nodeID) {
    return String(projection?.nodes?.[nodeID]?.status || 'pending').toLowerCase();
  }

  function executionNodeLabel(node) {
    if (node.kind === 'agent') {
      const ref = node.agent_ref?.id;
      return nativeAgents.find(item => item.id === ref)?.name || ref || node.id;
    }
    if (node.kind === 'squad') {
      const ref = node.squad_ref?.id;
      return nativeSquads.find(item => item.id === ref)?.name || ref || node.id;
    }
    return node.kind || node.id;
  }

  function executionStatusClass(status) {
    if (['passed', 'completed', 'success', 'succeeded'].includes(status)) return 'good';
    if (['failed', 'cancelled', 'timed_out', 'blocked'].includes(status)) return 'bad';
    if (['running', 'ready', 'waiting'].includes(status)) return 'active';
    return 'warn';
  }

  function executionPlanCard(plan, selected) {
    const requirement = requirements.find(item => item.id === plan.requirement_id);
    const cache = executionTimelineCache.get(plan.id) || {};
    const projection = cache.projection;
    const status = executionPlanStatus(plan, projection);
    const graph = plan.graph_snapshot || {};
    const target = plan.selected_ref?.id || '-';
    return `<button type="button" class="execution-plan-card ${selected ? 'selected' : ''}" data-execution-plan-id="${escapeHTML(plan.id)}"><span class="execution-plan-card-top"><span class="mono">${escapeHTML((plan.id || '').slice(0, 12))}</span><span class="status ${executionStatusClass(status)}">${escapeHTML(status)}</span></span><strong>${escapeHTML(requirement?.title || requirement?.key || plan.requirement_id || t('executionPlans'))}</strong><span class="execution-plan-card-meta">${escapeHTML(target)} · ${escapeHTML(String((graph.nodes || []).length))} ${escapeHTML(t('runNodes'))}</span></button>`;
  }

  function executionGraphHTML(plan, projection) {
    const graph = plan?.graph_snapshot || {};
    const nodes = Array.isArray(graph.nodes) ? graph.nodes : [];
    if (!nodes.length) return `<div class="execution-empty">${escapeHTML(t('runNoEvents'))}</div>`;
    return `<div class="execution-graph-grid">${nodes.map((node, index) => { const status = executionNodeStatus(projection, node.id); return `<article class="execution-node ${executionStatusClass(status)}" style="--node-delay:${index * 55}ms"><div class="execution-node-orbit"><span></span></div><header><span class="mono">${escapeHTML(node.id || `node-${index + 1}`)}</span><span class="status ${executionStatusClass(status)}">${escapeHTML(status)}</span></header><strong>${escapeHTML(executionNodeLabel(node))}</strong><small>${escapeHTML(node.kind || 'node')} · ${escapeHTML(String(node.agent_ref?.revision || node.squad_ref?.version || graph.version || 0))}</small></article>`; }).join('')}</div>`;
  }

  function executionLogHTML(cache) {
    const events = Array.isArray(cache?.events) ? cache.events : [];
    if (!events.length) return `<div class="execution-log-empty"><span class="terminal-cursor">▋</span>${escapeHTML(t('runNoEvents'))}</div>`;
    return events.map(event => {
      const payload = event.payload ? (typeof event.payload === 'string' ? event.payload : JSON.stringify(event.payload)) : '';
      const text = [event.event_type || event.type || 'event', event.node_id || '', payload].filter(Boolean).join('  ');
      return `<div class="execution-log-line"><span class="execution-log-seq">${escapeHTML(String(event.sequence || ''))}</span><span class="execution-log-time">${escapeHTML(event.created_at ? new Date(event.created_at).toLocaleTimeString(locale === 'zh' ? 'zh-CN' : 'en-US', {hour12: false}) : '--:--:--')}</span><code>${escapeHTML(text)}</code></div>`;
    }).join('');
  }

  function executionEvidenceHTML(cache) {
    const projection = cache?.projection || {};
    const plan = cache?.plan || {};
    const attempts = Array.isArray(projection.attempts) ? projection.attempts : [];
    const eventCount = Array.isArray(cache?.events) ? cache.events.length : 0;
    return `<div class="execution-evidence-list"><div><span>${escapeHTML(t('runStatus'))}</span><strong>${escapeHTML(executionPlanStatus(plan, projection))}</strong></div><div><span>${escapeHTML(t('runSelectedBy'))}</span><strong class="mono">${escapeHTML(plan.selected_ref?.id || '-')}</strong></div><div><span>${escapeHTML(t('runRevision'))}</span><strong class="mono">${escapeHTML(String(plan.selected_ref?.version || plan.selected_ref?.revision || '-'))}</strong></div><div><span>${escapeHTML(t('runEvents'))}</span><strong>${escapeHTML(String(eventCount))}</strong></div><div><span>${escapeHTML(t('runNodes'))}</span><strong>${escapeHTML(String(Object.keys(projection.nodes || {}).length || (plan.graph_snapshot?.nodes || []).length))}</strong></div><div><span>attempts</span><strong>${escapeHTML(String(attempts.length))}</strong></div></div><div class="execution-digest mono">${escapeHTML(t('runPlanHash'))}: ${escapeHTML(plan.plan_hash || '-')}</div>`;
  }

  async function loadExecutionTimeline(planID) {
    if (!planID) return;
    const current = executionTimelineCache.get(planID) || {};
    executionTimelineCache.set(planID, {...current, loading: true});
    if (currentView === 'executions') render();
    try {
      const response = await api(`/api/v1/execution-plans/${encodeURIComponent(planID)}/timeline`);
      executionTimelineCache.set(planID, {...response, loading: false, fetchedAt: Date.now()});
    } catch (_) {
      executionTimelineCache.set(planID, {...current, loading: false, error: true});
    }
    if (currentView === 'executions') render();
  }

  window.adroOnStreamEvent = () => {
    if (currentView !== 'executions' || !activeExecutionPlanID) return;
    if (executionTimelineRefreshTimer) return;
    executionTimelineRefreshTimer = setTimeout(() => {
      executionTimelineRefreshTimer = null;
      void loadExecutionTimeline(activeExecutionPlanID);
    }, 120);
  };

  renderPipelines = function graphExecutionCockpit() {
    const plans = nativePlans.slice().reverse();
    if (!activeExecutionPlanID && plans[0]) activeExecutionPlanID = plans[0].id;
    const activePlan = plans.find(item => item.id === activeExecutionPlanID) || plans[0];
    if (activePlan && activePlan.id !== activeExecutionPlanID) activeExecutionPlanID = activePlan.id;
    const cache = activePlan ? (executionTimelineCache.get(activePlan.id) || {}) : {};
    const cards = plans.map(plan => executionPlanCard(plan, plan.id === activePlan?.id)).join('');
    if (!activePlan) return `<div class="execution-cockpit view-stack"><section class="execution-hero"><div><span class="execution-kicker">GRAPH RUNNER / LOCAL RUNTIME</span><h2>${escapeHTML(t('executionCockpit'))}</h2><p>${escapeHTML(t('executionCockpitSubtitle'))}</p></div></section><div class="execution-empty-state"><div class="execution-empty-mark">∿</div><strong>${escapeHTML(t('runNoPlans'))}</strong><span>${escapeHTML(t('runNoStagePipeline'))}</span></div></div>`;
    return `<div class="execution-cockpit view-stack"><section class="execution-hero"><div><span class="execution-kicker">GRAPH RUNNER / LOCAL RUNTIME <i></i></span><h2>${escapeHTML(t('executionCockpit'))}</h2><p>${escapeHTML(t('executionCockpitSubtitle'))}</p></div><div class="execution-hero-badge"><span class="terminal-cursor">▋</span>${escapeHTML(t('runLive'))}</div></section><div class="execution-plan-strip">${cards}</div><section class="execution-run-head"><div><span class="mono">${escapeHTML(activePlan.id)}</span><h3>${escapeHTML(requirements.find(item => item.id === activePlan.requirement_id)?.title || activePlan.requirement_id)}</h3><p>${escapeHTML(t('runNoStagePipeline'))}</p></div><button class="secondary" type="button" data-refresh-execution-plan="${escapeHTML(activePlan.id)}">↻ ${escapeHTML(t('runRefresh'))}</button></section><div class="execution-cockpit-grid"><section class="execution-graph-panel"><div class="execution-panel-head"><div><span>${escapeHTML(t('runGraph'))}</span><small>${escapeHTML(String(activePlan.graph_snapshot?.nodes?.length || 0))} ${escapeHTML(t('runNodes'))}</small></div><span class="status ${executionStatusClass(executionPlanStatus(activePlan, cache.projection))}">${escapeHTML(executionPlanStatus(activePlan, cache.projection))}</span></div>${executionGraphHTML(activePlan, cache.projection)}</section><section class="execution-log-panel"><div class="execution-panel-head"><div><span>${escapeHTML(t('runConsole'))}</span><small>${escapeHTML(String(cache.events?.length || 0))} ${escapeHTML(t('runEvents'))}</small></div><span class="execution-live-dot"></span></div><div class="execution-log" aria-live="polite">${executionLogHTML(cache)}</div></section></div><section class="execution-evidence-panel"><div class="execution-panel-head"><div><span>${escapeHTML(t('runEvidence'))}</span><small>${escapeHTML(activePlan.selected_ref?.id || '-')}</small></div></div>${executionEvidenceHTML({...cache, plan: activePlan})}</section></div>`;
  };

  document.addEventListener('click', event => {
    const planButton = event.target.closest?.('[data-execution-plan-id]');
    if (planButton) {
      activeExecutionPlanID = planButton.dataset.executionPlanId;
      render();
      void loadExecutionTimeline(activeExecutionPlanID);
      return;
    }
    const refresh = event.target.closest?.('[data-refresh-execution-plan]');
    if (refresh) void loadExecutionTimeline(refresh.dataset.refreshExecutionPlan);
  });

  const baseRender = render;
  render = function enhancedRender() {
    // Core polling refreshes the shared data model every 20 seconds. Keep an
    // active chat DOM stable during that refresh; chat-specific operations
    // already reload the transcript and own their render cycle.
    if (currentView === 'chats' && ($('#chatComposer' || $('#chatCreateDialog')?.open))) return;
    baseRender();
    if (currentView === 'delivery') {
      $('#pageTitle').textContent = t('deliveryTitle');
      $('#pageSubtitle').textContent = t('deliverySubtitle');
      $('#pageActions').innerHTML = `<button class="primary" id="newRequirement"><span aria-hidden="true">＋</span>${escapeHTML(t('deliveryCreate'))}</button>`;
      $('#appView').innerHTML = renderDelivery();
      $('#newRequirement').onclick = () => showDialog('requirement');
      bindDeliveryView();
    }
    if (currentView === 'executions') {
      $('#pageTitle').textContent = t('deliveryTitle');
      $('#pageSubtitle').textContent = t('executionCockpitSubtitle');
      $('#pageActions').innerHTML = `<button class="secondary" id="deliveryBack" type="button">← ${escapeHTML(t('deliveryBack'))}</button>`;
      $('#deliveryBack').onclick = () => {
        currentView = 'delivery';
        applyMenuAccess();
        render();
      };
      if (activeExecutionPlanID && !executionTimelineCache.has(activeExecutionPlanID)) void loadExecutionTimeline(activeExecutionPlanID);
    }
    if (currentView === 'chats') renderChatPage();
  };
  const chatNav = document.querySelector('[data-view="chats"]');
  if (chatNav) chatNav.addEventListener('click', () => { setTimeout(loadChatList, 0); });

  async function bootstrap() {
    applyTranslations();
    try {
      const response = await api('/api/v1/auth/me');
      if (!response.user) throw new Error('interactive identity required');
      await enterApplication(response.user);
    } catch (_) {
      showLogin();
    }
  }

  bootstrap();
})();
