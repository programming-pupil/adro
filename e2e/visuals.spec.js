const { test, expect } = require('@playwright/test');

test('captures the ADRO technical console on desktop and mobile', async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto('/');
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
  await expect(page.locator('#agentDialog')).not.toBeVisible();
  await expect(page.locator('#connectionText')).toHaveText('控制面已连接');
  await page.screenshot({ path: 'var/adro-workbench-cyber.png', fullPage: true });

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await expect(page.locator('#autoGeneratePlan')).toBeVisible();
  await expect(page.locator('#planAgentField')).toBeHidden();
  await page.locator('#autoGeneratePlan').check();
  await expect(page.locator('#planAgentField')).toBeVisible();
  await page.screenshot({ path: 'var/adro-requirement-auto-plan-cyber.png', fullPage: true });
  await page.locator('#cancelDialog').click();

  await page.locator('.nav-item[data-view="executions"]').click();
  await expect(page.locator('#appView')).toContainText('执行舱');
  await expect(page.locator('#newPipeline')).toHaveCount(0);
  await expect(page.locator('#pipelineDialog')).toHaveCount(0);
  await expect(page.locator('#appView')).not.toContainText('1→7');
  await page.screenshot({ path: 'var/adro-execution-cockpit-cyber.png', fullPage: true });

  await page.locator('.nav-chat[data-view="chats"]').click();
  await expect(page.locator('.nav-chat[data-view="chats"]')).toHaveClass(/active/);
  await page.screenshot({ path: 'var/adro-chat-active-cyber.png', fullPage: true });

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
  const ownerSelect = page.locator('#agentForm select[name="member"]');
  await ownerSelect.selectOption({ index: 0 });
  const ownerID = await ownerSelect.inputValue();
  await page.locator('#agentForm input[name="name"]').fill('Design Review Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill('Review architecture, risk, and evidence before engineering.');
  await page.locator('#agentForm input[name="role"]').fill('reviewer');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#appView')).toContainText(ownerID);
  await page.screenshot({ path: 'var/adro-agents-cyber.png', fullPage: true });

  await page.locator('.nav-item[data-view="artifacts"]').click();
  await expect(page.locator('#screenshotFile')).toBeAttached();
  await page.screenshot({ path: 'var/adro-artifacts-cyber.png', fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.nav-item[data-view="workbench"]').click();
  await page.screenshot({ path: 'var/adro-workbench-mobile-cyber.png', fullPage: true });
});

test('keeps entity composers fast, localized, and attachment-aware', async ({ page }) => {
  test.setTimeout(90_000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });

  await page.goto('/');
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await expect(page.locator('#agentDialog')).not.toBeVisible();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await page.locator('#requirementForm input[name="attachments"]').setInputFiles([
    {name: 'brief.txt', mimeType: 'text/plain', buffer: Buffer.from('brief')},
    {name: 'screen.png', mimeType: 'image/png', buffer: Buffer.from('not-a-real-png')}
  ]);
  await expect(page.locator('#requirementAttachmentPreview')).toContainText('brief.txt');
  await expect(page.locator('#requirementAttachmentPreview')).toContainText('screen.png');
  await expect(page.locator('#requirementAttachmentPreview img')).toHaveCount(1);
  await page.locator('#requirementAttachmentPreview [data-remove-entity-attachment="requirement"]').first().click();
  await expect(page.locator('#requirementAttachmentPreview')).not.toContainText('brief.txt');
  await page.locator('#cancelDialog').click();

  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('entity-composer-project');
  await page.locator('#resourceFields input[name="clone_url"]').fill('https://example.invalid/entity-composer.git');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('tr').filter({hasText: 'entity-composer-project'}).first()).toBeVisible();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm textarea[name="description"]').fill('Entity composer related requirement\n\nKeep the linked project and executor editable.');
  await page.locator('#requirementRepository').selectOption({label: 'entity-composer-project'});
  const linkedRepositoryID = await page.locator('#requirementRepository').inputValue();
  await page.locator('#requirementAssignee').selectOption({index: 0});
  const linkedAssigneeID = await page.locator('#requirementAssignee').inputValue();
  await page.locator('#requirementForm button[type="submit"]').click();
  const linkedRequirement = page.locator('tr[data-requirement-id]').filter({hasText: 'Entity composer related requirement'}).first();
  await expect(linkedRequirement).toBeVisible();
  const linkedRequirementID = await linkedRequirement.getAttribute('data-requirement-id');

  await page.locator('.nav-item[data-view="bugs"]').click();
  await page.locator('#newResource').click();
  await expect(page.locator('#bugDialog')).toBeVisible();
  await expect(page.locator('[data-i18n="bugDescriptionOnly"]')).toHaveText('Bug 描述');
  await expect(page.locator('#bugDescription')).toHaveAttribute('placeholder', /复现步骤/);
  await expect(page.locator('#bugRequirement')).toBeVisible();
  await page.locator('#bugRequirement').selectOption(linkedRequirementID);
  await expect(page.locator('#bugRepository')).toHaveValue(linkedRepositoryID);
  await expect(page.locator('#bugAssignee')).toHaveValue(linkedAssigneeID);
  await page.locator('#bugRepository').selectOption({index: 0});
  await page.locator('#bugAssignee').selectOption({index: 0});
  await page.evaluate(() => {
    const input = document.querySelector('#bugDescription');
    const transfer = new DataTransfer();
    transfer.items.add(new File(['image'], 'clipboard.png', {type: 'image/png'}));
    input.dispatchEvent(new ClipboardEvent('paste', {bubbles: true, clipboardData: transfer}));
  });
  await expect(page.locator('#bugAttachmentPreview img')).toHaveCount(1);
  await expect(page.locator('#bugAttachmentPreview')).toContainText('clipboard.png');
  await page.evaluate(() => {
    const dropZone = document.querySelector('#bugAttachments').closest('.file-drop');
    const transfer = new DataTransfer();
    transfer.items.add(new File(['log'], 'dropped.log', {type: 'text/plain'}));
    dropZone.dispatchEvent(new DragEvent('drop', {bubbles: true, dataTransfer: transfer}));
  });
  await expect(page.locator('#bugAttachmentPreview')).toContainText('dropped.log');
  await page.locator('#bugAttachmentPreview [data-remove-entity-attachment="bug"]').first().click();
  await page.locator('#bugAttachmentPreview [data-remove-entity-attachment="bug"]').first().click();
  await expect(page.locator('#bugAttachmentPreview')).toBeEmpty();
  await page.locator('#cancelBugDialog').click();

  await page.locator('#logoutButton').click();
  await expect(page.locator('#logoutConfirmDialog')).toBeVisible();
  await page.locator('#logoutConfirmCancel').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await page.locator('#logoutButton').click();
  await page.locator('#logoutConfirmButton').click();
  await expect(page.locator('#loginGate')).toBeVisible();
  expect(errors).toEqual([]);
});
