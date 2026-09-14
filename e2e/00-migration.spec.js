const { test, expect } = require('@playwright/test');

test.setTimeout(90_000);

test.beforeEach(async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#loginGate')).toBeVisible();
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
});

test('admin migration rejects invalid data then preflights and imports a verified export', async ({ page }) => {
  const fixtureAgent = await page.evaluate(async () => {
    const response = await fetch('/api/v1/workspaces/local/agents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Workspace-ID': 'local' },
      credentials: 'include',
      body: JSON.stringify({
        id: `migration-fixture-${crypto.randomUUID()}`,
        name: 'Migration fixture agent',
        status: 'active',
        executor_binding: { provider_id: 'local', runtime_id: 'codex' },
        input_schema: { id: 'migration-input', version: 1 },
        output_schema: { id: 'migration-output', version: 1 }
      })
    });
    return { ok: response.ok, status: response.status, body: await response.text() };
  });
  expect(fixtureAgent.ok, `migration fixture agent returned HTTP ${fixtureAgent.status}: ${fixtureAgent.body}`).toBeTruthy();

  // Use the authenticated browser context. `page.request` is an isolated API
  // client and does not carry the session cookie established by the login UI.
  const exported = await page.evaluate(async () => {
    const response = await fetch('/api/v1/workspaces/local/migration/export', {
      headers: { 'X-Workspace-ID': 'local' },
      credentials: 'include'
    });
    const bytes = new Uint8Array(await response.arrayBuffer());
    return { ok: response.ok, status: response.status, body: Array.from(bytes) };
  });
  expect(exported.ok, `migration export returned HTTP ${exported.status}`).toBeTruthy();
  const bundle = Buffer.from(exported.body);
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
  await panel.locator('[data-migration-conflict]').selectOption('rename');
  await page.route('**/api/v1/workspaces/local/migration/preflight?**', async route => {
    const response = await route.fetch();
    await route.fulfill({ response });
  });
  const preflightResponse = page.waitForResponse(response => response.url().includes('/migration/preflight?conflict=rename'));
  await panel.locator('[data-migration-preflight]').click();
  const preflight = await preflightResponse;
  expect(preflight.status()).toBe(200);
  await expect(panel.locator('[data-migration-report]')).toHaveClass(/good/);
  await expect(importButton).toBeEnabled();

  const importResponse = page.waitForResponse(response => response.url().includes('/migration/import?') && response.status() === 200);
  await importButton.click();
  await importResponse;
  await expect(page.locator('#appView [data-workspace-migration]')).toBeVisible();
});
