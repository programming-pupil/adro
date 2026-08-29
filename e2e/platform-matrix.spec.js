const { test, expect } = require('@playwright/test');

test('login, navigation, locale, and responsive layout remain usable', async ({ page }) => {
  const browserErrors = [];
  page.on('pageerror', error => browserErrors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') browserErrors.push(message.text());
  });
  await page.goto('/?api=http://127.0.0.1:18080');
  await page.locator('#loginLocaleToggle').click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();

  for (const view of ['workbench', 'requirements', 'bugs', 'repositories', 'agents', 'integrations', 'artifacts', 'admin']) {
    const item = page.locator(`.nav-item[data-view="${view}"]`);
    await item.scrollIntoViewIfNeeded();
    await item.click();
    await expect(item).toHaveClass(/active/);
    await expect(page.locator('#appView')).toBeVisible();
  }

  const layout = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    body: document.body.scrollWidth,
    app: document.querySelector('#appShell').getBoundingClientRect().width,
    overflowing: [...document.body.querySelectorAll('*')]
      .filter(element => {
        const rect = element.getBoundingClientRect();
        return !element.closest('.table-scroll') && rect.width > 0 && rect.right > document.documentElement.clientWidth + 1;
      })
      .slice(0, 12)
      .map(element => ({ tag: element.tagName, id: element.id, class: element.className, right: Math.round(element.getBoundingClientRect().right) }))
  }));
  expect(layout.overflowing, JSON.stringify(layout)).toEqual([]);
  expect(layout.body).toBeLessThanOrEqual(layout.viewport + 1);
  expect(layout.app).toBeLessThanOrEqual(layout.viewport + 1);
  expect(browserErrors).toEqual([]);
});
