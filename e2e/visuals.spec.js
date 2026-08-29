const { test, expect } = require('@playwright/test');

const apiQuery = '?api=http://127.0.0.1:18080';

test('captures the ADRO technical console on desktop and mobile', async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto(`/${apiQuery}`);
  await expect(page.locator('#loginGate')).toBeVisible();
  await expect(page.locator('#loginLocaleToggle')).toHaveText('EN');
  await page.locator('#loginLocaleToggle').click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.locator('#loginLocaleToggle')).toHaveText('中文');
  await page.locator('#loginLocaleToggle').click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await page.screenshot({ path: 'var/adro-login-cyber.png', fullPage: true });
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  await page.screenshot({ path: 'var/adro-workbench-cyber.png', fullPage: true });

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await page.screenshot({ path: 'var/adro-requirement-form-cyber.png', fullPage: true });
  await page.locator('#cancelDialog').click();

  await page.locator('.nav-item[data-view="bugs"]').click();
  await page.locator('#newResource').click();
  await expect(page.locator('#bugDialog')).toBeVisible();
  expect(await page.locator('#bugForm button[type="submit"]').evaluate(element => element.getBoundingClientRect().bottom <= window.innerHeight)).toBe(true);
  await page.screenshot({ path: 'var/adro-bug-form-cyber.png', fullPage: true });
  await page.locator('#cancelBugDialog').click();

  await page.locator('.nav-item[data-view="admin"]').click();
  await page.locator('#newUser').click();
  await expect(page.locator('#userDialog')).toBeVisible();
  expect(await page.locator('#userForm button[type="submit"]').evaluate(element => element.getBoundingClientRect().bottom <= window.innerHeight)).toBe(true);
  await page.screenshot({ path: 'var/adro-access-control-cyber.png', fullPage: true });
  await page.locator('#cancelUserDialog').click();

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  await page.locator('#agentForm input[name="member"]').fill('design-reviewer');
  await page.locator('#agentForm input[name="name"]').fill('Design Review Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Review architecture, risk, and evidence before engineering.');
  await page.locator('#agentForm input[name="role"]').fill('reviewer');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#appView')).toContainText('design-reviewer');
  await page.screenshot({ path: 'var/adro-agents-cyber.png', fullPage: true });

  await page.locator('.nav-item[data-view="artifacts"]').click();
  await expect(page.locator('#screenshotFile')).toBeAttached();
  await page.screenshot({ path: 'var/adro-artifacts-cyber.png', fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.nav-item[data-view="workbench"]').click();
  await page.screenshot({ path: 'var/adro-workbench-mobile-cyber.png', fullPage: true });
});
