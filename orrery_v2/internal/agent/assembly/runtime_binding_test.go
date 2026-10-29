package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestAssemblerRuntimeBindingRejectsUnavailableRuntime(t *testing.T) {
	profile := validProfile()
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.RegisterUnavailable(agentcore.RuntimeKindCodex, "codex CLI missing"); err != nil {
		t.Fatalf("register unavailable: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})
	_, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err == nil {
		t.Fatalf("expected unavailable runtime error")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) || assemblyErr.Code != CodeRuntimeNotAvailable {
		t.Fatalf("expected runtime_not_available, got %T %v", err, err)
	}
}

func TestAssemblerBindsSystemManagedProfileWithoutRuntimeSpecToPiRuntime(t *testing.T) {
	profile := validProfile()
	profile.RuntimeSpec = agentcore.RuntimeSpec{}
	profile.ToolRefs = []agentcore.ToolRef{}
	profile.SkillRefs = []agentcore.SkillRef{}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindPi, fakeSessionRuntime{kind: agentcore.RuntimeKindPi}); err != nil {
		t.Fatalf("register pi: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapter := instance.Base().Runtime()
	if got := adapter.Capabilities(context.Background()).Transport.Provider; got != string(agentcore.RuntimeKindPi) {
		t.Fatalf("runtime kind = %s", got)
	}
}

func TestAssemblerBindsHumanProfileWithoutRuntimeSpecToPiRuntimeWhenBaseAgentManaged(t *testing.T) {
	profile := validProfile()
	profile.AgentKind = agentcore.AgentKindHuman
	profile.RuntimeSpec = agentcore.RuntimeSpec{}
	profile.ToolRefs = []agentcore.ToolRef{}
	profile.SkillRefs = []agentcore.SkillRef{}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindPi, fakeSessionRuntime{kind: agentcore.RuntimeKindPi}); err != nil {
		t.Fatalf("register pi: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{},
		RuntimeRegistry:   runtimes,
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapter := instance.Base().Runtime()
	if got := adapter.Capabilities(context.Background()).Transport.Provider; got != string(agentcore.RuntimeKindPi) {
		t.Fatalf("runtime kind = %s", got)
	}
}

func TestAssemblerHydratesRuntimeSpecFromProfileResolver(t *testing.T) {
	profile := validProfile()
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw, ConfigRef: "runtime-openclaw-local"}
	profile.ToolRefs = []agentcore.ToolRef{}
	profile.SkillRefs = []agentcore.SkillRef{}
	runtimes := newFakeSessionRuntimeRegistry()
	if err := runtimes.Register(agentcore.RuntimeKindOpenClaw, fakeSessionRuntime{kind: agentcore.RuntimeKindOpenClaw}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	assembler := NewAssembler(AssemblerOptions{
		ToolResolver:      fakeToolResolver{},
		SkillResolver:     fakeSkillResolver{},
		MemoryBinder:      fakeMemoryBinder{},
		RuntimeRegistry:   runtimes,
		RuntimeResolver:   fakeRuntimeSpecResolver{spec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw, ConfigRef: "runtime-openclaw-local", Config: json.RawMessage(`{"agent_id":"planner"}`)}},
		ComponentRegistry: fakeComponentRegistry{},
		Pipeline:          fakePipelineDependencies{},
	})

	instance, err := assembler.Build(context.Background(), profile, explicitBuildOptions())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := string(instance.Profile().RuntimeSpec.Config); got != `{"agent_id":"planner"}` {
		t.Fatalf("profile runtime config = %s", got)
	}
	if got := string(instance.Base().RuntimeSpec().Config); got != `{"agent_id":"planner"}` {
		t.Fatalf("base runtime config = %s", got)
	}
}

type fakeRuntimeSpecResolver struct {
	spec agentcore.RuntimeSpec
	err  error
}

func (r fakeRuntimeSpecResolver) ResolveRuntimeSpec(context.Context, agentcore.RuntimeSpec) (agentcore.RuntimeSpec, error) {
	return r.spec, r.err
}
