# Document relation semantics and disputed fact premises

Document extraction v2 requires a directed `relation_code` from the host's
`relation_codes`: subsidiary_of, owns, supplies, customer_of, works_for,
party_to, produces, located_in, depends_on, related_to. The model chooses the
meaning and the independent reviewer checks the exact quote, code and direction.
Unknown codes fail validation. The original relationship description remains
source evidence; it does not become a new LinkType or a business-domain command.

The immutable plan, decision hash and atomic graph projection include this code.
Neighborhood and relation readers expose only the governed code after field
policy, without forwarding arbitrary link properties. A rule may narrow a
`knowledge_entity_related_to` body atom with `relation_code`, for example:

```json
{"kind":"link","type":"knowledge_entity_related_to","relation_code":"subsidiary_of","subject":"?subsidiary","object":"?parent"}
```

Omitting the code deliberately matches any meaning. A legacy relation without a
code cannot satisfy a narrowed atom. Codes are forbidden in conclusion atoms:
new source-owned knowledge links continue through their own domain command.

Before definite rule evaluation, the host groups eligible assertions by subject
and property. Different JSON values withhold that property's facts; equal values
retain their independent proof supports. An authoritative knowledge-entity
`conflicting_properties` marker also withholds that property when the visible
fact set is only a subset. Inactive assertions do not create conflicts. Whole
arrays are property values; this protocol does not add set-valued fact semantics.

`request.evaluation.fact_conflicts` persists subject, property, assertion IDs and
`status: disputed`. The authorized ContextPack retains the original source facts
and historical evaluations remain immutable. No winning source is selected by
timestamp, majority or host heuristics. The v3 document reviewer can adjudicate
complete, authorized conflicts through the existing atomic document command;
see [document conflict adjudication](ontology-document-conflicts.md). Until a
model accepts an offered value or sources become consistent, ordinary definite
rules cannot rely on disputed values.

When this removes the last proof of an existing derived relationship, the normal
continuous workflow asks the model to retract or defer, then applies any approved
retraction with the same ownership, authorization, transaction and receipt
checks. When supported values agree again, the model can reactivate the owned
relationship. There is no human approval step.

The document schema stamp includes the v3 conflict-review protocol and code
catalog, so existing active document watches re-observe and review old conflicts.
The rule semantics protocol is included in source and approval fences, so
published rule sets re-evaluate and old approvals or pending plans cannot cross
the changed premise interpretation. Work is bounded by the existing budgets.

No database migration is needed: optional fields use existing JSON columns.
Automatic source tombstones, clock-driven expiry, historical queries, strong
identity indexes and unrestricted schema discovery remain subsequent work.
