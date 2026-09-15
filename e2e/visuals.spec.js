const { test, expect } = require('@playwright/test');

test('captures the ADRO technical console on desktop and mobile', async ({ page }) => {
  test.setTimeout(180_000);
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

  await page.locator('.nav-item[data-view="delivery"]').click();
  await page.locator('#newRequirement').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await expect(page.locator('#autoGeneratePlan')).toBeVisible();
  await expect(page.locator('#planAgentField')).toBeHidden();
  await page.locator('#autoGeneratePlan').check();
  await expect(page.locator('#planAgentField')).toBeVisible();
  await page.screenshot({ path: 'var/adro-requirement-auto-plan-cyber.png', fullPage: true });
  await page.locator('#cancelDialog').click();

  await expect(page.locator('.nav-item[data-view="designReview"], .nav-item[data-view="executions"]')).toHaveCount(0);
  await expect(page.locator('#newPipeline')).toHaveCount(0);
  await expect(page.locator('#pipelineDialog')).toHaveCount(0);

  await page.locator('.nav-chat[data-view="chats"]').click();
  await expect(page.locator('.nav-chat[data-view="chats"]')).toHaveClass(/active/);
  await page.screenshot({ path: 'var/adro-chat-active-cyber.png', fullPage: true });

  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('delivery-visual-project');
  await page.locator('#resourceFields input[name="local_path"]').fill('/tmp');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('tr').filter({ hasText: 'delivery-visual-project' }).first()).toBeVisible();

  await page.locator('.nav-item[data-view="delivery"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementDescription').fill('Unified delivery visual acceptance\n\nKeep context, plan, development, validation, and bugs on one delivery canvas.');
  await page.locator('#requirementRepository').selectOption({ label: 'delivery-visual-project' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();
  const deliveryRow = page.locator('tr[data-delivery-requirement-id]').filter({ hasText: 'Unified delivery visual acceptance' }).first();
  await expect(deliveryRow).toBeVisible();
  const deliveryID = await deliveryRow.getAttribute('data-delivery-requirement-id');
  await deliveryRow.locator('[data-delivery-add-bug]').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await expect(page.locator('#deliveryDialogTitle')).toHaveText('创建 Bug');
  await expect(page.locator('#deliveryDialogSubtitle')).toHaveText('在父需求下记录一个可复现的缺陷');
  await expect(page.locator('#bugRequirement')).toHaveValue(deliveryID);
  await expect(page.locator('#bugRequirement')).toHaveAttribute('required', '');
  await expect(page.locator('#bugRequirement option[value=""]')).toHaveAttribute('disabled', '');
  await page.locator('#requirementDescription').fill('Delivery canvas visual regression\n\nReproduction steps\nOpen the delivery canvas\n\nExpected result\nThe linked bug stays in the parent context\n\nActual result\nVisual acceptance fixture');
  await page.screenshot({ path: 'var/adro-bug-form-cyber.png', fullPage: true });
  await page.locator('#requirementForm button[type="submit"]').click();
  await expect(deliveryRow.locator('.delivery-bug-summary')).toContainText('1');
  await expect(deliveryRow.locator('[data-delivery-toggle]')).toHaveAttribute('aria-expanded', 'true');
  await page.screenshot({ path: 'var/adro-delivery-board-cyber.png', fullPage: true });
  await deliveryRow.click();
  await expect(page.locator('#detailDialog')).toBeVisible();
  await expect(page.locator('.delivery-detail-canvas')).toBeVisible();
  await expect(page.locator('.delivery-related-item')).toContainText('Delivery canvas visual regression');
  expect(await page.locator('#detailDialog').evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(900);
  expect(await page.locator('#detailBody').evaluate(element => getComputedStyle(element).overflowY)).toBe('auto');
  await page.screenshot({ path: 'var/adro-delivery-canvas-cyber.png', fullPage: true });
  await page.locator('#closeDetail').click();

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileChatNavigation = await page.locator('.nav-chat[data-view="chats"]').evaluate(element => {
    const label = element.lastElementChild;
    return {
      buttonHeight: element.getBoundingClientRect().height,
      labelHeight: label.getBoundingClientRect().height,
      whiteSpace: getComputedStyle(label).whiteSpace
    };
  });
  expect(mobileChatNavigation.whiteSpace).toBe('nowrap');
  expect(mobileChatNavigation.buttonHeight).toBeLessThanOrEqual(44);
  expect(mobileChatNavigation.labelHeight).toBeLessThanOrEqual(20);
  await page.screenshot({ path: 'var/adro-delivery-board-mobile-cyber.png', fullPage: true });
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.locator('#newRequirement').click();
  await page.locator('.delivery-type-switch button[data-delivery-kind="bug"]').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await expect(page.locator('#deliveryDialogTitle')).toHaveText('创建 Bug');
  await expect(page.locator('#bugRequirement option[value=""]')).toHaveAttribute('disabled', '');
  await expect(page.locator('#requirementForm button[type="submit"]')).toBeDisabled();
  await page.locator('#bugRequirement').selectOption(deliveryID);
  await expect(page.locator('#requirementForm button[type="submit"]')).toBeEnabled();
  expect(await page.locator('#requirementForm button[type="submit"]').evaluate(element => element.getBoundingClientRect().bottom <= window.innerHeight)).toBe(true);
  await page.locator('#cancelDialog').click();

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
  await page.screenshot({ path: 'var/adro-agent-studio-cyber.png', fullPage: true });
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

  await page.locator('.nav-item[data-view="delivery"]').click();
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
  await page.locator('#resourceFields input[name="local_path"]').fill('/tmp');
  await page.locator('#resourceForm button[type="submit"]').click();
  await expect(page.locator('tr').filter({hasText: 'entity-composer-project'}).first()).toBeVisible();

  await page.locator('.nav-item[data-view="delivery"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm textarea[name="description"]').fill('Entity composer related requirement\n\nKeep the linked project and executor editable.');
  await page.locator('#requirementRepository').selectOption({label: 'entity-composer-project'});
  const linkedRepositoryID = await page.locator('#requirementRepository').inputValue();
  await page.locator('#requirementAssignee').selectOption({index: 0});
  const linkedAssigneeID = await page.locator('#requirementAssignee').inputValue();
  await page.locator('#requirementForm button[type="submit"]').click();
  const linkedRequirement = page.locator('tr[data-delivery-requirement-id]').filter({hasText: 'Entity composer related requirement'}).first();
  await expect(linkedRequirement).toBeVisible();
  const linkedRequirementID = await linkedRequirement.getAttribute('data-delivery-requirement-id');

  await page.locator('.nav-item[data-view="delivery"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('.delivery-type-switch button[data-delivery-kind="bug"]').click();
  await expect(page.locator('#requirementDialog')).toBeVisible();
  await expect(page.locator('#deliveryDialogTitle')).toHaveText('创建 Bug');
  await expect(page.locator('#requirementDescription')).toHaveAttribute('placeholder', /复现步骤/);
  await expect(page.locator('#bugRequirement')).toBeVisible();
  await expect(page.locator('#bugRequirement')).toHaveAttribute('required', '');
  await expect(page.locator('#bugRequirement option[value=""]')).toHaveAttribute('disabled', '');
  await expect(page.locator('#requirementForm button[type="submit"]')).toBeDisabled();
  await page.locator('#bugRequirement').selectOption(linkedRequirementID);
  await expect(page.locator('#requirementForm button[type="submit"]')).toBeEnabled();
  await expect(page.locator('#requirementRepository')).toHaveValue(linkedRepositoryID);
  await expect(page.locator('#requirementAssignee')).toHaveValue(linkedAssigneeID);
  await page.evaluate(() => {
    const input = document.querySelector('#requirementDescription');
    const transfer = new DataTransfer();
    transfer.items.add(new File(['image'], 'clipboard.png', {type: 'image/png'}));
    input.dispatchEvent(new ClipboardEvent('paste', {bubbles: true, clipboardData: transfer}));
  });
  await expect(page.locator('#requirementAttachmentPreview img')).toHaveCount(1);
  await expect(page.locator('#requirementAttachmentPreview')).toContainText('clipboard.png');
  await page.evaluate(() => {
    const dropZone = document.querySelector('#requirementAttachments').closest('.file-drop');
    const transfer = new DataTransfer();
    transfer.items.add(new File(['log'], 'dropped.log', {type: 'text/plain'}));
    dropZone.dispatchEvent(new DragEvent('drop', {bubbles: true, dataTransfer: transfer}));
  });
  await expect(page.locator('#requirementAttachmentPreview')).toContainText('dropped.log');
  await page.locator('#requirementAttachmentPreview [data-remove-entity-attachment="requirement"]').first().click();
  await page.locator('#requirementAttachmentPreview [data-remove-entity-attachment="requirement"]').first().click();
  await expect(page.locator('#requirementAttachmentPreview')).toBeEmpty();
  await page.locator('#cancelDialog').click();

  await page.locator('#logoutButton').click();
  await expect(page.locator('#logoutConfirmDialog')).toBeVisible();
  await page.locator('#logoutConfirmCancel').click();
  await expect(page.locator('#appShell')).toBeVisible();
  await page.locator('#logoutButton').click();
  await page.locator('#logoutConfirmButton').click();
  await expect(page.locator('#loginGate')).toBeVisible();
  expect(errors).toEqual([]);
});
