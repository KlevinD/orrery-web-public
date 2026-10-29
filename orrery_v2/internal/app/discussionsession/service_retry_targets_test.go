package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestServiceRetryDirectConversationRerunsTargetAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-direct",
		ConversationKind: "direct",
		Title:            "智员a",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedAt:        testTime,
		UpdatedAt:        testTime.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-direct", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "智员a", Role: "私聊对象", CreatedAt: testTime},
		{ConversationID: "conv-direct", MemberKind: "user", MemberID: "local-user", DisplayName: "Local User", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user", ConversationID: "conv-direct", Role: "user", AuthorID: "local-user", AuthorName: "Local User", Content: "你会啥？", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-old", ConversationID: "conv-direct", AgentID: "staffing-agent", Status: "failed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-old-reply", ConversationID: "conv-direct", Role: "assistant", AuthorID: "staffing-agent", AuthorName: "智员a", Content: "旧回复", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run", Status: "completed", RuntimeKind: "managed"}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "我是智员a，重试后继续。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime.Add(4 * time.Minute)},
		IDGenerator:      fixedIDGen("event-retry", "run-direct-retry", "msg-direct-retry"),
	})

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RetryMessageCommand{
		ConversationID: "conv-direct",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-direct-msg",
	})
	if err != nil {
		t.Fatal(err)
	}

	if lead.called {
		t.Fatalf("lead discussion agent should not run for direct retry: %+v", lead.request)
	}
	if !responder.called || responder.request.Mode != "direct" || responder.request.TargetAgent.ID != "staffing-agent" || responder.request.RunID != "run-direct-retry" {
		t.Fatalf("direct retry responder request = %+v", responder.request)
	}
	if !responder.request.ForceNewSession {
		t.Fatalf("direct retry must start a fresh runtime session: %+v", responder.request)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-direct-retry" || dto.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("direct retry runs = %+v", dto.Runs)
	}
	if len(dto.Messages) != 2 || dto.Messages[0].MessageID != "msg-user" || dto.Messages[1].MessageID != "msg-direct-retry" || dto.Messages[1].AuthorID != "staffing-agent" {
		t.Fatalf("direct retry messages = %+v", dto.Messages)
	}
	if dto.DirectTargetKind != "agent" || dto.DirectTargetID != "staffing-agent" || dto.DirectAgentID != "staffing-agent" {
		t.Fatalf("direct retry metadata = kind %q target %q agent %q", dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	debug, err := service.GetLatestRunDebug(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, "conv-direct")
	if err != nil {
		t.Fatal(err)
	}
	if debug.Selected.AgentID != "staffing-agent" {
		t.Fatalf("direct retry debug selected agent = %+v", debug.Selected)
	}
}

func TestServiceRetryMessageStreamDirectConversationRerunsTargetAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-direct",
		ConversationKind: "direct",
		DirectTargetKind: "agent",
		DirectTargetID:   "staffing-agent",
		DirectAgentID:    "staffing-agent",
		Title:            "智员a",
		Status:           "failed",
		CreatedBy:        "local-user",
		CreatedAt:        testTime,
		UpdatedAt:        testTime.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-direct", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "智员a", Role: "私聊对象", CreatedAt: testTime},
		{ConversationID: "conv-direct", MemberKind: "user", MemberID: "local-user", DisplayName: "Local User", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user", ConversationID: "conv-direct", Role: "user", AuthorID: "local-user", AuthorName: "Local User", Content: "你能干啥？", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-old", ConversationID: "conv-direct", AgentID: "staffing-agent", Status: "failed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-old-reply", ConversationID: "conv-direct", Role: "assistant", AuthorID: "staffing-agent", AuthorName: "智员a", Content: "旧失败回复", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	lead := &streamingDiscussionAgentRunner{
		agentID: "lead-agent",
		events: []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "lead should not stream",
			Status:           "completed",
			RuntimeKind:      "managed",
		}}},
	}
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
		AssistantMessage: "我是智员a，流式重试后继续。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime.Add(4 * time.Minute)},
		IDGenerator:      fixedIDGen("event-retry", "run-direct-stream", "msg-direct-stream"),
	})

	events, err := service.RetryMessageStream(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RetryMessageCommand{
		ConversationID: "conv-direct",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-direct-stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	var finalSnapshot *ConversationDTO
	for event := range events {
		switch event.Kind {
		case StreamEventSnapshot:
			kinds = append(kinds, "snapshot")
			finalSnapshot = event.Conversation
		case StreamEventRunStarted:
			if event.Run != nil {
				kinds = append(kinds, "run_started:"+event.Run.AgentID)
			}
		case StreamEventMessageCompleted:
			if event.Message != nil {
				kinds = append(kinds, "message:"+event.Message.AuthorID)
			}
		case StreamEventRunCompleted:
			if event.Run != nil {
				kinds = append(kinds, "run_completed:"+event.Run.AgentID)
			}
		case StreamEventError:
			t.Fatalf("direct retry stream error event: %s", event.Error)
		}
	}

	if lead.request.RunID != "" {
		t.Fatalf("lead stream agent should not run for direct retry: %+v", lead.request)
	}
	if !responder.streamCalled || responder.request.Mode != "direct" || responder.request.TargetAgent.ID != "staffing-agent" || responder.request.RunID != "run-direct-stream" {
		t.Fatalf("direct retry stream responder request = %+v", responder.request)
	}
	want := []string{"snapshot", "run_started:staffing-agent", "message:staffing-agent", "run_completed:staffing-agent", "snapshot"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("direct retry stream lifecycle = %v, want %v", kinds, want)
	}
	if finalSnapshot == nil || len(finalSnapshot.Runs) != 1 || finalSnapshot.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("direct retry stream final snapshot = %+v", finalSnapshot)
	}
}

func TestServiceRetryHumanAuthoredMentionRerunsMentionTarget(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-mention", Title: "@ Staffing", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-mention", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user", ConversationID: "conv-mention", Role: "user", AuthorID: "user-1", AuthorName: "本地用户", Content: "[@秦若兰](mention://agent/staffing-agent) 你看下", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-old", ConversationID: "conv-mention", AgentID: "staffing-agent", Status: "failed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-old-reply", ConversationID: "conv-mention", Role: "assistant", AuthorID: "staffing-agent", AuthorName: "秦若兰", Content: "旧回复", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run", Status: "completed", RuntimeKind: "managed"}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 我看完了。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime.Add(4 * time.Minute)},
		IDGenerator:      fixedIDGen("event-retry", "run-mention-retry", "msg-mention-retry"),
	})

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, RetryMessageCommand{
		ConversationID: "conv-mention",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-mention-msg",
	})
	if err != nil {
		t.Fatal(err)
	}

	if lead.called {
		t.Fatalf("lead discussion agent should not run for direct mention retry: %+v", lead.request)
	}
	if !responder.called || responder.request.Mode != "mention" || responder.request.TargetAgent.ID != "staffing-agent" || responder.request.SourceMessage.MessageID != "msg-user" {
		t.Fatalf("mention retry responder request = %+v", responder.request)
	}
	if !responder.request.ForceNewSession {
		t.Fatalf("mention retry must start a fresh runtime session: %+v", responder.request)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-mention-retry" || dto.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("mention retry runs = %+v", dto.Runs)
	}
	if len(dto.Messages) != 2 || dto.Messages[0].MessageID != "msg-user" || dto.Messages[1].AuthorID != "staffing-agent" {
		t.Fatalf("mention retry messages = %+v", dto.Messages)
	}
}
