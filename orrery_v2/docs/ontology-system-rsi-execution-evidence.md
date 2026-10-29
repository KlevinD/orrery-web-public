# AgentCore execution evidence

Production AgentCore captures its resolved execution manifest immediately before
the first model call, using the dispatch context even when the session was
prewarmed. BaseAgent binds the host-only observer to the durable trace attempt.
The request, model prompt and provider metadata cannot supply the host binary
identity. Unknown engines and hosts without a readable executable retain V1
coverage gaps.

The immutable `orrery-runtime-execution-v1` sidecar records:

- actual executable SHA-256, optional adjacent source archive SHA-256 and build commit;
- normalized effective model/tool budgets, concurrency, timeouts, transport ceiling;
- checkpoint, compaction, prompt cache, tool journal and scope-fence policies;
- backend identity, opaque model configuration hash and restored counters/deadline.

It stores no input/output, credential, memory contents or tool arguments. The
model hash identifies the locally resolved adapter configuration; it does not
verify the remote model implementation, pricing or quality. Source archive
identity is supplementary provenance, not proof that an arbitrary archive was
used to compile an executable.

Capture must succeed before inference. Evidence failure ends the turn before
business execution. Execution capture and terminal finalization lock the same
request key. An identical sidecar can replay; a different sidecar conflicts.
Attaching evidence to an already terminal attempt is rejected. Recovery uses
the original persisted sidecar, never the current host's version. V1 request
hashes, observations, case IDs, reviews and graph events remain unchanged.

Attempts with execution evidence receive an immutable
`orrery-runtime-review-v2` review. Its
`improvement.runtime_review_v2.upserted` event passes the regular ontology
mapping/conformance gate into `improvement_runtime_review`. The graph carries
execution, runtime-policy and executable hashes and retains all nine domains'
coverage gaps. Operator inspection is available through
`resourcectl improvements --tenant <scope> --attempt <attempt>`.

This closes the link from actual AgentCore policy/version to task evidence and
ontology review. It does not establish complete dependency coverage, measured
business improvement, causal attribution or automatic production adoption.
`baseline_ready` remains false; the trusted runtime baseline verifier and
domain apply/rollback adapters are still required for a complete RSI loop.
