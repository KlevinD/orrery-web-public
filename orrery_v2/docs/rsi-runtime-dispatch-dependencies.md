# Original-attempt dispatch dependency evidence

Contracts preserved: ORR-RSI-01, ORR-TRACE-01, ORR-RUNTIME-01,
ORR-ONTOLOGY-01, ORR-REPLY-01 and ORR-STORAGE-01.

The in-process AgentCore reports host observations when model/tool callbacks
actually enter. These are distinct from request bindings, model tool-call
suggestions, authorization checks and callback admission failures. Each model
entry hashes the resolved messages, exposed tool schemas and model identity.
Each tool entry identifies the actual bound tool/version and hashes its binding,
validated arguments and call identity. Credentials and raw content are excluded.
A callback entry does not prove remote acceptance or business success.

The host-only context observer is bound to the original trace attempt. A hash-only
receipt is frozen into terminal evidence and committed with its original review,
case and projection outbox. The existing private feedback journal retains that
same receipt on a database outage. Recovery never rereads current bindings or
executes business work. Legacy terminal bytes remain compatible via optional
fields; older engines retain missing evidence instead of historical backfill.
Journal recovery scans at most 16 entries per call to bound aggregate memory;
its retained cursor prevents large or corrupt entries from starving later work.

Collection is limited to 512 entries and 512 KiB per attempt. Count/byte overflow
and invalid host observations are explicit. Finalization freezes observations;
late callbacks cannot rewrite the immutable receipt. `runner_returned` records
the runner returning, including errors; it does not attest completion of abandoned
callbacks, delivery, knowledge retrieval, or the entire workflow. A crash before
terminal finalization can still lose the in-memory dependency observations.

The existing improvement projection creates a typed
`improvement_runtime_dependencies` node with links to its original case and runtime
review, preserving original tenant/access scope. It projects counts and hashes,
not raw model/tool inputs. Parent nodes precede links, and replay is idempotent.
`resourcectl improvements --tenant ... --attempt ...` exposes the receipt at
`terminal.dependencies` through its existing read-only inspection path.

This is evidence for freezing future baselines, not a replayable baseline package.
All nine domain gaps, `baseline_ready=false`, unassessed business quality and
unresolved causal attribution remain. Skill-package bytes, exact knowledge/memory
revisions, remote implementations, independent paired business evaluation and
production adoption/observation/rollback require separate evidence and wiring.
No extra worker, scheduled task, user notification or memory injection is added.
