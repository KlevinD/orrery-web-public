// Validate the design index and map changed source paths to review contracts.
// This is a review aid, not a proof of semantic compatibility or business success.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

const project = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const baseline = path.join(project, 'docs/design-baseline');
const manifest = JSON.parse(fs.readFileSync(path.join(baseline, 'contracts.json'), 'utf8'));
const errors = [];
const exists = relative => fs.existsSync(path.join(project, relative));
const indexText = fs.readFileSync(path.join(baseline, '11-change-contract.md'), 'utf8');
const ids = new Set();
for (const article of manifest.articles) {
  const filename = path.join(baseline, article);
  if (!fs.existsSync(filename)) { errors.push(`Missing article: ${article}`); continue; }
  const content = fs.readFileSync(filename, 'utf8');
  if (!content.includes(manifest.baseline_id)) errors.push(`Missing baseline ID: ${article}`);
  for (const match of content.matchAll(/\]\(([^)]+)\)/g)) {
    const target = match[1].split('#')[0];
    if (!target || /^[a-z]+:/i.test(target)) continue;
    if (!fs.existsSync(path.resolve(path.dirname(filename), target))) errors.push(`Broken link in ${article}: ${target}`);
  }
}
for (const contract of manifest.contracts) {
  if (ids.has(contract.id)) errors.push(`Duplicate contract: ${contract.id}`);
  ids.add(contract.id);
  if (!indexText.includes(contract.id)) errors.push(`Contract missing from review table: ${contract.id}`);
  if (!manifest.articles.includes(contract.article)) errors.push(`Unknown article: ${contract.article}`);
  for (const source of [...contract.sources, ...contract.regressions]) {
    if (!exists(source)) errors.push(`Missing reference for ${contract.id}: ${source}`);
  }
  if (!contract.requirement || !contract.implementation) errors.push(`Incomplete contract: ${contract.id}`);
}
const args = process.argv.slice(2);
const refIndex = args.indexOf('--changed-ref');
let affected = [];
let changed = [];
if (refIndex !== -1) {
  const ref = args[refIndex + 1];
  if (!ref || ref.startsWith('-')) throw new Error('--changed-ref requires a Git reference');
  const repository = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: project, encoding: 'utf8' }).trim();
  const repositoryPath = source => path.relative(repository, path.resolve(project, source)).replaceAll('\\', '/');
  changed = execFileSync('git', ['diff', '--name-only', ref, '--', '.'], { cwd: repository, encoding: 'utf8' }).trim().split(/\r?\n/).filter(Boolean);
  affected = manifest.contracts.filter(c => changed.some(f => c.sources.map(repositoryPath).some(s => f === s || f.startsWith(s.replace(/\/$/, '') + '/')))).map(c => ({ id: c.id, requirement: c.requirement, article: c.article, regressions: c.regressions }));
}
console.log(JSON.stringify({ baseline: manifest.baseline_id, revision: manifest.revision, articles: manifest.articles.length, contracts: ids.size, errors, changed, affected_contracts_for_review: affected, semantic_compatibility_verified: false }, null, 2));
process.exitCode = errors.length ? 1 : 0;
