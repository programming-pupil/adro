#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const command = process.argv[2] || 'verify';
const registry = JSON.parse(readFileSync(join(root, 'release/dependencies.json'), 'utf8'));
const packageJSON = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
const lock = JSON.parse(readFileSync(join(root, 'package-lock.json'), 'utf8'));

function fail(message) {
  process.stderr.write(`release assets: ${message}\n`);
  process.exit(1);
}

function run(program, args, options = {}) {
  try {
    return execFileSync(program, args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], ...options }).trim();
  } catch (error) {
    const detail = error.stderr?.toString().trim() || error.message;
    fail(`${program} ${args.join(' ')} failed: ${detail}`);
  }
}

function sha256(data) {
  return createHash('sha256').update(data).digest('hex');
}

function dependencyKey(item) {
  return `${item.ecosystem}:${item.name}@${item.version}`;
}

function actualDependencies() {
  const modules = run('go', ['list', '-m', '-f', '{{if not .Main}}{{.Path}}|{{.Version}}{{end}}', 'all'])
    .split('\n')
    .filter(Boolean)
    .map(line => {
      const [name, version] = line.split('|');
      return { ecosystem: 'go', name, version };
    });
  const packages = Object.entries(lock.packages)
    .filter(([path]) => path.startsWith('node_modules/'))
    .map(([path, metadata]) => ({ ecosystem: 'npm', name: path.slice('node_modules/'.length), version: metadata.version }));
  return [...modules, ...packages].sort((a, b) => dependencyKey(a).localeCompare(dependencyKey(b)));
}

function declaredDependencies() {
  return Object.entries(registry)
    .flatMap(([ecosystem, items]) => items.map(item => ({ ecosystem, ...item })))
    .sort((a, b) => dependencyKey(a).localeCompare(dependencyKey(b)));
}

function validateRegistry() {
  const actual = actualDependencies();
  const declared = declaredDependencies();
  const actualKeys = actual.map(dependencyKey);
  const declaredKeys = declared.map(dependencyKey);
  if (JSON.stringify(actualKeys) !== JSON.stringify(declaredKeys)) {
    fail(`dependency registry does not match manifests\nactual: ${actualKeys.join(', ')}\ndeclared: ${declaredKeys.join(', ')}`);
  }
  for (const item of declared) {
    if (!item.license || !item.source || !item.supplier || !item.scope) {
      fail(`${dependencyKey(item)} is missing license, source, supplier, or scope`);
    }
  }
  return declared;
}

function spdxID(item) {
  return `SPDXRef-${item.ecosystem}-${item.name}-${item.version}`.replace(/[^A-Za-z0-9.-]/g, '-');
}

function purl(item) {
  const name = item.ecosystem === 'npm' ? item.name.replace('@', '%40') : item.name;
  const type = item.ecosystem === 'go' ? 'golang' : 'npm';
  return `pkg:${type}/${name}@${item.version}`;
}

function licenseSource(item) {
  if (item.ecosystem === 'npm') {
    const installed = join(root, 'node_modules', item.name, 'LICENSE');
    if (existsSync(installed)) return installed;

    // Optional packages (for example platform-specific npm packages) may be
    // omitted by npm ci on the current runner. The checked-in copy remains
    // the canonical source so release verification is platform-independent.
    return join(root, licenseTarget(item));
  }
  const moduleDir = run('go', ['list', '-m', '-f', '{{.Dir}}', `${item.name}@${item.version}`]);
  return join(moduleDir, 'LICENSE');
}

function licenseTarget(item) {
  const safe = `${item.ecosystem}-${item.name}@${item.version}`.replace(/[^A-Za-z0-9._@-]/g, '_');
  return join('THIRD_PARTY_LICENSES', `${safe}.txt`);
}

function generatedFiles(outputRoot) {
  const dependencies = validateRegistry();
  const manifestHash = sha256([
    readFileSync(join(root, 'go.mod')),
    readFileSync(join(root, 'go.sum')),
    readFileSync(join(root, 'package-lock.json')),
    readFileSync(join(root, 'release/dependencies.json'))
  ].map(value => sha256(value)).join('\n'));
  const rootID = 'SPDXRef-Package-ADRO';
  const packages = dependencies.map(item => ({
    SPDXID: spdxID(item),
    name: item.name,
    versionInfo: item.version,
    downloadLocation: item.source,
    filesAnalyzed: false,
    licenseConcluded: item.license,
    licenseDeclared: item.license,
    copyrightText: 'NOASSERTION',
    supplier: `Organization: ${item.supplier}`,
    externalRefs: [{ referenceCategory: 'PACKAGE-MANAGER', referenceType: 'purl', referenceLocator: purl(item) }]
  }));
  const sbom = {
    spdxVersion: 'SPDX-2.3',
    dataLicense: 'CC0-1.0',
    SPDXID: 'SPDXRef-DOCUMENT',
    name: `adro-${packageJSON.version}`,
    documentNamespace: `https://adro.dev/spdx/adro-${packageJSON.version}-${manifestHash}`,
    creationInfo: { created: '1970-01-01T00:00:00Z', creators: ['Tool: scripts/release-assets.mjs'] },
    documentDescribes: [rootID],
    packages: [{
      SPDXID: rootID,
      name: 'adro',
      versionInfo: packageJSON.version,
      downloadLocation: 'https://github.com/programming-pupil/adro',
      filesAnalyzed: false,
      licenseConcluded: 'Apache-2.0',
      licenseDeclared: 'Apache-2.0',
      copyrightText: 'Copyright 2026 ADRO contributors',
      supplier: 'Organization: ADRO contributors'
    }, ...packages],
    relationships: dependencies.map(item => item.scope === 'runtime' ? {
      spdxElementId: rootID,
      relationshipType: 'DEPENDS_ON',
      relatedSpdxElement: spdxID(item)
    } : {
      spdxElementId: spdxID(item),
      relationshipType: 'DEV_DEPENDENCY_OF',
      relatedSpdxElement: rootID
    })
  };
  const notices = [
    'ADRO Third-Party Notices',
    `Generated from go.mod, go.sum, package-lock.json, and release/dependencies.json for ADRO ${packageJSON.version}.`,
    'The release verifier fails when a manifest dependency is absent from this list.',
    ''
  ];
  const files = new Map();
  files.set('SBOM', `${JSON.stringify(sbom, null, 2)}\n`);
  for (const item of dependencies) {
    const source = licenseSource(item);
    let license;
    try {
      license = readFileSync(source);
    } catch (error) {
      fail(`license text is unavailable for ${dependencyKey(item)} at ${source}: ${error.message}`);
    }
    const target = licenseTarget(item);
    files.set(target, license.toString().endsWith('\n') ? license : Buffer.concat([license, Buffer.from('\n')]).toString());
    notices.push(`${item.name} ${item.version}`);
    notices.push(`  ecosystem: ${item.ecosystem}`);
    notices.push(`  scope: ${item.scope}`);
    notices.push(`  license: ${item.license}`);
    notices.push(`  source: ${item.source}`);
    notices.push(`  license-file: ${target}`);
    notices.push(`  license-sha256: ${sha256(license)}`);
    notices.push('');
  }
  files.set('THIRD_PARTY_NOTICES', `${notices.join('\n').trimEnd()}\n`);
  for (const [path, content] of files) {
    const destination = join(outputRoot, path);
    mkdirSync(dirname(destination), { recursive: true });
    writeFileSync(destination, content);
  }
  return files;
}

function verifyGenerated() {
  const temp = mkdtempSync(join(tmpdir(), 'adro-release-'));
  try {
    const expected = generatedFiles(temp);
    for (const path of expected.keys()) {
      const generated = readFileSync(join(temp, path));
      let checkedIn;
      try {
        checkedIn = readFileSync(join(root, path));
      } catch (error) {
        fail(`${path} is missing; run scripts/release-assets.mjs generate`);
      }
      if (!generated.equals(checkedIn)) {
        fail(`${path} is stale; run scripts/release-assets.mjs generate`);
      }
    }
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
  process.stdout.write('SPDX SBOM, notices, and license texts match dependency manifests\n');
}

function git(args) {
  return run('git', args);
}

function artifactRecord(path) {
  const absolute = resolve(root, path);
  const data = readFileSync(absolute);
  return { path: relative(root, absolute), sha256: sha256(data), size_bytes: statSync(absolute).size };
}

function createManifest(output) {
  const commit = git(['rev-parse', '--verify', 'HEAD']);
  const tag = git(['describe', '--tags', '--exact-match', 'HEAD']);
  const expectedTag = `v${packageJSON.version}`;
  if (tag !== expectedTag) fail(`release tag ${tag} does not match package version ${expectedTag}`);
  if (git(['status', '--porcelain', '--untracked-files=no']) !== '') fail('tracked worktree changes prevent a traceable release manifest');
  verifyGenerated();
  const paths = ['SBOM', 'THIRD_PARTY_NOTICES', 'openapi/openapi.yaml', 'go.sum', 'package-lock.json'];
  const manifest = {
    schema_version: 1,
    product: 'ADRO',
    version: packageJSON.version,
    tag,
    commit,
    source_tree: git(['rev-parse', 'HEAD^{tree}']),
    committed_at: git(['show', '-s', '--format=%cI', 'HEAD']),
    builder: { go: run('go', ['version']), node: process.version },
    artifacts: paths.map(artifactRecord)
  };
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`);
  process.stdout.write(`release manifest written to ${output}\n`);
}

function verifyManifest(path) {
  const manifest = JSON.parse(readFileSync(path, 'utf8'));
  const checks = [
    ['version', packageJSON.version],
    ['tag', git(['describe', '--tags', '--exact-match', 'HEAD'])],
    ['commit', git(['rev-parse', '--verify', 'HEAD'])],
    ['source_tree', git(['rev-parse', 'HEAD^{tree}'])]
  ];
  for (const [field, expected] of checks) {
    if (manifest[field] !== expected) fail(`manifest ${field}=${manifest[field]} does not match ${expected}`);
  }
  for (const artifact of manifest.artifacts || []) {
    const actual = artifactRecord(artifact.path);
    if (actual.sha256 !== artifact.sha256 || actual.size_bytes !== artifact.size_bytes) fail(`manifest artifact mismatch: ${artifact.path}`);
  }
  process.stdout.write(`release manifest ${path} is traceable to ${manifest.commit}\n`);
}

switch (command) {
  case 'generate':
    generatedFiles(root);
    process.stdout.write('generated SPDX SBOM, notices, and third-party license texts\n');
    break;
  case 'verify':
    verifyGenerated();
    break;
  case 'manifest': {
    const outputIndex = process.argv.indexOf('--output');
    if (outputIndex < 0 || !process.argv[outputIndex + 1]) fail('manifest requires --output <path>');
    createManifest(resolve(root, process.argv[outputIndex + 1]));
    break;
  }
  case 'verify-manifest':
    if (!process.argv[3]) fail('verify-manifest requires a manifest path');
    verifyManifest(resolve(root, process.argv[3]));
    break;
  default:
    fail(`unknown command ${command}`);
}
