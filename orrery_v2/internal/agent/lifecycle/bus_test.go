package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
)

func TestSignalBusExecutesHandlersInRegistrationOrder(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	var calls []string

	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("first", func(context.Context, SignalEnvelope) error {
		calls = append(calls, "first")
		return nil
	}), HandlerOptions{})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("second", func(context.Context, SignalEnvelope) error {
		calls = append(calls, "second")
		return nil
	}), HandlerOptions{})

	if _, err := bus.Emit(context.Background(), validSignal()); err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("handlers ran out of order: %#v", calls)
	}
}

func TestSignalBusRejectsInvalidSignalBeforeHandlers(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	called := false
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("must-not-run", func(context.Context, SignalEnvelope) error {
		called = true
		return nil
	}), HandlerOptions{})

	signal := validSignal()
	signal.CorrelationID = ""

	_, err := bus.Emit(context.Background(), signal)
	if err == nil {
		t.Fatal("expected invalid signal error")
	}
	if !IsMissingCorrelationID(err) {
		t.Fatalf("expected missing correlation id error, got %v", err)
	}
	if called {
		t.Fatal("handler ran for invalid signal")
	}
}

func TestSignalBusStopPolicyReturnsHandlerFailure(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	want := errors.New("permission denied")
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("permission", func(context.Context, SignalEnvelope) error {
		return want
	}), HandlerOptions{FailurePolicy: FailurePolicyStop})

	_, err := bus.Emit(context.Background(), validSignal())
	if err == nil {
		t.Fatal("expected handler failure")
	}
	if !IsHandlerFailed(err) {
		t.Fatalf("expected handler_failed, got %v", err)
	}
	var failure HandlerFailure
	if !errors.As(err, &failure) {
		t.Fatalf("expected HandlerFailure, got %T", err)
	}
	if failure.HandlerName != "permission" || failure.SignalName != BeforeTaskAccepted || failure.Policy != FailurePolicyStop {
		t.Fatalf("unexpected failure details: %#v", failure)
	}
}

func TestSignalBusContinueAndDegradePoliciesContinueAfterFailure(t *testing.T) {
	for _, policy := range []FailurePolicy{FailurePolicyContinue, FailurePolicyDegrade} {
		t.Run(string(policy), func(t *testing.T) {
			bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
			var calls []string
			mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("soft-fail", func(context.Context, SignalEnvelope) error {
				calls = append(calls, "soft-fail")
				return errors.New("soft failure")
			}), HandlerOptions{FailurePolicy: policy})
			mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("after", func(context.Context, SignalEnvelope) error {
				calls = append(calls, "after")
				return nil
			}), HandlerOptions{})

			report, err := bus.Emit(context.Background(), validSignal())
			if err != nil {
				t.Fatalf("emit should not return soft failure as error for %s: %v", policy, err)
			}
			if len(report.SoftFailures) != 1 || !(HandlerFailures{Failures: report.SoftFailures}).IsSoft() {
				t.Fatalf("expected soft failure report, got %#v", report)
			}
			if len(calls) != 2 || calls[1] != "after" {
				t.Fatalf("expected later handler to run, got %#v", calls)
			}
		})
	}
}

func TestSignalBusRetryPolicyRetriesThenDegrades(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	attempts := 0
	afterCalled := false
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("notify", func(context.Context, SignalEnvelope) error {
		attempts++
		return errors.New("temporary delivery failure")
	}), HandlerOptions{
		FailurePolicy: FailurePolicyRetry,
		RetryPolicy:   RetryPolicy{MaxAttempts: 3, Then: FailurePolicyDegrade},
	})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("after", func(context.Context, SignalEnvelope) error {
		afterCalled = true
		return nil
	}), HandlerOptions{})

	report, err := bus.Emit(context.Background(), validSignal())
	if err != nil {
		t.Fatalf("retry then degrade should not return hard error: %v", err)
	}
	if len(report.SoftFailures) != 1 || !(HandlerFailures{Failures: report.SoftFailures}).IsSoft() {
		t.Fatalf("expected retry then degrade soft failure report, got %#v", report)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if !afterCalled {
		t.Fatal("expected handler after retry degrade to run")
	}
}

func testNow() time.Time {
	return time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
}

func mustSubscribe(t *testing.T, bus *InMemorySignalBus, name SignalName, handler SignalHandler, opts HandlerOptions) SubscriptionID {
	t.Helper()
	id, err := bus.Subscribe(name, handler, opts)
	if err != nil {
		t.Fatalf("subscribe %s failed: %v", name, err)
	}
	return id
}
