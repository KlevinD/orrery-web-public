# Published documents to knowledge instances

The `ontology_document_inference` Agent tool publishes a watch for one visible
`knowledge_document` object. The owning Agent must currently have the ontology
inference tools with `ontology.manage` and `knowledge_search` with `knowledge.read`.
The runtime supplies the tenant and Agent identity; request arguments cannot
override them.

```json
{"operation":"publish","tenant_id":"runtime-tenant","watch_id":"contract-doc-1","root_id":"visible-document-object-id","expected_version":0}
```

Use `list` to find watches, `status` with `watch_id` to inspect the latest model
decisions and receipts, and `pause` with `watch_id` and `expected_version` to stop
future effects. `retry` with the current version schedules a fresh model attempt
after a deferral or failure. Resuming uses `publish` with the same immutable root
and the paused version. These operations do not require human approval.

## Execution

1. Resolve current Agent grants and ontology scope. Read the visible document's
   source locator and its enabled knowledge bridge binding.
2. Load the latest locally attested published revision, pinned by revision and
   SHA-256. Verify provider document, knowledge base, folder, publication state,
   MIME type and actual content bytes. Indexed PageIndex snapshots are supported
   for documents without editable revision history.
3. Offer existing knowledge identities from the same tenant and access boundary,
   filtered through the current object view and field policies. The model extracts
   a complete replacement for this document's claims and resolves identities.
4. Validate literal quotes, typed properties, offered IDs, local references and
   size limits. Persist the exact immutable plan. A second model call approves,
   rejects or defers that exact plan with a reason; preserve its model ID and hash.
5. Resolve current grants, source and schema again. Under a transaction, fence
   authorization, bridge head, watch version, identity versions and projection
   ownership. Apply `ontology.knowledge_instances.apply` and persist its action
   run, evidence, projections, current claims and durable receipt atomically.

Durable outbox tasks and 30-second bounded observations survive restarts. The
observation hash excludes the workflow's graph outputs, preventing feedback loops.
Duplicate jobs reuse prepared plans, decisions and receipts. Stale approved plans
become `superseded` and schedule new extraction instead of executing stale evidence.

## Domain semantics

`knowledge_entity` is an instance owned by `knowledge_instances`, with a kind such
as company, person, contract or product. It is not a native operations person,
legal entity or business contract. Those domains retain their own write commands.
Identity keys are scoped to tenant, document access boundary, kind and normalized
name. Model resolution can reuse an offered existing identity for aliases; it
cannot merge arbitrary identities or move them between access boundaries.

Attributes are typed, explicitly registered properties. Relations are supported
between newly created or resolved knowledge entities, plus document-to-entity
links. A revision replaces only its watch's claims. Other documents' claims
survive. Distinct values remain separate facts; a conflicting property is omitted
from the single-value object projection and listed in `conflicting_properties`.
The v3 independent reviewer can instead accept one offered source value or defer
the conflict, retaining all raw evidence; see
[document conflict adjudication](ontology-document-conflicts.md).
When all claims withdraw, the identity remains with status `withdrawn`, allowing
later reactivation and retaining receipt history.

Current fact retraction also removes that source stream's current assertion rows,
so withdrawn claims cannot continue as inference premises. Historical model plans,
source revision evidence and action receipts remain available for audit.

## Current bounds

This first version accepts at most 96 KiB of source text, 40 entities, 80 relations
and 20 attributes per entity. It fails closed if the same access boundary exceeds
200 stored identity candidates or model context budgets. It does not automatically
merge identities, rewrite native business records, or resolve contradictory source
values by selecting a winner. Schema evolution, unrestricted entity fusion and
historical numerical reasoning remain separate capabilities.

Freshness means the latest locally attested bridge head, rather than an atomic
transaction with the remote provider. Provider synchronization advances that head.
A new published revision with empty supported claims can withdraw this document's
claims through the same model workflow. Deleted or unpublished sources currently
stop new execution; automatic tombstone-driven claim withdrawal is not included.
No watch is created for every existing document automatically; the Agent publishes
watches for the documents it is authorized to maintain.

Tests use controlled models to verify orchestration, boundaries and transactions;
they do not measure a live language model's extraction accuracy.
