(() => {
  const menuIDs = [
    'workbench', 'requirements', 'bugs', 'humanQA', 'designReview', 'executions',
    'diffs', 'testing', 'repositories', 'agents', 'mcp', 'skills', 'automations',
    'integrations', 'artifacts', 'runners', 'cost', 'admin'
  ];

  Object.assign(translations.zh, {
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
  });
  Object.assign(translations.en, {
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
  });

  let currentUser = null;
  let directory = [];
  let managedUsers = [];
  let availableMenus = menuIDs.slice();

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
    document.querySelectorAll('.nav-item').forEach(item => {
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
    document.querySelectorAll('.nav-item').forEach(item => item.classList.toggle('active', item.dataset.view === currentView));
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
  };

  const baseOpenRequirement = openRequirement;
  openRequirement = async function enhancedRequirementDetails(id) {
    await baseOpenRequirement(id);
    try {
      const response = await api(`/api/v1/attachments?owner_type=requirement&owner_id=${encodeURIComponent(id)}`);
      const items = response.items || [];
      const body = $('#detailBody');
      if (!body || !items.length) return;
      const block = document.createElement('div');
      block.className = 'detail-block';
      block.innerHTML = `<h3>${escapeHTML(t('attachments'))} · ${items.length}</h3><div class="attachment-list">${items.map(item => `<div class="attachment-item"><span>${escapeHTML(item.filename)}</span><span class="mono">${escapeHTML(formatBytes(item.size_bytes))}</span></div>`).join('')}</div>`;
      body.appendChild(block);
    } catch (_) {}
  };

  function formatBytes(value) {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / 1024 / 1024).toFixed(1)} MiB`;
  }

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
