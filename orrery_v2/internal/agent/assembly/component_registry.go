package assembly

import agentcore "orrery_v2/internal/agent/core"

type ComponentRegistry interface {
	Delivery(profile agentcore.AgentProfile) (DeliveryComponent, error)
	Evidence(profile agentcore.AgentProfile) (EvidenceComponent, error)
	QA(profile agentcore.AgentProfile) (QAComponent, error)
	Summary(profile agentcore.AgentProfile) (SummaryComponent, error)
	Notifier(profile agentcore.AgentProfile) (NotifierComponent, error)
	Policy(profile agentcore.AgentProfile) (PolicyComponent, error)
}

type DeliveryComponent interface {
	agentcore.ResultCommitter
	RequiredComponent()
}

type EvidenceComponent interface {
	agentcore.ResultValidator
	RequiredComponent()
}

type QAComponent interface {
	agentcore.ResultValidator
	RequiredComponent()
}

type SummaryComponent interface {
	agentcore.SummaryGenerator
	RequiredComponent()
}

type NotifierComponent interface {
	agentcore.Notifier
	RequiredComponent()
}

type PolicyComponent interface {
	agentcore.ResultValidator
	RequiredComponent()
}

type ComponentRegistryDeps struct {
	Delivery agentcore.ResultCommitter
	Evidence agentcore.ResultValidator
	QA       agentcore.ResultValidator
	Summary  agentcore.SummaryGenerator
	Notifier agentcore.Notifier
	Policy   agentcore.ResultValidator
}

type DefaultComponentRegistry struct {
	deps ComponentRegistryDeps
}

func NewDefaultComponentRegistry(deps ComponentRegistryDeps) (DefaultComponentRegistry, error) {
	if err := validateComponentRegistryDeps("", deps); err != nil {
		return DefaultComponentRegistry{}, err
	}
	return DefaultComponentRegistry{deps: deps}, nil
}

func NewDefaultComponentRegistryFromPipeline(p PipelineDependencyProvider, profile agentcore.AgentProfile) (DefaultComponentRegistry, error) {
	if p == nil {
		return DefaultComponentRegistry{}, componentDependencyMissing(profile.ID, "pipeline", "pipeline dependencies are not configured")
	}
	delivery, err := p.DeliveryCommitter(profile)
	if err != nil {
		return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "delivery", "delivery component missing", err)
	}
	summary, err := p.SummaryGenerator(profile)
	if err != nil {
		return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "summary", "summary component missing", err)
	}
	notifier, err := p.Notifier(profile)
	if err != nil {
		return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "notifier", "notifier component missing", err)
	}

	var evidence agentcore.ResultValidator
	var qa agentcore.ResultValidator
	var policy agentcore.ResultValidator
	if split, ok := p.(SplitValidatorProvider); ok {
		evidence, err = split.EvidenceValidatorForProfile(profile)
		if err != nil {
			return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "evidence", "evidence component missing", err)
		}
		qa, err = split.QAValidatorForProfile(profile)
		if err != nil {
			return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "qa", "qa component missing", err)
		}
		policy, err = split.PolicyValidatorForProfile(profile)
		if err != nil {
			return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "policy", "policy component missing", err)
		}
	} else {
		validator, err := p.ResultValidator(profile)
		if err != nil {
			return DefaultComponentRegistry{}, componentDependencyMissingWithCause(profile.ID, "validator", "result validator missing", err)
		}
		evidence = validator
		qa = validator
		policy = validator
	}

	deps := ComponentRegistryDeps{
		Delivery: delivery,
		Evidence: evidence,
		QA:       qa,
		Summary:  summary,
		Notifier: notifier,
		Policy:   policy,
	}
	if err := validateComponentRegistryDeps(profile.ID, deps); err != nil {
		return DefaultComponentRegistry{}, err
	}
	return DefaultComponentRegistry{deps: deps}, nil
}

func (r DefaultComponentRegistry) Delivery(profile agentcore.AgentProfile) (DeliveryComponent, error) {
	if r.deps.Delivery == nil {
		return nil, componentDependencyMissing(profile.ID, "delivery", "delivery component missing")
	}
	return deliveryComponent{ResultCommitter: r.deps.Delivery}, nil
}

func (r DefaultComponentRegistry) Evidence(profile agentcore.AgentProfile) (EvidenceComponent, error) {
	if r.deps.Evidence == nil {
		return nil, componentDependencyMissing(profile.ID, "evidence", "evidence component missing")
	}
	return validatorComponent{ResultValidator: r.deps.Evidence}, nil
}

func (r DefaultComponentRegistry) QA(profile agentcore.AgentProfile) (QAComponent, error) {
	if r.deps.QA == nil {
		return nil, componentDependencyMissing(profile.ID, "qa", "qa component missing")
	}
	return qaComponent{ResultValidator: r.deps.QA}, nil
}

func (r DefaultComponentRegistry) Summary(profile agentcore.AgentProfile) (SummaryComponent, error) {
	if r.deps.Summary == nil {
		return nil, componentDependencyMissing(profile.ID, "summary", "summary component missing")
	}
	return summaryComponent{SummaryGenerator: r.deps.Summary}, nil
}

func (r DefaultComponentRegistry) Notifier(profile agentcore.AgentProfile) (NotifierComponent, error) {
	if r.deps.Notifier == nil {
		return nil, componentDependencyMissing(profile.ID, "notifier", "notifier component missing")
	}
	return notifierComponent{Notifier: r.deps.Notifier}, nil
}

func (r DefaultComponentRegistry) Policy(profile agentcore.AgentProfile) (PolicyComponent, error) {
	if r.deps.Policy == nil {
		return nil, componentDependencyMissing(profile.ID, "policy", "policy component missing")
	}
	return policyComponent{ResultValidator: r.deps.Policy}, nil
}

func validateComponentRegistryDeps(profileID agentcore.AgentProfileID, deps ComponentRegistryDeps) error {
	if deps.Delivery == nil {
		return componentDependencyMissing(profileID, "delivery", "delivery component missing")
	}
	if deps.Evidence == nil {
		return componentDependencyMissing(profileID, "evidence", "evidence component missing")
	}
	if deps.QA == nil {
		return componentDependencyMissing(profileID, "qa", "qa component missing")
	}
	if deps.Summary == nil {
		return componentDependencyMissing(profileID, "summary", "summary component missing")
	}
	if deps.Notifier == nil {
		return componentDependencyMissing(profileID, "notifier", "notifier component missing")
	}
	if deps.Policy == nil {
		return componentDependencyMissing(profileID, "policy", "policy component missing")
	}
	return nil
}

func componentDependencyMissing(profileID agentcore.AgentProfileID, field, message string) Error {
	return componentDependencyMissingWithCause(profileID, field, message, nil)
}

func componentDependencyMissingWithCause(profileID agentcore.AgentProfileID, field, message string, cause error) Error {
	return newError(CodeComponentMissing, profileID, field, message, cause)
}

type ProductionComponentRegistry struct {
	DeliveryCommitterValue agentcore.ResultCommitter
	EvidenceValidator      agentcore.ResultValidator
	QAValidator            agentcore.ResultValidator
	SummaryGeneratorValue  agentcore.SummaryGenerator
	ReplyNotifier          agentcore.Notifier
	PolicyValidator        agentcore.ResultValidator
	SignalSinkValue        agentcore.SignalSink
	TraceRecorderValue     agentcore.TraceRecorder
	NormalizerValue        agentcore.ResultNormalizer
}

func NewProductionComponentRegistry(registry ProductionComponentRegistry) (ProductionComponentRegistry, error) {
	deps := ComponentRegistryDeps{
		Delivery: registry.DeliveryCommitterValue,
		Evidence: registry.EvidenceValidator,
		QA:       registry.QAValidator,
		Summary:  registry.SummaryGeneratorValue,
		Notifier: registry.ReplyNotifier,
		Policy:   registry.PolicyValidator,
	}
	if err := validateComponentRegistryDeps("", deps); err != nil {
		return ProductionComponentRegistry{}, err
	}
	if registry.TraceRecorderValue == nil {
		return ProductionComponentRegistry{}, componentDependencyMissing("", "trace", "trace recorder missing")
	}
	if registry.NormalizerValue == nil {
		return ProductionComponentRegistry{}, componentDependencyMissing("", "normalizer", "result normalizer missing")
	}
	return registry, nil
}

func (r ProductionComponentRegistry) Delivery(agentcore.AgentProfile) (DeliveryComponent, error) {
	if r.DeliveryCommitterValue == nil {
		return nil, nil
	}
	return deliveryComponent{ResultCommitter: r.DeliveryCommitterValue}, nil
}

func (r ProductionComponentRegistry) Evidence(agentcore.AgentProfile) (EvidenceComponent, error) {
	if r.EvidenceValidator == nil {
		return nil, nil
	}
	return validatorComponent{ResultValidator: r.EvidenceValidator}, nil
}

func (r ProductionComponentRegistry) QA(agentcore.AgentProfile) (QAComponent, error) {
	if r.QAValidator == nil {
		return nil, nil
	}
	return qaComponent{ResultValidator: r.QAValidator}, nil
}

func (r ProductionComponentRegistry) Summary(agentcore.AgentProfile) (SummaryComponent, error) {
	if r.SummaryGeneratorValue == nil {
		return nil, nil
	}
	return summaryComponent{SummaryGenerator: r.SummaryGeneratorValue}, nil
}

func (r ProductionComponentRegistry) Notifier(agentcore.AgentProfile) (NotifierComponent, error) {
	if r.ReplyNotifier == nil {
		return nil, nil
	}
	return notifierComponent{Notifier: r.ReplyNotifier}, nil
}

func (r ProductionComponentRegistry) Policy(agentcore.AgentProfile) (PolicyComponent, error) {
	if r.PolicyValidator == nil {
		return nil, nil
	}
	return policyComponent{ResultValidator: r.PolicyValidator}, nil
}

func (r ProductionComponentRegistry) DeliveryCommitterForProfile(profile agentcore.AgentProfile) (agentcore.ResultCommitter, error) {
	component, err := r.Delivery(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (r ProductionComponentRegistry) DeliveryCommitter(profile agentcore.AgentProfile) (agentcore.ResultCommitter, error) {
	return r.DeliveryCommitterForProfile(profile)
}

func (r ProductionComponentRegistry) SignalSink(agentcore.AgentProfile) (agentcore.SignalSink, error) {
	return r.SignalSinkValue, nil
}

func (r ProductionComponentRegistry) SummaryGeneratorForProfile(profile agentcore.AgentProfile) (agentcore.SummaryGenerator, error) {
	component, err := r.Summary(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (r ProductionComponentRegistry) SummaryGenerator(profile agentcore.AgentProfile) (agentcore.SummaryGenerator, error) {
	return r.SummaryGeneratorForProfile(profile)
}

func (r ProductionComponentRegistry) TraceRecorder(agentcore.AgentProfile) (agentcore.TraceRecorder, error) {
	if r.TraceRecorderValue == nil {
		return nil, nil
	}
	return r.TraceRecorderValue, nil
}

func (r ProductionComponentRegistry) NotifierForProfile(profile agentcore.AgentProfile) (agentcore.Notifier, error) {
	component, err := r.Notifier(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (r ProductionComponentRegistry) ResultValidator(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	policy, err := r.Policy(profile)
	if err != nil || policy == nil {
		return nil, err
	}
	return policy, nil
}

func (r ProductionComponentRegistry) ResultNormalizer(agentcore.AgentProfile) (agentcore.ResultNormalizer, error) {
	return r.NormalizerValue, nil
}

func (r ProductionComponentRegistry) EvidenceValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := r.Evidence(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (r ProductionComponentRegistry) QAValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := r.QA(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (r ProductionComponentRegistry) PolicyValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := r.Policy(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

type deliveryComponent struct{ agentcore.ResultCommitter }

func (deliveryComponent) RequiredComponent() {}

type validatorComponent struct{ agentcore.ResultValidator }

func (validatorComponent) RequiredComponent() {}

type qaComponent struct{ agentcore.ResultValidator }

func (qaComponent) RequiredComponent() {}

type summaryComponent struct{ agentcore.SummaryGenerator }

func (summaryComponent) RequiredComponent() {}

type notifierComponent struct{ agentcore.Notifier }

func (notifierComponent) RequiredComponent() {}

type policyComponent struct{ agentcore.ResultValidator }

func (policyComponent) RequiredComponent() {}

type ComponentSet struct {
	Delivery DeliveryComponent
	Evidence EvidenceComponent
	QA       QAComponent
	Summary  SummaryComponent
	Notifier NotifierComponent
	Policy   PolicyComponent
}
