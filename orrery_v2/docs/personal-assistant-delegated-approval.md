# Personal Assistant Delegated Approval

## Behavior

The approval heartbeat can review and decide a pending approval on behalf of its
assigned human, **only with an explicit, owner-specific delegation grant**. Empty
configuration leaves automatic decisions disabled. Ordinary notification opt-in
does not authorize delegation. No frontend layout or existing conversation history
is replaced, and no real user Todo is completed as part of installation.

The first release supports:

- `information.read`, restricted to server-recognized read-only tools and an exact
  resource, or an exact private/source conversation for its runtime approvals.
- `development.create_commit`, only for an exact repository and with
  `allow_reversible_write: true`. This does not authorize pushing or merging.

All other action types stay human. This includes payment, deletion, identity or
permission changes, external publishing/sending, business approvals, councils,
high/unknown risk, and requests explicitly requiring manual HIL. Do not lower an
existing request's risk or remove a human-only flag to make it eligible.

For eligible requests, the worker makes one tool-free model call using the full
decision snapshot. The configured runtime approval reviewer profile is reused.
The call has an 8-second deadline, a 700-token response limit, strict JSON output,
and a 24 KB input bound. Oversized or incomplete material, absent evidence,
conflicting context, model errors, uncertainty or insufficient permissions keep
the original approval pending and continue its original human notification path.
The explanation and missing information are included in new human reminders.
Previously delivered messages are not resent just to update their explanation.

Before commit, the server checks current account/profile status, permissions,
private assistant binding/preferences, grant expiration, exact assignment, action,
scope and reviewed material again. PostgreSQL locks the reviewed decision row
through the transaction; workflow versions protect the current seat. A material
change, reassignment or human decision invalidates the old review. The assistant
cannot review its own request or fill another human's approval seat.

The worker retains the workflow version read before model review. A concurrent
decision in another seat changes that version, so the old review is not submitted
against a refreshed version. The owner's seat stays pending and the original
human reminder explains that the workflow changed; the model is not retried for
the same material and task.

The actual resolver is the assistant agent, never a forged human actor. Receipts
and task audit events include the human represented, consent reference, grant ID,
policy digest, scope and review fingerprint. A successful intermediate seat does
not complete a multi-stage workflow. Existing HIL observers handle continuation;
an approval receipt is not proof that the subsequent business operation succeeded.

## Configuration

Install owner-approved grants in the API service environment and restart through
the normal release process. Values below are placeholders, not an authorization:

```sh
ORRERY_PERSONAL_ASSISTANT_APPROVAL_DELEGATIONS='[{"id":"owner-approved-grant","tenant_id":"your-tenant","owner_id":"verified-owner-account","human_profile_id":"verified-human-profile","consent_reference":"owner-consent-record","expires_at":"2026-10-01T00:00:00Z","rules":[{"action_type_id":"information.read","scope":{"kind":"conversation","id":"owner-approved-conversation"}}]}]'
```

Grants are limited to 32, each with at most 32 exact rules. Unknown fields, missing
consent/expiry, wildcard resource scopes and unsupported actions fail startup.
Select a valid `runtime_approvals.reviewer.llm_profile_id` through existing settings.
The represented user still needs `approvals.read`, `approvals.decide` and
`runtime.action.execute` (read) or `development.manage` (repository commit).
The grant does not grant those permissions, create a channel binding, change
runtime sandbox policy, or enable arbitrary tools.

Runtime approvals created before the new policy-provenance marker remain human.
Legacy cross-tenant built-in approval records also remain human; a grant is not
implicitly extended from the owner's tenant to the shared workflow tenant.

There is no new public approval endpoint, agent tool or settings UI for installing
grants. Grant creation/change remains an operator action backed by explicit owner
consent. Approval text, model output and quoted WeChat messages cannot create grants.
The existing deterministic WeChat approval-reply route is unchanged by this feature.

## Context And Recovery

New passive notifications find the owner's existing private chat across verified
username/account/human-profile aliases, with exact tenant and owner checks. Already
persisted notices remain pinned to their original conversations.

On each private personal-assistant turn, both native and resumed external runtimes
receive recent notification records independently of chat-history timestamps. The
records include actual channel delivery status and freshly authorized Todo/approval
state. Group and Mission discussions do not receive this private context. Only
`delivered` means an external message was delivered; an in-app record or enqueue
does not. Content is bounded, escaped reference data, not an instruction or consent.

Automatic decisions and review failures create passive, idempotent in-app records.
Uncertain pending decisions retain the original WeChat reminder path. Automatic
results do not generate a new external push channel or one new HIL per scan.

The core database creates `personal_assistant_approval_reviews` only when grants
are configured and background workers are enabled. It stores one attempt per
decision/material/task/human, including grant evidence, review output and model
profile. A process restart never retries the same model review. Interrupted
attempts older than one minute reconcile against actual HIL receipts or task audit:
committed decisions are recovered; uncommitted reviews fall back to human handling.
Notification retries cannot generate another receipt.

Notices whose recipient is disabled, loses read access, or no longer owns the
private binding are suppressed (`notice_sent = -1`), never marked delivered. Their
review history remains stored; these notices cannot block later pending batches.

Remove grants and restart to stop new delegated decisions. Revoking the current
user's permissions, disabling their assistant/binding/preferences, or allowing the
grant to expire also blocks new decisions. Existing receipts and audit are retained;
an approval already committed cannot be undone by deleting configuration. Business
rollback, where available, is a separate authorized action.

## Verification

```sh
go test ./internal/app/hil ./internal/app/discussionsession
go test ./cmd/server -run 'TestPersonalAssistantDelegation|TestPersonalAssistantNotification|TestProductionPersonalAssistant'
go test ./internal/app/todo ./internal/gateway/outbound ./internal/app/runtimeapproval
```

Tests use isolated databases and controlled model/channel responses. They cover
real HIL commits, agent attribution, multi-stage seats, concurrent human changes,
permission revocation, stale material, missing evidence, malformed model output,
single review/restart behavior, notification identity aliases, current-state
context, tenant isolation and runtime prompt propagation. They do not approve
production work, complete the user's test Todo, or send real WeChat messages.

### Implementation Validation (2026-09-07)

- Full server, HIL, discussion-session, Todo, outbound-delivery and runtime-approval
  package tests passed locally on Windows, without skipping any test. The
  Linux/amd64 server cross-build passed.
- The original baseline exceeded the 800-line production-file limit in
  `production_message_gateway.go`. The three assistant approval reply functions
  now live in the existing approval reply module, with identical function bodies.
  `TestProductionServerFilesStayFocused` passes without changing its threshold.
- The workflow-version regression reproduces a concurrent human decision during
  model review. Before the fix the stale approval committed; after the fix the
  owner's seat stays pending, with one review and an explicit escalation reason.
  Workflow-version and concurrent-worker regression tests also passed 20 runs.
- The context-graph production gate tests passed locally. GitHub Actions did not
  start on PR #204: both workflows report an account payment/spending-limit block.
  No CI checks or merge controls were bypassed; an account administrator must
  resolve the billing issue before GitHub-hosted verification can run.
- Windows race-detector validation is unavailable in this environment because
  CGO is disabled and no C compiler is installed. No production model calls or
  real WeChat deliveries were exercised. Deployment and grant activation require
  separate approval.
