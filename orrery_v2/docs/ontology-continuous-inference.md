# Continuous ontology inference

An authorized Agent can publish deterministic rules for one fixed root through
`ontology_inference_rules`. The host persists each immutable publication, the
active version, observation cursor, candidate dependencies, model judgments and
effect receipts. No human approval is required for this inference workflow.

```json
{
  "operation": "publish",
  "tenant_id": "runtime-tenant",
  "rule_set_id": "project-mission-watch",
  "expected_version": 0,
  "root_id": "existing-visible-project-id",
  "rules": [{
    "id": "supported-mission",
    "version": "1",
    "when": [{"kind": "link", "type": "source_relation", "subject": "?p", "object": "?m"}],
    "then": {"kind": "link", "type": "derived_relation", "subject": "?p", "object": "?m"}
  }]
}
```

The example type and object IDs are placeholders. Endpoints and schema must
already exist, and executable relationship types must explicitly allow
`ontology.derived_link.assert` with many-to-many cardinality. The Agent must
currently have the inference, workflow and rule publication tools with
`ontology.manage`, in addition to normal scope and source access.

Publication and its first outbox task commit atomically. Every 30 seconds a
bounded sweeper checks due publications for source or authorization changes;
it commits its cursor and the corresponding evaluation task together. Outbox
leases, heartbeat, retries and dead-letter status use the platform worker.
Pausing or publishing another version fences old tasks at commit time. Equal
publication retries return the same version; changes require the current
`expected_version`. `list` returns this Agent's rule sets; `pause` preserves
existing assertions and stops further tasks for the publication.

An evaluation rebuilds the authorized two-hop input. Each candidate fingerprint
includes its grounded proof graph, relevant source objects and evidence content,
rule bodies and versions, schema constraints and competing assertion sets.
Empty competing sets are dependencies too. An unrelated visible object's label
does not invalidate the candidate. Relevant object records are currently tracked
as a whole. The current dependency index and each run's historical dependency
ledger make the support inspectable. Derived edges and their generated evidence
cannot become independent support for themselves.

Only changed executable relationship candidates go to the model. The model gets
the fingerprinted evidence and the exact relationship preview and returns
`approve`, `reject`, `retract` or `defer` with a reason. The host strictly parses
the response and persists it before calling the existing review, plan and apply
workflow as the stored Agent owner. It rechecks current identity, permissions,
publication version and sources at each step. The preview must still match before
apply. Failed model calls do not approve anything. A deferred decision is retained
and leaves a visible failed/dead-letter task after the retry budget; changing
support produces a new decision task.

An applied candidate whose proof changes becomes `revision_required`. Tracking
does not itself delete a relationship. The model may approve a supported revision
or decide to retract its owned assertion. The command checks that the assertion,
current receipt, source owner and projection still agree. It commits the graph
effect, evidence, ActionRun, writeback receipt and candidate state in one database
transaction. Retraction removes only the owned current projection. Reactivation
is possible when support returns. Immutable effect receipts remain replayable by
their original plan and idempotency key even after later revisions/retractions.

The observer currently detects changes using a bounded tenant source digest,
then recomputes the published query's bounded closure. This is not a per-fact
incremental Datalog engine: the dependency fingerprints avoid unnecessary model
judgments, while source observation and the short write fence remain conservative.
Existing context limits and source fence budgets still apply. Missing endpoint
access, conflicting ownership, policy changes or incomplete input fail closed.
Submitted document quotations still need attested source revisions before they
can execute. Cross-publication ownership transfer, entity merging, new-object
batches, arbitrary property writes and SHACL remain separate capabilities.
