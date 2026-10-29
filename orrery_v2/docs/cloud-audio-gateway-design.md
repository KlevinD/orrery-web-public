# Orrery Native Speech With Replaceable Backends

Status: first implementation slice completed locally on 2026-09-05; not deployed or enabled in production. The remaining sections describe the target design, not a claim that every stage is implemented.

## Implementation Status

- Product boundary: speech is an Orrery capability. Orrery owns authenticated APIs, private conversation access, voice provenance, approval safety and audit. OpenClaw supplies only STT/TTS execution, not user accounts, conversations, preferences, model reasoning or channel polling.
- Implemented: Orrery-owned `internal/app/speech` use cases and authenticated `/api/profile/personal-assistant/audio/speech` and `/audio/transcriptions` endpoints. Synthesis reads an existing private assistant reply, never caller-supplied arbitrary text. Transcription reads an existing authorized private attachment. Both use `private, no-store`; neither sends a conversation turn or a channel message.
- Implemented: replaceable `audio.Provider` ports, OpenAI-compatible Gateway adapter, and OpenClaw SDK adapter pinned to installed version 2026.5.12. SDK invocations have isolated temporary state, no application environment credentials, explicit cloud-only STT routing and disabled TTS fallback. The gateway voice path uses the same backend selection.
- Verified on ECS: OpenClaw synthesis generated a valid 20,592-byte MP3 from synthetic text. STT was explicitly reported unconfigured and not tested. This was an isolated SDK acceptance test, not an Orrery production release or proof of channel voice delivery.
- Implemented: explicit, pinned HTTPS model-gateway routing for transcription and synthesis, existing profile/secret resolution, bounded input/output, no redirects, scoped idempotency headers, redacted errors and cancellation.
- Implemented: Weixin voice provenance, platform transcription reuse, encrypted CDN audio download, optional pinned SILK-to-WAV helper, cloud transcription after binding/tenant/viewer checks, and private conversation attachment storage. Voice-only callbacks without usable text/media receive an explanation instead of being silently dropped.
- Implemented: voice-derived exact approval replies require typed confirmation and never reach either the quick-approval handler or the assistant runtime. Voice cannot trigger `/new`, account binding or agent-switch commands. Existing typed approval behavior is unchanged.
- Tested: actual SILK encode/decode round trip, cloud protocol contract tests, invalid profiles/media, authorization rejection, voice approval handling, channel regressions, native private speech use cases, and SDK isolation. Real TTS passed the ECS SDK test above; real STT remains unverified.
- Not implemented in this slice: Feishu/DingTalk audio transport, TTS delivery to channels, frontend recording/playback controls, personal reply preferences, durable per-stage transcription checkpoints, consecutive-message batching, or a separate seven-day recording retention policy. Native speech APIs are implemented locally, not activated in production. Do not enable automatic voice replies yet.
- Retry boundary: existing inbound durable jobs and conversation idempotency remain in use. STT may be called again after a downstream failure; a sent idempotency header is not a guarantee that a gateway deduplicates billing. Add durable transcription checkpoints before production activation.
- Retention: successfully accepted audio uses existing private discussion attachments and conversation retention. No new organization-facing audio index or statistics projection was added.

The initial read-only ECS check on 2026-09-05 found only two enabled chat profiles, both using `gpt-5.6-luna`, and no `ORRERY_AUDIO_*` configuration. The subsequent Hello API speech probes failed for the current route/group; see the operations record. This is distinct from OpenClaw: its installed SDK later passed synthetic TTS. No private recordings were used, and no production configuration was changed.

See [audio Gateway operations](cloud-audio-gateway-operations.md) for opt-in configuration and deployment gates.

## Existing Integration

`internal/gateway/channel` owns channel identity, inbound jobs, and outbound delivery. It is distinct from the cloud model gateway.

`internal/agent/runtime/llmprofile` supplies model profiles and secret resolution. `internal/agent/runtime/providers/eino/provider_factory.go` currently builds chat-completion clients from those profiles. It does not provide STT or TTS. Reuse the profile and secret interfaces, not the chat request format.

The Weixin adapter reuses provider-supplied `voice_item.text` or downloads raw voice media when platform text is absent. Feishu and DingTalk audio transport must be implemented and verified independently.

## Routing Contract

Channel or Orrery API -> authenticated private conversation -> Orrery speech service -> selected backend (OpenClaw SDK or model Gateway) -> cloud speech provider.

The model-Gateway backend uses explicitly configured gateway profiles. The ECS OpenClaw backend uses a separate operator-managed speech-only configuration, never the shared OpenClaw agent configuration. Channel adapters must never contain provider credentials or call a speech vendor directly. Missing configuration fails explicitly; there is no fallback to the first chat model, local CLI or another speech supplier.

Configure separate profile IDs for `audio.transcribe` and `audio.synthesize`. Reuse existing secret references; do not expose keys in personal settings. A profile must declare its supported protocol, models, input formats, output formats, languages, limits, and voices. Validate advertised capabilities against a real gateway request before enabling them.

An OpenAI-compatible gateway can be supported through multipart transcription and JSON speech adapters only after its audio endpoints are confirmed. Do not infer audio support from successful chat requests or from a model-list response.

## Application Ports

Transcribe(context, request) -> transcript, language, duration, usage, gateway request ID.

The request contains tenant ID, owner ID, conversation ID, source message ID, idempotency key, authorized private attachment reference, optional language hint, and configured profile ID. Resolve these identities server-side from the verified channel binding. Never trust identity fields provided by a media callback.

Synthesize(context, request) -> private audio attachment, MIME type, duration, usage, gateway request ID.

The request contains the same identity scope plus persisted assistant message ID, text, voice, language, and supported output format. Only accepted user-facing assistant output may be synthesized; exclude internal reasoning and tool output.

Keep cloud HTTP protocol adapters separate from application orchestration. Share timeouts, secret resolution, and usage recording with existing infrastructure where interfaces permit. Use bounded reads, reject redirects carrying authorization, and redact provider error bodies and signed media URLs.

## Inbound State

Received -> identity verified -> media downloading -> media ready -> transcribing -> transcript ready -> conversation turn queued -> completed.

Platform text can skip transcription if supplied, but retain provenance: `platform` or `model_gateway`. Preserve source modality even after transcription so voice cannot enter text-only approval command handling.

Download and transcription may run independently of an active assistant turn. Conversation turns remain serial. Scope deduplication to tenant, account, and provider event ID. Retry media or STT stages without duplicating the conversation message or rerunning an already committed assistant turn.

Use a configurable short collection window for consecutive text and media from the same bound user and conversation; start with two seconds and a five-second maximum. Once an assistant turn starts, later messages become a new batch. Wait for attachments already assigned to a batch before model execution; never substitute a historical image for a pending new image. Reply with an explicit failure when media cannot be prepared.

## Outbound State

Assistant text persisted -> reply preference resolved -> optional TTS -> channel encoding -> delivery -> receipt recorded.

Default to text replies. Personal preferences support text, match-input, and voice-with-text, plus language and an allowed voice. Unsupported channel audio delivery falls back to text. Long replies receive a bounded spoken summary with complete text retained.

Record audio and text delivery independently. A TTS failure must not rerun the assistant. A delivery timeout with unknown receipt must not blindly resend or promise exactly-once delivery; use provider idempotency where available and reconcile receipt state.

## Privacy and Approval

Store media under existing private conversation ownership. Exclude recordings and transcripts from organization discovery, ontology content projections, and organization-facing assistant statistics. Operational telemetry contains only necessary status, latency, request IDs, and usage; never content or credentials.

Proposed raw recording retention is seven days, configurable by policy. Transcript retention follows the conversation. Conversation deletion removes related stored and derived audio; retries must respect deletion and ownership changes. Check the cloud gateway's retention and tenant access policy before activation.

Speech can ask about approvals but cannot directly authorize them. A transcribed approval response must identify the pending item and request typed or button confirmation. This applies equally to platform-provided and gateway-generated transcripts.

## Delivery Plan and Acceptance

1. Verify the configured cloud gateway supports actual STT and TTS requests, and select authorized profiles and voices. Capability discovery alone does not count as acceptance.
2. Add shared audio ports, strict profile routing, private attachments, bounded cloud adapters, and durable stage state. Test missing profiles, cancellation, invalid media, provider errors, and secret redaction.
3. Connect each channel's verified audio download path. Test one real voice message per channel, tenant isolation, duplicate callbacks, unsupported codecs, and empty transcription. Raw codecs may require a sandboxed conversion step before STT.
4. Add consecutive-message collection and sequential conversation execution. Reproduce the text-before-image incident and verify the new image is included in the intended turn.
5. Add personal reply settings, TTS, channel-specific audio upload/send, and receipt handling. Test text fallback without duplicate assistant execution.
6. Verify voice approval cannot execute a decision, including platform transcripts. Verify private media expiration and deletion.

Enable each capability per channel only after its end-to-end test succeeds. Preserve existing text and image behavior during rollout.

## Required Deployment Inputs

The cloud gateway URL and secret reference should come from existing authorized profiles. Its audio protocol, available STT/TTS models, formats, quotas, and voices remain unverified. These must be resolved before production activation; do not invent a model ID or silently send private audio to a different service.
