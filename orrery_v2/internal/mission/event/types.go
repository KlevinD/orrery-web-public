package event

type EventType string

const (
	EventMissionCreated     EventType = "MissionCreated"
	EventMissionGoalUpdated EventType = "MissionGoalUpdated"
	EventMissionPaused      EventType = "MissionPaused"
	EventMissionCompleted   EventType = "MissionCompleted"
	EventMissionCancelled   EventType = "MissionCancelled"

	EventStaffingRequested         EventType = "StaffingRequested"
	EventStaffingDecisionRecorded  EventType = "StaffingDecisionRecorded"
	EventAgentInvited              EventType = "AgentInvited"
	EventAgentJoined               EventType = "AgentJoined"
	EventAgentReleased             EventType = "AgentReleased"
	EventAgentReplaced             EventType = "AgentReplaced"
	EventMissionAssignmentCreated  EventType = "MissionAssignmentCreated"
	EventMissionAssignmentReleased EventType = "MissionAssignmentReleased"
	EventMissionAssignmentChanged  EventType = "MissionAssignmentChanged"
	EventCandidateRejected         EventType = "CandidateRejected"

	EventTaskContractCreated EventType = "TaskContractCreated"
	EventTaskAssigned        EventType = "TaskAssigned"
	EventTaskAccepted        EventType = "TaskAccepted"
	EventTaskStarted         EventType = "TaskStarted"
	EventTaskBlocked         EventType = "TaskBlocked"
	EventTaskSubmitted       EventType = "TaskSubmitted"
	EventReworkRequested     EventType = "ReworkRequested"
	EventReworkAssigned      EventType = "ReworkAssigned"
	EventReworkResolved      EventType = "ReworkResolved"
	EventTaskApproved        EventType = "TaskApproved"
	EventTaskRejected        EventType = "TaskRejected"
	EventTaskCancelled       EventType = "TaskCancelled"

	EventAgentResultSubmitted    EventType = "AgentResultSubmitted"
	EventSummarySubmitted        EventType = "SummarySubmitted"
	EventDeliverableSubmitted    EventType = "DeliverableSubmitted"
	EventEvidenceSubmitted       EventType = "EvidenceSubmitted"
	EventQAPlanCompleted         EventType = "QAPlanCompleted"
	EventQARequested             EventType = "QARequested"
	EventQAApproved              EventType = "QAApproved"
	EventQARejected              EventType = "QARejected"
	EventQASkipped               EventType = "QASkipped"
	EventAcceptanceReportCreated EventType = "AcceptanceReportCreated"

	EventDiscussionGroupCreated  EventType = "DiscussionGroupCreated"
	EventMessagePosted           EventType = "MessagePosted"
	EventAgentMentioned          EventType = "AgentMentioned"
	EventAgentReplied            EventType = "AgentReplied"
	EventInboxItemCreated        EventType = "InboxItemCreated"
	EventInboxItemClosed         EventType = "InboxItemClosed"
	EventDecisionRecorded        EventType = "DecisionRecorded"
	EventApprovalDecided         EventType = "ApprovalDecided"
	EventBlockerRaised           EventType = "BlockerRaised"
	EventClarificationRequested  EventType = "ClarificationRequested"
	EventMemoryCandidateCreated  EventType = "MemoryCandidateCreated"
	EventMemoryCandidateAccepted EventType = "MemoryCandidateAccepted"
	EventMemoryRecordCreated     EventType = "MemoryRecordCreated"
	EventMemoryRecordUpdated     EventType = "MemoryRecordUpdated"
	EventMemoryRecordDisabled    EventType = "MemoryRecordDisabled"
	EventMemoryDigestGenerated   EventType = "MemoryDigestGenerated"
	EventMemoryAccessDenied      EventType = "MemoryAccessDenied"

	EventAgentRunQueued    EventType = "AgentRunQueued"
	EventAgentRunStarted   EventType = "AgentRunStarted"
	EventAgentRunCompleted EventType = "AgentRunCompleted"
	EventAgentRunFailed    EventType = "AgentRunFailed"
	EventAgentRunTimedOut  EventType = "AgentRunTimedOut"
	EventAgentRunCancelled EventType = "AgentRunCancelled"
	EventAgentRunBlocked   EventType = "AgentRunBlocked"
)

func IsKnownEventType(t EventType) bool {
	_, ok := payloadFactories[t]
	return ok
}

type ActorKind string

const (
	ActorKindAgent  ActorKind = "agent"
	ActorKindHuman  ActorKind = "human"
	ActorKindSystem ActorKind = "system"
	ActorKindUser   ActorKind = "user"
)

func (k ActorKind) IsValid() bool {
	switch k {
	case ActorKindAgent, ActorKindHuman, ActorKindSystem, ActorKindUser:
		return true
	default:
		return false
	}
}

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

func (a ActorRef) IsZero() bool {
	return a.Kind == "" && a.ID == ""
}
