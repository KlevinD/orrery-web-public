package discussionsession

import (
	"context"
	"errors"
	"testing"
)

func TestServicePersonalAssistantDoesNotAutoRecallMemoryAndCapturesExchange(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID:  "lead-agent",
		response: DiscussionAgentResponse{AssistantMessage: "I will keep the plan concise.", Status: "completed", RuntimeKind: "codex"},
	}
	memory := &capturingDiscussionAgentMemoryProvider{context: DiscussionAgentMemoryContext{
		PersonalDigest: DiscussionAgentMemoryDigest{
			DigestID:   "digest.personal",
			MemoryKind: "personal",
			Summary:    "The user prefers concise plans.",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MemoryProvider:  memory,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "Plan my day.",
		IdempotencyKey:   "send-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	logicalAgentID := PersonalAssistantAgentID("tenant-a", "account-1")
	if memory.called {
		t.Fatalf("personal assistant memory was automatically queried for %q", memory.agentID)
	}
	if runner.request.AgentID != "lead-agent" || runner.request.LogicalAgentID != logicalAgentID {
		t.Fatalf("runtime request identity = host %q logical %q", runner.request.AgentID, runner.request.LogicalAgentID)
	}
	if runner.request.Memory.PersonalDigest.Summary != "" || len(runner.request.Memory.CollectiveDigests) != 0 {
		t.Fatalf("runtime received automatic memory = %+v", runner.request.Memory)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "lead-agent" || dto.Runs[0].LogicalAgentID != logicalAgentID {
		t.Fatalf("run identity = %+v", dto.Runs)
	}
	if len(memory.exchanges) != 1 {
		t.Fatalf("captured exchanges = %+v", memory.exchanges)
	}
	exchange := memory.exchanges[0]
	if exchange.AgentID != logicalAgentID || exchange.OwnerID != "account-1" || exchange.TenantID != "tenant-a" ||
		exchange.ConversationID != "conv-1" || exchange.RunID != "run-1" || exchange.UserMessageID != "msg-user" ||
		exchange.AssistantMessageID != "msg-assistant" || exchange.UserContent != "Plan my day." ||
		exchange.AssistantContent != "I will keep the plan concise." {
		t.Fatalf("captured exchange = %+v", exchange)
	}
}

func TestServicePersonalAssistantContinuesWhenMemoryIsUnavailable(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID:  "lead-agent",
		response: DiscussionAgentResponse{AssistantMessage: "ok", Status: "completed", RuntimeKind: "pi"},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MemoryProvider:  &capturingDiscussionAgentMemoryProvider{err: errors.New("PageIndex unavailable")},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "test",
		IdempotencyKey:   "send-1",
	})
	if err != nil {
		t.Fatalf("personal assistant should continue without memory: %v", err)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" || dto.Status == "failed" {
		t.Fatalf("personal assistant after memory failure = %+v", dto)
	}
	if runner.request.Memory.PersonalDigest.Summary != "" || len(runner.request.Memory.CollectiveDigests) != 0 {
		t.Fatalf("runtime received memory after provider failure: %+v", runner.request.Memory)
	}
}
