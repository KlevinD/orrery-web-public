# Goal 08: Organization Identity and Mission Staffing

> This goal must be implemented as a production staffing domain slice, not as a lightweight roster or a compile-only set of structs. A thin implementation that only creates `MissionTeam`, stores member IDs, or picks the first matching profile is not accepted. Goal 08 must connect AgentProfile organization identity, directory queries, availability, appointment policy, mission-team state, event candidates, and audit evidence into a durable staffing system.

## Purpose

Goal 08 implements the organization and staffing layer for Agent Company Mission Control.

It consumes the foundations from earlier goals:

- Goal 01: platform DB, migration, RoutedUOW, event/outbox, queue, lock, config, secret, logging, metrics, tracing, health, and testkit.
- Goal 02: AgentProfile, AgentIdentity, AgentOrgIdentity, RuntimeSpec, TaskContract, AgentResult, refs, and shared validation errors.
- Goal 03: lifecycle signals when staffing decisions trigger or affect agent runs.
- Goal 04: MissionEvent candidate/event sink and projection boundaries.
- Goal 05: tool, skill, and memory refs as profile query inputs, not team members.
- Goal 06: delivery/evidence/QA contracts that human and AI team members must use after appointment.
- Goal 07: AgentAssembler, BaseAgent, RuntimeAdapter, ContextPack, and AgentInstance construction.

Organization, department, position, role, reporting line, authority, availability, cost, quality, and risk are properties used to staff AgentInstances into MissionTeams. They do not create a separate HR product, and they must not turn tools, runtimes, APIs, or market templates into members.

## Current Rework Decision

The implementation must be designed around these decisions:

1. Preserve existing AgentProfile and AgentOrgIdentity contracts from Goal 02; do not duplicate them in a new organization package.
2. Treat organization directory as a query/index view over profiles and instances, not as a parallel source of truth.
3. Implement MissionTeam and MissionAssignment as mission-domain state with durable repositories and event candidates.
4. Implement staffing as policy-driven selection plus appointment, release, and replacement workflows, not as direct list mutation.
5. Reuse existing Orrery staffing, agent catalog, workspace membership, and role resolver code only when it can be moved behind the new boundaries without preserving old flat squad/member semantics.
6. Ensure RealHumanAgent and AI Agent share the same AgentProfile, AgentInstance, TaskContract, AgentResult, Deliverable, Evidence, and QA exits.
7. Treat every thin path as incomplete until covered by the acceptance scenarios in this README.

## Global Requirements

Before implementation, follow:

- `implementation-goals/README.md`
- `project-structure-guidelines.md`
- `module-decomposition-and-implementation-guide.md`
- `technical-architecture-rewrite-plan.md`
- Goal 07 README, because staffing can only appoint runnable AgentInstances through the Stage 7 runtime assembly contracts.

This goal may not weaken global architecture requirements. It may not add MVP substitutes for platform storage, eventing, transactions, logging, or testkit.

## Stage 0 Infrastructure Usage Contract

Goal 08 must use platform infrastructure from Goal 01:

- Profile, directory, availability, team, assignment, decision, and audit persistence must use platform DB stores through repositories and RoutedUOW.
- Staffing commands must write team state and event candidates in the same unit of work.
- Candidate evaluation and appointment audit logs must use platform logging/tracing/metrics.
- MissionTeam projections must be produced from MissionEvent/event-store boundaries, not from ad hoc timeline rows.
- Background availability refresh, stale appointment cleanup, and scheduled staffing work must use platform queue/lock/claim/liveness where needed.
- Configurable staffing thresholds must come from ConfigManager last-known-good snapshots.
- Secrets for external HR/workspace systems must use platform secret providers, never stored in AgentProfile or MissionTeam records.
- Tests must use platform testkit or explicit repository fakes scoped to tests.

Goal 08 must not create a private DB connection, private transaction helper, private queue, private event log, private logger, private config store, global in-memory roster, or temporary-file staffing state.

## Production Responsibilities

Goal 08 is responsible for:

- AgentProfile repository boundary.
- AgentDirectoryQuery.
- AgentDirectoryEntry.
- AgentAvailability.
- AgentCostProfile.
- AgentQualitySignal.
- AgentRiskProfile.
- AgentInstance provisioning/reuse boundary.
- MissionTeam.
- MissionTeamMember.
- MissionAssignment.
- StaffingRequest.
- StaffingDecision.
- CandidateEvaluation.
- Appointment.
- Release.
- Replacement.
- AssignmentPolicy.
- StaffingPolicy.
- StaffingAudit.
- MissionTeamRepository.
- StaffingService.
- Directory-backed candidate selector.
- Event candidate emission for staffing facts.
- Migration notes for old Orrery staffing/catalog/workspace-member code.

Goal 08 is not responsible for:

- Static AgentProfile schema redesign beyond query/repository interfaces.
- Tool/Skill/Memory registry storage.
- Runtime provider execution.
- BaseAgent pipeline behavior.
- TaskContract generation.
- DiscussionGroup membership materialization, inbox delivery, mention handling, or message storage.
- Frontend organization chart.
- Complex HR/payroll/performance/legal employment systems.
- General workflow/BPMN engines.
- Legacy `mission_staffing`, `squad`, or market catalog compatibility as core semantics.

## Forbidden Implementation Patterns

Do not implement:

- `internal/agent/organization/*` as a new peer domain.
- A flat member pool that bypasses AgentProfile org identity.
- MissionTeam members that are ToolRef, SkillRef, RuntimeAdapter, CLI, API, RPA, MCP server, browser script, webhook, or market template.
- Direct appointment of `RuntimeSpec`, provider name, or catalog kind.
- MissionAssignment overwriting stable AgentOrgIdentity.
- Organization manager relationship automatically granting mission team membership, discussion visibility, or task authority.
- Worker receiving Mission Task before active appointment and assignment.
- Deleting released/replaced assignment history.
- In-memory-only team repository as production implementation.
- Placeholder policies whose only behavior is `Allow() bool`.
- Candidate selection that simply picks the first enabled profile without scoring, rejection reasons, and audit.
- Fake availability, fake cost, or fake quality data silently accepted as production signals.
- Event emission outside the event candidate / UnitOfWork boundary.

## Package Layout

Target code belongs under `orrery_v2/internal/agent` and `orrery_v2/internal/mission/team`.

```text
internal/agent/core/
  profile_repository.go
  profile_query.go
  directory_entry.go
  availability.go
  cost_profile.go
  quality_signal.go
  risk_profile.go
  instance_provisioner.go

internal/mission/team/
  team.go
  member.go
  assignment.go
  role.go
  staffing_request.go
  staffing_decision.go
  candidate.go
  appointment.go
  release.go
  replacement.go
  policy.go
  selector.go
  service.go
  repository.go
  events.go
  audit.go
  errors.go
  migration_notes.md
  testkit.go
```

Dependency direction:

```text
agent/core profile query -> shared refs and platform repository interfaces only
mission/team -> agent/core profile query interfaces
mission/team -> mission/core ids
mission/team -> mission/event EventSink or event candidate boundary
mission/team -> platform UOW/logging/metrics/tracing through interfaces
mission/team must not import runtime providers, discussion storage, transport, or frontend
agent/core must not import mission/team
transport -> app services only
```

If import cycles appear, fix the boundary. Do not move staffing contracts into random utility packages to silence the compiler.

## Organization Identity Design

Stable organization identity is owned by AgentProfile / AgentIdentity:

```text
AgentOrgIdentity {
  organization_ref
  department_ref
  position_ref
  role_refs[]
  manager_agent_ref
  reporting_line_ref
  authority_scope
  employment_type
  supervision_policy
}
```

Goal 08 adds query and operational metadata, not a second identity source:

```text
AgentDirectoryEntry {
  agent_profile_id
  agent_instance_id
  real_name
  display_name
  role_name
  agent_kind
  org_identity
  persona_summary
  skill_refs[]
  tool_refs[]
  runtime_spec_summary
  memory_profile_ref
  availability
  cost_profile
  quality_signal
  risk_profile
  enabled
  updated_at
}
```

Directory entries must be derived from profile, instance, availability, cost, quality, and risk sources. They may be indexed for performance, but the index is not authoritative identity state.

## Directory Query Design

`AgentDirectoryQuery` must support:

```text
organization
department
position
role
manager
reporting_line
agent_kind
employment_type
skill_refs
tool_refs
runtime_kind
memory_kind
availability_window
authority_scope
max_cost
min_quality_score
allowed_risk_levels
enabled_only
exclude_agent_instances[]
include_humans
include_ai
pagination
sort
```

Required behavior:

- Query returns only enabled profiles when `enabled_only` is true.
- Query must not return tools, skills, runtimes, templates, or market catalog rows as members.
- Query must preserve secret refs only as refs; no secret values.
- Query must return enough rejection context for staffing audit when candidates are filtered out.
- Query implementation must be testable without runtime provider startup.

## Availability, Cost, Quality, and Risk

Staffing decisions must not be based only on profile tags.

```text
AgentAvailability {
  agent_profile_id
  agent_instance_id
  status
  available_from
  available_until
  capacity
  current_load
  reason
  source
  updated_at
}
```

Status values:

```text
available
busy
offline
disabled
unknown
```

```text
AgentCostProfile {
  agent_profile_id
  cost_unit
  estimated_cost_per_hour
  estimated_cost_per_task
  budget_class
  source
}
```

```text
AgentQualitySignal {
  agent_profile_id
  score
  sample_size
  evidence_refs[]
  last_success_at
  last_failure_at
}
```

```text
AgentRiskProfile {
  agent_profile_id
  risk_level
  reasons[]
  mitigations[]
}
```

If availability, cost, quality, or risk is unknown, policy must treat it explicitly. Unknown may be allowed only when the staffing policy says so and the audit records the reason.

## Mission Team Design

### MissionTeam

```text
MissionTeam {
  team_id
  mission_id
  members[]
  created_at
  updated_at
  version
}
```

### MissionTeamMember

```text
MissionTeamMember {
  member_id
  mission_id
  agent_instance_id
  agent_profile_id
  joined_at
  joined_by
  status
  visibility
  assignment_refs[]
  release_ref
  replacement_ref
}
```

Member status:

```text
invited
active
released
replaced
unavailable
```

### MissionAssignment

```text
MissionAssignment {
  assignment_id
  mission_id
  member_id
  agent_instance_id
  agent_profile_id
  mission_role
  responsibility_boundary
  authority_scope
  task_refs[]
  assigned_by
  assigned_at
  released_at
  replaced_by_assignment_id
  replacement_reason
  status
}
```

Mission roles:

```text
PM
HR
Planner
Coordinator
Worker
QAPlan
QACheck
Reviewer
Approver
RealHumanContributor
Observer
```

MissionAssignment is temporary mission responsibility. It must never rewrite the profile's department, position, manager, reporting line, or employment type.

## Staffing Workflow

### StaffingRequest

```text
StaffingRequest {
  request_id
  mission_id
  requested_by
  capability_gap
  desired_roles[]
  required_skills[]
  required_tools[]
  authority_requirements
  availability_window
  budget_constraints
  quality_constraints
  risk_constraints
  allow_unknown_availability
  allow_human
  allow_ai
  reason
  status
  created_at
}
```

Request status:

```text
requested
evaluating
appointed
rejected
cancelled
expired
```

### CandidateEvaluation

```text
CandidateEvaluation {
  candidate_profile_id
  candidate_instance_id
  matched_roles[]
  matched_skills[]
  matched_tools[]
  availability_result
  authority_result
  cost_result
  quality_result
  risk_result
  score
  rejection_reasons[]
}
```

### StaffingDecision

```text
StaffingDecision {
  decision_id
  request_id
  selected_agent_profile_id
  selected_agent_instance_id
  appointment
  rejected_candidates[]
  rationale
  decided_by
  decided_at
  audit_ref
}
```

### HR Staffing Flow

```text
Receive capability gap
-> create StaffingRequest
-> query AgentDirectory by role / skill / tool / availability / authority / cost / quality / risk
-> evaluate candidates through AssignmentPolicy
-> record accepted and rejected candidate reasons
-> instantiate or reuse AgentInstance through Goal 07 boundary
-> create MissionTeamMember
-> create MissionAssignment
-> persist team state and staffing events in one UnitOfWork
-> emit StaffingRequested / AgentJoined / MissionAssignmentCreated
-> Goal 09 later materializes discussion membership / inbox notifications
```

## AgentInstance Provisioning and Reuse

Goal 08 decides *who* should join a mission. Goal 07 decides how a runnable instance is assembled.

```go
type AgentInstanceProvisioner interface {
    GetOrCreateInstance(ctx context.Context, profile AgentProfile, scope AgentInstanceScope) (AgentInstanceRef, error)
}
```

Rules:

- AI agents may reuse a warm AgentInstance only if runtime config, memory scope, permissions, and mission isolation policy allow it.
- RealHumanAgent instances map to human identity/inbox refs, but still receive tasks through standard TaskContract and return AgentResult.
- Instance reuse decisions must be auditable.
- A failed instance provisioning step must fail appointment before team state is committed.

## AssignmentPolicy

Policy must validate:

- AgentProfile exists and is enabled.
- AgentProfile org identity is complete.
- Agent kind is allowed for the request.
- Stable org identity supports requested mission role.
- Availability satisfies the requested window or unknown availability is explicitly allowed.
- Authority scope satisfies mission role and task authority.
- Required skills and tools are present as refs.
- RuntimeSpec is valid but not treated as assignee.
- Memory profile refs are compatible with mission scope.
- Risk level is allowed or mitigation is recorded.
- Cost fits budget constraints when provided.
- Duplicate active assignment is idempotent only when the command idempotency key matches; otherwise return conflict.
- Tools, skills, runtimes, APIs, RPA scripts, and market templates are rejected.
- RealHumanAgent is allowed only when human handoff/inbox policy is available or explicitly deferred by integration contract.

Policy errors must use stable codes:

```text
profile_not_found
profile_disabled
org_identity_incomplete
candidate_not_agent
availability_denied
authority_denied
skill_missing
tool_missing
runtime_not_available
memory_scope_denied
risk_denied
budget_denied
duplicate_assignment
team_member_not_found
assignment_not_active
event_commit_failed
```

## Repository and Unit Of Work

### AgentProfileRepository

```go
type AgentProfileRepository interface {
    GetProfile(ctx context.Context, id AgentProfileID) (AgentProfile, error)
    QueryDirectory(ctx context.Context, q AgentDirectoryQuery) (AgentDirectoryPage, error)
    GetAvailability(ctx context.Context, id AgentProfileID) (AgentAvailability, error)
}
```

### MissionTeamRepository

```go
type MissionTeamRepository interface {
    GetTeam(ctx context.Context, missionID MissionID) (MissionTeam, error)
    SaveTeam(ctx context.Context, team MissionTeam) error
    GetAssignment(ctx context.Context, assignmentID MissionAssignmentID) (MissionAssignment, error)
    SaveAssignment(ctx context.Context, assignment MissionAssignment) error
    SaveStaffingRequest(ctx context.Context, request StaffingRequest) error
    SaveStaffingDecision(ctx context.Context, decision StaffingDecision) error
    AppendAudit(ctx context.Context, audit StaffingAudit) error
}
```

Writes must happen inside RoutedUOW:

```text
validate command
-> load profiles/team/assignments
-> evaluate policy
-> provision/reuse AgentInstance
-> save request/decision/member/assignment/audit
-> append MissionEvent candidates
-> commit UOW
```

No production repository may be in-memory only. Test fakes must live in `_test.go` or `testkit.go`.

## MissionTeamService

```go
type MissionTeamService interface {
    RequestStaffing(ctx context.Context, req StaffingRequest) (StaffingRequestID, error)
    Appoint(ctx context.Context, cmd AppointmentCommand) (MissionAssignment, error)
    Release(ctx context.Context, cmd ReleaseCommand) error
    Replace(ctx context.Context, cmd ReplaceCommand) (MissionAssignment, error)
    ListTeam(ctx context.Context, missionID MissionID) (MissionTeam, error)
}
```

Required behavior:

- `RequestStaffing` persists request and emits `StaffingRequested`.
- `Appoint` creates or reuses AgentInstance, creates/updates MissionTeamMember, creates MissionAssignment, emits `AgentJoined` and `MissionAssignmentCreated`.
- `Release` marks active assignment released, preserves history, emits `AgentReleased` and `MissionAssignmentReleased`.
- `Replace` releases old assignment, appoints replacement, links old/new assignment refs, emits `AgentReplaced`.
- `ListTeam` returns team state without reading runtime provider, discussion store, or transport packages.
- Every mutation is idempotent by command idempotency key or returns conflict.
- Every rejection produces audit with stable reason codes.

## Events

Goal 08 produces MissionEvent candidates only. Goal 04 owns persistence/projection mechanics.

Events:

```text
StaffingRequested
StaffingDecisionRecorded
AgentJoined
AgentReleased
AgentReplaced
MissionAssignmentCreated
MissionAssignmentReleased
MissionAssignmentChanged
CandidateRejected
```

Event payloads must include refs, not full profile dumps or secret values:

```text
mission_id
team_id
request_id
decision_id
member_id
assignment_id
agent_profile_id
agent_instance_id
mission_role
rationale_ref
audit_ref
occurred_at
```

## Existing Implementation Reuse Plan

Review and migrate from Orrery:

```text
mission_staffing service / tests / migrations
agent catalog / agent templates
managed runtime roles / PM agent resolver
workspace member / comment author member
existing mission team or participant tables
existing agent availability / visibility fields
```

Adoption rules:

| Source | Adopt | Must Remove |
| --- | --- | --- |
| `mission_staffing` | request, appointment, replacement flow ideas and tests | flat squad/team semantics |
| agent catalog/templates | AgentProfile seeds and import mapping | `kind=tool` as member |
| runtime roles | role matching hints | provider/runtime as assignee |
| workspace member/comment author | RealHumanAgent seed data | discussion/comment storage semantics |
| existing migrations | column names and indexes when clean | old nullable mega-row shortcuts |

If a legacy implementation is not migrated, migration notes must explain why.

## Testing and Acceptance

### Required Test Files

```text
internal/agent/core/profile_repository_test.go
internal/agent/core/directory_query_test.go
internal/agent/core/availability_test.go
internal/agent/core/cost_profile_test.go
internal/agent/core/instance_provisioner_test.go
internal/mission/team/staffing_request_test.go
internal/mission/team/candidate_selector_test.go
internal/mission/team/appointment_test.go
internal/mission/team/release_test.go
internal/mission/team/replacement_test.go
internal/mission/team/policy_test.go
internal/mission/team/repository_test.go
internal/mission/team/events_test.go
internal/mission/team/audit_test.go
internal/mission/team/service_test.go
internal/mission/team/integration_test.go
```

### Acceptance Scenarios

1. AgentProfile repository loads profiles with real_name, org_identity, persona, memory refs, skill refs, tool refs, runtime spec, and enabled state.
2. Directory query filters by organization, department, position, role, manager, availability, skills, tools, runtime kind, authority, risk, and enabled state.
3. Directory query never returns ToolRef, RuntimeAdapter, CLI, API, RPA, MCP server, or market template as a candidate.
4. Missing organization, department, position, role/authority, or profile enabled state rejects staffing.
5. Availability unavailable/busy/offline rejects appointment unless policy explicitly allows unknown or degraded staffing.
6. Cost, quality, and risk constraints influence candidate selection and are recorded in evaluation audit.
7. Candidate selector returns accepted and rejected candidates with stable reason codes.
8. AgentInstanceProvisioner is invoked before appointment commit and failure aborts the appointment.
9. AI AgentInstance can be reused only when isolation policy allows it.
10. RealHumanAgent can be appointed but must use TaskContract / AgentResult / Evidence / QA exits.
11. Appointment creates MissionTeamMember and MissionAssignment in one UOW.
12. Appointment emits `AgentJoined` and `MissionAssignmentCreated` event candidates.
13. Duplicate appointment with same idempotency key is idempotent.
14. Duplicate active assignment with a different idempotency key returns conflict.
15. Release marks assignment/member released and never deletes history.
16. Release emits `AgentReleased` and `MissionAssignmentReleased`.
17. Replacement links old and new assignment refs and records replacement reason.
18. Replacement emits `AgentReplaced`.
19. Same AgentProfile may have different MissionAssignments in different missions without changing stable org identity.
20. Organization manager relationship does not grant team membership, task authority, or discussion visibility.
21. Worker cannot receive a Mission Task without active MissionAssignment.
22. Team listing returns mission team state without importing runtime providers, discussion storage, or transport.
23. Staffing audit contains selected candidate rationale and rejected candidate reasons.
24. Event candidate emission and team repository writes commit atomically.
25. Legacy fields such as squad/team catalog semantics do not appear in new core contracts except migration notes.
26. Integration test proves a staffed worker can be assembled by Goal 07 and then receive a TaskContract through the standard runtime path.

### Required Completion Commands

```bash
go test ./internal/agent/core ./internal/mission/team
rg "internal/agent/organization" orrery_v2/internal
rg "RuntimeAdapter|RuntimeSpec|ToolRef|SkillRef|MCP|RPA|CLI" orrery_v2/internal/mission/team
rg "internal/agent/runtime/providers|internal/mission/discussion|internal/transport" orrery_v2/internal/mission/team
rg "squad_id|market_kind|catalog_kind|comment_author|workspace_member" orrery_v2/internal/mission/team
```

Expected:

- `go test` passes.
- `internal/agent/organization` does not exist.
- Runtime/tool/skill terms appear only in tests, policy rejection tests, or comments explicitly proving they cannot be members.
- `mission/team` does not import runtime providers, discussion storage, or transport.
- Legacy squad/catalog/comment/workspace terms appear only in migration notes that explain removed semantics.

## Implementation Plan and Commit Boundaries

Each substage must be implemented with TDD and committed independently.

### Substage 8.1: Contracts and Legacy Boundary Audit

Goal:

- Define final organization, directory, availability, cost, quality, risk, team, assignment, staffing, decision, and audit contracts.
- Document legacy Orrery migration source paths.
- Prove no `internal/agent/organization/*` package is needed.

Required commit:

```bash
git commit -m "stage8 define staffing contracts"
```

### Substage 8.2: Agent Directory and Profile Query

Goal:

- Implement profile repository/query interfaces and directory filtering.
- Add availability, cost, quality, and risk query surfaces.
- Reject non-agent candidates.

Required commit:

```bash
git commit -m "stage8 implement agent directory queries"
```

### Substage 8.3: Staffing Policy and Candidate Selection

Goal:

- Implement AssignmentPolicy and CandidateSelector.
- Score candidates and record accepted/rejected reasons.
- Cover duplicate, authority, availability, skill/tool, risk, budget, and human/AI policy cases.

Required commit:

```bash
git commit -m "stage8 implement staffing policy selection"
```

### Substage 8.4: MissionTeam Repository and Events

Goal:

- Implement durable MissionTeamRepository.
- Persist team/member/assignment/request/decision/audit through platform UOW.
- Emit event candidates atomically.

Required commit:

```bash
git commit -m "stage8 persist mission team events"
```

### Substage 8.5: Appointment, Release, and Replacement Service

Goal:

- Implement MissionTeamService workflows.
- Enforce idempotency/conflict behavior.
- Preserve release/replacement history.

Required commit:

```bash
git commit -m "stage8 implement staffing service workflows"
```

### Substage 8.6: AgentInstance Provisioning Integration

Goal:

- Integrate Goal 07 AgentInstance provisioning/reuse boundary.
- Prove appointed workers can be assembled and assigned TaskContract.
- Ensure provisioning failure aborts appointment before commit.

Required commit:

```bash
git commit -m "stage8 integrate agent instance staffing"
```

### Substage 8.7: Migration Notes and Final Acceptance

Goal:

- Complete migration notes.
- Run all acceptance commands.
- Fix forbidden imports, legacy field leakage, and missing test files.

Required commit:

```bash
git commit -m "stage8 complete staffing acceptance"
```

## Cross Goal Contracts

### CROSS_GOAL: 01-infrastructure-foundation

Needs:

- RoutedUOW.
- DBRouter.
- migrations.
- event/outbox.
- logger/metrics/tracing.
- config/secret.
- testkit.

Goal 08 contract:

- Team state and event candidates commit in one UOW.
- No private DB, queue, config, logger, or global in-memory staffing state.

### CROSS_GOAL: 02-core-contracts

Needs:

- AgentProfile.
- AgentIdentity.
- AgentOrgIdentity.
- AgentInstanceRef.
- ToolRef / SkillRef / MemoryProfileRef.
- RuntimeSpec.

Goal 08 contract:

- Consume existing identity/profile contracts.
- Add query and operational staffing contracts only when they do not belong in Goal 02 static schema.

### CROSS_GOAL: 03-lifecycle-signals

Needs:

- Signals when staffing decisions affect running agent/task lifecycles.

Goal 08 contract:

- Staffing service may emit lifecycle-adjacent signals through interfaces, but MissionEvent remains business fact source.

### CROSS_GOAL: 04-event-ledger-projection

Needs:

- Event candidate boundary.
- TeamView projection.

Goal 08 contract:

- Emit staffing and assignment event candidates.
- Do not write projections directly.

### CROSS_GOAL: 05-resource-registry-memory

Needs:

- Skill/tool refs and memory profile refs for directory filtering.

Goal 08 contract:

- Tool/Skill/Memory remain refs on AgentProfile and query inputs.
- They can never become MissionTeamMember.

### CROSS_GOAL: 06-task-delivery-qa

Needs:

- TaskContract, AgentResult, Deliverable, Evidence, QA exits.

Goal 08 contract:

- Appointed members must deliver through the same task delivery and QA contracts.
- RealHumanAgent uses the same output path.

### CROSS_GOAL: 07-base-agent-runtime

Needs:

- AgentAssembler.
- AgentInstance provisioning/reuse.
- RuntimeSpec availability.

Goal 08 contract:

- Staffing selects AgentProfile/AgentInstance; Goal 07 assembles runnable instance.
- RuntimeAdapter is never a team member.

### CROSS_GOAL: 09-discussion-inbox

Needs:

- Discussion membership materialization.
- Human inbox/handoff.
- Notifications for appointment/release/replacement.

Goal 08 contract:

- Emit membership/assignment facts only.
- Goal 09 consumes facts to update discussion/inbox state.
- Organization manager relationship alone grants no discussion visibility.

### CROSS_GOAL: 10-api-transport

Needs:

- App service commands and query DTOs.

Goal 08 contract:

- Transport calls app services only.
- No HTTP/WebSocket handler imports mission/team repositories directly.

## Definition of Done

Goal 08 is complete only when:

- All required test files exist or this README explicitly documents an equivalent merged test.
- All acceptance scenarios pass.
- All completion commands pass.
- No `internal/agent/organization/*` peer package exists.
- Tool, skill, runtime, API, RPA, CLI, MCP server, and market template cannot become MissionTeamMember.
- MissionAssignment never mutates stable AgentOrgIdentity.
- MissionTeam mutations and MissionEvent candidates commit atomically.
- Candidate selection records rejected reasons and selected rationale.
- Release and replacement preserve historical assignments.
- RealHumanAgent follows the same delivery/evidence/QA contracts as AI agents.
- AgentInstance provisioning is integrated with Goal 07 and appointment aborts on provisioning failure.
- Mission/team packages do not import runtime providers, discussion storage, transport, or frontend.
- Migration notes document adopted Orrery code, removed legacy semantics, residual differences, and test coverage.
