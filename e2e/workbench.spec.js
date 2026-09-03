const { test, expect } = require('@playwright/test');

test.setTimeout(90_000);

const apiQuery = '?api=http://127.0.0.1:18080';
const menuViews = [
  'workbench', 'requirements', 'bugs', 'humanQA', 'designReview',
  'executions', 'diffs', 'testing', 'repositories', 'agents', 'mcp',
  'skills', 'automations', 'integrations', 'artifacts', 'runners', 'cost', 'admin'
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  const requestHosts = new Set();
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(message.text());
  });
  page.on('request', request => requestHosts.add(new URL(request.url()).hostname));
  await page.goto(`/${apiQuery}`);
  await expect(page.locator('#loginGate')).toBeVisible();
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  page.__adroErrors = errors;
  page.__adroRequestHosts = requestHosts;
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
  await page.locator('#agentForm input[name="member"]').fill('comment-evidence-owner');
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
  await page.locator('#agentForm input[name="member"]').fill('browser-agent-owner');
  await page.locator('#agentForm input[name="name"]').fill('Browser Delivery Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Run the acceptance workflow and return evidence.');
  await page.locator('#agentForm input[name="role"]').fill('developer');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  await expect(page.locator('#appView')).toContainText('browser-agent-owner');
  await expect(page.locator('#appView')).toContainText('pb-');
  expect(page.__adroErrors).toEqual([]);
});

test('creates and operates native Agent, Squad, and immutable Plan records', async ({ page }) => {
  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('native-orchestration-service');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/native-orchestration.git');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('#resourceDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('Native orchestration acceptance');
  await page.locator('#requirementForm textarea[name="description"]').fill('Create an immutable plan from a published revisioned squad.');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('Agent and Squad revisions are frozen\nTimeline and replay are available');
  await page.locator('#requirementRepository').selectOption({ label: 'native-orchestration-service' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();
  await expect(page.locator('#requirementDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.locator('#agentForm input[name="member"]').fill('native-orchestration-owner');
  await page.locator('#agentForm input[name="name"]').fill('Browser Native Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Execute the frozen graph with evidence.');
  await page.locator('#agentForm input[name="role"]').fill('delivery-lead');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();

  const agentRow = page.locator('tr').filter({ hasText: 'Browser Native Agent' }).first();
  await expect(agentRow).toContainText('active');
  const agentID = await agentRow.locator('.orchestration-id').textContent();
  await agentRow.locator('[data-orchestration-action="validate"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('validate');
  await agentRow.locator('[data-orchestration-action="capabilities"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('capabilities');

  await page.locator('#newSquad').click();
  await page.locator('#squadForm input[name="name"]').fill('Browser Native Squad');
  await page.locator('#squadForm textarea[name="description"]').fill('Revision-locked browser acceptance squad');
  await page.locator('#squadLeader').selectOption(agentID.trim());
  await page.locator('#squadForm button[type="submit"]').click();
  await expect(page.locator('#squadDialog')).not.toBeVisible();

  const squadRow = page.locator('tr').filter({ hasText: 'Browser Native Squad' }).first();
  await expect(squadRow).toContainText('draft');
  const squadID = (await squadRow.locator('.orchestration-id').textContent()).trim();
  await squadRow.locator('[data-orchestration-action="validate"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('validate');
  await squadRow.locator('[data-orchestration-action="dry-run"]').click();
  await expect(page.locator('#orchestrationStatus')).toContainText('dry-run');
  await squadRow.locator('[data-orchestration-action="publish"]').click();

  const publishedSquad = page.locator('tr').filter({ hasText: 'Browser Native Squad' }).first();
  await expect(publishedSquad).toContainText('published');
  await expect(publishedSquad).toContainText('v1');

  await page.locator('#newPlan').click();
  const requirementOption = page.locator('#nativePlanRequirement option').filter({ hasText: 'Native orchestration acceptance' }).first();
  await page.locator('#nativePlanRequirement').selectOption(await requirementOption.getAttribute('value'));
  const squadOption = page.locator('#nativePlanTarget option').filter({ hasText: 'Squad · Browser Native Squad' }).first();
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
