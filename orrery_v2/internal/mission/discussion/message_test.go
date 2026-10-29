package discussion

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestMessageValidationRequiresCompressedContentAndKnownKind(t *testing.T) {
	msg := Message{
		ID:                "message-1",
		MissionID:         missioncore.MissionID("mission-1"),
		DiscussionGroupID: "discussion-1",
		Author:            AuthorRef{Kind: AuthorAgent, AgentID: agentcore.AgentID("agent-1")},
		Content:           "Please review the QA result.",
		MessageKind:       MessageKindRequest,
		Visibility:        MessageVisibility{Scope: VisibilityMission},
		CreatedAt:         time.Now().UTC(),
	}

	if err := ValidateMessage(msg); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	msg.Content = "internal_trace: full runtime log"
	if err := ValidateMessage(msg); err == nil {
		t.Fatalf("internal trace content should be rejected")
	}
}

func TestReplyThreadKeepsRootThreadID(t *testing.T) {
	root := Message{ID: "message-root", ThreadID: "thread-root"}
	reply, err := BuildReplyMessage(root, Message{
		ID:      "message-reply",
		Content: "Replying with context.",
	})

	if err != nil {
		t.Fatalf("BuildReplyMessage returned error: %v", err)
	}
	if reply.ReplyToMessageID != root.ID || reply.ThreadID != root.ThreadID || reply.MessageKind != MessageKindReply {
		t.Fatalf("reply did not preserve thread: %#v", reply)
	}
}
