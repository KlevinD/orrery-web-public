// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import test from 'node:test';

const require = createRequire(new URL('../../../web/package.json', import.meta.url));
const esm = await import(pathToFileURL(require.resolve('svelte/package.json').replace(/package\.json$/, 'src/compiler/index.js')).href);
const cjs = require('svelte/compiler');

test('Svelte ESM and CommonJS compile runes, bindings, computed properties and SSR identically', () => {
  const source = `<script>
    let { initial = 1 } = $props();
    let count = $state(initial);
    const key = 'value';
    const object = { [key]: count };
    class Example { value = count; [key]() { return object[key]; } }
    const increment = () => count += new Example().value;
  </script>
  <button onclick={increment}>{count}: {object.value}</button>`;
  for (const generate of ['client', 'server']) {
    const options = { filename: 'CoordinateFixture.svelte', generate, dev: true };
    const a = esm.compile(source, options);
    const b = cjs.compile(source, options);
    assert.equal(a.js.code, b.js.code);
    assert.deepEqual(a.js.map, b.js.map);
    assert.deepEqual(a.warnings, b.warnings);
    assert.ok(a.js.map.sourcesContent.includes(source));
  }
  assert.equal(esm.VERSION, cjs.VERSION);
});

test('compiler diagnostics keep the original source line and column', () => {
  for (const compiler of [esm, cjs]) {
    assert.throws(() => compiler.compile('<p>hello</p>\n<script>\nlet x = ;\n</script>', { filename: 'Broken.svelte' }), error => {
      assert.equal(error.start.line, 3);
      assert.equal(error.start.column, 8);
      assert.match(error.message, /Unexpected token/);
      return true;
    });
  }
});

test('Rollup import and require preserve references and source maps after tree shaking', async () => {
  const rollupEsm = await import(pathToFileURL(require.resolve('rollup').replace(/dist[\\/]rollup.js$/, 'dist/es/rollup.js')).href);
  const rollupCjs = require('rollup');
  const source = `const key = 'value'; const object = { [key]: 3 }; class Example { value = object.value; [key]() { return object[key]; } } const unused = 99; export default new Example().value;`;
  for (const { rollup } of [rollupEsm, rollupCjs]) {
    const bundle = await rollup({ input: 'fixture', plugins: [{ name: 'fixture', resolveId: id => id, load: () => source }] });
    try {
      const { output: [chunk] } = await bundle.generate({ format: 'es', sourcemap: true });
      assert.doesNotMatch(chunk.code, /unused/);
      assert.equal((await import('data:text/javascript;base64,' + Buffer.from(chunk.code).toString('base64'))).default, 3);
      assert.ok(chunk.map.sourcesContent.includes(source));
    } finally { await bundle.close(); }
  }
});

test('Rollup parse diagnostics locate a failure on its original line', async () => {
  const { rollup } = require('rollup');
  await assert.rejects(rollup({ input: 'broken', plugins: [{ name: 'broken', resolveId: id => id, load: () => 'const ok = 1;\n\nexport const x = ;' }] }), error => {
    assert.equal(error.loc.line, 3);
    assert.equal(error.loc.column, 17);
    return true;
  });
});
