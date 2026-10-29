package discussionsession

import (
	"context"
	"testing"
)

func TestSaveDiscussionTargetRunKeepsPAIdentityOnlyForLeadApprovalContinuation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:                   "conv-pa-mission",
		ConversationKind:                 ConversationKindGroup,
		TenantID:                         "tenant-a",
		OwnerID:                          "account-1",
		CreatedBy:                        "user-1",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
		Status:                           "running",
		CreatedAt:                        testTime,
		UpdatedAt:                        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-pa-mission", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "Lead", Role: "Lead", CreatedAt: testTime},
		{ConversationID: "conv-pa-mission", MemberKind: "agent", MemberID: "worker-agent", DisplayName: "Worker", Role: "Worker", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})

	lead, err := service.saveDiscussionTargetRun(ctx, RunRecord{
		RunID: "run-lead", ConversationID: "conv-pa-mission", Mode: discussionApprovalContinuationMode,
		AgentID: "lead-agent", AgentName: "Lead", Status: "running", CreatedAt: testTime, StartedAt: testTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lead.LogicalAgentID != PersonalAssistantAgentID("tenant-a", "account-1") || lead.AgentName != "Personal Assistant" {
		t.Fatalf("PA host approval run = %+v", lead)
	}

	worker, err := service.saveDiscussionTargetRun(ctx, RunRecord{
		RunID: "run-worker", ConversationID: "conv-pa-mission", Mode: discussionApprovalContinuationMode,
		AgentID: "worker-agent", AgentName: "Worker", Status: "running", CreatedAt: testTime, StartedAt: testTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if worker.LogicalAgentID != "" || worker.AgentName != "Worker" {
		t.Fatalf("worker approval run must retain physical identity: %+v", worker)
	}
}
