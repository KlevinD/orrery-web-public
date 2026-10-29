# Runtime case diagnosis

Protocol `orrery-runtime-diagnosis-v1` connects new native runtime terminal evidence to a model diagnosis and the existing improvement state machine. Under production `ORRERY_RSI_MODE=llm`, the API registers a background sweeper; this shares the platform lifecycle and creates no user scheduled task or separate operating system worker.

The first enablement records a durable activation time. Only native execution requests captured from that time onward, with an immutable execution snapshot and dependency receipt, enqueue work atomically with their terminal/case. Existing cases and older requests recovered later are excluded. The timestamp survives restarts. Pending requests older than 24 hours expire without inference.

A model call receives hashes, identifiers, the original request/execution/terminal/review/dependency metadata and available validated business assessments. Raw prompts, task/output text, credentials and artifact bytes stay outside this input. Missing evidence remains explicit; the diagnosis is a hypothesis and cannot prove business causality or a complete experiment baseline. Formal tasks allow up to one minute for domain acceptance evidence to reconcile before diagnosing its absence.

Each paid-call attempt reserves a database slot before inference. The default global limit is four attempts per UTC hour, configurable with `ORRERY_RSI_CASE_DIAGNOSIS_MAX_CALLS_PER_HOUR` (1–100). The selected profile/model and prompt hash are host metadata. `ORRERY_RSI_CASE_DIAGNOSIS_PROFILE` selects an explicit profile; a missing explicit selection fails without fallback. `ORRERY_RSI_CASE_DIAGNOSIS=off` disables the sweeper. Model errors/invalid answers remain recorded; running calls whose outcome is unknown are not automatically repeated after restart.

The response must cite offered evidence IDs and choose from the nine supported target kinds. Extra authority fields, tool calls, invented IDs and invalid target kinds fail validation. The frozen input, model response and receipt hash are saved before the existing Claim/Advance operations move the same case from observed to diagnosed. Recovery can apply a saved answer or acknowledge an already applied diagnosis without paying for another call; stale cases or expired apply leases remain failed rather than overwriting another learner.

Ontology retains the original task access reference. Its existing case node receives the diagnosed state and the exact diagnosis receipt/input references; model prose stays private in the case/evidence ledger. RuntimeReview, business acceptance, artifact verification and the executing Agent's public reply remain separate.

This stage does not generate change sets, run candidate source, activate resources or claim full RSI. Source experiments and production adoption still require verified baselines, independent evaluation and the existing domain authority.
