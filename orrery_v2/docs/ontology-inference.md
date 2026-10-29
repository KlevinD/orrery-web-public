# Ontology inference and knowledge candidates

Orrery now evaluates positive, function-free rules over a bounded, authenticated
context and records their conclusions as review candidates. This adds the rule
evaluation and support tracking identified in the Semantica comparison without
changing the ownership of business facts or the ActionContract execution boundary.
The implementation is native Go and adds no Python runtime dependency.

## Entry points

- `POST /api/ontology/inference/evaluate`: requires ontology manage capability and
  canonical scope manage authorization.
- `ontology_infer`: standard Agent/MCP tool with the same service and manage
  permission. The trusted runtime tenant overrides an argument tenant. It does
  not add permissions to the calling principal.
- `GET /api/ontology/inference/schema-export?tenant_id=...`: requires ontology read
  capability and canonical scope read authorization; returns OWL and SHACL Turtle
  plus the Registry JSON sidecar.

Example rule request (type/property/link identifiers must exist in the scope):

```json
{
  "tenant_id": "organization:example",
  "root_id": "project-P",
  "rules": [{
    "id": "delayed-project-risk",
    "version": "1",
    "when": [
      {"kind": "fact", "subject": "?p", "predicate": "status", "value": "delayed"},
      {"kind": "link", "type": "project_supports_mission", "subject": "?p", "object": "?m"}
    ],
    "then": {"kind": "fact", "subject": "?m", "predicate": "risk", "value": true}
  }]
}
```

Rules are query input, not published production rules. IDs and versions, the
canonical rule hash, filtered input snapshot and its hash are saved in the run.
Only subject/object positions accept `?variable`; values are literal JSON. Heads
may derive facts or links, never objects, metadata changes, callbacks or actions.
Object IDs remain case-sensitive and conclusion identities include the tenant.
The positive fixed-point evaluator supports recursive rules. It does not implement
negation, aggregates, arbitrary functions or OWL description-logic reasoning.

## Support tracking and withdrawal

Each premise has a stable identity tied to its tenant, assertion reference and
actual selected value. Each derived conclusion records every direct rule firing,
the rule version and premise IDs. Multiple independent source assertions remain
distinct supports. Rule determinism does not imply certainty of source assertions;
the evaluator does not assign a probability of truth to derived candidates.

Supply `previous_run_id` to compare with an earlier evaluation by the same
principal for the same root and context bounds. The service recomputes the least
fixed point from the new visible inputs. It returns `retained_ids` and
`withdrawn_ids`; it never returns old values or quotes through this comparison.
Removing one support retains a conclusion supported by another. Removing the last
external support also removes conclusions in recursive cycles. Historical runs
remain immutable records. The ledger uses the existing `ontology_reasoning_runs`
JSON columns, survives service recreation, and requires no new database migration.

Withdrawal applies to the derived candidate set. It does not silently remove an
already approved business fact. Such a business change requires a new governed
decision and the appropriate domain/ActionContract operation.

## Knowledge material

The optional `documents` array accepts requester-submitted text and an
`extractor_version`. Structured claims include `quote` and `candidate`; the quote
must literally occur in that text. The service also extracts the existing
`ontology.assertions` YAML frontmatter format. An existing model or external
extractor can supply quoted natural-language claims through this contract.
This release does not add a new LLM extraction provider or automatically infer
arbitrary business relationships from unstructured text.

Entity claims get tenant-scoped proposed IDs from their type and canonical key.
Link and fact claims resolve only against the visible objects in the bounded
context. Missing/ambiguous entities are rejected rather than guessed. All valid
claims become review proposals. Claims do not feed the rule engine as established
facts until they have passed the separate business governance process.

The ledger saves source hashes, byte counts, extractor versions and quoted/anchored
evidence. Full submitted documents are not stored in the reasoning run. Submitted
material is not represented as an attested knowledge-store revision; citation
matching establishes traceability, not the truth of the quoted statement.

## Quality, authorization and budgets

Candidate checks use the scoped Registry: object identity/type, link direction
and cardinality, fact value type, required entity properties and evidence.
Rejected candidates never get proposals. Evaluation produces review candidates.
An authorized Agent can judge a candidate and execute the relationship command
described below without human review or confirmation.
OWL and SHACL exports represent classes, property types, required fields and
outgoing cardinalities. JSON preserves tenant, authority, sensitivity, temporal
and allowed-action metadata. JSON/array/ref encodings, inverse cardinalities,
duration normalization and evidence requirements need host validation.
Export is not an OWL consistency proof or an ontology migration/import API.

Production context is selected through the existing scope-authorized Context API,
then additionally filtered for both workbench and reasoning purpose policies.
Masked object properties and same-name facts are excluded together, including
fact-only representations. Redacted fact values/predicates/status do not enter the
engine. Authorization and binding revisions are checked before and after
evaluation. Only visible evidence references are retained.

Production requests use current context. The recorded time coordinates describe
capture time; they do not claim an atomic historical database snapshot. Historical
date inputs remain unavailable in this API/MCP tool. Internal evaluation can use
an already selected historical ContextPack with explicit time coordinates.

Limits: two graph hops, 25 objects, 100 links, 100 facts and evidence per object,
1,000 facts total, 32 rules, six atoms per rule, 200,000 evaluation work units,
4,000 derivations, 500 candidates and a 15-second request deadline. A global
limit of two inference calls and one per principal bounds concurrency. Submitted
material is limited to eight documents of 128 KiB each, 200 claims total, and
HTTP/MCP input is limited to 2 MiB. Incomplete context and exceeded inference
budgets fail the request; no partial closure is presented as complete.

The earlier ContextBundle historical-selection and field-masking fixes remain
part of the production source baseline. This feature does not alter their API.

## Current candidates and complete proof paths

Structural validation propagates through the proof graph before proposals are
created. A rejected intermediate conclusion cannot justify a downstream one.
An independent valid proof retains the conclusion; invalid proof branches are
removed from its evidence traversal. Candidate cardinality is checked against
established context relations, so a rejected hypothesis cannot reserve a slot.

Each evaluation returns `current_candidates` separately from its immutable run
ledger. State is persisted by tenant, original requester and root query. A
candidate's `support_hash` covers the complete authorized context, Registry and
rule versions and submitted document receipts. A changed hash clears an approval
and plan. Missing candidates become `withdrawn`; previously applied assertions
become `revision_required`. `withdrawn_ids` includes document candidates as well
as rule conclusions. `revised_ids` distinguishes stable conclusion identity from
changed supporting inputs. Fact conflict issues identify differing existing
values, registered authority sources and conflict policies; they never overwrite
the established value automatically.

Reevaluation is explicit. This release does not install event-driven inference
subscriptions or automatically retract an applied assertion from the graph.
Applied inference edges cannot act as independent premises for a new evaluation,
which prevents an old assertion from justifying its own continued validity.

## Model decision, preview and relationship execution

The workflow requires an authenticated Agent or user with `ontology.manage` and
scope manage access. The calling model judges the evidence itself, records an
approve or reject decision with a nonempty reason, inspects the exact planned
relationship and confirms execution. Human review or confirmation is not needed.
The server reconstructs the same supporting input using the decision maker's
current permissions and purpose policies. The actual Agent ID and actor kind are
recorded in candidate state, execution records and receipts.

Agents call `ontology_infer`, then `ontology_inference_workflow` with operation
`review`, `plan` and `apply`. Runtime identity, permissions and tenant override
model arguments; the tool cannot impersonate a human or grant itself scopes.
The default Ontology steward has both tools and instructions to complete this
workflow autonomously. Existing custom prompt content is preserved when adding
the model workflow instructions.

The same workflow is available over HTTP:

1. Evaluate at `POST /api/ontology/inference/evaluate`. Select a current candidate
   and retain `run_id`, `candidate_id`, `revision` and `support_hash`.
2. POST those references to `/api/ontology/inference/review`, with
   `expected_revision`, `decision: "approve"` (or `"reject"`) and an optional
   decision reason (required for Agents). Use the returned revision for the next request.
3. POST the references to `/api/ontology/inference/plan`. The response contains
   the exact relation type and endpoints. A plan lasts ten minutes and binds the
   planner, current security revisions and supporting input.
4. POST to `/api/ontology/inference/apply`, adding the returned `plan_id`, a stable
   `idempotency_key` and `confirmed: true`. Retain the returned receipt. An exact
   retry by the same planner returns that receipt after restart as well.

The initial command is `ontology.derived_link.assert`. A relation definition
must have `suggested_allowed: true`, `cardinality: "many_to_many"`, and include
that command in `allowed_actions`. Enable a suitable relation definition through
the existing definition governance workflow before applying candidates. A
dedicated derived relation type makes its meaning and source ownership explicit.
Other relation types, new entities, fact changes and requester-supplied document
claims are not executable by this command. Document approvals need an attested
knowledge source and revision integration first.

The model's decision does not override schema, source, tenant or permission
checks. A rejected candidate creates no relationship. The plan and confirmation
steps are performed by the model; they do not create a human approval task.

The command owns an `ontology_inference_links` assertion and an associated graph
projection. One transaction persists the assertion, evidence, relationship,
completed ActionRun, writeback receipt and candidate state. It refuses an ID
already owned by another source. Source and Registry data are checked under a
commit fence along with scope, authorization and policy revisions. PostgreSQL
locks the relevant input tables for this short commit; SQLite uses its writer
lock. The internal source digest is bounded to 20,000 rows; current session
metadata is bounded to 1,000 candidates including retained applied receipts.

A 409 response means the candidate, source, expected revision or plan has changed;
evaluate again and review the new result. The typed web client is
`ontologyInferenceApi`. The existing web assets have no new inference screen.
