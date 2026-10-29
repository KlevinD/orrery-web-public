# Discussion Context Continuity

## Session Selection

Group, personal-assistant, direct, and mention turns carry prior run records to
the same session selector. Selection remains scoped to the current conversation,
physical/logical agent, runtime, compatible configuration, and resumable status.
Explicit retry still starts a fresh session. Website receptionist isolation is
unchanged.

Canonical runtime responses preserve only an allowlist of server-owned context
fingerprints, persona revisions, history checkpoints, and truncation diagnostics.
They do not copy provider settings, credentials, or approval decisions. The native
backend remains authoritative for its session ID.

Existing personal-assistant runs without the current persona revision are not
trusted for resume. Their next turn bootstraps a new session with recent history;
subsequent turns can resume the newly versioned session. No database backfill or
modification of old messages is necessary.

## Context Budget

The platform bounds injected context to its existing configured ceiling. Native
Pi, Codex, Claude, OpenClaw, and ACP sessions manage their complete model history,
native system prompts, and tools themselves. Server-side tool registration data
is not subtracted again from the platform's text injection allowance. Direct
model-call paths continue reserving model-visible tool schemas; output schemas,
secret bindings, and authorization metadata do not count as model input.

Discussion text is assembled from typed, server-owned parts before flattening.
Current input, identity/context boundaries, current approval material, and history
delimiters remain intact. Optional tool descriptions and older whole messages
yield to newer messages. User text is never parsed as section metadata.

If required content alone exceeds the allowance, the agent fails before invoking
the runtime instead of executing a partially clipped instruction. The prompt
builder exposes `llm_context_required_overflow` for diagnostics. Successful
trimming is recorded as `llm_context_truncated`; omitted source parts are not
retained in runtime request metadata.

## Attachments And Approval Dialogue

Image-only history retains attachment references. A fresh Pi discussion session
without current images can reload the most recent image-bearing message from
the same conversation, at most four images within the existing 16 MiB limit.
Resumed sessions use native image history. Expired historical files produce an
explicit unavailable-image notice without failing normal text chat. Existing
realpath, media-type, and workspace boundaries still apply.

Approval inquiry classification must distinguish explanation requests from
action consent. A sentence containing "approve" does not by itself disable tools
or force a fresh review session. This does not grant any new approval authority;
server-side HIL authorization and dedicated read-only review requests remain in
effect.

## Regression Coverage

When the retained context is insufficient, bound agents can retrieve original
messages with the [session history tools](session-history-tools.md), subject to
tenant, ownership, membership, and current-audience boundaries. This is separate
from native session reuse and memory-summary recall.

Tests cover canonical provider event projection, persisted two-turn continuation
with a recreated runner across all four discussion modes, production mention
wiring, structured trimming and overflow, logical-identity isolation, image
recovery, and approval-inquiry classification. Live model calls and production
database tests remain opt-in; unit tests do not execute real approvals or modify
production user data.
