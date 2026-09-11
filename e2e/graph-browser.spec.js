const fs = require('fs');
const path = require('path');
const { test, expect } = require('@playwright/test');

const apiURL = process.env.ADRO_GRAPH_BROWSER_API_URL || 'http://127.0.0.1:18086';
const reportFile = process.env.ADRO_GRAPH_BROWSER_REPORT || path.resolve('var/test-report/real-codex/browser-graph-evidence.json');
const repositoryURL = process.env.ADRO_GRAPH_BROWSER_REPOSITORY_URL || 'https://example.invalid/browser-real-graph.git';

function parseJSON(text) {
  try {
    return JSON.parse(text);
  } catch (_) {
    return { raw: text };
  }
}

function writeEvidence(evidence) {
  fs.mkdirSync(path.dirname(reportFile), { recursive: true });
  fs.writeFileSync(reportFile, `${JSON.stringify(evidence, null, 2)}\n`);
}

test('creates a graph in the browser, executes it with real Codex, and replays evidence', async ({ page }) => {
  test.skip(process.env.ADRO_RUN_BROWSER_REAL_GRAPH !== '1', 'real Codex browser graph suite is run by its dedicated entrypoint');

  const evidence = { api_url: apiURL, browser_created: true, repository_url: repositoryURL.startsWith('file://') ? 'file://<fixture-repo>' : repositoryURL, commit_sha: process.env.ADRO_COMMIT_SHA || '', timeline: null, replay: null, runs: [] };
  const apiHeaders = { 'X-Workspace-ID': 'local', 'X-Member-ID': 'admin' };

  await page.goto(`/?api=${encodeURIComponent(apiURL)}`);
  await expect(page.locator('#loginGate')).toBeVisible();
  await page.locator('#loginForm input[name="username"]').fill('admin');
  await page.locator('#loginForm input[name="password"]').fill('AdminPass123!');
  await page.locator('#loginForm button[type="submit"]').click();
  await expect(page.locator('#appShell')).toBeVisible();

  await page.locator('.nav-item[data-view="repositories"]').click();
  await page.locator('#newResource').click();
  await page.locator('#resourceFields input[name="name"]').fill('browser-real-graph-repository');
  await page.locator('#resourceFields input[name="clone_url"]').fill(repositoryURL);
  await page.locator('#resourceForm button[type="submit"]').click();

  await page.locator('.nav-item[data-view="requirements"]').click();
  await page.locator('#newRequirement').click();
  await page.locator('#requirementForm input[name="title"]').fill('Browser-created real graph acceptance');
  await page.locator('#requirementForm textarea[name="description"]').fill('Execute one immutable browser-created graph through the real local Codex provider.');
  await page.locator('#requirementForm textarea[name="acceptance"]').fill('The browser-created plan reaches a terminal pass and its timeline and replay are consistent.');
  await page.locator('#requirementRepository').selectOption({ label: 'browser-real-graph-repository' });
  await page.locator('#requirementAssignee').selectOption({ index: 0 });
  await page.locator('#requirementForm button[type="submit"]').click();

  const requirementRow = page.locator('tr[data-requirement-id]').filter({ hasText: 'Browser-created real graph acceptance' }).first();
  await expect(requirementRow).toBeVisible();
  const requirementID = await requirementRow.getAttribute('data-requirement-id');
  expect(requirementID).toBeTruthy();

  await page.locator('.nav-item[data-view="agents"]').click();
  await page.locator('#newAgent').click();
  const runtimeSelect = page.locator('#agentForm select[name="runtime"]');
  await expect(runtimeSelect.locator('option[value="codex"]')).toBeEnabled();
  await runtimeSelect.selectOption('codex');
  await expect(runtimeSelect).toHaveValue('codex');
  await page.locator('#agentForm select[name="member"]').selectOption({ index: 0 });
  await page.locator('#agentForm input[name="name"]').fill('Browser Real Graph Agent');
  await page.locator('#agentForm textarea[name="instructions"]').fill([
    'You are the real Codex executor for a browser-created ADRO graph.',
    'Use the terminal immediately and run exactly pwd in the provided checkout. Do not modify files or use another tool.',
    'Return exactly one ADRO_RESULT_JSON marker after the terminal command completes.',
    'The marker must be valid JSON with outcome pass, reason_code browser_graph_real, summary browser graph real execution passed, evidence_ids [browser-graph-real-1], and fields {browser_created_graph:true}.',
  ].join('\n'));
  await page.locator('#agentForm input[name="role"]').fill('browser-real-graph');
  await page.locator('#agentForm select[name="access_mode"]').selectOption('workspace');
  await page.locator('#agentForm button[type="submit"]').click();
  await expect(page.locator('#agentDialog')).not.toBeVisible();

  const agentRow = page.locator('tr').filter({ hasText: 'Browser Real Graph Agent' }).first();
  await expect(agentRow).toContainText('active');
  const agentID = (await agentRow.locator('.orchestration-id').textContent()).trim();
  expect(agentID).toBeTruthy();
  const agentResponse = await page.request.get(`${apiURL}/api/v1/workspaces/local/agents/${encodeURIComponent(agentID)}`, { headers: apiHeaders });
  evidence.agent = { status: agentResponse.status(), ok: agentResponse.ok(), body: parseJSON(await agentResponse.text()) };
  writeEvidence(evidence);
  expect(agentResponse.ok()).toBeTruthy();
  expect(evidence.agent.body.access_policy).toEqual({ mode: 'workspace' });
  expect(evidence.agent.body.executor_binding.runtime_id).toBe('codex');

  await page.locator('#newPlan').click();
  await page.locator('#nativePlanRequirement').selectOption(requirementID);
  await page.locator('#nativePlanTarget').selectOption(`agent:${agentID}`);
  await expect(page.locator('#nativePlanGraphSummary')).toBeVisible();
  await page.locator('#nativePlanGraphValidate').click();
  await expect(page.locator('#nativePlanGraphStatus')).toContainText('计划图已通过检查');
  await page.locator('#nativePlanForm button[type="submit"]').click();
  await expect(page.locator('#nativePlanDialog')).not.toBeVisible();

  const planRow = page.locator('tr').filter({ has: page.locator('[data-orchestration-kind="plan"]') }).filter({ hasText: requirementID }).first();
  await expect(planRow).toContainText('ready');
  const planID = (await planRow.locator('.orchestration-id').textContent()).trim();
  expect(planID).toBeTruthy();

  const startResponse = await page.request.post(`${apiURL}/api/v1/requirements/${encodeURIComponent(requirementID)}/start`, {
    headers: apiHeaders,
    data: {}
  });
  evidence.requirement_id = requirementID;
  evidence.start = { status: startResponse.status(), ok: startResponse.ok(), body: parseJSON(await startResponse.text()) };
  writeEvidence(evidence);
  expect(startResponse.ok()).toBeTruthy();
  const workItemsResponse = await page.request.get(`${apiURL}/api/v1/requirements/${encodeURIComponent(requirementID)}/work-items`, { headers: apiHeaders });
  evidence.work_items = { status: workItemsResponse.status(), ok: workItemsResponse.ok(), body: parseJSON(await workItemsResponse.text()) };
  writeEvidence(evidence);
  expect(workItemsResponse.ok()).toBeTruthy();
  const workItems = evidence.work_items.body;
  const workItemID = workItems.items?.[0]?.id;
  expect(workItemID).toBeTruthy();
  evidence.work_item_id = workItemID;
  writeEvidence(evidence);

  const tickResponse = await page.request.post(`${apiURL}/api/v1/execution-plans/${encodeURIComponent(planID)}/tick`, {
    headers: { ...apiHeaders, 'Idempotency-Key': `browser-real-graph-tick-${planID}` },
    data: { work_item_id: workItemID, agent_binding_id: agentID }
  });
  evidence.plan_id = planID;
  evidence.agent_id = agentID;
  evidence.tick = { status: tickResponse.status(), ok: tickResponse.ok(), body: parseJSON(await tickResponse.text()) };
  writeEvidence(evidence);
  expect(tickResponse.ok()).toBeTruthy();

  let terminalTimeline;
  const deadline = Date.now() + 25 * 60 * 1000;
  while (Date.now() < deadline) {
    const response = await page.request.get(`${apiURL}/api/v1/execution-plans/${encodeURIComponent(planID)}/timeline`, { headers: apiHeaders });
    expect(response.ok()).toBeTruthy();
    const timeline = await response.json();
    const projection = timeline.projection || {};
    if (projection.status === 'terminal') {
      terminalTimeline = timeline;
      break;
    }
    await page.waitForTimeout(1000);
  }
  expect(terminalTimeline).toBeTruthy();
  evidence.timeline = terminalTimeline;
  for (const attempt of Object.values(terminalTimeline.projection.attempts || {})) {
    if (!attempt.run_id) continue;
    const runResponse = await page.request.get(`${apiURL}/api/v1/runs/${encodeURIComponent(attempt.run_id)}`, { headers: apiHeaders });
    expect(runResponse.ok()).toBeTruthy();
    evidence.runs.push(await runResponse.json());
  }
  writeEvidence(evidence);
  expect(terminalTimeline.projection.terminal_outcome).toBe('succeeded');
  expect(terminalTimeline.events.some(event => (event.type || event.event_type) === 'attempt.finished')).toBeTruthy();

  const replayResponse = await page.request.get(`${apiURL}/api/v1/execution-plans/${encodeURIComponent(planID)}/replay`, { headers: apiHeaders });
  expect(replayResponse.ok()).toBeTruthy();
  evidence.replay = await replayResponse.json();
  writeEvidence(evidence);
  expect(evidence.replay.projection.status).toBe('terminal');
  expect(evidence.replay.projection.terminal_outcome).toBe('succeeded');

  writeEvidence(evidence);
  await page.screenshot({ path: process.env.ADRO_GRAPH_BROWSER_SCREENSHOT || path.join(path.dirname(reportFile), 'browser-graph.png'), fullPage: true });
});
