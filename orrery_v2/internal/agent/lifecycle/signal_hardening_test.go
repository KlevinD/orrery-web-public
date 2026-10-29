package lifecycle

import (
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

func TestSignalBuilderCreatesIDsCorrelationAndCausation(t *testing.T) {
	builder := NewSignalBuilder(SignalBuilderOptions{
		Clock:       clock.NewFake(testNow()),
		IDGenerator: idgen.NewDeterministic("sig"),
	})
	parent := validSignal()
	parent.SignalID = SignalID("signal-parent")
	parent.CorrelationID = CorrelationID("corr-parent")

	signal, err := builder.FromState(validRunState(), AfterTaskAccepted, EmptyPayload{}, SignalBuildOptions{
		Parent: parent,
		Metadata: map[string]string{
			"component": "permission",
		},
	})
	if err != nil {
		t.Fatalf("build signal failed: %v", err)
	}

	if signal.SignalID != SignalID("sig-000001") {
		t.Fatalf("expected generated signal id, got %q", signal.SignalID)
	}
	if signal.CorrelationID != parent.CorrelationID {
		t.Fatalf("expected inherited correlation id, got %q", signal.CorrelationID)
	}
	if signal.CausationID != CausationID(parent.SignalID) {
		t.Fatalf("expected causation from parent signal id, got %q", signal.CausationID)
	}
	if !signal.EmittedAt.Equal(testNow()) {
		t.Fatalf("expected builder clock timestamp, got %s", signal.EmittedAt)
	}
}

func TestSignalEnvelopeValidationRequiresIdentityAndKnownSignal(t *testing.T) {
	signal := validSignal()
	signal.Name = SignalName("MadeUpSignal")
	signal.SignalID = ""
	signal.MissionID = ""
	signal.AgentID = ""
	signal.TaskID = ""
	signal.Attempt = 0
	signal.EmittedAt = time.Time{}

	err := signal.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, field := range []string{"signal_id", "name", "mission_id", "agent_id", "task_id", "attempt", "emitted_at"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("expected validation error to mention %s, got %v", field, err)
		}
	}
}

func TestSignalMetadataRejectsSecretsAndOversizedValues(t *testing.T) {
	for name, metadata := range map[string]map[string]string{
		"secret key": {"api_token": "secret-value"},
		"secret value": {
			"note": "Bearer abc123",
		},
		"oversized value": {
			"note": strings.Repeat("x", MaxMetadataValueBytes+1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			signal := validSignal()
			signal.SignalID = SignalID("signal-001")
			signal.Metadata = metadata
			err := signal.Validate()
			if err == nil {
				t.Fatal("expected metadata validation error")
			}
			if !IsInvalidSignal(err) {
				t.Fatalf("expected invalid signal, got %v", err)
			}
		})
	}
}

func TestPayloadSafetyRejectsTypedFullMemoryTraceAndSecretFields(t *testing.T) {
	for name, payload := range map[string]SignalPayload{
		"context raw memory": ContextBuildPayload{ContextPackRef: "ctx-001", RawMemory: "full memory text"},
		"runtime full trace": RuntimeProgressPayload{ProgressRef: "progress-001", FullTrace: "massive trace"},
		"runtime secret":     RuntimeStartPayload{RuntimeRequestRef: "runtime-001", SecretRef: "actual-secret"},
	} {
		t.Run(name, func(t *testing.T) {
			signal := validSignal()
			signal.SignalID = SignalID("signal-001")
			signal.Payload = payload
			err := signal.Validate()
			if err == nil {
				t.Fatal("expected payload validation error")
			}
			if !IsInvalidSignal(err) {
				t.Fatalf("expected invalid signal, got %v", err)
			}
		})
	}
}
