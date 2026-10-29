# Laya memory relevance observation

This change extends the existing Laya shadow observer to semantic memory recall. It is composed on the exact production `context-integrity-20261015-1405f468` source archive, SHA-256 `9aa69c296f595e3bd3275dbc18d0302df2e6931ca2c49e6ba16bfaa35106c9b6`. That snapshot already contains the RSI runtime/publication and temporal context fixes. The older GitNexus root index is not the source of truth for this composed release.

## Behavior

`pageindex.Service.Recall` first checks profile access, document identity, canonical record identity, lifecycle and duplicates. Only its final authorized records are observed. Production wires the same observer into the recall service used by discussion memory, explicit memory search, and memory distillation source recall.

For up to six records, Laya classifies an excerpt as `direct`, `partial`, `unrelated`, or `insufficient`. Relevant stable preferences and constraints count as useful memory. The observer records a suggested order for analysis; no caller consumes this order. Original record content, source evidence, result count and order remain unchanged. The deterministic retrieval fallback, query-free startup digest and underlying PageIndex search are not filtered by this change.

Inference remains local, asynchronous and bounded by the existing worker, eight-job queue, five-second timeout, cache and 900-byte per-state request guard. Memory jobs share this queue with the other shadow tasks; busy periods can drop observations with `queue_full`. They do not drop memory from the business response. Building bounded inputs still takes local CPU time, so zero overhead is not claimed.

The full access subject and requested profiles identify the cache scope. Full query and canonical memory record, including evidence references and content outside the excerpt, identify each input hash. Model text contains at most 96 UTF-8 bytes of query and 300 bytes of memory. `input_truncated` is set when these excerpts or the six-record limit omit material. Source identities are pseudonymous hashes of profile ID and memory ID. Logs contain decisions, hashes, timing, model/prompt revision and fallback reasons, without raw query, memory content or evidence references.

Canonical memories remain in the existing store. This stage does not create immutable historical memory snapshots or persist the private query in a new evaluation dataset. Source hashes can be reconstructed with `BuildLayaRecallJob` by an authorized administrator; they are not a public memory lookup API. A future replay or labeled production experiment must deliberately preserve the authorized query and exact source revision.

## Validation and evaluation

Tests check that an adversarial provider cannot expose another Agent's records, archived records or duplicates to the observer, that blocked/closed inference cannot change business results, that evidence and access changes invalidate cache identity, and that long Chinese inputs fit the deployed request budget. A gated `TestLayaMemoryLiveCanary` uses isolated synthetic memory and the real loopback Laya service. Set `ORRERY_LAYA_CANARY_KEY_FILE` explicitly to run it; ordinary tests skip it.

`laya-shadow-eval --suite memory --key-file <local-key-file>` evaluates eighteen synthetic decisions in six Chinese/English fixtures, including order facts, user preferences and external-send constraints. `--suite baseline` retains the previous knowledge/RSI evaluation and is the default. `--suite all` runs both. Output includes expected labels, actual choices and matches. Exit success establishes completed API calls; it does not establish semantic correctness. These fixtures are not a production accuracy estimate, independent holdout or training set.

### ECS result on 2026-10-15

Native memory tests and the real-model canary passed at 07:24:46 UTC. The six synthetic fixtures completed without inference fallback, taking 525–666 ms per three-record fixture. Only 6 of 18 labels matched the reviewed expectations. Known order facts and explicit user preferences were sometimes labeled `insufficient`; an unknown external-sharing restriction was labeled `unrelated`. This is evidence against enabling memory exclusion with the current model and prompt. The fixtures were not relabeled or used to tune the prompt after seeing the results. Retain this result as a baseline for later domain adaptation and independent evaluation.

## Rollout

The existing `ORRERY_LAYA_MODE=shadow` enables the observation when this source is deployed. There is no active memory filtering mode and no new database migration. Switching the model off keeps the original recall behavior. Automatic task wake-up gating, repair selection and RSI learning-value selection are separate future changes. This shadow stage does not reduce LLM tokens; active filtering requires labeled business validation, especially false exclusions of preferences, constraints and source evidence.
