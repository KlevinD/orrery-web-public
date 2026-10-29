# Model adjudication of document fact conflicts

The independent document reviewer now chooses `accept` or `defer` for each
offered conflict, alongside approving/rejecting the exact document replacement.
No human approval step is involved. Accept names an existing assertion ID;
the host never accepts a generated value, an unknown assertion, a partial case,
or an omitted decision for an offered case. A matching value may retain several
independent source supports. The model must justify its choice from literal
quotes and document revision metadata, and defer when dates, definitions or
source quality cannot be reconciled. Recency and majority are not host rules.

`PreviewConflicts` freezes every source-owned claim for all affected identities,
including claims that the reviewer cannot read. Conflicts describe the state
after replacing this watch's assertions. The production host offers a complete
case only when the current agent can read every competing document AND every
competing fact through the policy-aware context reader, source bytes match the
published revision, and the cited quote is still literal. Unavailable, stale,
masked or oversized cases stay unresolved; they do not authorize a partial
choice. Provider/other dependency failures still retry the durable task.

The immutable document plan, model choices/reasons, input hashes and receipt
reuse the existing document batch tables. Each source assertion stores a source
stamp without full document text; legacy stamps are recovered from immutable
applied batches. Internal entity resolution records never enter candidate
prompts. Status rechecks source/fact visibility before exposing conflict reasons.
Generic graph evidence includes an audit reference instead of cross-source
free-text reasons. No database migration or additional tool grant is needed.

On accept, the entity property is the chosen existing value. That value's raw
facts remain active; competing raw facts become `disputed`, retaining their
values, quotes, original provenance and source evidence. Both fact projections
and static assertion records change through `SaveFact`. Thus the existing
policy-aware evaluation API admits only the accepted value as a definite premise.
On defer or omitted cases, all raw assertions remain available and the existing
conflict marker withholds them from definite inference. This is a projection of
model acceptance; it does not edit a document or a business-source record.

Every later replacement of an affected source invalidates the old resolution.
The fresh plan either resolves the complete current set again or restores the
disputed state. Removing the last contradictory source naturally restores a
single supported value. The continuous inference workflow observes these input
changes and asks its model to retract/reactivate owned derived relationships.
Previously recorded plans, choices and receipts remain immutable.

Before and after model latency, the host re-resolves current agent grants,
scope, document revisions and fact visibility. At commit it checks all source
heads, policy/profile/schema fences, the full claim-set hash, entity versions,
and every touched domain-owned projection. PostgreSQL serializes these writes
with the existing table fences. Source replacement, adjudications, raw/static
fact projections, evidence, action run, writeback and receipt commit together.
A duplicate request reuses the persisted first decision and exact receipt,
including after restart; a stale choice supersedes the batch and re-observes.

Budgets: 2,000 existing claims per affected document replacement, 40 offered
conflict cases, 40 assertions per case and 64 KiB total conflict evidence. Cases
that exceed the latter limits stay disputed. Existing source/model, identity
candidate and graph-neighborhood budgets still apply. Active watches re-observe
the v3 schema stamp after upgrade; paused watches resume processing when enabled.

This stage does not add automatic deletion/unpublication tombstones, timer-driven
expiry, historical temporal evaluation, strong entity identity indexes, or
adjudication of arbitrary assertions outside `knowledge_instances`.
