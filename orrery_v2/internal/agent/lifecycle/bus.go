package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/logging"
)

type SignalBus interface {
	Emit(ctx context.Context, signal SignalEnvelope) (EmitReport, error)
	Subscribe(name SignalName, handler SignalHandler, opts HandlerOptions) (SubscriptionID, error)
	Unsubscribe(id SubscriptionID) error
}

type SignalBusOptions struct {
	Logger  logging.Logger
	Clock   clock.Clock
	Metrics MetricSink
}

type InMemorySignalBus struct {
	mu            sync.RWMutex
	logger        logging.Logger
	clock         clock.Clock
	metrics       MetricSink
	nextID        int
	registrations map[SignalName][]HandlerRegistration
}

func NewInMemorySignalBus(opts SignalBusOptions) *InMemorySignalBus {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	return &InMemorySignalBus{
		logger:        opts.Logger,
		clock:         opts.Clock,
		metrics:       opts.Metrics,
		registrations: map[SignalName][]HandlerRegistration{},
	}
}

func (b *InMemorySignalBus) Subscribe(name SignalName, handler SignalHandler, opts HandlerOptions) (SubscriptionID, error) {
	if !IsKnownSignalName(name) {
		return "", fmt.Errorf("subscribe lifecycle handler: unknown signal %q", name)
	}
	if handler == nil {
		return "", fmt.Errorf("subscribe lifecycle handler: nil handler")
	}
	if strings.TrimSpace(handler.Name()) == "" {
		return "", fmt.Errorf("subscribe lifecycle handler: empty handler name")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := SubscriptionID(fmt.Sprintf("subscription-%d", b.nextID))
	reg := HandlerRegistration{
		ID:      id,
		Name:    name,
		Handler: handler,
		Options: opts.normalized(),
		order:   b.nextID,
	}
	b.registrations[name] = append(b.registrations[name], reg)
	return id, nil
}

func (b *InMemorySignalBus) Unsubscribe(id SubscriptionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for name, registrations := range b.registrations {
		for i, reg := range registrations {
			if reg.ID == id {
				b.registrations[name] = append(registrations[:i], registrations[i+1:]...)
				return nil
			}
		}
	}
	return fmt.Errorf("subscription %q not found", id)
}

func (b *InMemorySignalBus) Emit(ctx context.Context, signal SignalEnvelope) (EmitReport, error) {
	if signal.EmittedAt.IsZero() {
		signal.EmittedAt = b.clock.Now()
	}
	if err := signal.Validate(); err != nil {
		return EmitReport{}, err
	}

	registrations := b.handlers(signal.Name)
	if b.logger != nil {
		b.logger.Debug(ctx, "agent lifecycle signal emitted",
			logging.String("signal_name", string(signal.Name)),
			logging.String("run_id", signal.RunID.String()),
			logging.String("correlation_id", string(signal.CorrelationID)),
			logging.Int("handler_count", len(registrations)),
		)
	}
	b.captureMetric(ctx, "agent.lifecycle.signal.emit", signalTelemetryFields(ctx, signal))

	var softFailures []HandlerFailure
	for _, reg := range registrations {
		if err := b.runHandler(ctx, reg, signal); err != nil {
			var failures HandlerFailures
			if errors.As(err, &failures) {
				softFailures = append(softFailures, failures.Failures...)
				continue
			}
			return EmitReport{}, err
		}
	}
	return EmitReport{SoftFailures: append([]HandlerFailure(nil), softFailures...)}, nil
}

type EmitReport struct {
	SoftFailures []HandlerFailure
}

func (b *InMemorySignalBus) handlers(name SignalName) []HandlerRegistration {
	b.mu.RLock()
	defer b.mu.RUnlock()

	registrations := b.registrations[name]
	copied := make([]HandlerRegistration, len(registrations))
	copy(copied, registrations)
	return copied
}

func (b *InMemorySignalBus) runHandler(ctx context.Context, reg HandlerRegistration, signal SignalEnvelope) error {
	policy := reg.Options.FailurePolicy
	if policy == FailurePolicyRetry {
		return b.runRetryHandler(ctx, reg, signal)
	}

	err := b.invokeHandler(ctx, reg, signal)
	return b.applyFailurePolicy(ctx, reg, signal, err, policy)
}

func (b *InMemorySignalBus) runRetryHandler(ctx context.Context, reg HandlerRegistration, signal SignalEnvelope) error {
	retry := reg.Options.RetryPolicy.normalized()
	var lastErr error
	for i := 0; i < retry.MaxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return b.applyFailurePolicy(ctx, reg, signal, err, FailurePolicyStop)
		}
		if err := b.invokeHandler(ctx, reg, signal); err != nil {
			lastErr = err
			if retry.Backoff > 0 && i < retry.MaxAttempts-1 {
				if err := sleepWithContext(ctx, retry.Backoff); err != nil {
					return b.applyFailurePolicy(ctx, reg, signal, err, FailurePolicyStop)
				}
			}
			continue
		}
		return nil
	}
	return b.applyFailurePolicy(ctx, reg, signal, lastErr, retry.Then)
}

func (b *InMemorySignalBus) invokeHandler(ctx context.Context, reg HandlerRegistration, signal SignalEnvelope) (err error) {
	startedAt := b.clock.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
		fields := signalTelemetryFields(ctx, signal)
		fields["handler"] = reg.Handler.Name()
		fields["failure_policy"] = string(reg.Options.FailurePolicy)
		fields["duration_ms"] = int(b.clock.Now().Sub(startedAt).Milliseconds())
		if err != nil {
			fields["error"] = err.Error()
		}
		b.captureMetric(ctx, "agent.lifecycle.handler.run", fields)
	}()
	return reg.Handler.HandleSignal(ctx, signal)
}

func (b *InMemorySignalBus) applyFailurePolicy(ctx context.Context, reg HandlerRegistration, signal SignalEnvelope, err error, policy FailurePolicy) error {
	if err == nil {
		return nil
	}

	failure := HandlerFailure{
		HandlerName:       reg.Handler.Name(),
		SignalName:        signal.Name,
		Policy:            policy,
		DegradeCapability: reg.Options.DegradeCapability,
		Cause:             err,
	}
	switch policy {
	case FailurePolicyContinue, FailurePolicyDegrade:
		if b.logger != nil {
			b.logger.Warn(ctx, "agent lifecycle signal handler failed",
				logging.String("signal_name", string(signal.Name)),
				logging.String("handler", reg.Handler.Name()),
				logging.String("failure_policy", string(policy)),
				logging.Any("error", failure),
			)
		}
		if policy == FailurePolicyDegrade {
			if failure.Cause == nil {
				failure.Cause = fmt.Errorf("degraded capability %s", reg.Options.DegradeCapability)
			}
		}
		return HandlerFailures{Failures: []HandlerFailure{failure}}
	case FailurePolicyStop:
		return errors.Join(ErrHandlerFailed, failure)
	default:
		return errors.Join(ErrHandlerFailed, failure)
	}
}

func (b *InMemorySignalBus) captureMetric(ctx context.Context, name string, fields map[string]any) {
	if b.metrics != nil {
		b.metrics.CaptureContext(ctx, name, fields)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
