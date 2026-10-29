// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import isReference from '../packages/ast-reference/index.js';
import { getLocator, locate } from '../packages/source-location/index.js';
import callsites from '../packages/error-callsite/index.cjs';

test('scope positions distinguish computed keys, values, bindings and labels', () => {
  const id = { type: 'Identifier', name: 'value' };
  const other = { type: 'Identifier', name: 'other' };
  const cases = [
    [undefined, true],
    [{ type: 'VariableDeclarator', id }, true],
    [{ type: 'FunctionDeclaration', id }, true],
    [{ type: 'MemberExpression', object: id, property: other, computed: false }, true],
    [{ type: 'MemberExpression', object: other, property: id, computed: false }, false],
    [{ type: 'MemberExpression', object: other, property: id, computed: true }, true],
    [{ type: 'Property', key: id, value: other, computed: false }, false],
    [{ type: 'Property', key: id, value: id, computed: false }, true],
    [{ type: 'Property', key: id, value: other, computed: true }, true],
    [{ type: 'MethodDefinition', key: id, computed: false }, false],
    [{ type: 'MethodDefinition', key: id, computed: true }, true],
    [{ type: 'PropertyDefinition', key: id, value: other, computed: false }, false],
    [{ type: 'PropertyDefinition', key: other, value: id, computed: false }, true],
    [{ type: 'ImportSpecifier', imported: id, local: other }, false],
    [{ type: 'ImportSpecifier', imported: other, local: id }, true],
    [{ type: 'ExportSpecifier', exported: id, local: other }, false],
    [{ type: 'ExportSpecifier', exported: other, local: id }, true],
    ...['MetaProperty', 'LabeledStatement', 'BreakStatement', 'ContinueStatement'].map(type => [{ type, label: id }, false]),
  ];
  for (const [parent, expected] of cases) assert.equal(isReference(id, parent), expected, JSON.stringify(parent));
  assert.equal(isReference({ type: 'Literal', value: 1 }), false);
  assert.equal(isReference({ type: 'MemberExpression', object: id, computed: false }), true);
  assert.equal(isReference({ type: 'MemberExpression', object: id, computed: true }), false);
});

test('locations match UTF-16 prefix coordinates in forward and reverse order', () => {
  for (const source of ['', 'plain', 'a\nb\n', '\n\n', 'a\r\nb', 'a\rb', '中😀\nx\u2028z', '\r\n尾😀\n']) {
    for (const options of [{}, { offsetLine: 1, offsetColumn: 4 }, { offsetLine: -2, offsetColumn: -1 }]) {
      const locator = getLocator(source, options);
      for (const character of [...Array.from({ length: source.length + 1 }, (_, n) => n)].reverse()) {
        const lines = source.slice(0, character).split('\n');
        assert.deepEqual(locator(character), {
          line: lines.length - 1 + (options.offsetLine || 0),
          column: lines.at(-1).length + (options.offsetColumn || 0), character,
        });
      }
      for (const invalid of [-1, source.length + 1, NaN, Infinity]) assert.equal(locator(invalid), undefined);
      assert.equal(locator('absent'), undefined);
    }
  }
});

test('string searches support explicit start indices, repeated matches and EOF', () => {
  const locator = getLocator('x\nx\nx');
  assert.deepEqual(locator('x', 2), { line: 1, column: 0, character: 2 });
  assert.deepEqual(locator('x', 3), { line: 2, column: 0, character: 4 });
  assert.deepEqual(locate('x\nx', 'x', { startIndex: 1, offsetLine: 1 }), { line: 2, column: 0, character: 2 });
  assert.deepEqual(locate('', ''), { line: 0, column: 0, character: 0 });
  assert.deepEqual(locate('x\n', 2), { line: 1, column: 0, character: 2 });
});

test('native CallSites preserve formatter, stack value, cache and descriptor', () => {
  const descriptor = Object.getOwnPropertyDescriptor(Error, 'prepareStackTrace');
  const formatted = { custom: true };
  const formatter = () => formatted;
  Error.prepareStackTrace = formatter;
  try {
    const error = new Error('capture');
    const frames = callsites(error);
    assert.ok(frames.length > 0);
    assert.match(frames[0].getFileName(), /utilities\.test\.mjs$/);
    assert.equal(typeof frames[0].getLineNumber(), 'number');
    assert.equal(error.stack, formatted);
    assert.equal(Error.prepareStackTrace, formatter);
    assert.equal(callsites(error), frames);
  } finally {
    if (descriptor) Object.defineProperty(Error, 'prepareStackTrace', descriptor);
    else delete Error.prepareStackTrace;
  }
  assert.deepEqual(Object.getOwnPropertyDescriptor(Error, 'prepareStackTrace'), descriptor);
  const error = new TypeError('native formatting');
  assert.ok(callsites(error).length > 0);
  assert.match(error.stack, /^TypeError: native formatting\n    at /);
});

test('formatted, missing and throwing stacks do not corrupt the global formatter', () => {
  const error = new Error('already formatted');
  const stack = error.stack;
  assert.equal(callsites(error), undefined);
  assert.equal(error.stack, stack);
  assert.equal(callsites({}), undefined);
  assert.equal(callsites(null), undefined);
  const before = Object.getOwnPropertyDescriptor(Error, 'prepareStackTrace');
  assert.throws(() => callsites({ get stack() { throw new Error('getter failed'); } }), /getter failed/);
  assert.deepEqual(Object.getOwnPropertyDescriptor(Error, 'prepareStackTrace'), before);
});

test('why-is-node-running reports the real allocation site of a live timer', () => {
  const require = createRequire(new URL('../../../web/package.json', import.meta.url));
  const why = require('why-is-node-running');
  // The upstream diagnostic filters by the platform path separator. Use a CJS
  // allocation site so Windows receives a filesystem path rather than an ESM URL.
  const timer = require(fileURLToPath(new URL('./fixtures/timer-site.cjs', import.meta.url)))();
  try {
    const lines = [];
    why({ error: (...args) => lines.push(args.join(' ')) });
    assert.match(lines.join('\n'), /Timeout/);
    assert.match(lines.join('\n'), /timer-site\.cjs:\d+/);
    assert.doesNotMatch(lines.join('\n'), /unknown stack trace/);
  } finally { clearTimeout(timer); }
});
