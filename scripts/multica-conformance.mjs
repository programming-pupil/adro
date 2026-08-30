#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { existsSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const startedAt = new Date().toISOString();
const report = { schema_version: 1, suite: 'adro-real-multica-v1', started_at: startedAt, status: 'running', checks: [], evidence: {} };
let activeWebSocketProbe;
let activeWebSocketReadyFile;
const required = [
  'ADRO_CONFORMANCE_BASE_URL', 'ADRO_CONFORMANCE_USERNAME', 'ADRO_CONFORMANCE_PASSWORD',
  'ADRO_CONFORMANCE_WORKSPACE_ID', 'ADRO_CONFORMANCE_REPOSITORY_ID', 'ADRO_CONFORMANCE_MEMBER_ID',
  'ADRO_MULTICA_URL', 'ADRO_MULTICA_TOKEN', 'ADRO_MULTICA_WS_URL'
];

class Blocked extends Error {}
class Failed extends Error {}

function scopedWebSocketURL(rawURL) {
  let parsed;
  try { parsed = new URL(rawURL); } catch { throw new Blocked('ADRO_MULTICA_WS_URL is not a valid WebSocket URL'); }
  if (parsed.protocol !== 'ws:' && parsed.protocol !== 'wss:') throw new Blocked('ADRO_MULTICA_WS_URL must use ws or wss');
  if (!parsed.searchParams.has('runtime_id') && !parsed.searchParams.has('runtime_ids')) {
    if (!process.env.ADRO_MULTICA_RUNTIME_ID) throw new Blocked('ADRO_MULTICA_RUNTIME_ID is required when ADRO_MULTICA_WS_URL has no runtime scope');
    parsed.searchParams.set('runtime_ids', process.env.ADRO_MULTICA_RUNTIME_ID);
  }
  return parsed.toString();
}

function startWebSocketProbe(runID, readyFile) {
  const websocketURL = scopedWebSocketURL(process.env.ADRO_MULTICA_WS_URL);
  const child = spawn('go', ['run', './cmd/adro-conformance', '--websocket-url', websocketURL, '--run-id', runID, '--ready-file', readyFile, '--timeout', '45s'], {
    cwd: root,
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe']
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk.toString(); });
  child.stderr.on('data', chunk => { stderr += chunk.toString(); });
  const result = new Promise(resolveResult => child.on('close', (code, signal) => resolveResult({ code, signal, stdout, stderr })));
  return { child, result, closed: false };
}

async function waitForWebSocketReady(probe, readyFile) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (existsSync(readyFile)) return;
    if (probe.closed) throw new Blocked('authenticated daemon WebSocket exited before the handshake completed');
    await delay(100);
  }
  throw new Blocked('authenticated daemon WebSocket did not complete its handshake before timeout');
}

async function finishWebSocketProbe(probe) {
  const result = await probe.result;
  if (result.code !== 0) {
    let parsed = {};
    try { parsed = JSON.parse(result.stdout); } catch {}
    throw new Blocked(parsed.reason || 'authenticated daemon WebSocket did not deliver a valid run event');
  }
  try { return JSON.parse(result.stdout); } catch { throw new Blocked('authenticated daemon WebSocket returned non-JSON output'); }
}

function record(name, status, detail = '') {
  report.checks.push({ name, status, ...(detail ? { detail } : {}) });
}

function block(name, detail) {
  record(name, 'blocked', detail);
  throw new Blocked(detail);
}

function assertEvidence(name, value, detail) {
  if (!value) block(name, detail);
  record(name, 'passed');
  return value;
}

function delay(ms) {
  return new Promise(resolveDelay => setTimeout(resolveDelay, ms));
}

const baseURL = (process.env.ADRO_CONFORMANCE_BASE_URL || '').replace(/\/$/, '');
let cookie = '';

async function request(method, path, { body, form, idempotencyKey, expected = [200] } = {}) {
  const headers = { Accept: 'application/json' };
  if (cookie) headers.Cookie = cookie;
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
  let payload;
  if (form) {
    payload = form;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  let response;
  try {
    response = await fetch(`${baseURL}${path}`, { method, headers, body: payload, signal: AbortSignal.timeout(30_000) });
  } catch (error) {
    throw new Blocked(`${method} ${path} is unreachable: ${error.message}`);
  }
  const text = await response.text();
  let parsed = {};
  if (text) {
    try { parsed = JSON.parse(text); } catch { throw new Failed(`${method} ${path} returned non-JSON`); }
  }
  if (!expected.includes(response.status)) {
    const code = parsed.error_code || `http_${response.status}`;
    if ([501, 502, 503, 504].includes(response.status) || code.includes('capability') || code.includes('provider')) {
      throw new Blocked(`${method} ${path} blocked by ${code}`);
    }
    throw new Failed(`${method} ${path} returned ${response.status} (${code})`);
  }
  return { body: parsed, response };
}

// Starting a requirement intentionally exercises Multica's native assignment
// trigger. The conformance run must then be the first task on this fresh issue;
// cancel that automatically-created task through the real Multica API before
// asking ADRO to enqueue its explicit rerun. This setup is scoped to the issue
// created by this process and never changes Provider rerun behavior.
function conformanceProviderWorkspaceID() {
  // ADRO's workspace id is an application-local identifier; Multica's native
  // task routes require the provider workspace UUID. Keep an explicit override
  // for hosted setups and otherwise use the adapter's configured workspace.
  return process.env.ADRO_CONFORMANCE_PROVIDER_WORKSPACE_ID
    || process.env.ADRO_MULTICA_WORKSPACE_ID
    || '';
}

async function cancelNativeAssignmentTasks(issueID) {
  const nativeBaseURL = process.env.ADRO_MULTICA_URL.replace(/\/$/, '');
  const providerWorkspaceID = conformanceProviderWorkspaceID();
  if (!providerWorkspaceID) throw new Blocked('ADRO_MULTICA_WORKSPACE_ID is required for native assignment cleanup');
  const query = new URLSearchParams({ workspace_id: providerWorkspaceID });
  let response;
  try {
    response = await fetch(`${nativeBaseURL}/api/issues/${encodeURIComponent(issueID)}/task-runs?${query}`, {
      headers: { Accept: 'application/json', Authorization: `Bearer ${process.env.ADRO_MULTICA_TOKEN}` },
      signal: AbortSignal.timeout(30_000)
    });
  } catch (error) {
    throw new Blocked(`GET Multica task-runs is unreachable: ${error.message}`);
  }
  const text = await response.text();
  let parsed = {};
  if (text) {
    try { parsed = JSON.parse(text); } catch { throw new Failed('GET Multica task-runs returned non-JSON'); }
  }
  if (!response.ok) throw new Blocked(`GET Multica task-runs returned ${response.status}`);
  const tasks = Array.isArray(parsed) ? parsed : (parsed.items || parsed.tasks || []);
  let cancelled = 0;
  for (const task of tasks) {
    const status = String(task.status || '').toLowerCase();
    if (!task.id || !['queued', 'dispatched', 'running'].includes(status)) continue;
    const cancelQuery = new URLSearchParams({ workspace_id: providerWorkspaceID });
    let cancelResponse;
    try {
      cancelResponse = await fetch(`${nativeBaseURL}/api/issues/${encodeURIComponent(issueID)}/tasks/${encodeURIComponent(task.id)}/cancel?${cancelQuery}`, {
        method: 'POST',
        headers: { Accept: 'application/json', Authorization: `Bearer ${process.env.ADRO_MULTICA_TOKEN}` },
        signal: AbortSignal.timeout(30_000)
      });
    } catch (error) {
      throw new Blocked(`POST Multica task cancel is unreachable: ${error.message}`);
    }
    if (!cancelResponse.ok) throw new Blocked(`POST Multica task cancel returned ${cancelResponse.status}`);
    cancelled++;
  }
  record('native-assignment-cleanup', 'passed', `cancelled ${cancelled} active assignment task(s)`);
}

async function waitForRun(runID, label) {
  const timeoutMS = Number(process.env.ADRO_CONFORMANCE_TIMEOUT_MS || 600_000);
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    const { body } = await request('GET', `/api/v1/runs/${encodeURIComponent(runID)}`);
    const status = String(body.status || '').toLowerCase();
    if (['completed', 'succeeded', 'success', 'failed', 'cancelled', 'canceled'].includes(status)) {
      if (!['completed', 'succeeded', 'success'].includes(status)) throw new Failed(`${label} ended with status ${status}`);
      return body;
    }
    await delay(5000);
  }
  throw new Blocked(`${label} did not reach a terminal status before timeout`);
}

function writeReport() {
  report.finished_at = new Date().toISOString();
  const output = `${JSON.stringify(report, null, 2)}\n`;
  if (process.env.ADRO_CONFORMANCE_REPORT) writeFileSync(process.env.ADRO_CONFORMANCE_REPORT, output);
  process.stdout.write(output);
}

async function main() {
  const missing = required.filter(name => !process.env[name]);
  if (missing.length) block('configuration', `missing ${missing.join(', ')}`);
  const login = await request('POST', '/api/v1/auth/login', {
    body: { username: process.env.ADRO_CONFORMANCE_USERNAME, password: process.env.ADRO_CONFORMANCE_PASSWORD }
  });
  cookie = (login.response.headers.get('set-cookie') || '').split(';')[0];
  assertEvidence('adro-authentication', cookie, 'ADRO login did not return a session cookie');

  const diagnostics = (await request('GET', '/api/v1/provider/diagnostics')).body;
  if (diagnostics.provider !== 'multica') block('provider-selection', `expected multica, got ${diagnostics.provider || 'unknown'}`);
  if (diagnostics.reachability_state !== 'reachable') block('provider-reachability', diagnostics.error_codes?.join(', ') || 'provider is unreachable');
  record('provider-selection', 'passed');
  record('provider-reachability', 'passed');
  report.evidence.adapter_version = diagnostics.adapter_version || '';
  report.evidence.server_version = diagnostics.server_version || '';
  report.evidence.capabilities = diagnostics.capabilities || [];

  const suffix = Date.now().toString(36);
  const requirement = (await request('POST', '/api/v1/requirements', {
    idempotencyKey: `multica-conformance-${suffix}`,
    expected: [201],
    body: {
      workspace_id: process.env.ADRO_CONFORMANCE_WORKSPACE_ID,
      title: `ADRO real Multica conformance ${suffix}`,
      description: 'Create a traceable change, run tests, and submit it for review.',
      acceptance_criteria: ['A real run has session, workdir, commit, checks, submission, attachment, and repair evidence.'],
      assignee_member_ids: [process.env.ADRO_CONFORMANCE_MEMBER_ID],
      repository_ids: [process.env.ADRO_CONFORMANCE_REPOSITORY_ID]
    }
  })).body;
  report.evidence.requirement_id = assertEvidence('requirement-create', requirement.id, 'ADRO returned no requirement ID');

  await request('POST', `/api/v1/requirements/${requirement.id}/start`, { idempotencyKey: `start-${suffix}` });
  const itemPage = (await request('GET', `/api/v1/requirements/${requirement.id}/work-items`)).body;
  const workItem = itemPage.items?.[0];
  report.evidence.work_item_id = assertEvidence('real-work-item', workItem?.id, 'no materialized work item was returned');
  report.evidence.provider_issue_id = assertEvidence('provider-issue-binding', workItem?.provider_issue_id, 'work item has no real provider issue binding');
  await cancelNativeAssignmentTasks(workItem.provider_issue_id);

  const reportPath = process.env.ADRO_CONFORMANCE_REPORT || '.adro-conformance.json';
  const wsReadyFile = resolve(root, `${reportPath}.ws-ready`);
  try { unlinkSync(wsReadyFile); } catch {}
  const wsProbe = startWebSocketProbe('pending', wsReadyFile);
  activeWebSocketProbe = wsProbe;
  activeWebSocketReadyFile = wsReadyFile;
  wsProbe.child.on('close', () => { wsProbe.closed = true; });
  await waitForWebSocketReady(wsProbe, wsReadyFile);

  const initial = (await request('POST', `/api/v1/work-items/${workItem.id}/run`, {
    idempotencyKey: `run-${suffix}`,
    expected: [202],
    body: { input: 'Add a small conformance marker file, run the repository tests, and submit the change for review.' }
  })).body.run || {};
  const runID = assertEvidence('initial-run-id', initial.provider_run_id || initial.id, 'provider returned no run/task ID');
  const sessionID = assertEvidence('initial-session-id', initial.session_id, 'provider returned no session ID');
  const workDir = assertEvidence('initial-workdir', initial.work_dir, 'provider returned no workdir');
  Object.assign(report.evidence, { initial_run_id: runID, session_id: sessionID, work_dir: workDir });

  let wsResult;
  try { wsResult = await finishWebSocketProbe(wsProbe); }
  catch (error) { block('daemon-websocket', error.message); }
  assertEvidence('daemon-websocket', wsResult.status === 'passed', wsResult.reason || 'daemon WebSocket check failed');
  report.evidence.websocket_event_sha256 = wsResult.event_sha256;

  const snapshot = await waitForRun(runID, 'initial run');
  assertEvidence('code-change', snapshot.head_commit && snapshot.head_commit !== snapshot.baseline_commit, 'run snapshot has no changed head commit');
  assertEvidence('test-conclusion', ['passed', 'success', 'succeeded'].includes(String(snapshot.checks_conclusion).toLowerCase()), 'run snapshot has no passing check conclusion');
  assertEvidence('submission', snapshot.submission_url, 'run snapshot has no review/submission URL');
  report.evidence.initial_head_commit = snapshot.head_commit;
  report.evidence.initial_submission_url = snapshot.submission_url;

  const form = new FormData();
  form.set('target_type', 'issue');
  form.set('target_id', workItem.provider_issue_id);
  form.set('file', new Blob([Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')], { type: 'image/png' }), 'conformance.png');
  const attachment = (await request('POST', '/api/v1/screenshots', { form, idempotencyKey: `attachment-${suffix}`, expected: [201] })).body;
  assertEvidence('provider-attachment', attachment.delivery === 'delivered' && attachment.provider_receipt?.provider_attachment_id, `attachment delivery=${attachment.delivery || 'unknown'}`);
  report.evidence.provider_attachment_id = attachment.provider_receipt.provider_attachment_id;

  const bug = (await request('POST', '/api/v1/bugs', {
    idempotencyKey: `bug-${suffix}`,
    expected: [201],
    body: {
      workspace_id: process.env.ADRO_CONFORMANCE_WORKSPACE_ID,
      requirement_id: requirement.id,
      work_item_id: workItem.id,
      repository_id: process.env.ADRO_CONFORMANCE_REPOSITORY_ID,
      assignee_member_id: process.env.ADRO_CONFORMANCE_MEMBER_ID,
      title: `Conformance repair ${suffix}`,
      steps_to_reproduce: 'Run the conformance repair marker check.',
      expected: 'The marker is repaired and tests pass.',
      actual: 'The marker requires one real repair run.'
    }
  })).body;
  report.evidence.bug_id = assertEvidence('repair-bug-create', bug.id, 'ADRO returned no bug ID');
  const repaired = (await request('POST', `/api/v1/bugs/${bug.id}/repair`, { idempotencyKey: `repair-${suffix}`, expected: [202] })).body;
  const repairRun = repaired.run || {};
  const repairRunID = assertEvidence('repair-run-id', repairRun.provider_run_id || repairRun.id, 'repair returned no run/task ID');
  if (repairRun.session_id !== sessionID || repaired.session_reused !== true) block('same-session-repair', 'repair did not reuse the original session');
  if (repairRun.work_dir !== workDir) block('same-workdir-repair', 'repair did not reuse the original workdir');
  record('same-session-repair', 'passed');
  record('same-workdir-repair', 'passed');
  report.evidence.repair_run_id = repairRunID;

  const repairSnapshot = await waitForRun(repairRunID, 'repair run');
  assertEvidence('repair-test-conclusion', ['passed', 'success', 'succeeded'].includes(String(repairSnapshot.checks_conclusion).toLowerCase()), 'repair has no passing check conclusion');
  assertEvidence('repair-submission', repairSnapshot.submission_url, 'repair has no second submission URL');
  const attempts = (await request('GET', `/api/v1/work-items/${workItem.id}/repair-attempts`)).body.items || [];
  const attempt = attempts.find(item => item.provider_task_id === repairRunID);
  assertEvidence('repair-provenance', attempt?.provider_session_id === sessionID && attempt?.provider_work_dir === workDir, 'repair attempt lacks matching session/workdir provenance');

  report.status = 'passed';
}

try {
  await main();
} catch (error) {
  report.status = error instanceof Blocked ? 'blocked' : 'failed';
  report.reason = error.message;
  if (!(error instanceof Blocked) && !(error instanceof Failed)) report.reason = `unexpected conformance error: ${error.message}`;
  process.exitCode = error instanceof Blocked ? 2 : 1;
} finally {
  if (activeWebSocketProbe && !activeWebSocketProbe.closed) activeWebSocketProbe.child.kill('SIGTERM');
  if (activeWebSocketReadyFile) { try { unlinkSync(activeWebSocketReadyFile); } catch {} }
  writeReport();
}
