# Shared LLM routing

Orrery's shared Pi and Codex runtime paths resolve their model after execution
authorization and before prewarming/opening the provider session. Eino's model
factory and Pi use the same `llmprofile` selector. Provider wire protocols and
credentials remain in the provider adapters.

## Selection and inheritance

- An execution's explicit model override (including a scheduler fallback) wins.
- An explicit task profile wins over the agent profile through the existing
  provider-specific option merge. A task without a profile inherits the agent.
- `llm_profile_id: platform-default` references the current platform default.
  It does not copy a model name or provider credential into the agent.
- An explicit agent profile wins over the platform default. Legacy inline
  provider bindings with their own credential retain that binding.
- With no explicit binding, select the enabled global default. The first
  enabled profile remains the legacy fallback when no default is marked.
- Missing or disabled explicit profiles fail visibly. They never silently
  select a different provider. Ambiguous defaults/profiles are rejected.

The default is read for each new execution. Once selected, its model is pinned
for that execution, including a prewarmed backend. If settings change between
prewarming and execution, the existing request comparison invalidates stale
prewarming and opens a backend for the newly selected model.

`resolved_llm_profile_id`, `resolved_llm_model`, and `llm_selection_source` are
included in response metadata. Credentials and provider config are excluded.
Codex receives the selected model in its thread-open request; its provider
endpoint/authentication remain in its managed Codex configuration. Switching a
Codex provider endpoint still requires updating that provider configuration.

New system-managed workforce agents store the live platform-default reference.
Memory synthesis, memory ontology extraction, and preference extraction on
branches that include it no longer pin a historical model in source code.

## Existing installations

The deployment migration converts copies of the current Luna default into
inheritance, preserving different explicitly selected models. It removes matching
runtime model copies, replaces matching base-agent profile snapshots with
`{"profile_id":"platform-default"}`, and removes matching task model copies.
Archive/history, execution snapshots, schedules and enablement are preserved.
The migration is applied only after the new server is ready. It stores a private
backup on the server and uses compare-and-set updates in one database transaction.

## Dedicated capabilities

ASR/TTS, image generation, embedding, reranking, and Laya decision inference keep
their capability-specific adapters and configuration. This change does not
replace those models with the default chat model or enable Laya active decisions.
It establishes shared LLM selection and inheritance, not a universal inference
executor or a new model-management frontend.
