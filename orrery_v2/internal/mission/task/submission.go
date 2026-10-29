package task

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type TaskSubmissionID string
type DeliverableSubmissionID string
type EvidenceSubmissionID string

type TaskSubmission struct {
	ID                    TaskSubmissionID
	TaskID                TaskID
	AgentID               agentcore.AgentID
	RunID                 agentcore.AgentRunID
	AgentResultRef        agentcore.AgentResultID
	InternalTraceRef      string
	RuntimeAttemptID      string `json:",omitempty"`
	Summary               agentcore.Summary
	UserReport            string `json:",omitempty"`
	Blocker               *agentcore.Blocker
	DeliverableRefs       []agentcore.DeliverableID
	EvidenceRefs          []agentcore.EvidenceID
	DeliverableRecordRefs []DeliverableSubmissionID `json:",omitempty"`
	EvidenceRecordRefs    []EvidenceSubmissionID    `json:",omitempty"`
	SubmittedAt           time.Time
	IdempotencyKey        string
	TaskVersionAccepted   int64
}

type DeliverableSubmission struct {
	ID           DeliverableSubmissionID
	SubmissionID TaskSubmissionID
	TaskID       TaskID
	AgentID      agentcore.AgentID
	Deliverable  agentcore.Deliverable
	SubmittedAt  time.Time
}

type EvidenceSubmission struct {
	ID           EvidenceSubmissionID
	SubmissionID TaskSubmissionID
	TaskID       TaskID
	AgentID      agentcore.AgentID
	Evidence     agentcore.Evidence
	SubmittedAt  time.Time
}

func SubmissionIdempotencyKey(runID agentcore.AgentRunID, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if runID == "" {
		return ""
	}
	return "run:" + runID.String()
}
