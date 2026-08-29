#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(new URL('..', import.meta.url).pathname);
const path = join(root, 'apps/web/index.html');
const html = readFileSync(path, 'utf8');
if (!/^<!doctype html>/i.test(html) || !/<html\s+lang="[^"]+"/i.test(html) || !/<meta\s+name="viewport"/i.test(html)) {
  throw new Error('index.html lacks doctype, language, or viewport metadata');
}
const ids = [...html.matchAll(/\sid="([^"]+)"/g)].map(match => match[1]);
const duplicateIDs = ids.filter((id, index) => ids.indexOf(id) !== index);
if (duplicateIDs.length) throw new Error(`duplicate HTML ids: ${[...new Set(duplicateIDs)].join(', ')}`);
const scripts = [...html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/gi)].map(match => match[1]);
if (!scripts.length) throw new Error('index.html contains no inline application script');
const temporary = mkdtempSync(join(tmpdir(), 'adro-html-'));
try {
  scripts.forEach((source, index) => {
    const target = join(temporary, `inline-${index}.js`);
    writeFileSync(target, source);
    execFileSync(process.execPath, ['--check', target], { stdio: 'inherit' });
  });
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
process.stdout.write(`HTML structure and ${scripts.length} inline script block(s) are valid\n`);
