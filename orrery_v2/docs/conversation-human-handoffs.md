# Conversation Human Handoffs

An Agent with `todo.write` can create a manual, human-assigned Todo linked to an existing conversation using `todo_create.source_conversation_id`. A Mission is not required. The request must include the human's known identity and a description of the blocker, attempts made, and requested action. This does not replace protected-action approval.

The creator must already have access to the source conversation. Creating a Todo does not invite its assignee to that conversation. The assignee can read the explicitly shared Todo context and comments; the source link is shown only when the detail viewer also has conversation access.

## Human Workflow

1. The source conversation receives a passive notice with a Todo link. The assigned person's work inbox and right sidebar show a human-action item.
2. The person opens the Todo, updates its normal workflow status, and can discuss the problem in comments.
3. Completing the Todo requires a nonempty `result` supplied by a human actor. The frontend action is "Complete and return result". An Agent cannot complete this handoff on the person's behalf.
4. A passive result notice is delivered to the source conversation. This does not interrupt an active turn or automatically authorize/start another Agent turn; the next conversation turn can use the returned result.

## Delivery and Recovery

Notice payloads and stable idempotency keys are persisted with the Todo mutation. Delivery uses the existing idempotent discussion notice service. `todo.source_notices` retries pending notices independently of scheduled Todo automation. The original creator's current conversation access is checked at delivery; revoked access leaves the item pending and does not broaden permissions.

The inbox requests `include_pending_delivery=true` so completed handoffs remain visible until their result is delivered. Other Todo list queries retain their existing completed-item filtering. Optimistic concurrency prevents delivery retries from overwriting concurrent human edits. New fields are additive to the existing record payload; no schema migration or historical data rewrite is required.

## Verification

- Todo service tests cover creation, persisted retry after restart, human-only completion, required result, duplicate completion, tenant isolation, canonical assignee inbox matching, private source access, and ReBAC enforcement.
- Existing discussion notice tests cover passive/idempotent delivery without interrupting active turns.
- Browser tests cover the inbox, right sidebar, required result form, completion response and responsive layout at 1440px and 390px.

Existing standalone Todos are unchanged; source associations are not inferred or backfilled. Comments are not automatically forwarded, and there is no automatic Agent selection or continuation on human completion.
