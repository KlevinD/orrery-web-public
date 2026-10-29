package missioncontrol

import (
	"context"
	"encoding/json"
	"testing"

	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/platform/execution"
)

func TestMentionExecutionDispatcherQueuesRequiredAgentMention(t *testing.T) {
	queue := &capturingMentionQueue{}
	dispatcher := NewMentionExecutionDispatcher(queue)
	item := missiondiscussion.InboxItem{
		ID:                "inbox-1",
		TenantID:          "tenant-a",
		MissionID:         missioncore.MissionID("mission-1"),
		DiscussionGroupID: "discussion-1",
		MessageID:         "message-1",
		Recipient:         missiondiscussion.ViewerRef{Kind: missiondiscussion.ViewerAgent, Ref: "agent-2"},
		RequiredResponse:  true,
		Status:            missiondiscussion.InboxPending,
	}

	if err := dispatcher.DispatchMention(context.Background(), item); err != nil {
		t.Fatalf("DispatchMention returned error: %v", err)
	}

	if len(queue.messages) != 1 {
		t.Fatalf("expected one queued message, got %#v", queue.messages)
	}
	message := queue.messages[0]
	if message.Topic != MentionDispatchTopic || message.Priority != 10 || message.MaxAttempts != 3 {
		t.Fatalf("unexpected queued message metadata: %#v", message)
	}
	var payload MentionDispatchPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.TenantID != "tenant-a" || payload.InboxItemID != "inbox-1" || payload.AgentID != "agent-2" || !payload.RequiredResponse {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestMentionExecutionDispatcherIgnoresNonRequiredOrNonAgentMentions(t *testing.T) {
	queue := &capturingMentionQueue{}
	dispatcher := NewMentionExecutionDispatcher(queue)

	for _, item := range []missiondiscussion.InboxItem{
		{Recipient: missiondiscussion.ViewerRef{Kind: missiondiscussion.ViewerAgent, Ref: "agent-2"}, RequiredResponse: false, Status: missiondiscussion.InboxPending},
		{Recipient: missiondiscussion.ViewerRef{Kind: missiondiscussion.ViewerUser, Ref: "user-1"}, RequiredResponse: true, Status: missiondiscussion.InboxPending},
		{Recipient: missiondiscussion.ViewerRef{Kind: missiondiscussion.ViewerAgent, Ref: "agent-2"}, RequiredResponse: true, Status: missiondiscussion.InboxResponded},
	} {
		if err := dispatcher.DispatchMention(context.Background(), item); err != nil {
			t.Fatalf("DispatchMention returned error: %v", err)
		}
	}

	if len(queue.messages) != 0 {
		t.Fatalf("expected no queued messages, got %#v", queue.messages)
	}
}

type capturingMentionQueue struct {
	messages []execution.Message
}

func (q *capturingMentionQueue) Submit(_ context.Context, message execution.Message) (string, error) {
	q.messages = append(q.messages, message)
	return "job-1", nil
}
