# Personal Assistant Notification Heartbeat

## Behavior

Personal assistant notifications no longer wait behind the shared serial sweeper.
Two independent, single-flight workers handle existing HIL/approval notifications
and explicitly enabled Todo/council notifications. Each starts with a catch-up
scan, wakes on committed changes, and checks again every 30 seconds. A scan has a
25-second cooperative deadline; external channel delivery latency is separate.

Committed HIL creation, workflow stage advancement, reassignment and resubmission,
and Todo creation/mutation wake the workers. The buffered wakeup coalesces bursts.
Database scans recover lost wakeups, process restarts and changes made by other
server instances. Workers cancel and join during gateway shutdown, and do not
start when background workers are disabled.

Without a separately configured [delegation grant](personal-assistant-delegated-approval.md),
polling and notification composition do not invoke a model, create HIL decisions,
start Agent turns, approve actions, or resume Missions. The assistant can still
explain approval material when the user actively asks in the conversation.

## Recipients and Scope

Existing HIL/approval notifications retain their private WeChat binding, enabled
assistant/preference checks and v4 idempotency keys. Unassigned and nonhuman tasks
are never sent. Pending-state checks also reject expired or reassigned decisions.

New Todo/council forwarding is **off by default**. Configure an explicit allowlist
in the API service environment, then restart through the normal release process:

```sh
ORRERY_PERSONAL_ASSISTANT_ATTENTION_RECIPIENTS='[{"tenant_id":"your-tenant","human_profile_id":"your-human-profile"}]'
```

Use verified tenant and human-profile identifiers, not guessed names. Configuration
rejects malformed JSON, unknown fields, duplicate/empty identities and more than
100 recipients. It does not create bindings or opt other users into forwarding.
The existing personal assistant notification toggle also disables these messages.

For each opted-in recipient, the worker reloads the enabled human profile, current
account, effective permissions, private channel binding and binding owner. Todo
queries use the normal authorized service, and each item is re-read before enqueue.
Council queries require a pending, open council in which that human participates
and has not voted. Permissions and private targets are checked again before enqueue.

Supported new reminders:

- Manual Todos assigned to that human, including conversation-linked handoffs.
- Todos awaiting that human's review.
- Blocked Todos assigned to that human or involving that human as creator/reviewer.
- Council decisions awaiting that human's vote.

These use `mention`, not `approval`, so they cannot become a target of the existing
WeChat approval-reply handler. Messages link to the relevant Todo or council view;
they do not invite approval by a generic reply.

Standalone Mission status, unassigned work, general chat mentions, recurring
overdue reminders and new Feishu/DingTalk personal-assistant routing are not added
by this change. Mission-related human action is covered when represented as an
assigned HIL/approval or Todo. Existing Feishu approval delivery remains unchanged.

## Delivery and Recovery

The durable gateway delivery/outbox owns channel retries and provider receipts.
Heartbeats check persisted idempotency keys before enqueueing new external records.
Todo keys identify tenant, item, recipient and attention kind (assigned, review,
blocked); title edits and repeated scans do not send again. Each attention kind is
sent at most once per item/recipient, including after reopening. Council keys are
per decision/recipient. Keys are not rotated on deploy.

Queue failures remain eligible on the next scan. For explicitly opted-in users,
an exhausted delivery can recover after a 30-minute cooldown while the source is
still actionable. Recovery grants four more attempts per batch, up to 24 total
attempts (the original 12 plus at most three recovery batches). Both the exhausted
outbox job and its delivery are reopened atomically, with cumulative attempts,
original content, delivery ID and provider client ID preserved. Pending, sending,
delivered and permanently rejected records are not reopened. Concurrent scans
cannot create another job. Recovery is driven by the heartbeat, not an inbound
user message. Actual WeChat delivery still depends on the provider accepting the
bound account and available context; no unverified context-expiry duration is
assumed.

Every personal assistant external send also rechecks the current source,
recipient, permissions and enabled private binding. Resolved, reassigned, expired,
already-voted or opted-out items are suppressed before provider IO, including
previously queued reminders. A concurrent change after that final check can
still race provider IO; source state and an external provider cannot share a
transaction. Temporary eligibility-read errors consume the bounded retry budget
without contacting the provider or exposing the underlying error.

For the same explicit allowlist, a passive notice is independently persisted in
the owner's personal assistant conversation and emits the existing realtime list
and message events. With no existing personal assistant chat, one private chat is
created without a user message or Agent execution. Each notification pins its
original chat and message identity, so restarts and new chats do not duplicate
it. In-app persistence survives external enqueue failures; realtime failures can
replay the already-persisted notice. Terminal external failures append a separate,
deduplicated historical failure notice while the source remains actionable.

Successful enqueue or an in-app notice is not proof of WeChat receipt. Inspect
the gateway delivery status and provider receipt. The in-app path is the fallback
added here; it is not an out-of-app push channel. This change does not enable or
send through new email, Feishu or DingTalk recipients. An additional proactive
external fallback requires an explicitly authorized channel configuration.

The notification-only path requires no new database migration, historical data
rewrite, automatic approval or frontend layout change. Disabling the allowlist stops new Todo/council enqueues,
their queued sends, recovery and the new in-app notices. Existing approval opt-in
preferences continue to control approval sending.

## Verification

```sh
go test ./internal/app/hil ./internal/app/todo ./cmd/server
go test ./internal/gateway/outbound
go test ./internal/app/discussionsession -run 'TestPersonalAssistantNotice'
```

Focused tests cover durable-commit wakeups, workflow reassignment, idle/startup
scans, burst coalescing, deadlines, shutdown, explicit opt-in, tenant/account/owner
and permission checks, pagination, resolved/reassigned suppression, retries and
restart deduplication. The integration test creates a real Todo, wakes a worker,
persists its delivery, dispatches through a fake WeChat sender and reconstructs the
worker against the same database. It does not send external test messages.
Recovery tests also cover cumulative retry caps, cooldown, concurrent recovery,
successful provider restoration without inbound messages, exact account binding,
pre-send source validation, passive in-app catch-up and realtime failure replay.
