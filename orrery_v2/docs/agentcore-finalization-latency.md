# AgentCore reply finalization: 2026-10-19

Baseline: production commit `148651985f15215565a989fa642de3250738568c`,
including the previous `ae3f5a51` latency instrumentation and concurrent
maintenance shutdown changes. The design baseline is revision 7 of
`ORRERY-DESIGN-BASELINE-20261017`; this patch changes implementation, not its
behavior contracts.

Ordinary replies previously loaded full conversation aggregates during each
user and host mention scan, including a redundant load before the queue loop.
Mention candidates now use the exact structured-routing and legacy-link rules
shared with target selection. Messages without agent candidates return before
loading context. Host reply lookup loads persisted messages only, and the
post-policy mission lookup loads mission links only. Actual mentions still
load fresh membership and full request context on each queue iteration. Host
lookup remains after user-mention execution and uses the persisted author and
completion time, including the personal assistant's logical identity.

Turn finalization loads the conversation record and runs before the existing
aggregate mutation. Expected revision, active-turn fencing, terminal run
updates, transaction boundary and error propagation remain intact. The final
snapshot is still loaded after commit, preserving concurrent messages and all
other canonical fields. Final text, tools, memory capture, approvals, channel
delivery, website configuration, PI and other runtime selection are unchanged.

Additional correlated timings are `post_policy`, `turn_finalize_load`,
`turn_finalize_commit` and `turn_finalize_snapshot`. They contain IDs, status
and elapsed time, without message or prompt bodies.

Affected contracts: ORR-RUNTIME-01, ORR-TRACE-01, ORR-TOOLS-01,
ORR-AUTH-01/03, ORR-REPLY-01, ORR-MEMORY-01, ORR-STORAGE-01,
ORR-DELIVERY-01/02 and ORR-DEPLOY-01. Tests exercise no-op routing despite an
unrelated corrupt child payload, genuine mention context failures, structured
routing precedence, a concurrent revision conflict, fresh post-commit messages
and terminal event ordering. Existing mention chains, loop limits, approvals,
personal assistant identity, permissions and delivery regressions also apply.

Deployment and production timing evidence are stored separately under
`D:/Orrery/artifacts/agentcore-finalization-latency-20261019`.
