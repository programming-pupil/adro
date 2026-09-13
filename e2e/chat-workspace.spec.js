const { test, expect } = require('@playwright/test');

test('renders the project-aware chat workspace with attachment drafting', async ({ page }) => {
  test.setTimeout(60_000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });

  await page.goto('/');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();

  const projectName = `chat-context-project-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const project = await page.evaluate(async canonicalName => {
    const response = await fetch('/api/v1/repositories', {
      method: 'POST',
      credentials: 'include',
      headers: {'Content-Type': 'application/json', 'X-Workspace-ID': 'local'},
      body: JSON.stringify({workspace_id: 'local', canonical_name: canonicalName, clone_url: 'https://example.invalid/chat-context.git', provider: 'git', default_branch: 'main'})
    });
    return response.json();
  }, projectName);
  await page.locator('#refreshButton').click();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');

  await page.locator('.nav-chat[data-view="chats"]').click();
  await page.locator('#chatNew').click();
  await page.locator('#chatCreateTitle').fill('发布风险讨论');
  await expect(page.locator(`#chatCreateProject option[value="${project.id}"]`)).toHaveCount(1);
  await expect(page.locator('#chatCreateProject')).toBeEnabled();
  await expect(page.locator('#chatCreateAgent')).toBeEnabled();
  await page.locator('#chatCreateProject').selectOption(project.id);
  if (await page.locator('#chatCreateAgent option').count() > 1) await page.locator('#chatCreateAgent').selectOption({index: 1});
  await page.locator('#chatCreateForm button[type="submit"]').click();
  await expect(page.locator('.chat-workspace')).toBeVisible();
  await expect(page.locator('#chatProject')).toHaveValue(project.id);
  await expect(page.locator('.chat-context-panel')).toContainText(projectName);

  await page.locator('#chatFiles').setInputFiles({name: 'release-notes.txt', mimeType: 'text/plain', buffer: Buffer.from('release context')});
  await expect(page.locator('#chatDraftFiles')).toContainText('release-notes.txt');
  await page.evaluate(() => {
    const transfer = new DataTransfer();
    const pixels = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='), char => char.charCodeAt(0));
    transfer.items.add(new File([pixels], 'pasted-context.png', {type: 'image/png'}));
    document.querySelector('#chatInput').dispatchEvent(new ClipboardEvent('paste', {bubbles: true, clipboardData: transfer}));
  });
  await expect(page.locator('#chatDraftFiles')).toContainText('pasted-context.png');
  await expect(page.locator('#chatDraftFiles img')).toHaveCount(1);
  await page.screenshot({path: 'var/adro-chat-attachments-cyber.png', fullPage: true});
  await page.locator('[data-chat-remove-file]').last().click();
  await expect(page.locator('#chatDraftFiles')).not.toContainText('pasted-context.png');
  await page.locator('#chatInput').fill('结合项目上下文，列出发布前风险。');
  await page.screenshot({path: 'var/adro-chat-workspace-cyber.png', fullPage: true});
  await page.setViewportSize({width: 390, height: 844});
  await page.screenshot({path: 'var/adro-chat-workspace-mobile-cyber.png', fullPage: true});
  expect(errors).toEqual([]);
});
