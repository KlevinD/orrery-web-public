package lifecycle

import "context"

type SubscriptionID string

type SignalHandler interface {
	Name() string
	HandleSignal(ctx context.Context, signal SignalEnvelope) error
}

type handlerFunc struct {
	name string
	fn   func(context.Context, SignalEnvelope) error
}

func HandlerFunc(name string, fn func(context.Context, SignalEnvelope) error) SignalHandler {
	return handlerFunc{name: name, fn: fn}
}

func (h handlerFunc) Name() string {
	return h.name
}

func (h handlerFunc) HandleSignal(ctx context.Context, signal SignalEnvelope) error {
	return h.fn(ctx, signal)
}

type HandlerOptions struct {
	FailurePolicy     FailurePolicy
	RetryPolicy       RetryPolicy
	DegradeCapability string
}

func (o HandlerOptions) normalized() HandlerOptions {
	if o.FailurePolicy == "" {
		o.FailurePolicy = FailurePolicyStop
	}
	if o.FailurePolicy == FailurePolicyRetry {
		o.RetryPolicy = o.RetryPolicy.normalized()
	}
	return o
}

type HandlerRegistration struct {
	ID      SubscriptionID
	Name    SignalName
	Handler SignalHandler
	Options HandlerOptions
	order   int
}
