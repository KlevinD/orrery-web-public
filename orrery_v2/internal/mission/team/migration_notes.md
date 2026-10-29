# Goal 08 Migration Notes

> 2026-10-15：旧的第三方源码迁入指引已退役，当前实现与兼容契约以[自有基础设施说明](../../../docs/runtime/owned-infrastructure.md)为准。

## Removed Legacy Semantics

- `squad_id` and `squad_member` are not core MissionTeam identifiers.
- `workspace_member` and `comment_author` are not MissionTeam members. They may seed RealHumanAgent profiles later.
- `market_kind` and `catalog_kind` cannot become members. Catalog/templates may seed AgentProfile records only.
- Issue/comment/task-queue routing remains outside this domain. Goal 08 emits mission staffing facts; later goals project them into discussion and inbox surfaces.

## Boundary Decision

No new agent organization peer package is introduced. Stable organization identity remains in `agent/core.AgentProfile` and `agent/core.AgentOrgIdentity`; Goal 08 adds directory and staffing operational state around those contracts.

Team state, staffing records, audits, and mission event candidates are committed through the platform UnitOfWork transaction because the current routed UnitOfWork facade exposes logical record put/get but not the event-store transaction boundary needed for atomic staffing facts. The repository table names remain router-compatible logical names, and the implementation keeps the stronger invariant required by Goal 08: team state and MissionEvent candidates are written or rolled back together.

Staffing workflows use platform logging, metrics, and tracing through injectable interfaces on `ServiceOptions`; tests wire the platform testkit logger, metric recorder, and tracer.

## Coverage

- Directory tests cover profile loading, operational signal joins, filters, and non-agent rejection.
- Policy and selector tests cover scoring, stable rejection codes, duplicate assignment, required refs, availability, budget, quality, and risk.
- Repository and event tests cover durable UOW-backed team state and mission event candidates.
- Service tests cover staffing request, appointment, idempotency, conflict, release, replacement, and provisioning failure rollback.
- Service tests cover platform staffing observability for mutation logs, metrics, and traces.
- Integration tests cover standard TaskContract assignment resolution and rejection of released assignments.
