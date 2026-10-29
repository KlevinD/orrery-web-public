# AgentCore conversation latency

This change reduces conversation-layer overhead shared by AgentCore and the existing PI, Codex and Claude adapters. Runtime selection, model settings, website routing, approvals and reply content are unchanged.

Personal assistant notification material is prepared once for a request, including empty results and the bounded error fallback. An in-process marker binds that preparation to the run, message, conversation, tenant, owner, Agent and execution/viewer identities and permissions. The marker is cleared before delegation, so a continuation refreshes the state even when its run ID is unchanged. Transport serialization cannot preserve the marker. This is request-local reuse, not a shared notification cache.

Conversation classification, member-addition policy and the turn-start revision retry read the main conversation record without loading messages, runs, members and other child records. Access checks still load their required membership data; the turn mutation retains its revision guard and execution lease.

Structured `discussion.stage` logs expose monotonic `duration_ms`, stage status and conversation/turn/run IDs. Stages cover stream preparation, turn start, run context, run creation, context/preferences/prompt-debug preparation, runtime execution and first emitted text, assistant message storage, memory capture and run/turn finalization. Stages are nested; their durations must not be summed. A completed stage means that operation returned successfully, not that the business task succeeded. Stages that fail before creating an ID leave that ID empty. First-text timing is emitted only when text arrives. Logs contain no prompt, notification body, reply, credentials or tool arguments.

Required persistence and personal assistant memory capture retain their existing order before terminal events. This release does not introduce asynchronous finalization or a background memory outbox.

Behavior contracts preserved: ORR-AUTH-01/03, ORR-RUNTIME-01, ORR-RECOVERY-01, ORR-TRACE-01, ORR-MEMORY-01, ORR-REPLY-01, ORR-DELIVERY-01/02 and ORR-DEPLOY-01. Regression covers managed/device routing, streaming/non-streaming preparation, empty/error notifications, fresh continuations, identity changes, transport boundaries, metadata-only lookup and timing privacy/correlation.
