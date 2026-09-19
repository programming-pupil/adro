const { test, expect } = require('@playwright/test');

const resourceProjection = {
  dashboard: {
    scope: { tenant_id: 'tenant-local', workspace_id: 'local' },
    exposure: { tokens: 76000, tool_calls: 48, output_bytes: 8388608, wall_time_nanos: 480000000000, concurrency_slots: 3 },
    consumed: { tokens: 61000, tool_calls: 39, output_bytes: 6291456, wall_time_nanos: 360000000000, concurrency_slots: 2 },
    active_reservations: [
      {
        id: 'reservation-release-a',
        scope: { tenant_id: 'tenant-local', workspace_id: 'local', agent_id: 'release-reviewer', cost_center: 'release-42' },
        state: { requested: { tokens: 18000, concurrency_slots: 1 }, reserved: { tokens: 18000, concurrency_slots: 1 }, consumed: {}, released: {}, overage: {} },
        status: 'reserved',
        expires_at: '2026-09-19T12:30:00Z'
      },
      {
        id: 'reservation-recovery-b',
        scope: { tenant_id: 'tenant-local', workspace_id: 'local', agent_id: 'recovery-agent', cost_center: 'incident-7' },
        state: { requested: { tokens: 12000, tool_calls: 8, concurrency_slots: 1 }, reserved: { tokens: 12000, tool_calls: 8, concurrency_slots: 1 }, consumed: {}, released: {}, overage: {} },
        status: 'reserved',
        expires_at: '2026-09-19T12:45:00Z'
      }
    ],
    recent_usage: [
      {
        id: 'usage-release-a', reservation_id: 'reservation-release-a',
        scope: { tenant_id: 'tenant-local', workspace_id: 'local', agent_id: 'release-reviewer', cost_center: 'release-42' },
        normalized: { tokens: 31000 }, estimated: { tokens: 25000 }, discrepancy: { tokens: 6000 },
        delayed_billing: true, observed_at: '2026-09-19T11:20:00Z'
      },
      {
        id: 'usage-recovery-b', reservation_id: 'reservation-recovery-b',
        scope: { tenant_id: 'tenant-local', workspace_id: 'local', agent_id: 'recovery-agent', cost_center: 'incident-7' },
        normalized: { tokens: 14000 }, estimated: { tokens: 14000 }, discrepancy: {},
        provider_usage_missing: true, observed_at: '2026-09-19T11:22:00Z'
      }
    ],
    overage_reservation_ids: ['reservation-overage-c'],
    missing_provider_usage: 1,
    delayed_bills: 1,
    child_agent_usage: {
      'release-reviewer': { tokens: 31000, tool_calls: 21, output_bytes: 4194304 },
      'recovery-agent': { tokens: 14000, tool_calls: 10, output_bytes: 1048576 }
    },
    generated_at: '2026-09-19T11:30:00Z'
  },
  quotas: [
    {
      scope: { tenant_id: 'tenant-local', workspace_id: 'local' },
      soft_limit: { tokens: 70000, tool_calls: 45, output_bytes: 7340032, wall_time_nanos: 420000000000, concurrency_slots: 3 },
      hard_limit: { tokens: 100000, tool_calls: 70, output_bytes: 12582912, wall_time_nanos: 600000000000, concurrency_slots: 4 },
      queue_weight: 2,
      emergency_priority_ceiling: 80,
      updated_at: '2026-09-19T10:00:00Z'
    }
  ]
};

test('renders durable resource burn, anomalies, reservations, and child-Agent attribution', async ({ page }) => {
  const browserErrors = [];
  const resourceRequests = [];
  page.on('pageerror', error => browserErrors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') browserErrors.push(message.text()); });
  await page.route('**/api/v1/resources?**', route => {
    resourceRequests.push(route.request());
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(resourceProjection) });
  });

  await page.goto('/');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect.poll(() => resourceRequests.length).toBeGreaterThan(0);
  expect(resourceRequests[0].headers()['x-workspace-id']).toBe('local');

  await page.locator('.nav-item[data-view="cost"]').click();
  await expect(page.locator('.resource-console')).toBeVisible();
  await expect(page.locator('.resource-stat-grid')).toContainText('76,000');
  await expect(page.locator('[data-resource-dimension="tokens"]')).toContainText('76,000 / 100,000');
  await expect(page.locator('.resource-anomaly-list')).toContainText('reservation-overage-c');
  await expect(page.locator('.resource-anomaly-list')).toContainText('Provider usage 缺失');
  await expect(page.locator('.resource-agent-list')).toContainText('release-reviewer');
  await expect(page.locator('.resource-agent-list')).toContainText('recovery-agent');
  await expect(page.locator('.resource-reservation-panel tbody tr')).toHaveCount(2);
  await expect(page.locator('.resource-reservation-panel')).toContainText('reservation-release-a');

  const beforeRefresh = resourceRequests.length;
  await page.locator('#resourceRefresh').click();
  await expect.poll(() => resourceRequests.length).toBeGreaterThan(beforeRefresh);

  await page.setViewportSize({ width: 390, height: 844 });
  const layout = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    body: document.body.scrollWidth,
    overflowing: [...document.body.querySelectorAll('.resource-console *')]
      .filter(element => !element.closest('.table-scroll') && element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().right > document.documentElement.clientWidth + 1)
      .map(element => ({ tag: element.tagName, class: element.className, right: Math.round(element.getBoundingClientRect().right) }))
      .slice(0, 10)
  }));
  expect(layout.overflowing, JSON.stringify(layout)).toEqual([]);
  expect(layout.body).toBeLessThanOrEqual(layout.viewport + 1);
  expect(browserErrors).toEqual([]);
});
