# Document entity identity and candidate recall

The document instance pipeline uses three model calls for a nonempty revision:
literal mention extraction, identity resolution against relevant candidates, and
independent review of the exact replacement plan. Empty revisions need only
extraction and review. Semantic decisions are made by models without a human
approval step; the host validates evidence, scope, versions and transactions.

## Qualified identity

Companies may provide up to four `identifiers`, each containing `scheme`,
`jurisdiction`, `authority`, `value` and a literal source `quote`. Supported
schemes are `company_registration` and `tax_registration`. The quote must include
the number, jurisdiction and issuing authority. Values preserve punctuation and
leading zeros while case and whitespace are normalized. Missing scope cannot
produce a strong identity; an unqualified `registration_id` remains a recall hint.

Bindings are unique within the exact tenant, document access boundary, entity kind
and normalized qualified identifier. Two companies with the same name and different
registrations can coexist. Different numbers in the same scheme/jurisdiction/authority
cannot be merged into one entity. Equal numbers in different scopes do not establish
sameness. When several identifiers are offered, any existing binding prevents creation
of a second identity, even if the primary deterministic identifier differs.

A new qualified entity ID derives from a sorted identifier; a new unqualified ID
derives from the document and its local mention reference. Names are never global
unique keys. Models must explicitly choose an offered `existing_id` to reuse an
identity. Existing IDs, other sources' claims and immutable receipts survive upgrades.
Previously merged legacy entities are not automatically split or rewritten.

## Recall and permissions

The first model call extracts grounded mentions without candidate IDs. The host
queries scoped indexes for their qualified identifiers, unqualified registration
numbers, names, aliases and name tokens, including Chinese bigrams. Entity kind is
filtered before the candidate budget. Previous claim subjects and relation endpoints
are mandatory candidates so a replacement can withdraw its own older assertions.
Their scope and readability are checked before previous assertions enter the
first model prompt, then checked again after extraction during relevant recall.

At most 200 relevant candidates are returned; having more than 200 total entities
in the access boundary no longer blocks extraction. Legacy index repair uses pages
of 100 and never replaces business projections. New commits update indexes in their
existing transaction. Cold repair may scan the access boundary in the database.

Every candidate is read through the ordinary graph object and field policies, then
matched again using only visible fields. Raw index aliases, legacy keys, registration
metadata and adjudication reasons cannot enter the model context. Mandatory endpoints
that cannot be read cause the replacement to fail closed. Model candidate context has
a 64 KiB budget; the complete model input retains its existing 192 KiB limit.
Omitted or unreadable candidate context marks recall incomplete without exposing
hidden IDs or policy details, so it cannot justify creating a duplicate identity.

Recall summaries record query hash, offered IDs and truncation reasons. Truncated
recall cannot prove that an entity is new: unresolved creation is persisted as a model
defer without business writes. An explicitly resolved existing identity may proceed
subject to all usual source, conflict and independent review checks. The resolution
call may set only `existing_id` and the top-level reason/defer; changing unsearched
material is rejected. Recall is bounded and does not claim exhaustive fuzzy matching.

## Persistence and history

Migration `0245_ontology_entity_identity` adds qualified binding, recall term and
index version tables. The default static `knowledge_entity` definition receives only
an additive, versioned `identity_keys` array property; customized owners and existing
properties are preserved. An incompatible schema prevents document processing.

The immutable plan includes identity assignments and the recall summary. Source
assertions, identity bindings, term indexes, graph projections, action writeback and
receipt commit atomically. A registration collision or late action failure rolls
everything back. Concurrent stale plans conflict; the existing supersession and
observation mechanism schedules a fresh model decision. Exact duplicate executions
return the original durable receipt.

Qualified bindings survive source withdrawal as identity history. They can recall
and reactivate the same historical entity but are not active business assertions.
The production inference API excludes `identity_keys` from both explicit fact inputs
and facts synthesized from object properties. No registration metadata alone can
support a business conclusion through that API. Entity merge/split commands, business
validity intervals and a shared retrieval evidence contract are separate work.
