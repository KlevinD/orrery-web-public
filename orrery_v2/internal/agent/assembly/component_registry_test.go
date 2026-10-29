package assembly

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestNewDefaultComponentRegistryRejectsMissingRequiredDeps(t *testing.T) {
	_, err := NewDefaultComponentRegistry(ComponentRegistryDeps{
		Delivery: fakeCommitter{},
	})
	if err == nil {
		t.Fatalf("expected missing dependencies to fail")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly Error, got %T: %v", err, err)
	}
	if assemblyErr.Code != CodeComponentMissing {
		t.Fatalf("error code = %q", assemblyErr.Code)
	}
	if assemblyErr.Field != "evidence" {
		t.Fatalf("error field = %q", assemblyErr.Field)
	}
}

func TestNewDefaultComponentRegistryProvidesRequiredComponents(t *testing.T) {
	registry, err := NewDefaultComponentRegistry(defaultComponentRegistryDeps())
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	profile := validProfile()
	if delivery, err := registry.Delivery(profile); err != nil || delivery == nil {
		t.Fatalf("delivery = %#v err=%v", delivery, err)
	}
	if evidence, err := registry.Evidence(profile); err != nil || evidence == nil {
		t.Fatalf("evidence = %#v err=%v", evidence, err)
	}
	if qa, err := registry.QA(profile); err != nil || qa == nil {
		t.Fatalf("qa = %#v err=%v", qa, err)
	}
	if summary, err := registry.Summary(profile); err != nil || summary == nil {
		t.Fatalf("summary = %#v err=%v", summary, err)
	}
	if notifier, err := registry.Notifier(profile); err != nil || notifier == nil {
		t.Fatalf("notifier = %#v err=%v", notifier, err)
	}
	if policy, err := registry.Policy(profile); err != nil || policy == nil {
		t.Fatalf("policy = %#v err=%v", policy, err)
	}
}

func TestNewDefaultComponentRegistryFromPipelineDerivesRequiredComponents(t *testing.T) {
	registry, err := NewDefaultComponentRegistryFromPipeline(fakePipelineDependencies{}, validProfile())
	if err != nil {
		t.Fatalf("derive default registry from pipeline: %v", err)
	}
	if delivery, err := registry.Delivery(validProfile()); err != nil || delivery == nil {
		t.Fatalf("delivery = %#v err=%v", delivery, err)
	}
	if qa, err := registry.QA(validProfile()); err != nil || qa == nil {
		t.Fatalf("qa = %#v err=%v", qa, err)
	}
}

func TestNewDefaultComponentRegistryFromPipelineUsesSplitValidators(t *testing.T) {
	pipeline := splitValidatorPipelineDependencies{
		evidence: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		qa:       agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		policy:   agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
	}
	registry, err := NewDefaultComponentRegistryFromPipeline(pipeline, validProfile())
	if err != nil {
		t.Fatalf("derive default registry from split pipeline: %v", err)
	}
	if _, ok := mustEvidenceComponent(t, registry).(validatorComponent); !ok {
		t.Fatalf("expected evidence validator component")
	}
	if _, ok := mustQAComponent(t, registry).(qaComponent); !ok {
		t.Fatalf("expected qa validator component")
	}
	if _, ok := mustPolicyComponent(t, registry).(policyComponent); !ok {
		t.Fatalf("expected policy validator component")
	}
}

func TestNewDefaultComponentRegistryFromPipelineRequiresPipeline(t *testing.T) {
	_, err := NewDefaultComponentRegistryFromPipeline(nil, validProfile())
	if err == nil {
		t.Fatalf("expected nil pipeline to fail")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly Error, got %T: %v", err, err)
	}
	if assemblyErr.Field != "pipeline" {
		t.Fatalf("error field = %q", assemblyErr.Field)
	}
}

func TestProductionComponentRegistryRequiresRealCallableComponents(t *testing.T) {
	registry := ProductionComponentRegistry{}
	if component, err := registry.Delivery(validProfile()); err != nil || component != nil {
		t.Fatalf("empty delivery = %#v err=%v", component, err)
	}

	registry = ProductionComponentRegistry{
		DeliveryCommitterValue: fakeCommitter{},
		EvidenceValidator:      agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		QAValidator:            agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		SummaryGeneratorValue:  agentcore.DefaultSummaryGenerator{},
		ReplyNotifier:          agentcore.NoopNotifier{},
		PolicyValidator:        agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
	}

	delivery, err := registry.Delivery(validProfile())
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if err := delivery.SubmitAgentResult(context.Background(), agentcore.AgentResult{}, agentcore.CommitOptions{}); err != nil {
		t.Fatalf("delivery component is not callable: %v", err)
	}
	summary, err := registry.Summary(validProfile())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if _, err := summary.GenerateSummary(agentcore.AgentInput{}, agentcore.RuntimeResponse{}, agentcore.AgentResult{Deliverables: []agentcore.Deliverable{{ID: "d-1"}}}); err != nil {
		t.Fatalf("summary component is not callable: %v", err)
	}
}

func TestNewProductionComponentRegistryRejectsIncompleteWiring(t *testing.T) {
	_, err := NewProductionComponentRegistry(ProductionComponentRegistry{
		DeliveryCommitterValue: fakeCommitter{},
	})
	if err == nil {
		t.Fatalf("expected incomplete production registry to fail")
	}
	var assemblyErr Error
	if !errors.As(err, &assemblyErr) {
		t.Fatalf("expected assembly Error, got %T: %v", err, err)
	}
	if assemblyErr.Field != "evidence" {
		t.Fatalf("error field = %q", assemblyErr.Field)
	}
}

func TestNewProductionComponentRegistryAcceptsCompleteWiring(t *testing.T) {
	registry, err := NewProductionComponentRegistry(ProductionComponentRegistry{
		DeliveryCommitterValue: fakeCommitter{},
		EvidenceValidator:      agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		QAValidator:            agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		SummaryGeneratorValue:  agentcore.DefaultSummaryGenerator{},
		ReplyNotifier:          agentcore.NoopNotifier{},
		PolicyValidator:        agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		TraceRecorderValue:     agentcore.NoopTraceRecorder{},
		NormalizerValue: agentcore.ResultNormalizerFunc(func(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
			return agentcore.AgentResult{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("new production registry: %v", err)
	}
	if delivery, err := registry.Delivery(validProfile()); err != nil || delivery == nil {
		t.Fatalf("delivery = %#v err=%v", delivery, err)
	}
}

func TestComponentPipelineProviderBridgesRegistryToBaseAgentDependencies(t *testing.T) {
	registry := ProductionComponentRegistry{
		DeliveryCommitterValue: fakeCommitter{},
		EvidenceValidator:      agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		QAValidator:            agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		SummaryGeneratorValue:  agentcore.DefaultSummaryGenerator{},
		ReplyNotifier:          agentcore.NoopNotifier{},
		PolicyValidator:        agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
	}
	provider := ComponentPipelineProvider{Registry: registry, Trace: agentcore.NoopTraceRecorder{}}

	if delivery, err := provider.DeliveryCommitter(validProfile()); err != nil || delivery == nil {
		t.Fatalf("delivery = %#v err=%v", delivery, err)
	}
	if summary, err := provider.SummaryGenerator(validProfile()); err != nil || summary == nil {
		t.Fatalf("summary = %#v err=%v", summary, err)
	}
	if trace, err := provider.TraceRecorder(validProfile()); err != nil || trace == nil {
		t.Fatalf("trace = %#v err=%v", trace, err)
	}
	if notifier, err := provider.Notifier(validProfile()); err != nil || notifier == nil {
		t.Fatalf("notifier = %#v err=%v", notifier, err)
	}
	if validator, err := provider.ResultValidator(validProfile()); err != nil || validator == nil {
		t.Fatalf("validator = %#v err=%v", validator, err)
	}
}

func TestComponentPipelineProviderRunsEvidenceQAPolicyValidators(t *testing.T) {
	var calls []string
	registry := ProductionComponentRegistry{
		EvidenceValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error {
			calls = append(calls, "evidence")
			return nil
		}),
		QAValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error {
			calls = append(calls, "qa")
			return nil
		}),
		PolicyValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error {
			calls = append(calls, "policy")
			return nil
		}),
	}
	validator, err := ComponentPipelineProvider{Registry: registry}.ResultValidator(validProfile())
	if err != nil {
		t.Fatalf("ResultValidator returned error: %v", err)
	}
	if err := validator.Validate(agentcore.AgentInput{}, agentcore.AgentResult{}); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if got := len(calls); got != 3 {
		t.Fatalf("validator calls = %v", calls)
	}
	if calls[0] != "evidence" || calls[1] != "qa" || calls[2] != "policy" {
		t.Fatalf("validator order = %v", calls)
	}
}

func TestComponentPipelineProviderStopsValidatorChainOnError(t *testing.T) {
	expected := errors.New("evidence failed")
	registry := ProductionComponentRegistry{
		EvidenceValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return expected }),
		QAValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error {
			t.Fatal("qa should not run after evidence error")
			return nil
		}),
		PolicyValidator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error {
			t.Fatal("policy should not run after evidence error")
			return nil
		}),
	}
	validator, err := ComponentPipelineProvider{Registry: registry}.ResultValidator(validProfile())
	if err != nil {
		t.Fatalf("ResultValidator returned error: %v", err)
	}
	if err := validator.Validate(agentcore.AgentInput{}, agentcore.AgentResult{}); !errors.Is(err, expected) {
		t.Fatalf("Validate error = %v, want %v", err, expected)
	}
}

func defaultComponentRegistryDeps() ComponentRegistryDeps {
	validator := agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil })
	return ComponentRegistryDeps{
		Delivery: fakeCommitter{},
		Evidence: validator,
		QA:       validator,
		Summary:  agentcore.DefaultSummaryGenerator{},
		Notifier: agentcore.NoopNotifier{},
		Policy:   validator,
	}
}

type splitValidatorPipelineDependencies struct {
	fakePipelineDependencies
	evidence agentcore.ResultValidator
	qa       agentcore.ResultValidator
	policy   agentcore.ResultValidator
}

func (p splitValidatorPipelineDependencies) EvidenceValidatorForProfile(agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	return p.evidence, nil
}

func (p splitValidatorPipelineDependencies) QAValidatorForProfile(agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	return p.qa, nil
}

func (p splitValidatorPipelineDependencies) PolicyValidatorForProfile(agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	return p.policy, nil
}

func mustEvidenceComponent(t *testing.T, registry DefaultComponentRegistry) EvidenceComponent {
	t.Helper()
	component, err := registry.Evidence(validProfile())
	if err != nil || component == nil {
		t.Fatalf("evidence = %#v err=%v", component, err)
	}
	return component
}

func mustQAComponent(t *testing.T, registry DefaultComponentRegistry) QAComponent {
	t.Helper()
	component, err := registry.QA(validProfile())
	if err != nil || component == nil {
		t.Fatalf("qa = %#v err=%v", component, err)
	}
	return component
}

func mustPolicyComponent(t *testing.T, registry DefaultComponentRegistry) PolicyComponent {
	t.Helper()
	component, err := registry.Policy(validProfile())
	if err != nil || component == nil {
		t.Fatalf("policy = %#v err=%v", component, err)
	}
	return component
}
