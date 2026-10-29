# Agent-authored channel error explanations

Channel adapters do not render gateway error events as chat replies. WeChat,
Feishu, DingTalk and email deliver Agent text; infrastructure failures stay in
processing state. Recoverable failures retain bounded retries and dead-letter
records. Non-recoverable failures terminate immediately without changing the
configured attempt limit. Email releases its deduplication marker on failure.

Failed voice transcription and unsuccessful approval replies supply verified
facts to the authorized Agent. No approval is inferred from a voice transcript.
An explanation turn disables runtime tools, skill resources and implicit tool
scopes. These constraints are enforced after profile bindings are resolved and
included in the current-turn context even for resumed PI sessions. They also
apply to other Agent runtimes through the existing runtime policy contract.

Failure to prepare a generated image for channel delivery creates one audited,
idempotent explanation turn against the original user message. It preserves the
original run and attachment, adds no user message, and does not regenerate the
image. Partial Agent text is withheld from external channels until final output
is available. Privacy filtering never invents a fallback assistant message.

When no authorized Agent can run, the system records status and retry information
without a synthetic chat answer. Unbound identities and invalid binding commands
remain subject to authorization checks. Website error SSE events contain status
codes without replacement dialogue; HTTP errors remain part of the API contract.
Approval requests, successful command confirmations and normal Agent explanations
remain available.

Validation covers transport suppression, retry classification, immediate terminal
failure, email retry/deduplication, voice approval safety, runtime resource removal,
PI resume context, explanation idempotency/authorization and image failure wiring.
