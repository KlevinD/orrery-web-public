package team

import (
	"context"
	"testing"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/resource/skill"
	"orrery_v2/internal/resource/tool"
)

func TestStaffedWorkerCanReceiveTaskContractThroughMissionAssignment(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-worker", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	assignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-worker", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}
	resolver := NewTaskAssignmentResolver(env.UnitOfWork, NewPlatformTeamRepository())
	contract := missiontask.TaskContract{
		ID:        "task-001",
		MissionID: "mission-001",
		Assignee:  missiontask.TaskAssigneeRef{MissionAssignmentRef: &missiontask.MissionAssignmentRef{ID: missiontask.MissionAssignmentID(assignment.ID)}},
		Goal:      "ship the staffed worker flow",
	}

	ref, err := missiontask.ResolveTaskAssignee(ctx, contract.Assignee, resolver)
	if err != nil {
		t.Fatalf("ResolveTaskAssignee: %v", err)
	}
	if ref.ProfileRef.ID != assignment.AgentProfileID {
		t.Fatalf("resolved profile = %#v, want %s", ref.ProfileRef, assignment.AgentProfileID)
	}
	if ref.InstanceRef.ID != assignment.AgentInstanceID {
		t.Fatalf("resolved instance = %#v, want %s", ref.InstanceRef, assignment.AgentInstanceID)
	}
}

func TestStaffedWorkerCanBeAssembledByGoal07AndReceiveTaskContract(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-worker", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	assignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-worker", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}

	runtimes := newStaffedSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, staffedSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := agentassembly.NewAssembler(agentassembly.AssemblerOptions{
		ToolResolver:      staffedToolResolver{},
		SkillResolver:     staffedSkillResolver{},
		MemoryBinder:      staffedMemoryBinder{},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: staffedProductionComponents(),
		Pipeline: agentassembly.ComponentPipelineProvider{
			Registry:   staffedProductionComponents(),
			Trace:      staffedDurableTraceRecorder{},
			Normalizer: staffedNormalizer{},
		},
	})
	instance, err := assembler.Build(ctx, profile, agentassembly.BuildOptions{InstanceID: assignment.AgentInstanceID})
	if err != nil {
		t.Fatalf("assemble staffed worker: %v", err)
	}
	if instance.ID() != assignment.AgentInstanceID {
		t.Fatalf("assembled instance = %s, want assignment instance %s", instance.ID(), assignment.AgentInstanceID)
	}

	resolver := NewTaskAssignmentResolver(env.UnitOfWork, NewPlatformTeamRepository())
	contract := missiontask.TaskContract{
		ID:        "task-001",
		MissionID: "mission-001",
		Assignee:  missiontask.TaskAssigneeRef{MissionAssignmentRef: &missiontask.MissionAssignmentRef{ID: missiontask.MissionAssignmentID(assignment.ID)}},
		Goal:      "ship through assembled worker",
	}
	ref, err := missiontask.ResolveTaskAssignee(ctx, contract.Assignee, resolver)
	if err != nil {
		t.Fatalf("ResolveTaskAssignee: %v", err)
	}
	if ref.ProfileRef.ID != assignment.AgentProfileID {
		t.Fatalf("resolved profile = %s, want assignment profile %s", ref.ProfileRef.ID, assignment.AgentProfileID)
	}
	if ref.InstanceRef.ID != instance.ID() {
		t.Fatalf("resolved instance = %s, want assembled instance %s", ref.InstanceRef.ID, instance.ID())
	}
}

func TestReleasedAssignmentCannotReceiveTaskContract(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-worker", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	assignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-worker", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}
	if err := service.Release(ctx, ReleaseCommand{IDempotencyKey: "release-worker", MissionID: "mission-001", AssignmentID: assignment.ID, ReleasedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}}); err != nil {
		t.Fatalf("release: %v", err)
	}
	resolver := NewTaskAssignmentResolver(env.UnitOfWork, NewPlatformTeamRepository())

	_, err = resolver.ResolveMissionAssignment(ctx, missiontask.MissionAssignmentRef{ID: missiontask.MissionAssignmentID(assignment.ID)})
	if err == nil {
		t.Fatalf("released assignment resolved as active")
	}
}

type staffedSessionRuntime struct {
	kind agentcore.RuntimeKind
}

func (r staffedSessionRuntime) RunSession(context.Context, agentcore.RuntimeRequest, agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	return agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusCompleted}, nil
}

func (staffedSessionRuntime) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (staffedSessionRuntime) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (staffedSessionRuntime) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}

func (staffedSessionRuntime) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }

func (r staffedSessionRuntime) Capabilities(context.Context) agentcore.Capabilities {
	return staffedSessionCapabilities(r.kind)
}

type staffedSessionRuntimeRegistry struct {
	runtimes map[agentcore.RuntimeKind]agentcore.SessionRuntime
}

func newStaffedSessionRuntimeRegistry() *staffedSessionRuntimeRegistry {
	return &staffedSessionRuntimeRegistry{runtimes: make(map[agentcore.RuntimeKind]agentcore.SessionRuntime)}
}

func (r *staffedSessionRuntimeRegistry) Register(kind agentcore.RuntimeKind, runtime agentcore.SessionRuntime) error {
	r.runtimes[kind] = runtime
	return nil
}

func (r *staffedSessionRuntimeRegistry) ResolveRuntime(_ context.Context, spec agentcore.RuntimeSpec) (agentcore.SessionRuntime, error) {
	return r.runtimes[spec.Kind], nil
}

func (r *staffedSessionRuntimeRegistry) Capabilities(ctx context.Context, kind agentcore.RuntimeKind) (agentcore.Capabilities, bool) {
	runtime := r.runtimes[kind]
	if runtime == nil {
		return agentcore.Capabilities{}, false
	}
	return runtime.Capabilities(ctx), true
}

func staffedSessionCapabilities(kind agentcore.RuntimeKind) agentcore.Capabilities {
	return agentcore.Capabilities{
		SupportsMultiTurn: true,
		Signals:           agentcore.SignalSet{TerminalResult: true},
		Transport:         agentcore.TransportEvidence{Provider: string(kind), Protocol: "canonical-test"},
	}
}

type staffedToolResolver struct{}

func (staffedToolResolver) Resolve(ctx context.Context, req agentassembly.ToolResolveRequest) (tool.ResolvedTool, error) {
	return tool.ResolvedTool{
		Ref: req.Ref,
		Definition: tool.ToolDefinition{
			ID:          req.Ref.ToolID,
			Name:        string(req.Ref.ToolID),
			Version:     "1.0.0",
			Description: "test tool",
			Enabled:     true,
		},
		GrantedPermissions: []string{"tool.use"},
	}, ctx.Err()
}

type staffedSkillResolver struct{}

func (staffedSkillResolver) Resolve(ctx context.Context, req agentassembly.SkillResolveRequest) (skill.ResolvedSkill, error) {
	return skill.ResolvedSkill{
		Ref:            req.Ref,
		MatchedVersion: "1.0.0",
		Definition: skill.SkillDefinition{
			ID:          req.Ref.SkillID,
			Name:        string(req.Ref.SkillID),
			Version:     "1.0.0",
			Description: "test skill",
			Enabled:     true,
		},
	}, ctx.Err()
}

type staffedMemoryBinder struct{}

func (staffedMemoryBinder) Bind(ctx context.Context, req agentassembly.MemoryBindRequest) (agentassembly.MemoryBinding, error) {
	return agentassembly.MemoryBinding{
		PersonalDigest: memcore.MemoryDigest{ID: agentcore.MemoryDigestID(req.PersonalProfileRef.MemoryProfileID), Summary: "mission memory"},
	}, ctx.Err()
}

func staffedProductionComponents() agentassembly.ProductionComponentRegistry {
	validator := agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil })
	return agentassembly.ProductionComponentRegistry{
		DeliveryCommitterValue: staffedCommitter{},
		EvidenceValidator:      validator,
		QAValidator:            validator,
		SummaryGeneratorValue:  agentcore.DefaultSummaryGenerator{},
		ReplyNotifier:          agentcore.NoopNotifier{},
		PolicyValidator:        validator,
		TraceRecorderValue:     staffedDurableTraceRecorder{},
		NormalizerValue:        staffedNormalizer{},
	}
}

type staffedDurableTraceRecorder struct{}

func (staffedDurableTraceRecorder) RecordTrace(context.Context, agentcore.AgentInput, agentcore.RuntimeResponse) (string, error) {
	return "", nil
}

func (staffedDurableTraceRecorder) BeginTrace(context.Context, agentcore.AgentInput) (agentcore.DurableTraceSession, error) {
	return staffedDurableTraceSession{}, nil
}

type staffedDurableTraceSession struct{}

func (staffedDurableTraceSession) Ref() string { return "test://trace" }
func (staffedDurableTraceSession) RecordLifecycle(context.Context, agentcore.TraceLifecycleEvent) error {
	return nil
}
func (staffedDurableTraceSession) RecordRequest(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (staffedDurableTraceSession) RecordRuntime(context.Context, agentcore.SessionEnvelope) error {
	return nil
}
func (staffedDurableTraceSession) Finish(context.Context, agentcore.TraceTerminal) error { return nil }

type staffedNormalizer struct{}

func (staffedNormalizer) Normalize(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
	return agentcore.AgentResult{
		ID:        agentcore.AgentResultID("result-" + input.RunID.String()),
		MissionID: missioncore.MissionID(input.Task.MissionID),
		TaskID:    missioncore.TaskID(input.Task.ID),
		AgentID:   input.ContextPack.AgentID,
		RunID:     input.RunID,
		Summary:   agentcore.Summary{Text: "staffed worker normalized"},
		CreatedAt: now,
	}, nil
}

type staffedCommitter struct{}

func (staffedCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}
