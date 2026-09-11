const { test, expect } = require('@playwright/test');
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

test.setTimeout(120_000);

const root = path.resolve(__dirname, '..');
const sourceSha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
execFileSync('ruby', ['scripts/coverage-ledger.rb', '--check'], { cwd: root, stdio: 'pipe' });
const reportRoot = path.join(root, 'var', 'test-report', 'coverage-ledger', sourceSha);
const menus = JSON.parse(fs.readFileSync(path.join(reportRoot, 'menus.json'), 'utf8'));
const actions = JSON.parse(fs.readFileSync(path.join(reportRoot, 'dom_actions.json'), 'utf8'));
const browserEvidenceRoot = path.join(root, 'var', 'test-report', 'browser', sourceSha);

async function login(page) {
  await page.goto('/?api=http://127.0.0.1:18080');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  const agentsResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'GET' && url.pathname === '/api/v1/workspaces/local/agents';
  });
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  const agents = await (await agentsResponse).json();
  const onboarding = page.locator('#agentDialog');
  if ((agents.items || []).length === 0) {
    await expect(onboarding).toBeVisible();
    await onboarding.locator('button[type="submit"]').click();
    await expect(onboarding).not.toBeVisible();
  }
}

function writeEvidence(name, value) {
  fs.mkdirSync(browserEvidenceRoot, { recursive: true, mode: 0o700 });
  fs.writeFileSync(path.join(browserEvidenceRoot, name), `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

test('release coverage ledger renders every declared menu', async ({ page }) => {
  await login(page);
  const results = [];
  expect(menus).toHaveLength(19);
  for (const menu of menus) {
    const selector = menu.menu_id === 'chats'
      ? `.nav-chat[data-view="${menu.menu_id}"]`
      : `.nav-item[data-view="${menu.menu_id}"]`;
    const navigation = page.locator(selector);
    await expect(navigation, `missing navigation for ${menu.menu_id}`).toHaveCount(1);
    await navigation.scrollIntoViewIfNeeded();
    await navigation.click();
    await expect(page.locator('#appView')).toBeVisible();
    await expect(page.locator('#pageTitle')).not.toHaveText('');
    results.push({ menu_id: menu.menu_id, case_id: menu.case_id, selector, title: await page.locator('#pageTitle').innerText(), status: 'passed' });
  }

  await page.reload();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  writeEvidence('menus.json', { schema_version: 1, source_sha: sourceSha, status: 'passed', results });
});

test('release coverage ledger exposes every registered action', async ({ page }) => {
  await login(page);
  const ids = new Set();
  for (const action of actions) {
    expect(action.action_id).toMatch(/^button:[a-f0-9]{16}:\d+$/);
    expect(ids.has(action.action_id), `duplicate ${action.action_id}`).toBe(false);
    ids.add(action.action_id);
    expect(action.selector).not.toContain('data-source-line');
    expect(fs.existsSync(path.join(root, action.source_file)), action.source_file).toBe(true);
  }

  const rendered = [];
  for (const menu of menus) {
    const selector = menu.menu_id === 'chats'
      ? `.nav-chat[data-view="${menu.menu_id}"]`
      : `.nav-item[data-view="${menu.menu_id}"]`;
    await page.locator(selector).click();
    const buttons = await page.locator('button').evaluateAll(elements => elements.map(element => ({
      id: element.id,
      type: element.type,
      data: Object.fromEntries([...element.attributes].filter(attribute => attribute.name.startsWith('data-')).map(attribute => [attribute.name, attribute.value])),
      disabled: element.disabled,
      visible: Boolean(element.offsetWidth || element.offsetHeight || element.getClientRects().length)
    })));
    rendered.push({ menu_id: menu.menu_id, buttons });
    for (const button of buttons.filter(item => item.visible)) {
      expect(Boolean(button.id) || Object.keys(button.data).length > 0 || button.type === 'submit', `${menu.menu_id} has an unregistered visible button`).toBe(true);
    }
  }

  writeEvidence('actions.json', { schema_version: 1, source_sha: sourceSha, status: 'passed', inventory_count: actions.length, rendered });
});
