package assembly

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type AgentAssembler interface {
	Build(ctx context.Context, profile agentcore.AgentProfile, opts BuildOptions) (agentcore.AgentInstance, error)
}

type AssemblerOptions struct {
	ToolResolver      ToolResolver
	SkillResolver     SkillResolver
	MemoryBinder      MemoryBinder
	RuntimeRegistry   agentcore.SessionRuntimeRegistry
	RuntimeResolver   RuntimeSpecResolver
	ComponentRegistry ComponentRegistry
	Pipeline          PipelineDependencyProvider
	DiaryFlush        agentcore.DiaryFlushCoordinator
}

type BuildOptions struct {
	AgentID     agentcore.AgentID
	InstanceID  agentcore.AgentInstanceID
	Permissions []string
}

type Assembler struct {
	tools      ToolResolver
	skills     SkillResolver
	memory     MemoryBinder
	runtimes   agentcore.SessionRuntimeRegistry
	resolver   RuntimeSpecResolver
	components ComponentRegistry
	pipeline   PipelineDependencyProvider
	diaryFlush agentcore.DiaryFlushCoordinator
}

type RuntimeSpecResolver interface {
	ResolveRuntimeSpec(ctx context.Context, spec agentcore.RuntimeSpec) (agentcore.RuntimeSpec, error)
}

func NewAssembler(opts AssemblerOptions) *Assembler {
	return &Assembler{
		tools:      opts.ToolResolver,
		skills:     opts.SkillResolver,
		memory:     opts.MemoryBinder,
		runtimes:   opts.RuntimeRegistry,
		resolver:   opts.RuntimeResolver,
		components: opts.ComponentRegistry,
		pipeline:   opts.Pipeline,
		diaryFlush: opts.DiaryFlush,
	}
}

func (a *Assembler) Build(ctx context.Context, profile agentcore.AgentProfile, opts BuildOptions) (agentcore.AgentInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		return nil, newError(CodeProfileInvalid, profile.ID, "profile", "agent profile is invalid", fmt.Errorf("%v", errs))
	}
	var err error
	profile, err = a.resolveRuntimeSpec(ctx, profile)
	if err != nil {
		return nil, err
	}
	if opts.InstanceID.IsZero() {
		return nil, newError(CodeInstanceMissing, profile.ID, "instance_id", "agent instance id is required", nil)
	}
	identity := buildIdentity(profile, opts)
	resources, err := NewEffectiveResourceBinder(EffectiveResourceBinderOptions{
		ToolResolver:  a.tools,
		SkillResolver: a.skills,
	}).Bind(ctx, EffectiveResourceBindRequest{
		Profile:     profile,
		AgentID:     identity.AgentID,
		Permissions: opts.Permissions,
	})
	if err != nil {
		return nil, err
	}
	memory, err := a.bindMemory(ctx, profile, identity.AgentID)
	if err != nil {
		return nil, err
	}
	runtime, err := a.bindRuntime(ctx, profile)
	if err != nil {
		return nil, err
	}
	components, err := a.loadComponents(profile)
	if err != nil {
		return nil, err
	}
	pipeline, err := a.loadPipelineDependencies(profile)
	if err != nil {
		return nil, err
	}
	agent, err := agentcore.NewProductionBaseAgent(agentcore.BaseAgentConfig{
		Identity:   identity,
		Profile:    profile,
		Bindings:   agentcore.AgentBindings{Tools: resources.Tools, Skills: resources.Skills, Memory: memory},
		Runtime:    runtime,
		Components: components,
		Signals:    pipeline.Signals,
		Delivery:   pipeline.Delivery,
		Summary:    pipeline.Summary,
		Trace:      pipeline.Trace,
		Notifier:   pipeline.Notifier,
		Normalizer: pipeline.Normalizer,
		Validator:  pipeline.Validator,
		DiaryFlush: a.diaryFlush,
	})
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "pipeline", "production agent dependencies missing", err)
	}
	return agent, nil
}

func (a *Assembler) bindMemory(ctx context.Context, profile agentcore.AgentProfile, agentID agentcore.AgentID) (agentcore.MemoryBinding, error) {
	if a.memory == nil {
		return agentcore.MemoryBinding{}, newError(CodeMemoryBindingDenied, profile.ID, "memory", "memory binder is not configured", nil)
	}
	binding, err := a.memory.Bind(ctx, MemoryBindRequest{
		AgentID:               agentID,
		Subject:               memoryAccessSubject(profile, agentID),
		PersonalProfileRef:    profile.PersonalMemoryProfileRef,
		CollectiveProfileRefs: profile.CollectiveMemoryScopeRefs,
	})
	if err != nil {
		return agentcore.MemoryBinding{}, newError(CodeMemoryBindingDenied, profile.ID, "memory", "memory binding denied", err)
	}
	warnings := make([]string, 0, len(binding.Warnings))
	for _, warning := range binding.Warnings {
		warnings = append(warnings, warning.Code)
	}
	collective := make([]agentcore.MemoryDigestRef, 0, len(binding.CollectiveDigests))
	for _, digest := range binding.CollectiveDigests {
		collective = append(collective, agentcore.MemoryDigestRef{
			Evidence:    digest.Evidence,
			ID:          digest.ID,
			MemoryKind:  scopeKindToAgentMemoryKind(digest.Scope.Kind),
			SummaryRef:  digest.Summary,
			Sensitivity: string(digest.Sensitivity),
		})
	}
	return agentcore.MemoryBinding{
		PersonalDigest: agentcore.MemoryDigestRef{
			Evidence:    binding.PersonalDigest.Evidence,
			ID:          binding.PersonalDigest.ID,
			MemoryKind:  agentcore.MemoryKindPersonal,
			SummaryRef:  binding.PersonalDigest.Summary,
			Sensitivity: string(binding.PersonalDigest.Sensitivity),
		},
		CollectiveDigests: collective,
		Warnings:          warnings,
	}, nil
}

func memoryAccessSubject(profile agentcore.AgentProfile, agentID agentcore.AgentID) memcore.AccessSubject {
	subject := memcore.AccessSubject{AgentID: agentID}
	if organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)); organizationID != "" {
		subject.OrganizationIDs = []string{organizationID}
	}
	if departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID)); departmentID != "" {
		subject.DepartmentIDs = []string{departmentID}
	}
	return subject
}

func (a *Assembler) bindRuntime(ctx context.Context, profile agentcore.AgentProfile) (agentcore.SessionRuntime, error) {
	if a.runtimes == nil {
		return nil, newError(CodeRuntimeNotAvailable, profile.ID, "runtime_spec", "runtime registry is not configured", nil)
	}
	spec := profile.RuntimeSpec
	if !spec.HasKind() && isSystemManagedBaseAgentProfile(profile) {
		spec.Kind = agentcore.RuntimeKindPi
	}
	runtime, err := a.runtimes.ResolveRuntime(ctx, spec)
	if err != nil {
		return nil, newError(CodeRuntimeNotAvailable, profile.ID, "runtime_spec", "canonical runtime is not available", err)
	}
	return runtime, nil
}

func (a *Assembler) resolveRuntimeSpec(ctx context.Context, profile agentcore.AgentProfile) (agentcore.AgentProfile, error) {
	if a == nil || a.resolver == nil || !profile.RuntimeSpec.HasKind() {
		return profile, nil
	}
	spec, err := a.resolver.ResolveRuntimeSpec(ctx, profile.RuntimeSpec)
	if err != nil {
		return agentcore.AgentProfile{}, newError(CodeRuntimeNotAvailable, profile.ID, "runtime_spec", "runtime profile could not be resolved", err)
	}
	profile.RuntimeSpec = spec
	return profile, nil
}

func isSystemManagedBaseAgentProfile(profile agentcore.AgentProfile) bool {
	return profile.BaseAgent.InheritsBaseAgent &&
		profile.BaseAgent.AuditRequired &&
		strings.TrimSpace(profile.BaseAgent.LLMAPIKeyRef) != "" &&
		strings.TrimSpace(profile.BaseAgent.SystemPrompt) != ""
}

func (a *Assembler) loadComponents(profile agentcore.AgentProfile) (any, error) {
	registry, err := a.componentRegistryForProfile(profile)
	if err != nil {
		return nil, err
	}
	delivery, err := registry.Delivery(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "delivery", "delivery component missing", err)
	}
	if delivery == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "delivery", "delivery component missing", nil)
	}
	evidence, err := registry.Evidence(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "evidence", "evidence component missing", err)
	}
	if evidence == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "evidence", "evidence component missing", nil)
	}
	qa, err := registry.QA(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "qa", "qa component missing", err)
	}
	if qa == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "qa", "qa component missing", nil)
	}
	summary, err := registry.Summary(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "summary", "summary component missing", err)
	}
	if summary == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "summary", "summary component missing", nil)
	}
	notifier, err := registry.Notifier(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "notifier", "notifier component missing", err)
	}
	if notifier == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "notifier", "notifier component missing", nil)
	}
	policy, err := registry.Policy(profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "policy", "policy component missing", err)
	}
	if policy == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "policy", "policy component missing", nil)
	}
	return ComponentSet{Delivery: delivery, Evidence: evidence, QA: qa, Summary: summary, Notifier: notifier, Policy: policy}, nil
}

func (a *Assembler) componentRegistryForProfile(profile agentcore.AgentProfile) (ComponentRegistry, error) {
	if a.components != nil {
		return a.components, nil
	}
	if a.pipeline == nil {
		return nil, newError(CodeComponentMissing, profile.ID, "components", "component registry is not configured", nil)
	}
	registry, err := NewDefaultComponentRegistryFromPipeline(a.pipeline, profile)
	if err != nil {
		return nil, newError(CodeComponentMissing, profile.ID, "components", "component registry could not be derived from pipeline", err)
	}
	return registry, nil
}

func (a *Assembler) loadPipelineDependencies(profile agentcore.AgentProfile) (PipelineDependencies, error) {
	if a.pipeline == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "pipeline", "pipeline dependencies are not configured", nil)
	}
	var out PipelineDependencies
	var err error
	out.Delivery, err = a.pipeline.DeliveryCommitter(profile)
	if err != nil || out.Delivery == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "delivery", "result committer missing", err)
	}
	out.Signals, err = a.pipeline.SignalSink(profile)
	if err != nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "signals", "signal sink missing", err)
	}
	out.Summary, err = a.pipeline.SummaryGenerator(profile)
	if err != nil || out.Summary == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "summary", "summary generator missing", err)
	}
	out.Trace, err = a.pipeline.TraceRecorder(profile)
	if err != nil || out.Trace == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "trace", "trace recorder missing", err)
	}
	out.Notifier, err = a.pipeline.Notifier(profile)
	if err != nil || out.Notifier == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "notifier", "notifier missing", err)
	}
	out.Validator, err = a.pipeline.ResultValidator(profile)
	if err != nil || out.Validator == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "validator", "result validator missing", err)
	}
	out.Normalizer, err = a.pipeline.ResultNormalizer(profile)
	if err != nil || out.Normalizer == nil {
		return PipelineDependencies{}, newError(CodeComponentMissing, profile.ID, "normalizer", "result normalizer missing", err)
	}
	return out, nil
}

func buildIdentity(profile agentcore.AgentProfile, opts BuildOptions) agentcore.AgentIdentity {
	agentID := opts.AgentID
	if agentID == "" {
		agentID = agentcore.AgentID(profile.ID)
	}
	instanceID := opts.InstanceID
	return agentcore.AgentIdentity{
		AgentID:        agentID,
		AgentProfileID: profile.ID,
		InstanceRef:    agentcore.AgentInstanceRef{ID: instanceID},
	}
}
