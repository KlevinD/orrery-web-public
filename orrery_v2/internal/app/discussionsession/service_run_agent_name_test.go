package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestMapConversationDTOResolvesRunAgentNameFromMembers(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-1"},
		Members: []ConversationMemberRecord{{
			ConversationID: "conv-1",
			MemberKind:     "agent",
			MemberID:       "profile-f54c4401bfb058eadd0e48a5a878b4e4",
			DisplayName:    "Codex",
		}},
		Runs: []RunRecord{{
			RunID:          "run-1",
			ConversationID: "conv-1",
			AgentID:        "profile-f54c4401bfb058eadd0e48a5a878b4e4",
			Status:         "running",
		}},
	}

	dto := mapConversationDTO(view)
	if len(dto.Runs) != 1 || dto.Runs[0].AgentName != "Codex" {
		t.Fatalf("run agent name = %+v, want Codex", dto.Runs)
	}
}

func TestMapRunsPrefersPersistedAgentName(t *testing.T) {
	runs := mapRuns([]RunRecord{{
		RunID:     "run-1",
		AgentID:   "profile-f54c4401bfb058eadd0e48a5a878b4e4",
		AgentName: "Codex",
	}})

	if len(runs) != 1 || runs[0].AgentName != "Codex" {
		t.Fatalf("run agent name = %+v, want Codex", runs)
	}
}

func TestPersonalAssistantRunNameDoesNotLeakManagedAgentIdentity(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID:   "conv-assistant",
			ConversationKind: ConversationKindPersonalAssistant,
			Title:            "我的助理",
		},
		Members: []ConversationMemberRecord{{
			ConversationID: "conv-assistant",
			MemberKind:     "agent",
			MemberID:       "lead-agent",
			DisplayName:    "顾明澈",
		}},
	}
	run := RunRecord{AgentID: "lead-agent", AgentName: "顾明澈"}

	if got := conversationRunAgentName(view, run); got != "我的助理" {
		t.Fatalf("personal assistant run name = %q, want 我的助理", got)
	}
}

func TestPersonalAssistantOriginGroupHostDoesNotLeakManagedAgentIdentity(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		ConversationID:                   "conv-mission-pa",
		TenantID:                         "tenant-a",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "subject-1",
	}}
	run := RunRecord{
		AgentID:        "lead-agent",
		LogicalAgentID: PersonalAssistantAgentID("tenant-a", "account-1"),
		Mode:           string(discussionTurnTargetGroup),
		AgentName:      "顾明澈",
	}
	if got := conversationRunAgentName(view, run); got != "Personal Assistant" {
		t.Fatalf("PA-origin group host name = %q, want Personal Assistant", got)
	}
	authorID, authorName, err := requiredConversationRunMessageAuthor(view, run)
	if err != nil {
		t.Fatalf("requiredConversationRunMessageAuthor returned error: %v", err)
	}
	if authorID != run.LogicalAgentID || authorName != "Personal Assistant" {
		t.Fatalf("PA-origin group author = %q/%q", authorID, authorName)
	}
	worker := run
	worker.Mode = "mention"
	if got := conversationRunAgentName(view, worker); got == "Personal Assistant" {
		t.Fatalf("worker mention inherited PA display identity: %q", got)
	}
}

func TestCreateDiscussionRunPersistsAgentName(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	at := time.Date(2026, 7, 22, 4, 53, 0, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Status: "running", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "profile-f54c4401bfb058eadd0e48a5a878b4e4", DisplayName: "Codex", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo})

	run, err := service.createDiscussionRun(ctx, "conv-1", "profile-f54c4401bfb058eadd0e48a5a878b4e4", at)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := repo.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentName != "Codex" || persisted.AgentName != "Codex" {
		t.Fatalf("run names = returned %q, persisted %q; want Codex", run.AgentName, persisted.AgentName)
	}
}

func TestRealtimeRunEventsIncludeAgentName(t *testing.T) {
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{RealtimePublisher: realtime})
	run := RunRecord{RunID: "run-1", ConversationID: "conv-1", AgentID: "profile-f54c4401bfb058eadd0e48a5a878b4e4", LogicalAgentID: "pa-tenant-a-account-1", AgentName: "Codex", Status: "running"}

	if err := service.appendRealtimeRunStarted(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	events := realtime.eventsByType(RealtimeEventDiscussionRunStarted)
	if len(events) != 1 || realtimePayloadString(t, events[0], "agent_name") != "Codex" || realtimePayloadString(t, events[0], "logical_agent_id") != "pa-tenant-a-account-1" {
		t.Fatalf("run started events = %+v, want agent_name Codex and logical_agent_id pa-tenant-a-account-1", events)
	}
}

func TestRealtimeRunTerminalEventsIncludeLogicalAgentID(t *testing.T) {
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{RealtimePublisher: realtime})
	run := RunRecord{
		RunID:          "run-1",
		ConversationID: "conv-1",
		AgentID:        "lead-agent",
		LogicalAgentID: "pa-tenant-a-account-1",
		AgentName:      "Personal Assistant",
		Status:         "completed",
	}

	if err := service.appendRealtimeRunCompleted(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Status = "failed"
	if err := service.appendRealtimeRunFailed(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{RealtimeEventDiscussionRunCompleted, RealtimeEventDiscussionRunFailed} {
		events := realtime.eventsByType(eventType)
		if len(events) != 1 || realtimePayloadString(t, events[0], "logical_agent_id") != "pa-tenant-a-account-1" {
			t.Fatalf("%s events = %+v, want logical_agent_id pa-tenant-a-account-1", eventType, events)
		}
	}
}
