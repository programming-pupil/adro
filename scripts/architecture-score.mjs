#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function fail(message) {
  process.stderr.write(`architecture score: ${message}\n`);
  process.exit(1);
}

function option(name, fallback = '') {
  const index = process.argv.indexOf(name);
  return index >= 0 && process.argv[index + 1] ? process.argv[index + 1] : fallback;
}

function run(cwd, program, args, allowNoMatch = false) {
  const result = spawnSync(program, args, { cwd, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  if (result.status === 0 || allowNoMatch && result.status === 1) return result.stdout || '';
  fail(`${program} ${args.join(' ')} failed in ${cwd}: ${(result.stderr || result.error?.message || '').trim()}`);
}

function git(cwd, args) {
  return run(cwd, 'git', args).trim();
}

const rulesPath = resolve(root, option('--rules', 'release/architecture-score-rules.json'));
const outputDir = resolve(root, option('--output', 'var/test-report/architecture-score'));
const rulesRaw = readFileSync(rulesPath);
const rules = JSON.parse(rulesRaw);
if (!Array.isArray(rules.dimensions) || rules.dimensions.length !== 15) fail('rules must define exactly 15 dimensions');

const project = { name: 'ADRO', path: root, ref: 'WORKTREE' };
project.commit = git(project.path, ['rev-parse', 'HEAD']);
project.branch = git(project.path, ['symbolic-ref', '--short', 'HEAD']);
project.dirty = git(project.path, ['status', '--porcelain']) !== '';

const pathCategories = {
  test: /(^|\/)(test|tests|e2e)(\/|$)|_test[.]|[.]spec[.]|[.]test[.]/i,
  evidence: /(^|\/)(docs\/evidence|evidence|var\/test-report)(\/|$)/i,
  release: /(^|\/)(scripts|release|[.]github\/workflows|docs\/operations|deploy|charts)(\/|$)|(^|\/)makefile$/i
};

function categoryMatches(path, category) {
  const test = pathCategories.test.test(path);
  const evidence = pathCategories.evidence.test(path);
  const release = pathCategories.release.test(path);
  if (category === 'test') return test;
  if (category === 'evidence') return evidence;
  if (category === 'release') return release;
  return !test && !evidence && !release;
}

function parseMatch(line, ref) {
  let normalized = line;
  if (ref !== 'WORKTREE' && normalized.startsWith(`${ref}:`)) normalized = normalized.slice(ref.length + 1);
  const match = normalized.match(/^(.+?):(\d+):(.*)$/);
  if (!match) return null;
  return { path: match[1], line: Number(match[2]), excerpt: match[3].trim().slice(0, 240) };
}

function evidenceFor(project, check) {
  const raw = project.ref === 'WORKTREE'
    ? run(project.path, 'rg', ['-n', '-i', '--no-heading', '--color', 'never', '--max-count', '8', check.pattern, '.'], true)
    : run(project.path, 'git', ['grep', '-n', '-I', '-i', '-E', check.pattern, project.ref, '--'], true);
  const unique = [];
  const seen = new Set();
  for (const line of raw.split('\n')) {
    const item = parseMatch(line, project.ref);
    if (!item || !categoryMatches(item.path, check.category)) continue;
    const key = `${item.path}:${item.line}`;
    if (seen.has(key)) continue;
    seen.add(key);
    unique.push(item);
    if (unique.length === 3) break;
  }
  return unique;
}

const dimensions = rules.dimensions.map(dimension => {
  const checks = dimension.checks.map(check => {
    const evidence = evidenceFor(project, check);
    return { ...check, matched: evidence.length > 0, evidence };
  });
  const matched = checks.filter(check => check.matched).length;
  return { id: dimension.id, name: dimension.name, kernel: Boolean(dimension.kernel), score: Number((matched / checks.length * 10).toFixed(2)), matched_checks: matched, total_checks: checks.length, checks };
});
const overall = Number((dimensions.reduce((sum, dimension) => sum + dimension.score, 0) / dimensions.length).toFixed(2));
const adro = { ...project, overall, dimensions };
const incompleteDimensions = dimensions.filter(item => item.matched_checks !== item.total_checks).map(item => item.id);
const gate = { all_dimensions_complete: incompleteDimensions.length === 0, incomplete_dimensions: incompleteDimensions };
gate.passed = gate.all_dimensions_complete;

const report = {
  schema_version: 1,
  generated_at: new Date().toISOString(),
  rules: { path: rulesPath.slice(root.length + 1), sha256: createHash('sha256').update(rulesRaw).digest('hex'), method: rules.method },
  project: adro,
  gate
};
mkdirSync(outputDir, { recursive: true });
writeFileSync(join(outputDir, 'architecture-score.json'), `${JSON.stringify(report, null, 2)}\n`);

const header = '| Dimension | Kernel | Score | Matched |\n| --- | :---: | ---: | ---: |';
const rows = rules.dimensions.map(dimension => {
  const a = adro.dimensions.find(item => item.id === dimension.id).score;
  const matched = adro.dimensions.find(item => item.id === dimension.id).matched_checks;
  return `| ${dimension.name} | ${dimension.kernel ? 'yes' : 'no'} | ${a.toFixed(2)} | ${matched}/5 |`;
}).join('\n');
const gaps = adro.dimensions.flatMap(dimension => dimension.checks.filter(check => !check.matched).map(check => `- ${dimension.name}: missing ${check.id} (${check.category}, \`${check.pattern}\`)`));
writeFileSync(join(outputDir, 'architecture-score.md'), `# ADRO architecture completeness score\n\n- Commit: \`${project.commit}\` (branch \`${project.branch}\`, dirty \`${project.dirty}\`)\n- Rules SHA-256: \`${report.rules.sha256}\`\n- Gate: **${gate.passed ? 'passed' : 'failed'}**\n- Overall: **${adro.overall.toFixed(2)}**\n\n${header}\n${rows}\n\n## Unmatched checks\n\n${gaps.length ? gaps.join('\n') : '- None.'}\n`);

process.stdout.write(JSON.stringify({ gate, total: adro.overall, output: outputDir }) + '\n');
if (!gate.passed) process.exit(1);
