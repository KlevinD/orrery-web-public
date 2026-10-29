package discussionsession

import (
	"context"
	"testing"
	"time"
)

type prewarmingDiscussionAgentRunner struct {
	capturingDiscussionAgentRunner
	prewarmed chan DiscussionAgentRequest
}

func (r *prewarmingDiscussionAgentRunner) PrewarmDiscussion(_ context.Context, request DiscussionAgentRequest) error {
	r.prewarmed <- request
	return nil
}

func TestServicePrewarmCreatesReusesAndMaterializesGroupDraft(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &prewarmingDiscussionAgentRunner{
		capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"},
		prewarmed:                      make(chan DiscussionAgentRequest, 2),
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-draft", "msg-user"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindHuman}

	created, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{
		Title: "New chat", IdempotencyKey: "prewarm-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ConversationID != "conv-draft" || created.ConversationKind != ConversationKindGroup || created.Status != "draft" || created.Title != "New chat" || len(created.Messages) != 0 {
		t.Fatalf("created draft = %+v", created)
	}
	page, err := service.ListConversations(context.Background(), viewer, ListConversationsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 0 || len(page.Conversations) != 0 {
		t.Fatalf("empty runtime draft leaked into discussion list = %+v", page)
	}
	first := waitForPrewarmRequest(t, runner.prewarmed)
	if first.ConversationID != "conv-draft" || first.ConversationKind != ConversationKindGroup || first.RunID != "prewarm-conv-draft" || first.Mode != "prewarm" {
		t.Fatalf("prewarm request = %+v", first)
	}

	reused, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{
		Title: "Another title", IdempotencyKey: "prewarm-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused.ConversationID != created.ConversationID {
		t.Fatalf("reused draft id = %q, want %q", reused.ConversationID, created.ConversationID)
	}
	waitForPrewarmRequest(t, runner.prewarmed)

	if _, _, err := service.beginUserMessage(context.Background(), viewer, SendMessageCommand{
		ConversationID: created.ConversationID,
		Content:        "Plan the launch",
		IdempotencyKey: "message-1",
	}); err != nil {
		t.Fatal(err)
	}
	materialized := mustConversationView(t, repo, created.ConversationID)
	if materialized.Status != "running" || materialized.Title != "Plan the launch" || len(materialized.Messages) != 1 {
		t.Fatalf("materialized draft = %+v", materialized)
	}
	page, err = service.ListConversations(context.Background(), viewer, ListConversationsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 1 || len(page.Conversations) != 1 || page.Conversations[0].ConversationID != created.ConversationID {
		t.Fatalf("materialized draft missing from discussion list = %+v", page)
	}
}

func TestServicePrewarmKeepsPersonalAssistantDraftSeparateAndPrivate(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &prewarmingDiscussionAgentRunner{
		capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"},
		prewarmed:                      make(chan DiscussionAgentRequest, 3),
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-group", "conv-assistant", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindHuman}

	group, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{
		ConversationKind: ConversationKindGroup, Title: "New chat", IdempotencyKey: "prewarm-group",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{
		ConversationKind: ConversationKindPersonalAssistant, Title: "My assistant", IdempotencyKey: "prewarm-assistant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if group.ConversationID == assistant.ConversationID || assistant.ConversationKind != ConversationKindPersonalAssistant {
		t.Fatalf("group=%+v assistant=%+v", group, assistant)
	}
	record := mustConversationView(t, repo, assistant.ConversationID)
	if record.OwnerID != viewer.OwnerID {
		t.Fatalf("assistant owner = %q, want %q", record.OwnerID, viewer.OwnerID)
	}
	if record.MemberAdditionsAllowed == nil || *record.MemberAdditionsAllowed {
		t.Fatalf("assistant member policy = %+v", record.MemberAdditionsAllowed)
	}
	if len(record.Members) != 1 || record.Members[0].MemberKind != "user" || record.Members[0].MemberID != viewer.ViewerID {
		t.Fatalf("assistant members = %+v", record.Members)
	}

	reused, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{
		ConversationKind: ConversationKindPersonalAssistant, Title: "Another title", IdempotencyKey: "prewarm-assistant-again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused.ConversationID != assistant.ConversationID {
		t.Fatalf("assistant draft id = %q, want %q", reused.ConversationID, assistant.ConversationID)
	}

	if _, _, err := service.beginUserMessage(context.Background(), viewer, SendMessageCommand{
		ConversationID:   assistant.ConversationID,
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "Handle this while I am offline",
		IdempotencyKey:   "message-assistant",
	}); err != nil {
		t.Fatal(err)
	}
	materialized := mustConversationView(t, repo, assistant.ConversationID)
	if materialized.Status != "running" || materialized.Title != "My assistant" || materialized.ConversationKind != ConversationKindPersonalAssistant || len(materialized.Messages) != 1 {
		t.Fatalf("materialized assistant = %+v", materialized)
	}
}

func TestServiceSendMessageRunsPrivatePersonalAssistantWithoutAgentMember(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID: "lead-agent",
		response: DiscussionAgentResponse{
			AssistantMessage: "I will handle it.",
			Status:           "completed",
			RuntimeKind:      "pi",
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-assistant", "msg-user", "run-assistant", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindHuman}

	detail, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "Handle this while I am offline",
		IdempotencyKey:   "send-assistant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called || runner.request.ConversationKind != ConversationKindPersonalAssistant {
		t.Fatalf("assistant runtime request = %+v", runner.request)
	}
	if len(detail.Members) != 1 || detail.Members[0].Kind != "user" || detail.Members[0].ID != viewer.ViewerID {
		t.Fatalf("assistant members = %+v", detail.Members)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].AgentID != "lead-agent" || detail.Runs[0].AgentName != "Personal Assistant" {
		t.Fatalf("assistant runs = %+v", detail.Runs)
	}
	assistantID := PersonalAssistantAgentID("", viewer.OwnerID)
	if len(detail.Messages) != 2 || detail.Messages[1].AuthorID != assistantID || detail.Messages[1].AuthorName != "Personal Assistant" || detail.Messages[1].Content != "I will handle it." {
		t.Fatalf("assistant messages = %+v", detail.Messages)
	}
	stored := mustConversationView(t, repo, detail.ConversationID)
	if len(stored.Members) != 1 || stored.Members[0].MemberKind != "user" {
		t.Fatalf("persisted assistant members = %+v", stored.Members)
	}
}

func TestServicePrewarmSkipsDirectAndActiveDiscussions(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &prewarmingDiscussionAgentRunner{
		capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"},
		prewarmed:                      make(chan DiscussionAgentRequest, 1),
	}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindHuman}
	for _, conversation := range []ConversationRecord{
		{ConversationID: "conv-direct", ConversationKind: "direct", Title: "Direct", Status: "completed", CreatedBy: viewer.ViewerID, CreatedAt: testTime, UpdatedAt: testTime},
		{ConversationID: "conv-active", Title: "Active", Status: "running", ActiveTurnID: "turn-1", CreatedBy: viewer.ViewerID, CreatedAt: testTime, UpdatedAt: testTime},
	} {
		if err := repo.SaveConversation(context.Background(), conversation); err != nil {
			t.Fatal(err)
		}
		seedDiscussionMembers(t, repo, conversation.ConversationID, viewer.ViewerID)
		if _, err := service.PrewarmConversation(context.Background(), viewer, PrewarmConversationCommand{ConversationID: conversation.ConversationID}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case request := <-runner.prewarmed:
		t.Fatalf("unexpected prewarm request = %+v", request)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForPrewarmRequest(t *testing.T, requests <-chan DiscussionAgentRequest) DiscussionAgentRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for discussion prewarm")
		return DiscussionAgentRequest{}
	}
}
