const { test, expect } = require('@playwright/test');

test.setTimeout(90_000);

const menuViews = [
  'workbench', 'requirements', 'bugs', 'humanQA', 'designReview',
  'executions', 'diffs', 'testing', 'repositories', 'agents', 'mcp',
  'skills', 'automations', 'integrations', 'artifacts', 'runners', 'cost', 'admin'
];
test.beforeEach(async ({ page }, testInfo) => {
  const errors = [];
  const requestHosts = new Set();
  if (testInfo.title.includes('first-run workspace import')) {
    await page.route('**/api/v1/workspaces/local/agents', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [] })
    }));
  }
  if (testInfo.title.includes('AI-assisted Agent creation')) {
    await page.route('**/api/v1/directory', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [
        { id: 'agent-owner', username: 'owner', display_name: 'Agent owner', status: 'active' },
        { id: 'member-a', username: 'alice', display_name: 'Alice', status: 'active' },
        { id: 'member-b', username: 'bob', display_name: 'Bob', status: 'active' }
      ] })
    }));
    await page.route('**/api/v1/runtimes/discovered', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [{ id: 'codex', name: 'OpenAI Codex', installed: true, adapter_available: true }] })
    }));
    await page.route('**/api/v1/runtimes/codex/models', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ runtime_id: 'codex', models: [{ id: 'gpt-5', label: 'GPT-5', thinking: { supported_levels: [{ value: 'high', label: 'High' }] }, service_tiers: [{ id: 'fast', name: 'Fast' }] }] })
    }));
    await page.route('**/api/v1/runtimes/codex/skills', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ runtime_id: 'codex', items: [
        { key: 'release-review', name: 'Release review', description: 'Review release evidence', source_path: '~/.codex/skills/release-review', provider: 'codex', root: 'provider', can_disable: true },
        { key: 'shared-plan', name: 'Shared plan', source_path: '~/.agents/skills/shared-plan', provider: 'codex', root: 'universal', can_disable: true }
      ] })
    }));
    await page.route('**/api/v1/skills', route => {
      if (route.request().method() !== 'GET') return route.continue();
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ items: [
          { id: 'skill-release', name: 'Release checks', version: '1', status: 'active' },
          { id: 'skill-disabled', name: 'Disabled checks', version: '1', status: 'disabled' }
        ] })
      });
    });
    await page.route('**/api/v1/mcp/servers', route => {
      if (route.request().method() !== 'GET') return route.continue();
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ items: [
          { id: 'mcp-release', name: 'Release tools', protocol: 'http', status: 'configured' },
          { id: 'mcp-disabled', name: 'Disabled tools', protocol: 'http', status: 'disabled' }
        ] })
      });
    });
  }
  if (testInfo.title.includes('OpenClaw gateway')) {
    await page.route('**/api/v1/runtimes/discovered', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [{ id: 'openclaw', name: 'OpenClaw', installed: true, adapter_available: true }] })
    }));
    await page.route('**/api/v1/runtimes/openclaw/models', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ runtime_id: 'openclaw', models: [] })
    }));
    await page.route('**/api/v1/runtimes/openclaw/skills', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ runtime_id: 'openclaw', items: [{ key: 'planning', name: 'Planning', source_path: '~/.openclaw/skills/planning', provider: 'openclaw', root: 'provider', can_disable: false }] })
    }));
  }
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(message.text());
  });
  page.on('response', response => {
    if (response.status() >= 400) errors.push(`${response.status()} ${response.url()}`);
  });
  page.on('request', request => requestHosts.add(new URL(request.url()).hostname));
  await page.goto('/');
  await expect(page.locator('#loginGate')).toBeVisible();
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  const agentsResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'GET' && url.pathname === '/api/v1/workspaces/local/agents';
  });
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  const agents = await (await agentsResponse).json();
  const onboardingDialog = page.locator('#agentDialog');
  if ((agents.items || []).length === 0) {
    await expect(onboardingDialog).toBeVisible();
    await expect(page.locator('#agentForm')).toHaveAttribute('data-onboarding', 'true');
    await expect(page.locator('#agentForm input[name="name"]')).toHaveValue('通用 Agent');
    await expect(page.locator('#closeAgentDialog')).toBeHidden();
    await expect(page.locator('#cancelAgentDialog')).toBeHidden();
    if (!testInfo.title.includes('first-run workspace import')) {
      await page.locator('#agentForm button[type="submit"]').click();
      await expect(onboardingDialog).not.toBeVisible();
    }
  }
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  page.__adroErrors = errors;
  page.__adroRequestHosts = requestHosts;
});

test('first-run workspace import requires preflight and bypasses manual Agent creation', async ({ page }) => {
  let preflightCalls = 0;
  let importCalls = 0;
  await page.route('**/api/v1/workspaces/local/migration/preflight?**', route => {
    preflightCalls += 1;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ valid: true, digest: '0123456789abcdef', counts: { agents: 1, requirements: 2 } })
    });
  });
  await page.route('**/api/v1/workspaces/local/migration/import?**', route => {
    importCalls += 1;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ preflight: { valid: true }, artifacts_put: 0 })
    });
  });

  const panel = page.locator('#agentForm [data-workspace-migration]');
  await expect(panel).toBeVisible();
  const importButton = panel.locator('[data-migration-import]');
  await panel.locator('[data-migration-file]').setInputFiles({
    name: 'workspace.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('fixture bundle')
  });
  await expect(importButton).toBeDisabled();
  await panel.locator('[data-migration-preflight]').click();
  await expect(panel.locator('[data-migration-report]')).toContainText('0123456789ab');
  await expect(importButton).toBeEnabled();

  await panel.locator('[data-migration-conflict]').selectOption('skip');
  await expect(importButton).toBeDisabled();
  await panel.locator('[data-migration-preflight]').click();
  await expect(importButton).toBeEnabled();
  await importButton.click();

  await expect(page.locator('#agentDialog')).not.toBeVisible();
  expect(preflightCalls).toBe(2);
  expect(importCalls).toBe(1);
  expect(page.__adroErrors).toEqual([]);
});

test('opens every workbench menu and keeps the browser error-free', async ({ page }) => {
  await expect(page.locator('.nav-item')).toHaveCount(18);
  for (const view of menuViews) {
    await page.locator(`.nav-item[data-view="${view}"]`).click();
    await expect(page.locator('#pageTitle')).not.toHaveText('');
    await expect(page.locator('#appView')).toBeVisible();
  }
  await expect(page.locator('iframe')).toHaveCount(0);
  await expect(page.locator('a[href^="http"]')).toHaveCount(0);
  expect([...page.__adroRequestHosts]).toEqual(['127.0.0.1']);
  expect(page.__adroErrors).toEqual([]);
});

test('AI-assisted Agent creation fills human controls and persists execution settings', async ({ page }) => {
	await page.route('**/api/v1/workspaces/local/agents', route => {
		if (route.request().method() !== 'POST') return route.continue();
		return route.fulfill({status: 201, contentType: 'application/json', body: route.request().postData() || '{}'});
	});
  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.route('**/api/v1/workspaces/local/agents/compose', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      draft: {
        name: 'Release reviewer',
        description: 'Reviews release evidence before approval.',
        role: 'reviewer',
        instructions: 'Review evidence and return a clear decision.',
        conversation_starters: [{ label: 'Review release', prompt: 'Review this release candidate.' }],
        access_policy: { mode: 'workspace' },
		skill_ids: ['skill-release'],
		mcp_server_ids: ['mcp-release'],
        network_access: false,
        max_concurrent_tasks: 2,
        token_budget: 60000,
        tool_call_budget: 80
      },
      evidence: { run_id: 'builder-run', runtime_id: 'local', output_sha256: 'abc' }
    })
  }));

  await page.locator('#agentBuilderPrompt').fill('Create a release reviewer');
  await page.locator('#composeAgentDraft').click();
  await expect(page.locator('#agentForm input[name="name"]')).toHaveValue('Release reviewer');
  await expect(page.locator('#agentForm textarea[name="description"]')).toHaveValue('Reviews release evidence before approval.');
  await expect(page.locator('#agentForm select[name="access_mode"]')).toHaveValue('workspace');
  await expect(page.locator('#agentForm input[name="starter_label_1"]')).toHaveValue('Review release');
	await expect(page.locator('#agentForm input[name="agent_skill_ids"][value="skill-release"]')).toBeChecked();
	await expect(page.locator('#agentForm input[name="agent_mcp_server_ids"][value="mcp-release"]')).toBeChecked();
	await expect(page.locator('#agentForm input[name="agent_skill_ids"][value="skill-disabled"]')).toHaveCount(0);
	await expect(page.locator('#agentForm input[name="agent_mcp_server_ids"][value="mcp-disabled"]')).toHaveCount(0);
  await expect(page.locator('#agentBuilderStatus')).toContainText('草稿已生成');

  await page.locator('#agentForm select[name="member"]').selectOption('agent-owner');
  await page.locator('#agentForm select[name="access_mode"]').selectOption('members');
  await expect(page.locator('#agentAccessMembersField')).toBeVisible();
  await page.locator('#agentForm input[name="agent_access_member_ids"][value="member-a"]').check();
  await page.locator('#agentForm input[name="agent_access_member_ids"][value="member-b"]').check();
  await page.locator('#agentForm details.agent-advanced').evaluate(element => { element.open = true; });
  await page.locator('#agentForm input[name="network"]').check();
  await expect(page.locator('#agentForm textarea[name="custom_args"]')).toBeHidden();
  await expect(page.locator('#agentForm textarea[name="runtime_config"]')).toBeHidden();
  await expect(page.locator('#agentForm textarea[name="environment"]')).toBeHidden();
  await expect(page.locator('[data-runtime-policy="codex"]')).toBeVisible();
  await page.locator('#agentForm select[name="codex_sandbox"]').selectOption('workspace-write');
  await page.locator('#agentForm select[name="codex_approval"]').selectOption('on-request');
  await page.locator('#agentForm input[data-runtime-skill-index="0"]').uncheck();

  const createRequest = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/workspaces/local/agents');
  await page.locator('#agentForm button[type="submit"]').click();
  const body = JSON.parse((await createRequest).postData());
  expect(body.description).toBe('Reviews release evidence before approval.');
  expect(body.access_policy).toEqual({ mode: 'members', member_ids: ['member-a', 'member-b'] });
  expect(body.conversation_starters).toHaveLength(1);
	expect(body.skill_ids).toEqual(['skill-release']);
	expect(body.disabled_runtime_skills).toEqual([{ runtime_id: 'codex', provider: 'codex', root: 'provider', key: 'release-review', name: 'Release review', plugin: '' }]);
	expect(body.mcp_server_ids).toEqual(['mcp-release']);
  expect(body.concurrency_budget).toEqual({ tokens: 60000, tool_calls: 80, concurrent: 2 });
  expect(body.tool_policy.network).toBe(true);
  expect(body.executor_binding.custom_args).toEqual([]);
  expect(body.executor_binding.runtime_config).toEqual({ sandbox_mode: 'workspace-write', approval_policy: 'on-request' });
  expect(body.executor_binding.environment).toEqual([]);
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  expect(page.__adroErrors).toEqual([]);
});

test('AI-assisted Agent creation supports one-click generation and creation', async ({ page }) => {
  await page.route('**/api/v1/workspaces/local/agents', route => {
    if (route.request().method() !== 'POST') return route.continue();
    return route.fulfill({ status: 201, contentType: 'application/json', body: route.request().postData() || '{}' });
  });
  await page.route('**/api/v1/workspaces/local/agents/compose', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      draft: {
        name: 'Incident coordinator',
        description: 'Coordinates incident response.',
        role: 'coordinator',
        instructions: 'Coordinate responders and preserve evidence.',
        conversation_starters: [],
        access_policy: { mode: 'workspace' },
        skill_ids: [],
        mcp_server_ids: [],
        network_access: false,
        max_concurrent_tasks: 1,
        token_budget: 120000,
        tool_call_budget: 200
      },
      evidence: { run_id: 'real-runtime-shaped-builder-run', runtime_id: 'codex', output_sha256: 'abc' }
    })
  }));

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.locator('#agentBuilderPrompt').fill('Create an incident response coordinator');
  const createRequest = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/workspaces/local/agents');
  await page.locator('#composeAndCreateAgent').click();
  const body = JSON.parse((await createRequest).postData());
  expect(body.name).toBe('Incident coordinator');
  expect(body.owner_id).toBe('agent-owner');
  expect(body.executor_binding.runtime_id).toBe('codex');
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  expect(page.__adroErrors).toEqual([]);
});

test('OpenClaw gateway policy stores only a secret reference', async ({ page }) => {
  await page.route('**/api/v1/workspaces/local/agents', route => {
    if (route.request().method() !== 'POST') return route.continue();
    return route.fulfill({ status: 201, contentType: 'application/json', body: route.request().postData() || '{}' });
  });
  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await expect(page.locator('[data-runtime-policy="openclaw"]')).toBeVisible();
  await expect(page.locator('#agentRuntimeSkillOptions input')).toBeDisabled();
  await page.locator('#agentForm input[name="name"]').fill('Gateway agent');
  await page.locator('#agentForm select[name="openclaw_mode"]').selectOption('gateway');
  await page.locator('#agentForm input[name="openclaw_host"]').fill('gateway.internal');
  await page.locator('#agentForm input[name="openclaw_port"]').fill('18789');
  await page.locator('#agentForm input[name="openclaw_auth_env"]').fill('OPENCLAW_TOKEN');
  await page.locator('#agentForm input[name="openclaw_secret_env"]').fill('ADRO_OPENCLAW_TOKEN');
  await page.locator('#agentForm input[name="openclaw_tls"]').check();

  const createRequest = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/workspaces/local/agents');
  await page.locator('#agentForm button[type="submit"]').click();
  const request = await createRequest;
  const body = JSON.parse(request.postData());
  expect(body.executor_binding.runtime_config).toEqual({
    mode: 'gateway',
    'gateway.host': 'gateway.internal',
    'gateway.port': '18789',
    'gateway.tls': 'true',
    'gateway.auth_env': 'OPENCLAW_TOKEN'
  });
  expect(body.executor_binding.environment).toEqual([{ name: 'OPENCLAW_TOKEN', secret_ref: 'env:ADRO_OPENCLAW_TOKEN' }]);
  expect(request.postData()).not.toContain('do-not-persist');
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  expect(page.__adroErrors).toEqual([]);
});

test('admin migration rejects invalid data then preflights and imports a verified export', async ({ page }) => {
  const exported = await page.request.get('http://127.0.0.1:18080/api/v1/workspaces/local/migration/export', {
    headers: { 'X-Workspace-ID': 'local' }
  });
  expect(exported.ok()).toBeTruthy();
  const bundle = await exported.body();
  expect(bundle.length).toBeGreaterThan(0);

  await page.locator('.nav-item[data-view="admin"]').click();
  const panel = page.locator('#appView [data-workspace-migration]');
  const file = panel.locator('[data-migration-file]');
  const importButton = panel.locator('[data-migration-import]');

  await file.setInputFiles({ name: 'invalid.zip', mimeType: 'application/zip', buffer: Buffer.from('not a zip') });
  await panel.locator('[data-migration-preflight]').click();
  await expect(panel.locator('[data-migration-report]')).toHaveClass(/bad/);
  await expect(importButton).toBeDisabled();

  await file.setInputFiles({ name: 'workspace.zip', mimeType: 'application/zip', buffer: bundle });
  await expect(importButton).toBeDisabled();
  const preflightResponse = page.waitForResponse(response => response.url().includes('/migration/preflight?') && response.status() === 200);
  await panel.locator('[data-migration-preflight]').click();
  await preflightResponse;
  await expect(panel.locator('[data-migration-report]')).toHaveClass(/good/);
  await expect(importButton).toBeEnabled();

  const importResponse = page.waitForResponse(response => response.url().includes('/migration/import?') && response.status() === 200);
  await importButton.click();
  await importResponse;
  await expect(page.locator('#appView [data-workspace-migration]')).toBeVisible();
});

test('opens the durable project chat and sends a harness-backed message', async ({ page }) => {
  await page.locator('.nav-chat[data-view="chats"]').click();
  await expect(page.locator('#pageTitle')).toHaveText('普通聊天');
  page.once('dialog', dialog => dialog.accept('Browser chat'));
  await page.locator('#chatNew').click();
  await expect(page.locator('.chat-list-item')).toContainText('Browser chat');
  await page.locator('#chatInput').fill('Keep this project context durable');
  await page.locator('#chatComposer button[type="submit"]').click();
  await expect(page.locator('#chatHistory')).toContainText('Keep this project context durable');
});

test('creates a requirement, opens details, switches locale, and reconnects by WebSocket', async ({ page }) => {
  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('browser-requirement-service');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/browser-requirement.git');
  await page.locator('#resourceForm button[type="submit"]').click();
  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('Browser acceptance requirement');
  await page.locator('#requirementForm textarea[name="description"]').fill('Created by the repeatable acceptance suite');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('The detail view renders the requirement\nThe uploaded brief is retained');
  await page.locator('#requirementRepository').selectOption({ label: 'browser-requirement-service' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm input[name="attachments"]').setInputFiles({ name: 'requirement-brief.txt', mimeType: 'text/plain', buffer: Buffer.from('acceptance evidence') });
  await page.locator('#requirementForm button[type="submit"]').click();
  await expect(page.locator('#requirementDialog')).not.toBeVisible();
  const row = page.locator('tr[data-requirement-id]').filter({ hasText: 'Browser acceptance requirement' }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.locator('#detailDialog')).toBeVisible();
  await expect(page.locator('#detailBody')).toContainText('requirement-brief.txt');
  await expect(page.locator('#detailAction')).toHaveText('开始编排');
  await page.locator('#detailAction').click();
  await expect(page.locator('#detailAction')).toHaveText('确认负责人并进入方案设计');
  await expect(page.locator('#detailBody')).toContainText('工作项');
  await page.locator('#closeDetail').click();
  await page.locator('#localeToggle').click();
  await expect(page.locator('#pageTitle')).toHaveText('Requirements');
  await page.locator('#localeToggle').click();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  expect(page.__adroErrors).toEqual([]);
});

test('posts a structured mention comment with an attachment and reply', async ({ page }) => {
  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('comment-evidence-service');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/comment-evidence.git');
  await page.locator('#resourceForm button[type="submit"]').click();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('Comment thread acceptance');
  await page.locator('#requirementForm textarea[name="description"]').fill('Exercise the structured comment delivery path.');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('The comment is retained with its attachment and reply.');
  await page.locator('#requirementRepository').selectOption({ label: 'comment-evidence-service' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.locator('#agentForm select[name="member"]').selectOption({ index: 0 });
  await page.locator('#agentForm input[name="name"]').fill('Comment Evidence Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Process the comment acceptance evidence.');
  await page.locator('#agentForm input[name="role"]').fill('delivery');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  await expect(page.locator('tr').filter({ hasText: 'Comment Evidence Agent' }).first()).toContainText('active');

  await page.locator('.nav-item[data-view="requirements"]').click();
  const row = page.locator('tr[data-requirement-id]').filter({ hasText: 'Comment thread acceptance' }).first();
  await row.click();
  await expect(page.locator('#detailDialog')).toBeVisible();
  await expect(page.locator('#commentInput')).toBeVisible();

  await page.locator('#commentInput').fill('@Comment');
  await expect(page.locator('.comment-mention-option').filter({ hasText: 'Comment Evidence Agent' }).first()).toBeVisible();
  await page.locator('.comment-mention-option').filter({ hasText: 'Comment Evidence Agent' }).first().click();
  await expect(page.locator('#commentInput')).toHaveValue(/mention:\/\/agent\//);
  await page.locator('#commentPreviewButton').click();
  await expect(page.locator('#commentPreview')).toBeVisible();
  await expect(page.locator('#commentPreview')).toContainText('agent:');

  await page.locator('#commentFiles').setInputFiles({ name: 'comment-evidence.txt', mimeType: 'text/plain', buffer: Buffer.from('comment delivery evidence') });
  await page.locator('#commentComposer button[type="submit"]').click();
  await expect(page.locator('#commentThread')).toContainText('@Comment Evidence Agent');
  await expect(page.locator('.comment-attachment')).toContainText('comment-evidence.txt');
  await expect(page.locator('.comment-activity')).toContainText('触发结果');

  const firstComment = page.locator('.comment-item').first();
  await firstComment.locator('.comment-reply').click();
  await expect(page.locator('#commentComposerContext')).toBeVisible();
  await page.locator('#commentInput').fill('Thread reply with additional evidence');
  await page.locator('#commentComposer button[type="submit"]').click();
  await expect(page.locator('#commentThread')).toContainText('Thread reply with additional evidence');
  await expect(page.locator('#commentThread .comment-item')).toHaveCount(2);
  expect(page.__adroErrors).toEqual([]);
});

test('renders @all as a broadcast-only outcome without a follow-up receipt', async ({ page }) => {
  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('broadcast-evidence-service');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/broadcast-evidence.git');
  await page.locator('#resourceForm button[type="submit"]').click();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('Broadcast-only comment acceptance');
  await page.locator('#requirementForm textarea[name="description"]').fill('Exercise the render-only all mention path.');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('The comment is broadcast without starting an Agent follow-up.');
  await page.locator('#requirementRepository').selectOption({ label: 'broadcast-evidence-service' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();

  const row = page.locator('tr[data-requirement-id]').filter({ hasText: 'Broadcast-only comment acceptance' }).first();
  await row.click();
  await expect(page.locator('#commentInput')).toBeVisible();
  await page.locator('#commentInput').fill('公告 [@all](mention://all/all)');
  await page.locator('#commentPreviewButton').click();
  await expect(page.locator('#commentPreview')).toContainText('仅广播');
  await page.locator('#commentComposer button[type="submit"]').click();
  await expect(page.locator('#commentThread')).toContainText('仅广播');
  await expect(page.locator('.comment-receipt')).toHaveCount(0);
  expect(page.__adroErrors).toEqual([]);
});

test('captures the screenshot delivery path through ArtifactStore and provider', async ({ page }) => {
  await page.locator('.nav-item[data-view="artifacts"]').click();
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64');
  await page.locator('#screenshotFile').setInputFiles({ name: 'acceptance.png', mimeType: 'image/png', buffer: png });
  await expect(page.locator('#screenshotPreviewImage')).toBeVisible();
  await page.locator('#screenshotTargetType').selectOption('comment');
  await page.locator('#screenshotTargetID').fill('comment-e2e');
  await page.locator('#uploadScreenshot').click();
  await expect(page.locator('#screenshotStatus')).toHaveText('截图已保存并完成投递');
  expect(page.__adroErrors).toEqual([]);
});

test('creates an ADRO agent binding from the workspace UI', async ({ page }) => {
  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  const ownerSelect = page.locator('#agentForm select[name="member"]');
  await ownerSelect.selectOption({ index: 0 });
  const ownerID = await ownerSelect.inputValue();
  await page.locator('#agentForm input[name="name"]').fill('Browser Delivery Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Run the acceptance workflow and return evidence.');
  await page.locator('#agentForm input[name="role"]').fill('developer');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  const row = page.locator('tr').filter({ hasText: 'Browser Delivery Agent' }).first();
  await expect(row).toContainText(ownerID);
  await expect(row).toContainText('r1');

  await row.locator('[data-orchestration-action="edit"]').click();
  await expect(page.locator('#agentDialog')).toBeVisible();
  await expect(page.locator('#agentForm select[name="member"]')).toHaveValue(ownerID);
  await page.locator('#agentForm textarea[name="description"]').fill('Owns browser delivery acceptance.');
  await page.locator('#agentForm details.agent-advanced').evaluate(element => { element.open = true; });
  await expect(page.locator('#agentForm textarea[name="custom_args"]')).toBeHidden();
  await expect(page.locator('#agentForm textarea[name="runtime_config"]')).toBeHidden();
  await expect(page.locator('#agentForm textarea[name="environment"]')).toBeHidden();
  await page.locator('#agentForm input[name="concurrent"]').fill('2');
  await page.locator('#agentForm input[name="tokens"]').fill('240000');
  await page.locator('#agentForm input[name="tool_calls"]').fill('400');
  await page.locator('#agentForm input[name="network"]').check();
  const runtimeID = await page.locator('#agentForm select[name="runtime"]').inputValue();
  if (runtimeID === 'codex') {
    await page.locator('#agentForm select[name="codex_sandbox"]').selectOption('workspace-write');
    await page.locator('#agentForm select[name="codex_approval"]').selectOption('on-request');
  }
  const patchRequest = page.waitForRequest(request => request.method() === 'PATCH' && new URL(request.url()).pathname.includes('/api/v1/workspaces/local/agents/'));
  await page.locator('#agentForm button[type="submit"]').click();
  const patch = JSON.parse((await patchRequest).postData());
  expect(patch.created_by).toBeUndefined();
  expect(patch.executor_binding.custom_args).toEqual([]);
  expect(patch.executor_binding.environment).toEqual([]);
  expect(patch.concurrency_budget).toMatchObject({ concurrent: 2, tokens: 240000, tool_calls: 400 });
  expect(patch.tool_policy.network).toBe(true);
  if (runtimeID === 'codex') expect(patch.executor_binding.runtime_config).toEqual({ sandbox_mode: 'workspace-write', approval_policy: 'on-request' });
  else expect(patch.executor_binding.runtime_config).toEqual({});
  await expect(page.locator('tr').filter({ hasText: 'Browser Delivery Agent' }).first()).toContainText('r2');
  expect(page.__adroErrors).toEqual([]);
});

test('disables ineffective model settings for runtime-managed profiles', async ({ page }) => {
  let unsupportedCatalogRequests = 0;
  await page.route('**/api/v1/runtimes/discovered', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: [
      { id: 'qwenpaw', name: 'QwenPaw', installed: true, adapter_available: true, model_selection_unsupported: true },
      { id: 'local', name: 'Local test runtime', installed: true, adapter_available: true }
    ] })
  }));
  await page.route('**/api/v1/runtimes/qwenpaw/models', route => {
    unsupportedCatalogRequests += 1;
    return route.fulfill({ status: 500, body: 'must not be requested' });
  });
  await page.route('**/api/v1/runtimes/local/models', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ models: [{ id: 'model-a', label: 'Model A' }] })
  }));
  await page.route('**/api/v1/runtimes/{qwenpaw,local}/skills', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: [] })
  }));

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await expect(page.locator('#agentRuntime')).toHaveValue('qwenpaw');
  await expect(page.locator('#agentModel')).toBeDisabled();
  await expect(page.locator('#agentThinking')).toBeDisabled();
  await expect(page.locator('#agentServiceTier')).toBeDisabled();
  expect(unsupportedCatalogRequests).toBe(0);

  await page.locator('#agentRuntime').selectOption('local');
  await expect(page.locator('#agentModel')).toBeEnabled();
  await expect(page.locator('#agentThinking')).toBeEnabled();
  await expect(page.locator('#agentServiceTier')).toBeEnabled();
  await expect(page.locator('#agentModel option[value="model-a"]')).toHaveCount(1);
  expect(page.__adroErrors).toEqual([]);
});

test('creates and operates native Agent, Squad, and immutable Plan records', async ({ page }) => {
  const runSuffix = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
  const repositoryName = `native-orchestration-service-${runSuffix}`;
  const requirementTitle = `Native orchestration acceptance ${runSuffix}`;
  const agentName = `Browser Native Agent ${runSuffix}`;
  const squadName = `Browser Native Squad ${runSuffix}`;

  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill(repositoryName);
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/native-orchestration.git');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('#resourceDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill(requirementTitle);
  await page.locator('#requirementForm textarea[name="description"]').fill('Create an immutable plan from a published revisioned squad.');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('Agent and Squad revisions are frozen\nTimeline and replay are available');
  await page.locator('#requirementRepository').selectOption({ label: repositoryName });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();
  await expect(page.locator('#requirementDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.locator('#agentForm select[name="member"]').selectOption({ index: 0 });
  await page.locator('#agentForm input[name="name"]').fill(agentName);
  await page.locator('#agentForm textarea[name="instructions"]').fill('Execute the frozen graph with evidence.');
  await page.locator('#agentForm input[name="role"]').fill('delivery-lead');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();

  const agentRow = page.locator('tr').filter({ hasText: agentName }).first();
  await expect(agentRow).toContainText('active');
  const agentID = await agentRow.locator('.orchestration-id').textContent();
  await agentRow.locator('[data-orchestration-action="validate"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('validate');
  await agentRow.locator('[data-orchestration-action="capabilities"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('capabilities');

  await page.locator('#newSquad').click();
  await page.locator('#squadForm input[name="name"]').fill(squadName);
  await page.locator('#squadForm textarea[name="description"]').fill('Revision-locked browser acceptance squad');
  await page.locator('#squadLeader').selectOption(agentID.trim());
  await page.locator('#squadForm button[type="submit"]').click();
  await expect(page.locator('#squadDialog')).not.toBeVisible();

  const squadRow = page.locator('tr').filter({ hasText: squadName }).first();
  await expect(squadRow).toContainText('draft');
  const squadID = (await squadRow.locator('.orchestration-id').textContent()).trim();
  await squadRow.locator('[data-orchestration-action="edit-graph"]').click();
  await expect(page.locator('#graphEditorDialog')).toBeVisible();
  await page.locator('#graphAddGate').click();
  await page.locator('#graphConnect').click();
  await page.locator('#graphEditorCanvas [data-graph-node]').nth(0).click();
  await page.locator('#graphEditorCanvas [data-graph-node]').nth(1).click();
  await page.locator('#graphConnect').click();
  await page.locator('#graphEditorCanvas [data-graph-node]').nth(1).click();
  await page.locator('#graphEditorCanvas [data-graph-node]').nth(0).click();
  await page.locator('.graph-edge-row [data-edge-field="max_traversals"]').nth(1).fill('1');
  await page.locator('.graph-edge-row [data-edge-field="max_traversals"]').nth(1).press('Tab');
  await expect(page.locator('#graphEditorSummary')).toContainText('gate');
  await page.locator('#graphEditorSave').click();
  await expect(page.locator('#graphEditorDialog')).not.toBeVisible();
  await expect(page.locator('#orchestrationStatus')).toContainText('图已保存');
  const refreshedSquadRow = page.locator('tr').filter({ hasText: squadName }).first();
  await expect(refreshedSquadRow).toContainText('r2');
  await refreshedSquadRow.locator('[data-orchestration-action="edit-graph"]').click();
  await expect(page.locator('#graphEditorSummary')).toContainText('gate');
  await page.locator('#closeGraphEditor').click();
  await squadRow.locator('[data-orchestration-action="validate"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('validate');
  await squadRow.locator('[data-orchestration-action="dry-run"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('dry-run');
  await squadRow.locator('[data-orchestration-action="publish"]').click();

  const publishedSquad = page.locator('tr').filter({ hasText: squadName }).first();
  await expect(publishedSquad).toContainText('published');
  await expect(publishedSquad).toContainText('v1');

  await page.locator('#newPlan').click();
  const requirementOption = page.locator('#nativePlanRequirement option').filter({ hasText: requirementTitle }).first();
  await page.locator('#nativePlanRequirement').selectOption(await requirementOption.getAttribute('value'));
  const squadOption = page.locator('#nativePlanTarget option').filter({ hasText: `Squad · ${squadName}` }).first();
  await page.locator('#nativePlanTarget').selectOption(await squadOption.getAttribute('value'));
  await page.locator('#nativePlanForm button[type="submit"]').click();
  await expect(page.locator('#nativePlanDialog')).not.toBeVisible();

  const planRow = page.locator('tr', { has: page.locator('[data-orchestration-kind="plan"]') }).filter({ hasText: squadID }).first();
  await expect(planRow).toContainText('ready');
  await expect(planRow.locator('.digest-cell')).not.toHaveText('-');
  await planRow.locator('[data-orchestration-action="timeline"]').click();
  await expect(page.locator('#timelineDialog')).toBeVisible();
  await expect(page.locator('#timelineContent')).toContainText('plan.created');
  await page.locator('#timelineDialog [data-close-orchestration="timelineDialog"]').first().click();
  await planRow.locator('[data-orchestration-action="replay"]').click();
  await expect(page.locator('#timelineDialog')).toBeVisible();
  await expect(page.locator('#timelineContent')).toContainText('projection');
  await expect(page.locator('#timelineContent')).toContainText('plan_id');

  expect(page.__adroErrors).toEqual([]);
});

test('executes resource actions from every ADRO-owned control menu', async ({ page }) => {
  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('payments-service');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/payments.git');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('#resourceDialog')).not.toBeVisible();
  const repository = page.locator('tr').filter({ hasText: 'payments-service' }).first();
  await expect(repository).toBeVisible();
  await repository.locator('[data-resource-action="index"]').click();
  await expect(repository).toContainText('已就绪');

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('payments release requirement');
  await page.locator('#requirementForm textarea[name="description"]').fill('Ship the payments release with regression evidence');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('The payments release passes regression tests');
  await page.locator('#requirementRepository').selectOption({ label: 'payments-service' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();
  await expect(page.locator('tr').filter({ hasText: 'payments release requirement' }).first()).toBeVisible();

  await page.locator('.nav-item[data-view="mcp"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('release-tools');
  await page.locator('#resourceFields input[name="endpoint"]').fill('https://example.invalid/mcp');
  await page.locator('#resourceForm button[type="submit"]').click();
  const mcp = page.locator('tr').filter({ hasText: 'release-tools' }).first();
  await expect(mcp).toBeVisible();
  await mcp.locator('[data-resource-action="discover"]').click();
  await mcp.locator('[data-resource-action="healthCheck"]').click();
  await expect(mcp).toContainText('不可达');
  await mcp.locator('[data-resource-action="approve"]').click();

  await page.locator('.nav-item[data-view="skills"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('release-checks');
  await page.locator('#resourceFields input[name="version"]').fill('1.0.0');
  await page.locator('#resourceForm button[type="submit"]').click();
  const skill = page.locator('tr').filter({ hasText: 'release-checks' }).first();
  await expect(skill).toBeVisible();
  await skill.locator('[data-resource-action="publish"]').click();
  await expect(skill).toContainText('已发布');

  await page.locator('.nav-item[data-view="automations"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('nightly-release');
  await page.locator('#resourceForm button[type="submit"]').click();
  const automation = page.locator('tr').filter({ hasText: 'nightly-release' }).first();
  await expect(automation).toBeVisible();
  await automation.locator('[data-resource-action="publish"]').click();
  await expect(automation).toContainText('健康');
  await automation.locator('[data-resource-action="trigger"]').click();

  await page.locator('.nav-item[data-view="runners"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('runner-east');
  await page.locator('#resourceFields input[name="provider"]').fill('local');
  await page.locator('#resourceFields input[name="version"]').fill('0.1.0');
  await page.locator('#resourceFields input[name="concurrency"]').fill('2');
  await page.locator('#resourceFields input[name="workspace_root"]').fill('/tmp');
  await page.locator('#resourceForm button[type="submit"]').click();
  const runner = page.locator('tr').filter({ hasText: 'runner-east' }).first();
  await expect(runner).toBeVisible();
  await runner.locator('[data-resource-action="heartbeat"]').click();
  await expect(runner).toContainText('健康');
  await runner.locator('[data-resource-action="execute"]').click();
  await page.locator('#runnerExecuteForm input[name="command"]').fill('/bin/echo runner-ready');
  await page.locator('#runnerExecuteForm button[type="submit"]').click();
  await expect(page.locator('#runnerExecuteDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="bugs"]').click();
  await page.locator('#newResource').click();
  await page.locator('#bugForm input[name="title"]').fill('release regression');
  await page.locator('#bugForm textarea[name="steps"]').fill('Run the release acceptance suite');
  await page.locator('#bugForm textarea[name="expected"]').fill('All checks pass');
  await page.locator('#bugForm textarea[name="actual"]').fill('The release check fails');
  await page.locator('#bugRepository').selectOption({ label: 'payments-service' });
  await page.locator('#bugAssignee').selectOption({ index: 0 });
  const relatedRequirement = page.locator('#bugRequirement option').filter({ hasText: 'payments release requirement' });
  await expect(relatedRequirement).toHaveCount(1);
  await page.locator('#bugRequirement').selectOption(await relatedRequirement.getAttribute('value'));
  await page.locator('#bugForm input[name="attachments"]').setInputFiles({ name: 'failure.log', mimeType: 'text/plain', buffer: Buffer.from('failure evidence') });
  await page.locator('#bugForm button[type="submit"]').click();
  const bug = page.locator('tr').filter({ hasText: 'release regression' }).first();
  await expect(bug).toBeVisible();
  await expect(bug).toContainText('payments release requirement');
  await bug.locator('[data-resource-action="repair"]').click();
  await expect(bug).toContainText('修复中');
  await bug.locator('[data-resource-action="verify"]').click();
  await expect(bug).toContainText('已验证');

  expect(page.__adroErrors).toEqual([]);
});

test('administrator assigns menu access and the backend enforces it', async ({ page, request }) => {
  await page.locator('.nav-item[data-view="admin"]').click();
  await page.locator('#newUser').click();
  await page.locator('#userForm input[name="username"]').fill('restricted.user');
  await page.locator('#userForm input[name="display_name"]').fill('Restricted User');
  await page.locator('#userForm input[name="password"]').fill('Restricted123!');
  await page.locator('#userForm input[name="menus"][value="workbench"]').check();
  await page.locator('#userForm input[name="menus"][value="requirements"]').check();
  await page.locator('#userForm input[name="menus"][value="bugs"]').uncheck();
  await page.locator('#userForm button[type="submit"]').click();
  await expect(page.locator('#appView')).toContainText('restricted.user');
  await page.locator('#logoutButton').click();
  await page.locator('#loginForm input[name="username"]').fill('restricted.user');
  await page.locator('#loginForm input[name="password"]').fill('Restricted123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('.nav-item[data-view="requirements"]')).toBeVisible();
  await expect(page.locator('.nav-item[data-view="bugs"]')).toBeHidden();
  const cookies = await page.context().cookies('http://127.0.0.1:18080');
  const denied = await request.get('http://127.0.0.1:18080/api/v1/bugs', { headers: { Cookie: cookies.map(cookie => `${cookie.name}=${cookie.value}`).join('; ') } });
  expect(denied.status()).toBe(403);
  expect(page.__adroErrors).toEqual([]);
});
