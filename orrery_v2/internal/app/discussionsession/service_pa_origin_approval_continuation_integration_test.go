package discussionsession

import (
	"context"
	"testing"
)

func TestServicePAOriginGroupLeadBlockedApprovalContinuationUsesPersonalAssistantIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPAOriginApprovalConversation(t, repo, "conv-pa-lead-approval")

	logicalAgentID := PersonalAssistantAgentID("tenant-a", "account-1")
	runner := &capturingDiscussionAgentRunner{
		agentID:  "lead-agent",
		response: blockedApprovalResponse("cmd-pa-lead", "session-pa-lead", "Lead needs approval."),
	}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Lead resumed after approval.",
		Status:           "completed",
		RuntimeKind:      "codex",
		SessionID:        "session-pa-lead",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("msg-root", "run-root", "msg-root-blocked", "run-root-resume", "msg-root-resumed"),
	})
	viewer := paOriginApprovalViewer()

	blocked, err := service.SendMessage(ctx, viewer, SendMessageCommand{
		ConversationID: "conv-pa-lead-approval",
		Content:        "Start the mission.",
		IdempotencyKey: "send-pa-lead-blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := requirePAApprovalRun(t, blocked, "run-root"); got.LogicalAgentID != logicalAgentID || got.AgentName != "Personal Assistant" || got.Status != "blocked" {
		t.Fatalf("blocked Lead run = %+v", got)
	}
	if got := requirePAApprovalMessage(t, blocked, "run-root"); got.AuthorID != logicalAgentID || got.AuthorName != "Personal Assistant" {
		t.Fatalf("blocked Lead message = %+v", got)
	}

	resumed, err := service.DecideRuntimeApproval(ctx, viewer, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-pa-lead-approval",
		ApprovalID:     "cmd-pa-lead",
		ActionID:       "approve_session",
		Remember:       true,
		IdempotencyKey: "approve-pa-lead",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(responder.requests) != 1 {
		t.Fatalf("approval continuation requests = %d, want 1", len(responder.requests))
	}
	request := responder.requests[0]
	if !request.PersonalAssistantHost || request.LogicalAgentID != logicalAgentID || request.TargetAgent.ID != "lead-agent" {
		t.Fatalf("Lead approval continuation request = %+v", request)
	}
	if request.PersonalAssistantOriginOwnerID != "account-1" || request.PersonalAssistantOriginSubjectID != "user-1" || request.ConversationKind != ConversationKindGroup {
		t.Fatalf("Lead approval PA provenance = %+v", request)
	}
	runtimeRequest := discussionAgentRequestFromMentionRequest(request)
	if !IsPersonalAssistantRuntimeRequest(&runtimeRequest) || runtimeRequest.LogicalAgentID != logicalAgentID || runtimeRequest.Viewer.ViewerID != "user-1" || runtimeRequest.Viewer.OwnerID != "account-1" || runtimeRequest.Viewer.ViewerKind != ViewerKindUser {
		t.Fatalf("Lead approval runtime request identity = %+v", runtimeRequest)
	}

	if got := requirePAApprovalRun(t, resumed, "run-root-resume"); got.AgentID != "lead-agent" || got.LogicalAgentID != logicalAgentID || got.AgentName != "Personal Assistant" || got.Status != "completed" {
		t.Fatalf("resumed Lead run = %+v", got)
	}
	if got := requirePAApprovalMessage(t, resumed, "run-root-resume"); got.AuthorID != logicalAgentID || got.AuthorName != "Personal Assistant" || got.Content != "Lead resumed after approval." {
		t.Fatalf("resumed Lead message = %+v", got)
	}
}

func TestServicePAOriginGroupWorkerBlockedApprovalContinuationKeepsPhysicalIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPAOriginApprovalConversation(t, repo, "conv-pa-worker-approval")

	responder := &capturingDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		blockedApprovalResponse("cmd-pa-worker", "session-pa-worker", "Worker needs approval."),
		{
			AssistantMessage: "Worker resumed after approval.",
			Status:           "completed",
			RuntimeKind:      "codex",
			SessionID:        "session-pa-worker",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent"},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("msg-worker", "run-worker", "msg-worker-blocked", "run-worker-resume", "msg-worker-resumed"),
	})
	viewer := paOriginApprovalViewer()

	blocked, err := service.SendMessage(ctx, viewer, SendMessageCommand{
		ConversationID: "conv-pa-worker-approval",
		Content:        "Please ask the worker.",
		Mentions: []MessageMentionDTO{{
			TargetKind:  "agent",
			TargetID:    "staffing-agent",
			DisplayName: "Worker Agent",
		}},
		IdempotencyKey: "send-pa-worker-blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := requirePAApprovalRun(t, blocked, "run-worker"); got.AgentID != "staffing-agent" || got.LogicalAgentID != "" || got.AgentName != "Worker Agent" || got.Status != "blocked" {
		t.Fatalf("blocked worker run = %+v", got)
	}
	if got := requirePAApprovalMessage(t, blocked, "run-worker"); got.AuthorID != "staffing-agent" || got.AuthorName != "Worker Agent" {
		t.Fatalf("blocked worker message = %+v", got)
	}

	resumed, err := service.DecideRuntimeApproval(ctx, viewer, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-pa-worker-approval",
		ApprovalID:     "cmd-pa-worker",
		ActionID:       "approve_session",
		Remember:       true,
		IdempotencyKey: "approve-pa-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(responder.requests) != 2 {
		t.Fatalf("worker requests = %d, want 2", len(responder.requests))
	}
	continuation := responder.requests[1]
	if continuation.PersonalAssistantHost || continuation.LogicalAgentID != "" || continuation.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("worker approval continuation request inherited PA identity: %+v", continuation)
	}
	runtimeRequest := discussionAgentRequestFromMentionRequest(continuation)
	if IsPersonalAssistantRuntimeRequest(&runtimeRequest) || runtimeRequest.LogicalAgentID != "" || runtimeRequest.PersonalAssistantOriginOwnerID != "" || runtimeRequest.PersonalAssistantOriginSubjectID != "" {
		t.Fatalf("worker approval runtime request inherited PA identity: %+v", runtimeRequest)
	}

	if got := requirePAApprovalRun(t, resumed, "run-worker-resume"); got.AgentID != "staffing-agent" || got.LogicalAgentID != "" || got.AgentName != "Worker Agent" || got.Status != "completed" {
		t.Fatalf("resumed worker run = %+v", got)
	}
	if got := requirePAApprovalMessage(t, resumed, "run-worker-resume"); got.AuthorID != "staffing-agent" || got.AuthorName != "Worker Agent" || got.Content != "Worker resumed after approval." {
		t.Fatalf("resumed worker message = %+v", got)
	}
}

func seedPAOriginApprovalConversation(t *testing.T, repo *Repository, conversationID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:                   conversationID,
		ConversationKind:                 ConversationKindGroup,
		TenantID:                         "tenant-a",
		OwnerID:                          "account-1",
		CreatedBy:                        "user-1",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
		Status:                           "completed",
		CreatedAt:                        testTime,
		UpdatedAt:                        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: conversationID, MemberKind: "user", MemberID: "user-1", DisplayName: "User", Role: "Participant", CreatedAt: testTime},
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "lead-agent", DisplayName: "Lead Agent", Role: "Lead", CreatedAt: testTime},
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Worker Agent", Role: "Staffing", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
}

func blockedApprovalResponse(requestID string, sessionID string, message string) DiscussionAgentResponse {
	return DiscussionAgentResponse{
		AssistantMessage: message,
		Status:           "blocked",
		RuntimeKind:      "codex",
		SessionID:        sessionID,
		ProviderMetadata: map[string]string{
			"approval_source":     "codex",
			"approval_request_id": requestID,
			"approval_kind":       "command_execution",
			"approval_type":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_session_id": sessionID,
			"approval_turn_id":    "turn-approval",
		},
	}
}

func paOriginApprovalViewer() ViewerRef {
	return ViewerRef{
		ViewerID:    "user-1",
		OwnerID:     "account-1",
		ViewerKind:  ViewerKindUser,
		DisplayName: "User",
		TenantID:    "tenant-a",
	}
}

func requirePAApprovalRun(t *testing.T, conversation ConversationDTO, runID string) RunDTO {
	t.Helper()
	for _, run := range conversation.Runs {
		if run.RunID == runID {
			return run
		}
	}
	t.Fatalf("run %q not found: %+v", runID, conversation.Runs)
	return RunDTO{}
}

func requirePAApprovalMessage(t *testing.T, conversation ConversationDTO, runID string) MessageDTO {
	t.Helper()
	for _, message := range conversation.Messages {
		if message.RunID == runID {
			return message
		}
	}
	t.Fatalf("assistant message for run %q not found: %+v", runID, conversation.Messages)
	return MessageDTO{}
}
