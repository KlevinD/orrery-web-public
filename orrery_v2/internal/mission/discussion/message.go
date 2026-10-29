package discussion

import (
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/team"
)

type MessageID string
type ReplyThreadID string
type MessageKind string
type AuthorKind string

const (
	MessageKindSummary       MessageKind = "summary"
	MessageKindDeliverable   MessageKind = "deliverable"
	MessageKindEvidence      MessageKind = "evidence"
	MessageKindDecision      MessageKind = "decision"
	MessageKindBlocker       MessageKind = "blocker"
	MessageKindRequest       MessageKind = "request"
	MessageKindReply         MessageKind = "reply"
	MessageKindClarification MessageKind = "clarification"
	MessageKindSystemNotice  MessageKind = "system_notice"

	AuthorAgent  AuthorKind = "agent"
	AuthorUser   AuthorKind = "user"
	AuthorSystem AuthorKind = "system"
)

type AuthorRef struct {
	Kind     AuthorKind
	AgentID  agentcore.AgentID
	UserID   string
	SystemID string
}

func (a AuthorRef) StableID() agentcore.AgentID {
	switch a.Kind {
	case AuthorAgent:
		return a.AgentID
	case AuthorUser:
		return agentcore.AgentID(a.UserID)
	case AuthorSystem:
		return agentcore.AgentID(a.SystemID)
	default:
		return agentcore.AgentID(a.UserID)
	}
}

type MessageVisibility struct {
	Scope VisibilityScope
	Refs  []string
}

type Message struct {
	ID                     MessageID
	TenantID               string
	MissionID              missioncore.MissionID
	DiscussionGroupID      DiscussionGroupID
	ReplyToMessageID       MessageID
	ThreadID               ReplyThreadID
	Author                 AuthorRef
	AuthorMissionRole      team.MissionRole
	Content                string
	Summary                string
	MessageKind            MessageKind
	Mentions               []Mention
	RelatedTaskID          missioncore.TaskID
	RelatedEvidenceID      string
	GoalRef                string
	MemoryCandidateRef     string
	ContextScope           string
	Visibility             MessageVisibility
	SourceRefs             []string
	IdempotencyKey         string
	IdempotencyFingerprint string
	CreatedAt              time.Time
}

func ValidateMessage(msg Message) error {
	if msg.ID == "" || msg.DiscussionGroupID == "" {
		return fmt.Errorf("%w: message and discussion group ids are required", ErrInvalidArgument)
	}
	if msg.Author.Kind == "" {
		return fmt.Errorf("%w: author is required", ErrInvalidArgument)
	}
	if msg.Author.StableID() == "" {
		return fmt.Errorf("%w: author stable id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(msg.Content) == "" {
		return fmt.Errorf("%w: content is required", ErrInvalidArgument)
	}
	if !msg.MessageKind.IsValid() {
		return fmt.Errorf("%w: message_kind is invalid", ErrInvalidArgument)
	}
	if msg.Visibility.Scope == "" {
		return fmt.Errorf("%w: visibility is required", ErrInvalidArgument)
	}
	if containsForbiddenRawContext(msg.Content) {
		return fmt.Errorf("%w: message content must not contain full internal trace, memory, or runtime log", ErrInvalidArgument)
	}
	return nil
}

func (k MessageKind) IsValid() bool {
	switch k {
	case MessageKindSummary, MessageKindDeliverable, MessageKindEvidence, MessageKindDecision, MessageKindBlocker, MessageKindRequest, MessageKindReply, MessageKindClarification, MessageKindSystemNotice:
		return true
	default:
		return false
	}
}

func BuildReplyMessage(parent Message, reply Message) (Message, error) {
	if parent.ID == "" {
		return Message{}, fmt.Errorf("%w: parent message is required", ErrInvalidArgument)
	}
	reply.ReplyToMessageID = parent.ID
	reply.ThreadID = parent.ThreadID
	if reply.ThreadID == "" {
		reply.ThreadID = ReplyThreadID(parent.ID)
	}
	reply.MessageKind = MessageKindReply
	return reply, nil
}

func containsForbiddenRawContext(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range []string{"internal_trace:", "full runtime log", "full memory", "raw memory", "raw discussion messages"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
