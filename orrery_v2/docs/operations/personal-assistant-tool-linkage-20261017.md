# Personal assistant bound resource handoff

Production base: `30efc7e9f012e719dfdfaaa1cad33ab453145a83`.
Behavior baseline: `ORRERY-DESIGN-BASELINE-20261017`, revision 2.
Contracts preserved: ORR-AUTH-01/02/03, ORR-TOOLS-01, ORR-RESOURCE-01,
ORR-RSI-01, ORR-TRACE-01 and ORR-DEPLOY-01.

The personal assistant could discover its bound browser MCP tools but the
execution envelope carried only the human principal's global permission scopes.
AgentCore rejected the resource permission before MCP dispatch. The host now
seals the resolved permission of each configured MCP tool separately from the
human's scopes, for ordinary owner conversations with existing `tool.call`.
Only permissions shared by the stored profile reference and the resolved
runtime binding are carried. Native write tools do not receive these grants.
Explicit incoming envelopes, actual Mission tasks and responsibility policy
bounds are preserved. Forbidden actions, allowed-tool lists, tenant checks,
profile ownership, MCP authorization and business approval remain enforced.
The new optional envelope field participates in its digest and deep clone;
envelopes without it retain their existing digest format.

New and existing personal assistants receive `rsi_learning_plan` independently
of the session-only memory bootstrap branch. It reads only the running Agent's
learning observations in the host-selected tenant and remains advisory. Website
reception receives no learning or private MCP capabilities from this repair.

Regression covers owner execution, missing binding/permission, different owner
or tenant, modified grants, explicit task/tool/action fences, Website rejection,
and existing/new assistant learning bindings. Deployment and live conversation
receipts are stored separately under
`D:/Orrery/artifacts/personal-assistant-tool-linkage-20261017`.

This restores two resource access paths. It does not establish a complete RSI
experiment/validation/activation/rollback loop, improve end-to-end reply latency,
or prove WeChat delivery. Live checks use the personal-assistant conversation API
and do not request channel delivery or replay historical tasks.
