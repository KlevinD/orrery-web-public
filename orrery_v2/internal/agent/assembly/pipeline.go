package assembly

import (
	agentcore "orrery_v2/internal/agent/core"
)

type PipelineDependencyProvider interface {
	DeliveryCommitter(profile agentcore.AgentProfile) (agentcore.ResultCommitter, error)
	SignalSink(profile agentcore.AgentProfile) (agentcore.SignalSink, error)
	SummaryGenerator(profile agentcore.AgentProfile) (agentcore.SummaryGenerator, error)
	TraceRecorder(profile agentcore.AgentProfile) (agentcore.TraceRecorder, error)
	Notifier(profile agentcore.AgentProfile) (agentcore.Notifier, error)
	ResultValidator(profile agentcore.AgentProfile) (agentcore.ResultValidator, error)
	ResultNormalizer(profile agentcore.AgentProfile) (agentcore.ResultNormalizer, error)
}

type SplitValidatorProvider interface {
	EvidenceValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error)
	QAValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error)
	PolicyValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error)
}

type PipelineDependencies struct {
	Delivery   agentcore.ResultCommitter
	Signals    agentcore.SignalSink
	Summary    agentcore.SummaryGenerator
	Trace      agentcore.TraceRecorder
	Notifier   agentcore.Notifier
	Validator  agentcore.ResultValidator
	Normalizer agentcore.ResultNormalizer
}

type ComponentPipelineProvider struct {
	Registry   ComponentRegistry
	Signals    agentcore.SignalSink
	Trace      agentcore.TraceRecorder
	Normalizer agentcore.ResultNormalizer
}

func (p ComponentPipelineProvider) DeliveryCommitter(profile agentcore.AgentProfile) (agentcore.ResultCommitter, error) {
	component, err := p.Registry.Delivery(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (p ComponentPipelineProvider) SignalSink(agentcore.AgentProfile) (agentcore.SignalSink, error) {
	return p.Signals, nil
}

func (p ComponentPipelineProvider) SummaryGenerator(profile agentcore.AgentProfile) (agentcore.SummaryGenerator, error) {
	component, err := p.Registry.Summary(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (p ComponentPipelineProvider) TraceRecorder(agentcore.AgentProfile) (agentcore.TraceRecorder, error) {
	return p.Trace, nil
}

func (p ComponentPipelineProvider) Notifier(profile agentcore.AgentProfile) (agentcore.Notifier, error) {
	component, err := p.Registry.Notifier(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (p ComponentPipelineProvider) ResultValidator(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	evidence, err := p.Registry.Evidence(profile)
	if err != nil || evidence == nil {
		return nil, err
	}
	qa, err := p.Registry.QA(profile)
	if err != nil || qa == nil {
		return nil, err
	}
	policy, err := p.Registry.Policy(profile)
	if err != nil || policy == nil {
		return nil, err
	}
	return validatorChain{evidence, qa, policy}, nil
}

func (p ComponentPipelineProvider) ResultNormalizer(agentcore.AgentProfile) (agentcore.ResultNormalizer, error) {
	return p.Normalizer, nil
}

func (p ComponentPipelineProvider) EvidenceValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := p.Registry.Evidence(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (p ComponentPipelineProvider) QAValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := p.Registry.QA(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

func (p ComponentPipelineProvider) PolicyValidatorForProfile(profile agentcore.AgentProfile) (agentcore.ResultValidator, error) {
	component, err := p.Registry.Policy(profile)
	if err != nil || component == nil {
		return nil, err
	}
	return component, nil
}

type validatorChain []agentcore.ResultValidator

func (c validatorChain) Validate(input agentcore.AgentInput, result agentcore.AgentResult) error {
	for _, validator := range c {
		if err := validator.Validate(input, result); err != nil {
			return err
		}
	}
	return nil
}
