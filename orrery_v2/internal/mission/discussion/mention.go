package discussion

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type MentionID string
type MentionTargetKind string

const (
	MentionTargetAgent MentionTargetKind = "agent"
	MentionTargetRole  MentionTargetKind = "mission_role"
	MentionTargetUser  MentionTargetKind = "user"
	MentionTargetTeam  MentionTargetKind = "team"
	MentionTargetAll   MentionTargetKind = "all"
)

type MentionTarget struct {
	Kind MentionTargetKind
	Ref  string
	Role string
}

type Mention struct {
	ID        MentionID
	MessageID MessageID
	Target    MentionTarget
	CreatedBy agentcore.AgentID
	CreatedAt time.Time
}

func (t MentionTarget) IsAll() bool {
	return t.Kind == MentionTargetAll
}

func (t MentionTarget) AgentID() agentcore.AgentID {
	if t.Kind != MentionTargetAgent {
		return ""
	}
	return agentcore.AgentID(t.Ref)
}
