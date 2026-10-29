package lifecycle

import "context"

type StandardLifecycleSteps struct {
	ReceiveTask     func(context.Context, *AgentRunState) error
	AcceptTask      func(context.Context, *AgentRunState) error
	BuildContext    func(context.Context, *AgentRunState) error
	StartRuntime    func(context.Context, *AgentRunState) error
	FinishRuntime   func(context.Context, *AgentRunState) error
	NormalizeResult func(context.Context, *AgentRunState) error
	ValidateResult  func(context.Context, *AgentRunState) error
	CommitResult    func(context.Context, *AgentRunState) error
	Reply           func(context.Context, *AgentRunState) error
}

func StandardLifecyclePlan(steps StandardLifecycleSteps) []PipelineStep {
	return []PipelineStep{
		{Name: "receive_task", Before: BeforeTaskReceived, After: AfterTaskReceived, Step: requiredStep(steps.ReceiveTask)},
		{Name: "accept_task", Before: BeforeTaskAccepted, After: AfterTaskAccepted, Step: requiredStep(steps.AcceptTask)},
		{Name: "build_context", Before: BeforeContextBuild, After: AfterContextBuild, Step: requiredStep(steps.BuildContext)},
		{Name: "start_runtime", Before: BeforeRuntimeStart, After: RuntimeStarted, Step: requiredStep(steps.StartRuntime)},
		{Name: "finish_runtime", Before: BeforeRuntimeFinish, After: AfterRuntimeFinish, Step: requiredStep(steps.FinishRuntime)},
		{Name: "normalize_result", Before: BeforeResultNormalize, After: AfterResultNormalize, Step: requiredStep(steps.NormalizeResult)},
		{Name: "validate_result", Before: BeforeResultValidate, After: AfterResultValidate, Step: requiredStep(steps.ValidateResult)},
		{Name: "commit_result", Before: BeforeResultCommit, After: AfterResultCommit, Step: requiredStep(steps.CommitResult)},
		{Name: "reply", Before: BeforeAgentReply, After: AfterAgentReply, Step: requiredStep(steps.Reply)},
	}
}

func StandardLifecycleSignalOrder() []SignalName {
	return []SignalName{
		BeforeTaskReceived,
		AfterTaskReceived,
		BeforeTaskAccepted,
		AfterTaskAccepted,
		BeforeContextBuild,
		AfterContextBuild,
		BeforeRuntimeStart,
		RuntimeStarted,
		BeforeRuntimeFinish,
		AfterRuntimeFinish,
		BeforeResultNormalize,
		AfterResultNormalize,
		BeforeResultValidate,
		AfterResultValidate,
		BeforeResultCommit,
		AfterResultCommit,
		BeforeAgentReply,
		AfterAgentReply,
	}
}

func requiredStep(step func(context.Context, *AgentRunState) error) func(context.Context, *AgentRunState) error {
	if step != nil {
		return step
	}
	return func(context.Context, *AgentRunState) error { return nil }
}
