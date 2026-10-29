# Personal assistant conversation read latency

The single-conversation repository previously paged through every conversation's
messages, members, turns, runs, events, mission links and attachments before
filtering in Go. In the production sample on 2026-10-17, three authorized reads
of completed two-message conversations took 3.078–4.322 seconds; their logical
tables contained approximately 90 MB of payloads. A turn performs several such
reads before execution, at completion and when closing its event stream.

Single-conversation reads now bind the conversation ID in the database predicate
and decode only matching records. Existing chronological ordering, equal-time
record-key ordering, full history and message pagination semantics are retained.
The repository remains an internal data access layer: viewer/tenant/membership
authorization still happens in the service. This change introduces no cache,
model substitution, new permissions, reply template or channel replay.

Affected design contracts: ORR-RUNTIME-01, ORR-AUTH-01/02/03, ORR-REPLY-01,
ORR-TRACE-01 and ORR-DEPLOY-01. PI and website bindings remain unchanged.
Regressions cover all seven aggregate parts, parameter binding and exact
conversation isolation, plus complete history and pagination beyond 1,000
records. Run the discussion-session package and the relevant production
personal-assistant, conversation-context and website isolation tests.

Live receipts, source/binary hashes and before/after latency samples belong in
`D:/Orrery/artifacts/personal-assistant-latency-20261017`. Model waiting and tool
execution remain separate latency sources; these small samples do not establish
a production latency percentile or prove WeChat delivery.
