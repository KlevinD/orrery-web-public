# 01-06 Integration Review

This document is the living review ledger for code already implemented under
`implementation-goals` 01-06. It tracks architecture drift, MVP-thin slices,
duplicated foundation code, phase fragmentation, and corrective work.

## Review Standard

The review is governed by the root business and architecture documents,
`agent-company-mission-control-model.md`, `project-structure-guidelines.md`,
`technical-architecture-rewrite-plan.md`, and the 01-06 goal READMEs.

1. Foundation code must serve real local-first production operation. Business
   modules must not build their own DB, transaction, queue, logging, file,
   config, or testkit infrastructure.
2. Each goal must be a production-grade slice, not a happy-path skeleton,
   demo, or MVP placeholder.
3. Implementation must stay aligned with the Agent Company / Mission Control
   model: agents are accountable members, tools are resources or runtime
   adapters, MissionEvent is durable business fact, and lifecycle Signal is a
   runtime hook.
4. Decoupling must support business flexibility, robustness, and readability.
   Decoupling for its own sake, excessive adapters, huge files, and repeated
   wheels are treated as drift.
5. Goals 01-06 must fuse into one backend loop consumable by goal 07 BaseAgent
   runtime, not six isolated local implementations.
6. Source files should stay under 800 lines. Long generated or archival docs
   must be split, regenerated, or explicitly classified instead of silently
   becoming the normal editing surface.

## Baseline Map

| Goal | Main code area | Current judgment |
| --- | --- | --- |
| 01 infrastructure foundation | `internal/platform/*` | Production foundation exists: config, split DBs, migrations, UOW, eventstore, outbox, execution, filestore, clock, secrets, metrics, tracing, health, testkit. |
| 02 core contracts | `internal/agent/core`, `internal/mission/core`, `internal/mission/task` | Shared language exists for Mission, AgentProfile, ContextPack, TaskContract, AgentResult, Deliverable, Evidence, and task lifecycle. |
| 03 lifecycle signals | `internal/agent/lifecycle` | SignalBus, Pipeline, FailurePolicy, hooks, and testkit establish the runtime boundary before BaseAgent. |
| 04 event ledger projection | `internal/mission/event`, `internal/mission/projection`, `internal/app/missioncontrol` | MissionEvent, projection read model, and query DTO boundary exist and are being hardened. |
| 05 resource registry memory | `internal/resource/*`, `internal/memory/*`, `internal/app/resourceadmin` | Direction matches the design: tools/MCP/skills are resources, memory is digested into context rather than injected raw. |
| 06 task delivery QA | `internal/mission/task`, `internal/agent/component/{delivery,evidence,qa}` | Delivery, evidence, QA, rejection, and rework loop exists, with task events adapted into MissionEvents. |

## Corrected Issues

| Date | Issue | Correction | Evidence |
| --- | --- | --- | --- |
| 2026-05-31 | `internal/platform/runtime.go` exceeded 800 lines and mixed boot assembly, hot reload, YAML watch, metrics server, and static config drift detection. | Split into `runtime.go`, `runtime_reload.go`, `runtime_metrics_server.go`, and `runtime_config.go`. The main runtime file now focuses on assembly and lifecycle entry points. | `go test ./internal/platform` passed. Line scan showed `runtime.go` at 473 lines. |
| 2026-05-31 | `internal/platform/module_test.go` exceeded 800 lines and module registrar validation tests were appended to a broad integration file. | Extracted registrar validation coverage to `module_registrar_validation_test.go`; kept cross-capability integration coverage in `module_test.go`. | `go test ./internal/platform` passed. Line scan showed `module_test.go` at 751 lines. |
| 2026-05-31 | `TaskEventReworkResolved` was adapted as another `ReworkRequested` MissionEvent, causing projections and audit logs to treat a resolved rework item as a new request. | Added `EventReworkResolved` and `ReworkResolvedPayload`, mapped the task event separately, and made projection close the existing rework item without regressing later task states. | `go test ./internal/mission/event ./internal/mission/projection` passed. |
| 2026-05-31 | Task delivery could emit several MissionEvents in one unit of work, but the task adapter fixed every task-derived MissionEvent at stream version 1. A real service + platform repository + event sink path therefore failed with `event stream conflict`. | Made `TaskEventSink` allocate the next stream version from the transaction eventstore view and added an integration test that submits an AgentResult through `DeliveryService`, `PlatformRepository`, `TaskEventSink`, and platform eventstore. | `go test ./internal/mission/event ./internal/mission/task ./internal/platform/uow ./internal/platform/eventstore` passed. |
| 2026-05-31 | MissionControl DTO did not expose whether a rework request was open or resolved after projection learned that state. | Added rework `status` and `resolved_at` fields to the DTO and mapper so clients consume the fused QA/rework lifecycle, not just the original request shell. | `go test ./internal/app/missioncontrol ./internal/mission/event ./internal/mission/projection` passed. |
| 2026-05-31 | Resource admin app service tests mostly used in-memory registries, leaving production platform registry assembly under-covered at the app boundary. | Added a service-level persistence test using `tool.NewPlatformRegistry` and `skill.NewPlatformRegistry` through command/query services. | `go test ./internal/app/resourceadmin ./internal/resource/tool ./internal/resource/skill ./internal/memory/...` passed. |
| 2026-05-31 | Rework reassignment reused `ReworkRequested`; with identical task/rework idempotency data it could be deduplicated away, so the ledger lost the PM assignment fact. Platform repository also only returned `open` rework requests even though the service expected assigned requests to remain active. | Added `TaskEventReworkAssigned`, `EventReworkAssigned`, `ReworkAssignedPayload`, projection handling for assigned rework, and treated `open` plus `assigned` as active repository states. Extended the bridge test through QA rejection, rework assignment, task acceptance, and rework resolution. | `go test ./internal/mission/event ./internal/mission/task ./internal/mission/projection ./internal/app/missioncontrol` passed. |

## Fusion Checklist

Target loop:

```text
TaskContract
-> lifecycle signals
-> AgentResult
-> Deliverable / Evidence
-> QA approve / reject / rework
-> MissionEvent
-> projection
-> app MissionControl DTO
```

Observed so far:

- `mission/task` defines task lifecycle, submission, QA, and rework task events.
- `mission/event/task_adapter.go` maps task events to MissionEvent payloads such
  as `AgentResultSubmitted`, `DeliverableSubmitted`, `EvidenceSubmitted`,
  `QARequested`, `QAApproved`, `QARejected`, `ReworkRequested`, and
  `ReworkResolved`.
- `mission/projection` consumes task, evidence, QA, and rework events into the
  Mission Control task board, evidence view, and QA view.

Still under review:

- Confirm service-layer task events are uniformly written into the platform
  eventstore/outbox instead of remaining only in in-memory task repositories.
  Delivery, QA rejection, rework assignment, task acceptance, and rework
  resolution are now covered through the real service/repository/sink path.
- Confirm `missioncontrol.QueryService` only exposes projection DTOs and does
  not leak domain structs, raw events, internal traces, or raw memory.
- Confirm lifecycle signals remain runtime hooks and are not confused with
  durable business facts.

## Boundary Checks

Foundation:

- Business modules must not call `sql.Open`, read `os.Getenv` directly for
  business behavior, write local files as persistence contracts, or use
  `fmt.Print*` as business logging.
- Production paths should use platform DB registry, RoutedUOW, EventRecorder,
  OutboxPublisher, FileStore, execution, logger, metrics, tracing, health, and
  testkit.
- Current scan found no reverse import from `internal/platform` into
  `internal/agent`, `internal/mission`, `internal/app`, or `internal/transport`.

Resource and memory:

- Tool/MCP/Skill are resources, not team members. `AgentProfile` should keep
  references rather than embedding tool objects as members.
- Memory must flow through retrieval, compression, permission filtering, and
  source binding before entering runtime context as `MemoryDigest` or
  `ContextPack`.
- Low-confidence memory must not become acceptance evidence.

Delivery and QA:

- QA is an agent capability configured through `AgentProfile`, not a separate
  top-level business center.
- Mission task may record QA decisions, but it should not own QA engines or
  parse runtime-private output.
- Worker tasks must be independently submittable, reviewable, rejectable, and
  reworkable.

## Risk Queue

| Priority | Risk | Current action |
| --- | --- | --- |
| P0 | Source files over 800 lines harm maintainability. | Source-code over-limit cases found so far have been split. Continue scanning all implemented code. |
| P0 | Task delivery events may remain domain-local instead of entering eventstore/outbox. | Delivery, QA, and rework bridge paths are now covered through platform repository/eventstore integration tests. |
| P1 | QueryService/DTO boundary may mix projection/domain concerns. | Audit `internal/app/missioncontrol` next. |
| P1 | Resource registry or memory may default to in-memory production paths. | Distinguish test fake, transient adapter, and production SQLite/platform assembly. |
| P1 | Generated docs, historical originals, and plan files may exceed 800 lines. | Classify or split after source-code constraints are clean. |

## Verification Entry

Each correction round should run at least:

```text
gofmt
go test ./...
line scan: all Go source files should be <= 800 lines
boundary scan: platform must not import agent / mission / app / transport
production-path scan: business packages must not directly use sql.Open / os.Getenv / fmt.Print*
```
