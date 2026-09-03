#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(process.env.ADRO_FAULT_REPORT_DIR || join(root, 'var/test-report/fault-matrix'));
const go = resolve(process.env.ADRO_GO_BIN || join(root, 'scripts/e2e-go.sh'));

const cases = [
  ['executor_kill_recovery', './internal/provider', 'TestLocalProviderPersistsRunSnapshotAndMarksInterruptedRun'],
  ['missing_thread_started', './internal/provider', 'TestLocalProviderRejectsCodexContinuationWithoutThreadProof'],
  ['journal_torn_tail', './internal/harness', 'TestJournalRecoversWhenSnapshotWasTorn'],
  ['journal_fsync_failure', './internal/events', 'TestPersistentBusFaultBeforeRenameDoesNotExposeEvent'],
  ['duplicate_outbox_delivery', './internal/orchestration', 'TestOutboxDispatcherRetriesAndTakesOverExpiredLease'],
  ['event_gap_out_of_order', './internal/events', 'TestPersistentBusRejectsSequenceGap'],
  ['lease_takeover', './internal/orchestration', 'TestHumanTakeoverFencesOldWorkerAndRoutesTimeoutEdge'],
  ['context_overflow', './internal/context', 'TestManifestOverflow'],
  ['approval_denial', './internal/runtime', 'TestToolLoopRequiresApprovalAndFailsClosedOnDenial'],
  ['attachment_tamper', './internal/artifact', 'TestFileStoreFailsClosedOnContentAndMetadataTampering'],
  ['comment_edit_race', './internal/store', 'TestConcurrentCommentEditAllowsSingleRevisionWinner'],
  ['database_unavailable', './internal/store', 'TestPersistentMutationRollsBackWhenStateCannotBeReplaced']
];

function command(program, args) {
  const result = spawnSync(program, args, { cwd: root, encoding: 'utf8', env: process.env });
  return { status: result.status ?? 1, stdout: result.stdout || '', stderr: result.stderr || '', error: result.error?.message || '' };
}

function text(program, args) {
  const result = command(program, args);
  return result.status === 0 ? result.stdout.trim() : `unavailable: ${result.stderr.trim() || result.error}`;
}

mkdirSync(output, { recursive: true });
const results = [];
let failed = false;
for (const [name, pkg, test] of cases) {
  const args = ['test', pkg, '-run', `^${test}$`, '-count=1', '-v'];
  const started = Date.now();
  const result = command(go, args);
  const duration = Date.now() - started;
  const log = `${result.stdout}${result.stderr}${result.error ? `${result.error}\n` : ''}`;
  const logPath = join(output, `${name}.log`);
  writeFileSync(logPath, log);
  const status = result.status === 0 ? 'passed' : 'failed';
  if (result.status !== 0) failed = true;
  results.push({
    name,
    status,
    exit_code: result.status,
    duration_ms: duration,
    command: `${go} ${args.join(' ')}`,
    test,
    log_sha256: createHash('sha256').update(log).digest('hex'),
    log: `${name}.log`
  });
  process.stdout.write(`[fault-matrix] ${name}: ${status} (${duration} ms)\n`);
}

const report = {
  schema_version: 1,
  generated_at: new Date().toISOString(),
  source: {
    branch: text('git', ['symbolic-ref', '--short', 'HEAD']),
    commit: text('git', ['rev-parse', 'HEAD']),
    dirty: command('git', ['status', '--porcelain']).stdout.trim() !== ''
  },
  toolchain: { go: text(go, ['version']), node: process.version },
  result: failed ? 'failed' : 'passed',
  cases: results
};
writeFileSync(join(output, 'fault-matrix.json'), `${JSON.stringify(report, null, 2)}\n`);
const rows = results.map(item => `| ${item.name} | ${item.test} | ${item.status} | ${item.duration_ms} ms | \`${item.log_sha256}\` |`).join('\n');
writeFileSync(join(output, 'fault-matrix.md'), `# ADRO fault-injection matrix\n\n- Result: **${report.result}**\n- Commit: \`${report.source.commit}\`\n- Working tree dirty: \`${report.source.dirty}\`\n- Go: \`${report.toolchain.go}\`\n- Node: \`${report.toolchain.node}\`\n\n| Fault | Executable assertion | Result | Duration | Log SHA-256 |\n| --- | --- | --- | ---: | --- |\n${rows}\n`);

if (failed) process.exit(1);
