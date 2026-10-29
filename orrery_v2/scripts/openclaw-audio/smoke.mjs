import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const [packagePath, configPath] = process.argv.slice(2);
if (!packagePath || !configPath) throw new Error('Usage: node smoke.mjs OPENCLAW_PACKAGE SPEECH_CONFIG');
const bridge = join(dirname(fileURLToPath(import.meta.url)), 'bridge.mjs');

async function call(request) {
  const scratch = await mkdtemp(join(tmpdir(), 'orrery-speech-smoke-'));
  try {
    return await new Promise((resolveOutput, reject) => {
      const child = spawn(process.execPath, [bridge, resolve(packagePath), resolve(configPath), scratch], {
        env: { OPENCLAW_STATE_DIR: join(scratch, 'state'), OPENCLAW_CONFIG_PATH: join(scratch, 'openclaw.json'),
          TMPDIR: scratch, TEMP: scratch, TMP: scratch },
        stdio: ['pipe', 'pipe', 'ignore'], timeout: 60000, killSignal: 'SIGKILL',
      });
      const chunks = [];
      let size = 0;
      child.stdout.on('data', chunk => {
        size += chunk.length;
        if (size > (16 << 20)) child.kill('SIGKILL');
        else chunks.push(chunk);
      });
      child.stdin.on('error', () => {});
      child.on('error', reject);
      child.on('close', code => code === 0 ? resolveOutput(Buffer.concat(chunks)) : reject(new Error(`speech operation ${request.operation} failed, exit=${code}`)));
      child.stdin.end(JSON.stringify(request));
    });
  } finally { await rm(scratch, { recursive: true, force: true }); }
}

const capabilities = JSON.parse(await call({ operation: 'capabilities' }));
console.log(JSON.stringify({ capabilities }));
let speech;
if (capabilities.synthesis_configured) {
  speech = await call({ operation: 'synthesize', text: 'This is an Orrery speech capability test.', format: 'mp3' });
  if (!(speech.subarray(0, 3).toString() === 'ID3' || (speech[0] === 255 && (speech[1] & 224) === 224))) throw new Error('invalid MP3 output');
  console.log(JSON.stringify({ synthesis: 'passed', bytes: speech.length, sha256: createHash('sha256').update(speech).digest('hex') }));
}
if (capabilities.transcription_configured && speech) {
  const result = JSON.parse(await call({ operation: 'transcribe', data: speech.toString('base64'), mime_type: 'audio/mpeg' }));
  if (typeof result.text !== 'string' || !/speech|test|orrery/i.test(result.text)) throw new Error('synthetic transcription did not match test speech');
  console.log(JSON.stringify({ transcription: 'passed', characters: result.text.length }));
} else {
  console.log(JSON.stringify({ transcription: 'not_tested', reason: 'synthetic audio or transcription configuration missing' }));
}
