package capture

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
)

func TestBuildDiscussionMessageDraftMapsDecisionMessageToFactCandidate(t *testing.T) {
	msg := discussion.Message{
		ID:                discussion.MessageID("message-1"),
		MissionID:         missioncore.MissionID("mission-1"),
		DiscussionGroupID: discussion.DiscussionGroupID("discussion-1"),
		Author:            discussion.AuthorRef{Kind: discussion.AuthorAgent, AgentID: agentcore.AgentID("agent-1")},
		Summary:           "Use deterministic capture.",
		MessageKind:       discussion.MessageKindDecision,
		RelatedTaskID:     missioncore.TaskID("task-1"),
		RelatedEvidenceID: "evidence-1",
		SourceRefs:        []string{"message-source-1"},
		CreatedAt:         time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}

	draft := BuildDiscussionMessageDraft(msg)

	if draft.ID != "candidate.discussion_message.message-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.SourceType != memcore.SourceDiscussionMessage || draft.Layer != memcore.MemoryLayerFact {
		t.Fatalf("source/layer = %s/%s", draft.SourceType, draft.Layer)
	}
	if draft.Summary != "Use deterministic capture." {
		t.Fatalf("Summary = %q", draft.Summary)
	}
	if len(draft.EvidenceRefs) != 2 {
		t.Fatalf("EvidenceRefs = %+v, want related evidence plus source ref", draft.EvidenceRefs)
	}
}
