# Personal assistant system updates

Scheduled task results continue to use the existing scheduled-result subscription and the executing Agent's public report. No new business schedule is created.

System errors, RSI milestones and automatic repair progress share the existing personal assistant Agent writer and WeChat delivery queue. Enable explicit source-to-recipient routes with `ORRERY_PERSONAL_ASSISTANT_SYSTEM_UPDATE_RECIPIENTS`:

```json
[{"tenant_id":"organization-a","human_profile_id":"human-a","source_tenant_id":"organization-a"}]
```

The destination identifies the assistant's organization and human owner. The source identifies the operational scope to read. An organization route does not imply system/global or other-organization access. Current account, profile, binding, operational permissions and ontology scope read authorization are required. As in the reliability UI, host repair receipts require additional scope management authorization; read-only viewers receive basic fault and governed execution status without loading these receipts. RSI additionally checks the observation's original resource ACL. These checks repeat before generation, enqueue, external delivery and use as assistant context. Configuration grants no permissions.

Activation creates a durable time fence for each route. Existing history is not replayed. The fence survives restarts. Removing a route blocks queued deliveries; re-enabling the same route retains its original fence and resumes later events.

A 30-second background heartbeat reads the original failure, improvement and governed repair ledgers. It starts at most one Agent explanation per heartbeat. The durable writer uses its existing five-minute model retry backoff. Notifications cover current milestones; several transitions between scans may be coalesced into the latest phase. Repeated occurrences within the same fault phase do not regenerate or resend the message. A new phase or a fault reopening after recovery has its own delivery identity. Source pagination and pending processing retain separate durable cursors.

Fault messages cover error/critical incidents and exclude warning-only conditions and failures originating from notifications or report-only Agent runs. Automatic repair uses current host receipts bound to the exact incident occurrence, plus governed repair execution status. A resolved host receipt requires verification, evidence and a real repair run. Pending/retry receipts for an already observed incident are refreshed even when its incident row did not change. A pre-activation incident first becomes eligible when its canonical ledger changes after activation.

RSI notifications begin with diagnosed cases, excluding raw observations and managed-repair feedback already covered by the fault stream. Material includes the actual stage, diagnosis, goal and evaluation measurements when present. A proposal, evaluation or technical recovery is distinct from activation, measured improvement and completion of the original business task.

The personal assistant authors the explanation in a read-only Agent run. Queues and retries preserve that report verbatim. No platform fallback message is sent if generation fails. The host neither executes repairs nor approves changes as part of this subscription. Existing business approval and QA remain authoritative. Delivery status and business status remain distinct in assistant context.

Durable metadata uses the jobs database's existing `platform_records`: `personal_assistant_system_update_cursors` and `personal_assistant_system_update_phases`. No schema migration is required. They are delivery state, not another source of truth for faults or RSI.
