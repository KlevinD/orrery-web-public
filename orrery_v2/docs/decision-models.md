# Decision model adapters

The shared decision pipeline supports bounded choice assessments independently
of a particular model. Knowledge relevance, memory relevance and RSI failure
classification use the same observer and may select different profiles.

`ORRERY_DECISION_MODE` accepts `off` or `shadow`. When unset it inherits the old
`ORRERY_LAYA_MODE`; existing deployments continue to use the Laya profile.
Shadow assessments do not change search results or execute repair/trade actions.

Set `ORRERY_DECISION_PROFILES_FILE` to an operator-owned JSON file:

```json
{
  "default_profile_id": "local-choice",
  "scene_profiles": {
    "knowledge": "llm-choice",
    "memory": "local-choice",
    "rsi_sample": "llm-choice"
  },
  "profiles": [
    {"profile_id":"local-choice","adapter":"laya","enabled":true,"timeout_ms":5000},
    {"profile_id":"llm-choice","adapter":"llm","enabled":true,"llm_profile_id":"platform-default","timeout_ms":30000}
  ]
}
```

Replace the file atomically. It is re-read before each assessment; changing a
profile or the platform LLM default affects subsequent jobs and separates their
cache entries. Missing, disabled, duplicate or unknown explicit bindings fail
without selecting another model. This release has an operator configuration
file, not a new client settings screen or an HTTP configuration endpoint.

The Laya adapter uses its existing localhost endpoint and key file, retaining its
known deployed model revision. The LLM adapter resolves enabled platform LLM
profiles and secret references for each assessment, supports OpenAI-compatible
Chat Completions and Responses, and requires a bounded structured choice result.
Other provider protocols require their own adapter; they are rejected explicitly.

Assessments retain profile, adapter, provider, selected model/revision, selection
source, prompt version, provenance hashes, latency and fallback status. An LLM's
model identifier is recorded as its configured revision; it is not proof of
immutable provider weights. LLM confidence is marked `self_reported` and is not a
calibrated probability. Errors and records exclude credentials and full evidence.
Legacy Laya assessment records remain readable.

Adding another model implementation requires registering a `decision.Factory`
with `decision.Router`, implementing `decision.Judge`, and providing model
metadata. Current choice assessments retain the existing bounds (six candidates,
two to five criteria, 900 bytes per serialized candidate). Prediction and
optimization capabilities need their own input/output contracts and adapters;
this release does not claim those models are already supported.

## RSI full-evidence LLM fallback

In shadow mode, RSI failures assessed by Laya are escalated asynchronously when
inference fails, the answer is insufficient, its selected probability is below
0.85, its input was truncated, or stored assessments disagree. Explicit missing
or disabled model bindings, shutdown cancellation, and assessments already made
by an LLM do not select another model automatically.

The fallback reads the original, scoped `RSILearningSample` from durable storage.
It sends its complete retained evidence rather than the bounded Laya summary;
it cannot retrieve source, user data or context that was never retained. Samples
above 128 KiB are preserved and marked `evidence_too_large`, without truncation
or a model request. The result is a private structured diagnosis with one of
`skill_defect`, `tool_usage`, `environment`, or `insufficient`. Learning plans
expose fixed advice and provenance, excluding private evidence and model prose.
The primary Laya assessment remains available separately.

`ORRERY_RSI_LLM_FALLBACK=off` disables escalation; unset or `on` enables it.
`ORRERY_RSI_LLM_FALLBACK_PROFILE` selects an existing enabled platform LLM profile;
unset uses `platform-default`. `ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR` defaults
to 20 and accepts 1 through 100. The limit is global across scopes and persists
in the database per UTC clock hour. Failed calls consume an attempt and budget.

One worker, an eight-item queue and a 45-second deadline keep diagnosis outside
the tool execution path. Each immutable observation reserves at most one durable
attempt before inference, including failed or unknown outcomes; restarting does
not retry it. Budget exhaustion is terminal for that observation. A full queue
or shutdown can leave a retained sample without a diagnosis. Failed, oversized,
exhausted or insufficient diagnoses continue to request evidence. Conflicting
later confident judgments also request evidence, and an unrecorded tool outcome
always requires reconciliation before any retry.

Fallback produces advice only. It does not generate or publish code, execute a
Mission, change permissions, or bypass the independent RSI experiment and
runtime verification gates. LLM confidence is marked `self_reported`.
