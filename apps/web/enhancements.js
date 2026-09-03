(() => {
  const menuIDs = [
    'workbench', 'requirements', 'bugs', 'humanQA', 'designReview', 'executions',
    'diffs', 'testing', 'chats', 'repositories', 'agents', 'mcp', 'skills', 'automations',
    'integrations', 'artifacts', 'runners', 'cost', 'admin'
  ];

  Object.assign(translations.zh, {
    chats: '普通聊天', chatSubtitle: '绑定项目的持久化讨论空间', newChat: '新建会话', chatTitle: '会话标题', chatProject: '绑定项目', chatMessagePlaceholder: '输入消息，讨论方案或上下文', sendMessage: '发送', noChats: '还没有聊天会话', noMessages: '开始一段新的讨论', chatSendFailed: '消息发送失败', chatCreateFailed: '会话创建失败', chatAttachments: '添加附件',
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
    runnerWorkspaceRoot: '工作区根目录', executeRunner: '执行命令', runnerCommand: '命令', runnerCommandPlaceholder: '例如 go test ./...', runnerWorkDir: '工作目录', runnerWorkDirPlaceholder: '留空使用 Runner 根目录', runnerEnv: '环境变量 JSON', runnerEnvPlaceholder: '{"CI":"true"}', runnerTimeout: '超时（毫秒）', runnerExecuteFailed: 'Runner 执行失败，请检查命令、路径和权限'
    ,nativeAgents: '版本化 Agent', nativeSquads: '已定义小队', executionPlans: '执行计划', newSquad: '新建小队', newPlan: '新建计划', validate: '校验', dryRun: 'Dry run', publish: '发布', enable: '启用', disable: '停用', archive: '归档', timeline: '时间线', replay: '重放', revision: '修订', graphNodes: '图节点', selectedTarget: '执行目标', squadName: '小队名称', squadDescription: '职责说明', squadLeader: 'Leader Agent', squadCreateFailed: '小队创建失败', planRequirement: '需求', planTarget: 'Agent / 小队', planCreateFailed: '执行计划创建失败', orchestrationReady: '原生自由编排控制面', orchestrationHelp: 'Agent 与 Squad 使用冻结 revision；发布计划后可从 timeline 重放每个 attempt、edge 与 evidence。', legacyBindings: '兼容责任人绑定', nativeAgentHelp: '此表直接读取 revisioned AgentDefinition，不再以显示名或旧 developer profile 作为编排主键。', lifecycleActionFailed: '生命周期操作失败', noPublishedTarget: '请先启用 Agent 或发布 Squad', planHash: 'Plan hash', openTimeline: '查看不可变事件时间线', closeTimeline: '关闭时间线', editGraph: '编辑图', forkSquad: '复制模板', graphEditor: 'Workflow Graph 编辑器', graphJSON: 'Graph JSON', graphJSONHelp: '导入/导出同一份 WorkflowGraph；发布前必须校验。', formatGraph: '格式化', validateGraph: '校验图', saveGraph: '保存图', graphSaved: '图已保存', graphValidationFailed: '图校验失败', graphNodeHint: '节点与边可任意增删；条件、回退、重试和汇聚保存在 JSON 契约中。', comments: '评论', commentPlaceholder: '输入评论，使用 @ 选择 Agent 或 Squad', preview: '预览触发', sendComment: '发布评论', commentSent: '评论已发布', commentPreviewFailed: '触发预览失败', noComments: '暂无评论', triggerOutcomes: '触发结果', invokeAgent: '调用 Agent', invokeSquad: '调用 Squad'
  });
  Object.assign(translations.en, {
    chats: 'Chat', chatSubtitle: 'Durable project-bound conversations', newChat: 'New conversation', chatTitle: 'Conversation title', chatProject: 'Project binding', chatMessagePlaceholder: 'Discuss an idea or share context', sendMessage: 'Send', noChats: 'No conversations yet', noMessages: 'Start a new discussion', chatSendFailed: 'Could not send the message', chatCreateFailed: 'Could not create the conversation', chatAttachments: 'Add attachments',
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
    runnerWorkspaceRoot: 'Workspace root', executeRunner: 'Execute command', runnerCommand: 'Command', runnerCommandPlaceholder: 'For example: go test ./...', runnerWorkDir: 'Working directory', runnerWorkDirPlaceholder: 'Leave blank to use the runner root', runnerEnv: 'Environment JSON', runnerEnvPlaceholder: '{"CI":"true"}', runnerTimeout: 'Timeout (ms)', runnerExecuteFailed: 'Runner execution failed; check the command, path, and permissions'
    ,nativeAgents: 'Revisioned agents', nativeSquads: 'Squad definitions', executionPlans: 'Execution plans', newSquad: 'New squad', newPlan: 'New plan', validate: 'Validate', dryRun: 'Dry run', publish: 'Publish', enable: 'Enable', disable: 'Disable', archive: 'Archive', timeline: 'Timeline', replay: 'Replay', revision: 'Revision', graphNodes: 'Graph nodes', selectedTarget: 'Execution target', squadName: 'Squad name', squadDescription: 'Responsibility', squadLeader: 'Leader agent', squadCreateFailed: 'Could not create squad', planRequirement: 'Requirement', planTarget: 'Agent / squad', planCreateFailed: 'Could not create execution plan', orchestrationReady: 'Native free-form orchestration', orchestrationHelp: 'Agents and squads pin immutable revisions; a published plan can replay every attempt, edge, and evidence receipt from its timeline.', legacyBindings: 'Compatibility member bindings', nativeAgentHelp: 'This table reads revisioned AgentDefinition records directly; display names and legacy developer profiles are not orchestration identities.', lifecycleActionFailed: 'Lifecycle action failed', noPublishedTarget: 'Enable an agent or publish a squad first', planHash: 'Plan hash', openTimeline: 'Open immutable event timeline', closeTimeline: 'Close timeline', editGraph: 'Edit graph', forkSquad: 'Copy template', graphEditor: 'Workflow Graph editor', graphJSON: 'Graph JSON', graphJSONHelp: 'Import or export the same WorkflowGraph contract; validate before publishing.', formatGraph: 'Format', validateGraph: 'Validate graph', saveGraph: 'Save graph', graphSaved: 'Graph saved', graphValidationFailed: 'Graph validation failed', graphNodeHint: 'Nodes and edges are free-form; predicates, feedback, retries, and joins stay in the JSON contract.', comments: 'Comments', commentPlaceholder: 'Write a comment; use @ to choose an Agent or Squad', preview: 'Preview triggers', sendComment: 'Post comment', commentSent: 'Comment posted', commentPreviewFailed: 'Could not preview triggers', noComments: 'No comments yet', triggerOutcomes: 'Trigger outcomes', invokeAgent: 'Invoke agent', invokeSquad: 'Invoke squad'
  });
  Object.assign(translations.zh, { reply: '回复', cancelReply: '取消回复', retryTrigger: '重试触发', attachComment: '添加附件', attachmentReady: '附件已准备', commentReplyingTo: '正在回复', commentEmpty: '评论内容不能为空', commentSendFailed: '评论发布失败', commentLoadFailed: '评论加载失败', commentPreview: '触发预览', commentPreviewReady: '预览已更新', commentPreviewNoTargets: '没有可触发的结构化 mention', mentionAgent: 'Agent', mentionSquad: 'Squad', commentOutcome: '触发结果', commentFollowUp: '执行收据', commentNoOutcome: '暂无触发结果', outcomeQueued: '已排队', outcomeCoalesced: '已合并', outcomeDeferred: '已延迟', outcomeBlocked: '已阻止', outcomeStarted: '已启动', outcomeRunning: '运行中', outcomeCompleted: '已完成', outcomeFailed: '失败', outcomeRetrying: '重试中', outcomeCancelled: '已取消', outcomeTimedOut: '已超时', outcomeNotRequested: '未请求', planGraph: '计划图', planGraphHelp: '可在提交前临时编辑已选 Agent 或 Squad 的图。', planGraphLoad: '载入图', planGraphValidate: '校验并预览', planGraphStatus: '提交前检查', planGraphReady: '计划图已通过检查', planGraphInvalid: '计划图校验失败', graphDiagnostics: '节点/边/循环/并发' });
  Object.assign(translations.en, { reply: 'Reply', cancelReply: 'Cancel reply', retryTrigger: 'Retry trigger', attachComment: 'Attach files', attachmentReady: 'Files attached', commentReplyingTo: 'Replying to', commentEmpty: 'Comment cannot be empty', commentSendFailed: 'Could not post the comment', commentLoadFailed: 'Could not load comments', commentPreview: 'Preview triggers', commentPreviewReady: 'Preview updated', commentPreviewNoTargets: 'No structured mentions to invoke', mentionAgent: 'Agent', mentionSquad: 'Squad', commentOutcome: 'Trigger outcome', commentFollowUp: 'Execution receipt', commentNoOutcome: 'No trigger outcome', outcomeQueued: 'Queued', outcomeCoalesced: 'Coalesced', outcomeDeferred: 'Deferred', outcomeBlocked: 'Blocked', outcomeStarted: 'Started', outcomeRunning: 'Running', outcomeCompleted: 'Completed', outcomeFailed: 'Failed', outcomeRetrying: 'Retrying', outcomeCancelled: 'Cancelled', outcomeTimedOut: 'Timed out', outcomeNotRequested: 'Not requested', planGraph: 'Plan graph', planGraphHelp: 'Temporarily edit the selected Agent or Squad graph before submitting.', planGraphLoad: 'Load graph', planGraphValidate: 'Validate and preview', planGraphStatus: 'Pre-submit checks', planGraphReady: 'Plan graph passed checks', planGraphInvalid: 'Plan graph validation failed', graphDiagnostics: 'nodes / edges / loops / concurrency' });

  let currentUser = null;
  let directory = [];
  let managedUsers = [];
  let availableMenus = menuIDs.slice();
  let nativeAgents = [];
  let nativeSquads = [];
  let nativePlans = [];
  let activeGraphEditor = null;
  let commentReplyParentID = '';
  let commentMentionIndex = -1;
  let commentMentionOptions = [];
  let commentMentionStart = -1;
  let commentMentionTargetID = '';
  let commentDraftFiles = [];
  let commentMentionRoster = [];
  let commentMentionRosterPromise = null;
  let activeCommentTargetID = '';
  let activeCommentItems = [];
  let commentActivity = new Map();

  const baseOrchestrationLoadCore = loadCore;
  loadCore = async function loadCoreWithOrchestration(force = false) {
    await baseOrchestrationLoadCore(force);
    if (window.adroCanAccessMenu?.('agents')) await loadOrchestrationData();
  };

  async function loadOrchestrationData() {
    const settled = await Promise.allSettled([
      api('/api/v1/workspaces/local/agents'),
      api('/api/v1/workspaces/local/squads'),
      api('/api/v1/execution-plans?workspace_id=local')
    ]);
    if (settled[0].status === 'fulfilled') nativeAgents = settled[0].value.items || [];
    if (settled[1].status === 'fulfilled') nativeSquads = settled[1].value.items || [];
    if (settled[2].status === 'fulfilled') nativePlans = settled[2].value.items || [];
    if (currentView === 'agents') render();
  }

  const focusIfPresent = selector => {
    const element = $(selector);
    if (element) element.focus();
  };
  const idempotencyKey = () => typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : String(Date.now());

  window.adroCanAccessMenu = menu => (currentUser && currentUser.role === 'admin') || availableMenus.includes(menu);

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
      item.hidden = !availableMenus.includes(item.dataset.view);
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
    if (!availableMenus.includes(currentView)) {
      currentView = availableMenus[0] || 'workbench';
    }
    document.querySelectorAll('.nav-item, .nav-chat').forEach(item => item.classList.toggle('active', item.dataset.view === currentView));
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

  $('#logoutButton').onclick = async () => {
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

  showDialog = async function enhancedRequirementDialog() {
    $('#formError').textContent = '';
    await loadIdentityData();
    $('#requirementRepository').innerHTML = optionMarkup(repositories, item => item.id, item => item.canonical_name, 'noProjects');
    $('#requirementAssignee').innerHTML = optionMarkup(directory, item => item.id, item => `${item.display_name} · ${item.username}`, 'noExecutors');
    applyTranslations();
    $('#requirementDialog').showModal();
    setTimeout(() => focusIfPresent('#requirementForm input[name="title"]'), 0);
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
    const criteria = String(data.get('acceptance')).split('\n').map(item => item.trim()).filter(Boolean);
    const files = Array.from(formElement.elements.attachments.files || []);
    $('#formError').textContent = '';
    submit.disabled = true;
    try {
      const created = await api('/api/v1/requirements', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey() },
        body: JSON.stringify({
          workspace_id: 'local', title: String(data.get('title')).trim(), description: String(data.get('description')).trim(),
          acceptance_criteria: criteria, assignee_member_ids: [String(data.get('assignee'))],
          repository_ids: [String(data.get('repository'))], priority: String(data.get('priority') || 'normal')
        })
      });
      try { await uploadEntityFiles('requirement', created.id, files); } catch (_) { $('#formError').textContent = t('uploadFailed'); return; }
      closeDialog();
      formElement.reset();
      await loadCore(true);
    } catch (_) {
      $('#formError').textContent = t('createFailed');
    } finally {
      submit.disabled = false;
    }
  };

  const baseOpenResourceDialog = openResourceDialog;
  openResourceDialog = function enhancedResourceDialog(kind) {
    if (kind === 'bug') {
      showBugDialog();
      return;
    }
    baseOpenResourceDialog(kind);
  };

  async function showBugDialog() {
    $('#bugFormError').textContent = '';
    // Open from the cached control-plane snapshot first. Directory refreshes
    // can be slow while the stream is reconnecting, but they must not make the
    // create action appear unresponsive.
    const populate = () => {
      $('#bugRepository').innerHTML = optionMarkup(repositories, item => item.id, item => item.canonical_name, 'noProjects');
      $('#bugAssignee').innerHTML = optionMarkup(directory, item => item.id, item => `${item.display_name} · ${item.username}`, 'noExecutors');
      $('#bugRequirement').innerHTML = optionMarkup(requirements, item => item.id, item => `${item.key} · ${item.title}`, 'noRelatedRequirement', true);
    };
    populate();
    applyTranslations();
    $('#bugDialog').showModal();
    setTimeout(() => focusIfPresent('#bugForm input[name="title"]'), 0);
    await loadIdentityData();
    if ($('#bugDialog').open) {
      populate();
      applyTranslations();
    }
  }

  const closeBugDialog = () => $('#bugDialog').close();
  $('#closeBugDialog').onclick = closeBugDialog;
  $('#cancelBugDialog').onclick = closeBugDialog;
  $('#bugDialog').addEventListener('click', event => { if (event.target === event.currentTarget) closeBugDialog(); });
  $('#bugForm').onsubmit = async event => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const data = new FormData(formElement);
    const submit = formElement.querySelector('button[type="submit"]');
    const files = Array.from(formElement.elements.attachments.files || []);
    $('#bugFormError').textContent = '';
    submit.disabled = true;
    try {
      const created = await api('/api/v1/bugs', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: 'local', title: String(data.get('title')).trim(), repository_id: String(data.get('repository')),
          assignee_member_id: String(data.get('assignee')), requirement_id: String(data.get('requirement') || ''),
          steps_to_reproduce: String(data.get('steps')).trim(), expected: String(data.get('expected')).trim(),
          actual: String(data.get('actual')).trim(), log_excerpt: String(data.get('log')).trim()
        })
      });
      try { await uploadEntityFiles('bug', created.id, files); } catch (_) { $('#bugFormError').textContent = t('uploadFailed'); return; }
      closeBugDialog();
      formElement.reset();
      await loadCore(true);
    } catch (_) {
      $('#bugFormError').textContent = t('resourceSaveFailed');
    } finally {
      submit.disabled = false;
    }
  };

  renderBugs = function enhancedBugTable() {
    const rows = bugs.map(item => `<tr data-bug-id="${escapeHTML(item.id)}"><td class="mono">${escapeHTML((item.id || '').slice(0, 10))}</td><td class="title-cell">${escapeHTML(item.title || '-')}</td><td><span class="status ${statusClass(item.status)}">${escapeHTML(statusLabel(item.status))}</span></td><td>${escapeHTML(repositoryLabel(item.repository_id))}</td><td class="muted">${escapeHTML(requirementLabel(item.requirement_id))}</td><td class="muted">${escapeHTML(userLabel(item.assignee_member_id))}</td><td><div class="row-actions">${item.status === 'OPEN' ? actionButton(item.id, 'bug', 'repair', 'accent') : ''}${item.status === 'HUMAN_TRIAGE_REQUIRED' ? actionButton(item.id, 'bug', 'triage') : ''}${item.status === 'REPAIRING' ? actionButton(item.id, 'bug', 'verify', 'accent') : ''}</div></td></tr>`);
    return `<div class="view-stack"><div class="menu-intro"><strong>${escapeHTML(t('menuOwned'))}</strong><span>${escapeHTML(t('menuActionHint'))}</span></div><div class="view-grid">${summaryCard(t('openBugs'), bugs.filter(item => item.status === 'OPEN').length, t('needsAttention'))}${summaryCard(t('repairingTitle'), bugs.filter(item => item.status === 'REPAIRING').length, t('repairing'))}${summaryCard(t('escalatedTitle'), bugs.filter(item => item.status === 'HUMAN_TRIAGE_REQUIRED').length, t('escalated'))}</div>${genericTable(t('bugs'), [t('key'), t('title'), t('status'), t('project'), t('requirementRelation'), t('executorColumn'), t('actions')], rows, t('noBugs'))}</div>`;
  };

  renderAdmin = function enhancedAdmin() {
    const userRows = managedUsers.map(user => `<tr><td><strong>${escapeHTML(user.display_name)}</strong><div class="mono">${escapeHTML(user.username)}</div></td><td><span class="status ${user.role === 'admin' ? 'active' : ''}">${escapeHTML(roleLabel(user.role))}</span></td><td><span class="status ${user.status === 'active' ? 'good' : 'bad'}">${escapeHTML(t(user.status === 'active' ? 'activeAccount' : 'disabledAccount'))}</span></td><td><span class="permission-count">${user.menu_ids.length} / ${menuIDs.length}</span></td><td><button class="action-button" type="button" data-edit-user="${escapeHTML(user.id)}">${escapeHTML(t('edit'))}</button></td></tr>`);
    const usersPanel = `<section class="panel"><div class="panel-head"><h2>${escapeHTML(t('userManagement'))}</h2><small>${managedUsers.length} ${escapeHTML(t('identityCount'))}</small></div><div class="admin-toolbar"><p>${escapeHTML(t('menuPermissionsHelp'))}</p><button class="primary" id="newUser" type="button"><span aria-hidden="true">＋</span>${escapeHTML(t('createUser'))}</button></div><div class="table-scroll"><table><thead><tr><th>${escapeHTML(t('displayName'))}</th><th>${escapeHTML(t('role'))}</th><th>${escapeHTML(t('accountStatus'))}</th><th>${escapeHTML(t('permissionSummary'))}</th><th>${escapeHTML(t('actions'))}</th></tr></thead><tbody>${userRows.length ? userRows.join('') : `<tr><td colspan="5" class="empty">${escapeHTML(t('noItems'))}</td></tr>`}</tbody></table></div></section>`;
    const audit = genericTable(t('auditChain'), [t('key'), t('eventType'), t('source'), t('time')], auditItems.slice(-25).reverse().map(item => `<tr><td class="mono">${escapeHTML(String(item.sequence || '').padStart(4, '0'))}</td><td>${escapeHTML(item.action || '-')}</td><td class="muted">${escapeHTML(item.actor_id || '-')}</td><td class="muted">${escapeHTML(new Date(item.created_at || Date.now()).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US'))}</td></tr>`), t('noEvents'));
    return `<div class="view-stack"><div class="menu-intro"><strong>${escapeHTML(t('accessControl'))}</strong><span>${escapeHTML(t('menuActionHint'))}</span></div>${usersPanel}${audit}</div>`;
  };

  function orchestrationAction(id, kind, action, label, variant = '') {
    return `<button class="action-button ${variant}" type="button" data-orchestration-kind="${escapeHTML(kind)}" data-orchestration-id="${escapeHTML(id)}" data-orchestration-action="${escapeHTML(action)}">${escapeHTML(t(label || action))}</button>`;
  }

  function ensureGraphDialog() {
    if ($('#graphEditorDialog')) return;
    document.body.insertAdjacentHTML('beforeend', `<dialog id="graphEditorDialog" class="orchestration-dialog graph-editor-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / WORKFLOW GRAPH</p><h2>${escapeHTML(t('graphEditor'))}</h2><p id="graphEditorTarget" class="mono"></p></div><button class="dialog-close" id="closeGraphEditor" type="button" aria-label="${escapeHTML(t('close'))}">×</button></div><form id="graphEditorForm"><label><span>${escapeHTML(t('graphJSON'))}</span><textarea id="graphEditorJSON" spellcheck="false" required></textarea><small class="form-help">${escapeHTML(t('graphJSONHelp'))}</small></label><div id="graphEditorSummary" class="graph-editor-summary"></div><p id="graphEditorStatus" class="form-error" role="status"></p><div class="form-actions"><button class="secondary" id="graphEditorFormat" type="button">${escapeHTML(t('formatGraph'))}</button><button class="secondary" id="graphEditorValidate" type="button">${escapeHTML(t('validateGraph'))}</button><button class="primary" id="graphEditorSave" type="submit">${escapeHTML(t('saveGraph'))}</button></div></form></dialog>`);
    $('#closeGraphEditor').onclick = () => $('#graphEditorDialog').close();
    $('#graphEditorDialog').addEventListener('click', event => { if (event.target === event.currentTarget) event.currentTarget.close(); });
    $('#graphEditorFormat').onclick = () => {
      try { $('#graphEditorJSON').value = JSON.stringify(JSON.parse($('#graphEditorJSON').value), null, 2); setGraphEditorStatus(''); } catch (_) { setGraphEditorStatus(t('graphValidationFailed'), true); }
    };
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

  function graphEditorInput() {
    try {
      const graph = JSON.parse($('#graphEditorJSON').value);
      if (!graph || typeof graph !== 'object' || Array.isArray(graph)) throw new Error('graph must be an object');
      return graph;
    } catch (error) {
      setGraphEditorStatus(error.message || t('graphValidationFailed'), true);
      return null;
    }
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

  function openGraphEditor(squad) {
    if (!squad) return;
    ensureGraphDialog();
    activeGraphEditor = {id: squad.id, revision: squad.revision, name: squad.name};
    $('#graphEditorTarget').textContent = `${squad.name || squad.id} · r${squad.revision}`;
    $('#graphEditorJSON').value = JSON.stringify(squad.graph || {}, null, 2);
    renderGraphEditorSummary(squad.graph || {});
    setGraphEditorStatus('');
    $('#graphEditorDialog').showModal();
    setTimeout(() => $('#graphEditorJSON').focus(), 0);
  }

  async function saveGraphEditor(event) {
    event.preventDefault();
    if (!activeGraphEditor) return;
    if (!(await validateGraphEditor())) return;
    const graph = graphEditorInput();
    try {
      await api(`/api/v1/workspaces/local/squads/${encodeURIComponent(activeGraphEditor.id)}/graph`, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({expected_revision: activeGraphEditor.revision, graph})});
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
    host.innerHTML = `<div class="plan-graph-head"><strong>${escapeHTML(t('planGraph'))}</strong><button class="secondary" id="nativePlanGraphLoad" type="button">${escapeHTML(t('planGraphLoad'))}</button></div><textarea id="nativePlanGraph" spellcheck="false" aria-label="${escapeHTML(t('planGraph'))}"></textarea><small class="form-help">${escapeHTML(t('planGraphHelp'))}</small><div id="nativePlanGraphSummary" class="graph-editor-summary"></div><p id="nativePlanGraphStatus" class="form-help" role="status"></p><div class="form-actions plan-graph-actions"><button class="secondary" id="nativePlanGraphFormat" type="button">${escapeHTML(t('formatGraph'))}</button><button class="secondary" id="nativePlanGraphValidate" type="button">${escapeHTML(t('planGraphValidate'))}</button></div>`;
    $('#nativePlanTarget').addEventListener('change', loadNativePlanGraph);
    $('#nativePlanGraphLoad').onclick = loadNativePlanGraph;
    $('#nativePlanGraphFormat').onclick = () => {
      const graph = nativePlanGraphInput();
      if (graph) {
        $('#nativePlanGraph').value = JSON.stringify(graph, null, 2);
        renderNativePlanGraphSummary(graph);
        setNativePlanGraphStatus('');
      }
    };
    $('#nativePlanGraphValidate').onclick = () => validateNativePlanGraph(String($('#nativePlanRequirement')?.value || ''));
  }

  renderAgents = function nativeOrchestrationStudio() {
    const agentRows = nativeAgents.map(agent => {
      const lifecycle = agent.status === 'active'
        ? orchestrationAction(agent.id, 'agent', 'disable', 'disable')
        : agent.status !== 'archived' ? orchestrationAction(agent.id, 'agent', 'enable', 'enable', 'accent') : '';
      const archive = agent.status !== 'archived' ? orchestrationAction(agent.id, 'agent', 'archive', 'archive', 'danger') : '';
      return `<tr><td><strong>${escapeHTML(agent.name || agent.id)}</strong><div class="mono orchestration-id">${escapeHTML(agent.id)}</div></td><td>${escapeHTML(agent.role || '-')}</td><td><span class="status ${agent.status === 'active' ? 'good' : agent.status === 'archived' ? 'bad' : 'warn'}">${escapeHTML(agent.status)}</span></td><td class="mono">r${escapeHTML(String(agent.revision || 0))}</td><td class="muted">${escapeHTML((agent.capabilities || []).map(item => item.name).join(', ') || '-')}</td><td><div class="row-actions">${orchestrationAction(agent.id, 'agent', 'validate', 'validate')}${orchestrationAction(agent.id, 'agent', 'capabilities', 'capabilities')}${lifecycle}${archive}</div></td></tr>`;
    });
    const squadRows = nativeSquads.map(squad => {
      const publish = squad.status === 'draft' ? orchestrationAction(squad.id, 'squad', 'publish', 'publish', 'accent') : '';
      const disable = squad.status === 'published' ? orchestrationAction(squad.id, 'squad', 'disable', 'disable') : '';
      const archive = squad.status !== 'archived' ? orchestrationAction(squad.id, 'squad', 'archive', 'archive', 'danger') : '';
      return `<tr><td><strong>${escapeHTML(squad.name || squad.id)}</strong><div class="mono orchestration-id">${escapeHTML(squad.id)}</div></td><td><span class="status ${squad.status === 'published' ? 'good' : squad.status === 'archived' ? 'bad' : 'active'}">${escapeHTML(squad.status)}</span></td><td class="mono">r${escapeHTML(String(squad.revision || 0))} · v${escapeHTML(String(squad.published_version || 0))}</td><td>${escapeHTML(String((squad.members || []).length))}</td><td>${escapeHTML(String((squad.graph?.nodes || []).length))}</td><td><div class="row-actions">${orchestrationAction(squad.id, 'squad', 'edit-graph', 'editGraph', 'accent')}${orchestrationAction(squad.id, 'squad', 'fork', 'forkSquad')}${orchestrationAction(squad.id, 'squad', 'validate', 'validate')}${orchestrationAction(squad.id, 'squad', 'dry-run', 'dryRun')}${publish}${disable}${archive}</div></td></tr>`;
    });
    const planRows = nativePlans.slice().reverse().map(plan => `<tr><td><strong>${escapeHTML(plan.requirement_id || '-')}</strong><div class="mono orchestration-id">${escapeHTML(plan.id)}</div></td><td><span class="status ${plan.status === 'ready' ? 'good' : 'active'}">${escapeHTML(plan.status || '-')}</span></td><td class="mono">${escapeHTML(plan.selected_ref?.id || '-')}@${escapeHTML(String(plan.selected_ref?.version || plan.selected_ref?.revision || '-'))}</td><td>${escapeHTML(String((plan.graph_snapshot?.nodes || []).length))}</td><td class="mono digest-cell" title="${escapeHTML(plan.plan_hash || '')}">${escapeHTML((plan.plan_hash || '-').slice(0, 16))}</td><td><div class="row-actions">${orchestrationAction(plan.id, 'plan', 'timeline', 'timeline', 'accent')}${orchestrationAction(plan.id, 'plan', 'replay', 'replay')}</div></td></tr>`);
    const legacyRows = agentProfiles.map(profile => `<tr><td class="mono">${escapeHTML(profile.member_id || '-')}</td><td class="mono">${escapeHTML(profile.default_agent_binding_id || '-')}</td><td>${escapeHTML(profile.default_role || '-')}</td><td><span class="status warn">compat</span></td></tr>`);
    return `<div class="view-stack orchestration-studio"><section class="orchestration-hero"><div><span class="orchestration-kicker">GRAPH-NATIVE / REVISION-LOCKED</span><h2>${escapeHTML(t('orchestrationReady'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><div class="orchestration-hero-actions"><button class="secondary" id="newSquad" type="button"><span aria-hidden="true">◇</span>${escapeHTML(t('newSquad'))}</button><button class="primary" id="newPlan" type="button"><span aria-hidden="true">▶</span>${escapeHTML(t('newPlan'))}</button></div></section><div id="orchestrationStatus" class="orchestration-status" role="status"></div><div class="view-grid orchestration-metrics">${summaryCard(t('nativeAgents'), nativeAgents.length, t('nativeAgentHelp'))}${summaryCard(t('nativeSquads'), nativeSquads.length, t('graphNodes'))}${summaryCard(t('executionPlans'), nativePlans.length, t('planHash'))}</div>${genericTable(t('nativeAgents'), [t('name'), t('role'), t('status'), t('revision'), t('capabilities'), t('actions')], agentRows, t('noItems'))}${genericTable(t('nativeSquads'), [t('name'), t('status'), t('revision'), t('agents'), t('graphNodes'), t('actions')], squadRows, t('noItems'))}${genericTable(t('executionPlans'), [t('planRequirement'), t('status'), t('selectedTarget'), t('graphNodes'), t('planHash'), t('actions')], planRows, t('noItems'))}${legacyRows.length ? genericTable(t('legacyBindings'), [t('assignees'), t('agentBinding'), t('role'), t('status')], legacyRows, t('noItems')) : ''}</div>`;
  };

  function ensureOrchestrationDialogs() {
    if ($('#squadDialog')) return;
    document.body.insertAdjacentHTML('beforeend', `<dialog id="squadDialog" class="orchestration-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / SQUAD</p><h2>${escapeHTML(t('newSquad'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><button class="dialog-close" data-close-orchestration="squadDialog" type="button">×</button></div><form id="squadForm"><label><span>${escapeHTML(t('squadName'))}</span><input name="name" required></label><label><span>${escapeHTML(t('squadDescription'))}</span><textarea name="description"></textarea></label><label><span>${escapeHTML(t('squadLeader'))}</span><select name="leader" id="squadLeader" required></select></label><p class="form-error" id="squadFormError" role="alert"></p><div class="form-actions"><button class="secondary" data-close-orchestration="squadDialog" type="button">${escapeHTML(t('cancel'))}</button><button class="primary" type="submit">${escapeHTML(t('create'))}</button></div></form></dialog><dialog id="nativePlanDialog" class="orchestration-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / IMMUTABLE PLAN</p><h2>${escapeHTML(t('newPlan'))}</h2><p>${escapeHTML(t('orchestrationHelp'))}</p></div><button class="dialog-close" data-close-orchestration="nativePlanDialog" type="button">×</button></div><form id="nativePlanForm"><label><span>${escapeHTML(t('planRequirement'))}</span><select name="requirement" id="nativePlanRequirement" required></select></label><label><span>${escapeHTML(t('planTarget'))}</span><select name="target" id="nativePlanTarget" required></select></label><div id="nativePlanGraphControls"></div><p class="form-error" id="nativePlanFormError" role="alert"></p><div class="form-actions"><button class="secondary" data-close-orchestration="nativePlanDialog" type="button">${escapeHTML(t('cancel'))}</button><button class="primary" type="submit">${escapeHTML(t('publish'))}</button></div></form></dialog><dialog id="timelineDialog" class="orchestration-dialog timeline-dialog"><div class="dialog-head"><div><p class="dialog-kicker">ADRO / REPLAY</p><h2>${escapeHTML(t('timeline'))}</h2><p id="timelinePlanID" class="mono"></p></div><button class="dialog-close" data-close-orchestration="timelineDialog" type="button">×</button></div><pre id="timelineContent" class="timeline-content"></pre><div class="form-actions"><button class="secondary" data-close-orchestration="timelineDialog" type="button">${escapeHTML(t('closeTimeline'))}</button></div></dialog>`);
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
    const target = $('#orchestrationStatus');
    if (!target) return;
    target.textContent = message;
    target.className = `orchestration-status ${bad ? 'bad' : 'good'}`;
  }

  function openSquadDialog() {
    ensureOrchestrationDialogs();
    $('#squadFormError').textContent = '';
    $('#squadLeader').innerHTML = nativeAgents.filter(agent => agent.status === 'active').map(agent => `<option value="${escapeHTML(agent.id)}">${escapeHTML(agent.name)} · r${escapeHTML(String(agent.revision))}</option>`).join('');
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
    const body = {
      name: String(data.get('name')).trim(), description: String(data.get('description')).trim(), status: 'draft',
      members: [{id: 'leader', agent_id: leader.id, role: leader.role || 'leader', leader: true, input_schema: leader.input_schema, output_schema: leader.output_schema, max_attempts: 3, budget: {tokens: 120000, tool_calls: 200, concurrent: 1}}],
      graph: graphForNativeAgent(leader),
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
    renderPermissionGrid((user && user.menu_ids) || ['workbench', 'requirements', 'bugs'], form.elements.role.value);
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
  window.adroOpenRunnerExecuteDialog = runnerID => {
    const form = $('#runnerExecuteForm');
    form.reset();
    form.elements.runner_id.value = runnerID;
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
    let env = {};
    try {
      const rawEnv = String(data.get('env') || '').trim();
      if (rawEnv) {
        env = JSON.parse(rawEnv);
        if (!env || Array.isArray(env) || typeof env !== 'object') throw new Error('env must be an object');
      }
    } catch (_) {
      $('#runnerExecuteError').textContent = t('runnerExecuteFailed');
      return;
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
      button.onclick = () => applyOrchestrationAction(button.dataset.orchestrationKind, button.dataset.orchestrationId, button.dataset.orchestrationAction);
    });
  };

  ensureOrchestrationDialogs();
  $('#agentForm').onsubmit = async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const member = String(data.get('member') || '').trim();
    const name = String(data.get('name') || '').trim();
    const instructions = String(data.get('instructions') || '').trim();
    const role = String(data.get('role') || '').trim() || 'developer';
    const nativeBody = {
      name, role, instructions, status: 'active', created_by: currentUser?.id || member,
      capabilities: [{name: 'session.start', version: 'v1'}, {name: 'stream.events', version: 'v1'}],
      executor_binding: {provider_id: rootInfo.provider || 'local', required_caps: ['run.snapshot.v1'], config_version: 'web-v1'},
      concurrency_budget: {tokens: 120000, tool_calls: 200, concurrent: 1},
      input_schema: {id: 'adro.context-envelope', version: 1}, output_schema: {id: 'adro.structured-result', version: 1},
      tool_policy: {network: false}, memory_policy: {require_evidence: true}
    };
    $('#agentFormError').textContent = '';
    try {
      await api('/api/v1/workspaces/local/agents', {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(nativeBody)});
      await api('/api/v1/agents', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({workspace_id: 'local', member_id: member, name, instructions, role})});
      closeAgentDialog(); form.reset(); await loadCore(true);
    } catch (_) { $('#agentFormError').textContent = t('agentSaveFailed'); }
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
      not_requested: 'outcomeNotRequested', unavailable: 'outcomeBlocked', rejected: 'outcomeBlocked'
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

  async function loadCommentThread(requirementID, initialItems = []) {
    if (activeCommentTargetID !== requirementID) return;
    try {
      const response = await api(`/api/v1/requirements/${encodeURIComponent(requirementID)}/comments?limit=250`);
      if (activeCommentTargetID !== requirementID) return;
      activeCommentItems = response.items || initialItems || [];
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
      const response = await api(`/api/v1/requirements/${encodeURIComponent(activeCommentTargetID)}/comments/trigger-preview`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({comment_id: `preview-${idempotencyKey()}`, revision: 1, content: input.value})});
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
      const created = await api(`/api/v1/requirements/${encodeURIComponent(activeCommentTargetID)}/comments`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify(body)});
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
      await loadCommentThread(activeCommentTargetID);
      if (status) status.textContent = uploadFailures ? t('uploadFailed') : t('commentSent');
    } catch (_) {
      if (status) status.textContent = t('commentSendFailed');
    } finally {
      if (submit) submit.disabled = false;
    }
  }

  function renderCommentSection(requirementID, initialItems) {
    const body = $('#detailBody');
    if (!body) return;
    activeCommentTargetID = requirementID;
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
    loadCommentThread(requirementID, initialItems);
  }

  const baseOpenRequirement = openRequirement;
  openRequirement = async function enhancedRequirementDetails(id) {
    await baseOpenRequirement(id);
    try {
      const detail = await api(`/api/v1/requirements/${encodeURIComponent(id)}`);
      const body = $('#detailBody');
      if (!body) return;
      const items = detail.attachments || [];
      if (items.length) {
        const block = document.createElement('div');
        block.className = 'detail-block';
        block.innerHTML = `<h3>${escapeHTML(t('attachments'))} · ${items.length}</h3><div class="attachment-list">${items.map(item => `<div class="attachment-item"><span>${escapeHTML(item.filename)}</span><span class="mono">${escapeHTML(formatBytes(item.size_bytes))}</span></div>`).join('')}</div>`;
        body.appendChild(block);
      }
      renderCommentSection(id, detail.comments || []);
    } catch (_) {
      const body = $('#detailBody');
      if (body) renderCommentSection(id, []);
    }
  };

  function formatBytes(value) {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / 1024 / 1024).toFixed(1)} MiB`;
  }

  let activeChatID = '';
  let activeChatData = null;

  function renderChatPage() {
    $('#pageTitle').textContent = t('chats');
    $('#pageSubtitle').textContent = t('chatSubtitle');
    $('#pageActions').innerHTML = `<button class="primary" id="chatNew"><span aria-hidden="true">＋</span>${escapeHTML(t('newChat'))}</button>`;
    const list = chats.map(item => `<button type="button" class="chat-list-item ${item.id === activeChatID ? 'active' : ''}" data-chat-id="${escapeHTML(item.id)}"><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.project_id || t('project'))}</small></button>`).join('');
    const messages = activeChatData?.messages || [];
    const messageHTML = messages.length ? messages.map(item => `<article class="chat-message ${item.role === 'user' ? 'user' : 'assistant'}"><header><span>${escapeHTML(item.role)}</span><time>${escapeHTML(new Date(item.created_at).toLocaleTimeString(locale === 'zh' ? 'zh-CN' : 'en-US', {hour: '2-digit', minute: '2-digit'}))}</time></header><p>${escapeHTML(item.content)}</p>${item.attachment_ids?.length ? `<small>${escapeHTML(item.attachment_ids.length)} ${escapeHTML(t('attachments'))}</small>` : ''}</article>`).join('') : `<p class="chat-empty">${escapeHTML(t('noMessages'))}</p>`;
    const projectOptions = repositories.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.canonical_name || item.id)}</option>`).join('');
    $('#appView').innerHTML = `<div class="chat-workspace"><aside class="chat-sidebar"><div class="chat-sidebar-head"><strong>${escapeHTML(t('chats'))}</strong><span>${escapeHTML(chats.length)} ${escapeHTML(t('items'))}</span></div><div class="chat-list">${list || `<p class="chat-empty">${escapeHTML(t('noChats'))}</p>`}</div></aside><section class="chat-panel"><div class="chat-history" id="chatHistory">${messageHTML}</div><form id="chatComposer" class="chat-composer"><div class="chat-compose-meta"><select id="chatProject" aria-label="${escapeHTML(t('chatProject'))}"><option value="">${escapeHTML(t('chatProject'))}</option>${projectOptions}</select><label class="chat-file-label" title="${escapeHTML(t('chatAttachments'))}">＋ <input id="chatFiles" type="file" multiple hidden></label></div><textarea id="chatInput" required placeholder="${escapeHTML(t('chatMessagePlaceholder'))}"></textarea><div class="chat-compose-actions"><span id="chatComposerStatus" role="status"></span><button class="primary" type="submit">${escapeHTML(t('sendMessage'))}</button></div></form></section></div>`;
    if ($('#chatProject') && activeChatData?.chat?.project_id) $('#chatProject').value = activeChatData.chat.project_id;
    document.querySelectorAll('[data-chat-id]').forEach(button => { button.onclick = () => { activeChatID = button.dataset.chatId; loadChatDetail(activeChatID); }; });
    $('#chatNew').onclick = createChatFromUI;
    $('#chatComposer').onsubmit = sendChatFromUI;
  }

  async function loadChatDetail(id) {
    try { activeChatData = await api(`/api/v1/chats/${encodeURIComponent(id)}`); renderChatPage(); } catch (_) { activeChatData = null; renderChatPage(); }
  }

  async function loadChatList() {
    try {
      const response = await api('/api/v1/chats');
      chats = response.items || [];
      if (!activeChatID && chats[0]) activeChatID = chats[0].id;
      if (activeChatID) await loadChatDetail(activeChatID); else { activeChatData = null; renderChatPage(); }
    } catch (_) { renderChatPage(); }
  }

  async function createChatFromUI() {
    const title = window.prompt(t('chatTitle'), t('newChat'));
    if (!title) return;
    const projectID = $('#chatProject')?.value || '';
    try {
      const created = await api('/api/v1/chats', {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify({workspace_id: 'local', project_id: projectID, title: title.trim()})});
      chats = [created, ...chats.filter(item => item.id !== created.id)]; activeChatID = created.id; await loadChatDetail(activeChatID);
    } catch (_) { window.alert(t('chatCreateFailed')); }
  }

  async function sendChatFromUI(event) {
    event.preventDefault();
    if (!activeChatID) { await createChatFromUI(); if (!activeChatID) return; }
    const form = event.currentTarget; const input = $('#chatInput'); const files = Array.from($('#chatFiles')?.files || []); const status = $('#chatComposerStatus');
    const attachmentIDs = [];
    try {
      status.textContent = '';
      for (const file of files) { const payload = new FormData(); payload.append('owner_type', 'chat_session'); payload.append('owner_id', activeChatID); payload.append('file', file); const attachment = await api('/api/v1/attachments', {method: 'POST', body: payload}); attachmentIDs.push(attachment.id); }
      await api(`/api/v1/chats/${encodeURIComponent(activeChatID)}/messages`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey()}, body: JSON.stringify({content: input.value, attachment_ids: attachmentIDs})});
      form.reset(); await loadChatDetail(activeChatID);
    } catch (_) { status.textContent = t('chatSendFailed'); }
  }

  const baseRender = render;
  render = function enhancedRender() { baseRender(); if (currentView === 'chats') renderChatPage(); };
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
