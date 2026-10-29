# Goal 07: Agent Assembly, BaseAgent and Runtime

> This goal must be implemented as a production runtime slice, not as a compile-only skeleton. The previous thin Stage 7 attempt is not accepted as completion: it introduced useful contract sketches, but it did not fully migrate Multica providers, did not use platform queue/claim/liveness infrastructure, did not wire real delivery/QA/summary/notifier components, and did not build ContextPack from the upstream contracts.

## Purpose

Goal 07 implements the unified Agent assembly entry point, BaseAgent execution kernel, and runtime adapter subsystem for the Agent Company Mission Control architecture.

It consumes the foundations from earlier goals:

- Goal 01: platform queue, claim, liveness, sweeper, FileStore, config, secret, logging, metrics, tracing, RoutedUOW, and testkit.
- Goal 02: AgentProfile, RuntimeSpec, ContextPack, AgentResult, TaskContract, Deliverable, Evidence, and refs.
- Goal 03: lifecycle signals and SignalBus.
- Goal 04: MissionEvent candidate/event sink boundary.
- Goal 05: tool resolver, skill resolver, memory binder, memory digest, and discussion digest boundaries.
- Goal 06: TaskDeliveryService, ResultPolicyChecker, delivery/evidence/QA contracts.

Codex, Claude, Cursor, Gemini, Browser, Human, HTTP, and later providers are runtime adapters. They are not team members, not task assignees, and not MissionTeam participants.

## Current Rework Decision

The implementation must be reworked around these decisions:

1. Preserve useful type names and tests only when they fit the final boundaries.
2. Replace skeleton providers with migrated Multica provider/session implementations.
3. Replace in-memory daemon store with adapters over platform execution queue/claim/liveness/sweeper.
4. Replace placeholder components with real delivery/evidence/QA/summary/notifier components or explicit interfaces backed by earlier goals.
5. Move ContextPack construction into a real builder that consumes task contract, mission goal, discussion digest, memory digest, tools, skills, permissions, and source refs.
6. Ensure AgentAssembler returns a fully runnable AgentInstance. A built agent must be capable of handling a TaskContract through commit, or Build must fail.
7. Treat all thin code paths as incomplete until covered by the acceptance scenarios in this README.

## Global Requirements

Before implementation, follow:

- `implementation-goals/README.md`
- `project-structure-guidelines.md`
- `module-decomposition-and-implementation-guide.md`
- `technical-architecture-rewrite-plan.md`

This goal may not weaken global architecture requirements. It may not create MVP-only substitutes for already available platform infrastructure.

## Stage 0 Infrastructure Usage Contract

Agent runtime must use platform infrastructure from Goal 01:

- Queue and runnable work dispatch must use platform execution queue abstractions.
- Claim and lease ownership must use platform claim/lease infrastructure.
- Heartbeat/liveness must use platform liveness infrastructure.
- Stale run recovery must use platform sweeper infrastructure.
- Raw runtime output and large transcripts must use FileStore/artifact storage.
- Secrets must use platform secret providers and may not be stored in runtime structs.
- Config must come from ConfigManager last-known-good snapshots and config subscriptions.
- Logging, metrics, tracing, and health must use platform logging/metrics/tracing/health.
- Database writes must go through RoutedUOW or earlier goal repositories/services.

Agent runtime must not introduce another private queue, private lease store, private liveness store, private config system, private secret store, or private logger.

## Production Responsibilities

Goal 07 is responsible for:

- `Agent` interface.
- `AgentInstance`.
- `AgentAssembler`.
- `ComponentRegistry`.
- `ResourceBinding`.
- `RuntimeBinding`.
- `MemoryBinding`.
- `BaseAgent`.
- `BaseAgentPipeline`.
- `ContextPackBuilder` integration.
- `PolicySet` / result policy bridge.
- `SummaryGenerator`.
- `Notifier`.
- `TraceRecorder`.
- `RuntimeAdapter`.
- `RuntimeRequest`.
- `RuntimeResponse`.
- `RuntimeResult`.
- `ResultNormalizer`.
- `ResultValidator`.
- `RuntimeRegistry`.
- `RuntimeBackend`.
- `RuntimeSession`.
- `ProcessRunner`.
- `ExecEnv`.
- `DaemonRunner`.
- `HumanRuntimeAdapter`.
- `HTTPRuntimeAdapter`.
- provider adapters: Codex, Claude, Cursor, Gemini, Browser, and extension points for later providers.
- migration notes for Multica and previous Orrery code.

Goal 07 is not responsible for:

- AgentProfile static schema design.
- Tool/Skill/Memory registry storage.
- TaskLifecycle state machine implementation.
- QA domain business rules beyond invoking Goal 06 interfaces.
- MissionTeam staffing.
- DiscussionGroup storage.
- HTTP/WebSocket transport.
- Frontend.
- Legacy `agent_task_queue` compatibility.

## Forbidden Implementation Patterns

Do not implement:

- `internal/agent/capabilities/*`.
- Concrete agents that bypass AgentAssembler to choose runtime/tools/skills/memory/QA.
- BaseAgent reading raw memory store history.
- RuntimeAdapter writing Mission, Task, Discussion, projection, timeline, or delivery state.
- RuntimeAdapter becoming a MissionTeam member.
- Runtime core fields named or modeled around `issue_id`, `comment_id`, `chat_session_id`, `autopilot_id`, or `squad_id`.
- Direct conversion from old `agent_task_queue.result` to `AgentResult`.
- In-memory queue/claim/liveness as production implementation.
- Placeholder components whose only behavior is `Name() string`.
- Provider adapters that only run `--version`, echo stdout, or wrap a generic process without provider protocol parsing.

## Package Layout

Target code belongs under `orrery_v2/internal/agent`.

```text
internal/agent/core/
  agent.go
  instance.go
  base_agent.go
  pipeline.go
  run_state.go
  context_pack_builder.go
  result_normalizer.go
  result_validator.go
  summary_generator.go
  trace.go
  errors.go

internal/agent/assembly/
  assembler.go
  component_registry.go
  resource_binding.go
  memory_binding.go
  runtime_binding.go
  validation.go
  errors.go

internal/agent/component/
  policy/
  delivery/
  evidence/
  qa/
  summary/
  notifier/

internal/agent/runtime/core/
  adapter.go
  backend.go
  registry.go
  session.go
  request.go
  response.go
  result.go
  options.go
  usage.go
  errors.go

internal/agent/runtime/process/
  command.go
  runner.go
  stderr_tail.go
  version.go
  env.go
  args.go

internal/agent/runtime/providers/
  codex/
  claude/
  cursor/
  gemini/
  browser/
  human/
  http/

internal/agent/runtime/execenv/
  prepare.go
  workspace.go
  context_files.go
  skills.go
  cleanup.go
  gc_meta.go

internal/agent/runtime/daemon/
  daemon.go
  worker.go
  heartbeat.go
  claim.go
  watchdog.go
  concurrency.go
  recovery.go
  store_adapter.go

internal/agent/runtime/migration_notes/
  multica.md
  orrery.md
```

Dependency direction:

```text
agent/core -> agent/lifecycle interfaces, mission/task interfaces, platform interfaces
agent/assembly -> resource/tool resolver, resource/skill resolver, memory binder, runtime registry
agent/runtime -> platform execution, filestore, logging, config, secret
agent/runtime/providers -> runtime/core and runtime/process only, no mission/discussion/event store
mission/task -> app service or BaseAgent facade, not runtime providers
transport -> app services only
```

If Go import cycles appear, fix the boundary. Do not move contracts into random packages to silence the compiler.

## Agent Assembly Design

### Required Flow

```text
Load AgentProfile
-> validate identity/persona/org identity/runtime spec/profile enabled state
-> resolve ToolRef through ToolResolver
-> resolve SkillRef through SkillResolver
-> bind MemoryProfileRef through MemoryBinder
-> load delivery/evidence/QA/summary/notifier/policy components
-> resolve RuntimeAdapter through RuntimeRegistry
-> construct BaseAgent with all required dependencies
-> return AgentInstance
```

`AgentAssembler.Build` must fail fast and return no partial AgentInstance if any required binding fails.

### Interface Shape

```go
type AgentAssembler interface {
    Build(ctx context.Context, profile agentcore.AgentProfile, opts BuildOptions) (agentcore.AgentInstance, error)
}

type ComponentRegistry interface {
    Delivery(profile agentcore.AgentProfile) (DeliveryComponent, error)
    Evidence(profile agentcore.AgentProfile) (EvidenceComponent, error)
    QA(profile agentcore.AgentProfile) (QAComponent, error)
    Summary(profile agentcore.AgentProfile) (SummaryComponent, error)
    Notifier(profile agentcore.AgentProfile) (NotifierComponent, error)
    Policy(profile agentcore.AgentProfile) (PolicyComponent, error)
}
```

Structured binding errors must use stable codes:

```text
tool_binding_denied
skill_version_not_found
memory_binding_denied
runtime_not_available
component_missing
profile_invalid
```

### Assembly Acceptance

An assembled AgentInstance must expose:

- Agent identity and profile.
- Bound tools with granted permissions and secret env refs, not secret values.
- Bound skills with matched versions and prompt summaries.
- Memory digest refs from MemoryBinder.
- RuntimeAdapter with capabilities.
- Delivery submitter.
- ResultPolicyChecker.
- SummaryGenerator.
- ResultNormalizer.
- ResultValidator.
- Notifier.
- TraceRecorder.
- Lifecycle SignalBus bridge.

If any of these are absent for a runnable profile, Build must fail.

## BaseAgent Pipeline

BaseAgent is the unified execution protocol. It is not a giant business class and not a specific PM/Worker/QA implementation.

### Fixed Stages

```text
ReceiveTask
AcceptTask
BuildContext
RunRuntime
NormalizeResult
ValidateResult
CommitResult
ReplyOrNotify
```

### Signal Order

Each run must emit lifecycle signals in this order:

```text
BeforeTaskReceived
AfterTaskReceived
BeforeTaskAccepted
AfterTaskAccepted
BeforeContextBuild
AfterContextBuild
BeforeRuntimeStart
RuntimeStarted
RuntimeProgress*
BeforeRuntimeFinish
AfterRuntimeFinish
BeforeResultNormalize
AfterResultNormalize
BeforeResultValidate
AfterResultValidate
BeforeResultCommit
AfterResultCommit
BeforeAgentReply
AfterAgentReply
```

Failure signals:

```text
AgentFailed
AgentBlocked
AgentCancelled
AgentTimeout
```

`RuntimeStarted` must be emitted after the runtime process/session is actually started, not after it finishes.

### Required Enforcement

BaseAgent must enforce:

- Every runtime output is normalized into `AgentResult`.
- Missing summary is handled by SummaryGenerator or rejected.
- Missing required evidence rejects commit.
- Missing acceptance mapping rejects commit when acceptance criteria exist.
- Untraceable evidence rejects commit.
- ResultPolicyChecker runs before commit.
- Commit only calls TaskDeliveryService/EventSink/app service interfaces.
- BaseAgent does not write old queue rows.
- Internal trace is stored as ref, not injected into discussion context.
- Failure classification emits the correct lifecycle signal.
- Timeout and idle watchdog lead to `AgentTimeout`.
- Cancellation leads to `AgentCancelled`.
- Blocked runtime result leads to `AgentBlocked`.

## ContextPack Builder

`ContextPack` is the only runtime context entry point. Runtime providers must not read full mission history, full discussion messages, raw memory rows, or task queue rows.

### RuntimeRequest Must Include

```text
run_id
agent_id
mission_id
task_id
context_pack
tool_bindings[]
skill_bindings[]
runtime_options
deadline
trace_policy
```

### ContextPack Must Include

```text
mission_final_goal
discussion_group_goal
agent_organization_identity
agent_mission_role
worker_personal_goal
task_contract
acceptance_criteria
required_evidence
personal_memory_digest
collective_memory_digest
discussion_digest
skills summary
tools summary
permission constraints
source_refs
```

### Builder Rules

- All agents receive mission final goal and discussion group goal.
- Worker tasks receive full worker personal goal.
- Non-worker collaborators receive worker goal refs only when needed for collaboration.
- Memory comes only from MemoryBinder digest output.
- Discussion context comes only from DiscussionDigest.
- Protected source refs must be immutable and have precedence.
- Raw memory and raw discussion messages are rejected.

## RuntimeAdapter Design

RuntimeAdapter only executes.

```go
type RuntimeAdapter interface {
    Run(ctx context.Context, req RuntimeRequest) (RuntimeResponse, error)
    Cancel(ctx context.Context, runID agentcore.AgentRunID) error
    Capabilities() RuntimeCapabilities
}
```

### RuntimeResponse

```text
run_id
status
raw_output_ref
messages[]
tool_calls[]
artifacts[]
usage
error
completed_at
session_id
provider_metadata
```

### RuntimeStatus

```text
completed
failed
blocked
timeout
cancelled
```

### ResultNormalizer

The normalizer maps:

```text
raw_output_ref -> AgentResult.internal_trace_ref
provider final text -> Summary candidate
declared artifacts -> Deliverable candidates
tool calls/tool results -> Evidence/audit refs
provider error -> classified failure
human submission -> Summary/Deliverable/Evidence candidates
usage -> trace/metrics only
```

The normalizer must not invent evidence by array index. It may produce candidates, but ResultValidator/ResultPolicyChecker decides if they satisfy the task.

## Runtime Backend and Session

Provider implementations must model the Multica backend/session/result pattern:

```go
type RuntimeBackend interface {
    Execute(ctx context.Context, prompt string, opts ExecOptions) (*RuntimeSession, error)
}

type RuntimeSession struct {
    Messages <-chan RuntimeMessage
    Result   <-chan RuntimeResult
}
```

`RuntimeAdapter.Run` wraps backend sessions into the unified `RuntimeResponse`.

Provider adapters must support:

- process startup errors with stderr tail.
- structured streaming messages.
- final result status.
- session id or resume id where provider supports it.
- token usage where provider exposes it.
- semantic inactivity timeout where applicable.
- cancellation.
- provider-specific blocked args filtering.
- provider-specific env filtering.

## Provider Migration Requirements

### Multica Source Paths

Review and migrate from:

```text
multica/server/pkg/agent/agent.go
multica/server/pkg/agent/codex.go
multica/server/pkg/agent/claude.go
multica/server/pkg/agent/gemini.go
multica/server/pkg/agent/cursor.go
multica/server/pkg/agent/opencode.go
multica/server/pkg/agent/openclaw.go
multica/server/pkg/agent/hermes.go
multica/server/pkg/agent/stderr_tail.go
multica/server/pkg/agent/version.go
multica/server/internal/daemon/execenv/
multica/server/internal/daemon/
```

### Codex Adapter

Codex must migrate the app-server JSON-RPC/session implementation, not a generic CLI wrapper.

Required behavior:

- spawn `codex app-server --listen stdio://`.
- initialize JSON-RPC.
- start or resume thread.
- send `turn/start`.
- parse legacy and raw notifications.
- map agent messages, tool use, tool result, file changes, status, errors.
- capture token usage from notifications and session JSONL fallback.
- enforce semantic inactivity timeout.
- auto-respond to daemon-mode approval requests through policy.
- return session/thread id.
- include stderr tail on startup/handshake failures.

### Claude Adapter

Claude must migrate stream-json behavior.

Required behavior:

- spawn `claude -p --output-format stream-json --input-format stream-json`.
- pass model, max turns, system prompt, resume session.
- write MCP config temp file when provided.
- parse assistant/user/system/result/log messages.
- map tool use/tool result.
- accumulate text output and usage.
- include stderr tail on failures.
- filter protocol-critical custom args.
- resolve failed resume session ids correctly.

### Gemini and Cursor Adapters

Gemini and Cursor must not be aliases of Codex.

Required behavior:

- implement provider-specific command shape.
- parse provider stream/json messages where available.
- map messages/tool calls/status/usage/session id into runtime contracts.
- capture stderr tail and version.
- filter protocol-critical args.

If a provider protocol is not yet available, the adapter must be explicitly marked unavailable by RuntimeRegistry with `runtime_not_available`, not registered as a fake working provider.

### Browser Adapter

Browser adapter must:

- depend on a browser automation backend interface.
- consume ContextPack and task constraints.
- return artifacts/evidence refs for screenshots, traces, console logs, network logs, or DOM captures.
- support timeout/cancel.
- not import frontend or transport packages.

### HTTP Adapter

HTTP adapter must:

- sign or authorize requests through platform secret/config interfaces.
- send RuntimeRequest as JSON.
- validate RuntimeResponse schema.
- enforce timeout.
- classify non-2xx responses.
- not expose secrets in request logs.

### Human Adapter

Human adapter must:

- create or wait on a human inbox/handoff interface.
- require human summary.
- accept deliverable/evidence/blocker/request submission.
- convert human submission into RuntimeResponse.
- use the same BaseAgent normalization/validation pipeline.
- support cancellation and timeout.

## Process Runner

`runtime/process` may be migrated from Multica.

Required behavior:

- command construction with executable, args, cwd, env, stdin.
- environment merge/filter helpers.
- hidden windows on Windows where applicable.
- stdout capture and optional streaming.
- bounded stderr tail.
- timeout and cancellation classification.
- process exit code capture.
- `--version` detection with noisy-line filtering.
- tests for Windows and non-Windows command behavior.

## ExecEnv

ExecEnv prepares isolated per-run workspaces. It must be migrated from Multica `internal/daemon/execenv` but use ContextPack instead of issue/comment semantics.

Required behavior:

- deterministic env root prediction from run id.
- workspace root creation.
- workdir/output/logs directories.
- context files from ContextPack.
- tool summary file.
- skill hydration file(s).
- provider-specific homes such as `CODEX_HOME`.
- MCP config materialization when runtime options contain MCP config.
- cleanup policy.
- GC metadata that references AgentRun, not issue/comment/chat/autopilot.
- no raw memory or raw discussion transcript file.

Context files must include:

```text
Mission final goal
Discussion group goal
Agent org identity
Agent mission role
Worker personal goal
Task contract summary
Acceptance criteria
Required evidence
Memory digest summaries
Discussion digest summary
Skill summaries
Tool summaries
Permission constraints
Source refs
```

## Runtime Daemon

The daemon must use Goal 01 platform execution infrastructure.

### AgentRunLease

```text
run_id
agent_id
runtime_id
mission_id
task_id
status
claimed_by
lease_expires_at
heartbeat_at
attempt
```

### Required Responsibilities

```text
claim runnable AgentRun
prepare execenv
run RuntimeAdapter
heartbeat while running
enforce hard timeout
enforce semantic inactivity watchdog
submit RuntimeResponse
persist raw output/artifact refs
cleanup workspace
recover stale lease through sweeper
respect concurrency limits
record metrics/traces
```

### Required Files

```text
internal/agent/runtime/daemon/store_adapter.go
internal/agent/runtime/daemon/claim.go
internal/agent/runtime/daemon/heartbeat.go
internal/agent/runtime/daemon/watchdog.go
internal/agent/runtime/daemon/concurrency.go
internal/agent/runtime/daemon/recovery.go
internal/agent/runtime/daemon/worker.go
internal/agent/runtime/daemon/daemon.go
```

No production daemon file may depend on an in-memory-only run store except in tests.

## Component Registry

ComponentRegistry must resolve real components:

- Delivery uses Goal 06 `TaskDeliveryService` or an adapter interface.
- Evidence uses Goal 06 evidence validators/contracts.
- QA uses Goal 06 QA checker/service boundaries.
- Summary uses production SummaryGenerator.
- Notifier uses a discussion/inbox interface when available, or a no-op only when policy says notifications are disabled.
- Policy uses ResultPolicyChecker.

A default registry returning string-named placeholder components is not accepted.

## SummaryGenerator

SummaryGenerator must:

- use provider final text when valid.
- generate a concise summary from deliverables/evidence when final text is absent.
- reject submission when neither runtime output nor artifacts can support a summary.
- never summarize raw full trace into discussion context.

## Notifier

Notifier must:

- decide whether to reply/mention based on policy and runtime result.
- call discussion/inbox interfaces only.
- emit `BeforeAgentReply` and `AfterAgentReply`.
- avoid writing discussion storage directly.

## TraceRecorder

TraceRecorder must:

- store raw provider output through FileStore or artifact store.
- return a stable trace ref.
- redact secrets.
- support bounded inline snippets only for logs.
- keep internal traces out of default discussion context.

## Testing and Acceptance

### Required Test Files

```text
internal/agent/assembly/assembler_test.go
internal/agent/assembly/runtime_binding_test.go
internal/agent/assembly/component_registry_test.go
internal/agent/core/base_agent_pipeline_test.go
internal/agent/core/context_pack_builder_test.go
internal/agent/core/result_normalizer_test.go
internal/agent/core/result_validator_test.go
internal/agent/core/summary_generator_test.go
internal/agent/core/trace_recorder_test.go
internal/agent/runtime/core/registry_test.go
internal/agent/runtime/process/runner_test.go
internal/agent/runtime/process/version_test.go
internal/agent/runtime/execenv/prepare_test.go
internal/agent/runtime/execenv/context_files_test.go
internal/agent/runtime/execenv/skills_test.go
internal/agent/runtime/daemon/claim_test.go
internal/agent/runtime/daemon/heartbeat_test.go
internal/agent/runtime/daemon/watchdog_test.go
internal/agent/runtime/daemon/recovery_test.go
internal/agent/runtime/daemon/concurrency_test.go
internal/agent/runtime/providers/codex/codex_test.go
internal/agent/runtime/providers/claude/claude_test.go
internal/agent/runtime/providers/gemini/gemini_test.go
internal/agent/runtime/providers/cursor/cursor_test.go
internal/agent/runtime/providers/browser/browser_test.go
internal/agent/runtime/providers/human/human_test.go
internal/agent/runtime/providers/http/http_test.go
```

### Acceptance Scenarios

1. AgentAssembler resolves ToolRef, SkillRef, MemoryProfileRef, RuntimeSpec, and all components into a runnable AgentInstance.
2. Binding failure returns structured error and no partial agent.
3. Missing required component fails Build.
4. Fake RuntimeAdapter completes full BaseAgent pipeline.
5. Every lifecycle stage emits signal in the documented order.
6. Runtime progress messages emit `RuntimeProgress`.
7. ContextPackBuilder injects mission goal, discussion goal, worker goal, task contract, acceptance criteria, required evidence, memory digest, discussion digest, skills, tools, permissions, and source refs.
8. ContextPackBuilder rejects raw memory and raw discussion messages.
9. RuntimeResponse normalizes into AgentResult without inventing evidence.
10. Missing summary triggers SummaryGenerator or rejects result.
11. Missing required evidence rejects commit.
12. Missing acceptance mapping rejects commit.
13. Runtime error emits AgentFailed.
14. Runtime blocked result emits AgentBlocked.
15. Context timeout and idle watchdog emit AgentTimeout.
16. Cancelled run emits AgentCancelled.
17. Commit calls TaskDeliveryService/EventSink boundary only.
18. Notifier emits reply signals and uses discussion/inbox interface only.
19. RuntimeAdapter does not write Mission/Task/Discussion state.
20. Codex provider supports app-server JSON-RPC session flow.
21. Claude provider supports stream-json session flow.
22. Human provider creates/waits for handoff and returns standard RuntimeResponse.
23. HTTP provider validates schema and classifies HTTP errors.
24. ExecEnv writes complete context files and provider homes from ContextPack.
25. Daemon claims platform run, heartbeats, enforces watchdog, submits response, and recovers stale lease.
26. Provider migrations do not preserve legacy runtime core fields.

### Required Completion Commands

```bash
go test ./internal/agent/core ./internal/agent/assembly ./internal/agent/runtime/...
rg "issue_id|comment_id|chat_session_id|autopilot_id|squad_id" orrery_v2/internal/agent/runtime
rg "internal/mission/discussion|internal/transport" orrery_v2/internal/agent/runtime
```

Expected:

- `go test` passes.
- The first `rg` returns no matches except migration notes that explicitly explain removed legacy semantics.
- The second `rg` returns no matches.

## Implementation Plan and Commit Boundaries

Each substage must be implemented with TDD and committed independently.

### Substage 7.1: Contract Correction and Skeleton Removal

Goal:

- Remove or refactor any current Stage 7 code that allows fake success.
- Establish final interfaces for AgentInstance, BaseAgent dependencies, RuntimeAdapter, RuntimeBackend, RuntimeSession, ResultNormalizer, ResultValidator, SummaryGenerator, TraceRecorder, Notifier, and component registry.

Required commit:

```bash
git commit -m "stage7 define production runtime contracts"
```

### Substage 7.2: Multica Runtime Backend Migration

Goal:

- Migrate Multica backend/session/result/process/stderr/version logic.
- Keep provider semantics behind runtime contracts.

Required commit:

```bash
git commit -m "stage7 migrate multica runtime backends"
```

### Substage 7.3: Provider Adapters

Goal:

- Implement Codex, Claude, Gemini, Cursor, Browser, Human, and HTTP adapters according to provider requirements.
- Do not register unavailable providers as working.

Required commit:

```bash
git commit -m "stage7 implement runtime provider adapters"
```

### Substage 7.4: ContextPackBuilder and ExecEnv

Goal:

- Build ContextPack from upstream contracts and bindings.
- Prepare execution environments from ContextPack only.

Required commit:

```bash
git commit -m "stage7 build context packs and exec environments"
```

### Substage 7.5: BaseAgent Pipeline

Goal:

- Implement the full BaseAgent lifecycle, normalization, validation, commit, notification, trace recording, and failure classification.

Required commit:

```bash
git commit -m "stage7 implement base agent pipeline"
```

### Substage 7.6: AgentAssembler and Components

Goal:

- Wire real resolvers, binders, runtime registry, delivery/evidence/QA/summary/notifier/policy components, SignalBus, and trace recorder.
- Build must fail when any required runtime dependency is absent.

Required commit:

```bash
git commit -m "stage7 wire agent assembler components"
```

### Substage 7.7: Runtime Daemon

Goal:

- Implement platform-backed claim, heartbeat, watchdog, concurrency, response submission, cleanup, and stale recovery.

Required commit:

```bash
git commit -m "stage7 implement platform runtime daemon"
```

### Substage 7.8: Migration Notes and Final Acceptance

Goal:

- Complete migration notes.
- Run all acceptance commands.
- Fix any illegal legacy dependencies or fields.

Required commit:

```bash
git commit -m "stage7 complete runtime acceptance"
```

## Cross Goal Contracts

### CROSS_GOAL: 01-infrastructure-foundation

Needs:

- Queue.
- Claim store.
- Liveness.
- Sweeper.
- FileStore.
- Logger.
- Config.
- Secret.
- Testkit.

Goal 07 contract:

- Runtime daemon only depends on platform interfaces.
- Agent runtime does not create private infrastructure.

### CROSS_GOAL: 02-core-contracts

Needs:

- AgentProfile.
- RuntimeSpec.
- ContextPack.
- AgentResult.
- TaskContract.

Goal 07 contract:

- Consume existing contracts.
- Add only runtime-specific execution contracts when they do not belong to Goal 02.
- RuntimeResponse -> AgentResult mapping must be explicit and tested.

### CROSS_GOAL: 03-lifecycle-signals

Needs:

- SignalBus.
- SignalName.
- AgentRunState.

Goal 07 contract:

- BaseAgent emits Goal 03 lifecycle signals in documented order.
- RuntimeProgress uses safe payload refs, not full trace.

### CROSS_GOAL: 04-event-ledger-projection

Needs:

- EventSink or MissionEvent candidate boundary after commit/failure.

Goal 07 contract:

- BaseAgent can submit event candidates.
- Runtime providers never write projections.

### CROSS_GOAL: 05-resource-registry-memory

Needs:

- ToolResolver.
- SkillResolver.
- MemoryBinder.
- MemoryDigest.
- DiscussionDigest.

Goal 07 contract:

- AgentAssembler consumes resolver/binder interfaces.
- ContextPack receives digest refs and summaries only.

### CROSS_GOAL: 06-task-delivery-qa

Needs:

- TaskDeliveryService.
- ResultPolicyChecker.
- QA component.

Goal 07 contract:

- BaseAgent commits through TaskDeliveryService.
- ResultPolicyChecker runs before commit.
- QA component is invoked through component boundary, not reimplemented.

### CROSS_GOAL: 09-discussion-inbox

Needs:

- Notifier.
- Discussion digest.
- Human handoff/inbox.

Goal 07 contract:

- BaseAgent calls discussion/inbox interfaces only.
- Notifier handles reply/mention/human handoff policy.

## Definition of Done

Goal 07 is complete only when:

- All required test files exist or the README explicitly documents why a file was merged into an equivalent focused test.
- All acceptance scenarios pass.
- All completion commands pass.
- Codex and Claude providers are migrated from real Multica behavior, not generic wrappers.
- Runtime daemon uses platform queue/claim/liveness/sweeper.
- AgentAssembler builds a fully runnable AgentInstance or fails.
- BaseAgent cannot report commit success without actual delivery commit.
- ContextPackBuilder exists and is the only runtime context path.
- Migration notes document source paths, adopted code, removed fields, residual differences, and test coverage.
- No runtime package imports mission discussion storage or transport.
- No runtime core contract preserves old issue/comment/chat/autopilot/squad semantics.
