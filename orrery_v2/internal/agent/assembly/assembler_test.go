package assembly

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/resource/skill"
	"orrery_v2/internal/resource/tool"
)

func TestAssemblerBuildsAgentInstanceWithResolvedBindings(t *testing.T) {
	profile := validProfile()
	tools := fakeToolResolver{resolved: []tool.ResolvedTool{{
		Ref: profile.ToolRefs[0],
		Definition: tool.ToolDefinition{
			ID:          profile.ToolRefs[0].ToolID,
			Name:        "shell",
			Description: "Run commands",
			Version:     "1.0.0",
			Enabled:     true,
		},
		GrantedPermissions: []string{"shell.exec"},
	}}}
	skills := fakeSkillResolver{resolved: []skill.ResolvedSkill{{
		Ref:            profile.SkillRefs[0],
		MatchedVersion: "2.0.0",
		Definition: skill.SkillDefinition{
			ID:          profile.SkillRefs[0].SkillID,
			Name:        "tdd",
			Version:     "2.0.0",
			Description: "Test first",
			Enabled:     true,
		},
	}}}
	memory := fakeMemoryBinder{binding: MemoryBinding{
		PersonalDigest: memcore.MemoryDigest{ID: "mem-personal", Summary: "prefers evidence"},
	}}
	runtimes := newFakeSessionRuntimeRegistry()
	adapter := fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}
	if err := runtimes.Register(agentcore.RuntimeKindCodex, adapter); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      tools,
		SkillResolver:     skills,
		MemoryBinder:      memory,
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	if instance.ID().IsZero() {
		t.Fatalf("expected stable agent instance id")
	}
	if len(instance.Bindings().Tools) != 1 {
		t.Fatalf("tool bindings = %d", len(instance.Bindings().Tools))
	}
	if len(instance.Bindings().Skills) != 1 {
		t.Fatalf("skill bindings = %d", len(instance.Bindings().Skills))
	}
	if instance.Bindings().Memory.PersonalDigest.ID != "mem-personal" {
		t.Fatalf("memory binding was not preserved")
	}
	base, ok := instance.(*agentcore.BaseAgent)
	if !ok {
		t.Fatalf("expected BaseAgent instance, got %T", instance)
	}
	runtimeAdapter := base.Runtime()
	if runtimeAdapter.Capabilities(context.Background()).Transport.Provider != string(agentcore.RuntimeKindCodex) {
		t.Fatalf("canonical runtime was not resolved")
	}
}

func TestAssemblerCarriesDiaryFlushCoordinatorIntoBaseAgent(t *testing.T) {
	flush := noopDiaryFlushCoordinator{}
	assembler := NewAssembler(AssemblerOptions{DiaryFlush: flush})
	if assembler.diaryFlush == nil {
		t.Fatalf("diary flush coordinator was dropped")
	}
}

type noopDiaryFlushCoordinator struct{}

func (noopDiaryFlushCoordinator) PrepareRuntimeRequest(_ context.Context, req agentcore.RuntimeRequest) agentcore.RuntimeRequest {
	return req
}

func (noopDiaryFlushCoordinator) FlushTerminal(context.Context, agentcore.DiaryFlushRequest) agentcore.DiaryFlushOutcome {
	return agentcore.DiaryFlushOutcome{Status: agentcore.DiaryFlushSkipped}
}

func TestAssemblerMemoryBindingErrorIncludesProfileAndCause(t *testing.T) {
	cause := errors.New("memory not found: memory-profile-coder")
	err := newError(CodeMemoryBindingDenied, "profile-coder", "memory", "memory binding denied", cause)

	got := err.Error()
	for _, want := range []string{"memory_binding_denied", "profile-coder", "memory", "memory not found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("assembly error %q missing %q", got, want)
		}
	}
}

func TestAssemblerMemoryBindingUsesTrustedProfileIdentitySubject(t *testing.T) {
	profile := validProfile()
	profile.PersonalMemoryProfileRef.AccessScope = "mission:secret-mission"
	profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{{
		MemoryProfileID: "memory-secret-mission",
		MemoryKind:      agentcore.MemoryKindMission,
		AccessScope:     "mission:secret-mission",
	}}
	binder := &capturingMemoryBinder{}
	assembler := &Assembler{memory: binder}

	if _, err := assembler.bindMemory(context.Background(), profile, "agent-runner"); err != nil {
		t.Fatalf("bind memory: %v", err)
	}

	subject := binder.request.Subject
	if subject.AgentID != "agent-runner" || len(subject.OrganizationIDs) != 1 || subject.OrganizationIDs[0] != "org-1" || len(subject.DepartmentIDs) != 1 || subject.DepartmentIDs[0] != "dept-1" {
		t.Fatalf("trusted memory subject = %+v", subject)
	}
	if len(subject.ProjectIDs) != 0 || len(subject.MissionIDs) != 0 || len(subject.DiscussionIDs) != 0 {
		t.Fatalf("untrusted memory scopes leaked into subject: %+v", subject)
	}
}

func TestAssemblerRuntimeRequestCarriesProfileBindingsForExplicitRuntime(t *testing.T) {
	profile := validProfile()
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}
	tools := fakeToolResolver{resolved: []tool.ResolvedTool{{
		Ref: profile.ToolRefs[0],
		Definition: tool.ToolDefinition{
			ID:          profile.ToolRefs[0].ToolID,
			Name:        "shell",
			Description: "Run commands",
			Version:     "1.0.0",
			Enabled:     true,
		},
		GrantedPermissions: []string{"shell.exec"},
	}}}
	skills := fakeSkillResolver{resolved: []skill.ResolvedSkill{{
		Ref:            profile.SkillRefs[0],
		MatchedVersion: "2.0.0",
		Definition: skill.SkillDefinition{
			ID:              profile.SkillRefs[0].SkillID,
			Name:            "tdd",
			Version:         "2.0.0",
			Description:     "Test first",
			Enabled:         true,
			PackageSnapshot: &skill.PackageSnapshot{Digest: strings.Repeat("a", 64)},
		},
	}}}
	runtimes := newFakeSessionRuntimeRegistry()
	adapter := &capturingSessionRuntime{kind: agentcore.RuntimeKindCodex}
	if err := runtimes.Register(agentcore.RuntimeKindCodex, adapter); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      tools,
		SkillResolver:     skills,
		MemoryBinder:      fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})
	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	input := validAssemblyAgentInput()
	input.RuntimeOptions.WorkingDirectory = t.TempDir()
	_, err = instance.Base().Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("handle agent input: %v", err)
	}

	if !runtimeRequestHasToolBinding(adapter.request.ToolBindings, "tool-shell") ||
		!runtimeRequestHasToolBinding(adapter.request.ToolBindings, "load_skill") {
		t.Fatalf("runtime request tool bindings = %+v", adapter.request.ToolBindings)
	}
	if len(adapter.request.SkillBindings) != 1 || adapter.request.SkillBindings[0].SkillID != "skill-tdd" {
		t.Fatalf("runtime request skill bindings = %+v", adapter.request.SkillBindings)
	}
	if adapter.request.SkillBindings[0].PackageDigest != strings.Repeat("a", 64) {
		t.Fatalf("runtime lost resolved package digest: %+v", adapter.request.SkillBindings)
	}
}

func TestAssemblerInjectsOrganizationWorldDigestAsCollectiveContext(t *testing.T) {
	profile := validProfile()
	profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{{
		MemoryProfileID: "memory-org",
		MemoryKind:      agentcore.MemoryKindOrganization,
	}}
	adapter := &capturingSessionRuntime{kind: agentcore.RuntimeKindCodex}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, adapter); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:  fakeToolResolver{},
		SkillResolver: fakeSkillResolver{},
		MemoryBinder: fakeMemoryBinder{binding: MemoryBinding{
			PersonalDigest: memcore.MemoryDigest{ID: "digest-personal", Summary: "Personal preferences"},
			CollectiveDigests: []memcore.MemoryDigest{{
				ID:      "digest-world",
				Scope:   memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
				Summary: "Organization World:\nAPI changes require OpenAPI contract tests.",
				Layers: []memcore.LayerDigest{{
					Layer:   memcore.MemoryLayerWorld,
					Topic:   "quality",
					Summary: "API changes require OpenAPI contract tests.",
				}},
			}},
		}},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})
	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	input := validAssemblyAgentInput()
	input.RuntimeOptions.WorkingDirectory = t.TempDir()
	_, err = instance.Base().Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("handle agent input: %v", err)
	}

	pack := adapter.request.ContextPack
	if len(pack.CollectiveMemoryDigestRefs) != 1 || pack.CollectiveMemoryDigestRefs[0].ID != "digest-world" || pack.CollectiveMemoryDigestRefs[0].MemoryKind != agentcore.MemoryKindOrganization {
		t.Fatalf("collective memory refs = %+v", pack.CollectiveMemoryDigestRefs)
	}
	if len(pack.CollectiveMemoryDigestSummaries) != 1 || pack.CollectiveMemoryDigestSummaries[0].Summary != "Organization World:\nAPI changes require OpenAPI contract tests." {
		t.Fatalf("collective memory summaries = %+v", pack.CollectiveMemoryDigestSummaries)
	}
	if pack.RawMemory != "" {
		t.Fatalf("raw memory leaked into context: %q", pack.RawMemory)
	}
}

func TestAssemblerUsesToolRefPermissionScopeForBinding(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "mission_brief_create", PermissionScope: "mission.write"}}
	tools := &capturingToolResolver{resolved: tool.ResolvedTool{
		Ref: profile.ToolRefs[0],
		Definition: tool.ToolDefinition{
			ID:          profile.ToolRefs[0].ToolID,
			Name:        "Mission brief create",
			Description: "Create a Mission brief.",
			Version:     "1.0.0",
			Enabled:     true,
		},
		GrantedPermissions: []string{"mission.write"},
	}}
	skills := fakeSkillResolver{resolved: []skill.ResolvedSkill{{
		Ref:            profile.SkillRefs[0],
		MatchedVersion: "1.0.0",
		Definition:     skill.SkillDefinition{ID: profile.SkillRefs[0].SkillID, Enabled: true},
	}}}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      tools,
		SkillResolver:     skills,
		MemoryBinder:      fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	if len(tools.requests) != 1 {
		t.Fatalf("tool resolver requests = %+v", tools.requests)
	}
	if len(tools.requests[0].Principal.Permissions) != 1 || tools.requests[0].Principal.Permissions[0] != "mission.write" {
		t.Fatalf("tool resolver principal permissions = %+v", tools.requests[0].Principal.Permissions)
	}
	if len(instance.Bindings().Tools) != 1 || instance.Bindings().Tools[0].ToolID != "mission_brief_create" {
		t.Fatalf("tool bindings = %+v", instance.Bindings().Tools)
	}
}

func TestAssemblerBuildsWithDefaultComponentRegistryDerivedFromPipeline(t *testing.T) {
	profile := validProfile()
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:    fakeToolResolver{resolved: []tool.ResolvedTool{{Ref: profile.ToolRefs[0], Definition: tool.ToolDefinition{ID: profile.ToolRefs[0].ToolID, Enabled: true}}}},
		SkillResolver:   fakeSkillResolver{resolved: []skill.ResolvedSkill{{Ref: profile.SkillRefs[0], MatchedVersion: "1.0.0", Definition: skill.SkillDefinition{ID: profile.SkillRefs[0].SkillID, Enabled: true}}}},
		MemoryBinder:    fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry: runtimes,
		Pipeline:        fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("build agent with pipeline-derived registry: %v", err)
	}
	if instance == nil {
		t.Fatalf("expected agent instance")
	}
}

func TestAssemblerRequiresComponentsWhenRegistryAndPipelineAreMissing(t *testing.T) {
	profile := validProfile()
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:    fakeToolResolver{resolved: []tool.ResolvedTool{{Ref: profile.ToolRefs[0], Definition: tool.ToolDefinition{ID: profile.ToolRefs[0].ToolID, Enabled: true}}}},
		SkillResolver:   fakeSkillResolver{resolved: []skill.ResolvedSkill{{Ref: profile.SkillRefs[0], MatchedVersion: "1.0.0", Definition: skill.SkillDefinition{ID: profile.SkillRefs[0].SkillID, Enabled: true}}}},
		MemoryBinder:    fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry: runtimes,
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err == nil {
		t.Fatalf("expected missing component registry and pipeline to fail")
	}
	if instance != nil {
		t.Fatalf("expected no partial agent")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly error, got %T: %v", err, err)
	}
	if assemblyErr.Code != CodeComponentMissing {
		t.Fatalf("error code = %q", assemblyErr.Code)
	}
	if assemblyErr.Field != "components" {
		t.Fatalf("error field = %q", assemblyErr.Field)
	}
}

func TestAssemblerPrefersExplicitComponentRegistryOverPipelineDefault(t *testing.T) {
	profile := validProfile()
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	expected := errors.New("explicit registry failed")
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{resolved: []tool.ResolvedTool{{Ref: profile.ToolRefs[0], Definition: tool.ToolDefinition{ID: profile.ToolRefs[0].ToolID, Enabled: true}}}},
		SkillResolver:     fakeSkillResolver{resolved: []skill.ResolvedSkill{{Ref: profile.SkillRefs[0], MatchedVersion: "1.0.0", Definition: skill.SkillDefinition{ID: profile.SkillRefs[0].SkillID, Enabled: true}}}},
		MemoryBinder:      fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: unavailableComponentRegistry{err: expected},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err == nil {
		t.Fatalf("expected explicit registry error")
	}
	if instance != nil {
		t.Fatalf("expected no partial agent")
	}
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want explicit registry error", err)
	}
}

func TestAssemblerBindingFailureIsStructuredAndReturnsNoPartialAgent(t *testing.T) {
	profile := validProfile()
	expected := errors.New("permission denied")
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{err: expected},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{},
		RuntimeRegistry:   newFakeSessionRuntimeRegistry(),
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err == nil {
		t.Fatalf("expected build error")
	}
	if instance != nil {
		t.Fatalf("expected no partial agent")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly Error, got %T: %v", err, err)
	}
	if assemblyErr.Code != CodeToolBindingDenied {
		t.Fatalf("error code = %q", assemblyErr.Code)
	}
}

func TestAssemblerRequiresExplicitInstanceID(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{}
	profile.SkillRefs = []agentcore.SkillRef{}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindCodex, fakeSessionRuntime{kind: agentcore.RuntimeKindCodex}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{binding: MemoryBinding{PersonalDigest: memcore.MemoryDigest{ID: "mem-personal"}}},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, BuildOptions{})
	if err == nil {
		t.Fatalf("expected missing instance id to fail")
	}
	if instance != nil {
		t.Fatalf("expected no partial agent")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly Error, got %T: %v", err, err)
	}
	if assemblyErr.Code != CodeInstanceMissing {
		t.Fatalf("error code = %q", assemblyErr.Code)
	}
	if assemblyErr.Field != "instance_id" {
		t.Fatalf("error field = %q", assemblyErr.Field)
	}
}

type fakeComponentRegistry struct{}

func (fakeComponentRegistry) Delivery(agentcore.AgentProfile) (DeliveryComponent, error) {
	return deliveryComponent{ResultCommitter: fakeCommitter{}}, nil
}

func (fakeComponentRegistry) Evidence(agentcore.AgentProfile) (EvidenceComponent, error) {
	return validatorComponent{ResultValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil })}, nil
}

func (fakeComponentRegistry) QA(agentcore.AgentProfile) (QAComponent, error) {
	return qaComponent{ResultValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil })}, nil
}

func (fakeComponentRegistry) Summary(agentcore.AgentProfile) (SummaryComponent, error) {
	return summaryComponent{SummaryGenerator: agentcore.DefaultSummaryGenerator{}}, nil
}

func (fakeComponentRegistry) Notifier(agentcore.AgentProfile) (NotifierComponent, error) {
	return notifierComponent{Notifier: agentcore.NoopNotifier{}}, nil
}

func (fakeComponentRegistry) Policy(agentcore.AgentProfile) (PolicyComponent, error) {
	return policyComponent{ResultValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil })}, nil
}

type fakePipelineDependencies struct{}

func (fakePipelineDependencies) DeliveryCommitter(agentcore.AgentProfile) (agentcore.ResultCommitter, error) {
	return fakeCommitter{}, nil
}

func (fakePipelineDependencies) SignalSink(agentcore.AgentProfile) (agentcore.SignalSink, error) {
	return nil, nil
}

func (fakePipelineDependencies) SummaryGenerator(agentcore.AgentProfile) (agentcore.SummaryGenerator, error) {
	return agentcore.DefaultSummaryGenerator{}, nil
}

func (fakePipelineDependencies) TraceRecorder(agentcore.AgentProfile) (agentcore.TraceRecorder, error) {
	return testDurableTraceRecorder{}, nil
}

type testDurableTraceRecorder struct{}

func (testDurableTraceRecorder) RecordTrace(context.Context, agentcore.AgentInput, agentcore.RuntimeResponse) (string, error) {
	return "", nil
}

func (testDurableTraceRecorder) BeginTrace(context.Context, agentcore.AgentInput) (agentcore.DurableTraceSession, error) {
	return testDurableTraceSession{}, nil
}

type testDurableTraceSession struct{}

func (testDurableTraceSession) Ref() string { return "test://trace" }
func (testDurableTraceSession) RecordLifecycle(context.Context, agentcore.TraceLifecycleEvent) error {
	return nil
}
func (testDurableTraceSession) RecordRequest(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (testDurableTraceSession) RecordRuntime(context.Context, agentcore.SessionEnvelope) error {
	return nil
}
func (testDurableTraceSession) Finish(context.Context, agentcore.TraceTerminal) error { return nil }

func (fakePipelineDependencies) Notifier(agentcore.AgentProfile) (agentcore.Notifier, error) {
	return agentcore.NoopNotifier{}, nil
}

func (fakePipelineDependencies) ResultValidator(agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	return agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }), nil
}

func (fakePipelineDependencies) ResultNormalizer(agentcore.AgentProfile) (agentcore.ResultNormalizer, error) {
	return agentcore.ResultNormalizerFunc(func(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
		return agentcore.AgentResult{
			ID:        agentcore.AgentResultID("result-" + input.RunID.String()),
			MissionID: missioncore.MissionID(input.Task.MissionID),
			TaskID:    missioncore.TaskID(input.Task.ID),
			AgentID:   input.ContextPack.AgentID,
			RunID:     input.RunID,
			Summary:   agentcore.Summary{Text: "normalized"},
		}, nil
	}), nil
}

type fakeCommitter struct{}

func (fakeCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}

type unavailableComponentRegistry struct {
	err error
}

func (r unavailableComponentRegistry) Delivery(agentcore.AgentProfile) (DeliveryComponent, error) {
	return nil, r.err
}

func (r unavailableComponentRegistry) Evidence(agentcore.AgentProfile) (EvidenceComponent, error) {
	return nil, r.err
}

func (r unavailableComponentRegistry) QA(agentcore.AgentProfile) (QAComponent, error) {
	return nil, r.err
}

func (r unavailableComponentRegistry) Summary(agentcore.AgentProfile) (SummaryComponent, error) {
	return nil, r.err
}

func (r unavailableComponentRegistry) Notifier(agentcore.AgentProfile) (NotifierComponent, error) {
	return nil, r.err
}

func (r unavailableComponentRegistry) Policy(agentcore.AgentProfile) (PolicyComponent, error) {
	return nil, r.err
}

type fakeToolResolver struct {
	resolved []tool.ResolvedTool
	err      error
}

type capturingToolResolver struct {
	resolved tool.ResolvedTool
	err      error
	requests []ToolResolveRequest
}

func (r fakeToolResolver) Resolve(ctx context.Context, req ToolResolveRequest) (tool.ResolvedTool, error) {
	if r.err != nil {
		return tool.ResolvedTool{}, r.err
	}
	if len(r.resolved) == 0 {
		return tool.ResolvedTool{}, nil
	}
	return r.resolved[0], nil
}

func (r *capturingToolResolver) Resolve(ctx context.Context, req ToolResolveRequest) (tool.ResolvedTool, error) {
	r.requests = append(r.requests, req)
	if r.err != nil {
		return tool.ResolvedTool{}, r.err
	}
	return r.resolved, nil
}

type fakeSkillResolver struct {
	resolved []skill.ResolvedSkill
	err      error
}

func (r fakeSkillResolver) Resolve(ctx context.Context, req SkillResolveRequest) (skill.ResolvedSkill, error) {
	if r.err != nil {
		return skill.ResolvedSkill{}, r.err
	}
	if len(r.resolved) == 0 {
		return skill.ResolvedSkill{}, nil
	}
	return r.resolved[0], nil
}

type fakeMemoryBinder struct {
	binding MemoryBinding
	err     error
}

func (b fakeMemoryBinder) Bind(ctx context.Context, req MemoryBindRequest) (MemoryBinding, error) {
	return b.binding, b.err
}

type capturingMemoryBinder struct {
	request MemoryBindRequest
	binding MemoryBinding
	err     error
}

func (b *capturingMemoryBinder) Bind(_ context.Context, req MemoryBindRequest) (MemoryBinding, error) {
	b.request = req
	return b.binding, b.err
}

type fakeSessionRuntime struct {
	kind agentcore.RuntimeKind
}

func (a fakeSessionRuntime) RunSession(ctx context.Context, request agentcore.RuntimeRequest, observer agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	observeAssemblyRuntimeStart(ctx, observer)
	return agentcore.RuntimeResponse{RunID: request.RunID, Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "ok", Status: agentcore.RuntimeMessageStatusFinal}}}, nil
}

func (a fakeSessionRuntime) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (a fakeSessionRuntime) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (a fakeSessionRuntime) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (a fakeSessionRuntime) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (a fakeSessionRuntime) Capabilities(context.Context) agentcore.Capabilities {
	return assemblySessionCapabilities(a.kind)
}

type capturingSessionRuntime struct {
	kind    agentcore.RuntimeKind
	request agentcore.RuntimeRequest
}

func (a *capturingSessionRuntime) RunSession(ctx context.Context, req agentcore.RuntimeRequest, observer agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	a.request = req
	observeAssemblyRuntimeStart(ctx, observer)
	return agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "ok", Status: agentcore.RuntimeMessageStatusFinal}}}, nil
}

func (a *capturingSessionRuntime) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (a *capturingSessionRuntime) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (a *capturingSessionRuntime) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (a *capturingSessionRuntime) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (a *capturingSessionRuntime) Capabilities(context.Context) agentcore.Capabilities {
	return assemblySessionCapabilities(a.kind)
}

type fakeSessionRuntimeRegistry struct {
	runtimes    map[agentcore.RuntimeKind]agentcore.SessionRuntime
	unavailable map[agentcore.RuntimeKind]string
}

func newFakeSessionRuntimeRegistry() *fakeSessionRuntimeRegistry {
	return &fakeSessionRuntimeRegistry{runtimes: map[agentcore.RuntimeKind]agentcore.SessionRuntime{}, unavailable: map[agentcore.RuntimeKind]string{}}
}

func (r *fakeSessionRuntimeRegistry) Register(kind agentcore.RuntimeKind, runtime agentcore.SessionRuntime) error {
	if !kind.IsValid() || runtime == nil {
		return errors.New("invalid canonical runtime registration")
	}
	r.runtimes[kind] = runtime
	delete(r.unavailable, kind)
	return nil
}

func (r *fakeSessionRuntimeRegistry) RegisterUnavailable(kind agentcore.RuntimeKind, reason string) error {
	if !kind.IsValid() {
		return errors.New("invalid unavailable runtime registration")
	}
	delete(r.runtimes, kind)
	r.unavailable[kind] = reason
	return nil
}

func (r *fakeSessionRuntimeRegistry) ResolveRuntime(_ context.Context, spec agentcore.RuntimeSpec) (agentcore.SessionRuntime, error) {
	if reason := r.unavailable[spec.Kind]; reason != "" {
		return nil, errors.New(reason)
	}
	runtime := r.runtimes[spec.Kind]
	if runtime == nil {
		return nil, errors.New("canonical runtime unavailable")
	}
	return runtime, nil
}

func (r *fakeSessionRuntimeRegistry) Capabilities(ctx context.Context, kind agentcore.RuntimeKind) (agentcore.Capabilities, bool) {
	runtime := r.runtimes[kind]
	if runtime == nil {
		return agentcore.Capabilities{}, false
	}
	return runtime.Capabilities(ctx), true
}

func assemblySessionCapabilities(kind agentcore.RuntimeKind) agentcore.Capabilities {
	return agentcore.Capabilities{
		SupportsMultiTurn: true,
		Signals:           agentcore.SignalSet{TerminalResult: true},
		Transport:         agentcore.TransportEvidence{Provider: string(kind), Protocol: "canonical-test"},
	}
}

func observeAssemblyRuntimeStart(ctx context.Context, observer agentcore.SessionEventObserver) {
	if observer != nil {
		_ = observer(ctx, agentcore.SessionEnvelope{TurnGeneration: 1, Event: agentcore.SessionEvent{Kind: agentcore.EventTurnStarted, TurnStarted: &agentcore.TurnStartedEvent{}}})
	}
}

func runtimeRequestHasToolBinding(bindings []agentcore.RuntimeToolBinding, id agentcore.ToolID) bool {
	for _, binding := range bindings {
		if binding.ToolID == id {
			return true
		}
	}
	return false
}

func validProfile() agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:          "profile-1",
		RealName:    "林构建",
		DisplayName: "林构建",
		RoleName:    "Worker",
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "dept-1"},
			PositionRef:       agentcore.PositionRef{ID: "pos-1"},
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona: agentcore.AgentPersona{ResponsibilityBoundary: "负责交付任务结果、整理证据并接受 BaseAgent 审计。"},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "你是测试用托管 Agent，必须经 BaseAgent 审计壳执行。",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: "mem-profile",
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		SkillRefs: []agentcore.SkillRef{{SkillID: "skill-tdd"}},
		ToolRefs:  []agentcore.ToolRef{{ToolID: "tool-shell"}},
		RuntimeSpec: agentcore.RuntimeSpec{
			Kind: agentcore.RuntimeKindCodex,
		},
		Enabled: true,
	}
}

func explicitBuildOptions() BuildOptions {
	return BuildOptions{InstanceID: "profile-1:runtime"}
}

func validAssemblyAgentInput() agentcore.AgentInput {
	return agentcore.AgentInput{
		RunID: "run-assembly",
		ContextPack: agentcore.ContextPack{
			ID:                  "ctx-assembly",
			AgentID:             "profile-1",
			MissionID:           "mission-assembly",
			TaskID:              "task-assembly",
			MissionFinalGoal:    "finish assembly test",
			DiscussionGroupGoal: "finish assembly test",
			WorkerPersonalGoal:  "finish assembly test",
			TaskContractRef:     agentcore.TaskContractRef{ID: "task-assembly"},
			SourceRefs: []agentcore.ContextSourceRef{{
				Kind:      agentcore.ContextSourceKindTaskContract,
				Ref:       agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: "task-assembly"}},
				Priority:  agentcore.SourcePriorityContract,
				Immutable: true,
			}},
			OverridePolicy: agentcore.OverridePolicyPreserveProtected,
		},
		Task: agentcore.TaskInput{
			ID:        "task-assembly",
			MissionID: "mission-assembly",
			Goal:      "finish assembly test",
		},
	}
}
