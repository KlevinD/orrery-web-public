package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/tracing"
)

func TestSignalBusRejectsInvalidSubscriptions(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})

	if _, err := bus.Subscribe(SignalName("NotASignal"), HandlerFunc("handler", func(context.Context, SignalEnvelope) error {
		return nil
	}), HandlerOptions{}); err == nil {
		t.Fatal("expected unknown signal subscription to fail")
	}
	if _, err := bus.Subscribe(BeforeTaskAccepted, nil, HandlerOptions{}); err == nil {
		t.Fatal("expected nil handler subscription to fail")
	}
	if _, err := bus.Subscribe(BeforeTaskAccepted, HandlerFunc("", func(context.Context, SignalEnvelope) error {
		return nil
	}), HandlerOptions{}); err == nil {
		t.Fatal("expected empty handler name subscription to fail")
	}
}

func TestSignalBusInterfaceExposesSubscriptionErrors(t *testing.T) {
	var bus SignalBus = NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})

	if _, err := bus.Subscribe(SignalName("NotASignal"), HandlerFunc("handler", func(context.Context, SignalEnvelope) error {
		return nil
	}), HandlerOptions{}); err == nil {
		t.Fatal("expected interface subscription to return invalid signal error")
	}
}

func TestSignalBusRecoversHandlerPanicAsHandlerFailure(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("panic-handler", func(context.Context, SignalEnvelope) error {
		panic("boom")
	}), HandlerOptions{FailurePolicy: FailurePolicyStop})

	_, err := bus.Emit(context.Background(), validSignal())
	if err == nil {
		t.Fatal("expected handler failure")
	}
	if !IsHandlerFailed(err) {
		t.Fatalf("expected handler failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "panic-handler") || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic details in error, got %v", err)
	}
}

func TestSignalBusAggregatesSoftFailuresAndRecordsTelemetry(t *testing.T) {
	sink := metrics.NewBoundedSink(20)
	bus := NewInMemorySignalBus(SignalBusOptions{
		Clock:   clock.NewFake(testNow()),
		Metrics: NewMetricSink(sink),
	})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("audit", func(context.Context, SignalEnvelope) error {
		return errors.New("audit failed")
	}), HandlerOptions{FailurePolicy: FailurePolicyContinue})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("cost", func(context.Context, SignalEnvelope) error {
		return errors.New("cost failed")
	}), HandlerOptions{FailurePolicy: FailurePolicyDegrade, DegradeCapability: "cost_estimation"})

	report, err := bus.Emit(context.Background(), validSignal())
	if err != nil {
		t.Fatalf("soft failures should not make direct emit fail: %v", err)
	}
	if len(report.SoftFailures) != 2 {
		t.Fatalf("expected 2 soft failures, got %#v", report.SoftFailures)
	}

	events := sink.Drain(20)
	if len(events) < 3 {
		t.Fatalf("expected emit and handler telemetry events, got %#v", events)
	}
	if events[0].Name != "agent.lifecycle.signal.emit" {
		t.Fatalf("expected lifecycle emit metric first, got %#v", events[0])
	}
}

func TestSignalBusTelemetryIncludesTraceFields(t *testing.T) {
	sink := metrics.NewBoundedSink(5)
	bus := NewInMemorySignalBus(SignalBusOptions{
		Clock:   clock.NewFake(testNow()),
		Metrics: NewMetricSink(sink),
	})
	ctx := tracing.WithSpanID(tracing.WithTraceID(context.Background(), "trace-001"), "span-001")

	if _, err := bus.Emit(ctx, validSignal()); err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	events := sink.Drain(5)
	if len(events) == 0 {
		t.Fatal("expected telemetry event")
	}
	if events[0].Fields["trace_id"] != "trace-001" || events[0].Fields["span_id"] != "span-001" {
		t.Fatalf("expected trace fields in telemetry, got %#v", events[0].Fields)
	}
}

func TestSignalBusRetryBackoffStopsWhenContextCancelled(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	attempts := 0
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("notify", func(context.Context, SignalEnvelope) error {
		attempts++
		return errors.New("temporary")
	}), HandlerOptions{
		FailurePolicy: FailurePolicyRetry,
		RetryPolicy: RetryPolicy{
			MaxAttempts: 5,
			Backoff:     10 * time.Millisecond,
			Then:        FailurePolicyStop,
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bus.Emit(ctx, validSignal())
	if err == nil {
		t.Fatal("expected context cancellation to stop retry")
	}
	if attempts > 1 {
		t.Fatalf("expected retry loop to stop immediately after cancellation, got %d attempts", attempts)
	}
}
