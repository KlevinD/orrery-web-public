package capture

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	discussion "orrery_v2/internal/mission/discussion"
)

func BuildDiscussionMessageDraft(msg discussion.Message) CandidateDraft {
	evidenceRefs := make([]string, 0, 2+len(msg.SourceRefs))
	if strings.TrimSpace(msg.RelatedEvidenceID) != "" {
		evidenceRefs = append(evidenceRefs, "evidence:"+strings.TrimSpace(msg.RelatedEvidenceID))
	}
	for _, ref := range msg.SourceRefs {
		if strings.TrimSpace(ref) != "" {
			evidenceRefs = append(evidenceRefs, "source:"+strings.TrimSpace(ref))
		}
	}
	summary := strings.TrimSpace(msg.Summary)
	if summary == "" {
		summary = strings.TrimSpace(msg.Content)
	}
	return CandidateDraft{
		ID:                "candidate.discussion_message." + idSegment(string(msg.ID)),
		MissionID:         msg.MissionID.String(),
		DiscussionGroupID: string(msg.DiscussionGroupID),
		SourceType:        memcore.SourceDiscussionMessage,
		SourceRef:         string(msg.ID),
		ProposedScope:     memcore.MemoryScope{Kind: memcore.ScopeDiscussion, ID: string(msg.DiscussionGroupID)},
		ProposedKind:      agentcore.MemoryKindDiscussion,
		Layer:             memcore.MemoryLayerFact,
		Topic:             discussionTopic(msg.MessageKind),
		Summary:           summary,
		Narrative:         strings.TrimSpace(msg.Content),
		Tags:              []string{"discussion", string(msg.MessageKind)},
		EvidenceRefs:      evidenceRefs,
		LineageRefs:       []string{"discussion_message:" + string(msg.ID)},
		CreatedBy:         msg.Author.StableID(),
		CreatedAt:         msg.CreatedAt,
		UpdatedAt:         msg.CreatedAt,
	}
}

func discussionTopic(kind discussion.MessageKind) string {
	switch kind {
	case discussion.MessageKindDecision:
		return "decision"
	case discussion.MessageKindEvidence:
		return "evidence"
	case discussion.MessageKindDeliverable:
		return "deliverable"
	case discussion.MessageKindRequest:
		return "request"
	case discussion.MessageKindBlocker:
		return "blocker"
	default:
		return "discussion"
	}
}
