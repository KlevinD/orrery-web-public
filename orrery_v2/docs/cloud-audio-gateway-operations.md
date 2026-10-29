# Cloud Audio Gateway Operations

## Current Boundary

The implementation connects Weixin voice input to private personal-assistant conversations and adds native authenticated Orrery speech APIs. Feishu and DingTalk audio transport, channel speech delivery, frontend speech controls, personal reply settings, and separate durable audio stages remain pending. No production release has been activated for this feature.

Orrery owns the feature. OpenClaw is only a replaceable backend, accessed through its speech SDK without starting the OpenClaw Gateway or channel consumers. Do not point the adapter at a user's OpenClaw configuration or share its session/TTS preference files.

## Native Orrery APIs

- `POST /api/profile/personal-assistant/audio/speech`: `{conversation_id, message_id, voice?}`; returns private MP3 for a persisted assistant reply. No arbitrary text, owner ID, cloud URL or credentials are accepted.
- `POST /api/profile/personal-assistant/audio/transcriptions`: `{conversation_id, attachment_id, language?}`; returns `{text, input_modality: "voice", approval_confirmation_required: true}` from an existing private audio attachment. It does not execute a conversation or approval action.
- Authentication and current-user identity are resolved by Orrery. The service additionally checks human caller, tenant, private conversation kind, ownership, message role and attachment access before sending content to the backend. Application audit records contain action metadata, not audio or transcript content. Automatic channel delivery is not implied by either endpoint.
- These on-demand APIs do not cache outputs or promise exactly-once provider billing. Repeated calls can repeat inference. Keep automatic retries/voice replies disabled until durable per-stage handling is complete.

## ECS OpenClaw Backend

Set `ORRERY_AUDIO_PROTOCOL=openclaw` and all four absolute paths:

| Variable | Example |
| --- | --- |
| `ORRERY_AUDIO_OPENCLAW_NODE` | `/usr/bin/node` |
| `ORRERY_AUDIO_OPENCLAW_SCRIPT` | release-owned `scripts/openclaw-audio/bridge.mjs` |
| `ORRERY_AUDIO_OPENCLAW_PACKAGE` | `/usr/lib/node_modules/openclaw` |
| `ORRERY_AUDIO_OPENCLAW_CONFIG` | operator-managed speech-only JSON file, mode 0600 |

The installed package must be OpenClaw 2026.5.12. No npm install, Docker or full OpenClaw Gateway startup is needed for this adapter. Paths must be controlled by the operator, not writable by end users. This process isolation is not an OS security sandbox: the helper runs under Orrery's OS account.

Start with `scripts/openclaw-audio/config.example.json` for Microsoft TTS through OpenClaw. This public Edge speech service requires no key but is best effort, not a commercial SLA. Before commercial activation, confirm provider terms, privacy and reliability requirements. The allowlist is deployment policy; it is not a shared user's voice preference.

For STT, add a `stt` object with `provider: "openai"`, an explicitly verified speech `model`, authorized HTTPS `baseUrl`, and speech-only `apiKey`. The adapter invokes OpenClaw's `transcribeAudioFile` using one explicit cloud provider entry; it never falls back to local Whisper, shell commands, the chat model or another provider. The JSON credential stays server-side and is never returned by the API or capability inspection. Obtain a working speech channel before enabling it; the existing Luna chat route has not passed STT.

Each call uses a new private temporary directory, separate SDK state and preference paths, bounded stdin/stdout, a 60-second outer deadline and at most two concurrent processes per adapter. Orrery does not pass its environment secrets or user/conversation IDs into the SDK. All temporary files are removed by the parent after success, failure or cancellation. TTS provider fallback is disabled; current output format is MP3.

Run `node scripts/openclaw-audio/smoke.mjs /usr/lib/node_modules/openclaw /absolute/speech-config.json` from the staged release. It uses synthetic speech only, reports configuration separately from real test results, and deletes test recordings. A configured flag is not a health check. On 2026-09-05 the actual ECS OpenClaw SDK generated valid MP3 (20,592 bytes); STT configuration was absent, so STT was reported `not_tested`. Orrery's running release was not replaced or restarted.

Platform-provided Weixin transcripts require no new cloud configuration. They retain a voice marker and cannot execute the exact typed approval shortcuts. An unsupported codec, missing model configuration, or empty transcript gets a user-facing explanation. Transient cloud errors continue through the existing inbound retry/dead-letter mechanism.

The model adapter is intentionally independent of the chat factory. It calls the configured Gateway's `/audio/transcriptions` and `/audio/speech` paths under the API prefix, never a hardcoded vendor. A profile must be enabled, unique, have a model and credential, and exactly match the configured Gateway base URL after trailing slash normalization. Missing profiles never fall back to a chat model.

## Deployment Configuration

Before enabling raw audio recognition, verify a supported speech model through the actual Gateway, create its profile through the existing administrator settings and secret-reference workflow, and select its profile ID explicitly. Keep it separate from the global chat default. No model ID or credential is supplied by this feature.

The server reads these environment variables at startup:

| Variable | Meaning |
| --- | --- |
| `ORRERY_AUDIO_PROTOCOL` | `openai-compatible` selects the model Gateway backend; `openclaw` selects the SDK backend above. Absent or unknown values disable raw speech capabilities. |
| `ORRERY_AUDIO_GATEWAY_BASE_URL` | Authorized HTTPS Gateway API base URL including the API prefix. No query, userinfo or fragment. |
| `ORRERY_AUDIO_TRANSCRIPTION_PROFILE_ID` | Existing enabled speech-recognition model profile, not a chat profile fallback. |
| `ORRERY_AUDIO_SPEECH_PROFILE_ID` | Existing enabled speech-synthesis model profile. |
| `ORRERY_AUDIO_VOICES` | Comma-separated synthesis voice allowlist for the model-Gateway backend. |
| `ORRERY_AUDIO_SILK_NODE` | Absolute path to Node.js 22 or newer, needed for raw SILK. |
| `ORRERY_AUDIO_SILK_SCRIPT` | Absolute path to the release's `scripts/audio-codec/decode-silk.mjs`. |

Install the codec beside the release using `npm ci --ignore-scripts --no-audit --no-fund` in `scripts/audio-codec`. The lockfile pins `silk-wasm` 3.7.1. Keep the helper and dependencies in a release-owned, non-user-writable directory. This codec helper only decodes SILK; the separate OpenClaw adapter can call cloud STT/TTS. No Docker or local model is required.

The helper receives media on stdin and emits WAV on stdout. It does not receive the API key or application environment. Input/output is bounded to 16 MiB; SILK duration is limited to 120 seconds, the helper has a 15-second process timeout and Node heap is capped at 128 MiB. This is not a general-purpose OS sandbox or total-process-memory guarantee. Cloud requests have a 60-second deadline. WAV, MP3, Ogg, FLAC, WebM and M4A containers are recognized; acceptance of their codecs also depends on the selected Gateway model.

The model-Gateway TTS adapter requires an explicit synthesis profile and voice allowlist, with WAV or MP3 output at the runtime port. The native Orrery read-aloud endpoint requests MP3. Neither backend is wired to automatic channel voice replies yet. Do not advertise full channel support based on SDK tests.

## Verification Gates

1. Finish durable STT checkpoints so downstream retries do not repeat speech inference, and define raw-audio retention/deletion requirements.
2. Confirm Gateway STT/TTS endpoints, models, allowed voices, pricing and private-data policy using synthetic non-private audio/text. Do not use actual user recordings to discover capabilities.
3. Run `go test ./internal/gateway/... ./internal/agent/runtime/audio`, the gateway/approval server tests and `npm test` in `scripts/audio-codec`.
4. Deploy through a reviewed feature branch and the normal release process. No server restart or model configuration was performed during local development.
5. Verify a real Weixin voice message from a bound personal-assistant owner, both with platform text and without it. Confirm no cross-owner attachment access, no organization projection, and no approval mutation from a spoken decision.
6. Keep voice output disabled until text/audio delivery records and channel-native media upload/send are implemented and tested separately.

The protocol shape for Weixin voice was checked against the Tencent-published `@tencent-weixin/openclaw-weixin` 2.4.8 package (`src/api/types.ts` and `src/media/media-download.ts`). Its `voice_item.media` uses the same encrypted CDN envelope as image media; raw SILK is decoded before cloud transcription.

## Local Verification Record

The native speech/OpenClaw extension passed the complete HTTP transport suite, native speech service suite, runtime audio suite, complete gateway suite, focused production gateway/approval/provider wiring and server structure checks, and five Node bridge tests. `npm run generate:api` synchronized the two native endpoints; `npm run check` reported zero errors and warnings. The SDK acceptance test on ECS passed TTS only. Temporary ECS acceptance scripts were removed; source and repeatable smoke tooling remain in the feature branch worktree. No Docker, OpenClaw channel polling, private-audio probe, production configuration update or release activation occurred.

On 2026-09-05 the complete gateway package suite, audio package, targeted production gateway/approval tests, private discussion/voice attachment tests, and the codec's two Node tests passed. The complete `cmd/server` suite was also run: its file-size check and approval wording assertion were corrected and passed targeted reruns. Two deployment-artifact tests initially lacked files because of sparse checkout; adding the existing tracked documentation directory resolved both, and they passed targeted reruns. There was no subsequent clean full-suite rerun after these final test/environment corrections.

The current work is on `feat/cloud-audio-gateway-20260905`, based on `origin/main`. It has not been merged or deployed. Raw audio activation remains gated on the remaining durability/privacy work and real Gateway speech verification above.

## ECS Luna Probe

On 2026-09-05, following the request to use the existing Luna and test on ECS, three real requests were made from ECS using the existing enabled profile `llm-helloapi-gpt-5.6-luna` and its existing secret reference. The input was synthetic silence and a short synthetic speech-generation test sentence; no private recordings were used.

| Request | Result |
| --- | --- |
| `POST /v1/audio/speech`, model `gpt-5.6-luna`, WAV output | HTTP 404, `bad_response_status_code` / `openai_error`. |
| `POST /v1/audio/transcriptions`, model `gpt-5.6-luna`, multipart WAV | HTTP 404, `bad_response_status_code` / `openai_error`. |
| `POST /v1/chat/completions`, same model with an `input_audio` item | HTTP 200, but the assistant explicitly reported that it could not access audio input. This is not a successful audio capability test. |

The currently configured Luna route therefore did not pass either speech endpoint test or the chat-audio input check. This observation concerns the current Gateway route, not every possible Luna deployment. No audio configuration was enabled, no API service was restarted, and no release was activated for this feature.

The live release was `unified-inbox-20260905-ff9a3b4b16`; its frontend was not overwritten. The local feature branch was fast-forwarded to the latest mainline (`d0f9a9735`) while preserving the uncommitted audio changes. The ECS data volume had approximately 151 MiB free, so any later deployment also needs a storage-capacity check. Docker was not started.

## Gateway Routing Diagnosis

A follow-up ECS investigation on 2026-09-05 distinguished missing API paths from missing speech-model channels. The public status endpoint identified Hello API `v1.0.0-rc.30`. The current token's model list contained 15 named models and no explicitly named speech models. The pricing/catalog endpoint required a separate login; that access control was not bypassed.

| Probe | Result and interpretation |
| --- | --- |
| Speech and transcription without a token | HTTP 401, `new_api_error`: both paths pass through Gateway authentication. |
| Speech with the existing token and no explicit model | HTTP 503, `model_not_found`: Gateway selected its `tts-1` default but reported no available channel for that model in the token's current group. |
| Multipart WAV transcription with the existing token and no explicit model | HTTP 503, `model_not_found`: Gateway selected its `whisper-1` default but reported no available channel in the same group. |
| Both speech paths with explicit `gpt-5.6-luna` | HTTP 404, `bad_response_status_code`, message `openai_error`. This differs from the Gateway's unknown-path error and points to failure on the selected Luna relay/upstream route. Upstream configuration and logs were not available for inspection. |
| Deliberately nonexistent API path | HTTP 404, `invalid_request_error`, message `Invalid URL`. |

The observed group was the current token's `内部组`. The transcription routing failure request ID was `202609051008009912376458268d9d6eDMpro52`; the speech routing failure request ID was `202609051008025255496898268d9d6RhWQEw3p`. These identifiers can be used by the Gateway operator to locate the failures without sharing the token.

Conclusion: the existing OpenAI-compatible paths and multipart/JSON request forms reach the Gateway's speech routing logic. Merely replacing the speech model with Luna does not provision speech capabilities. Keep Luna for conversation; the Gateway operator must provide a working speech-recognition channel and, if voice replies are needed, a speech-synthesis channel accessible to the current group. Then configure their explicit profile IDs in Orrery and verify real synthetic speech end to end. `tts-1` and `whisper-1` above are defaults observed in Gateway errors, not new model selections or a recommendation to switch providers.

No production configuration, channel, token permission, release or service state was changed during this diagnosis. The live release at this follow-up was `marketing-submission-20260905-1fe78308c`, with about 934 MiB free on `/data`; deployment must recheck the live release and disk capacity rather than reuse the earlier snapshot. The supplied public documentation URL timed out during retrieval, so the routing conclusion is based on actual ECS responses rather than assumed documentation.
