package discussion

import (
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
)

type DiscussionDigestID string

type DiscussionDigest struct {
	ID                  DiscussionDigestID
	MissionID           missioncore.MissionID
	DiscussionGroupID   DiscussionGroupID
	MissionFinalGoal    string
	DiscussionGroupGoal string
	CurrentPhase        string
	ActiveDecisions     []DigestDecision
	ResponsibilityMap   []ResponsibilityEntry
	WorkerGoalRefs      []WorkerGoalRef
	OpenQuestions       []DigestQuestion
	Blockers            []DigestBlocker
	EvidenceIndex       []EvidenceIndexEntry
	PendingMentions     []PendingMentionDigest
	RecentSummary       string
	SourceMessageRefs   []string
	SourceEventRefs     []string
	Visibility          MessageVisibility
	CreatedAt           time.Time
	ExpiresAt           time.Time
}

type DigestDecision struct {
	MessageID  string
	Summary    string
	SourceRefs []string
}

type ResponsibilityEntry struct {
	AgentInstanceID      agentcore.AgentInstanceID
	MissionAssignmentRef string
	MissionRole          string
}

type WorkerGoalRef struct {
	AgentInstanceID      agentcore.AgentInstanceID
	MissionAssignmentRef string
	MissionRole          string
	GoalRef              string
}

type DigestQuestion struct {
	MessageID string
	Summary   string
}

type DigestBlocker struct {
	MessageID string
	TaskID    missioncore.TaskID
	Summary   string
}

type EvidenceIndexEntry struct {
	EvidenceID string
	MessageID  string
	Summary    string
}

type PendingMentionDigest struct {
	InboxItemID      InboxItemID
	MessageID        MessageID
	MentionID        MentionID
	Recipient        ViewerRef
	RequiredResponse bool
	CreatedAt        time.Time
}

func (d DiscussionDigest) Ref() agentcore.DiscussionDigestRef {
	return agentcore.DiscussionDigestRef{ID: agentcore.DiscussionDigestID(d.ID), SummaryRef: d.RecentSummary}
}

func MemoryCandidateFromMessage(msg Message) (memcore.MemoryCandidate, bool) {
	kind, ok := memoryKindForMessage(msg.MessageKind)
	if !ok {
		return memcore.MemoryCandidate{}, false
	}
	summary := sanitizedSummary(msg)
	if summary == "" {
		return memcore.MemoryCandidate{}, false
	}
	return memcore.MemoryCandidate{
		ID:                "candidate-" + string(msg.ID),
		MissionID:         msg.MissionID.String(),
		DiscussionGroupID: string(msg.DiscussionGroupID),
		SourceType:        memcore.SourceDiscussionMessage,
		SourceRef:         string(msg.ID),
		ProposedScope:     memcore.MemoryScope{Kind: memcore.ScopeDiscussion, ID: string(msg.DiscussionGroupID)},
		ProposedKind:      kind,
		Summary:           summary,
		EvidenceRefs:      append([]string(nil), msg.SourceRefs...),
		CreatedBy:         msg.Author.StableID(),
		Status:            memcore.CandidateProposed,
	}, true
}

func memoryKindForMessage(kind MessageKind) (agentcore.MemoryKind, bool) {
	switch kind {
	case MessageKindDecision:
		return agentcore.MemoryKindCollective, true
	case MessageKindBlocker:
		return agentcore.MemoryKindDiscussion, true
	case MessageKindEvidence:
		return agentcore.MemoryKindProject, true
	default:
		return "", false
	}
}

func sanitizedSummary(msg Message) string {
	text := strings.TrimSpace(firstNonEmpty(msg.Summary, msg.Content))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		if containsForbiddenRawContext(line) {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	return truncate(strings.Join(kept, " "), 280)
}

func truncate(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func buildRecentSummary(messages []Message, decisions []DigestDecision, blockers []DigestBlocker, mentions []PendingMentionDigest) string {
	var parts []string
	if len(decisions) > 0 {
		parts = append(parts, fmt.Sprintf("%d active decision(s)", len(decisions)))
	}
	if len(blockers) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocker(s)", len(blockers)))
	}
	if len(mentions) > 0 {
		parts = append(parts, fmt.Sprintf("%d pending mention(s)", len(mentions)))
	}
	for i := len(messages) - 1; i >= 0 && len(parts) < 5; i-- {
		summary := sanitizedSummary(messages[i])
		if summary != "" {
			parts = append(parts, summary)
		}
	}
	return truncate(strings.Join(parts, " | "), 500)
}
