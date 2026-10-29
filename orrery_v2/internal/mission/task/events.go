package task

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type TaskEventType string

const (
	TaskEventTaskAccepted            TaskEventType = "TaskAccepted"
	TaskEventTaskStarted             TaskEventType = "TaskStarted"
	TaskEventTaskBlocked             TaskEventType = "TaskBlocked"
	TaskEventTaskSubmitted           TaskEventType = "TaskSubmitted"
	TaskEventTaskCancelled           TaskEventType = "TaskCancelled"
	TaskEventAgentResultSubmitted    TaskEventType = "AgentResultSubmitted"
	TaskEventDeliverableSubmitted    TaskEventType = "DeliverableSubmitted"
	TaskEventEvidenceSubmitted       TaskEventType = "EvidenceSubmitted"
	TaskEventQAPlanCompleted         TaskEventType = "QAPlanCompleted"
	TaskEventQARequested             TaskEventType = "QARequested"
	TaskEventQAApproved              TaskEventType = "QAApproved"
	TaskEventQARejected              TaskEventType = "QARejected"
	TaskEventQASkipped               TaskEventType = "QASkipped"
	TaskEventReworkRequested         TaskEventType = "ReworkRequested"
	TaskEventReworkAssigned          TaskEventType = "ReworkAssigned"
	TaskEventReworkResolved          TaskEventType = "ReworkResolved"
	TaskEventAcceptanceReportCreated TaskEventType = "AcceptanceReportCreated"
)

type TaskEvent struct {
	Type           TaskEventType
	TaskID         TaskID
	MissionID      string
	ActorID        agentcore.AgentID
	ActorProfileID agentcore.AgentProfileID
	Payload        any
	OccurredAt     time.Time
}
