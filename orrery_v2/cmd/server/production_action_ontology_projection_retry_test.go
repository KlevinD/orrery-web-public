package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestActionLifecycleProjectionMethodsUseRetryBoundary(t *testing.T) {
	for _, path := range []string{
		"production_agent_execution_ontology.go",
		"production_action_writeback_ontology.go",
	} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(source, []byte("projectActionOntologyEvents(")) {
			t.Fatalf("%s does not use the Action Ontology retry boundary", path)
		}
	}
}

func TestActionRunLifecycleOntologyEventsProjectsOnlyCurrentTransition(t *testing.T) {
	now := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	run := actioncontract.ActionRun{
		ID: "run-transition", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor:   actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"},
		Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeMCPGateway, ID: "mcp-gateway"},
		Status:  actioncontract.RunStatusStarted, CreatedAt: now,
		ObjectRefs: []actioncontract.ObjectRef{
			{Kind: "responsibility_policy", ID: "policy-1"},
			{Kind: "tool", ID: "marketing_visual_generate_image2"},
		},
	}

	started, err := actionRunLifecycleOntologyEvents(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 3 {
		t.Fatalf("started events = %d, want start plus two targets", len(started))
	}
	for _, event := range started {
		if event.Type == "action.run.completed" {
			t.Fatal("started transition included terminal event")
		}
	}

	completedAt := now.Add(time.Minute)
	for _, status := range []actioncontract.RunStatus{
		actioncontract.RunStatusCompleted,
		actioncontract.RunStatusFailed,
	} {
		run.Status = status
		run.CompletedAt = &completedAt
		terminal, err := actionRunLifecycleOntologyEvents(run)
		if err != nil {
			t.Fatal(err)
		}
		if len(terminal) != 1 || terminal[0].Type != "action.run.completed" {
			t.Fatalf("%s events = %+v, want only action.run.completed", status, terminal)
		}
		if got := terminal[0].Payload["status"]; got != string(status) {
			t.Fatalf("terminal status = %v, want %s", got, status)
		}
	}
}

func TestProjectActionOntologyEventsRetriesDeadlinesWithoutChangingEvent(t *testing.T) {
	event := graphprotocol.DomainEvent{
		ID: "action-run-completed:run-1:1", TenantID: "tenant-1",
		Type: "action.run.completed", StreamID: "run-1", Version: 1,
	}
	var attempts []graphprotocol.DomainEvent
	err := projectActionOntologyEvents(context.Background(), []graphprotocol.DomainEvent{event}, []time.Duration{0, 0}, func(_ context.Context, got graphprotocol.DomainEvent) error {
		attempts = append(attempts, got)
		if len(attempts) < 3 {
			return fmt.Errorf("begin postgres transaction: %w", context.DeadlineExceeded)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("projectActionOntologyEvents: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(attempts))
	}
	for _, got := range attempts {
		if !reflect.DeepEqual(got, event) {
			t.Fatalf("event changed across retries: got=%+v want=%+v", got, event)
		}
	}
}

func TestProjectActionOntologyEventsDoesNotRetryDeterministicFailure(t *testing.T) {
	want := errors.New("unsupported ontology event")
	attempts := 0
	err := projectActionOntologyEvents(context.Background(), []graphprotocol.DomainEvent{{ID: "event-1", Type: "invalid"}}, []time.Duration{0, 0}, func(context.Context, graphprotocol.DomainEvent) error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("error=%v attempts=%d, want deterministic single failure", err, attempts)
	}
}

func TestProjectActionOntologyEventsReturnsAfterThreeDeadlineFailures(t *testing.T) {
	attempts := 0
	err := projectActionOntologyEvents(context.Background(), []graphprotocol.DomainEvent{{ID: "event-timeout", Type: "action.run.completed"}}, []time.Duration{0, 0}, func(context.Context, graphprotocol.DomainEvent) error {
		attempts++
		return fmt.Errorf("project transaction: %w", context.DeadlineExceeded)
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 3 {
		t.Fatalf("error=%v attempts=%d, want deadline after 3 attempts", err, attempts)
	}
}

func TestProjectActionOntologyEventsCapsRetryAttemptsAtThree(t *testing.T) {
	attempts := 0
	err := projectActionOntologyEvents(context.Background(), []graphprotocol.DomainEvent{{ID: "event-capped", Type: "action.run.completed"}}, []time.Duration{0, 0, 0, 0}, func(context.Context, graphprotocol.DomainEvent) error {
		attempts++
		return context.DeadlineExceeded
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 3 {
		t.Fatalf("error=%v attempts=%d, want hard cap of 3 attempts", err, attempts)
	}
}

func TestProjectActionOntologyEventsDetachesFromCancelledCaller(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := projectActionOntologyEvents(parent, []graphprotocol.DomainEvent{{ID: "event-detached", Type: "action.run.completed"}}, nil, func(ctx context.Context, _ graphprotocol.DomainEvent) error {
		called = true
		if err := ctx.Err(); err != nil {
			t.Fatalf("projection inherited caller cancellation: %v", err)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("error=%v called=%v", err, called)
	}
}
