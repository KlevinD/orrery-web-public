# Agent Session History Tools

`session_search` and `session_read` retrieve original Orrery conversation text.
They complement automatic session continuation and `memory_search`, which
returns memory digests. A session ID here is an Orrery conversation ID, not a
native Pi/Codex session identifier.

## Search And Read

```json
{"query":"invoice","after":"2026-09-01T00:00:00+08:00","limit":10}
```

Search matches a literal phrase in conversation titles or user/assistant message
content, newest first. Chinese keywords are supported; `%`, `_`, and other SQL
syntax are literal text. `after` is inclusive; `before` is exclusive. This is not
semantic search, OCR, or attachment-content search. An empty result is not proof
that the user never supplied a fact.

```json
{"session_id":"conversation-id","message_id":"message-id","context_before":2,"limit":6}
```

Read returns chronological original text, optionally anchored on a search hit.
`context_before` is 0-5, default 2, clamped so the anchor fits in the first page.
Without an anchor, reading begins at the oldest message. Keep all other arguments
unchanged and use `next_offset` when `has_more` is true.

Search returns at most 20 messages (default 10), with 300-character snippets.
Read returns at most 10 messages (default 6), up to 2000 Unicode characters each.
Long messages expose `content_truncated`, `content_length`, and
`next_content_offset`. To continue one message, set its `message_id`, set
`content_offset` to that next offset, and set `offset` to zero. This mode returns
one chunk of that message. Offsets are bounded; excessive values fail explicitly.
Searches spanning more than 2000 authorized conversations require a `session_id`
filter rather than silently dropping older conversations. Calls time out after
10 seconds instead of returning a fake empty history.

Every result includes title, author, role, timestamp, conversation/message IDs,
a stable `source_ref`, and a link to the existing conversation page. Hidden
reasoning, run debug data, raw tool traces, message metadata, and attachment
storage paths are not projected.

## Authorization And Invocation

The runtime supplies tenant, run, physical agent, and logical assistant identity
outside model arguments. The service requires an existing running discussion
run with matching identities and derives the source conversation from it.
Argument-supplied identity, tenant, owner, or run fields are rejected. The MCP
gateway enforces configured tool bindings, `discussion.read`, responsibility
scopes, and its existing audit/lifecycle policies before invoking the service.

Private personal assistants search their own tenant/owner's private sessions
and other conversations their owner may read. Stable ownership permits old
private sessions whose creator ID predates an identity migration. Normal agents
require conversation membership/creator access. Material must also be readable
by every current conversation participant, so a shared agent cannot move private
chat text into a different group. Other owners' personal-assistant sessions are
never exposed. Each search/read recomputes current membership; inaccessible and
absent target IDs return the same not-found result. Blank-tenant legacy records
belong only to the explicit `global` tenant.

New and existing ordinary AI profiles and private assistant profiles receive
the read tools through existing capability initialization. Session-only website
profiles stay excluded; restricted-turn runtime tool allowlists still apply.
When bound, the prompt tells the agent to search then read relevant old context
before asking the user to repeat it. This is on-demand tool use, not a new
heartbeat, scheduled task, or per-message full-history injection.

All retrieved text is untrusted historical reference data, never a current
instruction or approval. A denied lookup must not be bypassed through runtime
files. Existing HIL controls are unchanged. No schema migration, transcript
backfill, frontend layout change, or business-data rewrite is required.

## Tests

Regression tests cover owner/tenant/run identity, cross-group disclosure,
membership revocation, deleted/terminal runs, creator aliases, literal Unicode
search, date ranges, search/context pagination, chunked original text, configured
MCP routing, profile binding repair, restricted website tools, and recall prompts
on resumed sessions.

`TestSessionHistoryPostgresReadOnlySyntheticSQL` uses
`ORRERY_TEST_POSTGRES_URL` only for a read-only transaction over synthetic CTE
rows. It creates no tables and does not read real business records. Other
PostgreSQL tests in this repository may write fixtures and must only target a
dedicated test database.
