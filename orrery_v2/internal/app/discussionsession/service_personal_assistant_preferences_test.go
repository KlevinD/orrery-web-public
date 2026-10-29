package discussionsession

import (
	"context"
	"errors"
	"testing"
)

type preferenceMemoryProvider struct {
	capturingDiscussionAgentMemoryProvider
	owners        []PersonalAssistantPreferenceOwner
	digest        DiscussionAgentMemoryDigest
	preferenceErr error
}

func (p *preferenceMemoryProvider) PersonalAssistantPreferences(_ context.Context, owner PersonalAssistantPreferenceOwner) (DiscussionAgentMemoryDigest, error) {
	p.owners = append(p.owners, owner)
	return p.digest, p.preferenceErr
}

func TestServicePersonalAssistantInjectsPreferencesAcrossRuntimeHosts(t *testing.T) {
	for _, host := range []string{"lead-agent", "alternate-host"} {
		t.Run(host, func(t *testing.T) {
			repo := newMemoryRepository(t)
			runner := &capturingDiscussionAgentRunner{agentID: host, response: DiscussionAgentResponse{AssistantMessage: "好的", Status: "completed", RuntimeKind: "codex"}}
			memory := &preferenceMemoryProvider{digest: DiscussionAgentMemoryDigest{DigestID: "preferences", MemoryKind: "personal", Summary: "默认用中文回答", SourceMemory: []string{"preference-language"}}}
			service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, MemoryProvider: memory, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant")})
			viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
			_, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "安排一下今天的工作", IdempotencyKey: "send-1"})
			if err != nil {
				t.Fatal(err)
			}
			if memory.called {
				t.Fatal("ordinary turn invoked broad memory recall")
			}
			if len(memory.owners) != 1 || memory.owners[0] != (PersonalAssistantPreferenceOwner{TenantID: "tenant-a", OwnerID: "account-1", AgentID: PersonalAssistantAgentID("tenant-a", "account-1")}) {
				t.Fatalf("preference identity: %+v", memory.owners)
			}
			if runner.request.AgentID != host || runner.request.Memory.PersonalDigest.Summary != memory.digest.Summary {
				t.Fatalf("runtime preferences: %+v", runner.request)
			}
			if len(memory.exchanges) != 1 || !memory.exchanges[0].PreferenceExtractionAllowed {
				t.Fatalf("private user source did not enable extraction: %+v", memory.exchanges)
			}
		})
	}
}

func TestServicePreferencesDoNotEnterSharedGroupsOrWrongOwnerRequests(t *testing.T) {
	ctx := context.Background()
	memory := &preferenceMemoryProvider{digest: DiscussionAgentMemoryDigest{Summary: "private preferences"}}
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), MemoryProvider: memory})
	request := DiscussionAgentRequest{ConversationKind: "group", ConversationOwnerID: "owner", ConversationTenantID: "tenant", PersonalAssistantOriginOwnerID: "owner", PersonalAssistantOriginSubjectID: "user", LogicalAgentID: PersonalAssistantAgentID("tenant", "owner")}
	if err := service.attachPersonalAssistantPreferences(ctx, &request); err != nil {
		t.Fatal(err)
	}
	if len(memory.owners) != 0 || request.Memory.PersonalDigest.Summary != "" {
		t.Fatal("private preferences leaked into a shared group")
	}
	request.ConversationKind = ConversationKindPersonalAssistant
	request.LogicalAgentID = PersonalAssistantAgentID("another-tenant", "owner")
	if err := service.attachPersonalAssistantPreferences(ctx, &request); err == nil {
		t.Fatal("mismatched tenant accepted")
	}
	if len(memory.owners) != 0 {
		t.Fatal("mismatched tenant reached preference provider")
	}
}

func TestServicePreferenceFailureDoesNotFailTheConversation(t *testing.T) {
	ctx := context.Background()
	memory := &preferenceMemoryProvider{preferenceErr: errors.New("memory offline")}
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "ok", Status: "completed"}}
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), DiscussionAgent: runner, MemoryProvider: memory, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant")})
	dto, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "hello", IdempotencyKey: "send-1"})
	if err != nil || len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("conversation failed: %+v %v", dto, err)
	}
	if len(memory.owners) != 1 || runner.request.Memory.PersonalDigest.Summary != "" {
		t.Fatal("failure did not yield an empty preference context")
	}
}
