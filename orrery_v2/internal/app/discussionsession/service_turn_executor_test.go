package discussionsession

import "testing"

func TestDiscussionTargetRequestFromRunBuildsModeSpecificRequest(t *testing.T) {
	source := MessageRecord{
		MessageID:      "msg-user",
		ConversationID: "conv-1",
		Role:           "user",
		AuthorID:       "user-1",
		AuthorName:     "本地用户",
		Content:        "[@秦若兰](mention://agent/staffing-agent) 你看下",
		CreatedAt:      testTime,
	}
	target := ConversationMemberRecord{
		ConversationID: "conv-1",
		MemberKind:     "agent",
		MemberID:       "staffing-agent",
		DisplayName:    "秦若兰",
		Role:           "Staffing",
		CreatedAt:      testTime,
	}
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-1", ConversationKind: "group"},
		Messages: []MessageRecord{
			source,
			{MessageID: "msg-old", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "收到", CreatedAt: testTime.Add(-1)},
		},
		Members: []ConversationMemberRecord{
			target,
			{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime},
		},
		LinkedMissions: []LinkedMissionRecord{{ConversationID: "conv-1", MissionID: "mission-1", Title: "任务", Status: "active", CreatedAt: testTime}},
		Attachments:    []AttachmentRecord{{AttachmentID: "att-1", ConversationID: "conv-1", MessageID: "msg-user", FileName: "brief.md", ContentType: "text/markdown", CreatedAt: testTime}},
	}
	run := RunRecord{RunID: "run-1", ConversationID: "conv-1", AgentID: "staffing-agent"}

	direct := discussionTargetRequestFromRun("direct", view, source, target, run)
	mention := discussionTargetRequestFromRun("mention", view, source, target, run)

	for _, request := range []DiscussionAgentMentionRequest{direct, mention} {
		if request.ConversationID != "conv-1" || request.RunID != "run-1" {
			t.Fatalf("request identity = %+v", request)
		}
		if request.Content != source.Content || request.SourceMessage.MessageID != source.MessageID {
			t.Fatalf("request source = %+v", request)
		}
		if request.TargetAgent.ID != "staffing-agent" || request.TargetAgent.DisplayName != "秦若兰" {
			t.Fatalf("request target = %+v", request.TargetAgent)
		}
		if len(request.History) != 2 || len(request.Members) != 2 || len(request.LinkedMissions) != 1 || len(request.Attachments) != 1 {
			t.Fatalf("request context counts history=%d members=%d missions=%d attachments=%d", len(request.History), len(request.Members), len(request.LinkedMissions), len(request.Attachments))
		}
	}
	if direct.Mode != "direct" || mention.Mode != "mention" {
		t.Fatalf("request modes direct=%q mention=%q", direct.Mode, mention.Mode)
	}
}

func TestDiscussionTargetRequestFromRunBuildsApprovalContinuationBase(t *testing.T) {
	source := MessageRecord{MessageID: "msg-user", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "批准", CreatedAt: testTime}
	target := ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰", CreatedAt: testTime}
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-1", ConversationKind: "direct"},
		Messages:           []MessageRecord{source},
		Members:            []ConversationMemberRecord{target, {ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}},
		Attachments:        []AttachmentRecord{{AttachmentID: "att-1", ConversationID: "conv-1", MessageID: "msg-user", FileName: "approval.json", CreatedAt: testTime}},
	}
	run := RunRecord{RunID: "run-resume", ConversationID: "conv-1", AgentID: "staffing-agent"}

	request := discussionTargetRequestFromRun(discussionApprovalContinuationMode, view, source, target, run)

	if request.Mode != discussionApprovalContinuationMode || request.RunID != "run-resume" {
		t.Fatalf("approval request identity = %+v", request)
	}
	if request.SourceMessage.MessageID != "msg-user" || request.TargetAgent.ID != "staffing-agent" || len(request.Attachments) != 1 {
		t.Fatalf("approval request context = %+v", request)
	}
}

func TestDiscussionTargetRequestFromRunMarksOnlyPAOriginLeadApprovalContinuation(t *testing.T) {
	source := MessageRecord{MessageID: "msg-user", ConversationID: "conv-pa", Role: "user", AuthorID: "user-1", Content: "approve", CreatedAt: testTime}
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		ConversationID:                   "conv-pa",
		ConversationKind:                 ConversationKindGroup,
		TenantID:                         "tenant-a",
		OwnerID:                          "account-1",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
	}}
	lead := ConversationMemberRecord{ConversationID: "conv-pa", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "Lead", Role: "Lead"}
	worker := ConversationMemberRecord{ConversationID: "conv-pa", MemberKind: "agent", MemberID: "worker-agent", DisplayName: "Worker", Role: "Worker"}

	hostRequest := discussionTargetRequestFromRun(discussionApprovalContinuationMode, view, source, lead, RunRecord{RunID: "run-lead", AgentID: "lead-agent"})
	if !hostRequest.PersonalAssistantHost || hostRequest.LogicalAgentID != PersonalAssistantAgentID("tenant-a", "account-1") {
		t.Fatalf("PA-origin Lead approval request = %+v", hostRequest)
	}
	if hostRequest.PersonalAssistantOriginSubjectID != "user-1" || hostRequest.ConversationOwnerID != "account-1" {
		t.Fatalf("PA-origin Lead context = %+v", hostRequest)
	}

	for _, request := range []DiscussionAgentMentionRequest{
		discussionTargetRequestFromRun("mention", view, source, lead, RunRecord{RunID: "run-mention", AgentID: "lead-agent"}),
		discussionTargetRequestFromRun(discussionApprovalContinuationMode, view, source, worker, RunRecord{RunID: "run-worker", AgentID: "worker-agent"}),
	} {
		if request.PersonalAssistantHost || request.LogicalAgentID != "" {
			t.Fatalf("worker or ordinary mention must retain physical identity: %+v", request)
		}
	}
}
