package main

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/outbox"
)

func TestProductionAgentPostCommitRepairPersistsSafePayloadAndReplaysNotifier(t *testing.T) {
	publisher := &capturingPostCommitRepairPublisher{}
	notifier := &capturingPostCommitRepairNotifier{}
	repair := &productionAgentPostCommitRepair{publisher: publisher, notifier: notifier}
	input := agentcore.AgentInput{
		TenantID: "tenant-a", RunID: "run-1",
		Task:           agentcore.TaskInput{MissionID: "mission-1", ID: "task-1"},
		ContextPack:    agentcore.ContextPack{ConversationID: "conversation-1", DiscussionGroupID: agentcore.DiscussionGroupID("group-1")},
		RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"secret_marker": "must-not-be-persisted"}},
	}
	result := agentcore.AgentResult{
		MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
		Summary: agentcore.Summary{Text: "Committed outcome."}, InternalTraceRef: "artifact://trace-1",
	}
	if err := repair.Schedule(context.Background(), input, result); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if publisher.message.Topic != productionAgentPostCommitRepairTopic || publisher.message.IdempotencyKey != "agent-post-commit-repair:run-1" || publisher.message.MaxAttempts != 20 {
		t.Fatalf("outbox message = %+v", publisher.message)
	}
	if strings.Contains(string(publisher.message.Payload), "must-not-be-persisted") || strings.Contains(string(publisher.message.Payload), "artifact://trace-1") {
		t.Fatalf("post-commit repair payload persisted Runtime options: %s", publisher.message.Payload)
	}
	if err := repair.HandleOutbox(context.Background(), outbox.Job{Topic: publisher.message.Topic, Payload: publisher.message.Payload}); err != nil {
		t.Fatalf("HandleOutbox: %v", err)
	}
	if notifier.calls != 1 || notifier.input.TenantID != "tenant-a" || notifier.input.ContextPack.ConversationID != "conversation-1" || notifier.input.ContextPack.DiscussionGroupID != "group-1" || notifier.result.RunID != "run-1" || notifier.result.Summary.Text != "Committed outcome." {
		t.Fatalf("notifier calls=%d input=%+v result=%+v", notifier.calls, notifier.input, notifier.result)
	}
}

type capturingPostCommitRepairPublisher struct {
	message platform.OutboxMessage
}

func (p *capturingPostCommitRepairPublisher) Publish(_ context.Context, message platform.OutboxMessage) (string, error) {
	p.message = message
	return "repair-job-1", nil
}

type capturingPostCommitRepairNotifier struct {
	calls  int
	input  agentcore.AgentInput
	result agentcore.AgentResult
}

func (n *capturingPostCommitRepairNotifier) Notify(_ context.Context, input agentcore.AgentInput, result agentcore.AgentResult) error {
	n.calls++
	n.input = input
	n.result = result
	return nil
}
