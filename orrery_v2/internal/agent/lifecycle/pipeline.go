package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/platform/clock"
)

type PipelineOptions struct {
	Bus        SignalBus
	Clock      clock.Clock
	Hooks      *HookRegistry
	Middleware []AgentMiddleware
	Builder    *SignalBuilder
}

type PipelineExecutor struct {
	bus        SignalBus
	clock      clock.Clock
	hooks      *HookRegistry
	middleware []AgentMiddleware
	builder    *SignalBuilder
}

type PipelineStep struct {
	Name    string
	Before  SignalName
	After   SignalName
	Payload SignalPayload
	Step    func(context.Context, *AgentRunState) error
}

func NewPipelineExecutor(opts PipelineOptions) *PipelineExecutor {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Hooks == nil {
		opts.Hooks = NewHookRegistry()
	}
	if opts.Builder == nil {
		opts.Builder = NewSignalBuilder(SignalBuilderOptions{Clock: opts.Clock})
	}
	return &PipelineExecutor{
		bus:        opts.Bus,
		clock:      opts.Clock,
		hooks:      opts.Hooks,
		middleware: append([]AgentMiddleware(nil), opts.Middleware...),
		builder:    opts.Builder,
	}
}

func (e *PipelineExecutor) RunStep(ctx context.Context, state *AgentRunState, step PipelineStep) error {
	if state == nil {
		return fmt.Errorf("%w: nil agent run state", ErrPipelineStopped)
	}
	if step.Step == nil {
		return fmt.Errorf("%w: nil pipeline step %q", ErrPipelineStopped, step.Name)
	}

	if err := e.emit(ctx, state, step.Before, step.Payload); err != nil {
		return e.stop(ctx, state, err)
	}
	for _, reg := range e.hooks.PreHookRegistrations() {
		if err := reg.Hook.BeforeAgentStep(ctx, state); err != nil {
			if e.applyHookFailure(state, HandlerFailure{
				HandlerName:       reg.Hook.Name(),
				SignalName:        step.Before,
				Policy:            reg.Options.FailurePolicy,
				DegradeCapability: reg.Options.DegradeCapability,
				Cause:             err,
			}) {
				continue
			}
			return e.stop(ctx, state, HandlerFailure{HandlerName: reg.Hook.Name(), SignalName: step.Before, Policy: reg.Options.FailurePolicy, Cause: err})
		}
	}

	wrapped := e.wrapStep(AgentStepFunc(step.Step))
	if err := wrapped.Run(ctx, state); err != nil {
		return e.classifyStepError(ctx, state, err)
	}

	for _, reg := range e.hooks.PostHookRegistrations() {
		if err := reg.Hook.AfterAgentStep(ctx, state); err != nil {
			if e.applyHookFailure(state, HandlerFailure{
				HandlerName:       reg.Hook.Name(),
				SignalName:        step.After,
				Policy:            reg.Options.FailurePolicy,
				DegradeCapability: reg.Options.DegradeCapability,
				Cause:             err,
			}) {
				continue
			}
			return e.stop(ctx, state, HandlerFailure{HandlerName: reg.Hook.Name(), SignalName: step.After, Policy: reg.Options.FailurePolicy, Cause: err})
		}
	}
	if err := e.emit(ctx, state, step.After, step.Payload); err != nil {
		return e.stop(ctx, state, err)
	}
	return nil
}

func (e *PipelineExecutor) RunPlan(ctx context.Context, state *AgentRunState, plan []PipelineStep) error {
	for _, step := range plan {
		if err := e.RunStep(ctx, state, step); err != nil {
			return err
		}
	}
	return nil
}

func (e *PipelineExecutor) EmitRuntimeProgress(ctx context.Context, state *AgentRunState, payload RuntimeProgressPayload) error {
	return e.emit(ctx, state, RuntimeProgress, payload)
}

func (e *PipelineExecutor) wrapStep(step AgentStep) AgentStep {
	wrapped := step
	for i := len(e.middleware) - 1; i >= 0; i-- {
		wrapped = e.middleware[i].Wrap(wrapped)
	}
	return wrapped
}

func (e *PipelineExecutor) classifyStepError(ctx context.Context, state *AgentRunState, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return e.stopWithSignal(ctx, state, AgentTimeout, err)
	case errors.Is(err, context.Canceled):
		return e.stopWithSignal(ctx, state, AgentCancelled, err)
	case errors.Is(err, ErrPipelineStopped):
		return e.stopWithSignal(ctx, state, AgentBlocked, err)
	default:
		return e.stopWithSignal(ctx, state, AgentFailed, err)
	}
}

func (e *PipelineExecutor) stop(ctx context.Context, state *AgentRunState, err error) error {
	return e.stopWithSignal(ctx, state, AgentFailed, err)
}

func (e *PipelineExecutor) stopWithSignal(ctx context.Context, state *AgentRunState, name SignalName, err error) error {
	state.AddError(err)
	if signalErr := e.emit(ctx, state, name, FailurePayload{Code: string(name), Message: err.Error()}); signalErr != nil {
		state.AddWarning(fmt.Sprintf("exception signal %s emission failed: %v", name, signalErr))
	}
	return errors.Join(ErrPipelineStopped, err)
}

func (e *PipelineExecutor) emit(ctx context.Context, state *AgentRunState, name SignalName, payload SignalPayload) error {
	if name == "" || e.bus == nil {
		return nil
	}
	signal, err := e.builder.FromState(*state, name, payload, SignalBuildOptions{})
	if err != nil {
		return err
	}
	report, err := e.bus.Emit(ctx, signal)
	if err != nil {
		var failures HandlerFailures
		if errors.As(err, &failures) && failures.IsSoft() {
			e.recordSoftFailures(state, failures)
			return nil
		}
		return err
	}
	if len(report.SoftFailures) > 0 {
		e.recordSoftFailures(state, HandlerFailures{Failures: report.SoftFailures})
	}
	return nil
}

func (e *PipelineExecutor) recordSoftFailures(state *AgentRunState, failures HandlerFailures) {
	for _, failure := range failures.Failures {
		state.AddWarning(failure.Error())
		if failure.Policy == FailurePolicyDegrade {
			state.MarkDegraded(failure.DegradeCapability)
		}
	}
}

func (e *PipelineExecutor) applyHookFailure(state *AgentRunState, failure HandlerFailure) bool {
	switch failure.Policy {
	case FailurePolicyContinue:
		state.AddWarning(failure.Error())
		return true
	case FailurePolicyDegrade:
		state.AddWarning(failure.Error())
		state.MarkDegraded(failure.DegradeCapability)
		return true
	default:
		return false
	}
}
