package discussionsession

import "testing"

func TestDiscussionAgentRequestFromConversationCarriesRunsTriggerMessageAndCurrentAttachments(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID:   "conv-1",
			ConversationKind: ConversationKindPersonalAssistant,
			TenantID:         "tenant-a",
			CreatedBy:        "human-1",
			OwnerID:          "account-1",
			Title:            "Launch",
		},
		Runs: []RunRecord{{
			RunID:       "run-prior",
			AgentID:     "lead-agent",
			Status:      "completed",
			RuntimeKind: "codex",
			SessionID:   "thread-1",
			CreatedAt:   testTime,
		}},
		Attachments: []AttachmentRecord{{
			AttachmentID:   "att-1",
			ConversationID: "conv-1",
			MessageID:      "msg-current",
			FileName:       "brief.txt",
			StoragePath:    "D:/data/discussions/conv-1/attachments/att-1-brief.txt",
			CreatedAt:      testTime,
		}, {
			AttachmentID:   "att-old",
			ConversationID: "conv-1",
			MessageID:      "msg-old",
			FileName:       "old.txt",
			StoragePath:    "D:/data/discussions/conv-1/attachments/att-old-old.txt",
			CreatedAt:      testTime,
		}},
	}
	run := RunRecord{RunID: "run-current", ConversationID: "conv-1", AgentID: "lead-agent"}
	message := MessageRecord{
		MessageID:  "msg-current",
		Role:       "assistant",
		AuthorID:   "staffing-agent",
		AuthorName: "秦若兰",
		Content:    "请负责人继续推进",
		CreatedAt:  testTime.AddDate(0, 0, 1),
	}

	request := discussionAgentRequestFromConversation(view, run, ViewerRef{ViewerID: "human-1", DisplayName: "本地用户", Language: "zh-CN"}, message, nil)

	if len(request.Runs) != 1 || request.Runs[0].SessionID != "thread-1" {
		t.Fatalf("request runs = %+v", request.Runs)
	}
	if request.ConversationKind != ConversationKindPersonalAssistant || request.ConversationOwnerID != "account-1" || request.ConversationTenantID != "tenant-a" {
		t.Fatalf("conversation identity = %+v", request)
	}
	if request.OutputLanguage != "zh-CN" {
		t.Fatalf("output language = %q", request.OutputLanguage)
	}
	if request.LogicalAgentID != PersonalAssistantAgentID("tenant-a", "account-1") {
		t.Fatalf("logical personal assistant id = %q", request.LogicalAgentID)
	}
	if request.TriggerMessage.MessageID != "msg-current" ||
		request.TriggerMessage.AuthorID != "staffing-agent" ||
		request.TriggerMessage.Role != "assistant" ||
		request.TriggerMessage.Content != "请负责人继续推进" {
		t.Fatalf("trigger message = %+v", request.TriggerMessage)
	}
	if len(request.Attachments) != 1 || request.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("attachments = %+v", request.Attachments)
	}
	if request.Attachments[0].MessageID != "msg-current" {
		t.Fatalf("attachment should keep message binding: %+v", request.Attachments[0])
	}
	if len(request.TriggerMessage.Attachments) != 1 || request.TriggerMessage.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("trigger message attachments = %+v", request.TriggerMessage.Attachments)
	}
}

func TestDiscussionAgentRequestFromMentionRequestUsesSourceAsTrigger(t *testing.T) {
	mention := DiscussionAgentMentionRequest{
		ConversationID: "conv-1",
		RunID:          "run-mention",
		Mode:           "mention",
		OutputLanguage: "en-US",
		Content:        "[@顾明澈](mention://agent/lead-agent) 请接手",
		SourceMessage: MessageDTO{
			MessageID:  "msg-source",
			Role:       "assistant",
			AuthorID:   "staffing-agent",
			AuthorName: "秦若兰",
			Content:    "[@顾明澈](mention://agent/lead-agent) 请接手",
			CreatedAt:  testTime.AddDate(0, 0, 1),
		},
		TargetAgent: ConversationMemberDTO{
			Kind:        "agent",
			ID:          "lead-agent",
			DisplayName: "顾明澈",
		},
		History: []MessageDTO{{
			MessageID: "msg-old",
			Role:      "user",
			AuthorID:  "user-1",
			Content:   "先做准备",
			CreatedAt: testTime,
		}, {
			MessageID: "msg-source",
			Role:      "assistant",
			AuthorID:  "staffing-agent",
			Content:   "[@顾明澈](mention://agent/lead-agent) 请接手",
			CreatedAt: testTime.AddDate(0, 0, 1),
		}},
		Attachments: []AttachmentDTO{{
			AttachmentID:   "att-1",
			ConversationID: "conv-1",
			FileName:       "brief.txt",
		}},
		ForceNewSession: true,
	}

	request := discussionAgentRequestFromMentionRequest(mention)

	if request.AgentID != "lead-agent" || request.MessageID != "msg-source" || request.Mode != "mention" {
		t.Fatalf("request identity = %+v", request)
	}
	if request.OutputLanguage != "en-US" {
		t.Fatalf("output language = %q", request.OutputLanguage)
	}
	if !request.ForceNewSession {
		t.Fatalf("fresh-session directive was not propagated: %+v", request)
	}
	if request.TriggerMessage.MessageID != "msg-source" ||
		request.TriggerMessage.AuthorID != "staffing-agent" ||
		request.TriggerMessage.Role != "assistant" {
		t.Fatalf("trigger message = %+v", request.TriggerMessage)
	}
	if len(request.History) != 1 || request.History[0].MessageID != "msg-old" {
		t.Fatalf("history should exclude source/current message: %+v", request.History)
	}
	if len(request.Attachments) != 1 || request.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("attachments = %+v", request.Attachments)
	}
}

func TestDiscussionAgentRequestFromMentionRequestKeepsPAHostIdentity(t *testing.T) {
	logicalID := PersonalAssistantAgentID("tenant-a", "account-1")
	request := discussionAgentRequestFromMentionRequest(DiscussionAgentMentionRequest{
		ConversationID:                   "conv-pa",
		ConversationKind:                 ConversationKindGroup,
		ConversationOwnerID:              "account-1",
		ConversationTenantID:             "tenant-a",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
		PersonalAssistantHost:            true,
		LogicalAgentID:                   logicalID,
		RunID:                            "run-pa",
		Mode:                             discussionApprovalContinuationMode,
		SourceMessage:                    MessageDTO{MessageID: "msg-user", AuthorID: "user-1", AuthorName: "User", Content: "approve"},
		TargetAgent:                      ConversationMemberDTO{Kind: "agent", ID: "lead-agent", DisplayName: "Lead", Role: "Lead"},
	})
	if request.AgentID != "lead-agent" || request.LogicalAgentID != logicalID || request.ConversationKind != ConversationKindGroup {
		t.Fatalf("PA host request identity = %+v", request)
	}
	if request.Viewer.ViewerKind != ViewerKindUser || request.Viewer.ViewerID != "user-1" || request.Viewer.AgentProfileID != "user-1" || request.Viewer.OwnerID != "account-1" || request.Viewer.TenantID != "tenant-a" {
		t.Fatalf("PA host principal = %+v", request.Viewer)
	}

	worker := discussionAgentRequestFromMentionRequest(DiscussionAgentMentionRequest{
		ConversationID:                   "conv-pa",
		ConversationKind:                 ConversationKindGroup,
		ConversationOwnerID:              "account-1",
		ConversationTenantID:             "tenant-a",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
		LogicalAgentID:                   logicalID,
		RunID:                            "run-worker",
		Mode:                             "mention",
		SourceMessage:                    MessageDTO{MessageID: "msg-worker", AuthorID: "lead-agent", Content: "delegate"},
		TargetAgent:                      ConversationMemberDTO{Kind: "agent", ID: "worker-agent", DisplayName: "Worker", Role: "Worker"},
	})
	if worker.AgentID != "worker-agent" || worker.LogicalAgentID != "" || worker.PersonalAssistantOriginOwnerID != "" || worker.PersonalAssistantOriginSubjectID != "" {
		t.Fatalf("worker must not inherit PA runtime identity: %+v", worker)
	}
}

func TestDiscussionAgentRequestHistoryExcludesToolCallAndResultRecords(t *testing.T) {
	records := []MessageRecord{{
		MessageID: "msg-user",
		Role:      "user",
		AuthorID:  "user-1",
		Content:   "先做准备",
		CreatedAt: testTime,
	}, {
		MessageID: "msg-tool-call",
		Role:      "assistant",
		AuthorID:  "lead-agent",
		Content:   `{"name":"mission_prepare","arguments":{"title":"Launch"}}`,
		Metadata:  map[string]string{"kind": "tool_call"},
		CreatedAt: testTime.AddDate(0, 0, 1),
	}, {
		MessageID: "msg-tool-result",
		Role:      "tool",
		AuthorID:  "mission_prepare",
		Content:   `{"mission_id":"mission-1"}`,
		Metadata:  map[string]string{"kind": "tool_result"},
		CreatedAt: testTime.AddDate(0, 0, 2),
	}, {
		MessageID: "msg-source",
		Role:      "user",
		AuthorID:  "user-1",
		Content:   "继续",
		CreatedAt: testTime.AddDate(0, 0, 3),
	}}
	messages := mapMessages(records)

	history := discussionHistoryWithoutMessage(messages, "msg-source")

	if len(history) != 1 || history[0].MessageID != "msg-user" {
		t.Fatalf("history should exclude tool call/result records: %+v", history)
	}
	sendHistory := promptHistoryWithoutMessage(records, "msg-source")
	if len(sendHistory) != 1 || sendHistory[0].MessageID != "msg-user" {
		t.Fatalf("send history should exclude tool call/result records: %+v", sendHistory)
	}
	retryHistory := retryPromptHistoryBeforeMessage(records, "msg-source")
	if len(retryHistory) != 1 || retryHistory[0].MessageID != "msg-user" {
		t.Fatalf("retry history should exclude tool call/result records: %+v", retryHistory)
	}
}
