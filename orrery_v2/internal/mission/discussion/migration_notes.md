# Discussion / Inbox Migration Notes

> 2026-10-15：旧的第三方源码迁入指引已退役，当前实现与兼容契约以[自有基础设施说明](../../../docs/runtime/owned-infrastructure.md)为准。

## Removed Legacy Coupling

- No `agent_task_queue` writes are performed for mentions or `@all`.
- No `issue_id`, legacy comment table, workspace subscriber table, or task queue trigger fields are part of the domain model.
- Discussion messages emit MissionEvent candidates and memory candidates, but do not directly mutate task lifecycle, QA, staffing, or memory records.

## Current Stage Boundaries

- `internal/mission/discussion` owns domain operations, repository contracts, event candidates, inbox state, and digest building.
- `internal/mission/projection` consumes the emitted events into existing DiscussionView and InboxSummary read models.
- `internal/app/missioncontrol` maps those projection views to DTOs exposed by the current HTTP transport.
