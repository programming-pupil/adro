#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';

const root = resolve(new URL('..', import.meta.url).pathname);
const reportRoot = resolve(process.env.ADRO_REAL_EVIDENCE_DIR || join(root, 'var/test-report/real-codex'));

function fail(message) {
  process.stderr.write(`real evidence: ${message}\n`);
  process.exit(1);
}

function readJSON(path) {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (error) {
    fail(`invalid JSON in ${path}: ${error.message}`);
  }
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function assertion(condition, message) {
  if (!condition) fail(message);
}

assertion(existsSync(reportRoot), `report directory does not exist: ${reportRoot}`);
const currentSha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const manifests = readdirSync(reportRoot, { withFileTypes: true })
  .filter(entry => entry.isDirectory())
  .filter(entry => /^[0-9]{8}T[0-9]{6}Z-[0-9]+$/.test(entry.name))
  .map(entry => {
    const path = join(reportRoot, entry.name, 'manifest.json');
    return existsSync(path) ? { runId: entry.name, dir: join(reportRoot, entry.name), path, data: readJSON(path) } : null;
  })
  .filter(Boolean)
  .sort((left, right) => left.runId.localeCompare(right.runId));

const suites = [
  { name: 'browser_graph_real', marker: 'scripts/browser-graph-real-e2e.sh' },
  { name: 'comment_handoff_real', marker: 'scripts/comment-handoff-real-e2e.sh' },
  { name: 'chat_compose_real', marker: 'scripts/chat-compose-real-e2e.sh' },
  { name: 'release_system_real', marker: 'scripts/release-system-e2e.sh' },
  { name: 'pipeline_real', marker: 'scripts/real-pipeline-e2e.sh' },
  { name: 'graph_real', marker: 'scripts/real-graph-orchestration-e2e.sh' },
];

function latestSuite(suite) {
  const matches = manifests.filter(item => String(item.data.command || '').includes(suite.marker));
  assertion(matches.length > 0, `${suite.name} has no manifest for ${suite.marker}`);
  return matches[matches.length - 1];
}

function verifyBase(suite, item) {
  const manifest = item.data;
  assertion(manifest.commit_sha === currentSha, `${suite.name} manifest ${item.runId} is for ${manifest.commit_sha || '<missing sha>'}, expected ${currentSha}`);
  assertion(manifest.command && manifest.command.includes('ADRO_REQUIRE_CODEX=1'), `${suite.name} manifest ${item.runId} was not run with ADRO_REQUIRE_CODEX=1`);
  assertion(/(^|[\\/])codex(?:\.exe)?$/.test(String(manifest.codex_command || '')), `${suite.name} manifest ${item.runId} does not identify a real Codex executable`);
  assertion(/^codex-cli\s+\S+/.test(String(manifest.codex_version || '')), `${suite.name} manifest ${item.runId} has no codex-cli version`);
  assertion(manifest.status === 'passed' || manifest.status === 'pass', `${suite.name} latest manifest ${item.runId} is ${manifest.status || '<missing status>'}`);
  assertion(Number(manifest.exit_status) === 0, `${suite.name} latest manifest ${item.runId} has exit_status=${manifest.exit_status}`);
  assertion(Array.isArray(manifest.evidence_files) && manifest.evidence_files.length > 0, `${suite.name} manifest ${item.runId} has no evidence file list`);
  for (const file of manifest.evidence_files) {
    const evidencePath = join(item.dir, file.path);
    assertion(/^[a-zA-Z0-9._/-]+$/.test(file.path), `${suite.name} manifest contains an unsafe evidence path: ${file.path}`);
    assertion(existsSync(evidencePath), `${suite.name} evidence file is missing: ${file.path}`);
    assertion(/^[a-f0-9]{64}$/.test(String(file.sha256 || '')), `${suite.name} evidence hash is invalid: ${file.path}`);
    assertion(sha256(evidencePath) === file.sha256, `${suite.name} evidence hash mismatch: ${file.path}`);
  }
}

const selected = {};
for (const suite of suites) {
  selected[suite.name] = latestSuite(suite);
  verifyBase(suite, selected[suite.name]);
}
const dshMatches = manifests.filter(item => String(item.data.command || '').includes('scripts/dsh-real-e2e.sh'));
assertion(dshMatches.length > 0, 'dsh_real has no manifest for scripts/dsh-real-e2e.sh');
const dsh = dshMatches[dshMatches.length - 1];
assertion(dsh.data.commit_sha === currentSha, `dsh_real manifest ${dsh.runId} is for ${dsh.data.commit_sha || '<missing sha>'}, expected ${currentSha}`);
assertion(dsh.data.status === 'passed' || dsh.data.status === 'pass', `dsh_real latest manifest ${dsh.runId} is ${dsh.data.status || '<missing status>'}`);
assertion(Number(dsh.data.exit_status) === 0, `dsh_real latest manifest ${dsh.runId} has exit_status=${dsh.data.exit_status}`);
assertion(dsh.data.runtime === 'dsh', 'dsh_real did not identify the DSH runtime');
assertion(String(dsh.data.model || '') === 'deepseek-official/deepseek-v4-flash', 'dsh_real did not use DeepSeek V4 Flash');
assertion(String(dsh.data.session_id || '').length > 0 && dsh.data.session_reused === true, 'dsh_real did not prove native session reuse');
for (const label of ['first', 'second']) {
  const run = dsh.data[label] || {};
  assertion(run.status === 'completed', `dsh_real ${label} run is ${run.status || '<missing>'}`);
  assertion(Number(run.executor_pid) > 0, `dsh_real ${label} run is missing executor pid`);
  assertion(String(run.work_dir || '').length > 0, `dsh_real ${label} run is missing workdir`);
  assertion(String(run.session_id || '') === String(dsh.data.session_id), `dsh_real ${label} session mismatch`);
  assertion(String(run.event_cursor || '').length > 0, `dsh_real ${label} is missing event cursor`);
  assertion(String(run.stdout_sha256 || '').length === 64 && String(run.stderr_sha256 || '').length === 64, `dsh_real ${label} is missing stream hashes`);
}
assertion(/^[a-f0-9]{64}$/.test(String(dsh.data.artifact_sha256 || '')), 'dsh_real is missing artifact hash');
assertion(dsh.data.secret_redaction?.api_keys_written === false, 'dsh_real may have written an API key');

const browser = selected.browser_graph_real.data;
for (const field of ['plan_id', 'requirement_id', 'work_item_id', 'agent_id', 'timeline_hash', 'replay_hash']) {
  assertion(String(browser[field] || '').length > 0, `browser_graph_real is missing ${field}`);
}
assertion(browser.terminal_outcome === 'succeeded', 'browser_graph_real did not reach succeeded terminal_outcome');
assertion(Number(browser.event_cursor) > 0, 'browser_graph_real did not record an event cursor');

const comment = selected.comment_handoff_real.data;
assertion(String(comment.requirement_id || '').length > 0, 'comment_handoff_real is missing requirement_id');
assertion(Array.isArray(comment.comment_ids) && comment.comment_ids.length === 3, 'comment_handoff_real did not record the three comment IDs');
assertion(String(comment.root_id || '').length > 0, 'comment_handoff_real is missing root_id');
assertion(Array.isArray(comment.follow_up_statuses) && comment.follow_up_statuses.length === 3 && comment.follow_up_statuses.every(status => status === 'completed'), 'comment_handoff_real does not show three completed follow-ups');
assertion(Array.isArray(comment.session_ids) && comment.session_ids.length === 1, 'comment_handoff_real did not preserve one provider session');
assertion(Array.isArray(comment.run_ids) && comment.run_ids.length >= 3, 'comment_handoff_real did not record provider run IDs');

const chatCompose = selected.chat_compose_real.data;
assertion(String(chatCompose.chat_id || '').length > 0, 'chat_compose_real is missing chat_id');
assertion(String(chatCompose.compose_run_id || '').length > 0, 'chat_compose_real is missing compose_run_id');
assertion(String(chatCompose.agent_id || '').length > 0, 'chat_compose_real is missing created agent_id');
assertion(chatCompose.secret_redaction?.api_keys_written === false, 'chat_compose_real may have written an API key');
for (const label of ['first', 'second', 'switched', 'restart']) {
  const run = chatCompose.chat_runs?.[label] || {};
  assertion(String(run.id || '').length > 0, `chat_compose_real is missing ${label} run id`);
  assertion(run.status === 'completed', `chat_compose_real ${label} run is ${run.status || '<missing>'}`);
  assertion(String(run.session_id || '').length > 0, `chat_compose_real ${label} run is missing session identity`);
  assertion(String(run.work_dir || '').length > 0, `chat_compose_real ${label} run is missing workdir`);
  assertion(Number(run.executor_pid) > 0, `chat_compose_real ${label} run is missing executor pid`);
  assertion(String(run.output_sha256 || '').length === 64, `chat_compose_real ${label} run is missing output hash`);
  assertion(Number(chatCompose.event_cursors?.[label]?.count) > 0, `chat_compose_real ${label} run is missing event cursor`);
}
const secondChat = chatCompose.chat_runs.second || {};
assertion(secondChat.session_continuity === 'proven', 'chat_compose_real native follow-up did not prove continuity');
const switchedChat = chatCompose.chat_runs.switched || {};
assertion(switchedChat.session_id !== secondChat.session_id, 'chat_compose_real binding switch reused the old native session');

const release = selected.release_system_real.data;
assertion(Array.isArray(release.workspace_ids) && release.workspace_ids.length === 2, 'release_system_real did not cover two workspaces');
assertion(String(release.chat_id || '').length > 0, 'release_system_real is missing chat_id');
assertion(Array.isArray(release.requirement_ids) && release.requirement_ids.length === 2, 'release_system_real did not record two requirements');
assertion(String(release.template_id || '').length > 0, 'release_system_real is missing workflow template evidence');

const pipeline = selected.pipeline_real.data;
for (const field of ['pipeline_id', 'requirement_id', 'bug_id']) {
  assertion(String(pipeline[field] || '').length > 0, `pipeline_real is missing ${field}`);
}

const graph = selected.graph_real.data;
for (const field of ['plan_id', 'graph_id', 'requirement_id', 'session_id', 'work_item_id', 'projection_hash', 'replay_projection_hash', 'artifact_hash', 'timeline_hash']) {
  assertion(String(graph[field] || '').length > 0, `graph_real is missing ${field}`);
}
assertion(Number(graph.event_cursor) > 0, 'graph_real did not record an event cursor');
assertion(graph.replay_match === true || graph.replay_match === 'true', 'graph_real replay projection did not match');
assertion(graph.assertions?.terminal_outcome === 'succeeded', 'graph_real did not reach succeeded terminal outcome');
assertion(Number(graph.assertions?.attempt_count) > 0, 'graph_real did not record attempts');

process.stdout.write(JSON.stringify({
  status: 'passed',
  source_sha: currentSha,
  suites: Object.fromEntries(Object.entries(selected).map(([name, item]) => [name, item.runId])),
  real_codex_only: true,
}) + '\n');
