const { test, expect } = require('@playwright/test');

const timer = {
  id: 'timer-approval-1', schedule_key: 'approval:1',
  scope: { tenant_id: 'local', workspace_id: 'local', session_id: 'session-1', run_id: 'run-1' },
  due_at: '2026-09-19T12:00:00Z', interval: 0, command: { name: 'approval.deadline', idempotency_key: 'approval:1' },
  command_digest: 'sha256:command', policy: 'catch_up', max_catch_up: 16, state: 'pending', fencing_token: 0,
  created_at: '2026-09-19T11:00:00Z', updated_at: '2026-09-19T11:00:00Z'
};

test('inspects, explains, and cancels a durable timer through the Runtime Inspector', async ({ page }) => {
  const timerRequests = [];
  const browserErrors = [];
  page.on('pageerror', error => browserErrors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') browserErrors.push(message.text()); });
  await page.route('**/api/v1/timers?**', route => {
    timerRequests.push(route.request());
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [timer], include_terminal: true }) });
  });
  await page.route('**/api/v1/timers/timer-approval-1/explain', route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ timer, reason: 'waiting_for_due_time', next_action: 'claim_when_due', occurrence_key: 'approval:1:generation:0' })
  }));
  await page.route('**/api/v1/timers/timer-approval-1/cancel', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ ...timer, state: 'cancelled', terminal_reason: 'operator_review' })
  }));

  await page.goto('/');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await page.locator('.nav-item[data-view="timers"]').click();
  await expect(page.locator('.timer-inspector')).toBeVisible();
  await expect.poll(() => timerRequests.length).toBeGreaterThan(0);
  expect(timerRequests[0].headers()['x-workspace-id']).toBe('local');
  await expect(page.locator('.timer-table-panel tbody')).toContainText('timer-approval-1');
  await expect(page.locator('.timer-stat-grid')).toContainText(/Pending|等待触发/);

  await page.locator('[data-timer-explain="timer-approval-1"]').click();
  await expect(page.locator('.timer-explanation')).toContainText('waiting_for_due_time');
  await expect(page.locator('.timer-explanation')).toContainText('claim_when_due');

  page.once('dialog', dialog => dialog.accept('operator_review'));
  await page.locator('[data-timer-cancel="timer-approval-1"]').click();
  await expect.poll(() => timerRequests.length).toBeGreaterThan(1);

  await page.setViewportSize({ width: 390, height: 844 });
  const layout = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    body: document.body.scrollWidth,
    overflowing: [...document.body.querySelectorAll('.timer-inspector *')]
      .filter(element => !element.closest('.table-scroll') && element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().right > document.documentElement.clientWidth + 1)
      .slice(0, 10).map(element => ({ tag: element.tagName, class: element.className }))
  }));
  expect(layout.overflowing, JSON.stringify(layout)).toEqual([]);
  expect(layout.body).toBeLessThanOrEqual(layout.viewport + 1);
  expect(browserErrors).toEqual([]);
});
