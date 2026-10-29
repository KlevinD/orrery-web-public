package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceListApprovalConversationsReturnsVisibleRuntimeApprovalData(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	ctx := context.Background()
	for _, conversation := range []ConversationRecord{
		{ConversationID: "conv-visible", Title: "Visible", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(time.Minute)},
		{ConversationID: "conv-hidden", Title: "Hidden", Status: "completed", CreatedBy: "user-2", CreatedAt: testTime, UpdatedAt: testTime},
	} {
		if err := repo.SaveConversation(ctx, conversation); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: "run-visible", ConversationID: "conv-visible", AgentID: "lead-agent", AgentName: "Lead",
		Status: "completed", RuntimeKind: "codex", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-visible", ConversationID: "conv-visible", Kind: discussionRuntimeApprovalEventKind,
		Status: "approved", Metadata: map[string]string{"approval_run_id": "run-visible", "approval_tool_name": "exec_command"}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "non-approval-visible", ConversationID: "conv-visible", Kind: "message_retry_requested", Status: "completed", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-hidden", ConversationID: "conv-hidden", Kind: discussionRuntimeApprovalEventKind, Status: "pending", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{
		ConversationID: "conv-visible", MissionID: "mission-1", Title: "Mission", Status: "active", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	type approvalConversationLister interface {
		ListApprovalConversations(context.Context, ViewerRef) ([]ConversationDTO, error)
	}
	lister, ok := any(service).(approvalConversationLister)
	if !ok {
		t.Fatal("discussion service does not implement the approval batch query")
	}
	conversations, err := lister.ListApprovalConversations(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser})
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 1 || conversations[0].ConversationID != "conv-visible" {
		t.Fatalf("approval conversations = %+v", conversations)
	}
	conversation := conversations[0]
	if len(conversation.Events) != 1 || conversation.Events[0].EventID != "approval-visible" {
		t.Fatalf("approval events = %+v", conversation.Events)
	}
	if len(conversation.Runs) != 1 || conversation.Runs[0].RunID != "run-visible" {
		t.Fatalf("approval runs = %+v", conversation.Runs)
	}
	if len(conversation.LinkedMissions) != 1 || conversation.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("approval mission links = %+v", conversation.LinkedMissions)
	}
}
