# Formal task evidence in RSI

This increment implements part of ORR-RSI-01, ORR-WORK-01, ORR-TRACE-01 and
ORR-ONTOLOGY-01. It records what the formal task/QA transaction accepted. It
does not authorize candidate adoption or establish causal improvement.

- Before dispatch, the host captures a business binding for the original
  task/mission and immutable runtime request. It hashes task input and, when a
  host-owned sealed execution envelope proves the tenant, the formal contract
  and task version. Raw goals, reports, artifact locations and QA text stay in
  their owning domain.
- BaseAgent replaces any normalized attempt value with the durable trace
  session's host-generated attempt ID before formal commit. Submission stores
  this ID. Even retries sharing run and trace cannot inherit another attempt's
  acceptance. Legacy results without this field retain their domain behavior
  and acquire no guessed historical RSI association.
- TaskOutcomeEventSink delegates the original event sink and captures typed
  submission, QA approved/rejected, and QA-not-required receipts in the same
  transaction. Failures roll back both domain changes and receipts.
- A bounded reconciliation pass uses a durable rotating cursor. It associates
  receipts only after the original runtime terminal, verifies exact attempt,
  run, trace, agent, task, mission and execution tenant, and persists immutable
  business assessments plus an outbox. Awaiting QA does not starve later runs.
- Assessments distinguish qa_pending, qa_approved, qa_rejected, blocked and
  accepted_without_qa. The last status follows the original contract; no QA
  requirement is relaxed. Contract changes or unavailable execution contracts
  remain blockers. A missing authoritative tenant fails association closed.
- The existing ontology improvement sweeper projects separate
  improvement_business_assessment nodes linked to the original case and runtime
  review. Replay is idempotent; failed publication retains the outbox. Original
  request, terminal, case and runtime review bytes remain unchanged.
- `resourcectl improvements --tenant ... --attempt ...` adds the original
  business binding and recomputed assessments when available. It is read-only.

QA approval records an acceptance decision, not independently verified artifact
bytes or channel delivery. Causal attribution remains unresolved and the trusted
baseline remains incomplete. Frozen nine-domain inventory, a production baseline
verifier, independent candidate experiments, domain adoption/rollback adapters
and observation after activation still require further implementation and proof.
No new scheduled business job or user-message template is introduced.
