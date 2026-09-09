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
