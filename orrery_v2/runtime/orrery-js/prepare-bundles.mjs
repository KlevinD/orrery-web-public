// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
// Maintainer tool: input directories must come from SRI-verified locked archives.
import { createRequire } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { replacement, sha256, transform } from './bundles.mjs';

const base = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.resolve(base, '../../web/package.json'));
const { parse } = require('acorn');
const files = ['dist/shared/rollup.js', 'dist/es/shared/node-entry.js', 'dist/shared/parseAst.js', 'dist/es/shared/parseAst.js'];
const records = [];
let svelteCompilerSha256;
for (const directory of process.argv.slice(2)) {
  const manifest = JSON.parse(readFileSync(path.join(directory, 'package.json'), 'utf8'));
  if (manifest.name === 'svelte' && manifest.version === '5.57.3') {
    svelteCompilerSha256 = sha256(readFileSync(path.join(directory, 'compiler/index.js')));
    continue;
  }
  if (manifest.name !== 'rollup') throw new Error('Expected a verified Rollup archive');
  for (const file of files) {
    const input = readFileSync(path.join(directory, file), 'utf8');
    const ast = parse(input, { ecmaVersion: 'latest', sourceType: 'module' });
    // The other getLocator in node-entry/rollup belongs to magic-string and
    // has a different (one-based) contract. It is not locate-character.
    const names = new Set(file.endsWith('parseAst.js') ? ['rangeContains', 'getLocator', 'locate'] : ['is_reference']);
    const ranges = ast.body.filter(node => node.type === 'FunctionDeclaration' && names.has(node.id.name))
      .map(node => ({ name: node.id.name, start: node.start, end: node.end, sha256: sha256(input.slice(node.start, node.end)) }));
    if (ranges.length !== names.size) throw new Error(`Missing or duplicated embedded helper: ${file}`);
    const output = transform(input, ranges);
    parse(output, { ecmaVersion: 'latest', sourceType: 'module' });
    records.push({ package: manifest.name, version: manifest.version, file, inputSha256: sha256(input), outputSha256: sha256(output), ranges });
  }
}
const helpers = Object.fromEntries(['rangeContains', 'getLocator', 'locate', 'is_reference'].map(name => [name, sha256(replacement(name))]));
if (!svelteCompilerSha256) throw new Error('Include the verified Svelte 5.57.3 archive');
writeFileSync(path.join(base, 'bundled-replacements.json'), JSON.stringify({ formatVersion: 1, svelteCompilerSha256, helpers, files: records }, null, 2) + '\n');
