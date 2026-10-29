import { readFile, writeFile, stat, unlink } from 'node:fs/promises';
import { isAbsolute, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const supportedVersion = '2026.5.12';
const maxAudioBytes = 16 << 20;
const maxRequestBytes = Math.ceil(maxAudioBytes / 3) * 4 + (64 << 10);

export class SpeechError extends Error {
  constructor(code) { super('speech request failed'); this.code = code; }
}

function requireValue(condition, code = 11) {
  if (!condition) throw new SpeechError(code);
}

function httpsURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password && !url.search && !url.hash;
  } catch { return false; }
}

export function capabilities(config) {
  const stt = config.stt;
  const tts = config.tts;
  return {
    transcription_configured: Boolean(stt?.provider === 'openai' && typeof stt.model === 'string' && stt.model.trim() &&
      httpsURL(stt.baseUrl) && typeof stt.apiKey === 'string' && stt.apiKey.trim()),
    synthesis_configured: Boolean(tts?.provider === 'microsoft' && Array.isArray(tts.voices) &&
      tts.voices.length && tts.voices.every(v => typeof v === 'string' && /^[a-zA-Z0-9-]{1,128}$/.test(v)) &&
      tts.voices.includes(tts.defaultVoice)),
  };
}

// Only speech configuration is projected into the SDK. Never load a shared agent
// configuration, auth store, personal TTS preferences, channel or chat session.
export async function executeRequest(config, request, sdk, scratch) {
  requireValue(config && typeof config === 'object' && !Array.isArray(config), 10);
  requireValue(request && typeof request === 'object' && !Array.isArray(request));
  const ready = capabilities(config);
  if (request.operation === 'capabilities') return Buffer.from(JSON.stringify(ready));

  if (request.operation === 'transcribe') {
    requireValue(ready.transcription_configured, 10);
    requireValue(typeof request.data === 'string' && request.data.length <= maxRequestBytes);
    const data = Buffer.from(request.data, 'base64');
    requireValue(data.length > 0 && data.length <= maxAudioBytes && data.toString('base64') === request.data);
    const extensions = { 'audio/wav': '.wav', 'audio/x-wav': '.wav', 'audio/mpeg': '.mp3',
      'audio/ogg': '.ogg', 'audio/flac': '.flac', 'audio/webm': '.webm', 'audio/mp4': '.m4a' };
    requireValue(Object.hasOwn(extensions, request.mime_type));
    requireValue(request.language === undefined || (typeof request.language === 'string' && request.language.length <= 32));
    const { model, baseUrl, apiKey } = config.stt;
    const cfg = {
      models: { providers: { openai: { baseUrl, apiKey, models: [] } } },
      tools: { media: { audio: { enabled: true, maxBytes: maxAudioBytes, maxChars: 65536, timeoutSeconds: 40,
        models: [{ type: 'provider', provider: 'openai', model, baseUrl, timeoutSeconds: 40 }],
      } } },
    };
    const filePath = join(scratch, 'input' + extensions[request.mime_type]);
    await writeFile(filePath, data, { mode: 0o600, flag: 'wx' });
    try {
      const result = await sdk.transcribeAudioFile({ filePath, cfg, agentDir: join(scratch, 'agent'),
        mime: request.mime_type, language: request.language });
      requireValue(result.provider === 'openai' && result.model === model, 13);
      requireValue(typeof result.text === 'string' && result.text.trim(), 12);
      requireValue(Buffer.byteLength(result.text) <= 65536, 13);
      return Buffer.from(JSON.stringify({ text: result.text.trim() }));
    } finally { await unlink(filePath); }
  }

  if (request.operation === 'synthesize') {
    requireValue(ready.synthesis_configured, 10);
    requireValue(typeof request.text === 'string' && request.text.trim() && [...request.text].length <= 4096);
    requireValue(request.format === 'mp3');
    const voice = request.voice || config.tts.defaultVoice;
    requireValue(config.tts.voices.includes(voice));
    const prefsPath = join(scratch, 'tts-prefs.json');
    const cfg = { messages: { tts: { provider: 'microsoft', maxTextLength: 4096, timeoutMs: 40000,
      prefsPath, modelOverrides: { enabled: false },
      providers: { microsoft: { voice, outputFormat: 'audio-24khz-48kbitrate-mono-mp3' } },
    } } };
    const result = await sdk.synthesizeSpeech({ text: request.text, cfg, prefsPath,
      disableFallback: true, timeoutMs: 40000 });
    requireValue(result.success && result.provider === 'microsoft' && result.fileExtension === '.mp3', 13);
    requireValue(Buffer.isBuffer(result.audioBuffer) && result.audioBuffer.length > 0 && result.audioBuffer.length <= maxAudioBytes, 13);
    return result.audioBuffer;
  }
  throw new SpeechError(11);
}

async function main() {
  const [packagePath, configPath, scratch] = process.argv.slice(2);
  requireValue([packagePath, configPath, scratch].every(v => typeof v === 'string' && isAbsolute(v)), 10);
  requireValue((await stat(scratch)).isDirectory(), 10);
  const configStat = await stat(configPath);
  requireValue(configStat.isFile() && configStat.size <= 65536, 10);
  if (process.platform !== 'win32') requireValue((configStat.mode & 0o077) === 0, 10);
  const config = JSON.parse(await readFile(configPath, 'utf8'));
  const metadata = JSON.parse(await readFile(join(packagePath, 'package.json'), 'utf8'));
  requireValue(metadata.name === 'openclaw' && metadata.version === supportedVersion, 10);
  const chunks = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    size += chunk.length;
    requireValue(size <= maxRequestBytes);
    chunks.push(chunk);
  }
  const request = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  // Provider SDK logging must not corrupt binary output or expose private content.
  console.log = console.info = console.warn = console.error = console.debug = () => {};
  const ready = capabilities(config);
  let sdk = {};
  if (request.operation === 'synthesize' && ready.synthesis_configured) {
    sdk = await import(pathToFileURL(join(packagePath, 'dist/plugin-sdk/tts-runtime.js')).href);
  } else if (request.operation === 'transcribe' && ready.transcription_configured) {
    sdk = await import(pathToFileURL(join(packagePath, 'dist/plugin-sdk/media-understanding-runtime.js')).href);
  }
  const output = await executeRequest(config, request, sdk, scratch);
  await new Promise((resolve, reject) => process.stdout.write(output, err => err ? reject(err) : resolve()));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const timer = setTimeout(() => process.exit(13), 55000);
  try { await main(); clearTimeout(timer); process.exit(0); }
  catch (error) { clearTimeout(timer); process.exit(error instanceof SpeechError ? error.code : 13); }
}
