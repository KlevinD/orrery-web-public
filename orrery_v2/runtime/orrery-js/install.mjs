// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import { existsSync, readFileSync, readdirSync, realpathSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { replacement, sha256, transform } from './bundles.mjs';

const base = path.dirname(fileURLToPath(import.meta.url));
const mode = process.argv[2];
// Docker uses /src/{web,runtime} and /app/runtime; do not require a monorepo prefix.
const roots = { web: path.resolve(base, '../../web'), pi: path.resolve(base, '../pi-coding-agent'), desktop: path.resolve(base, '../../../orrery-desktop') };
if (!Object.hasOwn(roots, mode)) throw new Error('Specify web, pi or desktop');
const project = roots[mode];
const check = process.argv.includes('--check');
const aliases = { 'is-reference': 'ast-reference', 'locate-character': 'source-location', stackback: 'error-callsite', 'proxy-agent-negotiate': 'proxy-negotiate' };
const required = { web: ['is-reference', 'locate-character', 'stackback'], pi: ['proxy-agent-negotiate'], desktop: ['stackback'] };
const found = new Set();
const bundles = JSON.parse(readFileSync(path.join(base, 'bundled-replacements.json'), 'utf8'));
for (const [name, digest] of Object.entries(bundles.helpers)) {
  if (sha256(replacement(name)) !== digest) throw new Error('Regenerate bundled replacements after changing helper source');
}
let patched = 0;
const visited = new Set();

function writeInstalled(file, output) {
  // Break Bun cache hardlinks before changing an installed package.
  const temporary = file + '.orrery-js-tmp';
  writeFileSync(temporary, output);
  renameSync(temporary, file);
}

function replaceFile(file, original, output) {
  const content = readFileSync(file);
  if (sha256(content) === sha256(output)) return;
  if (check) throw new Error(`Utility replacement missing or stale: ${file}`);
  if (sha256(content) !== original) throw new Error(`Unreviewed bundled code: ${file}`);
  writeInstalled(file, output);
  patched += 1;
}

function inspect(directory, alias) {
  const key = realpathSync(directory) + '\0' + alias;
  if (visited.has(key)) return;
  visited.add(key);
  const file = path.join(directory, 'package.json');
  if (!existsSync(file)) return;
  const manifest = JSON.parse(readFileSync(file, 'utf8'));
  const own = aliases[alias];
  if (own) {
    const source = path.join(base, 'packages', own);
    const expected = JSON.parse(readFileSync(path.join(source, 'package.json'), 'utf8'));
    if (manifest.name !== expected.name || manifest.version !== expected.version || manifest.license !== expected.license) {
      throw new Error(`Original dependency returned: ${directory}`);
    }
    for (const name of expected.files) {
      if (sha256(readFileSync(path.join(directory, name))) !== sha256(readFileSync(path.join(source, name)))) {
        throw new Error(`Installed Orrery utility differs: ${directory}/${name}; reinstall dependencies`);
      }
    }
    found.add(alias);
  }
  if (manifest.name === 'svelte') {
    if (manifest.version !== '5.57.3') throw new Error('Review the Svelte compiler entry before upgrading');
    // Node 22 supports synchronous require(ESM). Reuse the source compiler so both
    // entry points resolve the independently implemented packages, with no old bundle.
    const bridge = '// Orrery: source compiler bridge (Node >=22.19). Apache-2.0.\nmodule.exports = require(\'../src/compiler/index.js\');\n';
    replaceFile(path.join(directory, 'compiler/index.js'), bundles.svelteCompilerSha256, bridge);
  }
  if (manifest.name === 'rollup') {
    const records = bundles.files.filter(row => row.version === manifest.version);
    if (records.length !== 4) throw new Error(`Review embedded utilities in Rollup ${manifest.version}`);
    for (const record of records) {
      const target = path.join(directory, record.file);
      const input = readFileSync(target, 'utf8');
      if (sha256(input) !== record.outputSha256) {
        if (check || sha256(input) !== record.inputSha256) throw new Error(`Unreviewed bundled code: ${target}`);
        const output = transform(input, record.ranges);
        if (sha256(output) !== record.outputSha256) throw new Error('Replacement output identity changed');
        writeInstalled(target, output);
        patched += 1;
      }
      const map = target + '.map';
      if (existsSync(map)) {
        if (check) throw new Error(`Original helper source remains in map: ${map}`);
        rmSync(map);
      }
    }
    for (const [name, output] of [
      ['ORRERY-UTILITIES-LICENSE', readFileSync(path.join(base, 'packages/source-location/LICENSE'))],
      ['ORRERY-UTILITIES-NOTICE', 'Orrery embedded AST reference and source location utilities\nCopyright 2026 Minvia\nApache-2.0. Independently implemented replacements; original Rollup notices remain in LICENSE.md.\n'],
    ]) {
      const file = path.join(directory, name);
      if (!existsSync(file) || sha256(readFileSync(file)) !== sha256(output)) {
        if (check) throw new Error(`Missing Orrery embedded-source notice: ${file}`);
        writeInstalled(file, output);
      }
    }
  }
  walk(path.join(directory, 'node_modules'));
}

function walk(nodeModules) {
  if (!existsSync(nodeModules)) return;
  for (const entry of readdirSync(nodeModules, { withFileTypes: true })) {
    if (entry.name === '.bun') {
      // Bun's isolated store also contains packages reachable only from a dependency.
      for (const store of readdirSync(path.join(nodeModules, '.bun'))) walk(path.join(nodeModules, '.bun', store, 'node_modules'));
    } else if (entry.name.startsWith('@')) {
      for (const child of readdirSync(path.join(nodeModules, entry.name))) inspect(path.join(nodeModules, entry.name, child), entry.name + '/' + child);
    } else if (!entry.name.startsWith('.') && (entry.isDirectory() || entry.isSymbolicLink())) {
      inspect(path.join(nodeModules, entry.name), entry.name);
    }
  }
}

walk(path.join(project, 'node_modules'));
for (const name of required[mode]) if (!found.has(name)) throw new Error(`Missing installed replacement: ${name}`);
console.log(`Orrery utilities ${mode}: ${found.size} aliases verified; ${patched} bundled files replaced${check ? ' (check)' : ''}`);
