import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { capabilities, executeRequest } from './bridge.mjs';

const config = {
  stt: { provider: 'openai', model: 'explicit-stt', baseUrl: 'https://gateway.example/v1', apiKey: 'test-key' },
  tts: { provider: 'microsoft', defaultVoice: 'zh-CN-XiaoxiaoNeural', voices: ['zh-CN-XiaoxiaoNeural'] },
};
const voiceRequest = { operation: 'transcribe', data: Buffer.from('test audio').toString('base64'), mime_type: 'audio/wav' };

async function workspace(run) {
  const scratch = await mkdtemp(join(tmpdir(), 'orrery-speech-test-'));
  try { await run(scratch); } finally { await rm(scratch, { recursive: true, force: true }); }
}

test('capabilities report separate configuration, never pretend health', () => {
  assert.deepEqual(capabilities({}), { transcription_configured: false, synthesis_configured: false });
  assert.deepEqual(capabilities({ tts: config.tts }), { transcription_configured: false, synthesis_configured: true });
  for (const stt of [ { ...config.stt, apiKey: '' }, { ...config.stt, baseUrl: 'http://gateway.example' },
    { ...config.stt, provider: 'cli' }, { ...config.stt, baseUrl: 'https://user:secret@gateway.example' } ]) {
    assert.equal(capabilities({ stt }).transcription_configured, false);
  }
});

test('STT sends only explicit cloud configuration, isolated file and no chat identity', async () => {
  await workspace(async scratch => {
    const sdk = { transcribeAudioFile: async params => {
      assert.equal((await readFile(params.filePath)).toString(), 'test audio');
      assert.equal(params.agentDir, join(scratch, 'agent'));
      assert.equal(params.cfg.channels, undefined);
      assert.equal(params.activeModel, undefined);
      assert.equal(params.cfg.tools.media.audio.models.length, 1);
      assert.equal(params.cfg.tools.media.audio.models[0].type, 'provider');
      assert.equal(params.cfg.tools.media.audio.models[0].baseUrl, config.stt.baseUrl);
      assert.equal(params.cfg.models.providers.openai.apiKey, 'test-key');
      return { text: '  hello  ', provider: 'openai', model: 'explicit-stt' };
    } };
    const raw = await executeRequest(config, voiceRequest, sdk, scratch);
    assert.deepEqual(JSON.parse(raw), { text: 'hello' });
    assert.deepEqual(await readdir(scratch), []);
  });
});

test('STT deletes private media after provider errors and rejects provider substitution', async () => {
  for (const result of [new Error('private provider details'), { text: 'hello', provider: 'other', model: 'explicit-stt' }]) {
    await workspace(async scratch => {
      await assert.rejects(executeRequest(config, voiceRequest, { transcribeAudioFile: async () => {
        if (result instanceof Error) throw result;
        return result;
      } }, scratch));
      assert.deepEqual(await readdir(scratch), []);
    });
  }
});

test('TTS has no shared prefs, fallback, chat or model-controlled provider', async () => {
  await workspace(async scratch => {
    const raw = await executeRequest(config, { operation: 'synthesize', text: 'hello', format: 'mp3' }, {
      synthesizeSpeech: async params => {
        assert.equal(params.disableFallback, true);
        assert.equal(params.prefsPath, join(scratch, 'tts-prefs.json'));
        assert.equal(params.cfg.messages.tts.providers.microsoft.voice, config.tts.defaultVoice);
        assert.equal(params.cfg.messages.tts.modelOverrides.enabled, false);
        assert.equal(params.cfg.agents, undefined);
        return { success: true, provider: 'microsoft', fileExtension: '.mp3', audioBuffer: Buffer.from('ID3audio') };
      },
    }, scratch);
    assert.equal(raw.toString(), 'ID3audio');
  });
});

test('invalid inputs and disabled capabilities never call SDK', async () => {
  await workspace(async scratch => {
    const forbiddenSDK = new Proxy({}, { get() { assert.fail('SDK must not be invoked'); } });
    for (const request of [ { operation: 'exec' }, { ...voiceRequest, mime_type: '../../file' },
      { ...voiceRequest, data: 'not base64' }, { operation: 'synthesize', text: 'hello', format: 'mp3', voice: 'not-allowed' },
      { operation: 'synthesize', text: 'x'.repeat(4097), format: 'mp3' } ]) {
      await assert.rejects(executeRequest(config, request, forbiddenSDK, scratch), { code: 11 });
    }
    await assert.rejects(executeRequest({}, voiceRequest, forbiddenSDK, scratch), { code: 10 });
  });
});
