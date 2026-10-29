package lifecycle

import "context"

type AgentStep interface {
	Run(ctx context.Context, state *AgentRunState) error
}

type AgentStepFunc func(context.Context, *AgentRunState) error

func (f AgentStepFunc) Run(ctx context.Context, state *AgentRunState) error {
	return f(ctx, state)
}

type AgentMiddleware interface {
	Name() string
	Wrap(next AgentStep) AgentStep
}

type middlewareFunc struct {
	name string
	fn   func(AgentStep) AgentStep
}

func MiddlewareFunc(name string, fn func(AgentStep) AgentStep) AgentMiddleware {
	return middlewareFunc{name: name, fn: fn}
}

func (m middlewareFunc) Name() string {
	return m.name
}

func (m middlewareFunc) Wrap(next AgentStep) AgentStep {
	return m.fn(next)
}
