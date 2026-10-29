// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { once } from 'node:events';
import http from 'node:http';
import net from 'node:net';
import { createRequire, registerHooks } from 'node:module';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import test from 'node:test';
import { createNegotiateAuth } from '../packages/proxy-negotiate/index.js';
import { state } from './fixtures/kerberos.mjs';

registerHooks({ resolve(specifier, context, next) {
  if (specifier === 'kerberos') return { url: new URL('./fixtures/kerberos.mjs', import.meta.url).href, shortCircuit: true };
  return next(specifier, context);
} });
const require = createRequire(new URL('../../pi-coding-agent/package.json', import.meta.url));
const { HttpsProxyAgent } = await import(pathToFileURL(require.resolve('https-proxy-agent')).href);
const { HttpProxyAgent } = await import(pathToFileURL(require.resolve('http-proxy-agent')).href);

test('Negotiate challenges generate tokens through the optional Kerberos API', async () => {
  for (const [header, token] of [['Negotiate', ''], ['negotiate server-token', 'server-token'], [['Basic realm="proxy"', 'Negotiate server-token'], 'server-token']]) {
    state.calls.length = 0;
    assert.deepEqual(await createNegotiateAuth()({ scheme: 'Negotiate', response: { statusCode: 407, headers: { 'proxy-authenticate': header } } }),
      { headers: { 'Proxy-Authorization': 'Negotiate test-client-token' } });
    assert.deepEqual(state.calls, [['initializeClient', 'HTTP@proxy', { mechOID: 'test-spnego' }], ['step', token]]);
  }
});

test('unsupported, malformed, native failure and empty token paths reject', async () => {
  const auth = createNegotiateAuth();
  await assert.rejects(auth({ scheme: 'Basic', response: { headers: {} } }), /Expected Negotiate/);
  await assert.rejects(auth({ scheme: 'Negotiate', response: { headers: {} } }), /did not supply/);
  state.error = new Error('native credential failure');
  try { await assert.rejects(auth({ scheme: 'Negotiate', response: { headers: { 'proxy-authenticate': 'Negotiate' } } }), /native credential failure/); }
  finally { state.error = null; }
  state.token = '';
  try { await assert.rejects(auth({ scheme: 'Negotiate', response: { headers: { 'proxy-authenticate': 'Negotiate' } } }), /empty proxy authentication token/); }
  finally { state.token = 'test-client-token'; }
});

test('missing optional native peer fails explicitly when authentication is requested', () => {
  const source = new URL('../packages/proxy-negotiate/index.js', import.meta.url).href;
  const child = spawnSync(process.execPath, ['--input-type=module', '-e', `import {createNegotiateAuth} from ${JSON.stringify(source)}; try { await createNegotiateAuth()({scheme:'Negotiate',response:{headers:{'proxy-authenticate':'Negotiate'}}}); process.exit(1); } catch(e) { if(e.code!=='ERR_MODULE_NOT_FOUND') throw e; }`], { encoding: 'utf8' });
  assert.equal(child.status, 0, child.stderr);
});

async function listen(server, t) {
  const sockets = new Set();
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => { for (const socket of sockets) socket.destroy(); server.close(); });
  return server.address().port;
}

function request(url, agent) {
  return new Promise((resolve, reject) => {
    const req = http.get(url, { agent }, res => { let text = ''; res.setEncoding('utf8'); res.on('data', data => { text += data; }); res.on('end', () => resolve({ status: res.statusCode, text })); });
    req.setTimeout(5000, () => req.destroy(new Error('proxy test timeout')));
    req.on('error', reject);
  });
}

test('installed HTTPS agent retries CONNECT after 407 and tunnels the request', async t => {
  const origin = http.createServer((req, res) => res.end('origin response'));
  const targetPort = await listen(origin, t);
  const attempts = [];
  const proxy = http.createServer();
  proxy.on('connect', (req, socket, head) => {
    attempts.push(req.headers['proxy-authorization']);
    if (!req.headers['proxy-authorization']) {
      socket.end('HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Negotiate server-token\r\nContent-Length: 0\r\nConnection: close\r\n\r\n');
      return;
    }
    const upstream = net.connect(targetPort, '127.0.0.1', () => {
      socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length) upstream.write(head);
      socket.pipe(upstream).pipe(socket);
    });
    socket.on('close', () => upstream.destroy());
    upstream.on('error', () => socket.destroy());
  });
  const proxyPort = await listen(proxy, t);
  const agent = new HttpsProxyAgent(`http://127.0.0.1:${proxyPort}`, { negotiate: true });
  t.after(() => agent.destroy());
  assert.deepEqual(await request(`http://127.0.0.1:${targetPort}/`, agent), { status: 200, text: 'origin response' });
  assert.deepEqual(attempts, [undefined, 'Negotiate test-client-token']);
});

test('installed HTTPS agent preserves rejection after an unsuccessful retry', async t => {
  const proxy = http.createServer();
  proxy.on('connect', (req, socket) => socket.end('HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Negotiate\r\nContent-Length: 0\r\nConnection: close\r\n\r\n'));
  const port = await listen(proxy, t);
  const agent = new HttpsProxyAgent(`http://127.0.0.1:${port}`, { negotiate: true });
  t.after(() => agent.destroy());
  await assert.rejects(request('http://127.0.0.1:9/', agent), /authentication failed with status 407 after retry/);
});

test('installed HTTP agent preserves ordinary and Basic proxy behavior', async t => {
  const headers = [];
  const proxy = http.createServer((req, res) => { headers.push(req.headers['proxy-authorization']); res.end('forwarded'); });
  const port = await listen(proxy, t);
  for (const auth of ['', 'user:pass@']) {
    const agent = new HttpProxyAgent(`http://${auth}127.0.0.1:${port}`);
    try { assert.deepEqual(await request('http://example.invalid/resource', agent), { status: 200, text: 'forwarded' }); }
    finally { agent.destroy(); }
  }
  assert.deepEqual(headers, [undefined, 'Basic dXNlcjpwYXNz']);
});
