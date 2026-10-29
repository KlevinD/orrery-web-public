// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

export const sha256 = value => createHash('sha256').update(value).digest('hex');

export function replacement(name) {
  if (name === 'rangeContains') return '';
  const reference = readFileSync(new URL('./packages/ast-reference/index.js', import.meta.url), 'utf8');
  const location = readFileSync(new URL('./packages/source-location/index.js', import.meta.url), 'utf8');
  if (name === 'is_reference') return reference.replace('export default ', '').replaceAll('isReference', 'is_reference');
  if (name === 'getLocator') return location.slice(0, location.indexOf('\nexport function locate(')).replace('export ', '');
  if (name === 'locate') return location.slice(location.indexOf('export function locate(')).replace('export ', '');
  throw new Error(`Unknown embedded helper: ${name}`);
}

export function transform(input, ranges) {
  let result = input;
  for (const range of [...ranges].sort((a, b) => b.start - a.start)) {
    if (sha256(input.slice(range.start, range.end)) !== range.sha256) throw new Error('Embedded helper identity changed');
    result = result.slice(0, range.start) + replacement(range.name) + result.slice(range.end);
  }
  // The original map describes removed code and must not be distributed with this file.
  return result.replace(/^\/\/[#@] sourceMappingURL=.*(?:\r?\n|$)/gm, '');
}
